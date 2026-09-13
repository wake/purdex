package ccuds

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/wake/purdex/internal/peers"
)

// realFixture is a real Claude Code 2.1.270 registry file (pid 42603),
// used to pin the field set WriteRegistry must mirror.
const realFixture = `{"pid":42603,"sessionId":"f5dd0000-0000-4000-8000-000000000000","cwd":"/Users/wake","startedAt":1789325876833,"procStart":"Sun Sep 13 18:57:56 2026","version":"2.1.270","peerProtocol":1,"peerFeatures":["notify_idle","reply_across_default_dirs","artifact_yield"],"kind":"interactive","entrypoint":"cli","pidDomain":"darwin","messagingSocketPath":"/tmp/cc-socks/42603.sock","name":"wake-1","nameSource":"user","nameSince":1789325876834,"status":"idle","updatedAt":1789327249231,"statusUpdatedAt":1789327249231}`

const (
	testToken     = "0123456789abcdef0123456789abcdef"
	testProcStart = "Sun Sep 13 18:57:56 2026"
)

func sampleEntry(dir string) RegistryEntry {
	return RegistryEntry{
		PID:          4242,
		SessionID:    "7c9e6679-7425-40de-944b-e07fc1f90ae7",
		Name:         "pdx-proxy-air",
		Cwd:          "/Users/wake",
		ProcStart:    testProcStart,
		Version:      "2.1.270",
		Inbox:        filepath.Join(dir, "4242.sock"),
		PidDomain:    "darwin",
		PeerFeatures: []string{"notify_idle", "reply_across_default_dirs", "artifact_yield"},
	}
}

func TestRegistryFiles(t *testing.T) {
	jsonPath, keyPath := RegistryFiles("/reg", 4242, testToken)
	if jsonPath != "/reg/4242.json" {
		t.Fatalf("jsonPath = %q", jsonPath)
	}
	wantKey := fmt.Sprintf("/reg/4242.%x.key", sha256.Sum256([]byte(testToken)))
	if keyPath != wantKey {
		t.Fatalf("keyPath = %q, want %q", keyPath, wantKey)
	}
}

func TestWriteRegistry_MirrorsRealFieldSetAndKeyMode(t *testing.T) {
	dir := t.TempDir()
	e := sampleEntry(dir)
	before := time.Now().UnixMilli()
	created, err := WriteRegistry(dir, e, testToken)
	if err != nil {
		t.Fatalf("WriteRegistry: %v", err)
	}
	jsonPath, keyPath := RegistryFiles(dir, e.PID, testToken)
	if !reflect.DeepEqual(created, []string{jsonPath, keyPath}) {
		t.Fatalf("created = %v, want [%s %s]", created, jsonPath, keyPath)
	}

	// Field set: exactly the keys of a real entry (minus tmux, which a
	// virtual peer has no value for).
	var want, got map[string]any
	if err := json.Unmarshal([]byte(realFixture), &want); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(jsonPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("json file does not parse: %v\n%s", err, raw)
	}
	for k := range want {
		if _, ok := got[k]; !ok {
			t.Errorf("json missing key %q", k)
		}
	}
	for k := range got {
		if _, ok := want[k]; !ok {
			t.Errorf("json has unexpected key %q", k)
		}
	}
	if got["pid"] != float64(4242) || got["sessionId"] != e.SessionID || got["cwd"] != e.Cwd ||
		got["procStart"] != testProcStart || got["version"] != "2.1.270" || got["peerProtocol"] != float64(1) ||
		got["kind"] != "interactive" || got["entrypoint"] != "cli" || got["pidDomain"] != "darwin" ||
		got["messagingSocketPath"] != e.Inbox || got["name"] != e.Name || got["nameSource"] != "user" ||
		got["status"] != "idle" {
		t.Errorf("json values wrong:\n%s", raw)
	}
	if !reflect.DeepEqual(got["peerFeatures"], []any{"notify_idle", "reply_across_default_dirs", "artifact_yield"}) {
		t.Errorf("peerFeatures = %v", got["peerFeatures"])
	}
	for _, k := range []string{"startedAt", "nameSince", "updatedAt", "statusUpdatedAt"} {
		ms, ok := got[k].(float64)
		if !ok || int64(ms) < before || int64(ms) > time.Now().UnixMilli() {
			t.Errorf("%s = %v, want ms timestamp around now", k, got[k])
		}
	}
	jsonInfo, err := os.Stat(jsonPath)
	if err != nil {
		t.Fatal(err)
	}
	if jsonInfo.Mode().Perm() != 0o644 {
		t.Errorf("json mode = %o, want 644", jsonInfo.Mode().Perm())
	}

	// Key file: 0600, exactly {peerToken, procStart, pidDomain}.
	keyInfo, err := os.Stat(keyPath)
	if err != nil {
		t.Fatal(err)
	}
	if keyInfo.Mode().Perm() != 0o600 {
		t.Errorf("key mode = %o, want 600", keyInfo.Mode().Perm())
	}
	rawKey, err := os.ReadFile(keyPath)
	if err != nil {
		t.Fatal(err)
	}
	var key map[string]any
	if err := json.Unmarshal(rawKey, &key); err != nil {
		t.Fatalf("key file does not parse: %v", err)
	}
	wantKey := map[string]any{"peerToken": testToken, "procStart": testProcStart, "pidDomain": "darwin"}
	if !reflect.DeepEqual(key, wantKey) {
		t.Errorf("key = %v, want %v", key, wantKey)
	}
}

func TestWriteRegistry_ReadsBackThroughPeersReadRegistry(t *testing.T) {
	dir := t.TempDir()
	e := sampleEntry(dir)
	if _, err := WriteRegistry(dir, e, testToken); err != nil {
		t.Fatalf("WriteRegistry: %v", err)
	}
	start, err := peers.ParseProcStart(testProcStart)
	if err != nil {
		t.Fatal(err)
	}
	live := peers.Liveness{
		Stat:      func(string) error { return nil },
		PidAlive:  func(int) bool { return true },
		StartTime: func(int) (time.Time, error) { return start, nil },
	}
	entries, skipped, err := peers.ReadRegistry(dir, live)
	if err != nil {
		t.Fatalf("ReadRegistry: %v", err)
	}
	if skipped != 0 || len(entries) != 1 {
		t.Fatalf("skipped=%d len=%d, want 0 and 1", skipped, len(entries))
	}
	got := entries[0]
	if got.PID != e.PID || got.SessionID != e.SessionID || got.Name != e.Name || got.Inbox != e.Inbox ||
		got.ProcStart != e.ProcStart || got.Cwd != e.Cwd || got.Version != e.Version ||
		got.NameSource != "user" || got.Status != "idle" || got.Tmux != "" {
		t.Fatalf("entry = %+v", got)
	}
}

func TestWriteRegistry_RefusesExistingJSON(t *testing.T) {
	dir := t.TempDir()
	e := sampleEntry(dir)
	jsonPath, keyPath := RegistryFiles(dir, e.PID, testToken)
	if err := os.WriteFile(jsonPath, []byte(`{"pid":4242,"foreign":true}`), 0o644); err != nil {
		t.Fatal(err)
	}
	created, err := WriteRegistry(dir, e, testToken)
	if !errors.Is(err, os.ErrExist) {
		t.Fatalf("err = %v, want ErrExist", err)
	}
	if len(created) != 0 {
		t.Fatalf("created = %v, want none", created)
	}
	raw, _ := os.ReadFile(jsonPath)
	if string(raw) != `{"pid":4242,"foreign":true}` {
		t.Fatalf("foreign json was touched: %s", raw)
	}
	if _, err := os.Lstat(keyPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("key must not be created when json is refused: %v", err)
	}
}

func TestWriteRegistry_RollsBackJSONWhenKeyOccupied(t *testing.T) {
	dir := t.TempDir()
	e := sampleEntry(dir)
	jsonPath, keyPath := RegistryFiles(dir, e.PID, testToken)
	if err := os.WriteFile(keyPath, []byte(`{"peerToken":"other"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	created, err := WriteRegistry(dir, e, testToken)
	if !errors.Is(err, os.ErrExist) {
		t.Fatalf("err = %v, want ErrExist", err)
	}
	if len(created) != 0 {
		t.Fatalf("created = %v, want none after rollback", created)
	}
	if _, err := os.Lstat(jsonPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("json must be rolled back: %v", err)
	}
	raw, _ := os.ReadFile(keyPath)
	if string(raw) != `{"peerToken":"other"}` {
		t.Fatalf("foreign key was touched: %s", raw)
	}
}

func TestWriteRegistry_DefaultsFeaturesAndDomain(t *testing.T) {
	dir := t.TempDir()
	e := sampleEntry(dir)
	e.PeerFeatures = nil
	e.PidDomain = ""
	if _, err := WriteRegistry(dir, e, testToken); err != nil {
		t.Fatalf("WriteRegistry: %v", err)
	}
	feats, ok := ReadPeerFeatures(dir, e.PID)
	if !ok || !reflect.DeepEqual(feats, DefaultPeerFeatures) {
		t.Fatalf("features = %v ok=%v, want DefaultPeerFeatures", feats, ok)
	}
	raw, _ := os.ReadFile(filepath.Join(dir, "4242.json"))
	var got struct {
		PidDomain string `json:"pidDomain"`
	}
	if err := json.Unmarshal(raw, &got); err != nil || got.PidDomain == "" {
		t.Fatalf("pidDomain must default to a non-empty value: %q (%v)", got.PidDomain, err)
	}
}

func TestRemoveRegistry(t *testing.T) {
	dir := t.TempDir()
	a := filepath.Join(dir, "a")
	b := filepath.Join(dir, "b")
	if err := os.WriteFile(a, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := RemoveRegistry([]string{a, b}); err != nil {
		t.Fatalf("RemoveRegistry with a missing path: %v, want nil", err)
	}
	if _, err := os.Lstat(a); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("a not removed: %v", err)
	}
	if err := RemoveRegistry(nil); err != nil {
		t.Fatalf("RemoveRegistry(nil) = %v", err)
	}
	// A non-empty directory cannot be unlinked: that error is surfaced.
	sub := filepath.Join(dir, "sub")
	if err := os.MkdirAll(filepath.Join(sub, "inner"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := RemoveRegistry([]string{b, sub}); err == nil {
		t.Fatal("RemoveRegistry on a non-empty dir: err = nil")
	}
}

func TestReadPeerFeatures(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "42603.json"), []byte(realFixture), 0o644); err != nil {
		t.Fatal(err)
	}
	feats, ok := ReadPeerFeatures(dir, 42603)
	if !ok || !reflect.DeepEqual(feats, []string{"notify_idle", "reply_across_default_dirs", "artifact_yield"}) {
		t.Fatalf("real fixture: %v ok=%v", feats, ok)
	}

	if err := os.WriteFile(filepath.Join(dir, "7.json"), []byte(`{"pid":7,"sessionId":"s","procStart":"x"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if feats, ok := ReadPeerFeatures(dir, 7); ok {
		t.Fatalf("absent field: ok = true (%v)", feats)
	}

	if err := os.WriteFile(filepath.Join(dir, "8.json"), []byte(`{"pid":8,"peerFeatures":null}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if feats, ok := ReadPeerFeatures(dir, 8); ok {
		t.Fatalf("null field: ok = true (%v)", feats)
	}

	if err := os.WriteFile(filepath.Join(dir, "9.json"), []byte(`not json`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, ok := ReadPeerFeatures(dir, 9); ok {
		t.Fatal("unparsable: ok = true")
	}
	if _, ok := ReadPeerFeatures(dir, 10); ok {
		t.Fatal("missing file: ok = true")
	}

	// A symlink candidate is never followed.
	if err := os.Symlink(filepath.Join(dir, "42603.json"), filepath.Join(dir, "11.json")); err != nil {
		t.Fatal(err)
	}
	if _, ok := ReadPeerFeatures(dir, 11); ok {
		t.Fatal("symlink: ok = true")
	}
}

func TestRegistryProcStart(t *testing.T) {
	dir := t.TempDir()
	e := sampleEntry(dir)
	created, err := WriteRegistry(dir, e, testToken)
	if err != nil {
		t.Fatalf("WriteRegistry: %v", err)
	}
	for _, p := range created {
		if got := RegistryProcStart(p); got != testProcStart {
			t.Errorf("RegistryProcStart(%s) = %q, want %q", filepath.Base(p), got, testProcStart)
		}
	}
	if got := RegistryProcStart(filepath.Join(dir, "missing.json")); got != "" {
		t.Errorf("missing: %q", got)
	}
	bad := filepath.Join(dir, "bad.json")
	if err := os.WriteFile(bad, []byte("{"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := RegistryProcStart(bad); got != "" {
		t.Errorf("unparsable: %q", got)
	}
	noField := filepath.Join(dir, "nofield.json")
	if err := os.WriteFile(noField, []byte(`{"pid":1}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := RegistryProcStart(noField); got != "" {
		t.Errorf("no field: %q", got)
	}
}
