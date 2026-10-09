package claudeenv

import (
	"os"
	"slices"
	"strings"
	"testing"
)

func TestIsSessionVar_IdentityNotConfiguration(t *testing.T) {
	for _, n := range SessionVars {
		if !IsSessionVar(n) {
			t.Errorf("%s is not seen as a session variable", n)
		}
	}
	for _, n := range []string{"CLAUDE_CODE_SESSION_FUTURE", "CLAUDE_CODE_MESSAGING_NEXT"} {
		if !IsSessionVar(n) {
			t.Errorf("%s (a member of a session family) is kept", n)
		}
	}
	for _, n := range []string{"CLAUDE_CONFIG_DIR", "CLAUDE_CODE_PLUGIN_DIRS", "CLAUDE_CODE_DISABLE_TERMINAL_TITLE", "CLAUDE_CODE_USE_BEDROCK",
		"CLAUDE_CODE_OAUTH_TOKEN", "ANTHROPIC_API_KEY", "PATH", "TMUX", "CLAUDE", ""} {
		if IsSessionVar(n) {
			t.Errorf("%s is configuration, not session identity, and must be kept", n)
		}
	}
}

func TestFilter_DropsIdentityKeepsTheRestInOrder(t *testing.T) {
	env := []string{"PATH=/bin", "CLAUDECODE=1", "CLAUDE_CODE_MESSAGING_TOKEN=secret", "HOME=/h", "CLAUDE_CONFIG_DIR=/c", "CLAUDE_CODE_SESSION_ID=s", "EMPTY="}
	kept, removed := Filter(env)
	if want := []string{"PATH=/bin", "HOME=/h", "CLAUDE_CONFIG_DIR=/c", "EMPTY="}; !slices.Equal(kept, want) {
		t.Fatalf("kept = %v", kept)
	}
	if want := []string{"CLAUDECODE", "CLAUDE_CODE_MESSAGING_TOKEN", "CLAUDE_CODE_SESSION_ID"}; !slices.Equal(removed, want) {
		t.Fatalf("removed = %v", removed)
	}
	for _, n := range removed {
		if strings.Contains(n, "=") {
			t.Fatalf("a removed entry carries a value: %q", n)
		}
	}
}

func TestScrubProcess_RemovesFromTheEnvironmentAndKeepsConfig(t *testing.T) {
	t.Setenv("CLAUDECODE", "1")
	t.Setenv("CLAUDE_CODE_MESSAGING_SOCKET", "/tmp/x.sock")
	t.Setenv("CLAUDE_CODE_PLUGIN_DIRS", "/plugins")
	removed := ScrubProcess()
	if !slices.Contains(removed, "CLAUDECODE") || !slices.Contains(removed, "CLAUDE_CODE_MESSAGING_SOCKET") {
		t.Fatalf("removed = %v", removed)
	}
	if _, ok := os.LookupEnv("CLAUDECODE"); ok {
		t.Fatal("CLAUDECODE is still set")
	}
	if _, ok := os.LookupEnv("CLAUDE_CODE_MESSAGING_SOCKET"); ok {
		t.Fatal("the messaging socket is still set")
	}
	if os.Getenv("CLAUDE_CODE_PLUGIN_DIRS") != "/plugins" {
		t.Fatal("configuration was removed")
	}
	if again := ScrubProcess(); len(again) != 0 {
		t.Fatalf("a second scrub found %v", again)
	}
}

func TestTmuxGlobalUnsetArgs_OneListStartsTheServerThenUnsetsEachVariable(t *testing.T) {
	args := TmuxGlobalUnsetArgs()
	if args[0] != "start-server" || args[len(args)-1] != ";" {
		t.Fatalf("args = %v", args)
	}
	var unset []string
	for i := 0; i+4 < len(args); i++ {
		if args[i] == "set-environment" && args[i+1] == "-g" && args[i+2] == "-u" {
			unset = append(unset, args[i+3])
		}
	}
	if !slices.Equal(unset, SessionVars) {
		t.Fatalf("unset = %v, want %v", unset, SessionVars)
	}
}

// The eleven names #2122 saw leaking, spelled out: dropping one from SessionVars (the tmux unset takes only
// exact names) is red here, not silently kept by the prefix rule.
func TestSessionVars_AreTheElevenSeenLeaking(t *testing.T) {
	want := []string{"CLAUDECODE", "CLAUDE_PID", "CLAUDE_EFFORT", "CLAUDE_PLUGIN_DATA", "CLAUDE_CODE_CHILD_SESSION", "CLAUDE_CODE_ENTRYPOINT",
		"CLAUDE_CODE_EXECPATH", "CLAUDE_CODE_SESSION_ID", "CLAUDE_CODE_SESSION_ATTENDED", "CLAUDE_CODE_MESSAGING_SOCKET", "CLAUDE_CODE_MESSAGING_TOKEN"}
	if !slices.Equal(SessionVars, want) {
		t.Fatalf("SessionVars = %v", SessionVars)
	}
}
