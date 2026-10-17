package assistmod

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/wake/purdex/internal/devices"
	"github.com/wake/purdex/internal/gate"
	"github.com/wake/purdex/internal/team"
)

// rig is a module over a temporary gate directory, fake scripts that print what a file says (no ssh), and fake team / registry
// services.
type rig struct {
	t       *testing.T
	m       *Module
	dir     string // gate dir
	ctl     string // the fake scripts' control directory
	scripts string
	origins *fakeOrigins
	teams   *fakeTeams
	seats   *fakeSeats
	mux     *http.ServeMux
	clock   *fakeClock
}

type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *fakeClock) now() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return c.t }
func (c *fakeClock) add(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

type fakeOrigins struct {
	mu   sync.Mutex
	live map[string]team.Origin // session id → origin
}

func (f *fakeOrigins) ResolveOriginByRef(ref string) (team.Origin, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, o := range f.live {
		if o.Ref == ref {
			return o, true, nil
		}
	}
	return team.Origin{}, false, nil
}
func (f *fakeOrigins) ResolveOriginBySession(id string) (team.Origin, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	o, ok := f.live[id]
	return o, ok, nil
}
func (f *fakeOrigins) ListLiveOrigins() ([]team.Origin, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []team.Origin
	for _, o := range f.live {
		out = append(out, o)
	}
	return out, nil
}

type fakeTeams struct {
	teams []team.Team
	succ  map[string][]string
}

func (f *fakeTeams) ListLiveTeams() ([]team.Team, error)        { return f.teams, nil }
func (f *fakeTeams) RefSuccessors(ref string) ([]string, error) { return f.succ[ref], nil }

type fakeSeats struct{ m map[string]team.Seat }

func (f *fakeSeats) SeatOf(id string) (team.Seat, error) {
	if s, ok := f.m[id]; ok {
		return s, nil
	}
	return team.Seat{Role: team.SeatNone}, nil
}

func newRig(t *testing.T) *rig {
	t.Helper()
	r := &rig{t: t, dir: t.TempDir(), ctl: t.TempDir(), scripts: t.TempDir(),
		origins: &fakeOrigins{live: map[string]team.Origin{}}, teams: &fakeTeams{succ: map[string][]string{}},
		seats: &fakeSeats{m: map[string]team.Seat{}}, clock: &fakeClock{t: time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC)}}
	r.m = New()
	r.m.dir = r.dir
	r.m.now = r.clock.now
	r.m.logf = t.Logf
	r.m.origins, r.m.teams, r.m.seats = r.origins, r.teams, r.seats
	r.m.timeouts = timeouts{on: 5 * time.Second, off: 5 * time.Second, check: 5 * time.Second}
	r.mux = http.NewServeMux()
	r.m.RegisterRoutes(r.mux)
	for _, n := range []string{"assist-on", "assist-off", "assist-check"} {
		script := "#!/bin/sh\n" +
			"echo \"" + n + " $* host=$ASSIST_HOST\" >> " + r.ctl + "/calls.log\n" +
			"if [ -f " + r.ctl + "/" + n + ".pidfile ]; then sleep 1000 & echo $! > " + r.ctl + "/" + n + ".child; fi\n" +
			"while [ -f " + r.ctl + "/" + n + ".block ]; do sleep 0.05; done\n" +
			"[ -f " + r.ctl + "/" + n + ".sleep ] && sleep $(cat " + r.ctl + "/" + n + ".sleep)\n" +
			"[ -f " + r.ctl + "/" + n + ".err ] && cat " + r.ctl + "/" + n + ".err >&2\n" +
			"[ -f " + r.ctl + "/" + n + ".out ] && cat " + r.ctl + "/" + n + ".out\n" +
			"exit $(cat " + r.ctl + "/" + n + ".code 2>/dev/null || echo 0)\n"
		if err := os.WriteFile(filepath.Join(r.scripts, n), []byte(script), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	r.script("assist-on", `{"ok":true,"switch":"on","detail":"on ok"}`, 0)
	r.script("assist-off", `{"ok":true,"switch":"off","ended":0,"seconds":1}`, 0)
	return r
}

// script sets what a fake script prints and its exit code.
func (r *rig) script(name, out string, code int) {
	r.t.Helper()
	os.WriteFile(filepath.Join(r.ctl, name+".out"), []byte(out), 0o644)
	os.WriteFile(filepath.Join(r.ctl, name+".code"), []byte(itoa(code)), 0o644)
}

func (r *rig) flag(name, ext string, on bool) {
	p := filepath.Join(r.ctl, name+"."+ext)
	if on {
		os.WriteFile(p, nil, 0o644)
	} else {
		os.Remove(p)
	}
}

// calls is the fake scripts' log, one line per run.
func (r *rig) calls() []string {
	b, _ := os.ReadFile(filepath.Join(r.ctl, "calls.log"))
	s := strings.TrimSpace(string(b))
	if s == "" {
		return nil
	}
	return strings.Split(s, "\n")
}

func (r *rig) count(prefix string) int {
	n := 0
	for _, c := range r.calls() {
		if strings.HasPrefix(c, prefix) {
			n++
		}
	}
	return n
}

func itoa(n int) string { b, _ := json.Marshal(n); return string(b) }

func (r *rig) addHost(alias string) {
	r.t.Helper()
	if _, err := gate.EditAssist(r.dir, func(f *gate.AssistFile) (bool, string, error) {
		f.Hosts[alias] = gate.AssistHost{SSH: alias, Scripts: r.scripts, Gen: f.TakeGen()}
		return true, "", nil
	}); err != nil {
		r.t.Fatal(err)
	}
}

func (r *rig) file() gate.AssistFile {
	r.t.Helper()
	f, err := gate.ReadAssist(r.dir)
	if err != nil {
		r.t.Fatal(err)
	}
	return f
}

// edit changes the file directly, as a daemon that died or an older run would have left it.
func (r *rig) edit(fn func(*gate.AssistFile)) {
	r.t.Helper()
	if _, err := gate.EditAssist(r.dir, func(f *gate.AssistFile) (bool, string, error) { fn(f); return true, "", nil }); err != nil {
		r.t.Fatal(err)
	}
}

// do calls a route with the admin token (or without when device is set).
func (r *rig) do(method, path string, body any) (int, map[string]any) {
	r.t.Helper()
	return r.doAs(method, path, body, false)
}

func (r *rig) doAs(method, path string, body any, device bool) (int, map[string]any) {
	r.t.Helper()
	var rd *bytes.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	} else {
		rd = bytes.NewReader(nil)
	}
	req := httptest.NewRequest(method, path, rd)
	ctx := devices.WithAdmin(context.Background())
	if device {
		ctx = devices.WithPrincipal(context.Background(), devices.Principal{ID: "d_1"})
	}
	rec := httptest.NewRecorder()
	r.mux.ServeHTTP(rec, req.WithContext(ctx))
	out := map[string]any{}
	json.Unmarshal(rec.Body.Bytes(), &out)
	return rec.Code, out
}

func (r *rig) waitFor(what string, cond func() bool) {
	r.t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	r.t.Fatalf("timed out waiting for %s", what)
}

// checkOut is a good assist-check answer.
func checkOut(sw string, repos map[string]bool) string {
	rs := map[string]any{}
	for n, ok := range repos {
		v := map[string]any{"ready": ok}
		if !ok {
			v["why"] = "repo " + n + " is not cloned"
		}
		rs[n] = v
	}
	b, _ := json.Marshal(map[string]any{"host": "x", "assist": true, "switch": sw, "repos": rs, "residue": 0})
	return string(b)
}

func gateAnswer(sw string) gate.AssistAnswer { return gateAnswerRepo(sw, "") }

func gateAnswerRepo(sw, repo string) gate.AssistAnswer {
	a := gate.AssistAnswer{Assist: true, Switch: sw, Repos: map[string]gate.AssistRepo{}}
	if repo != "" {
		a.Repos[repo] = gate.AssistRepo{Ready: true}
	}
	return a
}

func (r *rig) readyOf(gen int, a gate.AssistAnswer) gate.AssistReady {
	return gate.AssistReady{Gen: gen, At: r.clock.now(), Answer: a}
}
