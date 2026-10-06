package nex

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/wake/purdex/internal/conversations"
	"github.com/wake/purdex/internal/module/agent"
	pstore "github.com/wake/purdex/internal/store"
	"lab.protype.tw/wake/nexen/store"
)

// buildConversations is a pure join: every test hands it materialized
// inputs and stat seams backed by maps, never a file.

const (
	cvRoot = "/home/u/.claude/projects"
	cvSlug = cvRoot + "/-work-app"
	cvA    = "aaaaaaaa-0000-4000-8000-000000000001"
	cvB    = "bbbbbbbb-0000-4000-8000-000000000002"
	cvC    = "cccccccc-0000-4000-8000-000000000003"
)

func cvPath(sid string) string { return cvSlug + "/" + sid + ".jsonl" }

// cvIndexRow is an index row for sid under cvSlug: cwd /work/app, mtime 1000.
func cvIndexRow(sid, first, last string) pstore.ConversationIndexRow {
	return pstore.ConversationIndexRow{SessionID: sid, TranscriptPath: cvPath(sid), Cwd: "/work/app",
		FirstEntrypoint: first, LastEntrypoint: last, MtimeMs: 1000}
}

// cvStint is an execution for sid: cwd /work/stint, its transcript at
// cvPath(lower(sid)), profile "default". archived sets ArchivedAt.
func cvStint(id, sid string, state store.State, archived bool, created, updated int64) store.Execution {
	e := store.Execution{ID: id, SessionID: sid, State: state, CreatedAt: created, UpdatedAt: updated,
		Cwd: "/work/stint", TranscriptPath: cvPath(strings.ToLower(sid)), EffectiveProfile: "default"}
	if archived {
		e.ArchivedAt = updated
	}
	return e
}

// cvEnded is an archived, terminated stint: not live.
func cvEnded(id, sid string, created, updated int64) store.Execution {
	return cvStint(id, sid, store.StateTerminated, true, created, updated)
}

type cvWorld struct {
	in       conversationInputs
	regular  map[string]bool // IsRegular answers
	missing  map[string]bool // DirExists answers false for these
	dirCalls []string
}

// newCVWorld: a successful, empty listing; no file is regular; every dir
// exists.
func newCVWorld() *cvWorld {
	w := &cvWorld{regular: map[string]bool{}, missing: map[string]bool{}}
	w.in.Scan = conversations.ScanResult{Present: map[string]conversations.Entry{}}
	w.in.IsRegular = func(p string) bool { return w.regular[p] }
	w.in.DirExists = func(p string) bool {
		w.dirCalls = append(w.dirCalls, p)
		return !w.missing[p]
	}
	return w
}

func (w *cvWorld) index(rows ...pstore.ConversationIndexRow) *cvWorld {
	w.in.IndexRows = append(w.in.IndexRows, rows...)
	return w
}

func (w *cvWorld) list(sid string, mtime int64) *cvWorld {
	w.in.Scan.Present[sid] = conversations.Entry{SessionID: sid, Path: cvPath(sid), MtimeMs: mtime}
	return w
}

func (w *cvWorld) execs(es ...store.Execution) *cvWorld {
	w.in.Execs = append(w.in.Execs, es...)
	return w
}

func (w *cvWorld) frame(sid string, verified bool) *cvWorld {
	w.in.Terminals = append(w.in.Terminals, agent.TerminalSession{
		FrameID: "f-" + sid, PaneID: "%1", AgentType: "cc", SessionID: sid, Verified: verified})
	return w
}

func (w *cvWorld) build() conversationsResult { return buildConversations(w.in) }

func cvIDs(rows []conversationRow) []string {
	ids := []string{}
	for _, r := range rows {
		ids = append(ids, r.SessionID)
	}
	return ids
}

func cvExpect(t *testing.T, res conversationsResult, ended, gone []string, unknown int) {
	t.Helper()
	if ended == nil {
		ended = []string{}
	}
	if gone == nil {
		gone = []string{}
	}
	gotEnded, gotGone := cvIDs(res.Ended), cvIDs(res.Gone)
	if !reflect.DeepEqual(gotEnded, ended) || !reflect.DeepEqual(gotGone, gone) || res.UnknownOwner != unknown {
		t.Fatalf("ended = %v, gone = %v, unknown_owner = %d\nwant ended = %v, gone = %v, unknown_owner = %d",
			gotEnded, gotGone, res.UnknownOwner, ended, gone, unknown)
	}
}

// cvOne returns the only row of rows, failing unless there is exactly one.
func cvOne(t *testing.T, rows []conversationRow) conversationRow {
	t.Helper()
	if len(rows) != 1 {
		t.Fatalf("rows = %v, want exactly one", cvIDs(rows))
	}
	return rows[0]
}

// §13.9: a terminal-born ended conversation → 已退出, 上次在 終端機.
func TestConversations_CliBornNoOwnerListedIsEnded(t *testing.T) {
	r := cvIndexRow(cvA, "cli", "cli")
	r.AITitle, r.FirstPrompt = "Fix the build", "please fix the build"
	res := newCVWorld().index(r).list(cvA, 2000).build()
	cvExpect(t, res, []string{cvA}, nil, 0)
	want := conversationRow{SessionID: cvA, Title: "Fix the build", TitleSource: "ai",
		FirstPrompt: "please fix the build", Cwd: "/work/app", CwdExists: true, LastActivityAt: 2000,
		LastIn: "terminal", TranscriptPath: cvPath(cvA)}
	if got := cvOne(t, res.Ended); !reflect.DeepEqual(got, want) {
		t.Fatalf("row =\n %+v\nwant\n %+v", got, want)
	}
}

// R-4-9: a verified live frame is an owner (running, not listed); an S held
// only by unverified frames is listed nowhere and counted, whatever the
// frame order (LiveSessions is unordered).
func TestConversations_TerminalOwners(t *testing.T) {
	type frame struct {
		sid      string
		verified bool
	}
	cases := []struct {
		name    string
		frames  []frame
		stints  []store.Execution
		ended   []string
		unknown int
	}{
		{name: "no frame", ended: []string{cvA}},
		{name: "verified frame", frames: []frame{{cvA, true}}},
		{name: "verified frame, upper-case id", frames: []frame{{strings.ToUpper(cvA), true}}},
		{name: "unverified only", frames: []frame{{cvA, false}}, unknown: 1},
		{name: "unverified only, upper-case id", frames: []frame{{strings.ToUpper(cvA), false}}, unknown: 1},
		{name: "two unverified frames count one S", frames: []frame{{cvA, false}, {cvA, false}}, unknown: 1},
		{name: "unverified then verified", frames: []frame{{cvA, false}, {cvA, true}}},
		{name: "verified then unverified", frames: []frame{{cvA, true}, {cvA, false}}},
		{name: "unverified frame and a live stint", frames: []frame{{cvA, false}},
			stints: []store.Execution{cvStint("E1", cvA, store.StateIdle, false, 1, 2)}},
		{name: "unverified frame of an out-of-scope S", frames: []frame{{cvC, false}}, ended: []string{cvA}},
		{name: "frame of another S", frames: []frame{{cvB, true}}, ended: []string{cvA}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := newCVWorld().index(cvIndexRow(cvA, "cli", "cli"), cvIndexRow(cvC, "sdk-cli", "sdk-cli")).
				list(cvA, 2000).list(cvC, 2000).execs(tc.stints...)
			for _, f := range tc.frames {
				w.frame(f.sid, f.verified)
			}
			cvExpect(t, w.build(), tc.ended, nil, tc.unknown)
		})
	}
}

// §13.9: a worker-born one with an archived stint and the transcript present
// → 已退出, 上次在 Worker.
func TestConversations_WorkerBornArchivedStintListedIsEnded(t *testing.T) {
	res := newCVWorld().index(cvIndexRow(cvA, "sdk-cli", "sdk-cli")).list(cvA, 2000).
		execs(cvEnded("E1", cvA, 10, 20)).build()
	cvExpect(t, res, []string{cvA}, nil, 0)
	got := cvOne(t, res.Ended)
	if got.LastIn != "worker" || got.LatestExecutionID != "E1" || got.EffectiveProfile != "default" {
		t.Fatalf("row = %+v", got)
	}
	if got.Cwd != "/work/app" || got.TranscriptPath != cvPath(cvA) || got.LastActivityAt != 2000 {
		t.Fatalf("index and listing must win over the stint: %+v", got)
	}
}

// §13.9: the same with the transcript removed → 已消失, with no index row
// needed (R-4-8: last activity is the stint's UpdatedAt).
func TestConversations_StintOnlyTranscriptMissingIsGone(t *testing.T) {
	e := cvEnded("E1", cvA, 10, 5000)
	e.TitleText = "Nexen title"
	res := newCVWorld().execs(e).build()
	cvExpect(t, res, nil, []string{cvA}, 0)
	want := conversationRow{SessionID: cvA, Title: "Nexen title", TitleSource: "nexen",
		Cwd: "/work/stint", CwdExists: true, LastActivityAt: 5000, LastIn: "worker",
		TranscriptPath: cvPath(cvA), LatestExecutionID: "E1", EffectiveProfile: "default"}
	if got := cvOne(t, res.Gone); !reflect.DeepEqual(got, want) {
		t.Fatalf("row =\n %+v\nwant\n %+v", got, want)
	}
}

// R-4-8: the latest stint's transcript, outside the listing, that Lstats as
// a regular file makes S present. Only the latest stint's path is stat'ed.
func TestConversations_StintTranscriptOutsideListing(t *testing.T) {
	t.Run("exists → ended", func(t *testing.T) {
		e := cvEnded("E1", cvA, 10, 20)
		e.TranscriptPath = "/elsewhere/" + cvA + ".jsonl"
		w := newCVWorld().execs(e)
		w.regular[e.TranscriptPath] = true
		res := w.build()
		cvExpect(t, res, []string{cvA}, nil, 0)
		if got := cvOne(t, res.Ended); got.TranscriptPath != e.TranscriptPath {
			t.Fatalf("transcript_path = %q", got.TranscriptPath)
		}
	})
	t.Run("only an older stint's path exists → gone", func(t *testing.T) {
		old, latest := cvEnded("E1", cvA, 10, 20), cvEnded("E2", cvA, 30, 40)
		old.TranscriptPath, latest.TranscriptPath = "/old/"+cvA+".jsonl", "/new/"+cvA+".jsonl"
		w := newCVWorld().execs(old, latest)
		w.regular[old.TranscriptPath] = true
		cvExpect(t, w.build(), nil, []string{cvA}, 0)
	})
	t.Run("empty path is never stat'ed", func(t *testing.T) {
		e := cvEnded("E1", cvA, 10, 20)
		e.TranscriptPath = ""
		w := newCVWorld().execs(e)
		w.regular[""] = true
		cvExpect(t, w.build(), nil, []string{cvA}, 0)
	})
}

// §13.5: nothing is stored as gone. Indexed, not listed → gone; listed again
// → ended.
func TestConversations_IndexedNotListedIsGoneThenListedAgainIsEnded(t *testing.T) {
	w := newCVWorld().index(cvIndexRow(cvA, "cli", "cli"))
	res := w.build()
	cvExpect(t, res, nil, []string{cvA}, 0)
	got := cvOne(t, res.Gone)
	if got.LastActivityAt != 1000 || got.TranscriptPath != cvPath(cvA) || got.LastIn != "terminal" {
		t.Fatalf("gone row = %+v", got)
	}

	res = w.list(cvA, 3000).build()
	cvExpect(t, res, []string{cvA}, nil, 0)
	if got := cvOne(t, res.Ended); got.LastActivityAt != 3000 {
		t.Fatalf("last_activity_at = %d, want the listed mtime", got.LastActivityAt)
	}
}

// R-4-7: an S whose indexed transcript_path lies in an unreadable slug dir
// (its parent dir, compared cleaned) is present.
func TestConversations_UnreadableDirKeepsIndexedPresent(t *testing.T) {
	cases := []struct {
		name  string
		dirs  []string
		ended bool
	}{
		{"its slug dir", []string{cvSlug}, true},
		{"its slug dir, uncleaned", []string{cvSlug + "/./"}, true},
		{"one of several", []string{cvRoot + "/-x", cvSlug}, true},
		{"a sibling sharing the prefix", []string{cvSlug + "-other"}, false},
		{"the root above it", []string{cvRoot}, false},
		{"none", nil, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := newCVWorld().index(cvIndexRow(cvA, "cli", "cli"))
			w.in.Scan.UnreadableDirs = tc.dirs
			if tc.ended {
				cvExpect(t, w.build(), []string{cvA}, nil, 0)
			} else {
				cvExpect(t, w.build(), nil, []string{cvA}, 0)
			}
		})
	}
	t.Run("an empty path lies in no dir", func(t *testing.T) {
		r := cvIndexRow(cvA, "cli", "cli")
		r.TranscriptPath = ""
		w := newCVWorld().index(r)
		w.in.Scan.UnreadableDirs = []string{"."} // filepath.Dir("")
		cvExpect(t, w.build(), nil, []string{cvA}, 0)
	})
}

// R-4-7 applied to the other path Purdex knows for S: a stint-only S whose
// latest transcript_path lies in an unreadable slug dir is not marked gone
// from that partial view (an unmounted slug target fails the Lstat too).
func TestConversations_UnreadableDirKeepsStintPathPresent(t *testing.T) {
	w := newCVWorld().execs(cvEnded("E1", cvA, 10, 20))
	w.in.Scan.UnreadableDirs = []string{cvSlug}
	cvExpect(t, w.build(), []string{cvA}, nil, 0)
}

// R-4-14 / §13.2: in scope = born interactively (first entrypoint non-empty,
// not sdk-*) or a stint.
func TestConversations_Scope(t *testing.T) {
	cases := []struct {
		name        string
		first, last string
		listed      bool
		ended, gone []string
	}{
		{name: "sdk-born, no stint, listed", first: "sdk-cli", last: "sdk-cli", listed: true},
		{name: "sdk-born, no stint, not listed", first: "sdk-cli", last: "sdk-cli"},
		{name: "sdk-born, last cli, no stint: the first decides", first: "sdk-ts", last: "cli", listed: true},
		{name: "IDE-born, no stint", first: "claude-vscode", last: "claude-vscode", listed: true, ended: []string{cvA}},
		{name: "IDE-born, no stint, not listed", first: "claude-vscode", gone: []string{cvA}},
		{name: "no entrypoint, no stint, listed", listed: true},
		{name: "no entrypoint, no stint, not listed"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := newCVWorld().index(cvIndexRow(cvA, tc.first, tc.last))
			if tc.listed {
				w.list(cvA, 2000)
			}
			res := w.build()
			cvExpect(t, res, tc.ended, tc.gone, 0)
			for _, r := range append(res.Ended, res.Gone...) {
				if r.LastIn != "terminal" {
					t.Errorf("last_in = %q, want terminal", r.LastIn)
				}
			}
		})
	}
	t.Run("no entrypoint, with a stint", func(t *testing.T) {
		res := newCVWorld().index(cvIndexRow(cvA, "", "")).list(cvA, 2000).execs(cvEnded("E1", cvA, 1, 2)).build()
		cvExpect(t, res, []string{cvA}, nil, 0)
		if got := cvOne(t, res.Ended); got.LastIn != "worker" {
			t.Fatalf("last_in = %q, want worker", got.LastIn)
		}
	})
	t.Run("sdk-born with a stint", func(t *testing.T) {
		res := newCVWorld().index(cvIndexRow(cvA, "sdk-cli", "sdk-cli")).list(cvA, 2000).execs(cvEnded("E1", cvA, 1, 2)).build()
		cvExpect(t, res, []string{cvA}, nil, 0)
	})
}

// §4.2 / D1: live = isLiveExecution (not archived, not terminated), any
// stint of S; failed and rejected are live until exited (Review Focus 3).
func TestConversations_LiveStintIsRunning(t *testing.T) {
	cases := []struct {
		name     string
		stints   []store.Execution
		listedAs bool
	}{
		{"failed, not archived", []store.Execution{cvStint("E1", cvA, store.StateFailed, false, 1, 2)}, false},
		{"rejected, not archived", []store.Execution{cvStint("E1", cvA, store.StateRejected, false, 1, 2)}, false},
		{"queued", []store.Execution{cvStint("E1", cvA, store.StateQueued, false, 1, 2)}, false},
		{"running", []store.Execution{cvStint("E1", cvA, store.StateRunning, false, 1, 2)}, false},
		{"idle", []store.Execution{cvStint("E1", cvA, store.StateIdle, false, 1, 2)}, false},
		{"an older stint live, the latest archived", []store.Execution{
			cvStint("E1", cvA, store.StateIdle, false, 1, 2), cvEnded("E2", cvA, 3, 4)}, false},
		{"failed, archived", []store.Execution{cvStint("E1", cvA, store.StateFailed, true, 1, 2)}, true},
		{"terminated, not archived", []store.Execution{cvStint("E1", cvA, store.StateTerminated, false, 1, 2)}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res := newCVWorld().index(cvIndexRow(cvA, "cli", "cli")).list(cvA, 2000).execs(tc.stints...).build()
			if tc.listedAs {
				cvExpect(t, res, []string{cvA}, nil, 0)
			} else {
				cvExpect(t, res, nil, nil, 0)
			}
		})
	}
}

// Session ids are compared lowercase; D9's key is SessionID, else
// ResumeSessionID.
func TestConversations_ExecutionJoinsLowercaseIndexRow(t *testing.T) {
	t.Run("only ResumeSessionID, mixed case", func(t *testing.T) {
		e := cvEnded("E1", "", 1, 2)
		e.ResumeSessionID = strings.ToUpper(cvA[:9]) + cvA[9:]
		res := newCVWorld().index(cvIndexRow(cvA, "cli", "cli")).list(cvA, 2000).execs(e).build()
		cvExpect(t, res, []string{cvA}, nil, 0)
		if got := cvOne(t, res.Ended); got.LatestExecutionID != "E1" {
			t.Fatalf("latest_execution_id = %q", got.LatestExecutionID)
		}
	})
	t.Run("upper-case SessionID", func(t *testing.T) {
		res := newCVWorld().index(cvIndexRow(cvA, "sdk-cli", "sdk-cli")).list(cvA, 2000).
			execs(cvEnded("E1", strings.ToUpper(cvA), 1, 2)).build()
		cvExpect(t, res, []string{cvA}, nil, 0)
		if got := cvOne(t, res.Ended); got.LatestExecutionID != "E1" {
			t.Fatalf("latest_execution_id = %q", got.LatestExecutionID)
		}
	})
	t.Run("a live one with only ResumeSessionID, upper-case → running", func(t *testing.T) {
		e := cvStint("E1", "", store.StateIdle, false, 1, 2)
		e.ResumeSessionID = strings.ToUpper(cvA)
		res := newCVWorld().index(cvIndexRow(cvA, "cli", "cli")).list(cvA, 2000).execs(e).build()
		cvExpect(t, res, nil, nil, 0)
	})
	t.Run("an upper-case index row", func(t *testing.T) {
		r := cvIndexRow(strings.ToUpper(cvA), "cli", "cli")
		res := newCVWorld().index(r).list(cvA, 2000).build()
		cvExpect(t, res, []string{cvA}, nil, 0)
		if got := cvOne(t, res.Ended); got.LastActivityAt != 2000 {
			t.Fatalf("the index row did not join the listing: %+v", got)
		}
	})
	t.Run("no session id at all → no row", func(t *testing.T) {
		res := newCVWorld().execs(cvEnded("E1", "", 1, 2)).build()
		cvExpect(t, res, nil, nil, 0)
	})
}

// A live execution is for S through either id (executionIsFor, as
// checkOwners): one that resumed S under a new session id still owns S, so
// S is not shown ended while a rebuild would answer 409.
func TestConversations_LiveExecutionOwnsItsResumeSessionID(t *testing.T) {
	e := cvStint("E1", cvB, store.StateRunning, false, 1, 2)
	e.ResumeSessionID = cvA
	res := newCVWorld().index(cvIndexRow(cvA, "cli", "cli")).list(cvA, 2000).execs(e).build()
	cvExpect(t, res, nil, nil, 0)
}

func TestConversations_Cwd(t *testing.T) {
	t.Run("the dir is gone", func(t *testing.T) {
		w := newCVWorld().index(cvIndexRow(cvA, "cli", "cli")).list(cvA, 2000)
		w.missing["/work/app"] = true
		got := cvOne(t, w.build().Ended)
		if got.Cwd != "/work/app" || got.CwdExists {
			t.Fatalf("cwd = %q exists = %v", got.Cwd, got.CwdExists)
		}
	})
	t.Run("no cwd anywhere", func(t *testing.T) {
		r := cvIndexRow(cvA, "cli", "cli")
		r.Cwd = ""
		w := newCVWorld().index(r).list(cvA, 2000)
		got := cvOne(t, w.build().Ended)
		if got.Cwd != "" || got.CwdExists {
			t.Fatalf("cwd = %q exists = %v", got.Cwd, got.CwdExists)
		}
		if len(w.dirCalls) != 0 {
			t.Fatalf("DirExists called with %q", w.dirCalls)
		}
	})
	t.Run("no index cwd → the latest stint's", func(t *testing.T) {
		r := cvIndexRow(cvA, "cli", "cli")
		r.Cwd = ""
		old, latest := cvEnded("E1", cvA, 1, 2), cvEnded("E2", cvA, 3, 4)
		old.Cwd, latest.Cwd = "/old", "/new"
		got := cvOne(t, newCVWorld().index(r).list(cvA, 2000).execs(latest, old).build().Ended)
		if got.Cwd != "/new" || !got.CwdExists {
			t.Fatalf("cwd = %q exists = %v", got.Cwd, got.CwdExists)
		}
	})
}

// R-4-10: custom > ai > the latest stint's TitleText > the prompt's first
// line (trimmed, ≤ 120 runes) > S[:8].
func TestConversations_Title(t *testing.T) {
	long := strings.Repeat("字", 300)
	cases := []struct {
		name          string
		custom, ai    string
		prompt        string
		stints        []store.Execution
		title, source string
	}{
		{name: "custom", custom: "Custom", ai: "AI", prompt: "p",
			stints: []store.Execution{cvTitled("E1", 1, "Nexen")}, title: "Custom", source: "custom"},
		{name: "ai", ai: "AI", prompt: "p",
			stints: []store.Execution{cvTitled("E1", 1, "Nexen")}, title: "AI", source: "ai"},
		{name: "nexen, the latest stint", prompt: "p",
			stints: []store.Execution{cvTitled("E1", 1, "Older"), cvTitled("E2", 2, "Latest")}, title: "Latest", source: "nexen"},
		{name: "the latest stint by id on a CreatedAt tie", prompt: "p",
			stints: []store.Execution{cvTitled("E2", 5, "By id"), cvTitled("E1", 5, "Not me")}, title: "By id", source: "nexen"},
		{name: "an older stint's title is not used", prompt: "the prompt",
			stints: []store.Execution{cvTitled("E1", 1, "Older"), cvTitled("E2", 2, "")}, title: "the prompt", source: "prompt"},
		{name: "a blank stint title is none", prompt: "the prompt",
			stints: []store.Execution{cvTitled("E1", 1, "  ")}, title: "the prompt", source: "prompt"},
		{name: "prompt, two lines", prompt: "  line one  \nline two", title: "line one", source: "prompt"},
		{name: "prompt, CRLF", prompt: "line one\r\nline two", title: "line one", source: "prompt"},
		{name: "prompt, 300 runes", prompt: long, title: strings.Repeat("字", 120), source: "prompt"},
		{name: "session id", title: cvA[:8], source: "session_id"},
		{name: "a prompt with a blank first line falls to the session id", prompt: "\nsecond", title: cvA[:8], source: "session_id"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := cvIndexRow(cvA, "cli", "cli")
			r.CustomTitle, r.AITitle, r.FirstPrompt = tc.custom, tc.ai, tc.prompt
			got := cvOne(t, newCVWorld().index(r).list(cvA, 2000).execs(tc.stints...).build().Ended)
			if got.Title != tc.title || got.TitleSource != tc.source {
				t.Fatalf("title = %q (%s), want %q (%s)", got.Title, got.TitleSource, tc.title, tc.source)
			}
			if got.FirstPrompt != tc.prompt {
				t.Fatalf("first_prompt = %q, want the index's %q", got.FirstPrompt, tc.prompt)
			}
		})
	}
}

func cvTitled(id string, created int64, title string) store.Execution {
	e := cvEnded(id, cvA, created, created+1)
	e.TitleText = title
	return e
}

// R-4-6: the last entrypoint, else the first; none → worker with a stint,
// terminal without; any value but cli / sdk-* → terminal.
func TestConversations_LastIn(t *testing.T) {
	cases := []struct {
		first, last string
		stint       bool
		want        string
	}{
		{"cli", "sdk-cli", false, "worker"},
		{"cli", "cli", true, "terminal"},
		{"sdk-cli", "cli", true, "terminal"},
		{"cli", "", false, "terminal"},
		{"sdk-cli", "", true, "worker"},
		{"", "", true, "worker"},
		{"", "", false, "terminal"},
		{"cli", "claude-vscode", false, "terminal"},
		{"claude-vscode", "", true, "terminal"},
		{"", "sdk-ts", false, "worker"},
	}
	for _, tc := range cases {
		if got := conversationLastIn(tc.first, tc.last, tc.stint); got != tc.want {
			t.Errorf("lastIn(first %q, last %q, stint %v) = %q, want %q", tc.first, tc.last, tc.stint, got, tc.want)
		}
	}
	// End to end: last sdk-cli on a cli-born S without a stint.
	got := cvOne(t, newCVWorld().index(cvIndexRow(cvA, "cli", "sdk-cli")).list(cvA, 2000).build().Ended)
	if got.LastIn != "worker" {
		t.Fatalf("last_in = %q, want worker", got.LastIn)
	}
}

// R-4-1 / §13.5: a failed listing marks nothing gone.
func TestBuild_RootErrorMarksNothingGone(t *testing.T) {
	w := newCVWorld().index(cvIndexRow(cvA, "cli", "cli")).execs(cvEnded("E1", cvB, 10, 5000))
	w.in.Scan = conversations.ScanResult{RootErr: errors.New("open /home/u/.claude/projects: no such file or directory")}
	res := w.build()
	cvExpect(t, res, []string{cvB, cvA}, nil, 0)
	if res.Ended[0].LastActivityAt != 5000 || res.Ended[1].LastActivityAt != 1000 {
		t.Fatalf("last_activity_at = %d, %d", res.Ended[0].LastActivityAt, res.Ended[1].LastActivityAt)
	}
	if res.Ended[1].TranscriptPath != cvPath(cvA) {
		t.Fatalf("transcript_path = %q, want the index's", res.Ended[1].TranscriptPath)
	}
}

func TestConversations_Order(t *testing.T) {
	w := newCVWorld().index(cvIndexRow(cvA, "cli", "cli"), cvIndexRow(cvB, "cli", "cli"), cvIndexRow(cvC, "cli", "cli"))
	w.list(cvC, 300).list(cvA, 100).list(cvB, 300)
	cvExpect(t, w.build(), []string{cvB, cvC, cvA}, nil, 0)

	g := newCVWorld().execs(cvEnded("E1", cvC, 1, 300), cvEnded("E2", cvA, 1, 900), cvEnded("E3", cvB, 1, 300))
	cvExpect(t, g.build(), nil, []string{cvA, cvB, cvC}, 0)
}

// The JSON tags are the wire format Task 38 serves and the SPA reads.
func TestConversations_RowWireFormat(t *testing.T) {
	b, err := json.Marshal(conversationRow{SessionID: cvA, Title: "t", TitleSource: "ai", LastActivityAt: 7, LastIn: "worker"})
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"session_id", "title", "title_source", "cwd_exists", "last_activity_at", "last_in"} {
		if _, ok := m[k]; !ok {
			t.Errorf("missing %q in %s", k, b)
		}
	}
	for _, k := range []string{"first_prompt", "cwd", "transcript_path", "latest_execution_id", "effective_profile"} {
		if _, ok := m[k]; ok {
			t.Errorf("%q should be omitted when empty: %s", k, b)
		}
	}
	if len(m) != 6 {
		t.Errorf("keys = %v", m)
	}
}
