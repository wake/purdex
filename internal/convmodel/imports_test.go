package convmodel

import (
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
)

const (
	selfImport   = "github.com/wake/purdex/internal/convmodel"
	ccnormImport = selfImport + "/ccnorm"
	scrubImport  = ccnormImport + "/scrub"
)

// toolAllowed is the one exception to the boundary: the fixture tools under
// ccnorm/cmd and ccnorm/scrub (U1-4d) may also import ccnorm itself, for its
// ReadFields table, and the scrub package. Everything else, a third-party
// module or any other internal package included, stays rejected for them too.
func toolAllowed(path string) []string {
	p := filepath.ToSlash(path)
	if strings.HasPrefix(p, "ccnorm/cmd/") || strings.HasPrefix(p, "ccnorm/scrub/") {
		return []string{selfImport, ccnormImport, scrubImport}
	}
	return []string{selfImport}
}

// TestImportBoundary keeps the conversation model reusable from other
// modules (U1-5, U4): the non-test files of convmodel import only the
// standard library, and those of convmodel/ccnorm (any depth) only the
// standard library and convmodel. A path is standard library when its first
// element has no dot.
//
// It parses imports only, so a file added later is checked automatically.
func TestImportBoundary(t *testing.T) {
	checked := 0

	n, bad, err := checkImports(".", false, nil)
	if err != nil {
		t.Fatal(err)
	}
	checked += n
	for _, b := range bad {
		t.Error(b)
	}

	n, bad, err = checkImportsFor("ccnorm", true, toolAllowed)
	switch {
	case os.IsNotExist(err):
		// ccnorm arrives in U1-4b
	case err != nil:
		t.Fatal(err)
	default:
		checked += n
		for _, b := range bad {
			t.Error(b)
		}
	}

	if checked == 0 {
		t.Fatal("no non-test .go files found; import boundary check did not run")
	}
}

// The per-path allowance is what keeps the exception narrow: the same import
// is accepted under cmd/ and scrub/ and refused anywhere else, and the
// exception does not widen to anything but ccnorm itself and scrub.
func TestToolAllowed_IsNarrow(t *testing.T) {
	root := t.TempDir()
	write := func(rel, src string) {
		t.Helper()
		p := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	imp := func(pkg string) string { return "package x\nimport _ \"" + pkg + "\"\n" }
	write("ccnorm/cmd/tool/main.go", imp(ccnormImport))
	write("ccnorm/scrub/scrub.go", imp(ccnormImport))
	write("ccnorm/cmd/tool/other.go", imp(scrubImport))
	write("ccnorm/normalizer.go", imp(ccnormImport))                                  // a ccnorm file importing ccnorm: refused
	write("ccnorm/cmd/tool/lights.go", imp("github.com/wake/purdex/internal/lights")) // another internal package: refused
	write("ccnorm/scrub/third.go", imp("github.com/spf13/cobra"))                     // third party: refused
	write("ccnorm/other/x.go", imp(scrubImport))                                      // scrub from elsewhere: refused

	wd, _ := os.Getwd()
	if err := os.Chdir(root); err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(wd)
	_, bad, err := checkImportsFor("ccnorm", true, toolAllowed)
	if err != nil {
		t.Fatal(err)
	}
	if len(bad) != 4 {
		t.Fatalf("violations = %q, want exactly the four refused imports", bad)
	}
	for _, want := range []string{"ccnorm/normalizer.go", "lights.go", "third.go", "ccnorm/other/x.go"} {
		found := false
		for _, b := range bad {
			found = found || strings.Contains(b, want)
		}
		if !found {
			t.Errorf("no violation for %s in %q", want, bad)
		}
	}
}

// The checker must flag a third-party import in a nested directory, must not
// descend when not asked to, and must honour the allowed set.
func TestCheckImports_Recursion(t *testing.T) {
	root := t.TempDir()
	write := func(rel, src string) {
		t.Helper()
		p := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("top.go", "package x\nimport \"fmt\"\nvar _ = fmt.Sprint\n")
	write("cmd/x/main.go", "package main\nimport _ \"github.com/spf13/cobra\"\n")
	write("cmd/x/main_test.go", "package main\nimport _ \"github.com/stretchr/testify\"\n")
	write("ok/ok.go", "package ok\nimport _ \""+selfImport+"\"\n")

	n, bad, err := checkImports(root, true, []string{selfImport})
	if err != nil {
		t.Fatal(err)
	}
	if n != 3 {
		t.Errorf("checked %d files, want 3 (test files skipped)", n)
	}
	if len(bad) != 1 || !strings.Contains(bad[0], "cobra") || !strings.Contains(bad[0], "main.go") {
		t.Errorf("violations = %q, want exactly the nested cobra import", bad)
	}

	n, bad, err = checkImports(root, false, nil)
	if err != nil || n != 1 || len(bad) != 0 {
		t.Errorf("non-recursive: n=%d bad=%q err=%v, want only top.go and no violations", n, bad, err)
	}

	n, bad, _ = checkImports(root, true, nil)
	if n != 3 || len(bad) != 2 {
		t.Errorf("without the allowed set: n=%d bad=%q, want the self import flagged too", n, bad)
	}
}

// checkImports parses the imports of the non-test .go files under root (all
// depths when recursive, else root only) and returns how many it checked and
// one message per import that is neither standard library nor in allowed.
func checkImports(root string, recursive bool, allowed []string) (checked int, violations []string, err error) {
	return checkImportsFor(root, recursive, func(string) []string { return allowed })
}

// checkImportsFor is checkImports with the allowed set chosen per file path.
func checkImportsFor(root string, recursive bool, allowedFor func(path string) []string) (checked int, violations []string, err error) {
	fset := token.NewFileSet()
	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, werr error) error {
		if werr != nil {
			return werr
		}
		if d.IsDir() {
			if path != root && !recursive {
				return filepath.SkipDir
			}
			return nil
		}
		name := d.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			return nil
		}
		checked++
		f, perr := parser.ParseFile(fset, path, nil, parser.ImportsOnly)
		if perr != nil {
			return perr
		}
		allowed := allowedFor(path)
		for _, imp := range f.Imports {
			p, uerr := strconv.Unquote(imp.Path.Value)
			if uerr != nil {
				return uerr
			}
			first, _, _ := strings.Cut(p, "/")
			if !strings.Contains(first, ".") || slices.Contains(allowed, p) {
				continue // standard library, or explicitly allowed
			}
			violations = append(violations, path+" imports "+strconv.Quote(p)+": only the standard library"+allowedSuffix(allowed)+" may be imported")
		}
		return nil
	})
	return checked, violations, err
}

func allowedSuffix(extra []string) string {
	if len(extra) == 0 {
		return ""
	}
	return " and " + strings.Join(extra, ", ")
}
