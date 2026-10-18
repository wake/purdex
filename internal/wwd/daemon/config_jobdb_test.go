package daemon

import (
	"strings"
	"testing"
)

func TestConfigJobDB(t *testing.T) {
	if c, err := loadCfg(t, ""); err != nil || c.JobDB.Enabled {
		t.Fatalf("off by default: %+v %v", c.JobDB, err)
	}
	c, err := loadCfg(t, "[jobdb]\nenabled = true\nhost = \"100.64.0.6\"\nport = 3307\nuser_hosts = [\"localhost\"]\ndefaults_file = \"~/.config/wwd/mysql-admin.cnf\"\n")
	if err != nil {
		t.Fatal(err)
	}
	if c.JobDB.Port != 3307 || c.JobDB.Host != "100.64.0.6" || strings.HasPrefix(c.JobDB.DefaultsFile, "~") ||
		!strings.HasSuffix(c.JobDB.DefaultsFile, "/.config/wwd/mysql-admin.cnf") {
		t.Fatalf("%+v", c.JobDB)
	}
	for name, body := range map[string]string{
		"no defaults file":  "[jobdb]\nenabled = true\n",
		"relative file":     "[jobdb]\nenabled = true\ndefaults_file = \"a.cnf\"\n",
		"port out of range": "[jobdb]\nenabled = true\nport = 70000\ndefaults_file = \"/a.cnf\"\n",
		"unknown key":       "[jobdb]\nenabled = false\nbogus = 1\n",
	} {
		if _, err := loadCfg(t, body); err == nil {
			t.Errorf("%s: want an error", name)
		}
	}
}
