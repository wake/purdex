package session

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wake/purdex/internal/module/hostconfig"
	"github.com/wake/purdex/internal/store"
	"github.com/wake/purdex/internal/tmux"
)

// NA-0b (no-agent launch panel spec §2.2, §2.3, §2.4): the plan a client renders and the launch it posts.

const launchInstance = "111:1000"

type fakeFacts struct {
	running bool
	last    *store.LastRun
	err     error
}

func (f *fakeFacts) AgentRunning(context.Context, string) (bool, error) { return f.running, f.err }
func (f *fakeFacts) LastRun(string) (*store.LastRun, error)             { return f.last, f.err }

type fakeTemplates map[string]hostconfig.ResumeTemplatePair

func (f fakeTemplates) ResumeTemplates() (map[string]hostconfig.ResumeTemplatePair, error) {
	return f, nil
}

type launchRig struct {
	mod   *SessionModule
	fake  *tmux.FakeExecutor
	mux   *http.ServeMux
	facts *fakeFacts
	code  string
}

func newLaunchRig(t *testing.T) *launchRig {
	t.Helper()
	mod, _, fake := newTestModule(t)
	mod.tmuxInstanceFn = instanceOf(fake.Instance)
	fake.SetInstance(launchInstance)
	fake.AddSession("work", "/w/proj")
	fake.SetActivePaneMetadata("work", tmux.TmuxPaneMetadata{SessionID: "$0", SessionName: "work", PaneCurrentCommand: "zsh"})
	fake.SetPaneCwd("$0:", "/somewhere/else")
	mux := http.NewServeMux()
	mod.RegisterRoutes(mux)
	facts := &fakeFacts{}
	mod.core.Registry.Register(AgentRunningKey, facts)
	mod.core.Registry.Register(hostconfig.ResumeTemplatesKey, fakeTemplates(hostconfig.DefaultResumeTemplates))
	sessions, err := mod.ListSessions()
	require.NoError(t, err)
	require.Len(t, sessions, 1)
	return &launchRig{mod: mod, fake: fake, mux: mux, facts: facts, code: sessions[0].Code}
}

func (r *launchRig) lastRun(agent, sid, cwd string) {
	r.facts.last = &store.LastRun{Code: r.code, TmuxInstance: launchInstance, AgentType: agent, SessionID: sid, Cwd: cwd, EndedAt: 1791000000000}
}

func (r *launchRig) get(t *testing.T, code, query string) (*httptest.ResponseRecorder, launchPlan) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/sessions/"+code+"/launch-plan"+query, nil)
	w := httptest.NewRecorder()
	r.mux.ServeHTTP(w, req)
	var plan launchPlan
	if w.Code == http.StatusOK {
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &plan), w.Body.String())
	}
	return w, plan
}

func (r *launchRig) plan(t *testing.T) launchPlan {
	t.Helper()
	w, plan := r.get(t, r.code, "")
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	return plan
}

func (r *launchRig) post(code, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/api/sessions/"+code+"/launch", strings.NewReader(body))
	w := httptest.NewRecorder()
	r.mux.ServeHTTP(w, req)
	return w
}

func (r *launchRig) launch(kind string, cd bool) *httptest.ResponseRecorder {
	b, _ := json.Marshal(map[string]any{"kind": kind, "cd": cd, "expected_tmux_instance": launchInstance})
	return r.post(r.code, string(b))
}

func codeOfBody(t *testing.T, w *httptest.ResponseRecorder) string {
	t.Helper()
	var b struct {
		Code string `json:"code"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &b), w.Body.String())
	return b.Code
}

func (r *launchRig) typed() []string {
	var out []string
	for _, k := range r.fake.RawKeysSent() {
		out = append(out, strings.Join(k.Keys, ""))
	}
	return out
}

// --- GET launch-plan --------------------------------------------------------

func TestLaunchPlan_UnknownSessionIs404(t *testing.T) {
	r := newLaunchRig(t)
	w, _ := r.get(t, "nosuch", "")
	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestLaunchPlan_NoRecord(t *testing.T) {
	r := newLaunchRig(t)
	p := r.plan(t)
	assert.False(t, p.AgentRunning)
	assert.Equal(t, launchInstance, p.TmuxInstance)
	assert.Nil(t, p.Last)
	assert.Nil(t, p.Dir, "no last: no directory row, and never an older generation's cwd")
	assert.Nil(t, p.Resume.Line)
	assert.Equal(t, "no_record", p.Resume.Reason)
	assert.Equal(t, "claude", p.Fresh.Line)
	assert.Nil(t, p.Busy)
}

// The wire shape both clients are written to: exact field names, `line: null` with a reason, and null for the absent parts.
func TestLaunchPlan_WireShape(t *testing.T) {
	r := newLaunchRig(t)
	w, _ := r.get(t, r.code, "")
	var raw map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &raw))
	for _, k := range []string{"agent_running", "tmux_instance", "last", "dir", "resume", "fresh", "busy"} {
		assert.Contains(t, raw, k)
	}
	assert.JSONEq(t, `{"line":null,"reason":"no_record"}`, string(raw["resume"]))
	assert.JSONEq(t, `{"line":"claude"}`, string(raw["fresh"]))
	assert.Equal(t, "null", string(raw["last"]))
	assert.Equal(t, "null", string(raw["busy"]))
	r.lastRun("cc", "S1", "/w/proj")
	w, _ = r.get(t, r.code, "")
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &raw))
	assert.JSONEq(t, `{"line":"claude --resume S1"}`, string(raw["resume"]), "no reason key when there is a line")
	assert.JSONEq(t, `{"agent_type":"cc","session_id":"S1","cwd":"/w/proj","ended_at":1791000000000}`, string(raw["last"]))
	assert.JSONEq(t, `{"path":"/w/proj","state":"move"}`, string(raw["dir"]))
}

func TestLaunchPlan_AgentRunningIsReportedAndNothingElse(t *testing.T) {
	r := newLaunchRig(t)
	r.lastRun("cc", "S1", "/w/proj")
	r.facts.running = true
	p := r.plan(t)
	assert.True(t, p.AgentRunning)
	assert.Nil(t, p.Last)
	assert.Nil(t, p.Dir)
}

func TestLaunchPlan_RestoredAfterADaemonRestartButNotAfterATmuxRestart(t *testing.T) {
	r := newLaunchRig(t)
	r.lastRun("cc", "S1", "/w/proj") // same generation: the daemon restarted, tmux did not
	assert.NotNil(t, r.plan(t).Last)

	r.facts.last.TmuxInstance = "999:1" // the record belongs to another tmux server
	p := r.plan(t)
	assert.Nil(t, p.Last, "a reused code never lends a record")
	assert.Nil(t, p.Dir)
	assert.Equal(t, "no_record", p.Resume.Reason)

	r.facts.last.TmuxInstance = "" // a record with no generation matches nothing
	assert.Nil(t, r.plan(t).Last)
}

func TestLaunchPlan_FreshLineIsTheAgentLastRun(t *testing.T) {
	for agent, want := range map[string]string{"cc": "claude", "codex": "codex", "opencode": "opencode", "aider": "claude", "": "claude"} {
		r := newLaunchRig(t)
		r.lastRun(agent, "S1", "/w/proj")
		assert.Equal(t, want, r.plan(t).Fresh.Line, "agent %q", agent)
	}
}

func TestLaunchPlan_Dir(t *testing.T) {
	cases := []struct{ pane, cwd, want string }{
		{"/w/proj", "/w/proj", "here"},
		{"/w/proj/", "/w/proj", "here"}, // one trailing slash trimmed
		{"/w/proj", "/w/proj/", "here"},
		{"/w/other", "/w/proj", "move"},
		{"/w/proj/sub", "/w/proj", "move"},
	}
	for _, c := range cases {
		r := newLaunchRig(t)
		r.fake.SetPaneCwd("$0:", c.pane)
		r.lastRun("cc", "S1", c.cwd)
		d := r.plan(t).Dir
		require.NotNil(t, d, "%+v", c)
		assert.Equal(t, c.cwd, d.Path)
		assert.Equal(t, c.want, d.State, "%+v", c)
	}
	r := newLaunchRig(t) // a record with no cwd has no directory row
	r.lastRun("cc", "S1", "")
	assert.Nil(t, r.plan(t).Dir)
}

func TestLaunchPlan_ResumeLineAndTheIDRule(t *testing.T) {
	long := strings.Repeat("a", 129)
	cases := []struct {
		id, want string
	}{
		{"3f2a9c1e-77aa-4d2e-9a3b-0c1d2e3f4a5b", "claude --resume 3f2a9c1e-77aa-4d2e-9a3b-0c1d2e3f4a5b"}, // a UUID
		{"ses_abc123XYZ", "claude --resume ses_abc123XYZ"},
		{strings.Repeat("a", 128), "claude --resume " + strings.Repeat("a", 128)},
		{"", "claude -c"}, // fallback, taken as is
		{"has space", "claude -c"},
		{"it's", "claude -c"},
		{"a;b", "claude -c"},
		{"a\nb", "claude -c"},
		{"$(x)", "claude -c"},
		{long, "claude -c"},
	}
	for _, c := range cases {
		r := newLaunchRig(t)
		r.lastRun("cc", c.id, "/w/proj")
		p := r.plan(t)
		require.NotNil(t, p.Resume.Line, "id %q", c.id)
		assert.Equal(t, c.want, *p.Resume.Line, "id %q", c.id)
	}
}

func TestLaunchPlan_ResumeEmptyAndMissingTemplates(t *testing.T) {
	r := newLaunchRig(t)
	set := func(m fakeTemplates) { r.mod.core.Registry.Register(hostconfig.ResumeTemplatesKey, m) }

	r.lastRun("cc", "S1", "/w/proj")
	set(fakeTemplates{"cc": {Exact: "", Fallback: "claude -c"}})
	p := r.plan(t)
	assert.Nil(t, p.Resume.Line, "an empty exact line is the owner's: not backfilled")
	assert.Equal(t, "empty_command", p.Resume.Reason)

	r.lastRun("cc", "", "/w/proj") // no usable id: the fallback is used, and it is empty
	set(fakeTemplates{"cc": {Exact: "claude --resume {id}", Fallback: "  "}})
	assert.Equal(t, "empty_command", r.plan(t).Resume.Reason)

	set(fakeTemplates{"cc": {Exact: "", Fallback: ""}})
	assert.Equal(t, "empty_command", r.plan(t).Resume.Reason)

	set(fakeTemplates{}) // the host has no template for this agent
	assert.Equal(t, "no_template", r.plan(t).Resume.Reason)
}

func TestLaunchPlan_Busy(t *testing.T) {
	for cmd, busy := range map[string]bool{"npm": true, "claude": true, "vim": true, "zsh": false, "-zsh": false, "bash": false, "FISH": false, "": false} {
		r := newLaunchRig(t)
		r.fake.SetActivePaneMetadata("work", tmux.TmuxPaneMetadata{SessionID: "$0", SessionName: "work", PaneCurrentCommand: cmd})
		r.mod.invalidateListCache()
		p := r.plan(t)
		if busy {
			require.NotNil(t, p.Busy, cmd)
			assert.Equal(t, cmd, p.Busy.Command)
		} else {
			assert.Nil(t, p.Busy, cmd)
		}
	}
}

// GET takes no expectation: one that is sent is ignored, however malformed — never a 400.
func TestLaunchPlan_IgnoresExpectedTmuxInstance(t *testing.T) {
	r := newLaunchRig(t)
	for _, q := range []string{"?expected_tmux_instance=", "?expected_tmux_instance=999:1", "?expected_tmux_instance=%7B%22%7D,#{x}", "?x=1"} {
		w, _ := r.get(t, r.code, q)
		assert.Equal(t, http.StatusOK, w.Code, q)
	}
}

func TestLaunchPlan_FactsUnavailableIs500NotAGuess(t *testing.T) {
	r := newLaunchRig(t)
	r.facts.err = assert.AnError
	w, _ := r.get(t, r.code, "")
	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

// --- POST launch ------------------------------------------------------------

func TestLaunch_BadRequests(t *testing.T) {
	r := newLaunchRig(t)
	for name, body := range map[string]string{
		"not json":            `{`,
		"unknown kind":        `{"kind":"other","cd":false,"expected_tmux_instance":"` + launchInstance + `"}`,
		"no kind":             `{"cd":false,"expected_tmux_instance":"` + launchInstance + `"}`,
		"missing expected":    `{"kind":"fresh","cd":false}`,
		"empty expected":      `{"kind":"fresh","cd":false,"expected_tmux_instance":""}`,
		"resume without line": `{"kind":"resume","cd":false,"expected_tmux_instance":"` + launchInstance + `"}`, // no record
	} {
		w := r.post(r.code, body)
		assert.Equal(t, http.StatusBadRequest, w.Code, name)
		assert.Equal(t, "bad_request", codeOfBody(t, w), name)
	}
	assert.Empty(t, r.typed())
	assert.Equal(t, http.StatusNotFound, r.post("nosuch", `{"kind":"fresh","cd":false,"expected_tmux_instance":"`+launchInstance+`"}`).Code)
}

func TestLaunch_FreshTypesTheStartLine(t *testing.T) {
	r := newLaunchRig(t)
	w := r.launch("fresh", false)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	assert.JSONEq(t, `{"line":"claude"}`, w.Body.String())
	assert.Equal(t, []string{"claude\r"}, r.typed())
}

func TestLaunch_ResumeTypesTheTemplateLine(t *testing.T) {
	r := newLaunchRig(t)
	r.lastRun("codex", "ses_Ab-1", "/w/proj")
	w := r.launch("resume", false)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	assert.JSONEq(t, `{"line":"codex resume ses_Ab-1"}`, w.Body.String())
	assert.Equal(t, []string{"codex resume ses_Ab-1\r"}, r.typed())
}

// The cd prefix is `cd '<path>' && <line>` with the path ALWAYS single-quoted, `'` as `'\”`: the clients preview the button
// with the same rule, so the daemon never quotes "when needed". An ordinary path is quoted too.
func TestLaunch_CdPrefixFormatIsPinned(t *testing.T) {
	for path, want := range map[string]string{
		"/w/proj":              `cd '/w/proj' && claude`,
		"/w/with space/dir":    `cd '/w/with space/dir' && claude`,
		"/w/it's/here":         `cd '/w/it'\''s/here' && claude`,
		"/w/$HOME/`x`;rm -rf ": "cd '/w/$HOME/`x`;rm -rf ' && claude",
		"/w/a\"b":              `cd '/w/a"b' && claude`,
	} {
		r := newLaunchRig(t)
		r.lastRun("cc", "S1", path)
		w := r.launch("fresh", true)
		require.Equal(t, http.StatusOK, w.Code, w.Body.String())
		assert.Equal(t, []string{want + "\r"}, r.typed(), path)
		assert.JSONEq(t, `{"line":`+quoteJSON(want)+`}`, w.Body.String())
	}
}

func quoteJSON(s string) string { b, _ := json.Marshal(s); return string(b) }

func TestLaunch_CdOnlyWhenAskedAndOnlyWhenTheDirectoryMoves(t *testing.T) {
	r := newLaunchRig(t)
	r.lastRun("cc", "S1", "/w/proj")
	require.Equal(t, http.StatusOK, r.launch("resume", false).Code)
	assert.Equal(t, []string{"claude --resume S1\r"}, r.typed(), "cd:false adds no prefix")

	r2 := newLaunchRig(t)
	r2.fake.SetPaneCwd("$0:", "/w/proj")
	r2.lastRun("cc", "S1", "/w/proj")
	require.Equal(t, http.StatusOK, r2.launch("resume", true).Code)
	assert.Equal(t, []string{"claude --resume S1\r"}, r2.typed(), "already there: no prefix even when asked")

	r3 := newLaunchRig(t) // no record: nothing to move to
	require.Equal(t, http.StatusOK, r3.launch("fresh", true).Code)
	assert.Equal(t, []string{"claude\r"}, r3.typed())
}

func TestLaunch_GenerationConflict(t *testing.T) {
	r := newLaunchRig(t)
	for _, exp := range []string{"999:1", "not a generation, #{x}", "111:1000 "} {
		b, _ := json.Marshal(map[string]any{"kind": "fresh", "cd": false, "expected_tmux_instance": exp})
		w := r.post(r.code, string(b))
		assert.Equal(t, http.StatusConflict, w.Code, exp)
		assert.Equal(t, "generation_conflict", codeOfBody(t, w), exp)
	}
	assert.Empty(t, r.typed())
}

// The server restarts after the daemon sampled the generation and before the keys go: the executor's one-connection check
// declines, and the answer is the same conflict with nothing typed.
func TestLaunch_ServerRestartsBetweenPlanAndSend(t *testing.T) {
	r := newLaunchRig(t)
	r.fake.SetActivePaneMetadataHook(func(string) { r.fake.SetInstance("222:2000") })
	w := r.launch("fresh", false)
	assert.Equal(t, http.StatusConflict, w.Code)
	assert.Equal(t, "generation_conflict", codeOfBody(t, w))
	assert.Empty(t, r.typed())
}

func TestLaunch_AgentRunningIs409(t *testing.T) {
	r := newLaunchRig(t)
	r.facts.running = true
	w := r.launch("fresh", false)
	assert.Equal(t, http.StatusConflict, w.Code)
	assert.Equal(t, "agent_running", codeOfBody(t, w))
	assert.Empty(t, r.typed())
}

func TestLaunch_BusyAtPlanTimeIs409(t *testing.T) {
	r := newLaunchRig(t)
	r.fake.SetActivePaneMetadata("work", tmux.TmuxPaneMetadata{SessionID: "$0", SessionName: "work", PaneCurrentCommand: "npm"})
	r.mod.invalidateListCache()
	w := r.launch("fresh", false)
	assert.Equal(t, http.StatusConflict, w.Code)
	assert.Equal(t, "busy", codeOfBody(t, w))
	assert.Empty(t, r.typed())
}

// The plan read the pane at a prompt; before the keys go, something starts in it. The server-side guard declines: 409 busy and
// not one key typed.
func TestLaunch_PaneTurnsBusyBetweenPlanAndSend(t *testing.T) {
	r := newLaunchRig(t)
	r.fake.SetActivePaneMetadataHook(func(string) { r.fake.SetPaneCommand("$0:", "npm") })
	w := r.launch("fresh", false)
	assert.Equal(t, http.StatusConflict, w.Code)
	assert.Equal(t, "busy", codeOfBody(t, w))
	assert.Empty(t, r.typed(), "nothing is typed into a pane that started a command")
	// and the session is not locked out by it
	r.fake.SetPaneCommand("$0:", "zsh")
	r.fake.SetActivePaneMetadataHook(nil)
	assert.Equal(t, http.StatusOK, r.launch("fresh", false).Code)
}

// Generation and busy both failing: the generation is the one reported (the server checks it first).
func TestLaunch_GenerationAndBusyBothFail_GenerationWins(t *testing.T) {
	r := newLaunchRig(t)
	r.fake.SetActivePaneMetadataHook(func(string) { r.fake.SetInstance("222:2000"); r.fake.SetPaneCommand("$0:", "npm") })
	w := r.launch("fresh", false)
	assert.Equal(t, "generation_conflict", codeOfBody(t, w))
	assert.Empty(t, r.typed())
}

func TestLaunch_SendFailureIs500AndReleasesTheClaim(t *testing.T) {
	r := newLaunchRig(t)
	r.fake.FailSendKeys = true
	assert.Equal(t, http.StatusInternalServerError, r.launch("fresh", false).Code)
	r.fake.FailSendKeys = false
	assert.Equal(t, http.StatusOK, r.launch("fresh", false).Code, "a send that did not go out frees the session at once")
}

// --- the claim --------------------------------------------------------------

func TestLaunch_TwoConcurrentPostsOneSendsOneIsAlreadyLaunching(t *testing.T) {
	r := newLaunchRig(t)
	const n = 8
	codes := make([]int, n)
	bodies := make([]string, n)
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			w := r.launch("fresh", false)
			codes[i], bodies[i] = w.Code, w.Body.String()
		}(i)
	}
	close(start)
	wg.Wait()
	ok, launching := 0, 0
	for i := range codes {
		switch {
		case codes[i] == http.StatusOK:
			ok++
		case codes[i] == http.StatusConflict && strings.Contains(bodies[i], "already_launching"):
			launching++
		default:
			t.Errorf("unexpected %d %s", codes[i], bodies[i])
		}
	}
	assert.Equal(t, 1, ok, "exactly one launch goes out")
	assert.Equal(t, n-1, launching)
	assert.Len(t, r.typed(), 1)
}

func TestLaunch_ClaimReleasedBySessionStartAndByTheTimer(t *testing.T) {
	r := newLaunchRig(t)
	require.Equal(t, http.StatusOK, r.launch("fresh", false).Code)
	w := r.launch("fresh", false)
	assert.Equal(t, "already_launching", codeOfBody(t, w), "a launch that went out holds the session")

	r.mod.ReleaseLaunchClaim("elsewhere") // another session's SessionStart
	assert.Equal(t, "already_launching", codeOfBody(t, r.launch("fresh", false)))
	r.mod.ReleaseLaunchClaim("work") // this session's root SessionStart
	assert.Equal(t, http.StatusOK, r.launch("fresh", false).Code)

	orig := launchClaimTTL
	launchClaimTTL = 30 * time.Millisecond
	t.Cleanup(func() { launchClaimTTL = orig })
	r2 := newLaunchRig(t)
	require.Equal(t, http.StatusOK, r2.launch("fresh", false).Code)
	assert.Eventually(t, func() bool { return r2.launch("fresh", false).Code == http.StatusOK }, 2*time.Second, 10*time.Millisecond, "released after the TTL")
}

// A SessionStart of an OLD generation (its event arrives after tmux restarted) must not release the new generation's claim:
// the name is re-resolved to its current instance.
func TestLaunch_SessionStartReleasesTheCurrentGenerationsClaimOnly(t *testing.T) {
	r := newLaunchRig(t)
	require.Equal(t, http.StatusOK, r.launch("fresh", false).Code)
	assert.True(t, r.mod.claims.held(launchClaimKey(r.code, launchInstance)))
	r.fake.SetInstance("222:2000") // tmux restarted: the claim was for the previous generation
	r.mod.ReleaseLaunchClaim("work")
	assert.True(t, r.mod.claims.held(launchClaimKey(r.code, launchInstance)), "a claim of another generation is not this SessionStart's")
}

// The nonce: launch A is released by its session's SessionStart, launch B takes the session, and A's late cleanup (its timer, a
// failed-send release) arrives. It must not release B's claim.
func TestLaunchClaims_LateCleanupOfAnEarlierLaunchCannotReleaseALaterOne(t *testing.T) {
	c := newLaunchClaims()
	key := launchClaimKey("code", "111:1")
	a, ok := c.take(key)
	require.True(t, ok)
	c.releaseKey(key) // SessionStart
	b, ok := c.take(key)
	require.True(t, ok)
	require.NotEqual(t, a, b, "a nonce is never reused")
	c.release(key, a) // A's late timer / failed-send cleanup
	assert.True(t, c.held(key), "B's claim survived A's late release")
	_, again := c.take(key)
	assert.False(t, again)
	c.release(key, b)
	assert.False(t, c.held(key))
}

func TestLaunchClaimKey_DoesNotConfuseCodeAndInstanceBoundaries(t *testing.T) {
	assert.NotEqual(t, launchClaimKey("ab", "c:1"), launchClaimKey("a", "bc:1"))
}
