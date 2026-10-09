package plugin

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"regexp"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/wake/purdex/internal/resources"
	"github.com/wake/purdex/internal/team"
)

func TestFiles_HasTheLayoutClaudeLoads(t *testing.T) {
	f := Files()
	for _, rel := range []string{".claude-plugin/plugin.json", "hooks/hooks.json", "hooks/register.js", "hooks/ask.js", "hooks/events.js", "hooks/lease.js", "hooks/member.js", "hooks/prompts.js", "skills/pdx-team/SKILL.md", "skills/pdx-lease/SKILL.md"} {
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
	if !strings.Contains(string(reg), "import { registerLease } from './lease.js'") || !strings.Contains(string(reg), "registerLease(on)") {
		t.Error("register.js does not import and register ./lease.js")
	}
	// The seed's task notice (T-2b): the mod checks the header `pdx task mine --seed` prints.
	if !strings.Contains(string(reg), "const TASKS_HEADER = '"+team.TaskSeedHeader+"'") {
		t.Error("register.js does not check the task notice header internal/team prints")
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

// The guard has to see every registration, because Claude Code refuses the whole module over
// an event registered twice without a matcher (M-U1-3), however the call is spelled. So it
// fails closed (U1-1b review, attack #3): every use of the name `on` in the code must be
// one it reads — on('<event>', …) or on?.('<event>', …) with a quoted literal as the whole
// first argument, its arguments on any line — or one that cannot register anything: a
// function's parameter, a property key, the two calls that hand `on` to the other files
// (registerAsk(on), registerEvents(on), registerLease(on), whose files are scanned too). Anything else (an
// alias, `on` passed elsewhere, a variable or template event name) is an error.
var (
	handsOnTo   = regexp.MustCompile(`(?:^|[^\w$.])(?:registerAsk|registerEvents|registerLease)\s*\(\s*$`)
	onParameter = regexp.MustCompile(`(?:^|[^\w$.])function\s*\*?\s*[\w$]*\s*\(\s*$`)
)

// scanRegistrations lists the on(...) calls of one hooks module, in source order, and what
// in it the scan cannot read (each a reason for the guard to fail).
func scanRegistrations(file, src string) (regs []registration, problems []string) {
	code, err := jsCode(src)
	if err != nil {
		return nil, []string{fmt.Sprintf("%s: %v; the registration guard cannot read the file", file, err)}
	}
	where := func(i int) string { return fmt.Sprintf("%s:%d", file, 1+strings.Count(src[:i], "\n")) }
	for i := 0; ; {
		k := strings.Index(code[i:], "on")
		if k < 0 {
			break
		}
		at := i + k
		i = at + 2
		if (at > 0 && isIdentByte(code[at-1])) || (at+2 < len(code) && isIdentByte(code[at+2])) {
			continue // a longer name: once, json, $on
		}
		prev, next := lastCode(code, at), skipSpace(code, at+2)
		if prev >= 0 && code[prev] == '.' {
			if prev >= 2 && code[prev-2:prev+1] == "..." {
				problems = append(problems, where(at)+": `on` is used other than in on('<event>', …): spread")
			}
			continue // a property: $.ui.on, x?.on
		}
		call := next
		if strings.HasPrefix(code[call:], "?.") {
			call = skipSpace(code, call+2)
		}
		switch {
		case call < len(code) && code[call] == '(':
			r, problem := readCall(file, src, code, call)
			if problem != "" {
				problems = append(problems, where(at)+": "+problem)
			} else {
				regs = append(regs, r)
			}
		case next < len(code) && code[next] == ')' && (handsOnTo.MatchString(code[:at]) || onParameter.MatchString(code[:at])):
			// registerAsk(on) / registerEvents(on) / registerLease(on), or function register(on)
		case next < len(code) && code[next] == ':' && prev >= 0 && (code[prev] == '{' || code[prev] == ','):
			// a property key: { on: … }
		default:
			problems = append(problems, where(at)+": `on` is used other than in on('<event>', …), registerAsk(on), registerEvents(on) or registerLease(on) (an alias, an argument, a parameter of an arrow, …): the guard cannot follow it, so call on('<event>', …) directly")
		}
	}
	return regs, problems
}

// readCall reads the on(…) call whose ( is at open: the event, a quoted literal that is the
// whole first argument, and the matcher, the second argument when it is a {…} literal.
func readCall(file, src, code string, open int) (registration, string) {
	q := skipSpace(code, open+1)
	if q >= len(code) || (code[q] != '\'' && code[q] != '"') {
		return registration{}, "on(…) whose event is not a quoted string literal (a variable, a template literal, an expression): write the event as 'event.name' so the guard can check it"
	}
	end := q + 1 + strings.IndexByte(code[q+1:], code[q]) // the literal's inside is blank: the next quote closes it
	event := src[q+1 : end]
	if event == "" || strings.Contains(event, "\\") {
		return registration{}, "on(…) whose event literal is empty or holds an escape: write the event name as it is"
	}
	comma := skipSpace(code, end+1)
	if comma >= len(code) || code[comma] != ',' {
		return registration{}, "on(…) whose event literal is not the whole first argument"
	}
	r := registration{file: file, event: event}
	if m := skipSpace(code, comma+1); m < len(code) && code[m] == '{' {
		closing := matchingBrace(code, m)
		if closing < 0 {
			return registration{}, "on(…) whose matcher {…} is not closed"
		}
		r.matcher = src[m : closing+1]
	}
	return r, ""
}

// jsCode returns src with every comment blanked and the inside of every string, template
// literal and regular expression literal blanked — their delimiters, and a template's ${ … }
// with the code in it, kept — newlines and byte offsets unchanged: what is left is the code
// alone, so "on(" in a comment, a string or a pattern is not a call, and a literal is found by
// its delimiters and read from src at the same offsets. A comment, literal or template left
// open, or a quoted string or pattern running into a newline, is an error: the scan could no
// longer be trusted. A `/` starts a pattern unless what precedes it ends an operand (a name
// that is not a keyword, a number, `)`, `]`, a literal) — the usual rule; it holds for the
// mod's code, and a misread pattern runs into a newline or the end and is an error.
func jsCode(src string) (string, error) {
	out := []byte(src)
	blank := func(from, to int) {
		for k := from; k < to; k++ {
			if out[k] != '\n' {
				out[k] = ' '
			}
		}
	}
	line := func(i int) int { return 1 + strings.Count(src[:i], "\n") }
	var exprs []int // per open ${ … } of a template: its { … } depth
	prev := -1      // the last code byte that is not white space
	n := len(src)
	// text reads template text from i up to its closing ` (returns the index after it) or to
	// a ${ (returns the index after the {, the expression opened).
	text := func(i int) (int, bool, error) {
		start := i
		for i < n {
			switch {
			case src[i] == '\\':
				i += 2
			case src[i] == '`':
				blank(start, i)
				return i + 1, false, nil
			case src[i] == '$' && i+1 < n && src[i+1] == '{':
				blank(start, i)
				return i + 2, true, nil
			default:
				i++
			}
		}
		return 0, false, fmt.Errorf("line %d: a template literal is not closed", line(start))
	}
	for i := 0; i < n; {
		c := src[i]
		switch {
		case c == '/' && i+1 < n && src[i+1] == '/':
			j := strings.IndexByte(src[i:], '\n')
			if j < 0 {
				j = n - i
			}
			blank(i, i+j)
			i += j
		case c == '/' && i+1 < n && src[i+1] == '*':
			j := strings.Index(src[i+2:], "*/")
			if j < 0 {
				return "", fmt.Errorf("line %d: a block comment is not closed", line(i))
			}
			blank(i, i+2+j+2)
			i += 2 + j + 2
		case c == '\'' || c == '"' || (c == '/' && startsPattern(out, prev)):
			j, inClass := i+1, false
			for ; ; j++ {
				if j >= n || src[j] == '\n' {
					return "", fmt.Errorf("line %d: a string or a regular expression is not closed on its line", line(i))
				}
				if src[j] == '\\' {
					if j+1 < n && src[j+1] == '\n' && c == '/' {
						return "", fmt.Errorf("line %d: a regular expression is not closed on its line", line(i))
					}
					j++ // the escaped byte (in a string, a newline here continues the line)
					continue
				}
				if c == '/' && src[j] == '[' {
					inClass = true
				} else if c == '/' && src[j] == ']' {
					inClass = false
				} else if src[j] == c && !inClass {
					break
				}
			}
			blank(i+1, j)
			prev, i = j, j+1
		case c == '`':
			j, opened, err := text(i + 1)
			if err != nil {
				return "", err
			}
			if opened {
				exprs = append(exprs, 0)
			}
			prev, i = j-1, j
		case c == '{':
			if len(exprs) > 0 {
				exprs[len(exprs)-1]++
			}
			prev, i = i, i+1
		case c == '}' && len(exprs) > 0 && exprs[len(exprs)-1] == 0:
			exprs = exprs[:len(exprs)-1] // the end of a ${ … }: back in the template's text
			j, opened, err := text(i + 1)
			if err != nil {
				return "", err
			}
			if opened {
				exprs = append(exprs, 0)
			}
			prev, i = j-1, j
		case c == '}':
			if len(exprs) > 0 {
				exprs[len(exprs)-1]--
			}
			prev, i = i, i+1
		default:
			if !isSpaceByte(c) {
				prev = i
			}
			i++
		}
	}
	if len(exprs) > 0 {
		return "", fmt.Errorf("a template literal is not closed")
	}
	return string(out), nil
}

// startsPattern says whether a / after the code byte at prev starts a regular expression.
func startsPattern(code []byte, prev int) bool {
	if prev < 0 || strings.IndexByte("(,=:[!&|?{};+-*%<>~^", code[prev]) >= 0 {
		return true
	}
	if !isIdentByte(code[prev]) {
		return false // ) ] or the end of a literal: a division
	}
	from := prev
	for from > 0 && isIdentByte(code[from-1]) {
		from--
	}
	switch string(code[from : prev+1]) {
	case "return", "typeof", "case", "do", "else", "in", "of", "new", "delete", "void", "throw", "instanceof", "yield", "await":
		return true
	}
	return false
}

func isIdentByte(c byte) bool {
	return c == '_' || c == '$' || c == '#' || c >= 0x80 || ('0' <= c && c <= '9') || ('a' <= c && c <= 'z') || ('A' <= c && c <= 'Z')
}

func isSpaceByte(c byte) bool { return c == ' ' || c == '\t' || c == '\n' || c == '\r' }

// skipSpace returns the index of the first byte at or after i that is not white space.
func skipSpace(code string, i int) int {
	for i < len(code) && isSpaceByte(code[i]) {
		i++
	}
	return i
}

// lastCode returns the index of the last byte before i that is not white space, -1 if none.
func lastCode(code string, i int) int {
	for i--; i >= 0 && isSpaceByte(code[i]); i-- {
	}
	return i
}

// matchingBrace returns the index of the } closing the { at open, -1 if none. Braces inside
// literals and comments are blank in code.
func matchingBrace(code string, open int) int {
	depth := 0
	for i := open; i < len(code); i++ {
		switch code[i] {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return i
			}
		}
	}
	return -1
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
		r, problems := scanRegistrations(name, string(b))
		for _, p := range problems {
			t.Error(p)
		}
		regs = append(regs, r...)
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
		{"hooks/lease.js", "tool.call", "{ tool: 'Bash' }"},
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
		{"a property named on is not a registration", map[string]string{"a.js": "on('tool.call', h)\n$.ui.on('tool.call', h)\nx.on('tool.call', h)\nx?.on('tool.call', h)\nfunction(on) {}"}, nil},
		// What the scan must still see (U1-1b review, attack #3): an optional call, a call
		// whose arguments start on the next line, a registration inside a template literal's
		// ${…}, and what a comment, a string or a regular expression holds is not one.
		{"an optional call", map[string]string{"a.js": "on('turn.complete', h)", "b.js": "on?.('turn.complete', h)"},
			[]string{`on("turn.complete") is registered without a matcher in a.js and in b.js`}},
		{"arguments on the next lines", map[string]string{"a.js": "on('turn.complete', h)", "b.js": "on\n  (\n    'turn.complete'\n    ,\n    h)"},
			[]string{`on("turn.complete") is registered without a matcher in a.js and in b.js`}},
		{"a matcher on the next line", map[string]string{"a.js": "on('tool.call', h)", "b.js": "on('tool.call',\n  { tool: 'Bash' }, h)"}, nil},
		{"inside a template expression", map[string]string{"a.js": "on('turn.complete', h)", "b.js": "const s = `x${on('turn.complete', h)}y`"},
			[]string{`on("turn.complete") is registered without a matcher in a.js and in b.js`}},
		{"comments, strings and a regular expression", map[string]string{"a.js": "on('turn.complete', h)", "b.js": "// on('turn.complete', h)\n/* on('turn.complete', h) */\nconst s = \"on('turn.complete', h)\" + 'on(\\'turn.complete\\', h)'\nconst re = /on\\('turn.complete'[\"/]/\nconst t = `on('turn.complete', h)`"}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var regs []registration
			for _, name := range []string{"a.js", "b.js"} {
				if src, ok := tc.files[name]; ok {
					r, problems := scanRegistrations(name, src)
					if len(problems) > 0 {
						t.Errorf("%s: problems %q", name, problems)
					}
					regs = append(regs, r...)
				}
			}
			got := registrationConflicts(regs)
			if strings.Join(got, "\n") != strings.Join(tc.want, "\n") {
				t.Errorf("conflicts = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestScanRegistrations_FailsClosed: a use of `on` the scan cannot read as on('<event>', …)
// is an error, not a skipped registration (U1-1b review, attack #3) — Claude Code refuses the
// whole module over an event registered twice without a matcher, whatever the spelling.
func TestScanRegistrations_FailsClosed(t *testing.T) {
	for _, tc := range []struct{ name, src, want string }{
		{"a variable event name", "const E = 'turn.complete'\non(E, h)", "not a quoted string literal"},
		{"a template literal event name", "on(`turn.complete`, h)", "not a quoted string literal"},
		{"a concatenated event name", "on('turn' + '.complete', h)", "the whole first argument"},
		{"an escape in the event name", "on('turn\\x2ecomplete', h)", "escape"},
		{"an alias", "const reg = on\nreg('turn.complete', h)", "used other than"},
		{"passed to a function", "helper(on)", "used other than"},
		{"called through call()", "on.call(null, 'turn.complete', h)", "used other than"},
		{"spread", "const o = { ...on }", "used other than"},
		{"a tagged template", "on`turn.complete`", "used other than"},
		{"an arrow parameter", "const f = (on) => on('turn.complete', h)", "used other than"},
		{"an unclosed string", "on('turn.complete, h)", "not closed"},
		{"an unclosed comment", "on('turn.complete', h) /* x", "not closed"},
		{"an unclosed template", "const s = `x${on('turn.complete', h)}", "not closed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, problems := scanRegistrations("a.js", tc.src)
			if len(problems) == 0 || !strings.Contains(strings.Join(problems, "\n"), tc.want) {
				t.Errorf("problems = %q, want one saying %q", problems, tc.want)
			}
		})
	}
	// What the mod itself does with `on` passes: registerAsk(on), registerEvents(on), a
	// function's parameter, a property key, a property access.
	ok := "export function register(on) {\n  registerAsk(on)\n  registerEvents( on )\n  registerLease(on)\n  on('tool.call', { tool: 'Bash' }, h)\n}\nconst label = { on: 'x', off: 'y' }[k]\n$.ui.on('x', h)\nconst json = once + on_ + $on\n"
	regs, problems := scanRegistrations("a.js", ok)
	if len(problems) > 0 || len(regs) != 1 || regs[0] != (registration{"a.js", "tool.call", "{ tool: 'Bash' }"}) {
		t.Errorf("scan = %+v, problems %q; want the one registration and no problem", regs, problems)
	}
}

// registerLease(on) is allowed like the other two, and a typo of it still fails closed.
func TestScanRegistrations_AllowsRegisterLease(t *testing.T) {
	regs, problems := scanRegistrations("a.js", "export function register(on) {\n  registerLease(on)\n}\n")
	if len(problems) > 0 || len(regs) != 0 {
		t.Errorf("scan = %+v, problems %q; want nothing registered and no problem", regs, problems)
	}
	_, problems = scanRegistrations("a.js", "export function register(on) {\n  registerLeas(on)\n}\n")
	if len(problems) == 0 || !strings.Contains(strings.Join(problems, "\n"), "used other than") {
		t.Errorf("a registerLeas(on) typo must fail closed, problems = %q", problems)
	}
}

// The lease skill tells an agent to wrap heavy commands in `pdx lease run`, what
// counts as heavy, how to behave while waiting, and never to acquire in a
// subshell (the lease is held for acquire's parent, which exits at once).
func TestSkill_LeaseMentionsRun(t *testing.T) {
	b, err := fs.ReadFile(Files(), "skills/pdx-lease/SKILL.md")
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	for _, want := range []string{
		"name: pdx-lease",
		"pdx lease run --kind <kind> -- <指令…>", // the line an agent copies
		"`test-full`", "`build`", "`test-pkg`", "`lint-full`", // D-7 kinds
		"--maxWorkers=3", // R7
		"等待就是主機忙，不是卡住",
		"不要用 `run_in_background` 繞過排隊", // R8
		"不要在子 shell 裡直接 acquire",
		"agent 一律用 `pdx lease run`",
		"pdx lease ls",
		// Review of P1-3c: the contract an agent following the text literally needs.
		"`--kind`、`--weight`、`--wait`、`--client-id`、`--config`）都要放在 `--` 之前", // flags before the separator
		"pdx lease run --kind build -- pnpm run build --wait 2m",             // the wrong example is shown as wrong
		"**12**", "**126**", "**127**", "以 `pdx lease:` 開頭的一行", // pdx's own exit codes
		"Purdex mod 已經替你處理前景的重 Bash", "你不必自己包 `pdx lease run`", "`advise` 期間 mod 只記錄不擋", // Task 2.4: the mod covers foreground Bash
		"完整 vitest 的名額規定照舊",                               // the coordinator-slot rule stays until P2 (Task 2.4)
		"sh -c 'cd spa && npx vitest run --maxWorkers=3'", // runs from the repo root
	} {
		if !strings.Contains(s, want) {
			t.Errorf("SKILL.md of pdx-lease lacks %q", want)
		}
	}
	// The weights in the table are the built-in ones of spec D-7.
	for _, row := range []string{"| `test-full` |", "| 35 |", "| 15 |", "| 10 |"} {
		if !strings.Contains(s, row) {
			t.Errorf("weight table lacks %q", row)
		}
	}
}

// The weights the skill prints are the built-in ones: a change of D-7 that
// forgets the skill fails here.
func TestSkill_LeaseWeightsAreTheBuiltInOnes(t *testing.T) {
	b, err := fs.ReadFile(Files(), "skills/pdx-lease/SKILL.md")
	if err != nil {
		t.Fatal(err)
	}
	row := regexp.MustCompile("(?m)^\\| `([a-z-]+)` \\|.*\\| (\\d+) \\|$")
	got := map[string]int{}
	for _, m := range row.FindAllStringSubmatch(string(b), -1) {
		n := 0
		fmt.Sscan(m[2], &n)
		got[m[1]] = n
	}
	if len(got) != len(resources.DefaultKinds) {
		t.Fatalf("skill table = %v, built-in = %v", got, resources.DefaultKinds)
	}
	for k, w := range resources.DefaultKinds {
		if got[k] != w {
			t.Errorf("kind %s: skill says %d, built-in is %d", k, got[k], w)
		}
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
		// T-2: the task flags come after the pinned grammar, and the two one-line rules.
		"[--task-subject <s> [--done-when <line>]…]", "Hand each piece of work to a member as a task: `pdx task add --to <ref>",
		"Report with `pdx report <kind>`", "`pdx task mine` lists your tasks",
		// /relay now (lead-command spec §2b): the user's early relay, never the agent's.
		"`/relay` (or `/relay now`) is the user's way to relay early: **you never run it**",
		// /lead (lead-command spec §3): requested at once, not judged.
		"When the user runs `/lead` or plainly asks you to become a lead, request it at once",
		// P6-5: the lead decides when a member is relayed, with pdx relay <ref> in the foreground; approval is the user's.
		"**you decide** whether and when to relay that member: `pdx relay <ref> --wait 9m`", "**in the foreground with Bash `timeout: 600000`**",
		"`relay_unsupported`: that member has no Purdex mod", "you never approve it",
		"**or your wait was interrupted (Ctrl-C, a Bash timeout): the relay keeps going",
		// #2062: the relay quota and its switch are the user's.
		"**The relay quota is the user's.**", "you never ask for more quota", "a lead sees its own in the header of `pdx team`, read-only",
		// The team's member limit is the user's too.
		"**The team's member limit is the user's.**", "you never ask for a larger team", "`members M/N` in the header of `pdx team`",
		// P6-6: a member never sees the control message; it writes the handoff with one Write when the prompt arrives.
		"When the lead relays you, the Purdex mod does it all", "write the handoff file it names with **one `Write`** and answer `HANDOFF-WRITTEN`", "your old ref still reaches you",
		// U24 (adopt spec D-U24-2/3): the commands, the foreground wait and the ambiguity hint.
		"`pdx adopt <ref>` takes a **running session on this host**", "**in the foreground with Bash `timeout: 600000`**",
		"`adopt_target_ambiguous` means two sessions share that ref", "`pdx release <ref>` lets a member go",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("SKILL.md lacks %q", want)
		}
	}
	if strings.Contains(s, "The member-relay command is not available yet") {
		t.Error("SKILL.md still says the member-relay command is not available (P6-5 brought it)")
	}
	if strings.Contains(s, "--root <dir> [--repo") {
		t.Error("SKILL.md still teaches the pre-P4 spawn grammar")
	}
	// U23 D-U23-2: the skill forbids turning on unattended mode, where an
	// agent asks for lead mode and where self relay is explained.
	for _, section := range []string{"## When to ask for lead mode, and how to wait", "## Self relay"} {
		body, ok := sectionOf(s, section)
		if !ok {
			t.Errorf("SKILL.md has no %q section", section)
			continue
		}
		for _, want := range []string{
			"**Never turn on 無人值守模式 (unattended mode).**",
			"It is the user's switch in Purdex.app: there is no `pdx` command for it,",
			"you must not call the daemon's route or edit host config to get around that.",
		} {
			if !strings.Contains(body, want) {
				t.Errorf("SKILL.md %q lacks %q", section, want)
			}
		}
	}
}

// Team name (TN-1, D-N1): the skill's request line carries --name and the next
// sentence tells every lead to always give one, in the section where an agent
// asks for lead mode.
func TestSkill_LeadRequestAsksForATeamName(t *testing.T) {
	b, err := fs.ReadFile(Files(), "skills/pdx-team/SKILL.md")
	if err != nil {
		t.Fatal(err)
	}
	body, ok := sectionOf(string(b), "## When to ask for lead mode, and how to wait")
	if !ok {
		t.Fatal("SKILL.md has no \"When to ask for lead mode\" section")
	}
	for _, want := range []string{
		`pdx lead request --reason "<why>" --name "<team name>" --label "<短名>" [--max-members N] [--root <dir>]`,
		"Always give `--name`: a name for the team's work (at most 64 bytes); it is shown in the team panel, and the user may change it when approving.",
		// Team label (TL-1c, D-L9)
		"Always give `--label`: a short, meaningful name of your own for the tab group label, about five Chinese characters (10 display columns), e.g. `A 線`, `資源線`, `資源派工` — a summary, never the name cut short.",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("SKILL.md lead-mode section lacks %q", want)
		}
	}
}

// sectionOf is the text of the markdown section that starts with heading
// (a whole line), up to the next "## " heading.
func sectionOf(s, heading string) (string, bool) {
	i := strings.Index(s, "\n"+heading+"\n")
	if i < 0 {
		return "", false
	}
	body := s[i+len(heading)+2:]
	if j := strings.Index(body, "\n## "); j >= 0 {
		body = body[:j]
	}
	return body, true
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

// skillEndMemberRule is D-U24-4 word for word (adopt plan decision 8): the lead asks before it closes a member on
// its own judgement, with exactly three options. A test-local golden: editing the rule is a reviewed change of two
// places.
const skillEndMemberRule = "When you yourself judge that a member is no longer needed, first ask the user with AskUserQuestion. " +
	"Name the member (its address and title) in the question, and give exactly three options: 釋出 / 關閉 / 保留. " +
	"Then do what the answer says: 釋出 → `pdx release <ref>`, 關閉 → `pdx kill <ref>`, 保留 → nothing. " +
	"When the user asked you directly to release or close a member, do it without asking."

// Mutation gates: add a fourth option, or drop the direct-request sentence, in the skill → red.
func TestSkill_EndMemberRuleIsPinned(t *testing.T) {
	b, err := fs.ReadFile(Files(), "skills/pdx-team/SKILL.md")
	if err != nil {
		t.Fatal(err)
	}
	body, ok := sectionOf(string(b), "## As a lead")
	if !ok {
		t.Fatal("no `## As a lead` section")
	}
	var para []string
	for _, line := range strings.Split(body, "\n") {
		if strings.Contains(line, "AskUserQuestion") {
			para = append(para, strings.TrimPrefix(line, "- "))
		}
	}
	if len(para) != 1 || para[0] != skillEndMemberRule {
		t.Fatalf("the end-member rule in `## As a lead` = %q, want exactly %q", para, skillEndMemberRule)
	}
	// the option list is exactly three items, in this order
	i := strings.Index(para[0], "exactly three options: ")
	opts := strings.SplitN(para[0][i+len("exactly three options: "):], ".", 2)[0]
	if opts != "釋出 / 關閉 / 保留" {
		t.Fatalf("options = %q", opts)
	}
	for _, want := range []string{"Name the member", "釋出 → `pdx release <ref>`", "關閉 → `pdx kill <ref>`", "保留 → nothing", "do it without asking"} {
		if !strings.Contains(para[0], want) {
			t.Errorf("rule lacks %q", want)
		}
	}
}
