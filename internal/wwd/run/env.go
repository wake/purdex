// Package run executes one job's command on this host (spec §5): an argv started directly, never through a shell, in a clean
// environment (PATH, TERM, USER and LOGNAME of the executing account, a workspace HOME), as its own process group, with a deadline, a cancel, and its output kept in capped files. Every process it
// starts carries WWD_JOB=<job id>, the mark that facts and the worker use to tell a job's tree from anyone else's.
package run

import (
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/wake/purdex/internal/wwd/facts"
)

// MarkEnv is the environment variable that names the job a process belongs to; OriginEnv names the connection it came in on (a job
// id is only unique per origin): the mark of a job is both.
const (
	MarkEnv   = facts.JobMarkerEnv
	OriginEnv = facts.OriginMarkerEnv
)

// fixedNames are set by the runner and cannot be allowed or overridden by a manifest: they point at the job's workspace or
// identify the job.
var fixedNames = map[string]bool{
	"HOME": true, "TMPDIR": true, "XDG_CONFIG_HOME": true, "XDG_CACHE_HOME": true, "XDG_DATA_HOME": true,
	"XDG_STATE_HOME": true, "XDG_RUNTIME_DIR": true, MarkEnv: true, OriginEnv: true,
	"USER": true, "LOGNAME": true,
}

// currentUser names the account this process runs as ("" when it cannot be told); a variable so a test can replace it.
var currentUser = func() string {
	if u, err := user.Current(); err == nil && u.Username != "" {
		return u.Username
	}
	return os.Getenv("USER")
}

// BuildEnv is the environment of a job's command: PATH and TERM, USER and LOGNAME (the account the runner runs as, left out when
// it cannot be told; macOS tools such as xcodebuild fail without a username), HOME, TMPDIR and the XDG directories inside the workspace,
// the job mark (the id, and the origin tag when there is one), and what the task's env list asks for. An entry is "NAME" (taken from parent when it has it) or "NAME=value"
// (fixed). parent is only read for allowed names; nothing else of it reaches the command.
func BuildEnv(jobID, origin, workspace string, taskEnv, parent []string) ([]string, error) {
	// the mark must be one the facts' shared check recognises, or the sweep and the attribution could not find the job
	tag, id, ok := facts.MarkFromEnv([]string{MarkEnv + "=" + jobID, OriginEnv + "=" + origin})
	if !ok {
		return nil, fmt.Errorf("run: invalid job id %q", jobID)
	}
	if origin != "" && tag != origin {
		return nil, fmt.Errorf("run: invalid origin tag %q", origin)
	}
	if !filepath.IsAbs(workspace) {
		return nil, fmt.Errorf("run: the workspace %q must be an absolute path", workspace)
	}
	parentVal := map[string]string{}
	for _, kv := range parent {
		if k, v, ok := strings.Cut(kv, "="); ok {
			parentVal[k] = v
		}
	}
	env := map[string]string{
		"TERM":            "dumb",
		"HOME":            filepath.Join(workspace, ".home"),
		"TMPDIR":          filepath.Join(workspace, ".tmp"),
		"XDG_CONFIG_HOME": filepath.Join(workspace, ".config"),
		"XDG_CACHE_HOME":  filepath.Join(workspace, ".cache"),
		"XDG_DATA_HOME":   filepath.Join(workspace, ".local", "share"),
		"XDG_STATE_HOME":  filepath.Join(workspace, ".local", "state"),
		MarkEnv:           id,
		// Purdex state directories, inside the workspace so a job never reads or writes the host's own.
		"PDX_GATE_DIR":         filepath.Join(workspace, ".pdx", "gate"),
		"PDX_CODEX_STATE_ROOT": filepath.Join(workspace, ".pdx", "codex"),
	}
	if origin != "" {
		env[OriginEnv] = origin
	}
	if name := currentUser(); name != "" {
		env["USER"], env["LOGNAME"] = name, name
	}
	if p, ok := parentVal["PATH"]; ok {
		env["PATH"] = p
	}
	for _, e := range taskEnv {
		name, value, fixed := strings.Cut(e, "=")
		if !envName.MatchString(name) {
			return nil, fmt.Errorf("run: %q is not an environment variable name", name)
		}
		if fixedNames[name] || strings.HasPrefix(name, "WWD_") || strings.HasPrefix(name, "PDX_") {
			return nil, fmt.Errorf("run: the task env may not set %s", name)
		}
		if fixed {
			env[name] = value
		} else if v, ok := parentVal[name]; ok {
			env[name] = v
		}
	}
	out := make([]string, 0, len(env))
	for k, v := range env {
		out = append(out, k+"="+v)
	}
	sort.Strings(out)
	return out, nil
}

// envName is what a variable name may look like; a name such as "--unset=X" would be taken as an option by env(1).
var envName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// envValue returns the value of name in an environment list ("" and false when absent).
func envValue(env []string, name string) (string, bool) {
	for _, kv := range env {
		if k, v, ok := strings.Cut(kv, "="); ok && k == name {
			return v, true
		}
	}
	return "", false
}
