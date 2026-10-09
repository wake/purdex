package push

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"testing"

	"github.com/wake/purdex/internal/push/apns"
	"github.com/wake/purdex/internal/team"
)

// payloadOf parses a payload, checks mutable-content, and returns open_approvals (ok=false = the key is absent).
func payloadOf(t *testing.T, payload string) (n int, ok bool) {
	t.Helper()
	var p struct {
		Aps    map[string]any `json:"aps"`
		Purdex map[string]any `json:"purdex"`
	}
	if err := json.Unmarshal([]byte(payload), &p); err != nil {
		t.Fatal(err)
	}
	if p.Aps["mutable-content"] != float64(1) {
		t.Fatalf("mutable-content missing: %s", payload)
	}
	v, has := p.Purdex["open_approvals"]
	if !has {
		return 0, false
	}
	f, isNum := v.(float64)
	if !isNum {
		t.Fatalf("open_approvals is not an integer: %s", payload)
	}
	return int(f), true
}

func badgeAsk(id string, terminalOnly bool) team.Approval {
	a := leadApproval(id)
	a.Kind = team.KindHookAsk
	a.Payload = json.RawMessage(`{"terminal_only":` + strconv.FormatBool(terminalOnly) + `,"questions":[{"question":"q?"}]}`)
	return a
}

func unpushed(id string) team.Approval {
	a := leadApproval(id)
	a.Kind = team.KindHookPermission
	return a
}

// The count is the reader's open set at send time: 3 open, one terminal_only ask and one unpushed kind do not count.
func TestBadge_CountComesFromTheReaderAtSendTime(t *testing.T) {
	te := newTriggerEnv(t, nil)
	te.register(tokA, "en", "mlab")
	te.events.readOpen = func() ([]team.Approval, error) {
		return []team.Approval{leadApproval("ap1"), badgeAsk("t1", true), unpushed("p1")}, nil
	}
	te.events.emit("opened", leadApproval("ap1"))
	p := te.waitSends(t, 1)[0].Payload
	if n, ok := payloadOf(t, p); !ok || n != 1 {
		t.Fatalf("open_approvals = %d (present %v), want 1: %s", n, ok, p)
	}
	t.Logf("known: %s", p)
}

// It is read at each send, not kept: the reader's set changing between two pushes changes the count with no event.
func TestBadge_ReadAgainOnEverySend(t *testing.T) {
	te := newTriggerEnv(t, nil)
	te.register(tokA, "en", "mlab")
	set := []team.Approval{leadApproval("ap1")}
	te.events.readOpen = func() ([]team.Approval, error) { return set, nil }
	te.events.emit("opened", leadApproval("ap1"))
	if n, _ := payloadOf(t, te.waitSends(t, 1)[0].Payload); n != 1 {
		t.Fatalf("first = %d, want 1", n)
	}
	set = []team.Approval{leadApproval("ap1"), leadApproval("ap2"), badgeAsk("a1", false)} // no event for ap1 / a1
	te.events.emit("opened", leadApproval("ap2"))
	if n, _ := payloadOf(t, te.waitSends(t, 2)[1].Payload); n != 3 {
		t.Fatalf("second = %d, want 3", n)
	}
}

func TestBadge_ReaderErrorLeavesTheFieldOut(t *testing.T) {
	te := newTriggerEnv(t, nil)
	te.register(tokA, "en", "mlab")
	te.events.readOpen = func() ([]team.Approval, error) { return nil, errors.New("db gone") }
	te.events.emit("opened", leadApproval("ap1"))
	p := te.waitSends(t, 1)[0].Payload
	if n, ok := payloadOf(t, p); ok {
		t.Fatalf("open_approvals = %d present on a failed read: %s", n, p)
	}
	t.Logf("unknown: %s", p)
}

// eventsOnly hides the reader: a feed that is not also an OpenApprovalsReader.
type eventsOnly struct{ team.ApprovalEvents }

func TestBadge_NoReaderLeavesTheFieldOut(t *testing.T) {
	e := newEnv(t)
	te := &triggerEnv{env: e, apns: &fakeAPNS{script: []apns.Result{{Class: apns.OK, Status: 200}}}, events: &fakeEvents{}}
	e.mod.events = eventsOnly{te.events}
	e.mod.newAPNs = func() apnsClient { return te.apns }
	if err := e.mod.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { e.mod.Stop(context.Background()) })
	te.register(tokA, "en", "mlab")
	te.events.emit("opened", leadApproval("ap1"))
	if n, ok := payloadOf(t, te.waitSends(t, 1)[0].Payload); ok {
		t.Fatalf("open_approvals = %d present with no reader", n)
	}
}
