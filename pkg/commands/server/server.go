package server

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/urfave/cli/v3"

	"github.com/ekristen/guppi/pkg/activity"
	"github.com/ekristen/guppi/pkg/auth"
	"github.com/ekristen/guppi/pkg/common"
	"github.com/ekristen/guppi/pkg/identity"
	"github.com/ekristen/guppi/pkg/namer"
	"github.com/ekristen/guppi/pkg/peer"
	"github.com/ekristen/guppi/pkg/portforward"
	"github.com/ekristen/guppi/pkg/preferences"
	"github.com/ekristen/guppi/pkg/recovery"
	"github.com/ekristen/guppi/pkg/server"
	"github.com/ekristen/guppi/pkg/sessionattrs"
	"github.com/ekristen/guppi/pkg/state"
	"github.com/ekristen/guppi/pkg/tmux"
	"github.com/ekristen/guppi/pkg/toolevents"
	"github.com/ekristen/guppi/pkg/webpush"
)

func Execute(ctx context.Context, c *cli.Command) error {
	client, err := tmux.NewClient()
	if err != nil {
		return err
	}

	stateMgr := state.NewManager(client)
	tracker := toolevents.NewTracker()
	actTracker := activity.NewTracker()

	interval := time.Duration(c.Int("discovery-interval")) * time.Second
	discovery := tmux.NewDiscovery(client, interval, func(sessions []*tmux.Session) {
		stateMgr.UpdateSessions(sessions)
		_ = recovery.TuneOomPanes(sessions)
	})
	go discovery.Run(ctx)

	reconciler := toolevents.NewReconciler(tracker, func(paneID string) toolevents.PaneState {
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
		panes, err := client.ListAllPanesDetailed()
		if err != nil {
			return nil
		}
		var infos []toolevents.PaneInfo
		for _, p := range panes {
			infos = append(infos, toolevents.PaneInfo{
				PaneID:  p.ID,
				Session: p.Session,
				Window:  p.Window,
				PID:     p.PID,
			})
		}
		return infos
	}, 5*time.Second)
	go detector.Run(ctx)

	silenceMonitor := toolevents.NewSilenceMonitor(tracker, detector, client)
	go silenceMonitor.Run(ctx)

	go runShellNameWatcher(ctx, client, stateMgr)

	var health *recovery.HealthPoller
	if !c.Bool("no-recovery") {
		snap := recovery.NewSnapshotter(stateMgr)
		go snap.Run(ctx)

		reb := recovery.NewRebuilder(client, stateMgr)
		health = recovery.NewHealthPoller(client, 3*time.Second, func() {
			logrus.Warn("tmux server gone, rebuilding from manifest")
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
		ctrlMode := tmux.NewControlMode(client, func(sessions []*tmux.Session) {
			stateMgr.UpdateSessions(sessions)
			_ = recovery.TuneOomPanes(sessions)
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

	attrsStore, err := sessionattrs.NewStore()
	if err != nil {
		logrus.WithError(err).Warn("failed to load session-attrs store, sync disabled")
		attrsStore = nil
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

	ptyRelay := peer.NewPTYRelay()
	ptyManager := peer.NewPTYManager(client.TmuxPath(), actTracker)

	deps := peer.SessionDeps{
		Manager:     peerMgr,
		LocalMgr:    stateMgr,
		Identity:    nodeIdentity,
		ActTracker:  actTracker,
		ToolTracker: tracker,
		PeerStore:   peerStore,
		TmuxClient:  client,
		PTYManager:  ptyManager,
		PTYRelay:    ptyRelay,
	}

	peerHandler := peer.NewHandler(deps)

	supervisor := peer.NewLinkSupervisor(deps)
	supervisor.Start(ctx)

	opts := &server.Options{
		Port:             int(c.Int("port")),
		SocketPath:       c.String("socket"),
		Client:           client,
		StateMgr:         stateMgr,
		Tracker:          tracker,
		ActivityTracker:  actTracker,
		PushKeys:         pushKeys,
		PushStore:        pushStore,
		PrefStore:        prefStore,
		AttrsStore:       attrsStore,
		AuthEnabled:      authEnabled,
		PasswordStore:    passwordStore,
		SessionMgr:       sessionMgr,
		Identity:         nodeIdentity,
		PeerStore:        peerStore,
		PeerMgr:          peerMgr,
		PeerHandler:      peerHandler,
		PTYRelay:         ptyRelay,
		LinkSupervisor:   supervisor,
		Detector:         detector,
		PortForwardStore: portforward.NewStore(),
		OnPrefsChanged:   applyNamerFromPrefs,
	}

	return server.Run(ctx, opts)
}

func init() {
	flags := []cli.Flag{
		&cli.IntFlag{
			Name:    "port",
			Aliases: []string{"p"},
			Usage:   "HTTP server port",
			Sources: cli.EnvVars("GUPPI_PORT"),
			Value:   7654,
		},
		&cli.IntFlag{
			Name:    "discovery-interval",
			Usage:   "Session discovery interval in seconds",
			Sources: cli.EnvVars("GUPPI_DISCOVERY_INTERVAL"),
			Value:   2,
		},
		&cli.BoolFlag{
			Name:    "no-control-mode",
			Usage:   "Disable tmux control mode (use polling only)",
			Sources: cli.EnvVars("GUPPI_NO_CONTROL_MODE"),
		},
		&cli.StringFlag{
			Name:    "socket",
			Usage:   "Unix socket path for local notify CLI (auto-detected if omitted)",
			Sources: cli.EnvVars("GUPPI_SOCKET"),
		},
		&cli.BoolFlag{
			Name:    "no-auth",
			Usage:   "Disable authentication (not recommended for remote access)",
			Sources: cli.EnvVars("GUPPI_NO_AUTH"),
		},
		&cli.BoolFlag{
			Name:    "no-recovery",
			Usage:   "Disable tmux crash recovery loops",
			Sources: cli.EnvVars("GUPPI_NO_RECOVERY"),
		},
	}

	cmd := &cli.Command{
		Name:        "server",
		Usage:       "start the guppi web server",
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

// runShellNameWatcher polls active-pane foreground commands and, when a
// non-agent session starts a new meaningful process, asks the AI namer to
// refresh that session's display name. The actual eligibility gate (namer
// enabled, no agent, not user-set) lives in TriggerShellNaming.
func runShellNameWatcher(ctx context.Context, client *tmux.Client, mgr *state.Manager) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	lastCmd := make(map[string]string)
	lastFire := make(map[string]time.Time)
	const minInterval = 30 * time.Second

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
				if cmd == "" || shellNames[cmd] || cmd == prev {
					continue
				}
				// New meaningful foreground process detected.
				if t, ok := lastFire[fg.Session]; ok && time.Since(t) < minInterval {
					continue
				}
				lastFire[fg.Session] = time.Now()

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
