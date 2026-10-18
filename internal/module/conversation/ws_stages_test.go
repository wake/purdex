package conversation

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wake/purdex/internal/convfeed"
)

// The conversation.stages frame and the snapshot's stages (plan §1.2).

type stagesFrame struct {
	Stages []stageJSON `json:"stages"`
}

func stagesOfFrame(t *testing.T, f wsFrame) []stageJSON {
	t.Helper()
	var v stagesFrame
	require.NoError(t, json.Unmarshal(f.Value, &v), string(f.Value))
	return v.Stages
}

func noFrameWithin(t *testing.T, c *wsClient, d time.Duration) {
	t.Helper()
	_ = c.conn.SetReadDeadline(time.Now().Add(d))
	if _, b, err := c.conn.ReadMessage(); err == nil {
		t.Fatalf("an unexpected frame: %s", b)
	}
}

func TestWSStages_SnapshotCarriesTheLedger(t *testing.T) {
	e, _ := wsEnv(t)
	e.transcript(idleTurns(1))
	now := time.Now()
	e.mod.stages.begin(sid, "a", "x", now)
	e.mod.stages.set(sid, "a", stageEntered, "", "fill", now)
	srv := e.server()
	c := e.connect(srv, "")
	var snap struct {
		Stages []stageJSON `json:"stages"`
	}
	require.NoError(t, json.Unmarshal(c.expect("conversation.snapshot").Value, &snap))
	require.Len(t, snap.Stages, 1)
	assert.Equal(t, "a", snap.Stages[0].ClientMsgID)
	assert.Equal(t, stageEntered, snap.Stages[0].Stage)
	assert.Equal(t, "fill", snap.Stages[0].Mode)
	c.expect("approvals.snapshot")
	noFrameWithin(t, c, 150*time.Millisecond) // the snapshot was the baseline: nothing is replayed
}

func TestWSStages_AStageChangeIsAFrameWithNoTranscriptChange(t *testing.T) {
	e, _ := wsEnv(t)
	e.transcript(idleTurns(1))
	srv := e.server()
	c := e.connect(srv, "")
	c.expect("conversation.snapshot")
	c.expect("approvals.snapshot")
	now := time.Now()
	e.mod.stages.begin(sid, "a", "x", now)
	got := stagesOfFrame(t, c.expect("conversation.stages"))
	require.Len(t, got, 1)
	assert.Equal(t, stageReceived, got[0].Stage)
	e.mod.stages.set(sid, "a", stageFailed, "busy", "", now)
	got = stagesOfFrame(t, c.expect("conversation.stages"))
	require.Len(t, got, 1)
	assert.Equal(t, stageFailed, got[0].Stage)
	assert.Equal(t, "busy", got[0].Reason)
}

// The frame leaves before the quiet-stream gate: a stage change must not wait for a transcript write or the next full pass.
func TestWSStages_AFrameLeavesThroughTheQuietGate(t *testing.T) {
	e, _, _ := quietEnv(t, time.Hour)
	srv := e.server()
	c := e.connect(srv, "")
	c.expect("conversation.snapshot")
	c.expect("approvals.snapshot")
	time.Sleep(150 * time.Millisecond) // past the first full pass: the gate is closed now
	e.mod.stages.begin(sid, "a", "x", time.Now())
	got := stagesOfFrame(t, c.expect("conversation.stages"))
	require.Len(t, got, 1)
}

func TestWSStages_AQuietLedgerSendsNothing(t *testing.T) {
	e, _ := wsEnv(t)
	e.transcript(idleTurns(1))
	e.mod.stages.begin(sid, "a", "x", time.Now())
	srv := e.server()
	c := e.connect(srv, "")
	c.expect("conversation.snapshot")
	c.expect("approvals.snapshot")
	noFrameWithin(t, c, 300*time.Millisecond) // 30 polls of 10 ms
}

func TestWSStages_ChangesOfOneTickShareAFrame(t *testing.T) {
	e, _ := wsEnv(t)
	e.transcript(idleTurns(1))
	e.mod.tweak.poll = 150 * time.Millisecond
	srv := e.server()
	c := e.connect(srv, "")
	c.expect("conversation.snapshot")
	c.expect("approvals.snapshot")
	time.Sleep(20 * time.Millisecond) // inside one poll interval
	now := time.Now()
	for _, id := range []string{"a", "b", "c"} {
		e.mod.stages.begin(sid, id, id, now)
	}
	e.mod.stages.set(sid, "a", stageEntered, "", "fill", now)
	got := stagesOfFrame(t, c.expect("conversation.stages"))
	require.Len(t, got, 3)
	byID := map[string]string{}
	for _, g := range got {
		byID[g.ClientMsgID] = g.Stage
	}
	assert.Equal(t, map[string]string{"a": stageEntered, "b": stageReceived, "c": stageReceived}, byID, "one entry per id, its latest stage")
	noFrameWithin(t, c, 200*time.Millisecond)
}

// queued and read come from the transcript: the header's queue row claims the entered id, the dequeue + user row reads it.
func TestWSStages_QueuedAndReadFollowTheTranscript(t *testing.T) {
	e, _ := wsEnv(t)
	p := e.transcript(idleTurns(1))
	e.owners.own = []convfeed.Owner{{TranscriptPath: p, Status: "running", SeenAt: 1}}
	// the transcript's rows are stamped in the test fixtures' epoch (http_test.go at()): the ledger's clock follows it
	now := time.UnixMilli(1791378000000 + 80_000)
	e.mod.tune.now = func() time.Time { return now }
	srv := e.server()
	c := e.connect(srv, "")
	c.expect("conversation.snapshot")
	c.expect("approvals.snapshot")
	e.mod.stages.begin(sid, "a", "wait for it", now)
	e.mod.stages.set(sid, "a", stageEntered, "", "fill", now)
	c.expect("conversation.stages") // received .. entered, coalesced
	appendRows(t, p, queueRow("enqueue", "wait for it"))
	var queued []stageJSON
	for i := 0; i < 4 && len(queued) == 0; i++ {
		if f := c.read(); f.Type == "conversation.stages" {
			queued = stagesOfFrame(t, f)
		}
	}
	require.Len(t, queued, 1)
	assert.Equal(t, stageQueued, queued[0].Stage)
	// send now: a dequeue row, then the message as a real user row
	appendRows(t, p, queueRow("dequeue", ""), userRow("u7", 90, "wait for it"))
	var read []stageJSON
	for i := 0; i < 6 && len(read) == 0; i++ {
		if f := c.read(); f.Type == "conversation.stages" {
			read = stagesOfFrame(t, f)
		}
	}
	require.Len(t, read, 1)
	assert.Equal(t, stageRead, read[0].Stage)
}

func TestWSStages_AReconnectSeesWhereTheMessagesAre(t *testing.T) {
	e, _ := wsEnv(t)
	e.transcript(idleTurns(1))
	now := time.Now()
	e.mod.stages.begin(sid, "a", "x", now)
	e.mod.stages.set(sid, "a", stageFailed, "generation", "", now)
	srv := e.server()
	for i := 0; i < 2; i++ { // twice: the second connection gets the same picture, and no delta of its own
		c := e.connect(srv, "")
		var snap struct {
			Stages []stageJSON `json:"stages"`
		}
		require.NoError(t, json.Unmarshal(c.expect("conversation.snapshot").Value, &snap))
		require.Len(t, snap.Stages, 1)
		assert.Equal(t, stageFailed, snap.Stages[0].Stage)
		c.expect("approvals.snapshot")
		noFrameWithin(t, c, 100*time.Millisecond)
	}
}
