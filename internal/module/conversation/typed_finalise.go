package conversation

import (
	"context"
	"time"

	"github.com/wake/purdex/internal/promptq"
)

// spawnFinaliser is what a caller that went away leaves behind. One finaliser per id (a second spawn is a no-op), it stops
// when the module stops, and it takes the same locks as the request before it cleans up.
func (m *Module) spawnFinaliser(c *typedCtx, tune typedTuning) {
	m.finMu.Lock()
	if _, running := m.finalising[c.id]; running {
		m.finMu.Unlock()
		return
	}
	if m.finalising == nil {
		m.finalising = map[string]struct{}{}
	}
	m.finalising[c.id] = struct{}{}
	m.finMu.Unlock()
	ctx, wg, ok := m.admitWS() // the module's lifetime: refused once Stop has begun, waited for by Stop
	if !ok {
		m.finDone(c.id)
		return
	}
	go func() {
		defer wg.Done()
		defer m.finDone(c.id)
		m.finalise(ctx, c, tune)
	}()
}

func (m *Module) finDone(id string) {
	m.finMu.Lock()
	delete(m.finalising, id)
	m.finMu.Unlock()
}

// finalise waits (bounded) for the fill to settle and, if our text landed in the composer and no Enter will ever be sent for
// it, cleans it up under the pane and session locks.
func (m *Module) finalise(ctx context.Context, c *typedCtx, tune typedTuning) {
	td := c.td
	deadline := time.NewTimer(tune.finaliseFor)
	defer deadline.Stop()
	tick := time.NewTicker(tune.poll)
	defer tick.Stop()
settle:
	for {
		r, ok := td.q.Lookup(c.id)
		if !ok {
			return
		}
		if r.Status != "" {
			break
		}
		select {
		case <-ctx.Done():
			return
		case <-deadline.C:
			break settle
		case <-tick.C:
		}
	}
	release, ok := m.lockPaneAndSession(ctx, c.owner.TmuxPaneID, c.owner.SessionID, tune.finaliseFor)
	if !ok {
		return
	}
	defer release()
	if r, ok := td.q.Lookup(c.id); ok && (r.Phase == promptq.PhaseFilled || r.Phase == promptq.PhaseUnknown) {
		m.cleanup(td, c.id)
	}
}
