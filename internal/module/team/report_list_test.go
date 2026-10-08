package teammod

import (
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/wake/purdex/internal/team"
)

func (f *fixture) listReports(inbox, query string) (int, team.ReportList, team.APIError) {
	f.t.Helper()
	return call[team.ReportList](f, http.MethodGet, "/api/team/reports?origin_inbox="+url.QueryEscape(inbox)+query, "")
}

func reportIDs(rs []team.Report) []string {
	out := []string{}
	for _, r := range rs {
		out = append(out, r.ID)
	}
	return out
}

// The lead lists its own team's reports (one task if it names one); a member
// the reports of one of its own tasks. Newest first. Whatever a caller may not
// see answers what a task that never existed answers.
func TestReports_ListScopedToTeamAndOwner(t *testing.T) {
	w := newTaskWorld(t)
	t1 := w.mustTask(leadInbox, w.ma.Ref, "a's", nil)    // 000000-1
	t2 := w.mustTask(leadInbox, w.mb.Ref, "b's", nil)    // 000000-2
	x1 := w.mustTask(lead2, w.mx.Ref, "team two's", nil) // 000000-1 of team 2
	post := func(inbox string, n int, k team.ReportKind, task string) {
		w.clock.Add(1)
		w.mustReport(inbox, rreq(n, k, task))
	}
	post(maInbox, 1, team.ReportAck, t1.ID)
	post(mbInbox, 2, team.ReportAck, t2.ID)
	post(mxInbox, 3, team.ReportAck, x1.ID)
	post(maInbox, 4, team.ReportProgress, t1.ID)

	ids := func(inbox, query string) []string {
		t.Helper()
		code, out, e := w.listReports(inbox, query)
		if code != 200 {
			t.Fatalf("list %s %q = %d %+v", inbox, query, code, e)
		}
		return reportIDs(out.Reports)
	}
	if got, want := ids(leadInbox, ""), []string{reportID(4), reportID(2), reportID(1)}; !slices.Equal(got, want) {
		t.Fatalf("lead, all = %v, want %v (its team only, newest first)", got, want)
	}
	if got, want := ids(leadInbox, "&task="+t1.ID), []string{reportID(4), reportID(1)}; !slices.Equal(got, want) {
		t.Fatalf("lead, task 1 = %v, want %v", got, want)
	}
	if got, want := ids(lead2, ""), []string{reportID(3)}; !slices.Equal(got, want) {
		t.Fatalf("lead of team 2 = %v, want %v", got, want)
	}
	if got, want := ids(maInbox, "&task="+t1.ID), []string{reportID(4), reportID(1)}; !slices.Equal(got, want) {
		t.Fatalf("member, own task = %v, want %v", got, want)
	}

	// The wire view of a report: the task's display id and the reporting member.
	_, out, _ := w.listReports(maInbox, "&task="+t1.ID)
	r := out.Reports[0]
	if r.Task != t1.ID || r.Member.Ref != w.ma.Ref || r.Kind != team.ReportProgress || r.Summary != "summary progress" {
		t.Fatalf("report view = %+v", r)
	}

	// Refusals. A nonexistent task is the baseline every unseen one must match.
	code, raw := w.do(http.MethodGet, "/api/team/reports?origin_inbox="+url.QueryEscape(maInbox)+"&task=000000-999", "")
	wantCode, wantBody := code, string(raw)
	if wantCode != http.StatusConflict || !strings.Contains(wantBody, team.ErrTaskNotFound) {
		t.Fatalf("a nonexistent task = %d %s", wantCode, wantBody)
	}
	for _, c := range []struct{ name, inbox, task string }{
		{"member, another member's task", maInbox, t2.ID},
		{"member, another team's id", maInbox, "abcdef-1"},
		{"member of team 2, team 1's seq 2", mxInbox, t2.ID},
		{"member, malformed", maInbox, "x-1"},
		{"lead, a nonexistent task", leadInbox, "000000-999"},
		{"lead, another team's id", leadInbox, "abcdef-1"},
		{"lead, malformed", leadInbox, "000000-01"},
		{"lead of team 2, team 1's seq 2", lead2, t2.ID},
	} {
		t.Run(c.name, func(t *testing.T) {
			code, raw := w.do(http.MethodGet, "/api/team/reports?origin_inbox="+url.QueryEscape(c.inbox)+"&task="+c.task, "")
			if code != wantCode || string(raw) != wantBody {
				t.Fatalf("= %d %s, want exactly %d %s", code, raw, wantCode, wantBody)
			}
		})
	}
	code, _, e := w.listReports(maInbox, "")
	wantErr(t, "member without task", code, e, 400, team.ErrBadRequest)
	if !strings.Contains(e.Detail, "task=<id>") {
		t.Fatalf("detail = %q", e.Detail)
	}
	code, _, e = w.listReports("/tmp/n.sock", "&task="+t1.ID)
	wantErr(t, "no role", code, e, 409, team.ErrNotMember)
	code, _, e = w.listReports("/tmp/99.sock", "")
	wantErr(t, "unknown origin", code, e, 400, team.ErrOriginUnknown)
}

func TestReports_ListSinceAndShape(t *testing.T) {
	w := newTaskWorld(t)
	tk := w.mustTask(leadInbox, w.ma.Ref, "work", nil)
	if code, raw := w.do(http.MethodGet, "/api/team/reports?origin_inbox="+url.QueryEscape(leadInbox), ""); code != 200 || !strings.Contains(string(raw), `"reports":[]`) {
		t.Fatalf("no reports = %d %s, want an empty list, never null", code, raw)
	}
	var at []int64
	for i := 1; i <= 3; i++ {
		w.clock.Add(10)
		at = append(at, w.clock.Load())
		w.mustReport(maInbox, rreq(i, team.ReportProgress, tk.ID))
	}
	for _, inbox := range []string{leadInbox, maInbox} {
		q := "&task=" + tk.ID
		for since, want := range map[string][]string{
			"":                        {reportID(3), reportID(2), reportID(1)},
			"&since=0":                {reportID(3), reportID(2), reportID(1)},
			"&since=" + itoa(at[1]):   {reportID(3), reportID(2)}, // created_at >= since
			"&since=" + itoa(at[2]+1): {},
		} {
			code, out, e := w.listReports(inbox, q+since)
			if code != 200 || !slices.Equal(reportIDs(out.Reports), want) {
				t.Fatalf("%s%s = %d %v %+v, want %v", inbox, since, code, reportIDs(out.Reports), e, want)
			}
		}
		for _, bad := range []string{"abc", "-1", "1.5", "99999999999999999999", "1e3"} {
			code, _, e := w.listReports(inbox, q+"&since="+bad)
			wantErr(t, "since="+bad, code, e, 400, team.ErrBadRequest)
		}
	}
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }

// A member whose right ended after the handler resolved it reads nothing: the
// store call that answers checks again, and the member gets what a task that
// never existed answers, with no report content in the body. Mutation gate:
// drop the guard from ListReportsForOwner → every row red.
func TestReports_ListRacesMemberState(t *testing.T) {
	for _, name := range raceNames() {
		t.Run(name, func(t *testing.T) {
			w := newTaskWorld(t)
			tk := w.mustTask(leadInbox, w.ma.Ref, "work", nil)
			secret := rreq(1, team.ReportProgress, tk.ID)
			secret.Summary = "SECRET-SUMMARY"
			secret.Body = "SECRET-BODY"
			w.mustReport(maInbox, secret)
			path := "/api/team/reports?origin_inbox=" + url.QueryEscape(maInbox) + "&task="
			wantCode, wantBody := w.missing(http.MethodGet, path+"000000-999", "")

			race := reportRaces(w, tk.ID)[name]
			w.m.afterTaskLookup = func() { w.m.afterTaskLookup = nil; race() }
			code, raw := w.do(http.MethodGet, path+tk.ID, "")
			if code != wantCode || string(raw) != wantBody {
				t.Fatalf("raced list = %d %s, want exactly %d %s", code, raw, wantCode, wantBody)
			}
			if strings.Contains(string(raw), "SECRET") {
				t.Fatalf("the body leaks report content: %s", raw)
			}
		})
	}
}
