package statuspending

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/wake/purdex/internal/config"
)

func payload(sid, model string) []byte {
	return []byte(fmt.Sprintf(`{"session_id":%q,"model":{"id":%q}}`, sid, model))
}

const sidA = "0a1b2c3d-0000-4000-8000-000000000001"

func TestDirFor_IsUnderTheDataDir(t *testing.T) {
	cfg := &config.Config{DataDir: "/data/pdx"}
	if got := DirFor(cfg); got != "/data/pdx/statusline-pending" {
		t.Fatalf("dir = %q", got)
	}
}

// Only a name that cannot leave the directory or hide is a file name. Mutation: SafeID true for all → the traversal cases write.
func TestSafeID(t *testing.T) {
	for _, ok := range []string{sidA, "abc", "A_b-9", "ses_123"} {
		if !SafeID(ok) {
			t.Errorf("%q refused", ok)
		}
	}
	for _, bad := range []string{"", ".", "..", "../x", "a/b", `a\b`, ".hidden", "a b", "a\x00b", "a.json", string(make([]byte, 129))} {
		if SafeID(bad) {
			t.Errorf("%q accepted", bad)
		}
	}
}

func TestSessionIDOf(t *testing.T) {
	if got := SessionIDOf(payload(sidA, "m")); got != sidA {
		t.Fatalf("got %q", got)
	}
	for _, raw := range []string{`{}`, `not json`, ``, `{"session_id":5}`} {
		if got := SessionIDOf([]byte(raw)); got != "" {
			t.Fatalf("%q → %q", raw, got)
		}
	}
}

// The file is where DirFor says, private, and carries the payload and its time. Mutation: no write → red; modes loosened → red.
func TestWrite_WritesAPrivateFile(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "statusline-pending")
	if err := Write(dir, payload(sidA, "claude-opus-5-5"), 1000); err != nil {
		t.Fatal(err)
	}
	di, err := os.Stat(dir)
	if err != nil || di.Mode().Perm() != 0o700 {
		t.Fatalf("dir: %v %v", di, err)
	}
	fi, err := os.Stat(filepath.Join(dir, sidA+".json"))
	if err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("file: %v %v", fi, err)
	}
	got, err := Load(dir)
	if err != nil || len(got) != 1 || got[0].SessionID != sidA || got[0].AtMs != 1000 {
		t.Fatalf("load = %+v (%v)", got, err)
	}
	var m struct{ Model struct{ ID string } }
	if json.Unmarshal(got[0].Raw, &m) != nil || m.Model.ID != "claude-opus-5-5" {
		t.Fatalf("raw = %s", got[0].Raw)
	}
	entries, _ := os.ReadDir(dir)
	n := 0
	for _, e := range entries {
		if e.Name() != lockName {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("a temp file was left: %v", entries)
	}
}

// Only the newest is kept, in either order of arrival. Mutations: an older payload replaces a newer one → red; a newer one does
// not replace an older → red.
func TestWrite_TheNewestWins(t *testing.T) {
	dir := t.TempDir()
	Write(dir, payload(sidA, "old"), 1000)
	Write(dir, payload(sidA, "new"), 2000)
	Write(dir, payload(sidA, "late-but-old"), 1500) // a slow render process finishing last
	got, _ := Load(dir)
	if len(got) != 1 || got[0].AtMs != 2000 || string(SessionIDOf(got[0].Raw)) != sidA {
		t.Fatalf("load = %+v", got)
	}
	var m struct{ Model struct{ ID string } }
	json.Unmarshal(got[0].Raw, &m)
	if m.Model.ID != "new" {
		t.Fatalf("kept %q, want new", m.Model.ID)
	}
}

// A payload with no safe session id is dropped, as it was before: nothing is written and nothing escapes the directory.
func TestWrite_UnsafeOrMissingSessionIDWritesNothing(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "p")
	for _, raw := range [][]byte{payload("../escape", "m"), payload("a/b", "m"), payload("", "m"), []byte(`not json`)} {
		if err := Write(dir, raw, 1); err == nil {
			t.Fatalf("%s accepted", raw)
		}
	}
	var seen []string
	filepath.Walk(root, func(p string, _ os.FileInfo, _ error) error { seen = append(seen, p); return nil })
	for _, p := range seen {
		if filepath.Ext(p) == ".json" {
			t.Fatalf("a file was written: %s", p)
		}
	}
}

// A full directory takes no new session (an update of an existing one is still fine). Mutation: no cap → red.
func TestWrite_TheDirectoryIsCapped(t *testing.T) {
	dir := t.TempDir()
	for i := 0; i < Cap; i++ {
		if err := Write(dir, payload(fmt.Sprintf("s%04d", i), "m"), 1); err != nil {
			t.Fatalf("%d: %v", i, err)
		}
	}
	if err := Write(dir, payload("one-too-many", "m"), 1); err != ErrFull {
		t.Fatalf("err = %v, want ErrFull", err)
	}
	if err := Write(dir, payload("s0003", "m2"), 2); err != nil {
		t.Fatalf("an existing session is still updated: %v", err)
	}
}

// A successful delivery removes the file only when it is not newer than the delivery. Mutations: never removes → red; removes a
// newer one → red.
func TestCleanupOnSuccess(t *testing.T) {
	dir := t.TempDir()
	Write(dir, payload(sidA, "m"), 1000)
	CleanupOnSuccess(dir, payload(sidA, "m"), 900) // an older render succeeded: the newer failure must stay
	if got, _ := Load(dir); len(got) != 1 {
		t.Fatal("a newer pending payload was removed")
	}
	CleanupOnSuccess(dir, payload(sidA, "m"), 1000)
	if got, _ := Load(dir); len(got) != 0 {
		t.Fatal("the delivered session's file stayed")
	}
	CleanupOnSuccess(filepath.Join(dir, "nope"), payload(sidA, "m"), 1) // no directory: nothing happens
	CleanupOnSuccess(dir, []byte(`not json`), 1)
}

// Load drops what is not a valid entry: a file whose name is not its session id, a corrupt one, a stray temp.
func TestLoad_DeletesTheInvalid(t *testing.T) {
	dir := t.TempDir()
	Write(dir, payload(sidA, "m"), 1000)
	os.WriteFile(filepath.Join(dir, "other.json"), []byte(`{"at_ms":1,"raw_status":`+string(payload(sidA, "m"))+`}`), 0o600) // name ≠ session id
	os.WriteFile(filepath.Join(dir, "bad.json"), []byte(`nope`), 0o600)
	got, err := Load(dir)
	if err != nil || len(got) != 1 || got[0].SessionID != sidA {
		t.Fatalf("load = %+v (%v)", got, err)
	}
	for _, name := range []string{"other.json", "bad.json"} {
		if _, err := os.Stat(filepath.Join(dir, name)); err == nil {
			t.Fatalf("%s was kept", name)
		}
	}
	if got, err := Load(filepath.Join(dir, "missing")); err != nil || len(got) != 0 {
		t.Fatalf("a missing directory is empty: %v %v", got, err)
	}
}

// The cost on the render path: what a delivery that worked adds. Reported in the PR.
func BenchmarkCleanupOnSuccess_NoDirectory(b *testing.B) {
	dir := filepath.Join(b.TempDir(), "missing")
	raw := payload(sidA, "m")
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		CleanupOnSuccess(dir, raw, 1)
	}
}

func BenchmarkCleanupOnSuccess_DirectoryNoFile(b *testing.B) {
	dir := b.TempDir()
	raw := payload(sidA, "m")
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		CleanupOnSuccess(dir, raw, 1)
	}
}

func TestRemove(t *testing.T) {
	dir := t.TempDir()
	Write(dir, payload(sidA, "m"), 1)
	Remove(dir, "../x") // not a safe name: nothing
	Remove(dir, sidA)
	if got, _ := Load(dir); len(got) != 0 {
		t.Fatal("not removed")
	}
}
