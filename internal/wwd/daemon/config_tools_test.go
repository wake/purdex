package daemon

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadConfigToolsExtraPath(t *testing.T) {
	home := t.TempDir()
	p := filepath.Join(home, "c.toml")
	write := func(body string) {
		t.Helper()
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	// ~/ is kept as written (the daemon resolves and checks it at start); an absolute path is cleaned; both are accepted
	write("[tools]\nextra_path = [\"~/Assist/toolchains.noindex/node/bin\", \"/opt/x//bin/\"]\n")
	c, err := LoadConfig(p, home)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(c.Tools.ExtraPath, "|"); got != "~/Assist/toolchains.noindex/node/bin|/opt/x/bin" {
		t.Errorf("extra_path = %q", got)
	}

	// a relative path would depend on the daemon's working directory
	write("[tools]\nextra_path = [\"node/bin\"]\n")
	if _, err := LoadConfig(p, home); err == nil || !strings.Contains(err.Error(), "tools.extra_path") {
		t.Errorf("a relative entry must fail naming the key: %v", err)
	}

	// a PATH separator would split one entry into two unchecked ones
	write("[tools]\nextra_path = [\"/a:/b\"]\n")
	if _, err := LoadConfig(p, home); err == nil {
		t.Error("an entry holding a path separator must fail")
	}

	write("[tools]\nextra_path = [\"\"]\n")
	if _, err := LoadConfig(p, home); err == nil {
		t.Error("an empty entry must fail")
	}
}
