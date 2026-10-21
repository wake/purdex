package daemon

import (
	"runtime"
	"strings"
	"testing"
	"time"
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
	if c.Worker.BackgroundDev || c.Worker.SpeedFactor != 0 || c.Worker.SandboxUser != "" || c.Worker.ExplicitLabelsOnly {
		t.Fatalf("worker = %+v: the host settings are off unless the config names them", c.Worker)
	}
}

func TestConfigReadsTheDiskFloor(t *testing.T) {
	c, err := load(t, "[worker]\ndisk_min_gb = 25.5\n")
	if err != nil || c.Worker.DiskMinGB == nil || *c.Worker.DiskMinGB != 25.5 {
		t.Fatalf("disk_min_gb: %+v %v", c.Worker.DiskMinGB, err)
	}
	if c, err = load(t, "[worker]\ndisk_min_gb = 0\n"); err != nil || c.Worker.DiskMinGB == nil || *c.Worker.DiskMinGB != 0 {
		t.Fatalf("0 is a floor of its own (none): %+v %v", c.Worker.DiskMinGB, err)
	}
	if c, err = load(t, "[worker]\nenabled = true\n"); err != nil || c.Worker.DiskMinGB != nil {
		t.Fatalf("not set: %+v %v", c.Worker.DiskMinGB, err)
	}
}

func TestConfigRefusesADiskFloorThatCannotBeOne(t *testing.T) {
	for _, v := range []string{"-1", "2000000"} {
		if _, err := load(t, "[worker]\ndisk_min_gb = "+v+"\n"); err == nil || !strings.Contains(err.Error(), "disk_min_gb") {
			t.Errorf("disk_min_gb = %s: %v", v, err)
		}
	}
}

func TestConfigReadsExplicitLabelsOnly(t *testing.T) {
	c, err := load(t, "[worker]\nexplicit_labels_only = true\n")
	if err != nil {
		t.Fatal(err)
	}
	if !c.Worker.ExplicitLabelsOnly {
		t.Fatalf("worker = %+v", c.Worker)
	}
}

func TestConfigRejectsABadSpeedFactor(t *testing.T) {
	for _, bad := range []string{"-1", "nan", "inf"} {
		if _, err := load(t, "[worker]\nspeed_factor = "+bad+"\n"); err == nil || !strings.Contains(err.Error(), "speed_factor") {
			t.Errorf("speed_factor = %s accepted (err %v)", bad, err)
		}
	}
}

// The dependency cache: its directory is <data_dir>/deps unless set, and it keeps 2 sets unless worker.deps_keep says (1 on a host short
// of disk); a count that cannot be is refused.
func TestConfigReadsTheDependencyCacheSettings(t *testing.T) {
	c, err := load(t, "[worker]\nenabled = true\n")
	if err != nil {
		t.Fatal(err)
	}
	if c.DepsRoot != c.DataDir+"/deps" || c.Worker.DepsKeep != 0 {
		t.Fatalf("defaults: deps_root %q deps_keep %d (data_dir %q)", c.DepsRoot, c.Worker.DepsKeep, c.DataDir)
	}
	c, err = load(t, "deps_root = \"/data/deps\"\n[worker]\nenabled = true\ndeps_keep = 1\n")
	if err != nil {
		t.Fatal(err)
	}
	if c.DepsRoot != "/data/deps" || c.Worker.DepsKeep != 1 {
		t.Fatalf("set: deps_root %q deps_keep %d", c.DepsRoot, c.Worker.DepsKeep)
	}
	for _, bad := range []string{"-1", "51"} {
		if _, err := load(t, "[worker]\ndeps_keep = "+bad+"\n"); err == nil || !strings.Contains(err.Error(), "deps_keep") {
			t.Errorf("deps_keep = %s accepted (err %v)", bad, err)
		}
	}
	c, err = load(t, "[worker]\ndeps_store_max_mb = 512\n")
	if err != nil || c.Worker.DepsStoreMaxMB != 512 {
		t.Fatalf("deps_store_max_mb: %+v %v", c.Worker, err)
	}
	for _, bad := range []string{"-1", "1000001"} {
		if _, err := load(t, "[worker]\ndeps_store_max_mb = "+bad+"\n"); err == nil || !strings.Contains(err.Error(), "deps_store_max_mb") {
			t.Errorf("deps_store_max_mb = %s accepted (err %v)", bad, err)
		}
	}
	c, err = load(t, "[worker]\ngocache_max_mb = 20480\n")
	if err != nil || c.Worker.GoCacheMaxMB != 20480 {
		t.Fatalf("gocache_max_mb: %+v %v", c.Worker, err)
	}
	if c, err = load(t, "[worker]\n"); err != nil || c.Worker.GoCacheMaxMB != 0 {
		t.Fatalf("gocache_max_mb unset is 0, the default of 10240: %+v %v", c.Worker, err)
	}
	for _, bad := range []string{"-1", "1000001"} {
		if _, err := load(t, "[worker]\ngocache_max_mb = "+bad+"\n"); err == nil || !strings.Contains(err.Error(), "gocache_max_mb") {
			t.Errorf("gocache_max_mb = %s accepted (err %v)", bad, err)
		}
	}
	if c, err = load(t, "[worker]\ngocache_prune_hours = 6\n"); err != nil || c.Worker.GoCachePruneHours != 6 {
		t.Fatalf("gocache_prune_hours: %+v %v", c.Worker, err)
	}
	if c, err = load(t, "[worker]\n"); err != nil || c.Worker.GoCachePruneHours != 0 {
		t.Fatalf("gocache_prune_hours unset is 0, the default of 24: %+v %v", c.Worker, err)
	}
	for _, bad := range []string{"-1", "8761"} {
		if _, err := load(t, "[worker]\ngocache_prune_hours = "+bad+"\n"); err == nil || !strings.Contains(err.Error(), "gocache_prune_hours") {
			t.Errorf("gocache_prune_hours = %s accepted (err %v)", bad, err)
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

// #3904: [worker] defer_patience_seconds. Not set is 120 seconds; 0 switches the leaving of jobs to a faster host off; a number is that many seconds.
func TestConfigDeferPatience(t *testing.T) {
	c, err := load(t, "[worker]\nenabled = true\n")
	if err != nil || c.Worker.DeferPatience() != 2*time.Minute {
		t.Fatalf("not set: %v %v", c.Worker.DeferPatience(), err)
	}
	if c, err = load(t, "[worker]\ndefer_patience_seconds = 0\n"); err != nil || c.Worker.DeferPatience() != 0 {
		t.Fatalf("0 is off: %v %v", c.Worker.DeferPatience(), err)
	}
	if c, err = load(t, "[worker]\ndefer_patience_seconds = 300\n"); err != nil || c.Worker.DeferPatience() != 5*time.Minute {
		t.Fatalf("300: %v %v", c.Worker.DeferPatience(), err)
	}
	for _, bad := range []string{"-1", "86401"} {
		if _, err := load(t, "[worker]\ndefer_patience_seconds = "+bad+"\n"); err == nil || !strings.Contains(err.Error(), "defer_patience_seconds") {
			t.Errorf("defer_patience_seconds = %s accepted (err %v)", bad, err)
		}
	}
}
