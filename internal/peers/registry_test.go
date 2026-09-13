package peers

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// fixture76973 is the real registry-file shape captured from mlab (pid 76973).
const fixture76973 = `{"pid":76973,"sessionId":"fa5d4c07-d9d9-4184-9e13-e491f2f4bf7c","cwd":"/Users/wake/Workspace/wake/purdex","startedAt":1789314156000,"procStart":"Sun Sep 13 15:22:36 2026","version":"2.1.270","peerProtocol":1,"peerFeatures":["notify_idle","reply_across_default_dirs","artifact_yield"],"kind":"interactive","entrypoint":"cli","pidDomain":"darwin","tmux":"mt1:@10.%10","messagingSocketPath":"/tmp/cc-socks/76973.sock","name":"purdex-47","nameSource":"derived","nameSince":1789314156000,"updatedAt":1789314156100,"status":"busy","statusUpdatedAt":1789314156100}`

// wantProcStart is the instant fixture76973's procStart denotes (UTC).
var wantProcStart = time.Date(2026, 9, 13, 15, 22, 36, 0, time.UTC)

func writeFixture(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatalf("write fixture %s: %v", name, err)
	}
}

// allTrueLiveness returns a Liveness whose Stat/PidAlive always succeed and
// whose StartTime always returns startTime.
func allTrueLiveness(startTime time.Time) Liveness {
	return Liveness{
		Stat:      func(path string) error { return nil },
		PidAlive:  func(pid int) bool { return true },
		StartTime: func(pid int) (time.Time, error) { return startTime, nil },
	}
}

func TestReadRegistry_HappyPath(t *testing.T) {
	dir := t.TempDir()
	writeFixture(t, dir, "76973.json", fixture76973)

	entries, skipped, err := ReadRegistry(dir, allTrueLiveness(wantProcStart))
	if err != nil {
		t.Fatalf("ReadRegistry: unexpected err: %v", err)
	}
	if skipped != 0 {
		t.Fatalf("skipped = %d, want 0", skipped)
	}
	if len(entries) != 1 {
		t.Fatalf("len(entries) = %d, want 1", len(entries))
	}

	e := entries[0]
	if e.PID != 76973 {
		t.Errorf("PID = %d, want 76973", e.PID)
	}
	if e.SessionID != "fa5d4c07-d9d9-4184-9e13-e491f2f4bf7c" {
		t.Errorf("SessionID = %q", e.SessionID)
	}
	if e.Cwd != "/Users/wake/Workspace/wake/purdex" {
		t.Errorf("Cwd = %q", e.Cwd)
	}
	if e.Tmux != "mt1:@10.%10" {
		t.Errorf("Tmux = %q", e.Tmux)
	}
	if e.Inbox != "/tmp/cc-socks/76973.sock" {
		t.Errorf("Inbox = %q", e.Inbox)
	}
	if e.ProcStart != "Sun Sep 13 15:22:36 2026" {
		t.Errorf("ProcStart = %q", e.ProcStart)
	}
	if e.Version != "2.1.270" {
		t.Errorf("Version = %q", e.Version)
	}
	if e.Name != "purdex-47" {
		t.Errorf("Name = %q", e.Name)
	}
	if e.NameSource != "derived" {
		t.Errorf("NameSource = %q", e.NameSource)
	}
	if e.Status != "busy" {
		t.Errorf("Status = %q", e.Status)
	}

	if got := e.TmuxSessionName(); got != "mt1" {
		t.Errorf("TmuxSessionName() = %q, want mt1", got)
	}
	if got := e.TmuxPaneID(); got != "%10" {
		t.Errorf("TmuxPaneID() = %q, want %%10", got)
	}
}

func TestReadRegistry_SameInstantDifferentZone(t *testing.T) {
	dir := t.TempDir()
	writeFixture(t, dir, "76973.json", fixture76973)

	// 2026-09-13T23:22:36+08:00 == 2026-09-13T15:22:36Z
	cst := time.Date(2026, 9, 13, 23, 22, 36, 0, time.FixedZone("CST", 8*3600))
	entries, skipped, err := ReadRegistry(dir, allTrueLiveness(cst))
	if err != nil {
		t.Fatalf("ReadRegistry: unexpected err: %v", err)
	}
	if skipped != 0 || len(entries) != 1 {
		t.Fatalf("skipped=%d len(entries)=%d, want 0 and 1", skipped, len(entries))
	}
}

func TestReadRegistry_OneSecondOff(t *testing.T) {
	dir := t.TempDir()
	writeFixture(t, dir, "76973.json", fixture76973)

	off := wantProcStart.Add(time.Second)
	entries, skipped, err := ReadRegistry(dir, allTrueLiveness(off))
	if err != nil {
		t.Fatalf("ReadRegistry: unexpected err: %v", err)
	}
	if skipped != 1 {
		t.Fatalf("skipped = %d, want 1", skipped)
	}
	if len(entries) != 0 {
		t.Fatalf("len(entries) = %d, want 0", len(entries))
	}
}

func TestReadRegistry_PidNotAlive(t *testing.T) {
	dir := t.TempDir()
	writeFixture(t, dir, "76973.json", fixture76973)

	live := allTrueLiveness(wantProcStart)
	live.PidAlive = func(pid int) bool { return false }

	entries, skipped, err := ReadRegistry(dir, live)
	if err != nil {
		t.Fatalf("ReadRegistry: unexpected err: %v", err)
	}
	if skipped != 1 || len(entries) != 0 {
		t.Fatalf("skipped=%d len(entries)=%d, want 1 and 0", skipped, len(entries))
	}
}

func TestReadRegistry_StatError(t *testing.T) {
	dir := t.TempDir()
	writeFixture(t, dir, "76973.json", fixture76973)

	live := allTrueLiveness(wantProcStart)
	live.Stat = func(path string) error { return os.ErrNotExist }

	entries, skipped, err := ReadRegistry(dir, live)
	if err != nil {
		t.Fatalf("ReadRegistry: unexpected err: %v", err)
	}
	if skipped != 1 || len(entries) != 0 {
		t.Fatalf("skipped=%d len(entries)=%d, want 1 and 0", skipped, len(entries))
	}
}

func TestReadRegistry_StartTimeError(t *testing.T) {
	dir := t.TempDir()
	writeFixture(t, dir, "76973.json", fixture76973)

	live := allTrueLiveness(wantProcStart)
	live.StartTime = func(pid int) (time.Time, error) { return time.Time{}, os.ErrPermission }

	entries, skipped, err := ReadRegistry(dir, live)
	if err != nil {
		t.Fatalf("ReadRegistry: unexpected err: %v", err)
	}
	if skipped != 1 || len(entries) != 0 {
		t.Fatalf("skipped=%d len(entries)=%d, want 1 and 0", skipped, len(entries))
	}
}

func TestReadRegistry_MissingSessionID(t *testing.T) {
	dir := t.TempDir()
	content := `{"pid":76973,"cwd":"/x","procStart":"Sun Sep 13 15:22:36 2026","messagingSocketPath":"/tmp/cc-socks/76973.sock"}`
	writeFixture(t, dir, "76973.json", content)

	entries, skipped, err := ReadRegistry(dir, allTrueLiveness(wantProcStart))
	if err != nil {
		t.Fatalf("ReadRegistry: unexpected err: %v", err)
	}
	if skipped != 1 || len(entries) != 0 {
		t.Fatalf("skipped=%d len(entries)=%d, want 1 and 0", skipped, len(entries))
	}
}

func TestReadRegistry_PidAsString(t *testing.T) {
	dir := t.TempDir()
	content := `{"pid":"76973","sessionId":"s","procStart":"Sun Sep 13 15:22:36 2026","messagingSocketPath":"/tmp/cc-socks/76973.sock"}`
	writeFixture(t, dir, "76973.json", content)

	entries, skipped, err := ReadRegistry(dir, allTrueLiveness(wantProcStart))
	if err != nil {
		t.Fatalf("ReadRegistry: unexpected err: %v", err)
	}
	if skipped != 1 || len(entries) != 0 {
		t.Fatalf("skipped=%d len(entries)=%d, want 1 and 0", skipped, len(entries))
	}
}

func TestReadRegistry_BadProcStart(t *testing.T) {
	dir := t.TempDir()
	content := `{"pid":76973,"sessionId":"s","procStart":"yesterday","messagingSocketPath":"/tmp/cc-socks/76973.sock"}`
	writeFixture(t, dir, "76973.json", content)

	entries, skipped, err := ReadRegistry(dir, allTrueLiveness(wantProcStart))
	if err != nil {
		t.Fatalf("ReadRegistry: unexpected err: %v", err)
	}
	if skipped != 1 || len(entries) != 0 {
		t.Fatalf("skipped=%d len(entries)=%d, want 1 and 0", skipped, len(entries))
	}
}

func TestReadRegistry_PidMismatchWithFilename(t *testing.T) {
	dir := t.TempDir()
	content := `{"pid":76974,"sessionId":"s","procStart":"Sun Sep 13 15:22:36 2026","messagingSocketPath":"/tmp/cc-socks/76973.sock"}`
	writeFixture(t, dir, "76973.json", content)

	entries, skipped, err := ReadRegistry(dir, allTrueLiveness(wantProcStart))
	if err != nil {
		t.Fatalf("ReadRegistry: unexpected err: %v", err)
	}
	if skipped != 1 || len(entries) != 0 {
		t.Fatalf("skipped=%d len(entries)=%d, want 1 and 0", skipped, len(entries))
	}
}

func TestReadRegistry_UnreadableFile(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root: file permissions do not block reads")
	}
	dir := t.TempDir()
	writeFixture(t, dir, "76973.json", fixture76973)
	path := filepath.Join(dir, "76973.json")
	if err := os.Chmod(path, 0o000); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(path, 0o644) })

	entries, skipped, err := ReadRegistry(dir, allTrueLiveness(wantProcStart))
	if err != nil {
		t.Fatalf("ReadRegistry: unexpected err: %v", err)
	}
	if skipped != 1 || len(entries) != 0 {
		t.Fatalf("skipped=%d len(entries)=%d, want 1 and 0", skipped, len(entries))
	}
}

func TestReadRegistry_MalformedJSON(t *testing.T) {
	dir := t.TempDir()
	writeFixture(t, dir, "76973.json", `{not json`)

	entries, skipped, err := ReadRegistry(dir, allTrueLiveness(wantProcStart))
	if err != nil {
		t.Fatalf("ReadRegistry: unexpected err: %v", err)
	}
	if skipped != 1 || len(entries) != 0 {
		t.Fatalf("skipped=%d len(entries)=%d, want 1 and 0", skipped, len(entries))
	}
}

func TestReadRegistry_NonMatchingFilesIgnored(t *testing.T) {
	dir := t.TempDir()
	writeFixture(t, dir, "76973.json", fixture76973)
	writeFixture(t, dir, ".key", "secret")
	writeFixture(t, dir, "76973.json.tmp.abc", "{}")
	writeFixture(t, dir, "notes.txt", "hello")

	entries, skipped, err := ReadRegistry(dir, allTrueLiveness(wantProcStart))
	if err != nil {
		t.Fatalf("ReadRegistry: unexpected err: %v", err)
	}
	if skipped != 0 {
		t.Fatalf("skipped = %d, want 0 (non-matching files must not count)", skipped)
	}
	if len(entries) != 1 {
		t.Fatalf("len(entries) = %d, want 1", len(entries))
	}
}

func TestReadRegistry_MissingDir(t *testing.T) {
	entries, skipped, err := ReadRegistry(filepath.Join(t.TempDir(), "does-not-exist"), allTrueLiveness(wantProcStart))
	if err == nil {
		t.Fatal("ReadRegistry: expected err for missing dir, got nil")
	}
	if entries != nil {
		t.Errorf("entries = %v, want nil", entries)
	}
	_ = skipped
}

func TestEntry_TmuxAccessors(t *testing.T) {
	empty := Entry{Tmux: ""}
	if got := empty.TmuxSessionName(); got != "" {
		t.Errorf("TmuxSessionName() = %q, want empty", got)
	}
	if got := empty.TmuxPaneID(); got != "" {
		t.Errorf("TmuxPaneID() = %q, want empty", got)
	}

	noPane := Entry{Tmux: "a:b"}
	if got := noPane.TmuxSessionName(); got != "a" {
		t.Errorf("TmuxSessionName() = %q, want a", got)
	}
	if got := noPane.TmuxPaneID(); got != "" {
		t.Errorf("TmuxPaneID() = %q, want empty", got)
	}
}

func TestParseProcStart(t *testing.T) {
	got, err := ParseProcStart("Sun Sep 13 15:22:36 2026")
	if err != nil {
		t.Fatalf("ParseProcStart: unexpected err: %v", err)
	}
	if !got.Equal(wantProcStart) {
		t.Errorf("ParseProcStart() = %v, want %v", got, wantProcStart)
	}

	if _, err := ParseProcStart("yesterday"); err == nil {
		t.Error("ParseProcStart(\"yesterday\"): expected err, got nil")
	}
}

func TestDefaultLiveness_Compiles(t *testing.T) {
	live := DefaultLiveness()
	if live.Stat == nil || live.PidAlive == nil || live.StartTime == nil {
		t.Fatal("DefaultLiveness returned a Liveness with nil fields")
	}
}
