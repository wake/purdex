package manifest

import (
	"fmt"
	"path"
	"strings"
)

// A task's run is split into argv at parse time and executed directly, never
// through a shell. Words are split on unquoted whitespace; quotes only group
// literal text and nothing is expanded. A word is a list of parts, each a
// literal or a placeholder.

type part struct {
	lit string // literal text, when ph is empty
	ph  string // placeholder name: files, packages, report, junit
}

type word []part

// multi reports whether the word is a lone {files} or {packages}.
func (w word) multi() (string, bool) {
	if len(w) == 1 && (w[0].ph == "files" || w[0].ph == "packages") {
		return w[0].ph, true
	}
	return "", false
}

const shellOperators = "|&;<>()`$"

// parseArgv splits run into words. Unquoted shell operators and backslashes
// are errors (the task needs a shell: call a script in the repo instead).
func parseArgv(run string) ([]word, error) {
	var (
		words  []word
		cur    word
		lit    strings.Builder
		inWord bool
	)
	flushLit := func() {
		if lit.Len() > 0 {
			cur = append(cur, part{lit: lit.String()})
			lit.Reset()
		}
	}
	endWord := func() {
		flushLit()
		if inWord {
			words = append(words, cur)
		}
		cur, inWord = nil, false
	}
	errShell := func(what string) error {
		return fmt.Errorf("run uses %s outside quotes; this task needs shell features, call a script in the repo instead", what)
	}
	for i := 0; i < len(run); i++ {
		c := run[i]
		switch {
		case c == ' ' || c == '\t' || c == '\n':
			endWord()
		case c == '\'' || c == '"':
			end := strings.IndexByte(run[i+1:], c)
			if c == '"' {
				end = closingDouble(run, i+1)
			}
			if end < 0 {
				return nil, fmt.Errorf("run has an unterminated quote")
			}
			body := run[i+1:]
			if c == '"' {
				body = unescapeDouble(run[i+1 : i+1+end])
			} else {
				body = body[:end]
			}
			if placeholderRe.MatchString(body) {
				return nil, fmt.Errorf("placeholder inside quotes in %q", body)
			}
			inWord = true
			lit.WriteString(body)
			i += end + 1
		case c == '\\':
			return nil, errShell("a backslash")
		case strings.IndexByte(shellOperators, c) >= 0:
			return nil, errShell(fmt.Sprintf("%q", string(c)))
		case c == '{':
			loc := placeholderRe.FindStringSubmatchIndex(run[i:])
			if loc == nil || loc[0] != 0 {
				inWord = true
				lit.WriteByte(c)
				continue
			}
			name := run[i+loc[2] : i+loc[3]]
			flushLit()
			cur = append(cur, part{ph: name})
			inWord = true
			i += loc[1] - 1
		default:
			inWord = true
			lit.WriteByte(c)
		}
	}
	endWord()
	if len(words) == 0 {
		return nil, fmt.Errorf("run is empty")
	}
	multis := 0
	for _, w := range words {
		for _, p := range w {
			if p.ph == "files" || p.ph == "packages" {
				if multis++; multis > 1 {
					return nil, fmt.Errorf("{files} and {packages} may appear only once")
				}
				if _, ok := w.multi(); !ok {
					return nil, fmt.Errorf("{%s} must be a word of its own", p.ph)
				}
			}
		}
	}
	if len(words[0]) != 1 || words[0][0].ph != "" {
		return nil, fmt.Errorf("the program name must be a literal word")
	}
	return words, nil
}

// closingDouble returns the offset (relative to start) of the closing double
// quote, honouring \" and \\; -1 if there is none.
func closingDouble(s string, start int) int {
	for i := start; i < len(s); i++ {
		switch s[i] {
		case '\\':
			i++
		case '"':
			return i - start
		}
	}
	return -1
}

// unescapeDouble resolves \" and \\ inside double quotes; any other
// backslash stays a literal backslash.
func unescapeDouble(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+1 < len(s) && (s[i+1] == '"' || s[i+1] == '\\') {
			i++
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

// RenderEnv carries what Render needs from the workspace.
type RenderEnv struct {
	// Report and Junit are the output file paths for {report} and {junit}.
	Report, Junit string
	// Workspace is the job's workspace root; Report and Junit must be
	// inside it (spec §6).
	Workspace string
	// Exists reports whether a repo-root-relative path exists in the
	// snapshot. Required when the task takes files.
	Exists func(repoRel string) bool
}

// Render validates args against the task's args kind and returns the argv to
// exec directly. {files} and {packages} expand to one element per value;
// {report} and {junit} are replaced inside their word.
func (t *Task) Render(args []string, env RenderEnv) ([]string, error) {
	switch t.Args {
	case ArgsNone:
		if len(args) > 0 {
			return nil, fmt.Errorf("task %s takes no arguments", t.Name)
		}
	case ArgsFiles:
		if len(args) == 0 {
			return nil, fmt.Errorf("task %s needs at least one file", t.Name)
		}
		if env.Exists == nil {
			return nil, fmt.Errorf("task %s: no snapshot to check files against", t.Name)
		}
		for _, a := range args {
			if err := checkRelPath(a); err != nil {
				return nil, fmt.Errorf("file %q: %w", a, err)
			}
			if !env.Exists(path.Join(t.Cwd, a)) {
				return nil, fmt.Errorf("file %q is not in the snapshot", a)
			}
		}
	case ArgsPackages:
		if len(args) == 0 {
			return nil, fmt.Errorf("task %s needs at least one package", t.Name)
		}
		for _, a := range args {
			if err := checkPackage(a); err != nil {
				return nil, err
			}
		}
	}
	outputs := map[string]string{"report": env.Report, "junit": env.Junit}
	var out []string
	for _, w := range t.argv {
		if _, ok := w.multi(); ok {
			out = append(out, args...)
			continue
		}
		var b strings.Builder
		for _, p := range w {
			if p.ph == "" {
				b.WriteString(p.lit)
				continue
			}
			if err := checkInside(env.Workspace, outputs[p.ph]); err != nil {
				return nil, fmt.Errorf("{%s}: %w", p.ph, err)
			}
			b.WriteString(outputs[p.ph])
		}
		out = append(out, b.String())
	}
	if t.Args == ArgsPass {
		out = append(out, args...)
	}
	return out, nil
}
