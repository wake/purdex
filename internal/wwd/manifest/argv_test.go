package manifest

import (
	"reflect"
	"strconv"
	"testing"
)

func taskWithRun(t *testing.T, run, args string) *Task {
	t.Helper()
	src := "[tasks.a]\nrun = " + strconv.Quote(run) + "\nargs = " + strconv.Quote(args) + "\n"
	if args == "pass" {
		src += "local = true\n"
	}
	return mustParse(t, src).Tasks["a"]
}

func TestRenderFilesArgv(t *testing.T) {
	task := mustParse(t, sample).Tasks["spa-file"]
	got, err := task.Render([]string{"src/a.test.ts", "src/b c.test.ts"}, RenderEnv{
		Report: "/w/report.json", Workspace: "/w",
		Exists: exists("spa/src/a.test.ts", "spa/src/b c.test.ts"),
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"pnpm", "vitest", "run", "--reporter=json", "--outputFile=/w/report.json", "src/a.test.ts", "src/b c.test.ts"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got  %q\nwant %q", got, want)
	}
}

func TestRenderValuesStayLiteral(t *testing.T) {
	task := mustParse(t, sample).Tasks["spa-file"]
	name := "$(touch x) `y` ;*~'\".ts"
	got, err := task.Render([]string{name}, RenderEnv{
		Report: "/w/r.json", Workspace: "/w", Exists: exists("spa/" + name),
	})
	if err != nil {
		t.Fatal(err)
	}
	if got[len(got)-1] != name {
		t.Fatalf("value altered: %q", got)
	}
}

func TestRenderFilesRejected(t *testing.T) {
	task := mustParse(t, sample).Tasks["spa-file"]
	env := RenderEnv{Report: "/w/r", Workspace: "/w", Exists: exists("spa/src/a.ts")}
	for name, args := range map[string][]string{
		"none":       nil,
		"dotdot":     {"../go.mod"},
		"inner dot2": {"src/../../go.mod"},
		"absolute":   {"/etc/passwd"},
		"missing":    {"src/nope.ts"},
		"empty":      {""},
		"flag":       {"--config=evil.js"},
		"nul":        {"src/a.ts\x00"},
	} {
		if _, err := task.Render(args, env); err == nil {
			t.Errorf("%s: Render succeeded, want error", name)
		}
	}
}

func TestRenderPackages(t *testing.T) {
	task := taskWithRun(t, "go test -json {packages}", "packages")
	got, err := task.Render([]string{"./internal/wwd/...", "./cmd/pdx"}, RenderEnv{})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"go", "test", "-json", "./internal/wwd/...", "./cmd/pdx"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %q", got)
	}
	for _, bad := range []string{"../x", "./a/../b", "-run=x", "a;b", "$(x)", "./a b", ""} {
		if _, err := task.Render([]string{bad}, RenderEnv{}); err == nil {
			t.Errorf("package %q accepted", bad)
		}
	}
}

func TestRenderNoneAndPass(t *testing.T) {
	lint := taskWithRun(t, "make lint", "none")
	if got, err := lint.Render(nil, RenderEnv{}); err != nil || !reflect.DeepEqual(got, []string{"make", "lint"}) {
		t.Fatalf("lint = %q, %v", got, err)
	}
	if _, err := lint.Render([]string{"x"}, RenderEnv{}); err == nil {
		t.Fatal("none task accepted arguments")
	}
	pass := taskWithRun(t, "go vet", "pass")
	got, err := pass.Render([]string{"./a", "-x"}, RenderEnv{})
	if err != nil || !reflect.DeepEqual(got, []string{"go", "vet", "./a", "-x"}) {
		t.Fatalf("pass = %q, %v", got, err)
	}
}

func TestArgvQuoting(t *testing.T) {
	task := taskWithRun(t, `tool 'a b' "c \"d\" \\ e" 'x$y' "$HOME" {packages}`, "packages")
	got, err := task.Render([]string{"./p"}, RenderEnv{})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"tool", "a b", `c "d" \ e`, "x$y", "$HOME", "./p"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %q\nwant %q", got, want)
	}
}

func TestParseRejectsShellFeatures(t *testing.T) {
	for name, run := range map[string]string{
		"pipe":          `a | b`,
		"and":           `a && b`,
		"semicolon":     `a; b`,
		"redirect out":  `a > out`,
		"redirect in":   `a < in`,
		"dollar":        `a $HOME`,
		"cmd subst":     `a $(b)`,
		"backtick":      "a `b`",
		"backslash":     `a\ b`,
		"unterminated":  `a 'b`,
		"unterminated2": `a "b`,
	} {
		src := "[tasks.a]\nrun = " + strconv.Quote(run) + "\nargs = \"none\"\n"
		if _, err := Parse([]byte(src)); err == nil {
			t.Errorf("%s: Parse accepted %s", name, run)
		}
	}
}

func TestParsePlaceholderPlacement(t *testing.T) {
	for name, run := range map[string]string{
		"quoted single": `tool '{files}'`,
		"quoted double": `tool "{files}"`,
		"glued files":   `tool x{files}`,
		"glued files 2": `tool {files}x`,
		"two files":     `tool {files} {files}`,
		"program":       `{files}`,
	} {
		src := "[tasks.a]\nrun = " + strconv.Quote(run) + "\nargs = \"files\"\n"
		if _, err := Parse([]byte(src)); err == nil {
			t.Errorf("%s: Parse accepted %s", name, run)
		}
	}
	// output placeholders may be glued to a word, and quoted braces that are
	// not placeholders stay literal
	task := taskWithRun(t, `tool --out={report} {a,b}`, "none")
	got, err := task.Render(nil, RenderEnv{Report: "/w/r", Workspace: "/w"})
	if err != nil || !reflect.DeepEqual(got, []string{"tool", "--out=/w/r", "{a,b}"}) {
		t.Fatalf("got %q, %v", got, err)
	}
}

func TestRenderOutputPathContainment(t *testing.T) {
	task := mustParse(t, sample).Tasks["spa-file"]
	ex := exists("spa/src/a.ts")
	for name, env := range map[string]RenderEnv{
		"empty":         {Report: "", Workspace: "/w", Exists: ex},
		"no workspace":  {Report: "/w/r.json", Exists: ex},
		"outside":       {Report: "/etc/r.json", Workspace: "/w", Exists: ex},
		"dotdot":        {Report: "/w/../etc/r.json", Workspace: "/w", Exists: ex},
		"sibling":       {Report: "/w2/r.json", Workspace: "/w", Exists: ex},
		"workspace dir": {Report: "/w", Workspace: "/w", Exists: ex},
		"relative":      {Report: "r.json", Workspace: "/w", Exists: ex},
		"nul":           {Report: "/w/r\x00.json", Workspace: "/w", Exists: ex},
	} {
		if _, err := task.Render([]string{"src/a.ts"}, env); err == nil {
			t.Errorf("%s: Render accepted report path %q", name, env.Report)
		}
	}
}
