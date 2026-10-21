package config_test

import "testing"

// [watch] wake_source names the host that holds the watcher's wake settings (docs/specs/2026-10-21-watcher-wake-settings-spec.md section 4):
// mlab unless config.toml says otherwise.
func TestWatch_WakeSourceDefaultsToMlab(t *testing.T) {
	for _, body := range []string{"port = 9000\n", "[watch]\n", "[watch]\nwake_source = \"\"\n", "[watch]\nwake_source = \"  \"\n"} {
		if got := loadToml(t, body).WakeSource(); got != "mlab" {
			t.Errorf("%q: wake_source = %q", body, got)
		}
	}
}

func TestWatch_WakeSourceIsRead(t *testing.T) {
	if got := loadToml(t, "[watch]\nwake_source = \"air26\"\n").WakeSource(); got != "air26" {
		t.Fatalf("wake_source = %q", got)
	}
}

func TestWatch_CloneDoesNotShareTheSection(t *testing.T) {
	cfg := loadToml(t, "[watch]\nwake_source = \"air26\"\n")
	c := cfg.Clone()
	c.Watch.WakeSource = "changed"
	if cfg.WakeSource() != "air26" {
		t.Fatal("Clone shares the [watch] section")
	}
}
