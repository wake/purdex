package remotejob

import (
	"testing"
)

// checkEnv holds a task's env to the names a remote task may take from the worker's environment: the fixed list, and the names the host
// declared in [jobs.env] (#3933).
func TestCheckEnvAllowsTheNamesTheHostDeclared(t *testing.T) {
	host := map[string]string{"PLAYWRIGHT_BROWSERS_PATH": "/x"}
	if err := checkEnv("j_0123456789abcdef", t.TempDir(), []string{"PLAYWRIGHT_BROWSERS_PATH"}, host); err != nil {
		t.Errorf("a declared name: %v", err)
	}
	if err := checkEnv("j_0123456789abcdef", t.TempDir(), []string{"PLAYWRIGHT_BROWSERS_PATH"}, nil); code(t, err) != CodeEnvNotAllowed {
		t.Errorf("an undeclared name must still be refused: %v", err)
	}
	if err := checkEnv("j_0123456789abcdef", t.TempDir(), []string{"AWS_SECRET_ACCESS_KEY"}, host); code(t, err) != CodeEnvNotAllowed {
		t.Errorf("a name the host did not declare must still be refused: %v", err)
	}
	// the fixed list is unchanged by a declaration
	if err := checkEnv("j_0123456789abcdef", t.TempDir(), []string{"LANG", "TZ", "CI"}, host); err != nil {
		t.Errorf("the inheritable names: %v", err)
	}
}

// What the runner owns cannot be taken or set, declared by the host or not: the runner's own check comes after.
func TestCheckEnvStillRefusesWhatTheRunnerOwns(t *testing.T) {
	host := map[string]string{"HOME": "/x", "WWD_JOB": "x"} // the config refuses these; the check does not rely on that
	for _, name := range []string{"HOME", "WWD_JOB"} {
		if err := checkEnv("j_0123456789abcdef", t.TempDir(), []string{name}, host); code(t, err) != CodeEnvInvalid {
			t.Errorf("%s: %v", name, err)
		}
	}
}
