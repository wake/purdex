package daemon

import (
	"runtime"
	"strings"
	"testing"
)

func TestConfigReadsTheWorkersHostSettings(t *testing.T) {
	body := "[worker]\nenabled = true\nbackground_dev = true\nspeed_factor = 1.5\n"
	if runtime.GOOS == "linux" {
		body += "sandbox_user = \"wwd-job\"\n"
	}
	c, err := load(t, body)
	if err != nil {
		t.Fatal(err)
	}
	if !c.Worker.BackgroundDev || c.Worker.SpeedFactor != 1.5 {
		t.Fatalf("worker = %+v", c.Worker)
	}
	if runtime.GOOS == "linux" && c.Worker.SandboxUser != "wwd-job" {
		t.Fatalf("sandbox_user = %q", c.Worker.SandboxUser)
	}
}

func TestConfigWorkerSettingsDefaultToNone(t *testing.T) {
	c, err := load(t, "[worker]\nenabled = true\n")
	if err != nil {
		t.Fatal(err)
	}
	if c.Worker.BackgroundDev || c.Worker.SpeedFactor != 0 || c.Worker.SandboxUser != "" {
		t.Fatalf("worker = %+v: the host settings are off unless the config names them", c.Worker)
	}
}

func TestConfigRejectsABadSpeedFactor(t *testing.T) {
	for _, bad := range []string{"-1", "nan", "inf"} {
		if _, err := load(t, "[worker]\nspeed_factor = "+bad+"\n"); err == nil || !strings.Contains(err.Error(), "speed_factor") {
			t.Errorf("speed_factor = %s accepted (err %v)", bad, err)
		}
	}
}

func TestConfigChecksTheSandboxUser(t *testing.T) {
	for _, bad := range []string{`"root"`, `""x`, `"a b"`, `"-x"`, `"Name"`} {
		if _, err := load(t, "[worker]\nsandbox_user = "+bad+"\n"); err == nil {
			t.Errorf("sandbox_user = %s accepted", bad)
		}
	}
	_, err := load(t, "[worker]\nsandbox_user = \"wwd-job\"\n")
	if runtime.GOOS != "linux" {
		if err == nil || !strings.Contains(err.Error(), "sandbox_user") {
			t.Fatalf("a host that is not Linux has no systemd units: err %v", err)
		}
	} else if err != nil {
		t.Fatalf("a plain account name on Linux: %v", err)
	}
}
