package cc

import "context"

// CCOperator interface for use by stream module.
type CCOperator interface {
	Exit(ctx context.Context, tmuxTarget string) error
	Launch(ctx context.Context, tmuxTarget string, cmd string) error
	Interrupt(ctx context.Context, tmuxTarget string) error
	GetStatus(ctx context.Context, tmuxTarget string) (*StatusInfo, error)
}

// CCHistoryProvider interface for use by agent module.
type CCHistoryProvider interface {
	GetHistory(cwd string, ccSessionID string) ([]map[string]any, error)
}

// Registry keys for core.Registry (same keys as before).
const (
	HistoryKey  = "cc.history"
	OperatorKey = "cc.operator"
)
