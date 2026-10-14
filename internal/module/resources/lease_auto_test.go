package resourcesmod

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wake/purdex/internal/core"
	"github.com/wake/purdex/internal/modevents"
	"github.com/wake/purdex/internal/resources"
)

// Without a seam the answer comes from the mod event registry: a live stream of
// the session that announced the prompt capability. A mod that announced
// nothing (an older one, `claude -p`) is not interactive. (M: read any stream.)
func TestSessionInteractive_ReadsTheModEventRegistry(t *testing.T) {
	reg := modevents.NewRegistry(time.Now)
	svcs := core.NewServiceRegistry()
	svcs.Register("modevents", reg)
	m := &Module{core: &core.Core{Registry: svcs}}
	assert.False(t, m.sessionInteractive("sid-a"), "no stream")

	batch := func(stream, sid string, caps ...string) modevents.Batch {
		return modevents.Batch{V: 1, Stream: stream, Agent: "cc", Caps: caps,
			Events: []modevents.Event{{Seq: 1, At: 1, SID: sid, Type: "heartbeat", Data: json.RawMessage(`{}`)}}}
	}
	_, err := reg.Apply(batch("stream-aaaa", "sid-a"))
	require.NoError(t, err)
	assert.False(t, m.sessionInteractive("sid-a"), "a mod that announced nothing")
	_, err = reg.Apply(batch("stream-bbbb", "sid-b", modevents.CapPromptV1))
	require.NoError(t, err)
	assert.True(t, m.sessionInteractive("sid-b"))
	assert.False(t, m.sessionInteractive("sid-a"), "another session's capability is not this one's")
	assert.False(t, (&Module{}).sessionInteractive("sid-b"), "no registry at all")
}

// An auto request from a session whose mod can run a prompt job is a queue
// request: answered at once, with a position. (M: resolve to wait.)
func TestAuto_InteractiveSessionIsQueued(t *testing.T) {
	f := newPosFix(t)
	f.m.interactive = func(sid string) bool { return sid == "sid-agent" }
	f.setZone("danger", "flat")
	rec := f.postAs(newCID(), pidA, resources.CallAuto, "go build ./...")
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	got := decodeLease(t, rec)
	assert.Equal(t, resources.StateQueued, got.State)
	assert.NotEmpty(t, got.PositionID)
	assert.Equal(t, resources.CallQueue, f.row(got.ID).Mode, "the row keeps what auto resolved to")
}

// A session without such a mod (claude -p, a script nobody watches) waits as
// before, and so does a caller no session is found for. (M: resolve to queue
// without asking.)
func TestAuto_OtherCallersWait(t *testing.T) {
	f := newPosFix(t)
	f.m.interactive = func(string) bool { return false }
	f.setZone("danger", "flat")
	got := decodeLease(t, f.postAs(newCID(), pidA, resources.CallAuto, "go build ./..."))
	assert.Equal(t, resources.StateWaiting, got.State)
	assert.Empty(t, got.PositionID, "a waiting caller makes no position")
	assert.Equal(t, resources.CallWait, f.row(got.ID).Mode)

	// No session behind the holder: nothing to ask about.
	f.m.interactive = func(string) bool { return true }
	rec := f.postAs(newCID(), 4242, resources.CallAuto, "go build ./...")
	require.Less(t, rec.Code, 300, rec.Body.String())
	got = decodeLease(t, rec)
	assert.Equal(t, resources.CallWait, f.row(got.ID).Mode)
	assert.Empty(t, got.PositionID)
}

// In advise mode nothing is queued, auto or not.
func TestAuto_AdviseNeverQueues(t *testing.T) {
	f := newPosFix(t)
	f.m.interactive = func(string) bool { return true }
	f.set.set(resources.Settings{Mode: resources.ModeAdvise})
	rec := f.postAs(newCID(), pidA, resources.CallAuto, "go build ./...")
	require.Less(t, rec.Code, 300, rec.Body.String())
	got := decodeLease(t, rec)
	assert.Equal(t, resources.CallWait, f.row(got.ID).Mode)
	assert.Empty(t, got.PositionID)
}

// The same client id again is the same request, though the row says queue.
func TestAuto_ReplayIsTheSameRequest(t *testing.T) {
	f := newPosFix(t)
	f.m.interactive = func(string) bool { return true }
	f.setZone("danger", "flat")
	cid := newCID()
	first := decodeLease(t, f.postAs(cid, pidA, resources.CallAuto, "go build ./..."))
	again := f.postAs(cid, pidA, resources.CallAuto, "go build ./...")
	assert.Equal(t, http.StatusOK, again.Code, again.Body.String())
	assert.Equal(t, first.ID, decodeLease(t, again).ID)
}

// An ask row is not one auto could have made: the same client id with auto is a
// reused id, not the ask request. (M: let auto replay any row.)
func TestAuto_DoesNotReplayAnAskRow(t *testing.T) {
	f := newPosFix(t)
	f.setZone("danger", "flat")
	cid := newCID()
	require.Less(t, f.postAs(cid, pidA, resources.CallAsk, "go build ./...").Code, 300)
	again := f.postAs(cid, pidA, resources.CallAuto, "go build ./...")
	assert.Equal(t, http.StatusConflict, again.Code, again.Body.String())
	assert.Equal(t, resources.ErrClientIDReused, apiCode(again))
}
