package manifest

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/wake/purdex/internal/wwd/wire"
)

const sample = `
[entry]
mode = "rewrite"

[tasks.spa-file]
run     = "pnpm vitest run --reporter=json --outputFile={report} {files}"
cwd     = "spa"
args    = "files"
local   = true
summary = "vitest-json"

[tasks.spa-related]
run       = "pnpm vitest related --run --reporter=json --outputFile={report} {files}"
cwd       = "spa"
args      = "files"
class     = "merge"
labels    = ["os:darwin", "repo:purdex"]
deps      = ["pnpm-lock.yaml"]
cacheable = true
timeout   = "30m"
summary   = "vitest-json"
qualify   = { args = ["src/lib/crypto-hash.test.ts"], reference = "air26" }
`

func mustParse(t *testing.T, src string) *Manifest {
	t.Helper()
	m, err := Parse([]byte(src))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	return m
}

func TestParseSample(t *testing.T) {
	m := mustParse(t, sample)
	if m.Entry.Mode != EntryRewrite {
		t.Fatalf("entry mode = %q", m.Entry.Mode)
	}
	rel := m.Tasks["spa-related"]
	if rel == nil {
		t.Fatal("spa-related missing")
	}
	if rel.Name != "spa-related" || rel.Class != wire.ClassMerge || rel.Timeout != 30*time.Minute || !rel.Cacheable {
		t.Fatalf("spa-related = %+v", rel)
	}
	if rel.Qualify == nil || rel.Qualify.Reference != "air26" || len(rel.Qualify.Args) != 1 {
		t.Fatalf("qualify = %+v", rel.Qualify)
	}
	f := m.Tasks["spa-file"]
	if f.Class != wire.ClassDev {
		t.Fatalf("default class = %q, want dev", f.Class)
	}
	if f.QueueTimeout != 2*time.Hour {
		t.Fatalf("default queue timeout = %v", f.QueueTimeout)
	}
	if !f.Local.Permits(50) {
		t.Fatal("local = true should permit any count")
	}
}

func TestParseDefaults(t *testing.T) {
	m := mustParse(t, "[tasks.a]\nrun = \"make lint\"\nargs = \"none\"\n")
	if m.Entry.Mode != EntryAdvise {
		t.Fatalf("default entry mode = %q", m.Entry.Mode)
	}
	a := m.Tasks["a"]
	if a.Summary != "plain" || a.Timeout != 30*time.Minute || a.Local.Permits(0) {
		t.Fatalf("defaults = %+v", a)
	}
}

func TestParseRejects(t *testing.T) {
	cases := map[string]string{
		"unknown key":            "[tasks.a]\nrun = \"x\"\nargs = \"none\"\nbogus = 1\n",
		"unknown table":          "[nope]\nx = 1\n",
		"empty run":              "[tasks.a]\nargs = \"none\"\n",
		"bad args kind":          "[tasks.a]\nrun = \"x\"\nargs = \"weird\"\n",
		"missing args":           "[tasks.a]\nrun = \"x\"\n",
		"bad class":              "[tasks.a]\nrun = \"x\"\nargs = \"none\"\nclass = \"urgent\"\n",
		"bad summary":            "[tasks.a]\nrun = \"x\"\nargs = \"none\"\nsummary = \"xml\"\n",
		"bad entry mode":         "[entry]\nmode = \"yolo\"\n",
		"abs cwd":                "[tasks.a]\nrun = \"x\"\nargs = \"none\"\ncwd = \"/etc\"\n",
		"dotdot cwd":             "[tasks.a]\nrun = \"x\"\nargs = \"none\"\ncwd = \"../x\"\n",
		"unknown placeholder":    "[tasks.a]\nrun = \"x {bogus}\"\nargs = \"none\"\n",
		"files without kind":     "[tasks.a]\nrun = \"x {files}\"\nargs = \"none\"\n",
		"packages without kind":  "[tasks.a]\nrun = \"x {packages}\"\nargs = \"files\"\n",
		"files kind no slot":     "[tasks.a]\nrun = \"x\"\nargs = \"files\"\n",
		"bad timeout":            "[tasks.a]\nrun = \"x\"\nargs = \"none\"\ntimeout = \"soon\"\n",
		"negative timeout":       "[tasks.a]\nrun = \"x\"\nargs = \"none\"\ntimeout = \"-5m\"\n",
		"bad local":              "[tasks.a]\nrun = \"x\"\nargs = \"none\"\nlocal = \"maybe\"\n",
		"local count wrong kind": "[tasks.a]\nrun = \"x {files}\"\nargs = \"files\"\nlocal = \"packages<=3\"\n",
		"pass not local":         "[tasks.a]\nrun = \"x\"\nargs = \"pass\"\n",
		"junit no placeholder":   "[tasks.a]\nrun = \"x\"\nargs = \"none\"\nsummary = \"junit\"\n",
		"vitest no report":       "[tasks.a]\nrun = \"x\"\nargs = \"none\"\nsummary = \"vitest-json\"\n",
		"bad db":                 "[tasks.a]\nrun = \"x\"\nargs = \"none\"\ndb = \"shared\"\n",
		"bad env":                "[tasks.a]\nrun = \"x\"\nargs = \"none\"\nenv = [\"1BAD=x\"]\n",
		"bad task name":          "[tasks.\"a b\"]\nrun = \"x\"\nargs = \"none\"\n",
		"duplicate env name":     "[tasks.a]\nrun = \"x\"\nargs = \"none\"\nenv = [\"MODE=fast\", \"MODE=slow\"]\n",
		"duplicate lock":         "[tasks.a]\nrun = \"x\"\nargs = \"none\"\nlocks = [\"db\", \"db\"]\n",
	}
	for name, src := range cases {
		if _, err := Parse([]byte(src)); err == nil {
			t.Errorf("%s: Parse succeeded, want error", name)
		}
	}
}

func TestLocalPolicy(t *testing.T) {
	m := mustParse(t, `
[tasks.few]
run = "go test {packages}"
args = "packages"
local = "packages<=2"
[tasks.off]
run = "x"
args = "none"
local = false
`)
	few := m.Tasks["few"].Local
	if !few.Permits(2) || few.Permits(3) {
		t.Fatalf("packages<=2: Permits(2)=%v Permits(3)=%v", few.Permits(2), few.Permits(3))
	}
	if m.Tasks["off"].Local.Permits(0) {
		t.Fatal("local=false must not permit")
	}
}

func exists(set ...string) func(string) bool {
	m := map[string]bool{}
	for _, s := range set {
		m[s] = true
	}
	return func(p string) bool { return m[p] }
}

func TestDefinitionHash(t *testing.T) {
	a := mustParse(t, sample).Tasks["spa-related"]
	b := mustParse(t, sample).Tasks["spa-related"]
	if a.DefinitionHash() != b.DefinitionHash() || len(a.DefinitionHash()) != 64 {
		t.Fatal("hash not stable")
	}
	change := func(from, to string) string {
		return strings.Replace(sample, from, to, 1)
	}
	for name, src := range map[string]string{
		"run": change("vitest related --run", "vitest related"),
		"cwd": change(`cwd       = "spa"`, `cwd       = "spa/src"`),
		"env": change(`cacheable = true`, "cacheable = true\nenv = [\"CI=1\"]"),
	} {
		other := mustParse(t, src).Tasks["spa-related"]
		if other.DefinitionHash() == a.DefinitionHash() {
			t.Errorf("%s change did not change hash", name)
		}
	}
	// fields outside the key must not change it
	c := mustParse(t, change(`timeout   = "30m"`, `timeout   = "45m"`)).Tasks["spa-related"]
	if c.DefinitionHash() != a.DefinitionHash() {
		t.Error("timeout must not be part of the hash")
	}
}

func TestDefinitionHashSummaryEnvOrderAndArgsKind(t *testing.T) {
	x := mustParse(t, "[tasks.a]\nrun=\"x\"\nargs=\"none\"\nenv=[\"A=1\",\"B\"]\n").Tasks["a"]
	y := mustParse(t, "[tasks.a]\nrun=\"x\"\nargs=\"none\"\nenv=[\"B\",\"A=1\"]\n").Tasks["a"]
	if x.DefinitionHash() != y.DefinitionHash() {
		t.Error("env order must not matter")
	}
	z := mustParse(t, "[tasks.a]\nrun=\"x\"\nargs=\"pass\"\nlocal=true\nenv=[\"A=1\",\"B\"]\n").Tasks["a"]
	if x.DefinitionHash() == z.DefinitionHash() {
		t.Error("args kind must be part of the hash")
	}
	s := mustParse(t, "[tasks.a]\nrun=\"x\"\nargs=\"none\"\nsummary=\"go-json\"\nenv=[\"A=1\",\"B\"]\n").Tasks["a"]
	if x.DefinitionHash() == s.DefinitionHash() {
		t.Error("summary must be part of the hash")
	}
}

func TestPurdexManifest(t *testing.T) {
	m, err := Load("../../../.wwd/tasks.toml")
	if err != nil {
		t.Fatalf("Load purdex manifest: %v", err)
	}
	want := map[string]wire.JobClass{
		"spa-file": wire.ClassDev, "go-pkg": wire.ClassDev,
		"spa-related": wire.ClassMerge, "go-changed": wire.ClassMerge, "lint": wire.ClassMerge,
		"execstat-guard": wire.ClassMerge, "tsc": wire.ClassMerge, "go-race": wire.ClassMerge, "mod-changed": wire.ClassMerge, "mod-hooks": wire.ClassMerge,
		"go-all": wire.ClassRelease, "spa-full": wire.ClassRelease,
		"tsc-force": wire.ClassRelease, "mod-all": wire.ClassRelease,
	}
	for name, class := range want {
		task := m.Tasks[name]
		if task == nil {
			t.Errorf("task %s missing", name)
			continue
		}
		if task.Class != class {
			t.Errorf("task %s class = %s, want %s", name, task.Class, class)
		}
	}
	if g := m.Tasks["execstat-guard"]; g != nil && strings.Contains(g.Run, "-run") {
		t.Error("execstat-guard must run without -run")
	}
	if r := m.Tasks["go-race"]; r != nil && !strings.Contains(r.Run, "-race") {
		t.Error("go-race must run the race detector")
	}
	if c := m.Tasks["mod-changed"]; c != nil && !strings.HasSuffix(c.Run, "./cmd/pdx/...") {
		t.Errorf("mod-changed must cover all of cmd/pdx, got %q", c.Run)
	}
	if h := m.Tasks["mod-hooks"]; h != nil && !strings.Contains(h.Run, "plugin test cmd/pdx/plugin/purdex") {
		t.Errorf("mod-hooks must run the mod's own tests, got %q", h.Run)
	}
	if h := m.Tasks["mod-hooks"]; h != nil && !slices.Contains(h.Labels, "tool:claude") {
		t.Error("mod-hooks runs the claude CLI and must need tool:claude (#3908)")
	}
	for _, name := range []string{"go-changed", "go-race", "mod-changed", "mod-hooks"} {
		if task := m.Tasks[name]; task != nil && !slices.Contains(task.Labels, "repo:purdex") {
			t.Errorf("%s lacks the repo:purdex label", name)
		}
	}
	for _, name := range []string{"spa-file", "go-pkg"} {
		if task := m.Tasks[name]; task != nil && !task.Local.Permits(1) {
			t.Errorf("%s must be allowed on the local fast lane", name)
		}
	}
}

func TestDBEnv(t *testing.T) {
	m := mustParse(t, `
[tasks.none]
run = "x"
args = "none"
[tasks.plain]
run = "x"
args = "none"
db = "per-job"
[tasks.wp]
run = "x"
args = "none"
db = "per-job"
db_env = "wptests"
[tasks.named]
run = "x"
args = "none"
db = "per-job"
db_env = "db"
`)
	for task, want := range map[string]string{"none": "", "plain": DBEnvDB, "wp": DBEnvWPTests, "named": DBEnvDB} {
		if got := m.Tasks[task].DBEnv; got != want {
			t.Errorf("%s: DBEnv = %q, want %q", task, got, want)
		}
	}
	for name, src := range map[string]string{
		"db_env without db":   "[tasks.a]\nrun = \"x\"\nargs = \"none\"\ndb_env = \"wptests\"\n",
		"unknown db_env":      "[tasks.a]\nrun = \"x\"\nargs = \"none\"\ndb = \"per-job\"\ndb_env = \"laravel\"\n",
		"empty db_env":        "[tasks.a]\nrun = \"x\"\nargs = \"none\"\ndb = \"per-job\"\ndb_env = \"\"\n",
		"db_env wrong case":   "[tasks.a]\nrun = \"x\"\nargs = \"none\"\ndb = \"per-job\"\ndb_env = \"WPTESTS\"\n",
		"empty db_env, no db": "[tasks.a]\nrun = \"x\"\nargs = \"none\"\ndb_env = \"\"\n",
	} {
		if _, err := Parse([]byte(src)); err == nil {
			t.Errorf("%s: Parse succeeded, want error", name)
		}
	}
}

func TestDefinitionHashSeesTheDatabase(t *testing.T) {
	m := mustParse(t, `
[tasks.none]
run = "x"
args = "none"
[tasks.plain]
run = "x"
args = "none"
db = "per-job"
[tasks.wp]
run = "x"
args = "none"
db = "per-job"
db_env = "wptests"
[tasks.named]
run = "x"
args = "none"
db = "per-job"
db_env = "db"
`)
	h := func(n string) string { return m.Tasks[n].DefinitionHash() }
	if h("none") == h("plain") || h("plain") == h("wp") {
		t.Fatal("a database, or the variable set it is given as, does not change the hash")
	}
	if h("plain") != h("named") {
		t.Fatal("the default db_env and an explicit db hash differently")
	}
}
