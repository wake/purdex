package push

import (
	"encoding/json"
	"regexp"
	"strconv"
	"testing"
	"time"

	"github.com/wake/purdex/internal/team"
)

var reOpen = regexp.MustCompile(`"open_approvals":(\d+)`)

// openCountOf reads the integer open_approvals out of a payload and checks mutable-content too.
func openCountOf(t *testing.T, payload string) int {
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
	m := reOpen.FindStringSubmatch(payload)
	if m == nil {
		t.Fatalf("open_approvals missing or not an integer: %s", payload)
	}
	n, _ := strconv.Atoi(m[1])
	return n
}

func badgeAsk(id string, terminalOnly bool) team.Approval {
	a := leadApproval(id)
	a.Kind = team.KindHookAsk
	a.Payload = json.RawMessage(`{"terminal_only":` + strconv.FormatBool(terminalOnly) + `,"questions":[{"question":"q?"}]}`)
	return a
}

func TestBadge_CountIncludesTheApprovalBeingPushed(t *testing.T) {
	te := newTriggerEnv(t, nil)
	te.register(tokA, "en", "mlab")
	te.events.emit("opened", leadApproval("ap1"))
	if n := openCountOf(t, te.waitSends(t, 1)[0].Payload); n != 1 {
		t.Fatalf("open_approvals = %d, want 1", n)
	}
	te.events.emit("opened", leadApproval("ap2"))
	if n := openCountOf(t, te.waitSends(t, 2)[1].Payload); n != 2 {
		t.Fatalf("open_approvals = %d, want 2", n)
	}
}

func TestBadge_ClosedLeavesTheCount(t *testing.T) {
	te := newTriggerEnv(t, nil)
	te.register(tokA, "en", "mlab")
	te.events.emit("opened", leadApproval("ap1"))
	te.events.emit("opened", leadApproval("ap2"))
	te.waitSends(t, 2)
	te.events.emit("closed", leadApproval("ap1"))
	te.events.emit("opened", leadApproval("ap3"))
	if n := openCountOf(t, te.waitSends(t, 3)[2].Payload); n != 2 { // ap2, ap3
		t.Fatalf("open_approvals = %d, want 2", n)
	}
}

func TestBadge_UnpushedKindsAndTerminalOnlyDoNotCount(t *testing.T) {
	te := newTriggerEnv(t, nil)
	te.register(tokA, "en", "mlab")
	perm := leadApproval("p1")
	perm.Kind = team.KindHookPermission
	adopt := leadApproval("p2")
	adopt.Kind = team.KindAdopt
	te.events.emit("opened", perm)
	te.events.emit("opened", adopt)
	te.events.emit("opened", badgeAsk("p3", true))
	te.events.emit("opened", badgeAsk("a1", false))
	if n := openCountOf(t, te.waitSends(t, 1)[0].Payload); n != 1 {
		t.Fatalf("open_approvals = %d, want 1 (only the answerable ask)", n)
	}
}

func TestBadge_SeededFromTheStartSnapshotAndOnAgentPushes(t *testing.T) {
	e := newAgentEnv(t, time.Hour, leadApproval("s1"), badgeAsk("s2", false), badgeAsk("s3", true))
	e.device(tokA, "en", "mlab", tabsOf("c1"))
	e.feed.emit(nev("c1", "PdxStop", "idle", stopDetail("done")))
	c := e.waitSends(t, 1)[0]
	if n := openCountOf(t, c.Payload); n != 2 {
		t.Fatalf("agent push open_approvals = %d, want 2 (snapshot minus terminal_only)", n)
	}
	t.Logf("agent push: %s", c.Payload)
	e.events.emit("closed", leadApproval("s1"))
	e.feed.emit(nev("c1", "PdxStop", "idle", stopDetail("again")))
	if n := openCountOf(t, e.waitSends(t, 2)[1].Payload); n != 1 {
		t.Fatalf("after close open_approvals = %d, want 1", n)
	}
}

func TestBadge_ApprovalPushPayloadLogged(t *testing.T) {
	te := newTriggerEnv(t, nil)
	te.register(tokA, "en", "mlab")
	te.events.emit("opened", leadApproval("ap1"))
	t.Logf("approval push: %s", te.waitSends(t, 1)[0].Payload)
}
