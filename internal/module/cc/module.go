package cc

import (
	"context"
	"net/http"

	"github.com/wake/tmux-box/internal/core"
	"github.com/wake/tmux-box/internal/detect"
	"github.com/wake/tmux-box/internal/module/session"
)

// CCModule groups Claude Code-related functionality: detection, history, and operations.
type CCModule struct {
	core          *core.Core
	detector      *detect.Detector
	sessions      session.SessionProvider
	resetPollerCh chan struct{} // signals poller to rebuild ticker with new interval
}

// New creates a new CCModule.
func New() *CCModule {
	return &CCModule{
		resetPollerCh: make(chan struct{}, 1),
	}
}

func (m *CCModule) Name() string          { return "cc" }
func (m *CCModule) Dependencies() []string { return []string{"session"} }

func (m *CCModule) Init(c *core.Core) error {
	m.core = c
	m.detector = detect.New(c.Tmux, c.Cfg.Detect.CCCommands)
	m.sessions = c.Registry.MustGet(session.RegistryKey).(session.SessionProvider)

	// Register CCDetector
	c.Registry.Register(DetectorKey, CCDetector(m))

	// Register CCHistoryProvider
	c.Registry.Register(HistoryKey, CCHistoryProvider(m))

	// Register CCOperator
	c.Registry.Register(OperatorKey, CCOperator(m))

	// Listen for config changes to update detector commands and poller interval
	c.OnConfigChange(func() {
		c.CfgMu.RLock()
		cmds := c.Cfg.Detect.CCCommands
		c.CfgMu.RUnlock()
		m.detector.UpdateCommands(cmds)
		// Signal poller to rebuild ticker with new interval
		select {
		case m.resetPollerCh <- struct{}{}:
		default: // already signalled
		}
	})

	return nil
}

func (m *CCModule) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/sessions/{code}/history", m.handleHistory)
}

func (m *CCModule) Start(ctx context.Context) error {
	m.startPoller(ctx)
	m.core.Events.OnSubscribe(m.sendStatusSnapshot)
	return nil
}

func (m *CCModule) Stop(_ context.Context) error {
	return nil
}
