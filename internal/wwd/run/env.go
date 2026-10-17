// Package run executes one job's command on this host (spec §5): an argv started directly, never through a shell, in a clean
// environment, as its own process group, with a deadline, a cancel, and its output kept in capped files. Every process it
// starts carries WWD_JOB=<job id>, the mark that facts and the worker use to tell a job's tree from anyone else's.
package run

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"
)

// MarkEnv is the environment variable that names the job a process belongs to.
const MarkEnv = "WWD_JOB"

// fixedNames are set by the runner and cannot be allowed or overridden by a manifest: they point at the job's workspace or
// identify the job.
var fixedNames = map[string]bool{
	"HOME": true, "TMPDIR": true, "XDG_CONFIG_HOME": true, "XDG_CACHE_HOME": true, "XDG_DATA_HOME": true,
	"XDG_STATE_HOME": true, "XDG_RUNTIME_DIR": true, MarkEnv: true,
}

// BuildEnv is the environment of a job's command: PATH and TERM, HOME, TMPDIR and the XDG directories inside the workspace,
// the job mark, and what the task's env list asks for. An entry is "NAME" (taken from parent when it has it) or "NAME=value"
// (fixed). parent is only read for allowed names; nothing else of it reaches the command.
func BuildEnv(jobID, workspace string, taskEnv, parent []string) ([]string, error) {
	if jobID == "" || strings.ContainsAny(jobID, "=\x00\n") {
		return nil, fmt.Errorf("run: invalid job id %q", jobID)
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
		MarkEnv:           jobID,
		// Purdex state directories, inside the workspace so a job never reads or writes the host's own.
		"PDX_GATE_DIR":         filepath.Join(workspace, ".pdx", "gate"),
		"PDX_CODEX_STATE_ROOT": filepath.Join(workspace, ".pdx", "codex"),
	}
	if p, ok := parentVal["PATH"]; ok {
		env["PATH"] = p
	}
	for _, e := range taskEnv {
		name, value, fixed := strings.Cut(e, "=")
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

// envValue returns the value of name in an environment list ("" and false when absent).
func envValue(env []string, name string) (string, bool) {
	for _, kv := range env {
		if k, v, ok := strings.Cut(kv, "="); ok && k == name {
			return v, true
		}
	}
	return "", false
}
