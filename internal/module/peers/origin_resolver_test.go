package peers

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	iagent "github.com/wake/purdex/internal/agent"
	ipeers "github.com/wake/purdex/internal/peers"
	"github.com/wake/purdex/internal/store"
)

// resolverFixture is a registry dir with two live entries (pid 10 in tmux,
// pid 20 without) and a bare module pointed at it; liveness is a fake, so
// nothing forks ps.
func resolverFixture(t *testing.T, live ipeers.Liveness) (*OriginResolver, string) {
	t.Helper()
	dir := t.TempDir()
	writeRegistryFixture(t, dir, "10.json", `{"pid":10,"sessionId":"sid-1","cwd":"/w","procStart":"`+targetProcStart+`","version":"2.1.270","tmux":"mt0:@1.%1","messagingSocketPath":"`+dir+`/10.sock","name":"n10","status":"idle"}`)
	writeRegistryFixture(t, dir, "20.json", `{"pid":20,"sessionId":"sid-2","cwd":"/w2","procStart":"`+targetProcStart+`","version":"2.1.270","messagingSocketPath":"`+dir+`/20.sock","name":"","status":"idle"}`)
	m := &Module{
		core:        newTestCore(t, "mlab:abc123", "mlab"), // alias "mlab" for Address
		registryDir: dir,
		liveness:    live,
		titles:      fakeTitles{"sid-1": "lead-team"},
		logf:        func(string, ...any) {},
	}
	return &OriginResolver{m: m}, dir
}

// fakeTitles is a TitleStore whose Snapshot lists one label per entry;
// Claim and Release are never reached by the resolver.
type fakeTitles map[string]string

func (f fakeTitles) Snapshot() ([]store.PeerLabel, error) {
	out := make([]store.PeerLabel, 0, len(f))
	for sid, label := range f {
		out = append(out, store.PeerLabel{SessionID: sid, Label: label, Rev: 1})
	}
	return out, nil
}
func (fakeTitles) Claim(string, string, time.Time) (store.PeerLabel, error) {
	return store.PeerLabel{}, errors.New("not used")
}
func (fakeTitles) Release(string, time.Time) (store.PeerLabel, bool, error) {
	return store.PeerLabel{}, false, errors.New("not used")
}

func TestOriginResolver_ResolveOrigin(t *testing.T) {
	r, dir := resolverFixture(t, allLiveLiveness(fixture76973ProcStart))
	o, ok, err := r.ResolveOrigin(dir + "/10.sock")
	if !ok || err != nil {
		t.Fatalf("pid 10 must resolve: ok=%v err=%v", ok, err)
	}
	if o.SessionID != "sid-1" || o.Ref != ipeers.RefID("sid-1") || o.Name != "n10" || o.PID != 10 ||
		o.ProcStart != targetProcStart || o.Cwd != "/w" || o.Tmux != "mt0:@1.%1" {
		t.Fatalf("origin = %+v", o)
	}
	if o.Title != "lead-team" || o.Address != "mlab/n10" {
		t.Fatalf("title/address = %q/%q, want lead-team / mlab/n10", o.Title, o.Address)
	}
	if o2, ok, err := r.ResolveOrigin(dir + "/20.sock"); !ok || err != nil || o2.Tmux != "" || o2.Name != "" || o2.Cwd != "/w2" ||
		o2.Title != "" || o2.Address != "mlab/"+ipeers.RefID("sid-2") {
		t.Fatalf("pid 20 = %+v ok=%v err=%v (no title; address falls back to the ref)", o2, ok, err)
	}
	r.m.titles = nil
	if o3, ok, err := r.ResolveOrigin(dir + "/10.sock"); !ok || err != nil || o3.Title != "" || o3.Address != "mlab/n10" {
		t.Fatalf("nil title store must give an empty title, not a panic: %+v ok=%v err=%v", o3, ok, err)
	}
	r.m.titles = failingTitles{}
	if o4, ok, err := r.ResolveOrigin(dir + "/10.sock"); !ok || err != nil || o4.Title != "" {
		t.Fatalf("a failing title store must give an empty title: %+v ok=%v err=%v", o4, ok, err)
	}
	// Not found is ok=false with no error: the registry was read and does not have it.
	if _, ok, err := r.ResolveOrigin(""); ok || err != nil {
		t.Fatalf("empty inbox must not resolve: ok=%v err=%v", ok, err)
	}
	if _, ok, err := r.ResolveOrigin(dir + "/99.sock"); ok || err != nil {
		t.Fatalf("unknown inbox must not resolve: ok=%v err=%v", ok, err)
	}
}

func TestOriginResolver_DeadAndProxyEntriesDoNotResolve(t *testing.T) {
	live := allLiveLiveness(fixture76973ProcStart)
	live.PidAlive = func(pid int) bool { return pid != 10 }
	r, dir := resolverFixture(t, live)
	if _, ok, err := r.ResolveOrigin(dir + "/10.sock"); ok || err != nil {
		t.Fatalf("a dead pid must not resolve: ok=%v err=%v", ok, err)
	}
	if !r.LiveSession("sid-2") || r.LiveSession("sid-1") || r.LiveSession("") {
		t.Fatal("LiveSession must follow registry liveness")
	}

	// A peer-proxy helper (D9 classification through Liveness.Info) is not a session.
	proxy := allLiveLiveness(fixture76973ProcStart)
	proxy.Info = func(pid int) (iagent.ProcessInfo, error) {
		argv := []string{"claude"}
		if pid == 20 {
			argv = []string{"pdx", "peer-proxy"}
		}
		return iagent.ProcessInfo{PID: pid, Argv: argv, StartTime: fixture76973ProcStart}, nil
	}
	r2, dir2 := resolverFixture(t, proxy)
	if _, ok, err := r2.ResolveOrigin(dir2 + "/20.sock"); ok || err != nil {
		t.Fatalf("a proxy helper must not resolve as an origin: ok=%v err=%v", ok, err)
	}
	if r2.LiveSession("sid-2") || !r2.LiveSession("sid-1") {
		t.Fatal("a proxy helper's session id must not count as live")
	}
}

func TestOriginResolver_RegistryReadErrorIsUnknownNotDead(t *testing.T) {
	r, _ := resolverFixture(t, allLiveLiveness(fixture76973ProcStart))
	// A regular file where the dir should be makes ReadRegistry fail
	// (a missing dir would read as an empty registry instead).
	file := filepath.Join(t.TempDir(), "not-a-dir")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	r.m.registryDir = file
	if !r.LiveSession("sid-1") {
		t.Fatal("a registry read error must not report the session dead")
	}
	// ResolveOrigin surfaces the read error (the handler answers 503 and the
	// CLI retries) instead of ok=false, which would read as origin_unknown.
	if _, ok, err := r.ResolveOrigin("/any.sock"); ok || err == nil {
		t.Fatalf("a registry read error must be an error, not an unknown origin: ok=%v err=%v", ok, err)
	}
}
