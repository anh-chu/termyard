package server

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/urfave/cli/v3"

	"github.com/ekristen/guppi/pkg/activity"
	"github.com/ekristen/guppi/pkg/auth"
	"github.com/ekristen/guppi/pkg/common"
	"github.com/ekristen/guppi/pkg/identity"
	"github.com/ekristen/guppi/pkg/layout"
	"github.com/ekristen/guppi/pkg/peer"
	"github.com/ekristen/guppi/pkg/portforward"
	"github.com/ekristen/guppi/pkg/preferences"
	"github.com/ekristen/guppi/pkg/server"
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

	if !c.Bool("no-control-mode") {
		fallbackInterval := 30 * time.Second
		ctrlMode := tmux.NewControlMode(client, func(sessions []*tmux.Session) {
			stateMgr.UpdateSessions(sessions)
		},
			tmux.WithOnConnect(func() {
				discovery.SetInterval(fallbackInterval)
			}),
			tmux.WithOnDisconnect(func() {
				discovery.SetInterval(interval)
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

	layoutStore, err := layout.NewStore()
	if err != nil {
		logrus.WithError(err).Warn("failed to load layout store, sync disabled")
		layoutStore = nil
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
		LayoutStore:      layoutStore,
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
