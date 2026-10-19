package install

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The limits of wwd.slice, the slice the worker's jobs run in on a Linux host, are installed as a systemd drop-in (#3549): the jobs
// have their own limits, but the slice they share had none, so two jobs together could take the whole machine.

func sliceDropIn(in *Installer) string {
	return filepath.Join(filepath.Dir(in.unitPath()), "wwd.slice.d", "10-wwd-limits.conf")
}

func (f *fakeRun) count(prefix string) int {
	n := 0
	for _, c := range f.calls {
		if strings.HasPrefix(c, prefix) {
			n++
		}
	}
	return n
}

func writeConfig(t *testing.T, in *Installer, body string) {
	t.Helper()
	if err := os.MkdirAll(in.ConfigDir(), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(in.ConfigPath(), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestInstallLinuxWritesTheSliceDropInWithTheDefaults(t *testing.T) {
	in, f, _ := newInstaller(t, "linux", "mserv", writeBin(t, t.TempDir(), "x"))
	if err := in.Install(context.Background()); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(sliceDropIn(in))
	if err != nil {
		t.Fatalf("no drop-in: %v", err)
	}
	for _, line := range []string{"[Slice]\n", "CPUWeight=50\n", "CPUQuota=300%\n", "MemoryMax=6G\n", "MemorySwapMax=0\n", "TasksMax=4096\n"} {
		if !strings.Contains(string(b), line) {
			t.Errorf("missing %q in\n%s", line, b)
		}
	}
	if !f.has("sudo install -D -m 0644") {
		t.Errorf("the drop-in is installed by sudo install -D (the directory may not exist): %v", f.calls)
	}
	// the drop-in is in place before systemd is told to read it
	drop, reload := -1, -1
	for i, c := range f.calls {
		if strings.Contains(c, "wwd.slice.d") && strings.HasPrefix(c, "sudo install") {
			drop = i
		}
		if strings.HasPrefix(c, "sudo systemctl daemon-reload") {
			reload = i
		}
	}
	if drop < 0 || reload < 0 || drop > reload {
		t.Errorf("drop-in at %d, daemon-reload at %d: %v", drop, reload, f.calls)
	}
}

// Nothing changed: nothing is written, systemd is not asked to reload, wwd is not restarted.
func TestInstallLinuxSliceDropInIsIdempotent(t *testing.T) {
	in, f, _ := newInstaller(t, "linux", "mserv", writeBin(t, t.TempDir(), "x"))
	if err := in.Install(context.Background()); err != nil {
		t.Fatal(err)
	}
	f.calls = nil
	if err := in.Install(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(f.calls) != 0 && !(len(f.calls) == 1 && strings.HasPrefix(f.calls[0], "systemctl is-active")) {
		t.Errorf("an unchanged install did something: %v", f.calls)
	}
}

// [slice] in the config is the override, one value at a time. A changed value is written and read by systemd (daemon-reload); the running
// wwd is not restarted for it: a slice's limits apply to the cgroup as soon as systemd has read them.
func TestInstallLinuxSliceOverrideFromConfigReloadsWithoutRestart(t *testing.T) {
	in, f, _ := newInstaller(t, "linux", "mserv", writeBin(t, t.TempDir(), "x"))
	if err := in.Install(context.Background()); err != nil {
		t.Fatal(err)
	}
	f.calls = nil
	writeConfig(t, in, "alias = \"mserv\"\n[slice]\nmemory_max = \"4G\"\ncpu_quota = \"200%\"\n")
	if err := in.Install(context.Background()); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(sliceDropIn(in))
	if !strings.Contains(string(b), "MemoryMax=4G\n") || !strings.Contains(string(b), "CPUQuota=200%\n") || !strings.Contains(string(b), "CPUWeight=50\n") {
		t.Errorf("the override, with the other defaults kept:\n%s", b)
	}
	if f.count("sudo systemctl daemon-reload") != 1 {
		t.Errorf("the change is read once: %v", f.calls)
	}
	if f.has("sudo systemctl restart") || f.has("sudo systemctl enable") {
		t.Errorf("a slice limit does not restart wwd: %v", f.calls)
	}
}

// A drop-in that was written but whose reload failed is read on the next run, though the file now matches.
func TestInstallLinuxSliceReloadsAgainAfterAFailedReload(t *testing.T) {
	in, f, _ := newInstaller(t, "linux", "mserv", writeBin(t, t.TempDir(), "x"))
	if err := in.Install(context.Background()); err != nil {
		t.Fatal(err)
	}
	writeConfig(t, in, "alias = \"mserv\"\n[slice]\nmemory_max = \"5G\"\n")
	f.failOn["sudo systemctl daemon-reload"] = true
	if err := in.Install(context.Background()); err == nil {
		t.Fatal("a failed daemon-reload was not reported")
	}
	f.failOn["sudo systemctl daemon-reload"] = false
	f.calls = nil
	if err := in.Install(context.Background()); err != nil {
		t.Fatal(err)
	}
	if f.count("sudo systemctl daemon-reload") != 1 {
		t.Errorf("the file matches but systemd never read it: %v", f.calls)
	}
}

// What is written to a file systemd reads as root is checked first: nothing is changed on a bad value.
func TestInstallLinuxRefusesABadSliceValue(t *testing.T) {
	in, f, _ := newInstaller(t, "linux", "mserv", writeBin(t, t.TempDir(), "x"))
	writeConfig(t, in, "alias = \"mserv\"\n[slice]\nmemory_max = \"6G\\nExecStart=/bin/sh\"\n")
	if err := in.Install(context.Background()); err == nil || !strings.Contains(err.Error(), "memory_max") {
		t.Fatalf("err = %v", err)
	}
	if _, err := os.Stat(sliceDropIn(in)); err == nil {
		t.Error("a drop-in was written")
	}
	if f.has("sudo install") {
		t.Errorf("sudo was used: %v", f.calls)
	}
}

// wwd refuses an unknown key in [slice] when it starts; the installer must not install the defaults for a misspelled key and only then
// leave wwd unable to start (R2).
func TestInstallLinuxRefusesAnUnknownSliceKeyBeforeChangingAnything(t *testing.T) {
	in, f, _ := newInstaller(t, "linux", "mserv", writeBin(t, t.TempDir(), "x"))
	writeConfig(t, in, "alias = \"mserv\"\n[slice]\nmemory_mx = \"4G\"\n")
	err := in.Install(context.Background())
	if err == nil || !strings.Contains(err.Error(), "memory_mx") {
		t.Fatalf("err = %v", err)
	}
	if len(f.calls) != 0 {
		t.Errorf("something was run: %v", f.calls)
	}
	if _, err := os.Stat(in.BinPath()); err == nil {
		t.Error("the binary was installed")
	}
	// a key of another table is not this table's business
	writeConfig(t, in, "alias = \"mserv\"\n[worker]\nenabled = true\n[slice]\nmemory_max = \"4G\"\n")
	if err := in.Install(context.Background()); err != nil {
		t.Fatalf("a valid config: %v", err)
	}
}

// Only a Linux host has a wwd.slice; --no-restart and --dry-run leave the machine alone.
func TestSliceDropInOnlyOnLinuxAndNeverOnDryRunOrNoRestart(t *testing.T) {
	in, f, _ := newInstaller(t, "darwin", "mlab", writeBin(t, t.TempDir(), "x"))
	if err := in.Install(context.Background()); err != nil {
		t.Fatal(err)
	}
	if f.has("sudo") {
		t.Errorf("sudo on macOS: %v", f.calls)
	}

	in, f, out := newInstaller(t, "linux", "mserv", writeBin(t, t.TempDir(), "x"))
	in.DryRun = true
	if err := in.Install(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(sliceDropIn(in)); err == nil || f.has("sudo") {
		t.Errorf("dry run changed something: %v", f.calls)
	}
	if !strings.Contains(out.String(), "wwd.slice.d") {
		t.Errorf("dry run does not say it would write the drop-in:\n%s", out)
	}

	in, f, out = newInstaller(t, "linux", "mserv", writeBin(t, t.TempDir(), "x"))
	in.NoRestart = true
	if err := in.Install(context.Background()); err != nil {
		t.Fatal(err)
	}
	if f.has("sudo systemctl") {
		t.Errorf("--no-restart told systemd something: %v", f.calls)
	}
	if !strings.Contains(out.String(), "daemon-reload") {
		t.Errorf("--no-restart does not say the drop-in is not read yet:\n%s", out)
	}
}

func TestUninstallLinuxRemovesTheSliceDropIn(t *testing.T) {
	in, f, _ := newInstaller(t, "linux", "mserv", writeBin(t, t.TempDir(), "x"))
	if err := in.Install(context.Background()); err != nil {
		t.Fatal(err)
	}
	f.calls = nil
	f.failOn["systemctl is-active"] = true // inactive once stopped
	f.says = map[string]string{"systemctl is-active": "inactive"}
	if err := in.Uninstall(context.Background(), false); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(sliceDropIn(in)); err == nil {
		t.Error("the drop-in is still there")
	}
	if !f.has("sudo rm -f " + sliceDropIn(in)) {
		t.Errorf("not removed by sudo rm: %v", f.calls)
	}
}
