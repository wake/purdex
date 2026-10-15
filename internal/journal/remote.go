package journal

import (
	"context"
	"errors"
	"fmt"
	"github.com/wake/purdex/internal/execstat"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// errNoRemote is "this folder has no `origin`" (a repository without one, or no repository): an answer, not a failure.
var errNoRemote = errors.New("journal: no origin")

// maxRemoteCache bounds the answers kept, so a daemon that sees many folders over a long time does not keep them all.
var maxRemoteCache = 256

var (
	remoteMu     sync.Mutex
	remoteCache  = map[string]cachedRemote{}
	remoteFlight = map[string]*flight{} // folder -> the git run in progress
	// runRemote prints the URL of origin in dir; replaced in tests. A git that ran and found no origin returns
	// errNoRemote; any other error (a timeout, git missing) is a failure that is not kept.
	runRemote = func(ctx context.Context, dir string) (string, error) {
		cmd := execstat.CommandContext(ctx, "journal.git-remote", "git", "-C", dir, "remote", "get-url", "origin")
		cmd.Env = gitEnv(os.Environ()) // the daemon's GIT_DIR / GIT_WORK_TREE must not redirect the question
		out, err := cmd.Output()
		if err := classifyRemote(ctx, err); err != nil {
			return "", err
		}
		return strings.TrimSpace(string(out)), nil
	}
)

type cachedRemote struct {
	url string
	at  time.Time
}

// flight is one git run that every caller for the same folder waits for.
type flight struct {
	done chan struct{}
	url  string
}

func clearRemoteCache() {
	remoteMu.Lock()
	remoteCache = map[string]cachedRemote{}
	remoteMu.Unlock()
}

// classifyRemote reads the outcome of `git remote get-url origin`: nil for a success; errNoRemote for a git that ran and
// exited non-zero (no such remote: 2, not a repository: 128); the error itself for anything else. A git cut short by its
// context (a timeout or a cancel kills it, which also looks like a non-zero exit) is the context's error, never "no origin".
func classifyRemote(ctx context.Context, err error) error {
	if err == nil {
		return nil
	}
	if ctxErr := ctx.Err(); ctxErr != nil {
		return ctxErr
	}
	var exit *exec.ExitError
	if errors.As(err, &exit) && exit.ExitCode() > 0 {
		return errNoRemote
	}
	return err
}

// RemoteURL is the https URL of the repository dir belongs to, from its `origin`, for linking `#123` refs
// (`https://github.com/owner/repo`): without credentials, user, port or `.git`. "" when there is no origin, or the origin
// is a local path, or git could not answer. The answer is kept for ten minutes per folder (a missing origin included; a
// failure that is not "no origin" is not; at most maxRemoteCache answers are kept). Callers that come while a folder's git
// is running share that run, so a page that loads many refs never forks git more than once for a folder. The run belongs
// to none of the callers: it ends by its own timeout, and a caller whose context ends stops waiting without affecting the
// others.
func RemoteURL(ctx context.Context, dir string) string {
	remoteMu.Lock()
	if c, ok := remoteCache[dir]; ok && now().Sub(c.at) < cacheTTL {
		remoteMu.Unlock()
		return c.url
	}
	f, running := remoteFlight[dir]
	if !running {
		f = &flight{done: make(chan struct{})}
		remoteFlight[dir] = f
		go f.run(context.Background(), dir)
	}
	remoteMu.Unlock()
	select {
	case <-f.done:
		return f.url
	case <-ctx.Done():
		return ""
	}
}

// run asks git for dir's origin, records the answer and wakes the waiters. A panic is a failure, not an answer, and the
// folder is not left registered as running.
func (f *flight) run(base context.Context, dir string) {
	var raw string
	var err error
	defer func() {
		if r := recover(); r != nil {
			raw, err = "", fmt.Errorf("journal: remote url of %s: %v", dir, r)
		}
		url := ""
		if n := NormalizeRemote(raw); err == nil && n != "" && !strings.HasPrefix(n, "file:") { // no origin: whatever came with it is not one
			url = "https://" + n
		}
		remoteMu.Lock()
		delete(remoteFlight, dir)
		if err == nil || errors.Is(err, errNoRemote) {
			pruneRemoteCacheLocked(now())
			remoteCache[dir] = cachedRemote{url: url, at: now()}
		}
		remoteMu.Unlock()
		f.url = url
		close(f.done)
	}()
	ctx, cancel := context.WithTimeout(base, gitTimeout) // base is no caller's context: the run ends by its own timeout
	defer cancel()
	raw, err = runRemote(ctx, dir)
}

// pruneRemoteCacheLocked drops the answers that have expired, and starts over when what is left is still at the cap.
func pruneRemoteCacheLocked(at time.Time) {
	for k, c := range remoteCache {
		if at.Sub(c.at) >= cacheTTL {
			delete(remoteCache, k)
		}
	}
	if len(remoteCache) >= maxRemoteCache {
		clear(remoteCache)
	}
}
