package journal

import (
	"context"
	"errors"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// errNoRemote is "this folder has no `origin`" (a repository without one, or no repository): an answer, not a failure.
var errNoRemote = errors.New("journal: no origin")

var (
	remoteMu     sync.Mutex
	remoteCache  = map[string]cachedRemote{}
	remoteFlight = map[string]*flight{} // folder -> the git run in progress
	// runRemote prints the URL of origin in dir; replaced in tests. A git that ran and found no origin returns
	// errNoRemote; any other error (a timeout, git missing) is a failure that is not kept.
	runRemote = func(ctx context.Context, dir string) (string, error) {
		out, err := exec.CommandContext(ctx, "git", "-C", dir, "remote", "get-url", "origin").Output()
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

// flight is one git run that callers for the same folder wait for.
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
// failure that is not "no origin" is not), and callers that come while a folder's git is running share that run, so a page
// that loads many refs never forks git more than once for a folder.
func RemoteURL(ctx context.Context, dir string) string {
	remoteMu.Lock()
	if c, ok := remoteCache[dir]; ok && now().Sub(c.at) < cacheTTL {
		remoteMu.Unlock()
		return c.url
	}
	if f, ok := remoteFlight[dir]; ok { // someone is asking git already: wait for that answer
		remoteMu.Unlock()
		select {
		case <-f.done:
			return f.url
		case <-ctx.Done():
			return ""
		}
	}
	f := &flight{done: make(chan struct{})}
	remoteFlight[dir] = f
	remoteMu.Unlock()

	gctx, cancel := context.WithTimeout(ctx, gitTimeout)
	raw, err := runRemote(gctx, dir)
	cancel()
	url := ""
	if n := NormalizeRemote(raw); err == nil && n != "" && !strings.HasPrefix(n, "file:") { // no origin: whatever came with it is not one
		url = "https://" + n
	}
	remoteMu.Lock()
	delete(remoteFlight, dir)
	if err == nil || errors.Is(err, errNoRemote) {
		remoteCache[dir] = cachedRemote{url: url, at: now()}
	}
	remoteMu.Unlock()
	f.url = url
	close(f.done)
	return url
}
