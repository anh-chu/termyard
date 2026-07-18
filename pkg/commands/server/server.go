package server

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/urfave/cli/v3"

	"github.com/anh-chu/termyard/pkg/activity"
	"github.com/anh-chu/termyard/pkg/auth"
	"github.com/anh-chu/termyard/pkg/common"
	"github.com/anh-chu/termyard/pkg/groupsync"
	"github.com/anh-chu/termyard/pkg/identity"
	"github.com/anh-chu/termyard/pkg/namer"
	"github.com/anh-chu/termyard/pkg/peer"
	"github.com/anh-chu/termyard/pkg/portforward"
	"github.com/anh-chu/termyard/pkg/preferences"
	"github.com/anh-chu/termyard/pkg/pty"
	"github.com/anh-chu/termyard/pkg/recovery"
	"github.com/anh-chu/termyard/pkg/scheduler"
	"github.com/anh-chu/termyard/pkg/server"
	"github.com/anh-chu/termyard/pkg/sessionattrs"
	"github.com/anh-chu/termyard/pkg/sessionorder"
	"github.com/anh-chu/termyard/pkg/state"
	"github.com/anh-chu/termyard/pkg/tmux"
	"github.com/anh-chu/termyard/pkg/toolevents"
	"github.com/anh-chu/termyard/pkg/webpush"
)

func Execute(ctx context.Context, c *cli.Command) error {
	client, err := tmux.NewClient()
	if err != nil {
		return err
	}

	stateMgr := state.NewManager(client)
	tracker := toolevents.NewTracker()
	tracker.EnablePersistence()
	actTracker := activity.NewTracker()

	// Session daemon registry — discover daemon-backed sessions alongside tmux.
	daemonReg := pty.NewRegistry(defaultSessionDir())
	stateMgr.SetDaemonRegistry(&daemonRegAdapter{reg: daemonReg})

	// mergedRefresh combines tmux sessions with daemon sessions before updating state.
	mergedRefresh := func(tmuxSessions []*tmux.Session) {
		daemonInfos := daemonReg.List()
		if len(daemonInfos) > 0 {
			logrus.WithField("count", len(daemonInfos)).Info("mergedRefresh: found daemon sessions")
		}
		for _, d := range daemonInfos {
			var created time.Time
			if t, err := time.Parse(time.RFC3339, d.Created); err == nil {
				created = t
			}
			tmuxSessions = append(tmuxSessions, &tmux.Session{
				Name:        d.ID,
				Created:     created,
				Backend:     "daemon",
				ProjectPath: d.Cwd,
				Windows: []*tmux.Window{{
					ID:     "daemon-" + d.ID,
					Name:   "shell",
					Active: true,
					Panes: []*tmux.Pane{{
						ID:          "daemon-" + d.ID + "-0",
						Active:      true,
						CurrentPath: d.Cwd,
					}},
				}},
			})
		}
		stateMgr.UpdateSessions(tmuxSessions)
		_ = recovery.TuneOomPanes(tmuxSessions)
	}

	interval := time.Duration(c.Int("discovery-interval")) * time.Second
	discovery := tmux.NewDiscovery(client, interval, func(sessions []*tmux.Session) {
		mergedRefresh(sessions)
	})
	go discovery.Run(ctx)

	reconciler := toolevents.NewReconciler(tracker, func(paneID string) toolevents.PaneState {
		// Check daemon sessions first.
		if idx := strings.Index(paneID, ":0.0"); idx > 0 {
			name := paneID[:idx]
			for _, d := range daemonReg.List() {
				if d.ID == name {
					pid := d.ShellPid
					if pid == 0 {
						pid = d.Pid
					}
					return toolevents.PaneState{Exists: true, CurrentCommand: d.Shell, PID: pid}
				}
			}
		}
		// Fall back to tmux.
		panes, err := client.ListAllPanes()
		if err != nil {
			return toolevents.PaneState{Exists: false}
		}
		for _, p := range panes {
			if p.ID == paneID {
				return toolevents.PaneState{Exists: true, CurrentCommand: p.CurrentCommand, PID: p.PID}
			}
		}
		return toolevents.PaneState{Exists: false}
	}, 3*time.Second)
	go reconciler.Run(ctx)

	detector := toolevents.NewDetector(tracker, func() []toolevents.PaneInfo {
		var infos []toolevents.PaneInfo
		// Tmux panes.
		if panes, err := client.ListAllPanesDetailed(); err == nil {
			for _, p := range panes {
				infos = append(infos, toolevents.PaneInfo{
					PaneID:  p.ID,
					Session: p.Session,
					Window:  p.Window,
					PID:     p.PID,
				})
			}
		}
		// Daemon session panes.
		for _, d := range daemonReg.List() {
			pid := d.ShellPid
			if pid == 0 {
				pid = d.Pid
			}
			infos = append(infos, toolevents.PaneInfo{
				PaneID:  d.ID + ":0.0",
				Session: d.ID,
				Window:  0,
				PID:     pid,
			})
		}
		return infos
	}, 5*time.Second)
	go detector.Run(ctx)

	silenceMonitor := toolevents.NewSilenceMonitor(tracker, detector, &compositeCaptureClient{tmux: client, daemon: daemonReg})
	go silenceMonitor.Run(ctx)

	go runShellNameWatcher(ctx, client, stateMgr)

	attrsStore, err := sessionattrs.NewStore()
	if err != nil {
		logrus.WithError(err).Warn("failed to load session-attrs store, sync disabled")
		attrsStore = nil
	}

	orderStore, err := sessionorder.NewStore()
	if err != nil {
		logrus.WithError(err).Warn("failed to load session-order store, sync disabled")
		orderStore = nil
	}

	groupStore, err := groupsync.NewStore()
	if err != nil {
		logrus.WithError(err).Warn("failed to load groups store, sync disabled")
		groupStore = nil
	}

	var health *recovery.HealthPoller
	if !c.Bool("no-recovery") {
		snap := recovery.NewSnapshotter(stateMgr, attrsStore)
		go snap.Run(ctx)

		reb := recovery.NewRebuilder(client, stateMgr, attrsStore)
		health = recovery.NewHealthPoller(client, 3*time.Second, func() {
			logrus.Warn("tmux server gone, rebuilding from manifest")
			stateMgr.SetRecovering(true)
			defer stateMgr.SetRecovering(false)
			if err := reb.Rebuild(ctx); err != nil {
				logrus.WithError(err).Error("rebuild failed")
			}
			if sessions, err := client.ListSessions(); err == nil {
				stateMgr.UpdateSessions(sessions)
				_ = recovery.TuneOomPanes(sessions)
			}
			discovery.SetInterval(interval)
		})
		go health.Run(ctx)
	}

	if !c.Bool("no-control-mode") {
		fallbackInterval := 30 * time.Second
		ctrlMode := tmux.NewControlMode(client, func(tmuxSessions []*tmux.Session) {
			mergedRefresh(tmuxSessions)
		},
			tmux.WithOnConnect(func() {
				discovery.SetInterval(fallbackInterval)
			}),
			tmux.WithOnDisconnect(func() {
				discovery.SetInterval(interval)
				if health != nil {
					health.Hint()
				}
			}),
			tmux.WithOnOutput(func(paneID string, dataLen int) {
				session := stateMgr.SessionForPane(paneID)
				if session != "" {
					actTracker.Record(session, dataLen)
				}
				silenceMonitor.RecordOutput(paneID)
				tracker.RecordProgress(paneID)
			}),
		)
		go ctrlMode.Run(ctx)
	}

	go tracker.RunInactivityPromoter(ctx, toolevents.DefaultInactivityTimeout)

	// Stuck monitor: flag agents that claim "active" but show no progress
	// (no tool events, no terminal output) and aren't at an input prompt.
	checkPrompt := func(paneID string) (bool, bool) {
		content, err := client.CapturePaneContent(paneID)
		if err != nil {
			return false, false
		}
		return toolevents.DetectPrompt(content).IsPrompt, true
	}
	go tracker.RunStuckMonitor(ctx, toolevents.DefaultStuckTimeout, checkPrompt)

	prefStore, err := preferences.NewStore()
	if err != nil {
		logrus.WithError(err).Warn("failed to load preferences, using defaults")
		prefStore = nil
	}

	// applyNamerFromPrefs (re)builds the AI session namer from preferences,
	// falling back to env vars. Called at startup and whenever preferences are
	// updated via the API.
	applyNamerFromPrefs := func(p *preferences.Preferences) {
		cfg := namer.Configure(p.AINaming.Enabled, p.AINaming.Endpoint, p.AINaming.APIKey, p.AINaming.Model)
		n := namer.New(cfg)
		stateMgr.SetNamer(n)
		if n.Enabled() {
			logrus.Info("AI session namer enabled")
		} else {
			logrus.Debug("AI session namer disabled")
		}
	}
	if prefStore != nil {
		applyNamerFromPrefs(prefStore.Get())
	}

	schedulerStore, err := scheduler.NewStore()
	if err != nil {
		logrus.WithError(err).Warn("failed to load scheduler store, schedules disabled")
		schedulerStore = nil
	}

	var pushKeys *webpush.VAPIDKeys
	var pushStore *webpush.Store
	vapidKeys, err := webpush.LoadOrCreateKeys()
	if err != nil {
		logrus.WithError(err).Warn("failed to load VAPID keys, push notifications will be unavailable")
	} else {
		pushKeys = vapidKeys
		pushStore = webpush.NewStore()
		pushSender := webpush.NewSender(pushKeys, pushStore, tracker)
		go pushSender.Run(ctx)
	}

	var (
		authEnabled   bool
		passwordStore *auth.PasswordStore
		sessionMgr    *auth.SessionManager
	)
	if !c.Bool("no-auth") {
		passwordStore, err = auth.NewPasswordStore()
		if err != nil {
			return fmt.Errorf("failed to initialize auth: %w", err)
		}
		sessionMgr = auth.NewSessionManager(24 * time.Hour)
		authEnabled = true

		if !passwordStore.HasPassword() {
			logrus.Info("no password set — open the dashboard in your browser to complete setup")
		}
	}

	hostname, _ := os.Hostname()
	nodeIdentity, err := identity.LoadOrCreate(hostname)
	if err != nil {
		return fmt.Errorf("failed to load identity: %w", err)
	}
	logrus.WithField("name", nodeIdentity.Name).WithField("fingerprint", nodeIdentity.Fingerprint()).Info("node identity loaded")

	peerStore, err := identity.NewPeerStore()
	if err != nil {
		return fmt.Errorf("failed to load peer store: %w", err)
	}

	peerMgr := peer.NewManager(nodeIdentity, peerStore, stateMgr)
	go peerMgr.Run()

	detector.SetHost(peerMgr.LocalID(), peerMgr.LocalName())
	silenceMonitor.SetHost(peerMgr.LocalID(), peerMgr.LocalName())
	reconciler.SetHost(peerMgr.LocalID(), peerMgr.LocalName())

	streamReg := peer.NewStreamRegistry()
	captureReg := peer.NewCaptureRegistry()
	fileReadReg := peer.NewFileReadRegistry()

	deps := peer.SessionDeps{
		Manager:     peerMgr,
		LocalMgr:    stateMgr,
		Identity:    nodeIdentity,
		ActTracker:  actTracker,
		ToolTracker: tracker,
		PeerStore:   peerStore,
		TmuxClient:  client,
		StreamReg:   streamReg,
		CaptureReg:  captureReg,
		FileReadReg: fileReadReg,
	}

	peerHandler := peer.NewHandler(deps, streamReg)

	supervisor := peer.NewLinkSupervisor(deps)
	supervisor.Start(ctx)

	opts := &server.Options{
		Port:             int(c.Int("port")),
		SocketPath:       c.String("socket"),
		TLSCert:          c.String("tls-cert"),
		TLSKey:           c.String("tls-key"),
		TLSAuto:          c.Bool("tls"),
		Client:           client,
		StateMgr:         stateMgr,
		Tracker:          tracker,
		ActivityTracker:  actTracker,
		PushKeys:         pushKeys,
		PushStore:        pushStore,
		PrefStore:        prefStore,
		AttrsStore:       attrsStore,
		OrderStore:       orderStore,
		GroupStore:       groupStore,
		AuthEnabled:      authEnabled,
		PasswordStore:    passwordStore,
		SessionMgr:       sessionMgr,
		Identity:         nodeIdentity,
		PeerStore:        peerStore,
		PeerMgr:          peerMgr,
		PeerHandler:      peerHandler,
		StreamReg:        streamReg,
		CaptureReg:       captureReg,
		FileReadReg:      fileReadReg,
		LinkSupervisor:   supervisor,
		Detector:         detector,
		PortForwardStore: portforward.NewStore(),
		SchedulerStore:   schedulerStore,
		DaemonReg:        daemonReg,
		RefreshSessions: func() {
			fresh, err := client.ListSessions()
			if err != nil {
				logrus.WithError(err).Debug("tmux list-sessions failed during refresh")
				fresh = nil // still merge daemon sessions
			}
			mergedRefresh(fresh)
		},
		OnPrefsChanged:   applyNamerFromPrefs,
	}
	if schedulerStore != nil {
		runner := scheduler.NewRunner(schedulerStore, client, stateMgr, peerMgr, func(req scheduler.CreateSessionReq) error {
			// Remote sessions still go through peer/tmux path.
			if req.Host != "" && peerMgr != nil && !peerMgr.IsLocal(req.Host) {
				return server.CreateSession(opts, req)
			}
			// Local sessions use daemon backend.
			shell := req.Command
			if shell == "" || shell == "shell" {
				shell = ""
			}
			cwd := req.Path
			if cwd == "~" {
				cwd = ""
			}
			if err := daemonReg.Create(req.Name, shell, cwd, 120, 40); err != nil {
				return err
			}
			if req.AgentType != "" {
				stateMgr.SetSessionAgentType(req.Name, req.AgentType)
			}
			fresh, _ := client.ListSessions()
			mergedRefresh(fresh)
			return nil
		}, logrus.WithField("component", "scheduler"))
		runner.SetCapEnforcer(func(job scheduler.Job) {
			// Pre-spawn: leave room for the incoming run.
			server.EnforceScheduleCap(opts, job.ID, job.MaxConcurrency-1)
		})
		opts.SchedulerRunner = runner
		go func() {
			for opts.Hub == nil {
				select {
				case <-ctx.Done():
					return
				case <-time.After(10 * time.Millisecond):
				}
			}
			runner.Run(ctx)
		}()
	}

	return server.Run(ctx, opts)
}

func init() {
	flags := []cli.Flag{
		&cli.IntFlag{
			Name:    "port",
			Aliases: []string{"p"},
			Usage:   "HTTP server port",
			Sources: cli.EnvVars("TERMYARD_PORT"),
			Value:   7654,
		},
		&cli.IntFlag{
			Name:    "discovery-interval",
			Usage:   "Session discovery interval in seconds",
			Sources: cli.EnvVars("TERMYARD_DISCOVERY_INTERVAL"),
			Value:   2,
		},
		&cli.BoolFlag{
			Name:    "no-control-mode",
			Usage:   "Disable tmux control mode (use polling only)",
			Sources: cli.EnvVars("TERMYARD_NO_CONTROL_MODE"),
		},
		&cli.StringFlag{
			Name:    "socket",
			Usage:   "Unix socket path for local notify CLI (auto-detected if omitted)",
			Sources: cli.EnvVars("TERMYARD_SOCKET"),
		},
		&cli.BoolFlag{
			Name:    "no-auth",
			Usage:   "Disable authentication (not recommended for remote access)",
			Sources: cli.EnvVars("TERMYARD_NO_AUTH"),
		},
		&cli.BoolFlag{
			Name:    "no-recovery",
			Usage:   "Disable tmux crash recovery loops",
			Sources: cli.EnvVars("TERMYARD_NO_RECOVERY"),
		},
		&cli.BoolFlag{
			Name:    "tls",
			Usage:   "Serve HTTPS with a self-signed cert (enables secure-context browser features over LAN)",
			Sources: cli.EnvVars("TERMYARD_TLS"),
		},
		&cli.StringFlag{
			Name:    "tls-cert",
			Usage:   "Path to a TLS certificate file (PEM); pair with --tls-key for a real cert",
			Sources: cli.EnvVars("TERMYARD_TLS_CERT"),
		},
		&cli.StringFlag{
			Name:    "tls-key",
			Usage:   "Path to a TLS private key file (PEM); pair with --tls-cert",
			Sources: cli.EnvVars("TERMYARD_TLS_KEY"),
		},
	}

	cmd := &cli.Command{
		Name:        "server",
		Usage:       "start the termyard web server",
		Description: "starts the web dashboard for monitoring and interacting with tmux sessions",
		Flags:       flags,
		Action:      Execute,
		Before: func(ctx context.Context, c *cli.Command) (context.Context, error) {
			logrus.Info("checking for tmux...")
			_, err := tmux.NewClient()
			if err != nil {
				return ctx, err
			}
			logrus.Info("tmux found")
			return ctx, nil
		},
	}

	common.RegisterCommand(cmd)
}

// shellNames lists foreground commands treated as "no meaningful process".
var shellNames = map[string]bool{
	"bash": true, "zsh": true, "sh": true, "fish": true, "tmux": true,
	"-bash": true, "-zsh": true, "-sh": true, "login": true,
}

// trivialCmds are short-lived navigation/inspection commands that should never
// drive a session rename on their own — they say nothing durable about the
// session's purpose.
var trivialCmds = map[string]bool{
	"ls": true, "cd": true, "pwd": true, "cat": true, "less": true,
	"more": true, "man": true, "clear": true, "echo": true, "which": true,
	"sleep": true, "watch": true, "top": true, "htop": true, "ps": true,
	"history": true, "env": true, "export": true, "head": true, "tail": true,
	"touch": true, "mkdir": true, "rm": true, "cp": true, "mv": true,
}

// runShellNameWatcher polls active-pane foreground commands and, when a
// non-agent session starts a new meaningful process, asks the AI namer to
// refresh that session's display name. The actual eligibility gate (namer
// enabled, no agent, not user-set) lives in TriggerShellNaming.
func runShellNameWatcher(ctx context.Context, client *tmux.Client, mgr *state.Manager) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	lastCmd := make(map[string]string)
	lastFire := make(map[string]time.Time)
	named := make(map[string]bool)
	// First name is assigned quickly; after a session already has a name we back
	// off hard so a meaningful label is not churned by later transient commands.
	const firstInterval = 20 * time.Second
	const renameInterval = 3 * time.Minute

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			fgs, err := client.ListForegroundCommands()
			if err != nil {
				continue
			}
			for _, fg := range fgs {
				prev := lastCmd[fg.Session]
				lastCmd[fg.Session] = fg.Command
				cmd := strings.TrimSpace(fg.Command)
				if cmd == "" || shellNames[cmd] || trivialCmds[cmd] || cmd == prev {
					continue
				}
				// New meaningful foreground process detected. Use a long cooldown
				// once the session already has a name so we don't churn it.
				interval := firstInterval
				if named[fg.Session] {
					interval = renameInterval
				}
				if t, ok := lastFire[fg.Session]; ok && time.Since(t) < interval {
					continue
				}
				lastFire[fg.Session] = time.Now()
				named[fg.Session] = true

				cmds := []string{cmd}
				if pane := tmux.PrimaryPane(currentWindows(client, fg.Session)); pane != nil {
					if content, err := client.CapturePaneHistory(pane.ID, -100); err == nil {
						cmds = recentCommands(content, cmd)
					}
				}
				go mgr.TriggerShellNaming(fg.Session, cmds)
			}
		}
	}
}

func currentWindows(client *tmux.Client, session string) []*tmux.Window {
	wins, err := client.ListWindows(session)
	if err != nil {
		return nil
	}
	for _, w := range wins {
		if panes, err := client.ListPanes(w.ID); err == nil {
			w.Panes = panes
		}
	}
	return wins
}

// recentCommands extracts up to a handful of recent input lines from captured
// pane content as a hint for naming. Falls back to [foreground] if nothing
// useful is found.
func defaultSessionDir() string {
	uid := fmt.Sprintf("%d", os.Getuid())
	return fmt.Sprintf("/tmp/termyard-sessions-%s", uid)
}

func recentCommands(content, foreground string) []string {
	lines := strings.Split(content, "\n")
	var out []string
	for i := len(lines) - 1; i >= 0 && len(out) < 6; i-- {
		l := strings.TrimSpace(lines[i])
		if l == "" {
			continue
		}
		// Strip common prompt prefixes ($, #, %, >).
		l = strings.TrimLeft(l, "$#%> ")
		if l == "" {
			continue
		}
		out = append(out, l)
	}
	if len(out) == 0 {
		return []string{foreground}
	}
	// reverse to chronological order
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out
}

// compositeCaptureClient tries daemon capture first (for daemon-style pane IDs
// like "sessionName:0.0"), then falls back to tmux.
type compositeCaptureClient struct {
	tmux   *tmux.Client
	daemon *pty.Registry
}

func (c *compositeCaptureClient) CapturePaneContent(paneID string) (string, error) {
	// Daemon pane IDs are "sessionName:0.0". Extract the session name.
	if idx := strings.Index(paneID, ":0.0"); idx > 0 {
		name := paneID[:idx]
		if text, err := c.daemon.Capture(name); err == nil {
			return text, nil
		}
	}
	return c.tmux.CapturePaneContent(paneID)
}

// daemonRegAdapter wraps pty.Registry to satisfy state.DaemonRegistry.
type daemonRegAdapter struct {
	reg *pty.Registry
}

func (a *daemonRegAdapter) List() []state.DaemonSessionInfo {
	infos := a.reg.List()
	out := make([]state.DaemonSessionInfo, len(infos))
	for i, info := range infos {
		out[i] = state.DaemonSessionInfo{
			ID:       info.ID,
			Pid:      info.Pid,
			ShellPid: info.ShellPid,
			Shell:    info.Shell,
			Cwd:      info.Cwd,
			Created:  info.Created,
		}
	}
	return out
}

func (a *daemonRegAdapter) Capture(name string) (string, error) {
	return a.reg.Capture(name)
}
