package dispatch

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// ---- client.Report wire tests -------------------------------------------------

func TestClient_Report_Success(t *testing.T) {
	var last *http.Request
	var body []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		last = r
		body, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"schema_version":1,"ack_seq":1}`))
	}))
	defer srv.Close()
	c := NewClient(srv.URL, "test-token")

	res, err := c.Report(context.Background(), "dsp_a1", []byte(`{"schema_version":1,"status":"accepted","seq":1}`))
	if err != nil {
		t.Fatalf("Report: %v", err)
	}
	if res.AckSeq != 1 {
		t.Fatalf("ack_seq = %d, want 1", res.AckSeq)
	}
	if last.Method != http.MethodPost || last.URL.Path != "/daemon/dispatches/dsp_a1/report" {
		t.Errorf("wire = %s %s", last.Method, last.URL.Path)
	}
	if ct := last.Header.Get("Content-Type"); ct != "application/json" {
		t.Errorf("content-type = %q", ct)
	}
	if len(body) == 0 {
		t.Error("request body was empty")
	}
}

func TestClient_Report_AcceptedRequired(t *testing.T) {
	f := loadFixture(t, "e4_report.accepted_required.json")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(f.HTTPStatus) // 409
		_, _ = w.Write(f.Payload)
	}))
	defer srv.Close()
	c := NewClient(srv.URL, "test-token")

	_, err := c.Report(context.Background(), "dsp_a1", []byte(`{"status":"running","seq":2}`))
	if !errors.Is(err, ErrAcceptedRequired) {
		t.Fatalf("want ErrAcceptedRequired, got %v", err)
	}
}

func TestClient_Report_StaleSeqIsNotError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		// stale_seq: Ploom returns 200 + current ack_seq (contract §4, not an error).
		_, _ = w.Write([]byte(`{"schema_version":1,"ack_seq":3}`))
	}))
	defer srv.Close()
	c := NewClient(srv.URL, "test-token")

	res, err := c.Report(context.Background(), "dsp_a1", []byte(`{"status":"running","seq":2}`))
	if err != nil {
		t.Fatalf("stale_seq must not be an error: %v", err)
	}
	if res.AckSeq != 3 {
		t.Fatalf("ack_seq = %d, want 3", res.AckSeq)
	}
}

// ---- payload builders ---------------------------------------------------------

func TestBuildRunningPayload_MatchesFixture(t *testing.T) {
	got, err := BuildRunningPayload("exc_9", 2)
	if err != nil {
		t.Fatalf("BuildRunningPayload: %v", err)
	}
	want := fixturePayloadMap(t, "e4_report.running.json")
	assertJSONEqual(t, got, want)
}

func TestBuildTerminalPayload_CompletedArtifactsMatchesFixture(t *testing.T) {
	arts := []Artifact{
		{Kind: "diff", Pointer: "pdx://dmn_1/execution/exc_9/diff", Meta: map[string]any{"files": 4, "add": 120, "del": 8}},
		{Kind: "transcript", Pointer: "pdx://dmn_1/execution/exc_9/transcript", Meta: map[string]any{"lines": 512}},
	}
	got, err := BuildTerminalPayload("exc_9", 3, "completed", arts, nil)
	if err != nil {
		t.Fatalf("BuildTerminalPayload: %v", err)
	}
	want := fixturePayloadMap(t, "e4_report.completed_artifacts.json")
	assertJSONEqual(t, got, want)
}

func TestBuildAcceptedPayload_ShapeAndNullSessionCode(t *testing.T) {
	got, err := BuildAcceptedPayload(AcceptedRow{
		ExecutionID:             "exc_9",
		DispatchID:              "dsp_a1",
		RepoLocation:            "/abs/repo",
		Provider:                "claude",
		AttemptNo:               1,
		EffectiveSandboxProfile: "ask",
		HeadAtStart:             "abc123",
		DirtyAtStart:            false,
		SessionCode:             "", // → null
	})
	if err != nil {
		t.Fatalf("BuildAcceptedPayload: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(got, &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if m["status"] != "accepted" || m["seq"].(float64) != 1 {
		t.Errorf("status/seq = %v/%v", m["status"], m["seq"])
	}
	if m["head_at_start"] != "abc123" || m["effective_sandbox_profile"] != "ask" {
		t.Errorf("immutable metadata = %+v", m)
	}
	if v, ok := m["session_code"]; !ok || v != nil {
		t.Errorf("session_code should be JSON null, got %v (present=%v)", v, ok)
	}
	if m["provider"] != "claude" || m["attempt_no"].(float64) != 1 {
		t.Errorf("provider/attempt = %v/%v", m["provider"], m["attempt_no"])
	}
	// No RepoLocationJSON → fallback echoes just {local_dir}.
	repo := m["repo_location"].(map[string]any)
	if repo["local_dir"] != "/abs/repo" || len(repo) != 1 {
		t.Errorf("fallback repo_location = %v", repo)
	}
}

// TestBuildAcceptedPayload_EchoesFullRepoLocation proves the full S5
// repo_location object (project_id/is_origin/…) is echoed verbatim when persisted
// (m0-contract §2 / e4_report.accepted.json), not collapsed to just local_dir.
func TestBuildAcceptedPayload_EchoesFullRepoLocation(t *testing.T) {
	got, err := BuildAcceptedPayload(AcceptedRow{
		ExecutionID:             "exc_9",
		DispatchID:              "dsp_a1",
		RepoLocation:            "/Users/wake/Workspace/wake/example",
		RepoLocationJSON:        `{"project_id":"prj_1","local_dir":"/Users/wake/Workspace/wake/example","is_origin":true}`,
		Provider:                "claude",
		AttemptNo:               1,
		EffectiveSandboxProfile: "ask",
		HeadAtStart:             "abc123def4567890abc123def4567890abc12345",
	})
	if err != nil {
		t.Fatalf("BuildAcceptedPayload: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(got, &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	repo := m["repo_location"].(map[string]any)
	if repo["project_id"] != "prj_1" {
		t.Errorf("project_id not echoed: %v", repo)
	}
	if repo["local_dir"] != "/Users/wake/Workspace/wake/example" {
		t.Errorf("local_dir = %v", repo["local_dir"])
	}
	if repo["is_origin"] != true {
		t.Errorf("is_origin not echoed: %v", repo)
	}
}

// ---- Sender behaviour with a stateful fake Ploom ------------------------------

func TestSender_AcceptedThenRunning_Ordering(t *testing.T) {
	fp := newFakePloom(t)
	o := newTestOutbox(t)
	s := NewSender(o, fp.client())

	enqueueAccepted(t, s, "exc_9", "dsp_a1")
	enqueueRunning(t, s, "exc_9", "dsp_a1", 2)

	if err := s.Flush(context.Background()); err != nil {
		t.Fatalf("Flush: %v", err)
	}

	fp.assertOrder(t, "accepted", "running")
	if seq, _ := o.Cursor("exc_9"); seq != 2 {
		t.Fatalf("cursor = %d, want 2", seq)
	}
	assertAcked(t, o, "exc_9", 1)
	assertAcked(t, o, "exc_9", 2)
}

func TestSender_RunningFirst_409_BackfillsAccepted(t *testing.T) {
	fp := newFakePloom(t)
	o := newTestOutbox(t)
	s := NewSender(o, fp.client())

	// Simulate a local state that believes accepted is acked (cursor=1, accepted
	// locally acked) but Ploom has never seen it → running triggers 409.
	enqueueAccepted(t, s, "exc_9", "dsp_a1")
	enqueueRunning(t, s, "exc_9", "dsp_a1", 2)
	a, _, _ := o.RecordBySeq("exc_9", 1)
	o.MarkAcked(a.ID)
	o.AdvanceCursor("exc_9", 1)

	// First flush: running → 409 accepted_required → sender re-sends accepted.
	if err := s.Flush(context.Background()); err != nil {
		t.Fatalf("Flush 1: %v", err)
	}
	// Second flush: accepted now acked at Ploom → running goes through.
	if err := s.Flush(context.Background()); err != nil {
		t.Fatalf("Flush 2: %v", err)
	}

	// Ploom saw a 409'd running, then accepted (backfill), then a successful running.
	statuses := fp.statuses()
	if len(statuses) < 3 || statuses[0] != "running" {
		t.Fatalf("expected running(409) first, got %v", statuses)
	}
	if !containsInOrder(statuses, "accepted", "running") {
		t.Fatalf("expected accepted backfill before successful running, got %v", statuses)
	}
	assertAcked(t, o, "exc_9", 2)
}

func TestSender_StaleSeq_NotResent(t *testing.T) {
	fp := newFakePloom(t)
	o := newTestOutbox(t)
	s := NewSender(o, fp.client())

	// Cursor already ahead of the queued running(seq=2): it is stale/projected.
	enqueueRunning(t, s, "exc_9", "dsp_a1", 2)
	o.AdvanceCursor("exc_9", 3)

	if err := s.Flush(context.Background()); err != nil {
		t.Fatalf("Flush: %v", err)
	}
	if n := len(fp.statuses()); n != 0 {
		t.Fatalf("stale report must not be sent, but Ploom saw %d", n)
	}
	assertAcked(t, o, "exc_9", 2)
	if seq, _ := o.Cursor("exc_9"); seq != 3 {
		t.Fatalf("cursor = %d, want 3", seq)
	}
}

func TestSender_5xx_BacksOffThenSucceeds(t *testing.T) {
	fp := newFakePloom(t)
	fp.fail5xx["exc_9"] = 1 // first accepted POST → 503
	o := newTestOutbox(t)
	clock := int64(1000)
	s := NewSender(o, fp.client(),
		WithNow(func() int64 { return atomic.LoadInt64(&clock) }),
		WithBackoff(func(int) time.Duration { return 10 * time.Second }),
	)

	enqueueAccepted(t, s, "exc_9", "dsp_a1")

	// First flush: accepted → 503 → backoff.
	if err := s.Flush(context.Background()); err != nil {
		t.Fatalf("Flush 1: %v", err)
	}
	a, _, _ := o.RecordBySeq("exc_9", 1)
	if a.Attempts != 1 || a.Acked {
		t.Fatalf("after 5xx: attempts=%d acked=%v", a.Attempts, a.Acked)
	}
	if a.NextAttemptAt <= 1000 {
		t.Fatalf("next_attempt_at not advanced: %d", a.NextAttemptAt)
	}
	// Still inside backoff window → not due, not sent again.
	if err := s.Flush(context.Background()); err != nil {
		t.Fatalf("Flush 2: %v", err)
	}
	if got, _, _ := o.RecordBySeq("exc_9", 1); got.Attempts != 1 {
		t.Fatalf("should not retry inside backoff, attempts=%d", got.Attempts)
	}
	// Advance the clock past backoff → retry succeeds.
	atomic.StoreInt64(&clock, 1000+11)
	if err := s.Flush(context.Background()); err != nil {
		t.Fatalf("Flush 3: %v", err)
	}
	assertAcked(t, o, "exc_9", 1)
}

func TestSender_401_PermanentFailure(t *testing.T) {
	fp := newFakePloom(t)
	fp.fail401 = true
	o := newTestOutbox(t)
	s := NewSender(o, fp.client())

	enqueueAccepted(t, s, "exc_9", "dsp_a1")
	if err := s.Flush(context.Background()); err != nil {
		t.Fatalf("Flush: %v", err)
	}

	a, _, _ := o.RecordBySeq("exc_9", 1)
	if !a.Permanent || a.Acked {
		t.Fatalf("401 should be permanent, got %+v", a)
	}
	if due, _ := o.DueExecutions(1 << 40); len(due) != 0 {
		t.Fatalf("permanent-failed execution must not be due, got %v", due)
	}
}

func TestSender_RestartReplay_ResumesFromCursor(t *testing.T) {
	fp := newFakePloom(t)
	o := newTestOutbox(t)

	// Sender #1 enqueues accepted + running but "crashes" before flushing.
	s1 := NewSender(o, fp.client())
	enqueueAccepted(t, s1, "exc_9", "dsp_a1")
	enqueueRunning(t, s1, "exc_9", "dsp_a1", 2)

	// Sender #2 (post-restart) shares the same durable outbox and replays.
	s2 := NewSender(o, fp.client())
	if err := s2.Flush(context.Background()); err != nil {
		t.Fatalf("replay Flush: %v", err)
	}

	fp.assertOrder(t, "accepted", "running")
	assertAcked(t, o, "exc_9", 1)
	assertAcked(t, o, "exc_9", 2)
}

func TestSender_DurabilityCut_ReconstructsAcceptedFromRow(t *testing.T) {
	fp := newFakePloom(t)
	o := newTestOutbox(t)
	// The in-flight accepted was lost (never hit the outbox); only running is
	// queued. The execution row survives, so accepted is rebuilt from it.
	reader := &fakeExecReader{rows: map[string]AcceptedRow{
		"exc_9": {
			ExecutionID:             "exc_9",
			DispatchID:              "dsp_a1",
			RepoLocation:            "/abs/repo",
			RepoLocationJSON:        `{"project_id":"prj_1","local_dir":"/abs/repo","is_origin":true}`,
			Provider:                "claude",
			AttemptNo:               1,
			EffectiveSandboxProfile: "ask",
			HeadAtStart:             "deadbeefcafe",
			DirtyAtStart:            true,
		},
	}}
	s := NewSender(o, fp.client(), WithExecutionReader(reader))

	enqueueRunning(t, s, "exc_9", "dsp_a1", 2)

	if err := s.Flush(context.Background()); err != nil {
		t.Fatalf("Flush: %v", err)
	}

	// accepted was reconstructed and sent before running, carrying the row's
	// immutable metadata.
	fp.assertOrder(t, "accepted", "running")
	acc := fp.first(t, "accepted")
	if acc["head_at_start"] != "deadbeefcafe" {
		t.Errorf("reconstructed head_at_start = %v", acc["head_at_start"])
	}
	if acc["effective_sandbox_profile"] != "ask" {
		t.Errorf("reconstructed sandbox = %v", acc["effective_sandbox_profile"])
	}
	if acc["dirty_at_start"] != true {
		t.Errorf("reconstructed dirty_at_start = %v", acc["dirty_at_start"])
	}
	repo, _ := acc["repo_location"].(map[string]any)
	if repo["local_dir"] != "/abs/repo" {
		t.Errorf("reconstructed repo_location = %v", acc["repo_location"])
	}
	// The reconstructed accepted echoes the FULL repo_location object (contract §2),
	// not just local_dir — project_id/is_origin survive the durability cut.
	if repo["project_id"] != "prj_1" {
		t.Errorf("reconstructed repo_location missing project_id: %v", acc["repo_location"])
	}
	if repo["is_origin"] != true {
		t.Errorf("reconstructed repo_location missing is_origin: %v", acc["repo_location"])
	}
	// The reconstructed accepted is now durably in the outbox.
	if _, ok, _ := o.RecordBySeq("exc_9", 1); !ok {
		t.Error("reconstructed accepted was not persisted to the outbox")
	}
	assertAcked(t, o, "exc_9", 2)
}

func TestSender_CompletedWithArtifacts_Sent(t *testing.T) {
	fp := newFakePloom(t)
	o := newTestOutbox(t)
	s := NewSender(o, fp.client())

	enqueueAccepted(t, s, "exc_9", "dsp_a1")
	completed, err := BuildTerminalPayload("exc_9", 2, "completed",
		[]Artifact{{Kind: "diff", Pointer: "pdx://dmn_1/execution/exc_9/diff", Meta: map[string]any{"files": 4, "add": 120, "del": 8}}},
		nil)
	if err != nil {
		t.Fatalf("BuildTerminalPayload: %v", err)
	}
	if err := s.Enqueue("exc_9", "dsp_a1", 2, "completed", completed); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}

	if err := s.Flush(context.Background()); err != nil {
		t.Fatalf("Flush: %v", err)
	}

	fp.assertOrder(t, "accepted", "completed")
	comp := fp.first(t, "completed")
	arts, ok := comp["artifacts"].([]any)
	if !ok || len(arts) != 1 {
		t.Fatalf("artifacts missing/wrong: %v", comp["artifacts"])
	}
	art := arts[0].(map[string]any)
	if art["kind"] != "diff" || art["pointer"] != "pdx://dmn_1/execution/exc_9/diff" {
		t.Errorf("artifact = %v", art)
	}
	assertAcked(t, o, "exc_9", 2)
}

// ---- test helpers -------------------------------------------------------------

func enqueueAccepted(t *testing.T, s *Sender, execID, dispatchID string) {
	t.Helper()
	p, err := BuildAcceptedPayload(AcceptedRow{
		ExecutionID: execID, DispatchID: dispatchID, RepoLocation: "/abs/repo",
		Provider: "claude", AttemptNo: 1, EffectiveSandboxProfile: "ask", HeadAtStart: "abc123",
	})
	if err != nil {
		t.Fatalf("build accepted: %v", err)
	}
	if err := s.Enqueue(execID, dispatchID, 1, "accepted", p); err != nil {
		t.Fatalf("enqueue accepted: %v", err)
	}
}

func enqueueRunning(t *testing.T, s *Sender, execID, dispatchID string, seq int) {
	t.Helper()
	p, err := BuildRunningPayload(execID, seq)
	if err != nil {
		t.Fatalf("build running: %v", err)
	}
	if err := s.Enqueue(execID, dispatchID, seq, "running", p); err != nil {
		t.Fatalf("enqueue running: %v", err)
	}
}

func assertAcked(t *testing.T, o *Outbox, execID string, seq int) {
	t.Helper()
	r, ok, err := o.RecordBySeq(execID, seq)
	if err != nil || !ok {
		t.Fatalf("RecordBySeq(%s,%d): ok=%v err=%v", execID, seq, ok, err)
	}
	if !r.Acked {
		t.Fatalf("record (%s,%d) not acked: %+v", execID, seq, r)
	}
}

func containsInOrder(hay []string, a, b string) bool {
	ai := -1
	for i, s := range hay {
		if s == a && ai == -1 {
			ai = i
		} else if s == b && ai != -1 && i > ai {
			return true
		}
	}
	return false
}

// fakeExecReader is a static ExecutionReader for the durability-cut path.
type fakeExecReader struct{ rows map[string]AcceptedRow }

func (r *fakeExecReader) LoadAcceptedRow(execID string) (AcceptedRow, bool, error) {
	row, ok := r.rows[execID]
	return row, ok, nil
}

// fakePloom is a stateful in-memory Ploom report endpoint implementing the
// contract's ack/ordering semantics (m0-contract §4).
type fakePloom struct {
	mu           sync.Mutex
	acceptedSeen map[string]bool
	ackSeq       map[string]int
	received     []map[string]any
	fail5xx      map[string]int // execID → remaining 5xx to serve
	fail401      bool
	srv          *httptest.Server
}

func newFakePloom(t *testing.T) *fakePloom {
	f := &fakePloom{
		acceptedSeen: map[string]bool{},
		ackSeq:       map[string]int{},
		fail5xx:      map[string]int{},
	}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /daemon/dispatches/{id}/report", f.handle)
	f.srv = httptest.NewServer(mux)
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakePloom) client() *Client { return NewClient(f.srv.URL, "test-token") }

func (f *fakePloom) handle(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	var raw map[string]any
	_ = json.Unmarshal(body, &raw)
	execID, _ := raw["execution_id"].(string)
	status, _ := raw["status"].(string)
	seq := 0
	if v, ok := raw["seq"].(float64); ok {
		seq = int(v)
	}

	f.mu.Lock()
	defer f.mu.Unlock()
	f.received = append(f.received, raw)

	w.Header().Set("Content-Type", "application/json")

	if f.fail401 {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	if n := f.fail5xx[execID]; n > 0 {
		f.fail5xx[execID] = n - 1
		w.WriteHeader(http.StatusServiceUnavailable)
		return
	}

	if status == "accepted" {
		f.acceptedSeen[execID] = true
		if seq > f.ackSeq[execID] {
			f.ackSeq[execID] = seq
		}
		f.writeAck(w, execID)
		return
	}
	// lifecycle
	if !f.acceptedSeen[execID] {
		w.WriteHeader(http.StatusConflict)
		_, _ = w.Write([]byte(`{"schema_version":1,"error":{"code":"accepted_required","message":"accepted must be acked first"}}`))
		return
	}
	if seq > f.ackSeq[execID] {
		f.ackSeq[execID] = seq
	}
	f.writeAck(w, execID)
}

func (f *fakePloom) writeAck(w http.ResponseWriter, execID string) {
	_ = json.NewEncoder(w).Encode(map[string]any{"schema_version": 1, "ack_seq": f.ackSeq[execID]})
}

func (f *fakePloom) statuses() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]string, 0, len(f.received))
	for _, r := range f.received {
		s, _ := r["status"].(string)
		out = append(out, s)
	}
	return out
}

// assertOrder checks that the given statuses each appear, in the given relative
// order, somewhere in the received stream (ignoring any 409'd interlopers).
func (f *fakePloom) assertOrder(t *testing.T, want ...string) {
	t.Helper()
	got := f.statuses()
	idx := 0
	for _, g := range got {
		if idx < len(want) && g == want[idx] {
			idx++
		}
	}
	if idx != len(want) {
		t.Fatalf("status order: got %v, want subsequence %v", got, want)
	}
}

// first returns the first received payload with the given status.
func (f *fakePloom) first(t *testing.T, status string) map[string]any {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, r := range f.received {
		if s, _ := r["status"].(string); s == status {
			return r
		}
	}
	t.Fatalf("no received report with status %q", status)
	return nil
}

// fixturePayloadMap loads a fixture's payload as a decoded map.
func fixturePayloadMap(t *testing.T, file string) map[string]any {
	t.Helper()
	f := loadFixture(t, file)
	var m map[string]any
	if err := json.Unmarshal(f.Payload, &m); err != nil {
		t.Fatalf("decode fixture %s payload: %v", file, err)
	}
	return m
}

func assertJSONEqual(t *testing.T, gotJSON []byte, want map[string]any) {
	t.Helper()
	var got map[string]any
	if err := json.Unmarshal(gotJSON, &got); err != nil {
		t.Fatalf("unmarshal got: %v", err)
	}
	gb, _ := json.Marshal(got)
	wb, _ := json.Marshal(want)
	if string(gb) != string(wb) {
		t.Fatalf("JSON mismatch:\n got  %s\n want %s", gb, wb)
	}
}
