package cc

import (
	"context"
	"net/http"

	"github.com/wake/tmux-box/internal/core"
	"github.com/wake/tmux-box/internal/detect"
	"github.com/wake/tmux-box/internal/module/session"
)

// NOTE: The CC status detection poller was removed in favour of the agent hook
// system (internal/module/agent). Status is now pushed by tbox hook commands
// rather than polled. The cc module still provides detector, operator, and
// history services for use by other modules.

// CCModule groups Claude Code-related functionality: detection, history, and operations.
type CCModule struct {
	core     *core.Core
	detector *detect.Detector
	sessions session.SessionProvider
}

// New creates a new CCModule.
func New() *CCModule {
	return &CCModule{}
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

	// Listen for config changes to update detector commands
	c.OnConfigChange(func() {
		c.CfgMu.RLock()
		cmds := c.Cfg.Detect.CCCommands
		c.CfgMu.RUnlock()
		m.detector.UpdateCommands(cmds)
	})

	return nil
}

func (m *CCModule) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/sessions/{code}/history", m.handleHistory)
}

func (m *CCModule) Start(_ context.Context) error {
	return nil
}

func (m *CCModule) Stop(_ context.Context) error {
	return nil
}
