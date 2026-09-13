package peers

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/wake/purdex/internal/agent"
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
	if err != nil {
		t.Fatalf("ReadRegistry: unexpected err for missing dir: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("entries = %v, want empty", entries)
	}
	if skipped != 0 {
		t.Errorf("skipped = %d, want 0", skipped)
	}
}

func TestReadRegistry_PathIsRegularFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "not-a-dir")
	if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	entries, _, err := ReadRegistry(path, allTrueLiveness(wantProcStart))
	if err == nil {
		t.Fatal("ReadRegistry: expected err when path is a regular file, got nil")
	}
	if entries != nil {
		t.Errorf("entries = %v, want nil", entries)
	}
}

// TestReadRegistry_SymlinkSkipped pins Item 3 (#reg-symlink): a "<pid>.json"
// candidate that is a symlink must never be followed — it is skipped, even
// when it points at an otherwise perfectly valid registry file.
func TestReadRegistry_SymlinkSkipped(t *testing.T) {
	dir := t.TempDir()
	outside := t.TempDir()
	targetPath := filepath.Join(outside, "target.json")
	if err := os.WriteFile(targetPath, []byte(fixture76973), 0o644); err != nil {
		t.Fatalf("write target: %v", err)
	}
	linkPath := filepath.Join(dir, "1.json")
	if err := os.Symlink(targetPath, linkPath); err != nil {
		t.Fatalf("symlink: %v", err)
	}

	entries, skipped, err := ReadRegistry(dir, allTrueLiveness(wantProcStart))
	if err != nil {
		t.Fatalf("ReadRegistry: unexpected err: %v", err)
	}
	if skipped != 1 || len(entries) != 0 {
		t.Fatalf("skipped=%d len(entries)=%d, want 1 and 0", skipped, len(entries))
	}
}

// TestReadRegistry_FIFOSkipped pins Item 3: a "<pid>.json" candidate that is
// a FIFO must be skipped WITHOUT blocking the reader — opening a FIFO with
// no writer blocks forever on a naive os.ReadFile, so this must be caught by
// the fstat regular-file check before any blocking read is attempted. The
// call is wrapped in a goroutine with a timeout so a regression that blocks
// fails the test instead of hanging the suite.
func TestReadRegistry_FIFOSkipped(t *testing.T) {
	dir := t.TempDir()
	fifoPath := filepath.Join(dir, "2.json")
	if err := syscall.Mkfifo(fifoPath, 0o600); err != nil {
		t.Fatalf("mkfifo: %v", err)
	}

	type result struct {
		entries []Entry
		skipped int
		err     error
	}
	done := make(chan result, 1)
	go func() {
		entries, skipped, err := ReadRegistry(dir, allTrueLiveness(wantProcStart))
		done <- result{entries, skipped, err}
	}()

	select {
	case r := <-done:
		if r.err != nil {
			t.Fatalf("ReadRegistry: unexpected err: %v", r.err)
		}
		if r.skipped != 1 || len(r.entries) != 0 {
			t.Fatalf("skipped=%d len(entries)=%d, want 1 and 0", r.skipped, len(r.entries))
		}
	case <-time.After(2 * time.Second):
		t.Fatal("ReadRegistry blocked on a FIFO candidate")
	}
}

// TestReadRegistry_OversizedFileSkipped pins Item 3: a "<pid>.json" candidate
// larger than maxRegistryFileBytes (64 KiB) is skipped without being
// unmarshalled.
func TestReadRegistry_OversizedFileSkipped(t *testing.T) {
	dir := t.TempDir()
	big := strings.Repeat("x", 70*1024)
	writeFixture(t, dir, "3.json", big)

	entries, skipped, err := ReadRegistry(dir, allTrueLiveness(wantProcStart))
	if err != nil {
		t.Fatalf("ReadRegistry: unexpected err: %v", err)
	}
	if skipped != 1 || len(entries) != 0 {
		t.Fatalf("skipped=%d len(entries)=%d, want 1 and 0", skipped, len(entries))
	}
}

// TestReadRegistry_ValidOneKiBFileParses pins Item 3's non-regression case:
// a well-formed registry file comfortably under the 64 KiB cap — here padded
// to ~1 KiB — still parses normally.
func TestReadRegistry_ValidOneKiBFileParses(t *testing.T) {
	dir := t.TempDir()
	pad := strings.Repeat("x", 850)
	content := fmt.Sprintf(`{"pid":76973,"sessionId":"fa5d4c07-d9d9-4184-9e13-e491f2f4bf7c","cwd":"/Users/wake/Workspace/%s","procStart":"Sun Sep 13 15:22:36 2026","messagingSocketPath":"/tmp/cc-socks/76973.sock"}`, pad)
	if len(content) < 900 || len(content) > 1200 {
		t.Fatalf("fixture size = %d bytes, want roughly 1 KiB", len(content))
	}
	writeFixture(t, dir, "76973.json", content)

	entries, skipped, err := ReadRegistry(dir, allTrueLiveness(wantProcStart))
	if err != nil {
		t.Fatalf("ReadRegistry: unexpected err: %v", err)
	}
	if skipped != 0 || len(entries) != 1 {
		t.Fatalf("skipped=%d len(entries)=%d, want 0 and 1", skipped, len(entries))
	}
	if entries[0].PID != 76973 {
		t.Errorf("PID = %d, want 76973", entries[0].PID)
	}
}

// TestMaxRegistryFileBytes_Value pins the named constant Item 3 requires, so
// a future change to the cap is a deliberate, visible edit here.
func TestMaxRegistryFileBytes_Value(t *testing.T) {
	if maxRegistryFileBytes != 64*1024 {
		t.Fatalf("maxRegistryFileBytes = %d, want %d", maxRegistryFileBytes, 64*1024)
	}
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
	if live.Stat == nil || live.PidAlive == nil || live.StartTime == nil || live.Info == nil {
		t.Fatal("DefaultLiveness returned a Liveness with nil fields")
	}
}

// --- D9: proxy recognition by argv --------------------------------------

func TestIsProxyProcess(t *testing.T) {
	cases := []struct {
		name string
		info agent.ProcessInfo
		want bool
	}{
		{
			name: "argv0 pdx with peer-proxy",
			info: agent.ProcessInfo{Argv: []string{"/usr/local/bin/pdx", "peer-proxy"}},
			want: true,
		},
		{
			name: "renamed argv0 but ExePath pdx",
			info: agent.ProcessInfo{ExePath: "/opt/pdx", Argv: []string{"pdx-renamed", "peer-proxy"}},
			want: true,
		},
		{
			name: "pdx alone, no peer-proxy arg",
			info: agent.ProcessInfo{Argv: []string{"pdx"}},
			want: false,
		},
		{
			name: "node running a peer-proxy.js script",
			info: agent.ProcessInfo{Argv: []string{"node", "peer-proxy.js"}},
			want: false,
		},
		{
			name: "empty argv",
			info: agent.ProcessInfo{Argv: nil},
			want: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := IsProxyProcess(tc.info); got != tc.want {
				t.Errorf("IsProxyProcess(%+v) = %v, want %v", tc.info, got, tc.want)
			}
		})
	}
}

// infoLiveness returns a Liveness with Stat/PidAlive always-true and Info
// set to infoFn; StartTime is left nil since, with Info set, ReadRegistry
// must never call it.
func infoLiveness(infoFn func(pid int) (agent.ProcessInfo, error)) Liveness {
	return Liveness{
		Stat:     func(path string) error { return nil },
		PidAlive: func(pid int) bool { return true },
		StartTime: func(pid int) (time.Time, error) {
			panic("StartTime must not be called when Info is set")
		},
		Info: infoFn,
	}
}

// TestReadRegistry_InfoClassifiesProxy pins D9: a fake Info reporting a pdx
// peer-proxy helper's argv sets Entry.IsProxy, using the Info-supplied
// StartTime for liveness (never falling back to the (panicking) StartTime
// field).
func TestReadRegistry_InfoClassifiesProxy(t *testing.T) {
	dir := t.TempDir()
	writeFixture(t, dir, "76973.json", fixture76973)

	live := infoLiveness(func(pid int) (agent.ProcessInfo, error) {
		return agent.ProcessInfo{
			Argv:      []string{"pdx", "peer-proxy"},
			StartTime: wantProcStart,
		}, nil
	})

	entries, skipped, err := ReadRegistry(dir, live)
	if err != nil {
		t.Fatalf("ReadRegistry: unexpected err: %v", err)
	}
	if skipped != 0 || len(entries) != 1 {
		t.Fatalf("skipped=%d len(entries)=%d, want 0 and 1", skipped, len(entries))
	}
	if !entries[0].IsProxy {
		t.Errorf("IsProxy = false, want true")
	}
}

// TestReadRegistry_InfoNonProxyEntry pins the non-proxy path: a fake Info
// reporting an ordinary cc argv leaves IsProxy false.
func TestReadRegistry_InfoNonProxyEntry(t *testing.T) {
	dir := t.TempDir()
	writeFixture(t, dir, "76973.json", fixture76973)

	live := infoLiveness(func(pid int) (agent.ProcessInfo, error) {
		return agent.ProcessInfo{
			Argv:      []string{"node", "/usr/local/bin/claude"},
			StartTime: wantProcStart,
		}, nil
	})

	entries, skipped, err := ReadRegistry(dir, live)
	if err != nil {
		t.Fatalf("ReadRegistry: unexpected err: %v", err)
	}
	if skipped != 0 || len(entries) != 1 {
		t.Fatalf("skipped=%d len(entries)=%d, want 0 and 1", skipped, len(entries))
	}
	if entries[0].IsProxy {
		t.Errorf("IsProxy = true, want false")
	}
}

// TestReadRegistry_InfoErrorFailsClosed pins the fail-closed rule (D9): an
// entry whose Info call errors must be skipped like a dead entry, never
// defaulting to IsProxy=false and being treated as a deliverable cc row.
func TestReadRegistry_InfoErrorFailsClosed(t *testing.T) {
	dir := t.TempDir()
	writeFixture(t, dir, "76973.json", fixture76973)

	live := infoLiveness(func(pid int) (agent.ProcessInfo, error) {
		return agent.ProcessInfo{}, fmt.Errorf("permission denied reading /proc/%d", pid)
	})

	entries, skipped, err := ReadRegistry(dir, live)
	if err != nil {
		t.Fatalf("ReadRegistry: unexpected err: %v", err)
	}
	if skipped != 1 || len(entries) != 0 {
		t.Fatalf("skipped=%d len(entries)=%d, want 1 and 0", skipped, len(entries))
	}
}

// TestReadRegistry_InfoEmptyArgvFailsClosed pins the fail-closed rule (D9)
// for the other unclassifiable case: Info succeeds but returns an empty
// Argv (e.g. a race where the process already exited).
func TestReadRegistry_InfoEmptyArgvFailsClosed(t *testing.T) {
	dir := t.TempDir()
	writeFixture(t, dir, "76973.json", fixture76973)

	live := infoLiveness(func(pid int) (agent.ProcessInfo, error) {
		return agent.ProcessInfo{Argv: nil, StartTime: wantProcStart}, nil
	})

	entries, skipped, err := ReadRegistry(dir, live)
	if err != nil {
		t.Fatalf("ReadRegistry: unexpected err: %v", err)
	}
	if skipped != 1 || len(entries) != 0 {
		t.Fatalf("skipped=%d len(entries)=%d, want 1 and 0", skipped, len(entries))
	}
}

// TestReadRegistry_NilInfoUnchanged pins backward compatibility: P1 fakes
// that set only StartTime (Info == nil) must behave exactly as before —
// this is already covered by every other test in this file (allTrueLiveness
// never sets Info), but this test makes the invariant explicit: a nil Info
// never fails an entry closed, and IsProxy defaults to false.
func TestReadRegistry_NilInfoUnchanged(t *testing.T) {
	dir := t.TempDir()
	writeFixture(t, dir, "76973.json", fixture76973)

	live := allTrueLiveness(wantProcStart)
	if live.Info != nil {
		t.Fatal("allTrueLiveness must not set Info")
	}

	entries, skipped, err := ReadRegistry(dir, live)
	if err != nil {
		t.Fatalf("ReadRegistry: unexpected err: %v", err)
	}
	if skipped != 0 || len(entries) != 1 {
		t.Fatalf("skipped=%d len(entries)=%d, want 0 and 1", skipped, len(entries))
	}
	if entries[0].IsProxy {
		t.Errorf("IsProxy = true, want false (Info is nil)")
	}
}
