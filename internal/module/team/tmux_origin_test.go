package teammod

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/wake/purdex/internal/module/session"
	"github.com/wake/purdex/internal/team"
	"github.com/wake/purdex/internal/tmux"
)

// ---- the tmux generation of a roster seat and the session code of a fresh half ----

func TestOriginPane(t *testing.T) {
	for in, want := range map[string]string{
		"tm-1:@1.%2": "%2", "a:b:@1.%12": "%12", "": "", "tm-1": "", "tm-1:@1": "", "tm-1:@1.%": "", "tm-1:@1.%x": "", "x.%1y": "",
	} {
		if got := originPane(in); got != want {
			t.Errorf("originPane(%q) = %q, want %q", in, got, want)
		}
	}
}

// codeOf is the session code /api/sessions gives the tmux session with id "$n".
func codeOf(t *testing.T, id string) string {
	t.Helper()
	c, err := session.EncodeSessionID(id)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func (f *fixture) showIn(sid, ref, tmuxOrigin string) {
	f.origins.show(team.Origin{SessionID: sid, Ref: ref, Name: "reg-" + sid, Address: "self/" + ref, Tmux: tmuxOrigin, PID: 42})
}

// A fresh half carries the code of the tmux session its CC session runs in; none outside tmux; a session that moved to another tmux
// session shows the new code on the next change.
// Mutation gates: code left out → red; the code of the wrong session → red; a list that ignores the code for "changed" → red.
func TestFreshSurface_CodeFollowsTheTmuxSession(t *testing.T) {
	f := newFixture(t)
	f.tmux.AddSession("sess-a", "/") // $0, pane %0
	f.tmux.AddSession("sess-b", "/") // $1, pane %1
	f.showIn("sid-1", "_abc123", "sess-a:@1.%0")
	w := f.watchFresh()
	w.drain()
	f.freshHalfWithFile("sid-1")

	v := f.getFresh()
	if len(v.Sessions) != 1 || v.Sessions[0].Code != codeOf(t, "$0") {
		t.Fatalf("fresh = %+v, want code %s", v.Sessions, codeOf(t, "$0"))
	}
	if evs := w.drain(); len(evs) != 1 || evs[0].Sessions[0].Code != codeOf(t, "$0") {
		t.Fatalf("event = %+v, want a changed with code %s", evs, codeOf(t, "$0"))
	}

	f.showIn("sid-1", "_abc123", "sess-b:@1.%1") // the CC session is in another tmux session now
	f.m.freshSync(true)
	if evs := w.drain(); len(evs) != 1 || evs[0].Op != "changed" || evs[0].Sessions[0].Code != codeOf(t, "$1") {
		t.Fatalf("after the move: %+v, want a changed with code %s", evs, codeOf(t, "$1"))
	}

	f.showIn("sid-1", "_abc123", "") // not in tmux
	code, body := f.do(http.MethodGet, team.RelayFreshRoute, nil)
	if code != http.StatusOK || strings.Contains(string(body), `"code"`) {
		t.Fatalf("outside tmux: %d %s, want no code key", code, body)
	}
}

// A fresh half whose tmux does not answer still shows, without a code.
func TestFreshSurface_NoCodeWhenTmuxDoesNotAnswer(t *testing.T) {
	f := newFixture(t)
	f.tmux.AddSession("sess-a", "/")
	f.showIn("sid-1", "_abc123", "sess-a:@1.%0")
	f.freshHalfWithFile("sid-1")
	f.tmux.SetPaneIdentityErr(errors.New("tmux: server busy"))
	v := f.getFresh()
	if len(v.Sessions) != 1 || v.Sessions[0].Code != "" {
		t.Fatalf("fresh = %+v, want the half without a code", v.Sessions)
	}
}

// The roster seat carries the tmux generation of a live session; the server restarting gives the next roster the new one; a session
// outside tmux carries none; a seat that is not live carries what team.db recorded.
// Mutation gates: tmux_instance left out → red; the stored value used for a live session → red; shown for a session outside tmux → red.
func TestRoster_TmuxInstanceOfASeat(t *testing.T) {
	f := newFixture(t)
	f.tmux.AddSession("sess-a", "/") // $0, pane %0
	f.tmux.AddSession("sess-b", "/") // $1, pane %1
	f.tmux.SetInstance("100:1700000000")
	f.memberTeam("")
	f.showIn("sid-1", "_abc123", "sess-a:@1.%0")
	f.showIn("sid-m1", "_mem001", "sess-b:@1.%1")

	r := f.getRoster()
	if got := r.Teams[0].Lead.TmuxInstance; got != "100:1700000000" {
		t.Fatalf("lead tmux_instance = %q", got)
	}
	if got := r.Teams[0].Members[0].TmuxInstance; got != "100:1700000000" {
		t.Fatalf("member tmux_instance = %q", got)
	}

	f.tmux.SetInstance("200:1700000999") // tmux restarted: a same-named session is of another generation
	r = f.getRoster()
	if got := r.Teams[0].Members[0].TmuxInstance; got != "200:1700000999" {
		t.Fatalf("after the restart member tmux_instance = %q, want the new generation", got)
	}

	f.showIn("sid-m1", "_mem001", "") // not in tmux
	r = f.getRoster()
	raw, _ := json.Marshal(r.Teams[0].Members[0])
	if r.Teams[0].Members[0].TmuxInstance != "" || strings.Contains(string(raw), "tmux_instance") {
		t.Fatalf("a session outside tmux: %s", raw)
	}
}

// A seat the registry does not list shows the generation team.db recorded for it, and none without a tmux session name.
func TestRoster_StoredSeatShowsTheRecordedGeneration(t *testing.T) {
	f := newFixture(t)
	f.approveLead(uid(1))
	row := newMember("op-m1", uid(1), "sid-m1", "_mem001", f.clock.Load())
	row.TmuxInstance = "7:77"
	if err := f.m.store.InsertMember(row); err != nil {
		t.Fatal(err)
	}
	r := f.getRoster()
	if got := r.Teams[0].Members[0]; got.Live || got.TmuxInstance != "7:77" {
		t.Fatalf("stored seat = live %v instance %q, want the recorded 7:77", got.Live, got.TmuxInstance)
	}
	if storedInstance("", "7:77") != "" {
		t.Fatal("an instance without a tmux session name was shown")
	}
}

// hangingTmux is a tmux that never answers a pane read until its context ends.
type hangingTmux struct {
	tmuxOps
}

func (h *hangingTmux) PaneIdentity(ctx context.Context, _, _ string) (tmux.PaneIdentity, error) {
	<-ctx.Done()
	return tmux.PaneIdentity{}, ctx.Err()
}

// A tmux that does not answer costs the fresh list ONE budget, not one per session: the list comes back with no codes after about
// originProbeTimeout, whatever the number of halves. Mutation gate: a budget per session → the elapsed time is 3x → red.
func TestFreshSurface_SilentTmuxCostsOneBudgetForTheWholeList(t *testing.T) {
	f := newFixture(t)
	f.m.tmux = &hangingTmux{tmuxOps: f.m.tmux}
	for _, sid := range []string{"sid-1", "sid-2", "sid-3"} {
		f.showIn(sid, "_"+sid, "sess:@1.%0")
		f.freshHalfWithFile(sid)
	}
	start := time.Now()
	v := f.getFresh()
	if len(v.Sessions) != 3 {
		t.Fatalf("sessions = %+v, want the three halves", v.Sessions)
	}
	if took := time.Since(start); took > originProbeTimeout+time.Second {
		t.Fatalf("the list took %v with a silent tmux, want about one budget (%v)", took, originProbeTimeout)
	}
	for _, s := range v.Sessions {
		if s.Code != "" {
			t.Fatalf("a code from a silent tmux: %+v", s)
		}
	}
}
