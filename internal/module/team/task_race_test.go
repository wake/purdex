package teammod

import (
	"net/http"
	"net/url"
	"testing"

	"github.com/wake/purdex/internal/team"
)

// A member's right to a task is checked again where the task is read or
// written (the owner-scoped store calls), not only by the handler's lookup.
// Each race changes the world between the lookup and the store call, through
// the afterTaskLookup seam: the old owner then gets exactly what a task that
// never existed answers, and nothing is written or leaked.
// Mutation gates: drop the owner check from SetTaskStatusByOwner → the status
// rows red; from GetTaskDetail → the show rows red.
func taskRaces(w *taskWorld, taskID string) map[string]func() {
	must := func(err error) {
		w.t.Helper()
		if err != nil {
			w.t.Fatal(err)
		}
	}
	return map[string]func(){
		"the lead reassigns it to B": func() {
			if code, _, e := w.reassign(leadInbox, taskID, w.mb.Ref); code != 200 {
				w.t.Fatalf("reassign: %d %+v", code, e)
			}
		},
		"the member is killed": func() { must(w.m.store.SetMemberState("op-a", team.MemberKilled, 5)) },
		"the member is released": func() {
			_, err := w.m.store.db.Exec(`UPDATE team_members SET state = 'released' WHERE spawn_op = 'op-a'`)
			must(err)
		},
		"the team ends": func() {
			ok, err := w.m.store.EndTeam(uid(1), "sid-1", team.TeamEndLeadGone, 9)
			must(err)
			if !ok {
				w.t.Fatal("the team did not end")
			}
		},
	}
}

// missing is what a task id that never existed answers a member.
func (w *taskWorld) missing(method, path string, body any) (int, string) {
	w.t.Helper()
	code, raw := w.do(method, path, body)
	if code != http.StatusConflict {
		w.t.Fatalf("%s %s = %d %s, want a 409", method, path, code, raw)
	}
	return code, string(raw)
}

func TestTasks_StatusRacesReassign(t *testing.T) {
	for name := range taskRaces(nil, "") {
		t.Run(name, func(t *testing.T) {
			w := newTaskWorld(t)
			tk := w.mustTask(leadInbox, w.ma.Ref, "work", nil)
			before := mustGetTask(t, w.m.store, uid(1), 1)
			wantCode, wantBody := w.missing(http.MethodPost, "/api/team/tasks/000000-999/status",
				team.TaskStatusRequest{OriginInbox: leadInbox, Status: team.TaskInProgress})

			race := taskRaces(w, tk.ID)[name]
			w.m.afterTaskLookup = func() { w.m.afterTaskLookup = nil; race() }
			code, raw := w.do(http.MethodPost, "/api/team/tasks/"+tk.ID+"/status",
				team.TaskStatusRequest{OriginInbox: maInbox, Status: team.TaskInProgress})
			if code != wantCode || string(raw) != wantBody {
				t.Fatalf("raced status = %d %s, want exactly %d %s", code, raw, wantCode, wantBody)
			}
			after := mustGetTask(t, w.m.store, uid(1), 1)
			if after.Status != team.TaskPending || after.UpdatedAt != before.UpdatedAt {
				t.Fatalf("the raced request wrote: %+v -> %+v", before, after)
			}
			if name == "the lead reassigns it to B" && after.OwnerKey != "op-b" {
				t.Fatalf("after the reassign the task is %+v, want B's", after)
			}
		})
	}
}

func TestTasks_ShowRacesReassign(t *testing.T) {
	for name := range taskRaces(nil, "") {
		t.Run(name, func(t *testing.T) {
			w := newTaskWorld(t)
			tk := w.mustTask(leadInbox, w.ma.Ref, "work", nil)
			mustInsertReport(t, w.m.store, newReport(uid(1), 1, "op-a", team.ReportProgress, 1, 10))
			wantCode, wantBody := w.missing(http.MethodGet, "/api/team/tasks/000000-999?origin_inbox="+url.QueryEscape(leadInbox), "")

			race := taskRaces(w, tk.ID)[name]
			w.m.afterTaskLookup = func() { w.m.afterTaskLookup = nil; race() }
			code, raw := w.do(http.MethodGet, "/api/team/tasks/"+tk.ID+"?origin_inbox="+url.QueryEscape(maInbox), "")
			if code != wantCode || string(raw) != wantBody {
				t.Fatalf("raced show = %d %s, want exactly %d %s (no task or report content)", code, raw, wantCode, wantBody)
			}
		})
	}
}

// GET /api/team/tasks has no task id to be "not found": a member whose right
// ended after the handler resolved it is not a member any more, and gets what
// a session with no role gets, 409 not_member, byte for byte, with no task in
// the body. Mutation gate: drop the active or live check from
// ListTasksForOwner → red.
func TestTasks_ListRacesMemberState(t *testing.T) {
	races := taskRaces(nil, "")
	delete(races, "the lead reassigns it to B") // a list has no task to lose
	names := []string{"the member moves to another team"}
	for name := range races {
		names = append(names, name)
	}
	for _, name := range names {
		for _, q := range []string{"", "&all=1"} {
			t.Run(name+q, func(t *testing.T) {
				w := newTaskWorld(t)
				tk := w.mustTask(leadInbox, w.ma.Ref, "work", nil)
				wantCode, wantBody := w.missing(http.MethodGet, "/api/team/tasks?origin_inbox="+url.QueryEscape("/tmp/n.sock"), "")

				race := taskRaces(w, tk.ID)[name]
				if name == "the member moves to another team" {
					race = func() {
						if _, err := w.m.store.db.Exec(`UPDATE team_members SET team_id = ? WHERE spawn_op = 'op-a'`, uid(2)); err != nil {
							t.Fatal(err)
						}
					}
				}
				w.m.afterTaskLookup = func() { w.m.afterTaskLookup = nil; race() }
				code, raw := w.do(http.MethodGet, "/api/team/tasks?origin_inbox="+url.QueryEscape(maInbox)+q, "")
				if code != wantCode || string(raw) != wantBody {
					t.Fatalf("raced list = %d %s, want exactly %d %s", code, raw, wantCode, wantBody)
				}
			})
		}
	}
}
