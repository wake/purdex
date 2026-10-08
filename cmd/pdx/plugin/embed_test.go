package plugin

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"regexp"
	"strings"
	"testing"
	"testing/fstest"
)

func TestFiles_HasTheLayoutClaudeLoads(t *testing.T) {
	f := Files()
	for _, rel := range []string{".claude-plugin/plugin.json", "hooks/hooks.json", "hooks/register.js", "hooks/ask.js", "hooks/events.js", "hooks/prompts.js", "skills/pdx-team/SKILL.md"} {
		if _, err := fs.Stat(f, rel); err != nil {
			t.Errorf("%s: %v", rel, err)
		}
	}
	// hooks.json names one module (Claude Code 2.1.292 loads a single path and refuses a
	// list of two: "names none in modules"), so register.js is the entry and brings in
	// ask.js, the AskUserQuestion 分流 (P8a-2), and events.js, the event reporter
	// (interface U1 spec §6.5), by import declarations.
	reg, err := fs.ReadFile(f, "hooks/register.js")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(reg), "import { register as registerAsk } from './ask.js'") || !strings.Contains(string(reg), "registerAsk(on)") {
		t.Error("register.js does not import and register ./ask.js")
	}
	if !strings.Contains(string(reg), "import { registerEvents } from './events.js'") || !strings.Contains(string(reg), "registerEvents(on)") {
		t.Error("register.js does not import and register ./events.js")
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

// registration is one on('<event>', …) call in a hooks module: its file, its event and
// its matcher literal (the leading {…} of the second argument; "" when it has none).
type registration struct {
	file, event, matcher string
}

// onCall finds on('<event>', at the start of a line or after a character that cannot end
// a property access (so $.ui.on( or x.on( is not one).
var onCall = regexp.MustCompile(`(?:^|[^.\w$])on\(\s*['"]([^'"]+)['"]\s*,\s*`)

// registrations lists the on(...) calls of one hooks module, in source order.
func registrations(file, src string) []registration {
	var out []registration
	for _, m := range onCall.FindAllStringSubmatchIndex(src, -1) {
		r := registration{file: file, event: src[m[2]:m[3]]}
		if rest := src[m[1]:]; strings.HasPrefix(rest, "{") {
			r.matcher = leadingBraces(rest)
		}
		out = append(out, r)
	}
	return out
}

// leadingBraces returns the {…} that s starts with, braces counted. The mod's matchers
// hold no brace inside a string or a regular expression; one that did would be cut short,
// still a matcher.
func leadingBraces(s string) string {
	depth := 0
	for i, c := range s {
		switch c {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return s[:i+1]
			}
		}
	}
	return s
}

// registrationConflicts names what makes Claude Code refuse to load the whole hooks module
// (interface U1 spec §3, M-U1-3: on("turn.complete") is registered twice without a
// matcher): an event registered without a matcher in two places across the plugin's
// files, or one file registering an event twice with the same matcher.
func registrationConflicts(regs []registration) []string {
	var out []string
	unmatched := map[string]string{} // event → the file that registered it first
	matched := map[string]bool{}     // file, event, matcher
	for _, r := range regs {
		if r.matcher == "" {
			if first, ok := unmatched[r.event]; ok {
				out = append(out, fmt.Sprintf("on(%q) is registered without a matcher in %s and in %s", r.event, first, r.file))
			} else {
				unmatched[r.event] = r.file
			}
			continue
		}
		key := r.file + "\x00" + r.event + "\x00" + strings.Join(strings.Fields(r.matcher), "") // spacing aside
		if matched[key] {
			out = append(out, fmt.Sprintf("%s registers on(%q, %s) twice", r.file, r.event, r.matcher))
		}
		matched[key] = true
	}
	return out
}

// TestHooks_NoEventRegisteredTwiceWithoutMatcher keeps the plugin loadable: register.js,
// ask.js and events.js are one module to Claude Code, and from U1-1b events.js owns
// tool.call, tool.check, agent.spawn, session.measure, session.end and classic.Stop
// without a matcher, so a later change must use a matcher there (spec §10).
func TestHooks_NoEventRegisteredTwiceWithoutMatcher(t *testing.T) {
	f := Files()
	names, err := fs.Glob(f, "hooks/*.js")
	if err != nil {
		t.Fatal(err)
	}
	var regs []registration
	for _, name := range names {
		b, err := fs.ReadFile(f, name)
		if err != nil {
			t.Fatal(err)
		}
		regs = append(regs, registrations(name, string(b))...)
	}
	for _, c := range registrationConflicts(regs) {
		t.Error(c)
	}
	// The scan sees what it guards: one of each kind, so a pattern that matched nothing
	// cannot pass vacuously.
	for _, want := range []registration{
		{"hooks/events.js", "tool.call", ""},
		{"hooks/events.js", "turn.complete", "{ turnId: /^/ }"},
		{"hooks/register.js", "turn.complete", ""},
		{"hooks/ask.js", "tool.call", "{ tool: 'AskUserQuestion' }"},
	} {
		found := false
		for _, r := range regs {
			found = found || r == want
		}
		if !found {
			t.Errorf("the scan did not find %+v among %d registrations", want, len(regs))
		}
	}
}

func TestRegistrationConflicts(t *testing.T) {
	for _, tc := range []struct {
		name  string
		files map[string]string
		want  []string
	}{
		{"one unmatched each", map[string]string{"a.js": "on('turn.start', h)\non('turn.complete', h)", "b.js": "on('tool.call', h)"}, nil},
		{"unmatched in two files", map[string]string{"a.js": "  on('turn.complete', h)", "b.js": "on(\"turn.complete\", async ($, e, next) => next(e))"},
			[]string{`on("turn.complete") is registered without a matcher in a.js and in b.js`}},
		{"unmatched twice in one file", map[string]string{"a.js": "on('session.end', h)\n  on('session.end', g)"},
			[]string{`on("session.end") is registered without a matcher in a.js and in a.js`}},
		{"matched beside unmatched", map[string]string{"a.js": "on('tool.call', { tool: 'Bash' }, h)", "b.js": "on('tool.call', h)"}, nil},
		{"the same matcher twice in a file", map[string]string{"a.js": "on('tool.check', { tool: 'Write' }, h)\non('tool.check', {tool:  'Write'}, g)"},
			[]string{`a.js registers on("tool.check", {tool:  'Write'}) twice`}},
		{"different matchers", map[string]string{"a.js": "on('tool.check', { tool: 'Write' }, h)\non('tool.check', { tool: 'Edit' }, g)"}, nil},
		{"the same matcher in two files", map[string]string{"a.js": "on('turn.start', { turnId: /^/ }, h)", "b.js": "on('turn.start', { turnId: /^/ }, g)"}, nil},
		{"a property named on is not a registration", map[string]string{"a.js": "on('tool.call', h)\n$.ui.on('tool.call', h)\nx.on('tool.call', h)\nfunction(on) {}"}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var regs []registration
			for _, name := range []string{"a.js", "b.js"} {
				if src, ok := tc.files[name]; ok {
					regs = append(regs, registrations(name, src)...)
				}
			}
			got := registrationConflicts(regs)
			if strings.Join(got, "\n") != strings.Join(tc.want, "\n") {
				t.Errorf("conflicts = %q, want %q", got, tc.want)
			}
		})
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
		// The CLI's own wait timeout is not the daemon's start timeout: check
		// pdx team before spawning again (PR P4-7 critic ruling).
		"`spawn_wait_timeout`", "do not spawn again",
		"Exit 14 (`member_start_timeout`) means the daemon gave up",
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
