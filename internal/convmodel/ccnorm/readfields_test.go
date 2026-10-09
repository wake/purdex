package ccnorm

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"strconv"
	"strings"
	"testing"
)

// toolInputKeys are the keys read from inside a tool_use input, which the
// "input" Subtree entry of ReadFields keeps whole; they need no entry of
// their own.
var toolInputKeys = map[string]bool{
	"file_path": true, "notebook_path": true, "old_string": true, "new_string": true,
	"edits": true, "command": true, "description": true, "pattern": true, "url": true,
	"query": true, "skill": true, "questions": true, "question": true,
	"subagent_type": true,
}

func pathSegments(p string) []string {
	var segs []string
	for _, s := range strings.Split(p, ".") {
		segs = append(segs, strings.TrimSuffix(s, "[]"))
	}
	return segs
}

// TestReadFields_CoversDecoderKeys parses the non-test sources of the package
// and checks that every string literal used as a key (x.str("k"), x.get("k"),
// o["k"]) is a path segment of ReadFields, so a new read cannot be added
// without the scrubber learning about it.
func TestReadFields_CoversDecoderKeys(t *testing.T) {
	listed := map[string]bool{}
	for _, f := range ReadFields {
		for _, s := range pathSegments(f.Path) {
			listed[s] = true
		}
	}
	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, ".", func(fi fs.FileInfo) bool {
		return !strings.HasSuffix(fi.Name(), "_test.go")
	}, 0)
	if err != nil {
		t.Fatal(err)
	}
	checked := 0
	check := func(pos token.Pos, lit ast.Expr) {
		bl, ok := lit.(*ast.BasicLit)
		if !ok || bl.Kind != token.STRING {
			return
		}
		k, _ := strconv.Unquote(bl.Value)
		checked++
		if !listed[k] && !toolInputKeys[k] {
			t.Errorf("%s: the normalizer reads key %q but ReadFields has no path with it", fset.Position(pos), k)
		}
	}
	for _, pkg := range pkgs {
		for _, f := range pkg.Files {
			ast.Inspect(f, func(n ast.Node) bool {
				switch x := n.(type) {
				case *ast.CallExpr:
					if sel, ok := x.Fun.(*ast.SelectorExpr); ok && (sel.Sel.Name == "str" || sel.Sel.Name == "get") && len(x.Args) == 1 {
						check(x.Pos(), x.Args[0])
					}
				case *ast.IndexExpr:
					if sel, ok := x.X.(*ast.SelectorExpr); ok && sel.Sel.Name == "Skipped" {
						return true // a counter, not a transcript key
					}
					check(x.Pos(), x.Index)
				}
				return true
			})
		}
	}
	if checked < 30 {
		t.Fatalf("only %d key reads found; the scan did not run", checked)
	}
}

func TestReadFields_WellFormed(t *testing.T) {
	rows := map[string]bool{"*": true, "user": true, "assistant": true, "system": true, "attachment": true, "custom-title": true, "ai-title": true}
	seen := map[string]bool{}
	for _, f := range ReadFields {
		if !rows[f.Row] || f.Path == "" || strings.HasPrefix(f.Path, ".") || strings.HasSuffix(f.Path, ".") {
			t.Errorf("malformed entry %+v", f)
		}
		k := f.Row + "|" + f.Path
		if seen[k] {
			t.Errorf("duplicate entry %+v", f)
		}
		seen[k] = true
	}
	for _, must := range []string{"user|toolDenialKind", "user|message.content[].tool_use_id", "assistant|message.content[].input", "attachment|attachment.prompt"} {
		if !seen[must] {
			t.Errorf("ReadFields lacks %s", must)
		}
	}
}

// ReadsRow must agree with the normalizer: a row it says is read is not
// counted as a skipped type / subtype, and one it says is not read is.
func TestReadsRow_AgreesWithNormalizer(t *testing.T) {
	cases := []struct {
		name, line, typ, sub, att string
	}{
		{"user", `{"type":"user","uuid":"u","message":{"content":"x"}}`, "user", "", ""},
		{"assistant", `{"type":"assistant","uuid":"a","message":{"content":[{"type":"text","text":"x"}]}}`, "assistant", "", ""},
		{"custom-title", `{"type":"custom-title","customTitle":"t"}`, "custom-title", "", ""},
		{"ai-title", `{"type":"ai-title","aiTitle":"t"}`, "ai-title", "", ""},
		{"turn_duration", `{"type":"system","subtype":"turn_duration","uuid":"s"}`, "system", "turn_duration", ""},
		{"local_command", `{"type":"system","subtype":"local_command","uuid":"s","content":"x"}`, "system", "local_command", ""},
		{"compact_boundary", `{"type":"system","subtype":"compact_boundary","uuid":"s"}`, "system", "compact_boundary", ""},
		{"informational", `{"type":"system","subtype":"informational","uuid":"s","content":"x"}`, "system", "informational", ""},
		{"stop_hook_summary", `{"type":"system","subtype":"stop_hook_summary","uuid":"s"}`, "system", "stop_hook_summary", ""},
		{"queued_command", `{"type":"attachment","uuid":"a","attachment":{"type":"queued_command","prompt":"x"}}`, "attachment", "", "queued_command"},
		{"hook_success", `{"type":"attachment","uuid":"a","attachment":{"type":"hook_success"}}`, "attachment", "", "hook_success"},
		{"last-prompt", `{"type":"last-prompt","uuid":"l"}`, "last-prompt", "", ""},
		{"file-history-snapshot", `{"type":"file-history-snapshot","uuid":"f"}`, "file-history-snapshot", "", ""},
	}
	for _, c := range cases {
		n := New(Options{})
		if _, err := n.Feed(0, []byte(c.line)); err != nil {
			t.Fatal(err)
		}
		skippedType := false
		for k := range n.Stats().Skipped {
			if strings.HasPrefix(k, "type:") || strings.HasPrefix(k, "system:") || strings.HasPrefix(k, "attachment:") {
				skippedType = true
			}
		}
		if got := ReadsRow(c.typ, c.sub, c.att); got == skippedType {
			t.Errorf("%s: ReadsRow = %v but the normalizer skipped the type = %v", c.name, got, skippedType)
		}
	}
}
