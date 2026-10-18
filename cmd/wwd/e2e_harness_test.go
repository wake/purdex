//go:build e2e

// The harness of the end-to-end tests of the wwd binary (WWD-4a-4c): real processes, real sockets and TCP, a real git repository.
//
// Safety, because the machine running this has a production wwd. Every process gets its own HOME, data directory, socket, work
// root and a token that is random per world, under a temporary directory, listens on 127.0.0.1 on a port picked here (never the
// production one), carries an alias that is unique per world, and is told about no peer but its siblings. The tests talk to those
// sockets only, check that the answer carries the alias they gave it, and kill nothing but
//   - the wwd processes they started (the handle of exec.Cmd), and
//   - the sleeper a test job started, found by its pid file AND by a command line that is unique to this world.
//
// A test run that is killed (go test -timeout, SIGKILL) cannot clean up after itself. Its directory then names the run
// (owner.pid) and the processes (wwd.pid, sleeper.pid), and the next run reaps what it left, but only when the run that owns the
// directory is gone and only the pids whose command line still proves they are ours.
//
// Accepted residue: that proof (`ps`) and the signal are two calls, as with pkill and every pid file. For a wrong process to be hit,
// the recorded process would have to exit between them AND the system hand its pid to another process within those milliseconds;
// macOS and Linux give pids out in sequence and wrap only after tens of thousands of new processes. Nothing here can make that
// airtight without a supervisor that outlives a killed test run, which is the thing that cannot be had.
package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"math/big"
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
	reapStale()
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

// cmdlineOf is the command line of pid ("" when there is no such process).
func cmdlineOf(pid int) string {
	out, err := exec.Command("ps", "-p", strconv.Itoa(pid), "-o", "command=").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func readPid(path string) int {
	b, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	n, err := strconv.Atoi(strings.TrimSpace(string(b)))
	if err != nil || n <= 1 {
		return 0
	}
	return n
}

// signalIf sends sig to pid only while its command line still satisfies ok. The check and the signal are two calls, but the
// command lines asked for are unique to one world, so a reused pid does not satisfy them.
func signalIf(pid int, sig syscall.Signal, ok func(cmd string) bool) bool {
	if pid <= 1 || !ok(cmdlineOf(pid)) {
		return false
	}
	return syscall.Kill(pid, sig) == nil
}

// staleGrace is how long a directory without an owner.pid is left alone: its run may be between MkdirTemp and writing the file.
const staleGrace = 2 * time.Minute

// reapStale ends what an earlier run that died left behind, and removes its directories.
func reapStale() {
	if old, _ := filepath.Glob(filepath.Join(os.TempDir(), "wwde-bin*")); len(old) > 0 {
		for _, d := range old {
			if fi, err := os.Stat(d); err == nil && time.Since(fi.ModTime()) > 24*time.Hour {
				os.RemoveAll(d)
			}
		}
	}
	dirs, _ := filepath.Glob(filepath.Join(os.TempDir(), "wwde[0-9]*"))
	for _, d := range dirs {
		owner := readPid(filepath.Join(d, "owner.pid"))
		if owner > 0 && strings.Contains(cmdlineOf(owner), "wwd.test") {
			continue // the run that owns it is alive (another session's)
		}
		if fi, err := os.Stat(d); owner == 0 && (err != nil || time.Since(fi.ModTime()) < staleGrace) {
			continue // a run is still setting it up (newWorld makes the directory, then writes owner.pid)
		}
		arg, _ := os.ReadFile(filepath.Join(d, "sleep-arg"))
		sleeper := "/bin/sleep " + strings.TrimSpace(string(arg))
		pids, _ := filepath.Glob(filepath.Join(d, "*", "wwd.pid"))
		for _, f := range pids {
			signalIf(readPid(f), syscall.SIGKILL, func(c string) bool { return strings.Contains(c, d) && strings.Contains(c, "-config") })
		}
		sleepers, _ := filepath.Glob(filepath.Join(d, "*", "work", "*", "sleeper.pid"))
		for _, f := range sleepers {
			signalIf(readPid(f), syscall.SIGKILL, func(c string) bool { return strings.TrimSpace(string(arg)) != "" && c == sleeper })
		}
		os.RemoveAll(d)
	}
}

// --- the world of one test

type world struct {
	t        *testing.T
	root     string // a short temporary directory: unix socket paths are limited to about 100 bytes
	id       string // makes the aliases of this world unique
	repo     string // the origin's working tree, the submitter's directory
	pool     string // the token file every process of the world shares
	sleepArg string // the argument of the sleeper's `sleep`, unique to this world, so its command line proves whose it is
}

func randHex(t *testing.T, n int) string {
	t.Helper()
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		t.Fatal(err)
	}
	return hex.EncodeToString(b)
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
	frac, err := rand.Int(rand.Reader, big.NewInt(900000))
	if err != nil {
		t.Fatal(err)
	}
	w := &world{t: t, root: root, id: randHex(t, 3), repo: filepath.Join(root, "e2erepo"), pool: filepath.Join(root, "pool.token"),
		sleepArg: fmt.Sprintf("300.%06d", frac.Int64()+100000)}
	for name, body := range map[string]string{"owner.pid": strconv.Itoa(os.Getpid()), "sleep-arg": w.sleepArg} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(body+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(w.pool, []byte(randHex(t, 32)+"\n"), 0o600); err != nil {
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
	w.write("sleeper.sh", "echo $$ > \"$HOME/sleeper.pid\"\nexec /bin/sleep "+w.sleepArg+"\n")
	ident := []string{"-c", "user.name=e2e", "-c", "user.email=e2e@example.invalid"}
	w.git(w.repo, append(ident, "add", "-A")...)
	w.git(w.repo, append(ident, "commit", "-q", "-m", "e2e")...)
	return w
}

// alias is the world's name for a host: unique, so two worlds that meet by accident do not take each other for their own.
func (w *world) alias(name string) string { return name + "-" + w.id }

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
	name      string // the host's name in this world; its alias is w.alias(name)
	worker    bool
	clone     string // a prepared clone; empty: this host has none
	peers     []*proc
	ownerFile string // fault.owner_active_file (also turns the owner rule on)
}

type proc struct {
	w       *world
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

// start runs one wwd. A port picked here can be taken by another program between the pick and wwd's bind; when the process then
// exits at start, another port is picked (up to three times). A port the caller gave is not retried.
func (w *world) start(o procOpts, port int) *proc {
	w.t.Helper()
	for attempt := 1; ; attempt++ {
		p, ok := w.launch(o, port)
		if ok {
			return p
		}
		if port != 0 || attempt == 3 {
			w.t.Fatalf("%s exited at start:\n%s", p.alias, p.logTail())
		}
		w.t.Logf("%s exited at start (attempt %d), picking another port:\n%s", p.alias, attempt, p.logTail())
	}
}

func (w *world) launch(o procOpts, port int) (*proc, bool) {
	t := w.t
	t.Helper()
	alias := w.alias(o.name)
	home := filepath.Join(w.root, o.name)
	p := &proc{w: w, t: t, alias: alias, home: home, sock: filepath.Join(home, "w.sock"), workDir: filepath.Join(home, "work"), port: port,
		exited: make(chan struct{}), logPath: filepath.Join(home, "wwd.log")}
	if p.port == 0 {
		p.port = freePort(t)
	}
	if err := os.MkdirAll(home, 0o700); err != nil {
		t.Fatal(err)
	}
	var cfg strings.Builder
	fmt.Fprintf(&cfg, "alias = %q\nport = %d\ntailnet_addr = \"127.0.0.1\"\ndata_dir = %q\nsocket = %q\ntoken_file = %q\nwork_root = %q\n",
		alias, p.port, filepath.Join(home, "data"), p.sock, w.pool, p.workDir)
	fmt.Fprintf(&cfg, "\n[worker]\nenabled = %v\nheavy = 1\nlight = 1\n", o.worker)
	if o.clone != "" {
		fmt.Fprintf(&cfg, "\n[repos]\ne2erepo = %q\n", o.clone)
	}
	for _, peer := range o.peers {
		fmt.Fprintf(&cfg, "\n[[peers]]\nalias = %q\naddr = \"127.0.0.1:%d\"\n", peer.alias, peer.port)
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
	// written for reapStale, should this run be killed before it can stop the process
	_ = os.WriteFile(filepath.Join(home, "wwd.pid"), []byte(strconv.Itoa(p.cmd.Process.Pid)+"\n"), 0o600)
	go func() { p.cmd.Wait(); logf.Close(); close(p.exited) }()
	t.Cleanup(p.cleanup)
	return p, p.waitReady()
}

func (p *proc) api() *client.Client { return client.New(p.sock) }

// waitReady waits for the socket to answer, and checks the answer is ours: an alias that is not the one this process was given
// means the socket is somebody else's, and nothing more is done through it. false: the process exited at start.
func (p *proc) waitReady() bool {
	p.t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case <-p.exited:
			return false
		default:
		}
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		info, err := p.api().Info(ctx)
		cancel()
		if err == nil {
			if info.Alias != p.alias {
				p.t.Fatalf("socket %s answered as %q, want %q: not touching it", p.sock, info.Alias, p.alias)
			}
			return true
		}
		time.Sleep(100 * time.Millisecond)
	}
	p.t.Fatalf("%s not ready:\n%s", p.alias, p.logTail())
	return false
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

// cleanup stops the process (TERM, then KILL) and ends the sleepers its jobs started.
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
		p.killSleeper(pid)
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
		if n := readPid(f); n > 0 {
			pids = append(pids, n)
		}
	}
	return pids
}

// killSleeper ends pid only while its command line is exactly this world's `sleep` (a pid file can outlive its process, and a
// pid can be reused; no other program is started with this argument).
func (p *proc) killSleeper(pid int) {
	want := "/bin/sleep " + p.w.sleepArg
	signalIf(pid, syscall.SIGKILL, func(c string) bool { return c == want })
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

// pair is an origin and one worker peer.
type pair struct {
	origin *proc
	worker *proc
}

// startPair starts an origin ("ea", no worker) and a worker peer that names it, with its own prepared clone.
func (w *world) startPair(workerName string, workerOn bool) *pair {
	a := w.start(procOpts{name: "ea"}, 0)
	b := w.start(procOpts{name: workerName, worker: workerOn, clone: w.clone(workerName), peers: []*proc{a}}, 0)
	return &pair{origin: a, worker: b}
}
