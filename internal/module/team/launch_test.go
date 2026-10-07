package teammod

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wake/purdex/internal/team"
)

// printArgv is a member command that prints each word it gets on its own line.
const printArgv = `printf '%s\n'`

// shellArgv composes a line, runs it in dir under each POSIX-family shell
// found (rc files off) and checks the output is exactly want: no word
// split, glob (dir holds bait for an unquoted "opus[1m]") or command
// substitution.
func shellArgv(t *testing.T, dir, cmd, pluginDir, model, effort string, want []string) {
	t.Helper()
	line, err := launchLine(cmd, pluginDir, model, effort)
	if err != nil || strings.HasSuffix(line, "\n") {
		t.Fatalf("%q: line %q err %v (the runner adds the newline)", pluginDir, line, err)
	}
	for _, f := range []string{"opus1", "opusm"} {
		if err := os.WriteFile(filepath.Join(dir, f), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	ran := 0
	for _, sh := range [][]string{{"sh"}, {"bash", "--norc", "--noprofile"}, {"zsh", "-f"}} {
		if bin, err := exec.LookPath(sh[0]); err == nil {
			ran++
			cmd := exec.Command(bin, append(sh[1:], "-c", line)...)
			cmd.Dir, cmd.Env = dir, []string{"PATH=/usr/bin:/bin", "HOME=" + dir}
			out, err := cmd.CombinedOutput()
			if got := strings.TrimSuffix(string(out), "\n"); err != nil || got != strings.Join(want, "\n") {
				t.Errorf("%s -c %s\n got %q (err %v)\nwant %q", sh[0], line, got, err, want)
			}
		}
	}
	matches, _ := filepath.Glob(filepath.Join(dir, "pwned*"))
	if ran == 0 || len(matches) > 0 {
		t.Fatalf("%d shells ran; commands embedded in %q ran: %v", ran, line, matches)
	}
}

// Spec §7.2 step 4 and U20 (a): the member command's words re-quoted (R2
// finding 1), then --plugin-dir and --model single-quoted ("[1m]" would
// glob), then the effort enum bare. A plugin dir with a space or a single
// quote stays one word.
func TestLaunchLine_QuotesModelAndPluginDir(t *testing.T) {
	line, err := launchLine(team.DefaultMemberCommand, "/Users/w/.config/pdx/cc-plugin/purdex", "opus[1m]", "high")
	if want := `'claude' '--dangerously-skip-permissions' --plugin-dir '/Users/w/.config/pdx/cc-plugin/purdex' --model 'opus[1m]' --effort high`; err != nil || line != want {
		t.Fatalf("line = %q (err %v)\nwant   %q", line, err, want)
	}
	for dir, quoted := range map[string]string{
		"/Users/w/Library/Application Support/Purdex/cc-plugin/purdex": `'/Users/w/Library/Application Support/Purdex/cc-plugin/purdex'`,
		"/tmp/it's/cc-plugin/purdex":                                   `'/tmp/it'\''s/cc-plugin/purdex'`,
		"/tmp/''/cc-plugin/purdex":                                     `'/tmp/'\'''\''/cc-plugin/purdex'`,
	} {
		line, err := launchLine("cld", dir, "sonnet", "")
		if want := "'cld' --plugin-dir " + quoted + " --model 'sonnet'"; err != nil || line != want {
			t.Errorf("line = %q (err %v)\nwant   %q", line, err, want)
		}
	}
	// The round trip: what the shell hands the member command.
	dir := t.TempDir()
	for _, c := range []struct{ pluginDir, model, effort string }{
		{dir + "/Application Support/cc-plugin/purdex", "opus[1m]", "xhigh"},
		{dir + "/it's/cc-plugin/purdex", "claude-opus-5-5[1m]", "max"},
		{dir + "/a  b/'x'/$(touch pwned1)/`touch pwned2`/;touch pwned3;/*/\\\"/cc-plugin/purdex", "fable", "low"},
	} {
		shellArgv(t, dir, printArgv, c.pluginDir, c.model, c.effort, []string{"--plugin-dir", c.pluginDir, "--model", c.model, "--effort", c.effort})
	}
}

// A member command with an assignment, quoted words and characters that are
// special only unquoted: the shell runs it with the assignment, its words
// as written, and every appended flag as its own word (R2 finding 1).
func TestLaunchLine_RequotesTheMemberCommand(t *testing.T) {
	line, err := launchLine(`FOO='a b' X=1 claude "--x=y z" 'p # q' =c %d`, "/d", "", "")
	if want := `FOO='a b' X='1' 'claude' '--x=y z' 'p # q' '=c' '%d' --plugin-dir '/d'`; err != nil || line != want {
		t.Fatalf("line = %q (err %v)\nwant   %q", line, err, want)
	}
	dir := t.TempDir()
	cmd := `FOO='a b' sh -c 'printf "%s\n" "$FOO" "$0" "$@"' "x;y" 'it'"'"'s'`
	shellArgv(t, dir, cmd, dir+"/p d", "opus[1m]", "max",
		[]string{"a b", "x;y", "it's", "--plugin-dir", dir + "/p d", "--model", "opus[1m]", "--effort", "max"})
}

// Without --model / --effort the member runs the host's defaults (U20 (a)),
// and --plugin-dir is still always there (spec §7.2 step 4).
func TestLaunchLine_NoModelNoEffort(t *testing.T) {
	for _, c := range []struct{ model, effort, want string }{
		{"", "", `'claude' '--dangerously-skip-permissions' --plugin-dir '/d/cc-plugin/purdex'`},
		{"opus", "", `'claude' '--dangerously-skip-permissions' --plugin-dir '/d/cc-plugin/purdex' --model 'opus'`},
		{"", "medium", `'claude' '--dangerously-skip-permissions' --plugin-dir '/d/cc-plugin/purdex' --effort medium`},
	} {
		if line, err := launchLine(" claude --dangerously-skip-permissions ", "/d/cc-plugin/purdex", c.model, c.effort); err != nil || line != c.want {
			t.Errorf("model %q effort %q: line = %q (err %v), want %q", c.model, c.effort, line, err, c.want)
		}
	}
	dir := t.TempDir()
	shellArgv(t, dir, printArgv, dir+"/x y/cc-plugin/purdex", "", "", []string{"--plugin-dir", dir + "/x y/cc-plugin/purdex"})
}

// The daemon checks again what the CLI checked (U20 (a), 400 at the
// handler): a model ValidModel refuses, an effort outside M25's five, a
// plugin dir that is not one absolute line, or a member command that is not
// one non-blank line never reaches a composed line: "" and an error.
func TestLaunchLine_RefusesBadModelOrEffort(t *testing.T) {
	refuse := func(what, cmd, dir, model, effort string) {
		t.Helper()
		if line, err := launchLine(cmd, dir, model, effort); err == nil || line != "" {
			t.Errorf("%s: line = %q, err = %v; want \"\" and an error", what, line, err)
		}
	}
	const pd = "/d/cc-plugin/purdex"
	for _, m := range []string{"a b", "'x'", "x;y", "$(x)", "`x`", "-x", "opus[2m]", "opus[1m]x", "opus\n", "opus\nrm -rf ~",
		"opus'", `"x"`, "x|y", "x&y", "x>y", "~x", "*", " opus", strings.Repeat("a", 65)} {
		if team.ValidModel(m) {
			t.Fatalf("test table: ValidModel accepts %q", m)
		}
		refuse("model "+m, team.DefaultMemberCommand, pd, m, "")
	}
	// Every one-byte suffix of a valid name: refused by ValidModel means
	// refused here; accepted means single-quoted as it is.
	for b := 0; b < 128; b++ {
		m := "opus" + string(rune(b))
		line, err := launchLine(team.DefaultMemberCommand, pd, m, "")
		if team.ValidModel(m) != (err == nil) || (err == nil && !strings.HasSuffix(line, " --model '"+m+"'")) {
			t.Errorf("model %q: ValidModel %v, line %q, err %v", m, team.ValidModel(m), line, err)
		}
	}
	for _, e := range []string{"High", "HIGH", "ultra", " high", "high;x", "max\n", "'high'", "$(x)"} {
		refuse("effort "+e, team.DefaultMemberCommand, pd, "", e)
	}
	for _, d := range []string{"", "relative/cc-plugin/purdex", "/a\nb", "/a\x00b", "/a\tb", "/a\x1bb", "/a\xffb"} {
		refuse("plugin dir "+d, team.DefaultMemberCommand, d, "", "")
	}
	for _, c := range []string{"", "   ", "claude\nrm -rf ~", "claude\r--x", "claude \\", "claude #", "claude >/tmp/log #", "claude 'x"} {
		refuse("member command "+c, c, pd, "", "")
	}
}
