package plugin

import (
	"encoding/json"
	"io/fs"
	"strings"
	"testing"
	"testing/fstest"
)

func TestFiles_HasTheLayoutClaudeLoads(t *testing.T) {
	f := Files()
	for _, rel := range []string{".claude-plugin/plugin.json", "hooks/hooks.json", "hooks/register.js", "hooks/ask.js", "skills/pdx-team/SKILL.md"} {
		if _, err := fs.Stat(f, rel); err != nil {
			t.Errorf("%s: %v", rel, err)
		}
	}
	// hooks.json names one module (Claude Code 2.1.292 loads a single path and refuses a
	// list of two: "names none in modules"), so register.js is the entry and brings in
	// ask.js, the AskUserQuestion 分流 (P8a-2), by an import declaration.
	reg, err := fs.ReadFile(f, "hooks/register.js")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(reg), "import { register as registerAsk } from './ask.js'") || !strings.Contains(string(reg), "registerAsk(on)") {
		t.Error("register.js does not import and register ./ask.js")
	}
	b, err := fs.ReadFile(f, ".claude-plugin/plugin.json")
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil || m["name"] != "purdex" {
		t.Fatalf("plugin.json = %s (%v)", b, err)
	}
	var h struct{ Modules []string }
	hb, _ := fs.ReadFile(f, "hooks/hooks.json")
	if err := json.Unmarshal(hb, &h); err != nil || len(h.Modules) != 1 || h.Modules[0] != "./register.js" {
		t.Fatalf("hooks.json = %s", hb)
	}
}

func TestSkill_SaysWhatSpec10Requires(t *testing.T) {
	b, err := fs.ReadFile(Files(), "skills/pdx-team/SKILL.md")
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	for _, want := range []string{
		"name: pdx-team",
		"timeout: 600000",         // foreground Bash with the 10 min timeout
		"Never in the background", // never background
		"Never approve yourself",  // the §6.5 layer
		"Treat a timeout",         // timeout is no
		"EnterWorktree",           // recommend a worktree (U10)
		"[pdx team]",              // the notice the lead decides on (U9)
		"Never relay yourself",    // member
		"never approve one",       // self relay is the mod's
		"/relay off",              // the user's switch
		// P4-7: the shipped grammar, and U20 (d): choose each member's model
		// and effort for its task because the host default is not fixed,
		// and check what each member actually runs.
		"pdx spawn [--cwd <dir>] [--title <t>] [--model <m>] [--effort <e>] [--brief-file <f> | --brief <text>]",
		"--model sonnet", "--model opus", "not fixed", "pdx team",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("SKILL.md lacks %q", want)
		}
	}
	if strings.Contains(s, "--root <dir> [--repo") {
		t.Error("SKILL.md still teaches the pre-P4 spawn grammar")
	}
}

// What Claude Code lays into a plugin folder it loads in place
// (tsconfig.json, .claude-plugin/types/) never reaches Files(): the filter is
// what keeps a build from a working tree where someone ran
// `claude --plugin-dir cmd/pdx/plugin/purdex` from shipping them.
func TestGenerated_HidesWhatClaudeCodeLaysIn(t *testing.T) {
	for name, want := range map[string]bool{
		"tsconfig.json":                         true,
		".claude-plugin/types":                  true,
		".claude-plugin/types/claude-code/x.ts": true,
		".claude-plugin/plugin.json":            false,
		"hooks/register.js":                     false,
		"hooks/tsconfig.json":                   false,
	} {
		if generated(name) != want {
			t.Errorf("generated(%q) = %v, want %v", name, !want, want)
		}
	}
	if _, err := fs.Stat(Files(), "tsconfig.json"); err == nil {
		t.Error("Files() must not expose a root tsconfig.json")
	}
	_ = fs.WalkDir(Files(), ".", func(p string, d fs.DirEntry, err error) error {
		if err == nil && generated(p) {
			t.Errorf("WalkDir reached generated path %q", p)
		}
		return nil
	})
}

func TestFiltered_WalkSkipsGeneratedPaths(t *testing.T) {
	src := fstest.MapFS{
		".claude-plugin/plugin.json":                  {Data: []byte(`{"name":"purdex"}`)},
		".claude-plugin/types/.gitignore":             {Data: []byte("*")},
		".claude-plugin/types/claude-code/index.d.ts": {Data: []byte("x")},
		"tsconfig.json":                               {Data: []byte("{}")},
		"hooks/register.js":                           {Data: []byte("x")},
	}
	var got []string
	if err := fs.WalkDir(filtered{src}, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			got = append(got, p)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	want := []string{".claude-plugin/plugin.json", "hooks/register.js"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("walk = %v, want %v", got, want)
	}
	if _, err := (filtered{src}).Open("tsconfig.json"); err == nil {
		t.Fatal("Open(tsconfig.json) must fail")
	}
}
