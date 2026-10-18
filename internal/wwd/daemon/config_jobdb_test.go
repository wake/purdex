package daemon

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestConfigJobDB(t *testing.T) {
	if c, err := loadCfg(t, ""); err != nil || c.JobDB.Enabled {
		t.Fatalf("off by default: %+v %v", c.JobDB, err)
	}
	home := t.TempDir()
	cnf := filepath.Join(home, "admin.cnf")
	if err := os.WriteFile(cnf, []byte("[client]\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	load := func(body string) (Config, error) {
		p := filepath.Join(home, "config.toml")
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		return LoadConfig(p, home)
	}
	c, err := load("[jobdb]\nenabled = true\nhost = \"100.64.0.6\"\nport = 3307\nuser_hosts = [\"localhost\"]\ndefaults_file = \"~/admin.cnf\"\n")
	if err != nil {
		t.Fatal(err)
	}
	if c.JobDB.Port != 3307 || c.JobDB.Host != "100.64.0.6" || c.JobDB.DefaultsFile != cnf {
		t.Fatalf("%+v", c.JobDB)
	}
	open := filepath.Join(home, "open.cnf")
	if err := os.WriteFile(open, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{
		"no defaults file":  "[jobdb]\nenabled = true\n",
		"relative file":     "[jobdb]\nenabled = true\ndefaults_file = \"a.cnf\"\n",
		"missing file":      "[jobdb]\nenabled = true\ndefaults_file = \"" + home + "/none.cnf\"\n",
		"group-readable":    "[jobdb]\nenabled = true\ndefaults_file = \"" + open + "\"\n",
		"a directory":       "[jobdb]\nenabled = true\ndefaults_file = \"" + home + "\"\n",
		"bad user host":     "[jobdb]\nenabled = true\nuser_hosts = [\"%\"]\ndefaults_file = \"" + cnf + "\"\n",
		"port out of range": "[jobdb]\nenabled = true\nport = 70000\ndefaults_file = \"" + cnf + "\"\n",
		"unknown key":       "[jobdb]\nenabled = false\nbogus = 1\n",
	} {
		if _, err := load(body); err == nil || (name != "unknown key" && !strings.Contains(err.Error(), "jobdb")) {
			t.Errorf("%s: want a jobdb error, got %v", name, err)
		}
	}
}
