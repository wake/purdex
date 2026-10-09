package teammod

import (
	"net/http"
	"testing"
	"time"

	"github.com/wake/purdex/internal/push"
	"github.com/wake/purdex/internal/team"
)

// pushKinds is the kinds of the `opened` ops in ops that the push module would turn into a notification: the same
// conversion and filter the module applies, over what the REAL team paths publish (push plan PU-2.6).
func pushKinds(ops []feedOp) []string {
	out := []string{}
	for _, o := range ops {
		if o.op != "opened" {
			continue
		}
		a := push.Approval{ID: o.a.ID, Kind: string(o.a.Kind), Payload: o.a.Payload,
			Origin: push.ApprovalOrigin{Title: o.a.Origin.Title, Name: o.a.Origin.Name, Ref: o.a.Origin.Ref}}
		if c, ok := push.ApprovalContent(a, "mlab", "en"); ok {
			out = append(out, c.Kind)
		}
	}
	return out
}

// settle gives the subscriber goroutine time to deliver what was published.
func settle() { time.Sleep(150 * time.Millisecond) }

func TestPushTriggers_ALeadRequestWithUnattendedOffIsPushed(t *testing.T) {
	f := newFixture(t)
	c := &collector{}
	_, unsub := f.m.SubscribeApprovals(c.fn)
	defer unsub()
	f.createFrom(uid(1), "/tmp/10.sock")
	settle()
	if got := pushKinds(c.got()); len(got) != 1 || got[0] != "lead" {
		t.Fatalf("pushed = %v, want [lead]", got)
	}
}

// Unattended mode approves a request at creation: the row is born approved and only a `closed` is published, so nothing
// is pushed - for a lead request and for a self relay alike.
func TestPushTriggers_UnattendedAutoApprovalsPushNothing(t *testing.T) {
	f := newFixture(t)
	f.realUnattended()
	f.switchTo(true)
	c := &collector{}
	_, unsub := f.m.SubscribeApprovals(c.fn)
	defer unsub()
	f.createFrom(uid(1), "/tmp/10.sock") // createApprovedLead
	f.begin("sid-2")                     // beginApproved
	settle()
	if len(c.got()) == 0 {
		t.Fatal("expected the closed ops of the auto-approved rows")
	}
	if got := pushKinds(c.got()); len(got) != 0 {
		t.Fatalf("pushed = %v, want nothing (ops %+v)", got, c.got())
	}
}

func TestPushTriggers_ASelfRelayRequestIsPushed(t *testing.T) {
	f := newFixture(t)
	c := &collector{}
	_, unsub := f.m.SubscribeApprovals(c.fn)
	defer unsub()
	f.begin("sid-2")
	settle()
	if got := pushKinds(c.got()); len(got) != 1 || got[0] != "self_relay" {
		t.Fatalf("pushed = %v, want [self_relay]", got)
	}
}

func TestPushTriggers_OnlyAnAnswerableAskIsPushed(t *testing.T) {
	f := newFixture(t)
	c := &collector{}
	_, unsub := f.m.SubscribeApprovals(c.fn)
	defer unsub()
	f.askBegin("toolu_1")           // answerable hook_ask (a mod session)
	f.askBeginPermission("toolu_2") // hook_permission
	f.openTerminalOnly("toolu_3")   // a hook_ask the phone cannot answer
	settle()
	opened := 0
	for _, o := range c.got() {
		if o.op == "opened" {
			opened++
		}
	}
	if opened != 3 {
		t.Fatalf("opened ops = %d, want all three to reach the feed", opened)
	}
	if got := pushKinds(c.got()); len(got) != 1 || got[0] != "hook_ask" {
		t.Fatalf("pushed = %v, want only the answerable hook_ask", got)
	}
}

// `closed` is never a push (R7).
func TestPushTriggers_ClosedIsNotPushed(t *testing.T) {
	f := newFixture(t)
	c := &collector{}
	_, unsub := f.m.SubscribeApprovals(c.fn)
	defer unsub()
	a := f.createFrom(uid(1), "/tmp/10.sock")
	f.do(http.MethodDelete, "/api/team/approvals/"+a.ID, nil)
	settle()
	if ops := c.got(); len(ops) != 2 || ops[1].op != "closed" {
		t.Fatalf("ops = %+v", ops)
	}
	if got := pushKinds(c.got()[1:]); len(got) != 0 {
		t.Fatalf("pushed = %v after the close", got)
	}
}

var _ = team.KindLead
