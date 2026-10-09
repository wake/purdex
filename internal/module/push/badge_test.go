package push

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
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

// keysOf returns purdex.open_approval_keys (ok=false = the key is absent).
func keysOf(t *testing.T, payload string) (keys []string, ok bool) {
	t.Helper()
	var p struct {
		Purdex map[string]any `json:"purdex"`
	}
	if err := json.Unmarshal([]byte(payload), &p); err != nil {
		t.Fatal(err)
	}
	v, has := p.Purdex["open_approval_keys"]
	if !has {
		return nil, false
	}
	arr, isArr := v.([]any)
	if !isArr {
		t.Fatalf("open_approval_keys is not an array: %s", payload)
	}
	for _, x := range arr {
		keys = append(keys, x.(string))
	}
	return keys, true
}

func withTmux(a team.Approval, tmux string) team.Approval { a.Origin.Tmux = tmux; return a }

// fakeCodes is the name -> code table of the fake session lookup.
func fakeCodes(m map[string]string) func(string) string { return func(n string) string { return m[n] } }

func TestKeys_SameSessionDedupedAndNoTmuxUsesApprovalID(t *testing.T) {
	te := newTriggerEnv(t, nil)
	te.mod.codeOf = fakeCodes(map[string]string{"dev": "c0de01", "ops": "c0de02"})
	te.register(tokA, "en", "mlab")
	te.events.readOpen = func() ([]team.Approval, error) {
		return []team.Approval{
			withTmux(leadApproval("ap1"), "dev:@1.%2"), withTmux(badgeAsk("a2", false), "dev:@1.%3"),
			withTmux(leadApproval("ap3"), "ops:@1.%2"),
			withTmux(leadApproval("ap4"), ""),           // no tmux -> a:ap4
			withTmux(leadApproval("ap5"), "gone:@1.%2"), // session gone -> a:ap5
			withTmux(badgeAsk("t1", true), "dev:@1.%9"), // terminal_only: excluded
			withTmux(unpushed("p1"), "other:@1.%1"),     // unpushed kind: excluded
		}, nil
	}
	te.events.emit("opened", leadApproval("ap1"))
	p := te.waitSends(t, 1)[0].Payload
	keys, ok := keysOf(t, p)
	want := []string{"a:ap4", "a:ap5", "s:c0de01", "s:c0de02"}
	if !ok || strings.Join(keys, ",") != strings.Join(want, ",") {
		t.Fatalf("keys = %v (present %v), want %v: %s", keys, ok, want, p)
	}
	if n, _ := payloadOf(t, p); n != 5 {
		t.Fatalf("open_approvals = %d, want 5", n)
	}
	t.Logf("sample: %s", p)
}

func TestKeys_ReadErrorLeavesThemOut(t *testing.T) {
	te := newTriggerEnv(t, nil)
	te.register(tokA, "en", "mlab")
	te.events.readOpen = func() ([]team.Approval, error) { return nil, errors.New("db gone") }
	te.events.emit("opened", leadApproval("ap1"))
	p := te.waitSends(t, 1)[0].Payload
	if keys, ok := keysOf(t, p); ok {
		t.Fatalf("keys %v present on a failed read: %s", keys, p)
	}
}

func TestKeys_CapAt32KeepsFullCountAndPayloadFits(t *testing.T) {
	te := newTriggerEnv(t, nil)
	te.register(tokA, "en", "mlab")
	var open []team.Approval
	for i := 0; i < 40; i++ {
		open = append(open, withTmux(leadApproval(fmt.Sprintf("ap%02d", i)), ""))
	}
	te.events.readOpen = func() ([]team.Approval, error) { return open, nil }
	big := leadApproval("ap00")
	big.Payload = json.RawMessage(`{"reason":"` + strings.Repeat("long ", 600) + `"}`)
	te.events.emit("opened", big)
	p := te.waitSends(t, 1)[0].Payload
	keys, ok := keysOf(t, p)
	if !ok || len(keys) != 32 || keys[0] != "a:ap00" || keys[31] != "a:ap31" {
		t.Fatalf("keys = %v", keys)
	}
	if n, _ := payloadOf(t, p); n != 40 {
		t.Fatalf("open_approvals = %d, want the full 40", n)
	}
	if len(p) > 4096 {
		t.Fatalf("payload %d bytes", len(p))
	}
}

// Approval pushes carry session_code when the origin's tmux session resolves (#2235), for every pushed kind.
func TestApprovalPushCarriesSessionCode(t *testing.T) {
	selfRelay := leadApproval("k3")
	selfRelay.Kind = team.KindSelfRelay
	selfRelay.Payload = json.RawMessage(`{"used_percentage":80}`)
	memberRelay := leadApproval("k4")
	memberRelay.Kind = team.KindMemberRelay
	memberRelay.Payload = json.RawMessage(`{"lead_title":"L","member_title":"M","used_percentage":80}`)
	kinds := map[string]team.Approval{
		"lead": leadApproval("k1"), "hook_ask": badgeAsk("k2", false), "self_relay": selfRelay, "member_relay": memberRelay,
	}
	for name, a := range kinds {
		for _, resolvable := range []bool{true, false} {
			t.Run(fmt.Sprintf("%s/%v", name, resolvable), func(t *testing.T) {
				te := newTriggerEnv(t, nil)
				if resolvable {
					te.mod.codeOf = fakeCodes(map[string]string{"dev": "c0de01"})
				} else {
					te.mod.codeOf = fakeCodes(nil)
				}
				te.register(tokA, "en", "mlab")
				te.events.emit("opened", a)
				p := te.waitSends(t, 1)[0].Payload
				var pl struct {
					Purdex map[string]any `json:"purdex"`
				}
				_ = json.Unmarshal([]byte(p), &pl)
				got, has := pl.Purdex["session_code"]
				if resolvable && got != "c0de01" || !resolvable && has {
					t.Fatalf("session_code = %v (present %v), resolvable %v: %s", got, has, resolvable, p)
				}
			})
		}
	}
}
