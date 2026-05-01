package codexbroker

import (
	"errors"
	"io"
	"os"
	"strings"
	"testing"
)

// fakeFS is an in-memory FS used to control EvalSymlinks/Stat behaviour for
// hash tests without depending on real-filesystem APFS / /var symlink quirks.
type fakeFS struct {
	// resolve maps an input path to the result EvalSymlinks should return.
	// Missing keys → error.
	resolve map[string]string
	// errors maps an input path to a non-nil error EvalSymlinks should return
	// instead of resolving. Takes precedence over `resolve`.
	errors map[string]error
}

func (f *fakeFS) Open(string) (io.ReadCloser, error)    { return nil, errors.New("not impl") }
func (f *fakeFS) Stat(string) (os.FileInfo, error)      { return nil, errors.New("not impl") }
func (f *fakeFS) Lstat(string) (os.FileInfo, error)     { return nil, errors.New("not impl") }
func (f *fakeFS) Glob(string) ([]string, error)         { return nil, errors.New("not impl") }
func (f *fakeFS) ReadDir(string) ([]os.DirEntry, error) { return nil, errors.New("not impl") }
func (f *fakeFS) EvalSymlinks(p string) (string, error) {
	if e, ok := f.errors[p]; ok {
		return "", e
	}
	if r, ok := f.resolve[p]; ok {
		return r, nil
	}
	return "", os.ErrNotExist
}

// TestBrokerKey_PrivateVarSymlink: macOS /var → /private/var; both raw paths
// must hash identically once EvalSymlinks resolves them.
func TestBrokerKey_PrivateVarSymlink(t *testing.T) {
	f := &fakeFS{resolve: map[string]string{
		"/var/foo":         "/private/var/foo",
		"/private/var/foo": "/private/var/foo",
	}}
	keyA, _, anomA := BrokerKey("/var/foo", f, false /* caseInsensitive */)
	keyB, _, anomB := BrokerKey("/private/var/foo", f, false)
	if anomA != nil || anomB != nil {
		t.Fatalf("unexpected anomalies: %v / %v", anomA, anomB)
	}
	if keyA != keyB {
		t.Errorf("keys differ: %q vs %q", keyA, keyB)
	}
	if len(keyA) != 16 {
		t.Errorf("key length = %d, want 16", len(keyA))
	}
}

// TestBrokerKey_CaseFold_OnInsensitiveVolume: case-insensitive → /Foo and /foo
// hash identically.
func TestBrokerKey_CaseFold_OnInsensitiveVolume(t *testing.T) {
	f := &fakeFS{resolve: map[string]string{
		"/Foo": "/Foo",
		"/foo": "/Foo",
	}}
	keyA, _, anomA := BrokerKey("/Foo", f, true /* caseInsensitive */)
	keyB, _, anomB := BrokerKey("/foo", f, true)
	if anomA != nil || anomB != nil {
		t.Fatalf("unexpected anomalies: %v / %v", anomA, anomB)
	}
	if keyA != keyB {
		t.Errorf("keys differ on case-insensitive volume: %q vs %q", keyA, keyB)
	}
}

// TestBrokerKey_CaseFold_OnSensitiveVolume: case-sensitive → /Foo and /foo
// hash differently.
func TestBrokerKey_CaseFold_OnSensitiveVolume(t *testing.T) {
	f := &fakeFS{resolve: map[string]string{
		"/Foo": "/Foo",
		"/foo": "/foo",
	}}
	keyA, _, anomA := BrokerKey("/Foo", f, false /* caseInsensitive */)
	keyB, _, anomB := BrokerKey("/foo", f, false)
	if anomA != nil || anomB != nil {
		t.Fatalf("unexpected anomalies: %v / %v", anomA, anomB)
	}
	if keyA == keyB {
		t.Errorf("keys identical on case-sensitive volume: %q", keyA)
	}
}

// TestBrokerKey_NFCAndNFDProduceDifferentKeys: codex CLI does NOT normalise
// unicode (verified against scripts/lib/state.mjs::resolveStateDir which
// hashes the raw realpath bytes). Our key MUST match codex's, so NFD and
// NFC inputs of the same "logical" path correctly produce different keys.
// P2 collision detection may report this as an anomaly, but it must not
// fold into the primary hash.
func TestBrokerKey_NFCAndNFDProduceDifferentKeys(t *testing.T) {
	nfc := "/tmp/caf\u00e9"  // precomposed e-acute
	nfd := "/tmp/cafe\u0301" // e + combining acute
	if nfc == nfd {
		t.Fatalf("test setup error: NFC and NFD bytes are identical")
	}
	f := &fakeFS{resolve: map[string]string{
		nfc: nfc,
		nfd: nfd,
	}}
	keyA, _, anomA := BrokerKey(nfc, f, false)
	keyB, _, anomB := BrokerKey(nfd, f, false)
	if anomA != nil || anomB != nil {
		t.Fatalf("unexpected anomalies: %v / %v", anomA, anomB)
	}
	if keyA == keyB {
		t.Errorf("NFC and NFD keys must differ to match codex's byte-level hash, got identical %q", keyA)
	}
}

// TestBrokerKey_UnresolvableReturnsAnomaly: EvalSymlinks fails → key derived
// from raw cwd, anomaly = AnomalyCwdUnresolvable.
func TestBrokerKey_UnresolvableReturnsAnomaly(t *testing.T) {
	want := os.ErrPermission
	f := &fakeFS{errors: map[string]error{
		"/tmp/missing": want,
	}}
	key, resolved, anom := BrokerKey("/tmp/missing", f, false)
	if anom == nil || *anom != AnomalyCwdUnresolvable {
		t.Errorf("expected AnomalyCwdUnresolvable, got %v", anom)
	}
	if resolved != "" {
		t.Errorf("resolved should be empty on failure, got %q", resolved)
	}
	if !strings.HasPrefix(key, "") || len(key) != 16 {
		t.Errorf("key length should be 16, got %d (%q)", len(key), key)
	}
	// Independent verification: hashing the raw cwd directly must match.
	keyRaw, _, _ := BrokerKey("/tmp/missing", &fakeFS{resolve: map[string]string{
		"/tmp/missing": "/tmp/missing",
	}}, false)
	if key != keyRaw {
		t.Errorf("unresolvable key %q should equal hash of raw cwd %q", key, keyRaw)
	}
}

// TestBrokerKey_DeterministicAcrossCalls: identical inputs produce identical
// outputs across repeated invocations.
func TestBrokerKey_DeterministicAcrossCalls(t *testing.T) {
	f := &fakeFS{resolve: map[string]string{"/tmp/x": "/tmp/x"}}
	a, _, _ := BrokerKey("/tmp/x", f, false)
	b, _, _ := BrokerKey("/tmp/x", f, false)
	if a != b {
		t.Errorf("non-deterministic: %q vs %q", a, b)
	}
}
