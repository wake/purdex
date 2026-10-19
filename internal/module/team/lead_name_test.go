package teammod

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	ipeers "github.com/wake/purdex/internal/peers"
	"github.com/wake/purdex/internal/team"
)

// fakeNames is the NameService of these tests: it records every call in order, and its hooks let a test act inside a rename.
type fakeNames struct {
	mu      sync.Mutex
	calls   []string
	cands   []ipeers.LeadCandidate
	candErr error
	// rename, when set, runs first in RenameForLead and may fail it; old is the name a successful rename reports.
	rename func(sid, code string) error
	old    string
	// undoErr is what UndoRenameForLead returns; recordErr what RecordLead returns.
	undoErr, recordErr error
}

func (n *fakeNames) note(s string) {
	n.mu.Lock()
	n.calls = append(n.calls, s)
	n.mu.Unlock()
}

func (n *fakeNames) log() []string {
	n.mu.Lock()
	defer n.mu.Unlock()
	return append([]string(nil), n.calls...)
}

func (n *fakeNames) count(prefix string) int {
	c := 0
	for _, s := range n.log() {
		if strings.HasPrefix(s, prefix) {
			c++
		}
	}
	return c
}

func (n *fakeNames) LeadCandidates(_ context.Context, sid string) ([]ipeers.LeadCandidate, error) {
	n.note("cands:" + sid)
	return n.cands, n.candErr
}

func (n *fakeNames) RenameForLead(_ context.Context, sid, code string) (string, error) {
	n.note("rename:" + sid + ":" + code)
	if n.rename != nil {
		if err := n.rename(sid, code); err != nil {
			return "", err
		}
	}
	return n.old, nil
}

func (n *fakeNames) UndoRenameForLead(_ context.Context, sid, old string) error {
	n.note("undo:" + sid + ":" + old)
	return n.undoErr
}

func (n *fakeNames) RecordLead(_ context.Context, sid string) error {
	n.note("record:" + sid)
	return n.recordErr
}

func (n *fakeNames) RecordPairCodes(context.Context, string, string) error { return nil }
func (n *fakeNames) CodeOf(context.Context, string) (string, bool)         { return "", false }
func (n *fakeNames) ReserveMember(context.Context, string, string, string, time.Time) (string, error) {
	return "", nil
}
func (n *fakeNames) ReserveExact(context.Context, string, string, string, time.Time) error {
	return nil
}
func (n *fakeNames) ReleaseReservation(string) {}

var _ ipeers.NameService = (*fakeNames)(nil)

var leadCands = []ipeers.LeadCandidate{
	{Code: "adam", Keep: true, Name: "Adam", Kind: team.LeadNameKeep},
	{Code: "kenji", Name: "Kenji", Kind: team.LeadNameFormer, LastAt: 5},
	{Code: "emma", Name: "Emma", Kind: team.LeadNameOther},
}

// withNames registers a fake name service whose old name is "purdex-88-b8" (the lead sid-1's current one).
func withNames(f *fixture) *fakeNames {
	n := &fakeNames{cands: leadCands, old: "purdex-88-" + ipeers.RefID("sid-1")[1:3]}
	f.core.Registry.Register(ipeers.NameServiceKey, n)
	return n
}

var leadAppClient = team.Client{Kind: "app", Label: "Purdex.app @ air26"}

func (f *fixture) decideLead(id, code string) (int, []byte) {
	return f.do(http.MethodPost, "/api/team/approvals/"+id+"/decide",
		team.DecideRequest{Decision: "approve", Grant: &team.Grant{LeadName: code}, Client: leadAppClient})
}

func TestLeadName_RenameComesBeforeTheTeamRow(t *testing.T) {
	f := newFixture(t)
	n := withNames(f)
	f.create(uid(1))
	n.rename = func(sid, code string) error {
		if _, found, err := f.m.store.LiveTeamByLead(sid); err != nil || found {
			t.Errorf("the team row exists before the rename: found=%v err=%v", found, err)
		}
		return nil
	}
	code, body := f.decideLead(uid(1), "kenji")
	a := decodeApproval(t, body)
	if code != 200 || a.State != team.StateApproved || a.Grant == nil || a.Grant.LeadName != "kenji" {
		t.Fatalf("decide: %d %s", code, body)
	}
	if _, found, _ := f.m.store.LiveTeamByLead("sid-1"); !found {
		t.Fatal("no team after the approval")
	}
	if got := n.log(); len(got) < 3 || got[1] != "rename:sid-1:kenji" || got[2] != "record:sid-1" {
		t.Fatalf("calls = %v (want rename then record)", got)
	}
}

// Every candidate the list offers can be picked, the "keep" one included (the spec's code shape is 3-12 lowercase letters).
func TestLeadName_EveryCandidateIsAccepted(t *testing.T) {
	for _, c := range leadCands {
		t.Run(c.Code, func(t *testing.T) {
			f := newFixture(t)
			withNames(f)
			f.create(uid(1))
			if code, body := f.decideLead(uid(1), c.Code); code != 200 {
				t.Fatalf("decide with %q: %d %s", c.Code, code, body)
			}
		})
	}
}

func TestLeadName_NoLeadNameStillRecordsTheLead(t *testing.T) {
	f := newFixture(t)
	n := withNames(f)
	f.create(uid(1))
	if code, body := f.do(http.MethodPost, "/api/team/approvals/"+uid(1)+"/decide", team.DecideRequest{Decision: "approve", Client: leadAppClient}); code != 200 {
		t.Fatalf("decide: %d %s", code, body)
	}
	if n.count("rename:") != 0 || n.count("record:sid-1") != 1 {
		t.Fatalf("calls = %v", n.log())
	}
}

func TestLeadName_RecordFailureOnlyLogs(t *testing.T) {
	f := newFixture(t)
	n := withNames(f)
	n.recordErr = errors.New("meta.db locked")
	f.create(uid(1))
	if code, body := f.decideLead(uid(1), "kenji"); code != 200 {
		t.Fatalf("decide: %d %s", code, body)
	}
}

func TestLeadName_Taken(t *testing.T) {
	f := newFixture(t)
	n := withNames(f)
	n.rename = func(string, string) error { return ipeers.ErrNameTaken }
	f.create(uid(1))
	code, body := f.decideLead(uid(1), "kenji")
	e := decodeErr(t, body)
	if code != http.StatusConflict || e.Error != team.ErrNameTaken || len(e.LeadNames) != 3 || e.LeadNames[1].Code != "kenji" || e.LeadNames[1].Kind != team.LeadNameFormer {
		t.Fatalf("decide: %d %s", code, body)
	}
	if row, _, _ := f.m.store.Get(uid(1)); row.State != team.StateOpen {
		t.Fatalf("the request is %s, want open", row.State)
	}
	if n.count("undo:") != 0 {
		t.Fatalf("an undo after a rename that did nothing: %v", n.log())
	}
}

// When the list cannot be refreshed after a name_taken, the body carries the one the request was made with.
func TestLeadName_TakenWithUnreadableListFallsBackToTheStoredOne(t *testing.T) {
	f := newFixture(t)
	n := withNames(f)
	f.create(uid(1))
	n.rename = func(string, string) error { n.candErr = errors.New("meta.db locked"); return ipeers.ErrNameTaken }
	code, body := f.decideLead(uid(1), "kenji")
	e := decodeErr(t, body)
	if code != http.StatusConflict || e.Error != team.ErrNameTaken || len(e.LeadNames) != 3 || e.LeadNames[0].Code != "adam" {
		t.Fatalf("decide: %d %s", code, body)
	}
}

func TestLeadName_UnknownAndMalformedCodes(t *testing.T) {
	f := newFixture(t)
	n := withNames(f)
	n.rename = func(string, string) error { return ipeers.ErrNameUnknown }
	f.create(uid(1))
	if code, body := f.decideLead(uid(1), "nobody"); code != 400 || decodeErr(t, body).Error != team.ErrBadRequest {
		t.Fatalf("unknown code: %d %s", code, body)
	}
	for _, bad := range []string{"Kenji", "ke", "kenji-x", "abcdefghijklm", "k3nji"} {
		if code, body := f.decideLead(uid(1), bad); code != 400 {
			t.Fatalf("code %q: %d %s", bad, code, body)
		}
	}
	if n.count("rename:") != 1 {
		t.Fatalf("a malformed code reached the name service: %v", n.log())
	}
}

func TestLeadName_NoNameServiceIs400(t *testing.T) {
	f := newFixture(t)
	f.create(uid(1))
	code, body := f.decideLead(uid(1), "kenji")
	if code != 400 || decodeErr(t, body).Error != team.ErrBadRequest {
		t.Fatalf("decide: %d %s", code, body)
	}
	if row, _, _ := f.m.store.Get(uid(1)); row.State != team.StateOpen {
		t.Fatalf("the request is %s, want open", row.State)
	}
}

func TestLeadName_DenyIgnoresTheName(t *testing.T) {
	f := newFixture(t)
	n := withNames(f)
	f.create(uid(1))
	code, _ := f.do(http.MethodPost, "/api/team/approvals/"+uid(1)+"/decide",
		team.DecideRequest{Decision: "deny", Grant: &team.Grant{LeadName: "kenji"}, Client: leadAppClient})
	if code != 200 || n.count("rename:") != 0 || n.count("record:") != 0 {
		t.Fatalf("deny: %d calls=%v", code, n.log())
	}
}

// A decide that loses the close after the rename gives the name back; whatever the undo answers, the decide's own answer stands.
func TestLeadName_LostCloseUndoesTheRename(t *testing.T) {
	for name, undoErr := range map[string]error{"nothing to undo": nil, "old name taken": ipeers.ErrNameTaken, "other error": errors.New("meta.db locked")} {
		t.Run(name, func(t *testing.T) {
			f := newFixture(t)
			n := withNames(f)
			n.undoErr = undoErr
			f.create(uid(1))
			n.rename = func(string, string) error { // someone else's close lands between the rename and ours
				_, won, err := f.m.store.CloseIfOpen(uid(1), Close{State: team.StateDenied, DecidedAt: 5})
				if err != nil || !won {
					t.Errorf("the other close: won=%v err=%v", won, err)
				}
				return nil
			}
			code, body := f.decideLead(uid(1), "kenji")
			if code != http.StatusConflict || decodeErr(t, body).Error != team.ErrAlreadyDecided {
				t.Fatalf("decide: %d %s", code, body)
			}
			if got := n.log(); n.count("undo:sid-1:"+n.old) != 1 || n.count("record:") != 0 {
				t.Fatalf("calls = %v", got)
			}
		})
	}
}

func TestLeadName_CloseErrorUndoesTheRename(t *testing.T) {
	for name, undoErr := range map[string]error{"nothing to undo": nil, "old name taken": ipeers.ErrNameTaken, "other error": errors.New("meta.db locked")} {
		t.Run(name, func(t *testing.T) {
			f := newFixture(t)
			n := withNames(f)
			n.undoErr = undoErr
			f.create(uid(1))
			n.rename = func(string, string) error {
				_, err := f.m.store.db.Exec(`DROP TABLE teams`)
				return err
			}
			code, body := f.decideLead(uid(1), "kenji")
			if code != http.StatusInternalServerError {
				t.Fatalf("decide: %d %s", code, body)
			}
			if n.count("undo:sid-1:"+n.old) != 1 || n.count("record:") != 0 {
				t.Fatalf("calls = %v", n.log())
			}
		})
	}
}

// Two decides with different names on one request: the lock lets the first rename and close, the second finds the request closed
// and renames nothing.
func TestLeadName_ConcurrentDecidesRenameOnce(t *testing.T) {
	f := newFixture(t)
	n := withNames(f)
	f.create(uid(1))
	var wg sync.WaitGroup
	codes := make([]int, 2)
	for i, c := range []string{"kenji", "emma"} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			codes[i], _ = f.decideLead(uid(1), c)
		}()
	}
	wg.Wait()
	if !(codes[0] == 200 && codes[1] == 409) && !(codes[0] == 409 && codes[1] == 200) {
		t.Fatalf("codes = %v", codes)
	}
	if n.count("rename:") != 1 || n.count("undo:") != 0 {
		t.Fatalf("calls = %v", n.log())
	}
}

func TestLeadName_SweeperCloseWaitsForTheApprovalLock(t *testing.T) {
	f := newFixture(t)
	f.create(uid(1))
	unlock := f.m.approvalLocks.lock(uid(1))
	done := make(chan struct{})
	go func() {
		_, _, _ = f.m.closeExpired(uid(1), 9_999_999_999, team.StateTimeout)
		close(done)
	}()
	select {
	case <-done:
		t.Fatal("closeExpired ran while the approval lock was held")
	case <-time.After(100 * time.Millisecond):
	}
	unlock()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("closeExpired did not run after the unlock")
	}
}

func TestLeadName_NoticeToTheRenamedSession(t *testing.T) {
	f := newFixture(t)
	n := withNames(f)
	f.create(uid(1))
	if code, body := f.decideLead(uid(1), "kenji"); code != 200 {
		t.Fatalf("decide: %d %s", code, body)
	}
	alias, _ := f.m.selfHost()
	deadline := time.Now().Add(5 * time.Second)
	for {
		f.sender.mu.Lock()
		got := append([]ipeers.SendRequest(nil), f.sender.selfNotices...)
		f.sender.mu.Unlock()
		if len(got) == 1 {
			want := fmt.Sprintf("[pdx team] 你的地址改成 %s/purdex-88-kenji，舊地址 %s/%s 仍可用", alias, alias, n.old)
			if got[0].Text != want || got[0].To != alias+"/"+ipeers.RefID("sid-1") {
				t.Fatalf("notice = %+v, want text %q", got[0], want)
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("no notice sent: %v", got)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestLeadName_NoticeFailureDoesNotFailTheDecide(t *testing.T) {
	f := newFixture(t)
	withNames(f)
	f.sender.err = errors.New("inbox gone")
	f.create(uid(1))
	if code, body := f.decideLead(uid(1), "kenji"); code != 200 {
		t.Fatalf("decide: %d %s", code, body)
	}
}

func TestLeadName_CreateFillsTheNames(t *testing.T) {
	leadNamesOf := func(f *fixture) ([]team.LeadName, bool) {
		a := f.create(uid(1))
		var p team.LeadPayload
		if err := json.Unmarshal(a.Payload, &p); err != nil {
			t.Fatal(err)
		}
		return p.LeadNames, p.LeadNames == nil
	}
	t.Run("with the service", func(t *testing.T) {
		f := newFixture(t)
		withNames(f)
		got, isNil := leadNamesOf(f)
		if isNil || len(got) != 3 || got[0].Code != "adam" || got[0].Kind != team.LeadNameKeep {
			t.Fatalf("lead_names = %+v", got)
		}
	})
	t.Run("no service is null", func(t *testing.T) {
		f := newFixture(t)
		if _, isNil := leadNamesOf(f); !isNil {
			t.Fatal("lead_names is not null without a name service")
		}
	})
	t.Run("read error is null and the request is created", func(t *testing.T) {
		f := newFixture(t)
		n := withNames(f)
		n.candErr = errors.New("meta.db locked")
		if _, isNil := leadNamesOf(f); !isNil {
			t.Fatal("lead_names is not null after a read error")
		}
	})
	t.Run("no persona is an empty list", func(t *testing.T) {
		f := newFixture(t)
		n := withNames(f)
		n.cands = nil
		got, isNil := leadNamesOf(f)
		if isNil || len(got) != 0 {
			t.Fatalf("lead_names = %+v nil=%v", got, isNil)
		}
	})
}

func TestLeadName_Route(t *testing.T) {
	path := func(id string) string { return "/api/team/approvals/" + id + "/lead-names" }
	t.Run("unknown id", func(t *testing.T) {
		f := newFixture(t)
		withNames(f)
		if code, body := f.do(http.MethodGet, path("nope"), nil); code != 404 || decodeErr(t, body).Error != team.ErrNotFound {
			t.Fatalf("%d %s", code, body)
		}
	})
	t.Run("closed", func(t *testing.T) {
		f := newFixture(t)
		withNames(f)
		f.create(uid(1))
		f.do(http.MethodPost, "/api/team/approvals/"+uid(1)+"/decide", team.DecideRequest{Decision: "deny", Client: leadAppClient})
		if code, body := f.do(http.MethodGet, path(uid(1)), nil); code != 409 || decodeErr(t, body).Error != team.ErrNotOpen {
			t.Fatalf("%d %s", code, body)
		}
	})
	t.Run("not a lead request", func(t *testing.T) {
		f := newFixture(t)
		withNames(f)
		f.create(uid(1))
		if _, err := f.m.store.db.Exec(`UPDATE approval_requests SET kind = 'self_relay' WHERE id = ?`, uid(1)); err != nil {
			t.Fatal(err)
		}
		if code, body := f.do(http.MethodGet, path(uid(1)), nil); code != 400 || decodeErr(t, body).Error != team.ErrBadRequest {
			t.Fatalf("%d %s", code, body)
		}
	})
	t.Run("no name service", func(t *testing.T) {
		f := newFixture(t)
		f.create(uid(1))
		if code, body := f.do(http.MethodGet, path(uid(1)), nil); code != 503 || decodeErr(t, body).Error != team.ErrNotReady {
			t.Fatalf("%d %s", code, body)
		}
	})
	t.Run("read error is not an empty list", func(t *testing.T) {
		f := newFixture(t)
		n := withNames(f)
		f.create(uid(1))
		n.candErr = errors.New("meta.db locked")
		if code, body := f.do(http.MethodGet, path(uid(1)), nil); code != 503 || decodeErr(t, body).Error != team.ErrNotReady {
			t.Fatalf("%d %s", code, body)
		}
	})
	t.Run("the list now", func(t *testing.T) {
		f := newFixture(t)
		n := withNames(f)
		f.create(uid(1))
		n.cands = leadCands[1:]
		code, body := f.do(http.MethodGet, path(uid(1)), nil)
		var r team.LeadNamesResponse
		if err := json.Unmarshal(body, &r); err != nil || code != 200 || len(r.LeadNames) != 2 || r.LeadNames[0].Code != "kenji" {
			t.Fatalf("%d %s (%v)", code, body, err)
		}
	})
}
