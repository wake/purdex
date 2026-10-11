package conversation

import (
	"context"
	"time"

	"github.com/wake/purdex/internal/convfeed"
	"github.com/wake/purdex/internal/module/nex"
)

// ExecOwnerSource finds the live worker execution that runs a session: the nex module. Soft dependency: it is fetched
// from the Registry at call time (nex may not be loaded, or may load later), so Dependencies() does not name it.
type ExecOwnerSource interface {
	LiveWorker(ctx context.Context, sessionID string) (nex.ExecOwner, bool, error)
}

// execOwners returns the nex source, or nil when the module is not loaded.
func (m *Module) execOwners() ExecOwnerSource {
	if m.core == nil || m.core.Registry == nil {
		return nil
	}
	svc, ok := m.core.Registry.Get(nex.ConversationOwnerKey)
	if !ok {
		return nil
	}
	src, _ := svc.(ExecOwnerSource)
	return src
}

// execAdapter maps the nex answer to the resolver's execution owner.
type execAdapter struct{ m *Module }

func (a execAdapter) LiveExecution(ctx context.Context, sid string) (convfeed.Owner, bool, error) {
	src := a.m.execOwners()
	if src == nil {
		return convfeed.Owner{}, false, nil
	}
	o, ok, err := src.LiveWorker(ctx, sid)
	if err != nil || !ok || !o.Live {
		return convfeed.Owner{}, false, err
	}
	return convfeed.Owner{TranscriptPath: o.TranscriptPath, Status: o.Light, SeenAt: time.Now().UnixMilli(), Backend: "execution"}, true, nil
}

// cheapExecLight is the execution counterpart of cheapLight: the worker's light from nex's 2 s cache, for a source the
// last full lookup resolved to an execution. A source that is not an execution keeps what the resolver said.
func (m *Module) cheapExecLight(ctx context.Context, src convfeed.Source, have bool, sid string) (status string, ok bool) {
	if !have || !src.Live || src.Backend != "execution" {
		return "", false
	}
	x := m.execOwners()
	if x == nil {
		return "", false
	}
	o, found, err := x.LiveWorker(ctx, sid)
	if err != nil || !found || !o.Live || o.Light == "" {
		return "", false
	}
	return o.Light, true
}

// execOwned reports whether a live worker execution runs the session (errors count as no: capabilities fail closed).
func (m *Module) execOwned(sid string) bool {
	x := m.execOwners()
	if x == nil {
		return false
	}
	o, ok, err := x.LiveWorker(context.Background(), sid)
	return err == nil && ok && o.Live
}
