package nex

import (
	"context"

	"lab.protype.tw/wake/nexen/execution"
	"lab.protype.tw/wake/nexen/store"
)

// nexService is the slice of *execution.Service the handoff endpoints call
// (spec §4.4). Nexen is embedded, so delegating and taking back are Go
// calls, not HTTP hops through the engine's own API — but the module keeps
// to these five methods behind an interface so tests substitute a fake and
// the import-boundary test can see exactly what is reached for. The
// signatures are Nexen's verbatim; the assertions below break the build
// if a Nexen bump changes them.
type nexService interface {
	Delegate(ctx context.Context, req execution.Request) (execution.Result, error)
	AcquireLease(ctx context.Context, executionID, principalID string) (store.Lease, error)
	ReleaseLease(ctx context.Context, executionID, leaseID, principalID string) error
	Interrupt(ctx context.Context, req execution.InterruptRequest) (execution.InterruptResult, error)
	Archive(ctx context.Context, req execution.ArchiveRequest) error
}

// nexStore reads one execution row. Nexen has no Service.Get; the row
// (state, session_id, resume_session_id, lease) comes from the store.
type nexStore interface {
	Get(ctx context.Context, id string) (store.Execution, error)
}

var (
	_ nexService = (*execution.Service)(nil)
	_ nexStore   = (*store.Store)(nil)
)
