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
	remoteMu    sync.Mutex
	remoteCache = map[string]cachedRemote{}
	// runRemote prints the URL of origin in dir; replaced in tests. A git that ran and found no origin returns
	// errNoRemote; any other error (a timeout, git missing) is a failure that is not kept.
	runRemote = func(ctx context.Context, dir string) (string, error) {
		out, err := exec.CommandContext(ctx, "git", "-C", dir, "remote", "get-url", "origin").Output()
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			return "", errNoRemote
		}
		return strings.TrimSpace(string(out)), err
	}
)

type cachedRemote struct {
	url string
	at  time.Time
}

func clearRemoteCache() {
	remoteMu.Lock()
	remoteCache = map[string]cachedRemote{}
	remoteMu.Unlock()
}

// RemoteURL is the https URL of the repository dir belongs to, from its `origin`, for linking `#123` refs
// (`https://github.com/owner/repo`): without credentials, user, port or `.git`. "" when there is no origin, or the origin
// is a local path, or git could not answer. The answer is kept for ten minutes per folder (a missing origin included; a
// failure that is not "no origin" is not), so a request never forks git twice in a row.
func RemoteURL(ctx context.Context, dir string) string {
	remoteMu.Lock()
	if c, ok := remoteCache[dir]; ok && now().Sub(c.at) < cacheTTL {
		remoteMu.Unlock()
		return c.url
	}
	remoteMu.Unlock()
	ctx, cancel := context.WithTimeout(ctx, gitTimeout)
	defer cancel()
	raw, err := runRemote(ctx, dir)
	if err != nil && !errors.Is(err, errNoRemote) {
		return ""
	}
	url := ""
	if n := NormalizeRemote(raw); err == nil && n != "" && !strings.HasPrefix(n, "file:") { // no origin: whatever came with it is not one
		url = "https://" + n
	}
	remoteMu.Lock()
	remoteCache[dir] = cachedRemote{url: url, at: now()}
	remoteMu.Unlock()
	return url
}
