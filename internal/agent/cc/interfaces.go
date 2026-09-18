package cc

import "context"

// CCOperator interface for use by the nex module.
type CCOperator interface {
	Exit(ctx context.Context, tmuxTarget string) error
	Launch(ctx context.Context, tmuxTarget string, cmd string) error
	Interrupt(ctx context.Context, tmuxTarget string) error
	GetStatus(ctx context.Context, tmuxTarget string) (*StatusInfo, error)
}

// OperatorKey is the core.Registry key under which the CC provider publishes
// its CCOperator (same key as before).
const OperatorKey = "cc.operator"
