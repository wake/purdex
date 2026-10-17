package gate

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Holds keep a host out of the walk for a while after a run failed there before it started (plan contract 9): one file per
// host and repo, assist-hold/<host>@<repo>.json = {until, why} ("@" cannot be in a name, so two pairs never share a file). Many gates write them, so a write takes assist-hold.lock and keeps
// the later until; a reader takes the file as one atomic read.
const (
	holdAfterFailure = 5 * time.Minute // ssh, push, worktree, install, or the host's gate ended before the start
	holdAfterNoSlot  = 2 * time.Minute // the host's gate did not admit the command within its wait (exit 75)
)

// AssistHold is the content of a hold file.
type AssistHold struct {
	Until time.Time `json:"until"`
	Why   string    `json:"why"`
}

func holdPath(dir, host, repo string) string {
	return filepath.Join(dir, "assist-hold", host+"@"+repo+".json")
}

// HoldHost keeps host out for repo until now+d. A hold already there that lasts longer stays.
func HoldHost(dir, host, repo string, d time.Duration, why string, now time.Time) error {
	if err := os.MkdirAll(filepath.Join(dir, "assist-hold"), 0o755); err != nil {
		return err
	}
	unlock, err := lockFile(filepath.Join(dir, "assist-hold.lock"))
	if err != nil {
		return err
	}
	defer unlock()
	h := AssistHold{Until: now.Add(d), Why: cleanWhy(why)}
	if old, ok := readHold(dir, host, repo); ok && old.Until.After(h.Until) {
		return nil
	}
	b, err := json.Marshal(h)
	if err != nil {
		return err
	}
	p := holdPath(dir, host, repo)
	tmp := p + ".new"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, p)
}

func readHold(dir, host, repo string) (AssistHold, bool) {
	b, err := os.ReadFile(holdPath(dir, host, repo))
	if err != nil {
		return AssistHold{}, false
	}
	var h AssistHold
	if json.Unmarshal(b, &h) != nil {
		return AssistHold{}, false
	}
	h.Why = cleanWhy(h.Why) // a file that was written by something else, or damaged, is cut when it is read too
	return h, true
}

// maxWhy is the longest reason a hold keeps (runes): it is shown on every status look.
const maxWhy = 200

// cleanWhy is a reason as one short printable line.
func cleanWhy(s string) string {
	s = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return ' '
		}
		return r
	}, s)
	if r := []rune(s); len(r) > maxWhy {
		s = string(r[:maxWhy]) + "…"
	}
	return s
}

// HeldHost says whether host is held off for repo at now, and why.
func HeldHost(dir, host, repo string, now time.Time) (AssistHold, bool) {
	h, ok := readHold(dir, host, repo)
	if !ok || !h.Until.After(now) {
		return AssistHold{}, false
	}
	return h, true
}
