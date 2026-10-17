package gate

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// The assist hosts (assist-dispatch plan X1a): assist.json in the gate directory says which hosts take work, whether each is
// switched on, and who may use them; assist-ready.json is the last check of each host. Their one writer each is the daemon's
// assist module (plan contract 8); the CLI and the gate only read.

// AssistVersion is the file version this code writes. A file with a newer one is read but never written (contract 11).
const AssistVersion = 1

// AssistFreshness is how old a check may be and still count (contract 5).
const AssistFreshness = 90 * time.Second

// ErrAssistNewer is EditAssist's refusal of a file written by a newer version.
var ErrAssistNewer = errors.New("assist_version: assist.json was written by a newer pdx; not editing it")

// AssistFile is assist.json.
type AssistFile struct {
	Version int
	NextGen int // only grows: a removed and re-added host never meets its old readiness
	Hosts   map[string]AssistHost
	Allowed []AssistAllow
	// Reserved is mlab kept free for a job that needs it (the release scripts, contract 12); past Until it reads as none.
	Reserved *AssistReserved `json:",omitempty"`
}

// AssistReserved says who has reserved mlab, why, and until when.
type AssistReserved struct {
	By, Reason string
	At, Until  time.Time
}

// ReservedNow is the reservation if it has not run out at now.
func (f AssistFile) ReservedNow(now time.Time) *AssistReserved {
	if f.Reserved == nil || !now.Before(f.Reserved.Until) {
		return nil
	}
	return f.Reserved
}

// AssistHost carries every field X2 and X3 read, empty or default until they are used (contract 11).
type AssistHost struct {
	SSH       string // the ssh alias the scripts run against (ASSIST_HOST)
	Scripts   string // directory of assist-on / assist-off / assist-check
	Slots     int    // dispatch slots mlab keeps for this host (0 = default 2)
	Can       []string
	Cannot    []string
	LoadLimit float64 // per core (0 = default 1.0)
	IdleMin   int     // CPU idle % (0 = default 30)
	HoldMin   int     // minutes held off after going over a limit (0 = default 5)
	On        bool    // what was asked
	Gen       int     // changes with every edit of this record
	Op        *AssistOp
	By        string
	Reason    string
	At        time.Time
}

// AssistOp is a switch that is running (or was, when a daemon died).
type AssistOp struct {
	Kind string // on | off
	ID   string
	At   time.Time
}

// AssistAllow is one allowed entry: a team (by team id) or a session (by ref).
type AssistAllow struct {
	Kind   string // team | session
	ID     string
	Name   string // shown
	Host   string // "" = every host
	By     string
	Reason string
	At     time.Time
}

// AssistEntry is who is asking: the team ids and refs of a session.
type AssistEntry struct{ Kind, ID string }

// AssistRepo is one repo's readiness on a host.
type AssistRepo struct {
	Ready bool   `json:"ready"`
	Why   string `json:"why,omitempty"`
}

// AssistAnswer is the JSON `assist-check --json` prints.
type AssistAnswer struct {
	Host    string                `json:"host"`
	Assist  bool                  `json:"assist"`
	Switch  string                `json:"switch"` // on | off | closing
	Repos   map[string]AssistRepo `json:"repos"`
	Residue int                   `json:"residue"`
}

// AssistReady is the last check of a host, for the generation it was run for. Err is set when the check itself failed.
type AssistReady struct {
	Gen    int          `json:"gen"`
	At     time.Time    `json:"at"`
	Answer AssistAnswer `json:"answer"`
	Err    string       `json:"err,omitempty"`
}

// TakeGen returns a fresh generation.
func (f *AssistFile) TakeGen() int {
	f.NextGen++
	return f.NextGen
}

func assistPath(dir string) string { return filepath.Join(dir, "assist.json") }

// ReadAssist reads assist.json: missing is no hosts; malformed is an error (a broken file sends nothing).
func ReadAssist(dir string) (AssistFile, error) {
	b, err := os.ReadFile(assistPath(dir))
	if os.IsNotExist(err) {
		return AssistFile{Version: AssistVersion, Hosts: map[string]AssistHost{}}, nil
	}
	if err != nil {
		return AssistFile{}, fmt.Errorf("assist_error: %w", err)
	}
	var f AssistFile
	if err := json.Unmarshal(b, &f); err != nil {
		return AssistFile{}, fmt.Errorf("assist_error: assist.json: %w", err)
	}
	if f.Hosts == nil {
		f.Hosts = map[string]AssistHost{}
	}
	for _, h := range f.Hosts {
		if h.Gen > f.NextGen { // a hand-edited or older file: never hand out a generation a host already has
			f.NextGen = h.Gen
		}
	}
	return f, nil
}

// EditAssist takes assist.lock, re-reads the file, calls fn and writes the result when fn says it changed. It returns fn's line.
func EditAssist(dir string, fn func(*AssistFile) (changed bool, line string, err error)) (string, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	unlock, err := lockFile(filepath.Join(dir, "assist.lock"))
	if err != nil {
		return "", err
	}
	defer unlock()
	f, err := ReadAssist(dir)
	if err != nil {
		return "", err
	}
	if f.Version > AssistVersion {
		return "", ErrAssistNewer
	}
	f.Version = AssistVersion
	changed, line, err := fn(&f)
	if err != nil || !changed {
		return line, err
	}
	b, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return "", err
	}
	return line, writeAtomic(assistPath(dir), b)
}

// ReadReady reads assist-ready.json (missing: nothing checked).
func ReadReady(dir string) (map[string]AssistReady, error) {
	b, err := os.ReadFile(filepath.Join(dir, "assist-ready.json"))
	if os.IsNotExist(err) {
		return map[string]AssistReady{}, nil
	}
	if err != nil {
		return nil, err
	}
	m := map[string]AssistReady{}
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, fmt.Errorf("assist-ready.json: %w", err)
	}
	return m, nil
}

// WriteReady replaces assist-ready.json; the poller is its only writer.
func WriteReady(dir string, m map[string]AssistReady) error {
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	return writeAtomic(filepath.Join(dir, "assist-ready.json"), b)
}

// AllowedFor says whether any of the entries is on the allowed list for host.
func AllowedFor(f AssistFile, host string, entries []AssistEntry) bool {
	for _, a := range f.Allowed {
		if a.Host != "" && a.Host != host {
			continue
		}
		for _, e := range entries {
			if a.Kind == e.Kind && a.ID == e.ID {
				return true
			}
		}
	}
	return false
}

// Usable is the one rule every reader uses (contract 5): the switch asked on with no operation pending, a check of the host's
// current generation that says on, that repo ready, the check at most 90 s old, and the entry allowed for the host.
func Usable(f AssistFile, ready map[string]AssistReady, now time.Time, host string, entries []AssistEntry, repo string) (bool, string) {
	h, ok := f.Hosts[host]
	if !ok {
		return false, "no such assist host"
	}
	if !h.On {
		return false, "assist is off"
	}
	if h.Op != nil {
		return false, "a switch is running"
	}
	r, ok := ready[host]
	if !ok || r.Gen != h.Gen {
		return false, "no check of this host yet"
	}
	if age := now.Sub(r.At); age > AssistFreshness || age < 0 {
		return false, "the last check is too old" // a time in the future is no check either
	}
	if r.Err != "" {
		return false, r.Err
	}
	if !r.Answer.Assist {
		return false, "assist is not installed on the host"
	}
	switch r.Answer.Switch {
	case "on":
	case "off":
		return false, "the host says off"
	default:
		return false, "the host is " + r.Answer.Switch
	}
	rp, ok := r.Answer.Repos[repo]
	if !ok {
		return false, "repo " + repo + " is not prepared on " + host
	}
	if !rp.Ready {
		if rp.Why == "" {
			return false, "repo " + repo + " is not ready"
		}
		return false, rp.Why
	}
	if !AllowedFor(f, host, entries) {
		return false, "not allowed"
	}
	return true, ""
}

// RepoFromURL is the basename of a remote URL without .git (contract 2).
func RepoFromURL(u string) string {
	u = strings.TrimRight(strings.TrimSpace(u), "/")
	u = strings.TrimSuffix(u, ".git")
	if i := strings.LastIndexAny(u, "/:"); i >= 0 {
		u = u[i+1:]
	}
	return u
}

// RepoOfDir is the repo name of a command's cwd (contract 2): the origin URL's basename, else the top level's basename, else "".
func RepoOfDir(cwd string) string {
	if cwd == "" {
		return ""
	}
	if out, err := exec.Command("git", "-C", cwd, "config", "--get", "remote.origin.url").Output(); err == nil {
		if n := RepoFromURL(string(out)); n != "" {
			return n
		}
	}
	if out, err := exec.Command("git", "-C", cwd, "rev-parse", "--show-toplevel").Output(); err == nil {
		return filepath.Base(strings.TrimSpace(string(out)))
	}
	return ""
}
