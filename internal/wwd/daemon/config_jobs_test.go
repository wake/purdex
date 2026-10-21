package daemon

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ---- [jobs.env]: variables the host gives to its jobs (#3933) ----

func loadIn(t *testing.T, body string) (Config, string, error) {
	t.Helper()
	home := t.TempDir()
	p := filepath.Join(home, "c.toml")
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := LoadConfig(p, home)
	return c, home, err
}

func TestConfigJobsEnvDefaultsToNone(t *testing.T) {
	c, _, err := loadIn(t, "[worker]\nenabled = true\n")
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Jobs.Env) != 0 {
		t.Fatalf("jobs.env = %v", c.Jobs.Env)
	}
}

// ~ and ~/... at the start of a value are the home of the account wwd runs as (like [tools] extra_path and [repos]); nothing else is expanded.
func TestConfigJobsEnvExpandsALeadingTilde(t *testing.T) {
	c, home, err := loadIn(t, `
[jobs.env]
PLAYWRIGHT_BROWSERS_PATH = "~/Assist/cache.noindex/ms-playwright"
THE_HOME = "~"
ABSOLUTE = "/opt/x"
NOT_A_TILDE = "a~/b"
DOLLAR = "$HOME/x"
ZERO = "0"
OTHER_USER = "~someone/x"
`)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"PLAYWRIGHT_BROWSERS_PATH": filepath.Join(home, "Assist", "cache.noindex", "ms-playwright"),
		"THE_HOME":                 home,
		"ABSOLUTE":                 "/opt/x",
		"NOT_A_TILDE":              "a~/b",
		"DOLLAR":                   "$HOME/x",
		"ZERO":                     "0", // PLAYWRIGHT_BROWSERS_PATH=0 is a real value: no absolute path is asked for
		"OTHER_USER":               "~someone/x",
	}
	for k, w := range want {
		if got := c.Jobs.Env[k]; got != w {
			t.Errorf("%s = %q, want %q", k, got, w)
		}
	}
	if len(c.Jobs.Env) != len(want) {
		t.Errorf("jobs.env = %v", c.Jobs.Env)
	}
}

// What a job's workspace sets, what identifies the job, and what the runner or the tool list own cannot be set by the host: wwd does not start.
func TestConfigJobsEnvRefusesTheNamesThatAreNotTheHostsToSet(t *testing.T) {
	for _, name := range []string{
		"HOME", "TMPDIR", "XDG_CONFIG_HOME", "XDG_CACHE_HOME", "XDG_DATA_HOME", "XDG_STATE_HOME", "XDG_RUNTIME_DIR", // the workspace's
		"USER", "LOGNAME", // the account the command runs as (the runner sets them)
		"WWD_JOB", "WWD_ORIGIN", "WWD_ANYTHING", "PDX_GATE_DIR", "PDX_ANYTHING", // the marks and the Purdex state directories
		"PATH", // [tools] extra_path is how a host shapes it
		"TERM", // the runner's
	} {
		_, _, err := loadIn(t, "[jobs.env]\n"+name+" = \"x\"\n")
		if err == nil || !strings.Contains(err.Error(), "jobs.env") || !strings.Contains(err.Error(), name) {
			t.Errorf("%s was accepted or the error does not say why: %v", name, err)
		}
	}
}

func TestConfigJobsEnvRefusesNamesThatAreNotVariableNames(t *testing.T) {
	for _, name := range []string{`"1BAD"`, `"A-B"`, `"--unset=X"`, `"A B"`, `""`, `"A=B"`} {
		if _, _, err := loadIn(t, "[jobs.env]\n"+name+" = \"x\"\n"); err == nil || !strings.Contains(err.Error(), "jobs.env") {
			t.Errorf("name %s: %v", name, err)
		}
	}
}

func TestConfigJobsEnvRefusesAValueWithANUL(t *testing.T) {
	if _, _, err := loadIn(t, "[jobs.env]\nX = \"a\\u0000b\"\n"); err == nil || !strings.Contains(err.Error(), "jobs.env") {
		t.Fatalf("a NUL in a value: %v", err)
	}
}

// The config is read strictly: a key [jobs] does not have is a mistake, not something to ignore.
func TestConfigJobsRefusesAnUnknownKey(t *testing.T) {
	if _, _, err := loadIn(t, "[jobs]\nenviroment = 1\n"); err == nil {
		t.Fatal("an unknown key under [jobs] was accepted")
	}
}

// A value is a string: a number or a table is refused by the reader, with the name in the message.
func TestConfigJobsEnvRefusesAValueThatIsNotAString(t *testing.T) {
	if _, _, err := loadIn(t, "[jobs.env]\nX = 1\n"); err == nil {
		t.Fatal("a number was taken for a value")
	}
}
