//go:build e2e

// End-to-end tests of the wwd binary: real processes, real sockets and TCP, a real git repository (WWD-4a-4c).
//
// Run with: go test -tags e2e -run TestE2E ./cmd/wwd/  (ask the load watcher first: this builds wwd and starts two or three of them).
//
// Safety, because the machine running this has a production wwd: every process gets its own HOME, data directory, socket and
// token under a temporary directory, listens on 127.0.0.1 on a port picked here (never the default one), and is told about no
// peer but its siblings. The tests talk to those sockets only, check that the answer carries the alias they gave it, and kill
// nothing but the pids they started (the wwd processes, and the sleeper a test job wrote down).
package main

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/wake/purdex/internal/wwd/client"
	"github.com/wake/purdex/internal/wwd/submit"
	"github.com/wake/purdex/internal/wwd/wire"
)

var e2eBin string

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "wwde-bin")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	e2eBin = filepath.Join(dir, "wwd")
	if out, err := exec.Command("go", "build", "-o", e2eBin, ".").CombinedOutput(); err != nil {
		fmt.Fprintf(os.Stderr, "go build wwd: %v\n%s", err, out)
		os.RemoveAll(dir)
		os.Exit(2)
	}
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

// --- the world of one test

type world struct {
	t    *testing.T
	root string // a short temporary directory: unix socket paths are limited to about 100 bytes
	repo string // the origin's working tree, the submitter's directory
	pool string // the token file every process shares
}

func newWorld(t *testing.T) *world {
	t.Helper()
	root, err := os.MkdirTemp("", "wwde")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		// A child of wwd that outlives it (the `go version` of the tool probe writes its telemetry under HOME as it ends) can
		// recreate a directory while it is being removed: remove until nothing is left.
		for i := 0; i < 10; i++ {
			if os.RemoveAll(root) == nil {
				if _, err := os.Stat(root); os.IsNotExist(err) {
					return
				}
			}
			time.Sleep(200 * time.Millisecond)
		}
		t.Errorf("could not remove %s", root)
	})
	w := &world{t: t, root: root, repo: filepath.Join(root, "e2erepo"), pool: filepath.Join(root, "pool.token")}
	if err := os.WriteFile(w.pool, []byte(strings.Repeat("ab12", 16)+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	w.git(root, "init", "-q", "-b", "main", w.repo)
	w.write(".wwd/tasks.toml", `[tasks.hello]
run = "/bin/echo hi"
args = "none"
labels = ["repo:e2erepo"]
timeout = "2m"

[tasks.sleeper]
run = "/bin/sh sleeper.sh"
args = "none"
labels = ["repo:e2erepo"]
timeout = "5m"
`)
	w.write("sleeper.sh", "echo $$ > \"$HOME/sleeper.pid\"\nexec /bin/sleep 300\n")
	w.git(w.repo, "-c", "user.name=e2e", "-c", "user.email=e2e@example.invalid", "add", "-A")
	w.git(w.repo, "-c", "user.name=e2e", "-c", "user.email=e2e@example.invalid", "commit", "-q", "-m", "e2e")
	return w
}

func (w *world) write(rel, body string) {
	w.t.Helper()
	p := filepath.Join(w.repo, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		w.t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		w.t.Fatal(err)
	}
}

func (w *world) git(dir string, args ...string) {
	w.t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		w.t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

// clone makes a prepared clone for a worker host.
func (w *world) clone(name string) string {
	dst := filepath.Join(w.root, name+"-clone", "e2erepo")
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		w.t.Fatal(err)
	}
	w.git(w.root, "clone", "-q", w.repo, dst)
	return dst
}

// --- one wwd process

type procOpts struct {
	alias     string
	worker    bool
	clone     string // a prepared clone; empty: this host has none
	peers     map[string]*proc
	ownerFile string // fault.owner_active_file (also turns the owner rule on)
}

type proc struct {
	t       *testing.T
	alias   string
	home    string
	sock    string
	workDir string
	port    int
	cmd     *exec.Cmd
	exited  chan struct{}
	logPath string
}

func freePort(t *testing.T) int {
	t.Helper()
	for i := 0; i < 20; i++ {
		l, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		port := l.Addr().(*net.TCPAddr).Port
		l.Close()
		if port != wire.Port && port != wire.Port-1 { // never the production wwd's ports
			return port
		}
	}
	t.Fatal("no free port")
	return 0
}

func (w *world) start(o procOpts, port int) *proc {
	t := w.t
	t.Helper()
	home := filepath.Join(w.root, o.alias)
	p := &proc{t: t, alias: o.alias, home: home, sock: filepath.Join(home, "w.sock"), workDir: filepath.Join(home, "work"), port: port,
		exited: make(chan struct{}), logPath: filepath.Join(home, "wwd.log")}
	if p.port == 0 {
		p.port = freePort(t)
	}
	if err := os.MkdirAll(home, 0o700); err != nil {
		t.Fatal(err)
	}
	var cfg strings.Builder
	fmt.Fprintf(&cfg, "alias = %q\nport = %d\ntailnet_addr = \"127.0.0.1\"\ndata_dir = %q\nsocket = %q\ntoken_file = %q\nwork_root = %q\n",
		o.alias, p.port, filepath.Join(home, "data"), p.sock, w.pool, p.workDir)
	fmt.Fprintf(&cfg, "\n[worker]\nenabled = %v\nheavy = 1\nlight = 1\n", o.worker)
	if o.clone != "" {
		fmt.Fprintf(&cfg, "\n[repos]\ne2erepo = %q\n", o.clone)
	}
	for alias, peer := range o.peers {
		fmt.Fprintf(&cfg, "\n[[peers]]\nalias = %q\naddr = \"127.0.0.1:%d\"\n", alias, peer.port)
	}
	if o.ownerFile != "" {
		fmt.Fprintf(&cfg, "\n[owner]\nenabled = true\n\n[fault]\nenabled = true\nowner_active_file = %q\n", o.ownerFile)
	}
	cfgPath := filepath.Join(home, "config.toml")
	if err := os.WriteFile(cfgPath, []byte(cfg.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	logf, err := os.Create(p.logPath)
	if err != nil {
		t.Fatal(err)
	}
	p.cmd = exec.Command(e2eBin, "-config", cfgPath)
	p.cmd.Env = []string{"HOME=" + home, "PATH=" + os.Getenv("PATH"), "LANG=en_US.UTF-8"}
	p.cmd.Stdout, p.cmd.Stderr = logf, logf
	if err := p.cmd.Start(); err != nil {
		t.Fatal(err)
	}
	go func() { p.cmd.Wait(); logf.Close(); close(p.exited) }()
	t.Cleanup(p.cleanup)
	p.waitReady()
	return p
}

func (p *proc) api() *client.Client { return client.New(p.sock) }

// waitReady waits for the socket to answer, and checks the answer is ours: an alias that is not the one this process was given
// means the socket is somebody else's, and nothing more is done through it.
func (p *proc) waitReady() {
	p.t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case <-p.exited:
			p.t.Fatalf("%s exited at start:\n%s", p.alias, p.logTail())
		default:
		}
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		info, err := p.api().Info(ctx)
		cancel()
		if err == nil {
			if info.Alias != p.alias {
				p.t.Fatalf("socket %s answered as %q, want %q: not touching it", p.sock, info.Alias, p.alias)
			}
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	p.t.Fatalf("%s not ready:\n%s", p.alias, p.logTail())
}

func (p *proc) logTail() string {
	b, _ := os.ReadFile(p.logPath)
	if len(b) > 4000 {
		b = b[len(b)-4000:]
	}
	return string(b)
}

// kill9 is the crash the lease protects against.
func (p *proc) kill9() {
	p.t.Helper()
	select {
	case <-p.exited:
		return
	default:
	}
	p.cmd.Process.Signal(syscall.SIGKILL)
	<-p.exited
}

// cleanup stops the process (TERM, then KILL) and ends the sleepers its jobs started. Only pids this test started or that the
// test's own jobs wrote down are touched.
func (p *proc) cleanup() {
	select {
	case <-p.exited:
	default:
		p.cmd.Process.Signal(syscall.SIGTERM)
		select {
		case <-p.exited:
		case <-time.After(10 * time.Second):
			p.cmd.Process.Signal(syscall.SIGKILL)
			<-p.exited
		}
	}
	for _, pid := range p.sleeperPids() {
		killSleeper(pid)
	}
	if p.t.Failed() {
		p.t.Logf("--- %s log tail ---\n%s", p.alias, p.logTail())
	}
}

// sleeperPids are the pids the sleeper task wrote under this process's work root ($HOME of a job is its directory).
func (p *proc) sleeperPids() []int {
	files, _ := filepath.Glob(filepath.Join(p.workDir, "*", "sleeper.pid"))
	var pids []int
	for _, f := range files {
		if b, err := os.ReadFile(f); err == nil {
			if n, err := strconv.Atoi(strings.TrimSpace(string(b))); err == nil && n > 1 {
				pids = append(pids, n)
			}
		}
	}
	return pids
}

// killSleeper ends pid only if it still is the `sleep 300` the sleeper task execs (a pid file can outlive its process).
func killSleeper(pid int) {
	out, err := exec.Command("ps", "-p", strconv.Itoa(pid), "-o", "command=").Output()
	if err != nil || !strings.Contains(string(out), "/bin/sleep 300") {
		return
	}
	syscall.Kill(pid, syscall.SIGKILL)
}

func alive(pid int) bool { return syscall.Kill(pid, 0) == nil }

// --- helpers for the scenarios

func eventually(t *testing.T, what string, within time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(within)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatalf("timed out after %s waiting for %s", within, what)
}

// submitTask prepares a submission from the origin's working tree, exactly as `pdx test` does, and sends it to the origin's socket.
func (w *world) submitTask(origin *proc, task string) string {
	w.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	prep, err := submit.Prepare(ctx, submit.Request{Dir: w.repo, Task: task, Submitter: wire.Submitter{Session: "e2e", Ref: "e2e", Host: origin.alias}})
	if err != nil {
		w.t.Fatalf("prepare %s: %v", task, err)
	}
	resp, err := origin.api().Submit(ctx, prep.Body)
	if err != nil {
		w.t.Fatalf("submit %s: %v", task, err)
	}
	if resp.Result != nil {
		w.t.Fatalf("submit %s: answered from the result cache", task)
	}
	return resp.ID
}

func (p *proc) job(id string) (wire.Job, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return p.api().Job(ctx, id)
}

func (p *proc) stateIs(id string, want ...wire.JobState) func() bool {
	return func() bool {
		j, err := p.job(id)
		if err != nil {
			return false
		}
		for _, s := range want {
			if j.State == s {
				return true
			}
		}
		return false
	}
}

func (p *proc) workerOn(on bool) {
	p.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cur, err := p.api().Worker(ctx)
	if err != nil {
		p.t.Fatal(err)
	}
	cur.Enabled = on
	if _, err := p.api().SetWorker(ctx, cur); err != nil {
		p.t.Fatal(err)
	}
}

// --- scenarios

// The processes find each other: each reports the other reachable in its pool, through TCP with the shared token.
func TestE2E_TwoDaemonsSeeEachOther(t *testing.T) {
	w := newWorld(t)
	a := w.start(procOpts{alias: "ea"}, 0)
	b := w.start(procOpts{alias: "eb", peers: map[string]*proc{"ea": a}}, 0)
	// eb's config names ea (ea's names nobody), so the pool to read is eb's
	eventually(t, "eb to see ea reachable", 60*time.Second, func() bool {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		pool, err := b.api().Pool(ctx)
		if err != nil {
			return false
		}
		for _, peer := range pool.Peers {
			if peer.Info.Alias == "ea" && peer.Reachable {
				return true
			}
		}
		return false
	})
}

// A submitted task goes from the origin's queue to a worker on another process, runs, and its result and log come back.
func TestE2E_JobRunsOnThePeerWorker(t *testing.T) {
	w := newWorld(t)
	pr := w.startPair("eb", true)
	a := pr.origin
	id := w.submitTask(a, "hello")
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	job, err := a.api().WaitJob(ctx, id, nil)
	if err != nil {
		t.Fatalf("wait: %v", err)
	}
	if job.State != wire.StateSucceeded || job.Result == nil || job.Result.Exit == nil || *job.Result.Exit != 0 {
		t.Fatalf("job = %+v", job)
	}
	if job.Result.Host != "eb" {
		t.Fatalf("ran on %q, want the peer eb", job.Result.Host)
	}
	if s := job.Result.Summary; s == nil || !strings.Contains(strings.Join(s.Tail, "\n"), "hi") {
		t.Fatalf("summary = %+v", job.Result.Summary)
	}
	log, err := a.api().Log(ctx, id)
	if err != nil || !strings.Contains(string(log), "hi") {
		t.Fatalf("log = %q err=%v", log, err)
	}
}

// Cancelling a running job ends its command on the worker.
func TestE2E_CancelStopsTheRunningCommand(t *testing.T) {
	w := newWorld(t)
	pr := w.startPair("eb", true)
	a := pr.origin
	id := w.submitTask(a, "sleeper")
	eventually(t, "the sleeper to run", 90*time.Second, func() bool { return a.stateIs(id, wire.StateRunning)() && len(pr.worker.sleeperPids()) > 0 })
	pid := pr.worker.sleeperPids()[0]
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := a.api().Cancel(ctx, id); err != nil {
		t.Fatal(err)
	}
	eventually(t, "the job to be cancelled", 60*time.Second, a.stateIs(id, wire.StateCancelled))
	eventually(t, "the sleeper to end", 30*time.Second, func() bool { return !alive(pid) })
}

// The worker dies without a word (kill -9): its lease lapses, the job is queued again and another worker takes it.
func TestE2E_KilledWorkerLeaseLapsesAndAnotherTakesTheJob(t *testing.T) {
	if testing.Short() {
		t.Skip("waits for a lease to lapse")
	}
	w := newWorld(t)
	pr := w.startPair("eb", true)
	a := pr.origin
	c := w.start(procOpts{alias: "ec", worker: false, clone: w.clone("ec"), peers: map[string]*proc{"ea": a}}, 0)
	id := w.submitTask(a, "sleeper")
	eventually(t, "the sleeper to run on eb", 90*time.Second, a.stateIs(id, wire.StateRunning))
	pr.worker.kill9()
	eventually(t, "the lapsed job to be queued again", 5*time.Minute, a.stateIs(id, wire.StateQueued))
	c.workerOn(true)
	eventually(t, "ec to run the job", 5*time.Minute, func() bool {
		j, err := a.job(id)
		return err == nil && j.State == wire.StateRunning && j.Claim != nil && j.Claim.Worker == "ec"
	})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = a.api().Cancel(ctx, id)
}

// The owner comes back (the fault hook stands in for the keyboard): the worker releases what it runs for reclaim, which does
// not count as an attempt, and takes it again once the owner is gone.
func TestE2E_OwnerReturnReclaimsTheRunningJob(t *testing.T) {
	if testing.Short() {
		t.Skip("waits for the owner rule")
	}
	w := newWorld(t)
	ownerFile := filepath.Join(w.root, "owner-active")
	a := w.start(procOpts{alias: "ea"}, 0)
	w.start(procOpts{alias: "eb", worker: true, clone: w.clone("eb"), peers: map[string]*proc{"ea": a}, ownerFile: ownerFile}, 0)
	id := w.submitTask(a, "sleeper")
	eventually(t, "the sleeper to run on eb", 90*time.Second, a.stateIs(id, wire.StateRunning))
	attempt := func() int {
		j, _ := a.job(id)
		if j.Claim == nil {
			return 0
		}
		return j.Claim.Attempt
	}
	first := attempt()
	if err := os.WriteFile(ownerFile, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	eventually(t, "the job to be released for reclaim", 2*time.Minute, a.stateIs(id, wire.StateQueued))
	if err := os.Remove(ownerFile); err != nil {
		t.Fatal(err)
	}
	eventually(t, "eb to take the job again", 2*time.Minute, a.stateIs(id, wire.StateClaimed, wire.StateRunning))
	if got := attempt(); got != first {
		t.Fatalf("a reclaim counted as an attempt: %d then %d", first, got)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = a.api().Cancel(ctx, id)
}

// pair is an origin and one worker peer.
type pair struct {
	origin *proc
	worker *proc
}

// startPair starts an origin (ea, no worker) and a worker peer that names it, with its own prepared clone.
func (w *world) startPair(workerAlias string, workerOn bool) *pair {
	a := w.start(procOpts{alias: "ea"}, 0)
	b := w.start(procOpts{alias: workerAlias, worker: workerOn, clone: w.clone(workerAlias), peers: map[string]*proc{"ea": a}}, 0)
	return &pair{origin: a, worker: b}
}
