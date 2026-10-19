package daemon

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadConfigAdvisorKnownResidents(t *testing.T) {
	home := t.TempDir()
	c, err := LoadConfig(filepath.Join(home, "none.toml"), home)
	if err != nil || len(c.Advisor.KnownResidents) != 0 {
		t.Fatalf("unset means none is known: %+v err %v", c.Advisor, err)
	}
	p := filepath.Join(home, "c.toml")
	os.WriteFile(p, []byte("[advisor]\nknown_residents = [\"OrbStack Helper\", \"mysqld\"]\n"), 0o600)
	c, err = LoadConfig(p, home)
	if err != nil || len(c.Advisor.KnownResidents) != 2 || c.Advisor.KnownResidents[0] != "OrbStack Helper" {
		t.Fatalf("%+v err %v", c.Advisor, err)
	}
}

func TestLoadConfigAdvisorRefusesBadNames(t *testing.T) {
	home := t.TempDir()
	p := filepath.Join(home, "c.toml")
	for name, body := range map[string]string{
		"empty":   "[advisor]\nknown_residents = [\"\"]\n",
		"control": "[advisor]\nknown_residents = [\"a\\nb\"]\n",
		"long":    "[advisor]\nknown_residents = [\"" + strings.Repeat("x", 129) + "\"]\n",
	} {
		os.WriteFile(p, []byte(body), 0o600)
		_, err := LoadConfig(p, home)
		if err == nil || !strings.Contains(err.Error(), "advisor.known_residents") {
			t.Fatalf("%s: must fail naming the key: %v", name, err)
		}
	}
	os.WriteFile(p, []byte("[advisor]\nknown_resident = [\"x\"]\n"), 0o600) // a typo
	if _, err := LoadConfig(p, home); err == nil {
		t.Fatal("unknown key must fail")
	}
}
