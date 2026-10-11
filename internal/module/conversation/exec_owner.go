package conversation

import (
	"context"
	"time"

	"github.com/wake/purdex/internal/convfeed"
)

// ExecOwnerSource finds the live worker execution that runs a session: the nex module. Soft dependency: it is fetched
// from the Registry at call time (nex may not be loaded, or may load later), so Dependencies() does not name it.
type ExecOwnerSource interface {
	LiveWorker(ctx context.Context, sessionID string) (convfeed.ExecOwner, bool, error)
}

// lookupExec is the one place that asks the execution source: found is true only for a live owner (an owner with Live=false is treated as not found, so the follow loop re-resolves); err means "could
// not tell" (the source answered with an error). With no source loaded the answer is "none".
func (m *Module) lookupExec(ctx context.Context, sid string) (o convfeed.ExecOwner, found bool, err error) {
	if m.core == nil || m.core.Registry == nil {
		return convfeed.ExecOwner{}, false, nil
	}
	svc, ok := m.core.Registry.Get(convfeed.ExecOwnerKey)
	if !ok {
		return convfeed.ExecOwner{}, false, nil
	}
	src, ok := svc.(ExecOwnerSource)
	if !ok {
		return convfeed.ExecOwner{}, false, nil
	}
	o, ok, err = src.LiveWorker(ctx, sid)
	if err != nil {
		return convfeed.ExecOwner{}, false, err
	}
	if !ok || !o.Live {
		return convfeed.ExecOwner{}, false, nil
	}
	return o, true, nil
}

// execAdapter maps the execution source to the resolver's execution lookup.
type execAdapter struct{ m *Module }

func (a execAdapter) LiveExecution(ctx context.Context, sid string) (convfeed.Owner, bool, error) {
	o, found, err := a.m.lookupExec(ctx, sid)
	if err != nil || !found {
		return convfeed.Owner{}, false, err
	}
	return convfeed.Owner{TranscriptPath: o.TranscriptPath, Status: o.Light, SeenAt: time.Now().UnixMilli(), Backend: "execution"}, true, nil
}

// execLightState is what the per-tick execution light found.
type execLightState int

const (
	execLightSkip  execLightState = iota // not an execution source, or the lookup failed: keep what the source says
	execLightFound                       // the owner is still live: light and the time it was read
	execLightGone                        // the source answered "no live worker": the follower must re-resolve at once
)

// cheapExecLight is the execution counterpart of cheapLight: the worker's light (read fresh by nex, its owner identity
// cached for 2 s), for a source the last full lookup resolved to an execution.
func (m *Module) cheapExecLight(ctx context.Context, src convfeed.Source, have bool, sid string) (light string, at time.Time, st execLightState) {
	if !have || !src.Live || src.Backend != "execution" {
		return "", time.Time{}, execLightSkip
	}
	o, found, err := m.lookupExec(ctx, sid)
	switch {
	case err != nil:
		return "", time.Time{}, execLightSkip
	case !found:
		return "", time.Time{}, execLightGone
	}
	light = o.Light
	if light == "" {
		light = "unknown"
	}
	at = o.At
	if at.IsZero() {
		at = time.Now()
	}
	return light, at, execLightFound
}
