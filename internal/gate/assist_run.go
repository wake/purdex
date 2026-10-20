package gate

// Running a command on an assist host (assist-dispatch plan X2a.3–7, spec §4): the working tree is committed (assist_snapshot.go),
// pushed to the host's repo under refs/assist/<job>, checked out as a detached worktree with the dependencies linked from the host's
// own cache, and the command is run there by the host's own `pdx gate`, its output streamed back. The job directory, the ref and
// the pid file are removed whatever happened. A failure before the host's gate printed the admitted mark is a failure before the start
// (nothing ran there); after it, the exit code is the command's.

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/wake/purdex/internal/wwd/snapshot"
)

// AssistRun describes one run sent to an assist host.
type AssistRun struct {
	Host string // the ssh alias
	Job  string // names the ref, the worktree, the pid file and the tag on the host; "" makes one
	Repo string // the repo's name on the host (~/Assist/repos.noindex/<Repo>); "" is the top level's basename
	SSH  string // the ssh program ("" = ssh)
	Git  string // the git program ("" = git)
	// Sigs are the interrupts to act on, when the caller already listens for them (a waiting gate does): then a signal that came while
	// the snapshot, the push or the preparation ran is not lost, and nothing is started after it. nil: RunAssist listens itself.
	Sigs <-chan os.Signal
	// AdmitWait is how long the host's gate may take to admit the command (its --wait-max); 0 = 60 s.
	AdmitWait time.Duration
}

// assistAfterPrepare is a seam for tests: it runs when the preparation on the host is done, before the command is started there.
var assistAfterPrepare func()

// AssistResult is what a run on an assist host came to.
type AssistResult struct {
	Code    int  // the command's exit code when Started
	Started bool // the host's gate admitted the command and started it; false is a failure before the start (Err says why)
	// Reclaimed says the host took the run back while it ran (assist-off): its .reclaimed mark was there at the end.
	Reclaimed bool
	TransferS float64 // snapshot, push and preparation
	RunS      float64 // the run itself
	Job       string
	Err       error // why it did not start (or, after the start, what went wrong around it)
	// Cancelled says a signal or the caller's context ended the run before it started (nothing to fall back to: the caller was cancelled).
	Cancelled bool
	// NotAdmitted says the host's gate did not admit the command within its wait (exit 75), which holds the host off for less.
	NotAdmitted bool
	// Uncertain says the connection went before the host's gate was seen to admit the command: it may have started there, so the
	// command must not be run again somewhere else.
	Uncertain bool
	// CleanupErr is set when the job could not be removed from the host (it is left for the sweep of assist-check).
	CleanupErr error
}

func newJobID() string {
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// shellQuote quotes one word for a POSIX shell.
func shellQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

func shellJoin(argv []string) string {
	q := make([]string, len(argv))
	for i, a := range argv {
		q[i] = shellQuote(a)
	}
	return strings.Join(q, " ")
}

// validWord keeps a job or repo name to what is safe in a path and a remote command.
func validWord(s string) bool {
	if s == "" || len(s) > 80 {
		return false
	}
	for _, r := range s {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_' || r == '.') {
			return false
		}
	}
	return !strings.HasPrefix(s, ".")
}

// remotePrepare makes the job directory and links the dependencies (args: repo job sha). One ssh. The dependencies of each lockfile
// directory (the root and spa/) are kept in ~/Assist/deps.noindex/<repo>/<dir>-<hash of the lockfile> and linked in; a missing set is
// installed once and moved there; the two most recently used per directory are kept.
const remotePrepare = `set -e
. ~/Assist/assist.env 2>/dev/null || true
repo=$1; job=$2; sha=$3
R=$HOME/Assist/repos.noindex/$repo; W=$HOME/Assist/tmp.noindex/$job
mkdir -p "$HOME/Assist/tmp.noindex" "$HOME/Assist/jobs.noindex" "$HOME/Assist/deps.noindex/$repo"
git -C "$R" worktree add -q --detach "$W" "$sha"
for d in . spa; do
  [ -f "$W/$d/pnpm-lock.yaml" ] || continue
  label=$(echo "$d" | tr -c 'a-zA-Z0-9\n' '_')
  h=$(shasum -a 256 "$W/$d/pnpm-lock.yaml" | cut -c1-16)
  D="$HOME/Assist/deps.noindex/$repo/$label-$h"
  if [ ! -d "$D/node_modules" ]; then
    if mkdir "$D.lock" 2>/dev/null; then
      echo $$ > "$D.lock/pid"
      if (cd "$W/$d" && pnpm install --frozen-lockfile) >&2 && mkdir -p "$D" && mv "$W/$d/node_modules" "$D/node_modules"; then
        rm -rf "$D.lock"
      else
        rm -rf "$D.lock"; echo "install of $d failed" >&2; exit 1
      fi
    else
      # another job is installing this set: wait for it. A lock is dead when its installer's process is gone (or, with no pid in it
      # yet, when it is two minutes old); a long install by a live installer is waited for, never taken over.
      i=0
      while [ ! -d "$D/node_modules" ] && [ -d "$D.lock" ] && [ "$i" -lt 1800 ]; do
        ip=$(cat "$D.lock/pid" 2>/dev/null || true)
        if [ -n "$ip" ]; then
          kill -0 "$ip" 2>/dev/null || { rm -rf "$D.lock"; echo "took over the dead install lock of $d" >&2; break; }
        elif [ -n "$(find "$D.lock" -maxdepth 0 -mmin +2 2>/dev/null)" ]; then
          rm -rf "$D.lock"; break
        fi
        sleep 1; i=$((i+1))
      done
      if [ ! -d "$D/node_modules" ] && mkdir "$D.lock" 2>/dev/null; then # the dead installer's work, done here
        echo $$ > "$D.lock/pid"
        if (cd "$W/$d" && pnpm install --frozen-lockfile) >&2 && mkdir -p "$D" && mv "$W/$d/node_modules" "$D/node_modules"; then rmdir "$D.lock" 2>/dev/null || rm -rf "$D.lock"; else rm -rf "$D.lock"; echo "install of $d failed" >&2; exit 1; fi
      fi
      [ -d "$D/node_modules" ] || { echo "the install of $d by another job did not finish" >&2; exit 1; }
    fi
  fi
  touch "$D"
  ln -s "$D/node_modules" "$W/$d/node_modules"
  used=$(for l in "$HOME"/Assist/tmp.noindex/*/node_modules "$HOME"/Assist/tmp.noindex/*/spa/node_modules; do [ -L "$l" ] && readlink "$l"; done)
  ls -dt "$HOME/Assist/deps.noindex/$repo/$label-"*[0-9a-f] 2>/dev/null | tail -n +3 | while read -r old; do
    case "$used" in *"$old/node_modules"*) ;; *) rm -rf "$old" ;; esac
  done
done
`

// remoteCleanup removes the job whatever happened (args: repo job) and says whether the host took the run back.
const remoteCleanup = `repo=$1; job=$2
R=$HOME/Assist/repos.noindex/$repo; W=$HOME/Assist/tmp.noindex/$job; J=$HOME/Assist/jobs.noindex/$job
rc=0
if [ -e "$J.reclaimed" ]; then echo reclaimed=1; fi
git -C "$R" worktree remove --force "$W" >/dev/null 2>&1 || rm -rf "$W" || rc=1
git -C "$R" worktree prune >/dev/null 2>&1 || rc=1
git -C "$R" update-ref -d "refs/assist/$job" >/dev/null 2>&1 || rc=1
rm -f "$J.pid" "$J.reclaimed" "$J.cancel" || rc=1
exit $rc
`

// remoteKill ends the job's process group (args: job): TERM now, KILL ten seconds later.
const remoteKill = `touch "$HOME/Assist/jobs.noindex/$1.cancel"
J=$HOME/Assist/jobs.noindex/$1.pid
[ -f "$J" ] || exit 0
p=$(cat "$J"); [ -n "$p" ] || exit 0
g=$(ps -o pgid= -p "$p" 2>/dev/null | tr -d ' ')
[ -n "$g" ] && [ "$g" -gt 1 ] || exit 0
kill -TERM -- "-$g" 2>/dev/null
( sleep 10; kill -KILL -- "-$g" 2>/dev/null ) >/dev/null 2>&1 &
`

// ssh runs a remote script on the host with positional arguments: the script is the command's text, so nothing in the arguments is
// ever parsed as shell.
func (a AssistRun) sshCmd(ctx context.Context, script string, args ...string) *exec.Cmd {
	prog := a.SSH
	if prog == "" {
		prog = "ssh"
	}
	remote := "sh -c " + shellQuote(script) + " assist " + shellJoin(args)
	return exec.CommandContext(ctx, prog, "-o", "BatchMode=yes", "-T", a.Host, remote)
}

// RunAssist sends the command of o to the host and runs it there, streaming its output to o.Stdout and o.Stderr. It never runs the
// command on this host.
func (g *Gate) RunAssist(ctx context.Context, o Options, a AssistRun) (res AssistResult) {
	res = AssistResult{Job: a.Job}
	if res.Job == "" {
		res.Job = newJobID()
	}
	a.Job = res.Job
	fail := func(err error) AssistResult { res.Err = err; return res }
	if len(o.Argv) == 0 {
		return fail(errors.New("no command"))
	}
	if !validWord(a.Host) || !validWord(a.Job) {
		return fail(fmt.Errorf("bad host or job name %q / %q", a.Host, a.Job))
	}
	stderrW := outOr(o.Stderr, os.Stderr)
	began := g.Now()
	dir := g.cwdOf(o)
	sigs := a.Sigs
	if sigs == nil {
		var stop func()
		sigs, stop = g.signals()
		defer stop()
	}
	// Until the command is started there, a signal ends the snapshot, the push or the preparation at once and nothing is started
	// after it; from the start on, the loop below acts on it.
	pre, cancelPre := context.WithCancel(ctx)
	defer cancelPre()
	var (
		preSig    atomic.Value // the os.Signal that came before the start
		preQuit   = make(chan struct{})
		preExited = make(chan struct{})
	)
	go func() {
		defer close(preExited)
		select {
		case s := <-sigs:
			preSig.Store(s)
			cancelPre()
		case <-preQuit:
		}
	}()
	var stopOnce sync.Once
	// idempotent, and deferred at once: every way out of RunAssist stops the watcher, or it would take the next signal of the caller
	stopWatching := func() { stopOnce.Do(func() { close(preQuit); <-preExited }) }
	defer stopWatching()
	signalled := func() (os.Signal, bool) { s, ok := preSig.Load().(os.Signal); return s, ok }
	cancelledNow := func(s os.Signal) AssistResult {
		res.Cancelled, res.Code, res.Err = true, 128+signalNumber(s), errors.New("cancelled")
		return res
	}
	snap, err := snapshot.SnapshotTree(pre, a.Git, dir)
	if err != nil {
		stopWatching()
		if s, ok := signalled(); ok {
			return cancelledNow(s)
		}
		return fail(fmt.Errorf("snapshot: %w", err))
	}
	repo := a.Repo
	if repo == "" {
		repo = strings.TrimSuffix(baseName(snap.Root), ".git")
	}
	if !validWord(repo) {
		return fail(fmt.Errorf("bad repo name %q", repo))
	}
	// Everything below this line must be cleaned up, whatever happens.
	cleanup := func() {
		c, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		var out bytes.Buffer
		cmd := a.sshCmd(c, remoteCleanup, repo, a.Job)
		cmd.Stdout = &out
		if err := cmd.Run(); err != nil {
			res.CleanupErr = fmt.Errorf("cleanup of job %s on %s: %w", a.Job, a.Host, err)
			g.say("%v", res.CleanupErr)
		}
		res.Reclaimed = strings.Contains(out.String(), "reclaimed=1")
	}
	defer cleanup()

	// push the snapshot under refs/assist/<job>
	gitProg := a.Git
	if gitProg == "" {
		gitProg = "git"
	}
	sshProg := a.SSH
	if sshProg == "" {
		sshProg = "ssh"
	}
	push := exec.CommandContext(pre, gitProg, "-C", snap.Root, "push", "-q", "--force", a.Host+":Assist/repos.noindex/"+repo, snap.SHA+":refs/assist/"+a.Job)
	push.Env = append(os.Environ(), "GIT_SSH_COMMAND="+shellQuote(sshProg)+" -o BatchMode=yes")
	var perr bytes.Buffer
	push.Stderr = &perr
	push.WaitDelay = 2 * time.Second // a signal ends it: what it left behind must not hold this run
	if err := push.Run(); err != nil {
		stopWatching()
		if s, ok := signalled(); ok {
			return cancelledNow(s)
		}
		return fail(fmt.Errorf("push to %s: %w: %s", a.Host, err, strings.TrimSpace(perr.String())))
	}
	prep := a.sshCmd(pre, remotePrepare, repo, a.Job, snap.SHA)
	var prepErr bytes.Buffer
	prep.Stderr = &prepErr
	prep.WaitDelay = 2 * time.Second
	if err := prep.Run(); err != nil {
		stopWatching()
		if s, ok := signalled(); ok {
			return cancelledNow(s)
		}
		return fail(fmt.Errorf("prepare on %s: %w: %s", a.Host, err, lastLines(prepErr.String(), 3)))
	}
	if assistAfterPrepare != nil {
		assistAfterPrepare()
	}
	stopWatching()
	if s, ok := signalled(); ok { // it came as the preparation ended: the command is not started
		return cancelledNow(s)
	}
	select { // one that the watcher had not taken yet is still in the channel
	case s := <-sigs:
		return cancelledNow(s)
	default:
	}
	res.TransferS = g.Now().Sub(began).Seconds()

	// run it there: the host's own gate admits it
	wait := a.AdmitWait
	if wait <= 0 {
		wait = 60 * time.Second
	}
	nonce := newJobID()
	script := `. ~/Assist/assist.env 2>/dev/null || true
repo=$1; job=$2; rel=$3; wait=$4; nonce=$5; shift 5
cd "$HOME/Assist/tmp.noindex/$job/$rel" || exit 70
echo $$ > "$HOME/Assist/jobs.noindex/$job.pid"
[ ! -e "$HOME/Assist/jobs.noindex/$job.cancel" ] || exit 75  # cancelled before it got here
PDX_GATE_ADMITTED_MARK=$nonce PDX_GATE_TAG="assist:$job" exec pdx gate --wait-max "$wait" -- "$@"
`
	args := append([]string{repo, a.Job, snap.Rel, fmt.Sprintf("%d", int(wait.Seconds())), nonce}, o.Argv...)
	run := a.sshCmd(context.Background(), script, args...) // ended by the signal path below, not by the context
	filter := newMarkFilter(stderrW, nonce)
	run.Stdout = outOr(o.Stdout, os.Stdout)
	run.Stderr = filter
	run.Stdin = nil
	run.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	run.WaitDelay = 2 * time.Second // what the ssh left behind must not hold the output pipes, and this run, open for ever
	g.say("running on %s (assist): %s", a.Host, strings.Join(truncateArgv(o.Argv), " "))
	if err := run.Start(); err != nil {
		return fail(fmt.Errorf("ssh to %s: %w", a.Host, err))
	}
	runStart := g.Now()
	done := make(chan error, 1)
	go func() { done <- run.Wait() }()
	var cancelled bool
	var killAt <-chan time.Time
loop:
	for {
		select {
		case err := <-done:
			filter.Flush()
			res.RunS = g.Now().Sub(runStart).Seconds()
			code := exitCode(err, run)
			res.Started = filter.sawAdmit && !filter.sawNoStart
			if code == 255 && !cancelled {
				// ssh itself failed (the connection went): whatever the host is still running for the job is ended before its
				// directory is removed, and the exit code is not the command's
				g.endRemote(a, a.Job)
				if res.Started {
					res.Code = code
					res.Err = fmt.Errorf("the ssh connection to %s was lost while the command ran; it was ended on the host", a.Host)
					break loop
				}
			}
			switch {
			case res.Started:
				res.Code = code
			case cancelled:
				res.Code = 130
				res.Cancelled = true
				res.Err = errors.New("cancelled")
			default:
				res.NotAdmitted = code == ExitRefused && !filter.sawNoStart
				res.Uncertain = code == 255
				res.Err = failedBeforeStart(a.Host, code, filter)
			}
			break loop
		case sig := <-sigs:
			if !cancelled {
				cancelled = true
				g.endRemote(a, a.Job)
				killAt = time.After(15 * time.Second)
				_ = sig
			}
		case <-ctx.Done():
			if !cancelled {
				cancelled = true
				g.endRemote(a, a.Job)
				killAt = time.After(15 * time.Second)
			}
			ctx = context.Background()
		case <-killAt:
			killAt = nil
			_ = syscall.Kill(-run.Process.Pid, syscall.SIGKILL) // the ssh itself
		}
	}
	return res
}

// endRemote ends the job's process group on the host: TERM, and KILL ten seconds later.
func (g *Gate) endRemote(a AssistRun, job string) {
	c, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := a.sshCmd(c, remoteKill, job).Run(); err != nil {
		g.say("ending the run on %s: %v", a.Host, err)
	}
}

// failedBeforeStart says why a run ended without the host's gate admitting the command.
func failedBeforeStart(host string, code int, f *markFilter) error {
	switch {
	case f.sawNoStart:
		return fmt.Errorf("%s admitted the command but could not start it", host)
	case code == ExitRefused:
		return fmt.Errorf("%s did not admit the command in time", host)
	case code == 255:
		return fmt.Errorf("ssh to %s failed", host)
	}
	return fmt.Errorf("%s ended with %d before the command started", host, code)
}

func baseName(p string) string {
	p = strings.TrimRight(p, "/")
	if i := strings.LastIndex(p, "/"); i >= 0 {
		return p[i+1:]
	}
	return p
}

func lastLines(s string, n int) string {
	s = strings.TrimSpace(s)
	lines := strings.Split(s, "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, " | ")
}

var _ io.Writer = (*markFilter)(nil)
