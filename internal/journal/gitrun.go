package journal

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// keptGitEnv are the only GIT_* variables a journal git process inherits from the daemon: how to reach a remote and where
// the user's own configuration is. Everything else (GIT_DIR, GIT_WORK_TREE, GIT_INDEX_FILE, GIT_COMMON_DIR, the object
// directories, GIT_NAMESPACE, GIT_CONFIG_PARAMETERS ...) could send a command to another repository than the one it is
// run for, so it is dropped.
var keptGitEnv = map[string]bool{
	"GIT_SSH": true, "GIT_SSH_COMMAND": true, "GIT_SSH_VARIANT": true, "GIT_ASKPASS": true,
	"GIT_CONFIG_GLOBAL": true, "GIT_CONFIG_SYSTEM": true, "GIT_CONFIG_NOSYSTEM": true,
}

// gitEnv is the daemon's environment without the repository-redirecting GIT_* variables, plus the journal's settings: no
// prompts, English messages (the push code reads git's output), no editor, and the journal's identity.
func gitEnv(environ []string) []string {
	env := make([]string, 0, len(environ)+8)
	sshSet := false
	for _, kv := range environ {
		name, _, _ := strings.Cut(kv, "=")
		if strings.HasPrefix(name, "GIT_") && !keptGitEnv[name] {
			continue
		}
		if name == "GIT_SSH_COMMAND" {
			sshSet = true
		}
		env = append(env, kv)
	}
	env = append(env, "LC_ALL=C", "GIT_TERMINAL_PROMPT=0", "GIT_EDITOR=true", "GIT_ALLOW_PROTOCOL=file:https:http:ssh:git",
		"GIT_AUTHOR_NAME=Purdex journal", "GIT_AUTHOR_EMAIL=journal@purdex.local",
		"GIT_COMMITTER_NAME=Purdex journal", "GIT_COMMITTER_EMAIL=journal@purdex.local")
	if !sshSet {
		env = append(env, "GIT_SSH_COMMAND=ssh -o BatchMode=yes -o ConnectTimeout=20")
	}
	return env
}

var urlUserinfo = regexp.MustCompile(`(://)[^/@\s]+@`)

// redactURLs hides the user and password of any URL in s (a remote's credentials must not reach the log).
func redactURLs(s string) string { return urlUserinfo.ReplaceAllString(s, "${1}***@") }

// git runs `git args` (in dir when not empty) with a timeout, the scrubbed environment, and no hooks and no signing. The
// combined output comes back with the error.
func (g *Git) git(ctx context.Context, dir string, timeout time.Duration, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	full := append([]string{"-c", "core.hooksPath=" + os.DevNull, "-c", "commit.gpgsign=false"}, args...)
	cmd := exec.CommandContext(ctx, "git", full...)
	cmd.Dir = dir
	cmd.Env = gitEnv(os.Environ())
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	err := cmd.Run()
	text := strings.TrimSpace(out.String())
	if err != nil {
		if len(text) > 300 {
			text = text[:300]
		}
		return text, fmt.Errorf("git %s: %w: %s", redactURLs(strings.Join(args, " ")), err, redactURLs(text))
	}
	return text, nil
}

func gitVersion(ctx context.Context) (int, int, error) {
	out, err := exec.CommandContext(ctx, "git", "--version").Output()
	if err != nil {
		return 0, 0, err
	}
	m := versionRe.FindStringSubmatch(string(out))
	if m == nil {
		return 0, 0, fmt.Errorf("unreadable git version %q", out)
	}
	major, _ := strconv.Atoi(m[1])
	minor, _ := strconv.Atoi(m[2])
	return major, minor, nil
}
