package teammod

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/wake/purdex/internal/promptq"
	"github.com/wake/purdex/internal/team"
)

type fakeCompactQueue struct {
	res   promptq.Result
	calls []string
}

func (q *fakeCompactQueue) SubmitCompact(_ context.Context, sid string) (promptq.Result, error) {
	q.calls = append(q.calls, sid)
	return q.res, nil
}

func (f *fixture) memberNow(inbox, target string) (int, team.MemberCompactionView, team.APIError) {
	f.t.Helper()
	code, raw := f.do(http.MethodPost, "/api/team/member-compaction", team.MemberCompactionRequest{OriginInbox: inbox, Target: target, Now: true})
	var v team.MemberCompactionView
	if code != http.StatusOK {
		return code, v, decodeErr(f.t, raw)
	}
	if err := json.Unmarshal(raw, &v); err != nil {
		f.t.Fatalf("decode %s: %v", raw, err)
	}
	return code, v, team.APIError{}
}

func TestMemberCompactionNow(t *testing.T) {
	f, root := newTeamFixture(t, 2)
	m1 := f.member(1, root, "sid-m1", "w-one", nil)
	q := &fakeCompactQueue{}
	f.core.Registry.Register(promptq.Key, q)

	for _, c := range []struct {
		res  promptq.Result
		code int
		want string // now, or the error code
	}{
		{promptq.Result{Status: promptq.Accepted, Reason: "now"}, 200, "now"},
		{promptq.Result{Status: promptq.Accepted, Reason: "turn_end"}, 200, "turn_end"},
		{promptq.Result{Status: promptq.Dropped, Reason: "unsupported_kind"}, 409, team.ErrCompactUnsupported},
		{promptq.Result{Status: promptq.Dropped, Reason: "x"}, 409, team.ErrCompactFailed},
		{promptq.Result{Status: promptq.Unknown}, 409, team.ErrCompactFailed},
		{promptq.Result{Status: promptq.NoMod}, 409, team.ErrCompactFailed},
	} {
		q.res = c.res
		code, v, e := f.memberNow("/tmp/10.sock", m1.Ref)
		got := v.Now
		if code != 200 {
			got = e.Error
		}
		if code != c.code || got != c.want {
			t.Fatalf("%+v: %d %q, want %d %q", c.res, code, got, c.code, c.want)
		}
	}
	if len(q.calls) != 6 || q.calls[0] != "sid-m1" {
		t.Fatalf("submit calls = %v", q.calls)
	}
}

func TestMemberCompactionNow_Refusals(t *testing.T) {
	f, root := newTeamFixture(t, 2)
	m1 := f.member(1, root, "sid-m1", "w-one", nil)
	q := &fakeCompactQueue{res: promptq.Result{Status: promptq.Accepted, Reason: "now"}}
	f.core.Registry.Register(promptq.Key, q)

	// its own session has an open relay op
	if err := f.m.store.CreateRelayOp(selfOp("op-1", "sid-m1", m1.Ref, 1000)); err != nil {
		t.Fatal(err)
	}
	if code, _, e := f.memberNow("/tmp/10.sock", m1.Ref); code != 409 || e.Error != team.ErrRelayOpen || e.Op == nil {
		t.Fatalf("own op: %d %+v, want 409 relay_open", code, e)
	}
	// a relay that has cleared into this session and whose seed turn is still running
	if _, err := f.m.store.db.Exec(`UPDATE relay_ops SET session_id = 'sid-old', new_session_id = 'sid-m1', state = 'cleared' WHERE id = 'op-1'`); err != nil {
		t.Fatal(err)
	}
	if code, _, e := f.memberNow("/tmp/10.sock", m1.Ref); code != 409 || e.Error != team.ErrRelayOpen {
		t.Fatalf("cleared into this session: %d %+v, want 409 relay_open", code, e)
	}
	if len(q.calls) != 0 {
		t.Fatalf("a refused now reached the queue: %v", q.calls)
	}
	// now with set or reset
	for _, req := range []team.MemberCompactionRequest{
		{OriginInbox: "/tmp/10.sock", Target: m1.Ref, Now: true, Reset: true},
		{OriginInbox: "/tmp/10.sock", Target: m1.Ref, Now: true, Set: &team.CompactionOverride{Auto: ptr(false)}},
	} {
		if code, _ := f.do(http.MethodPost, "/api/team/member-compaction", req); code != 400 {
			t.Fatalf("%+v = %d, want 400", req, code)
		}
	}
}
