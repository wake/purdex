# Peer Address v2 (session labels) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Let a Claude Code session name itself (`pdx msg name <label>`) and make that label the primary segment of its Peer Bridge address on every host, independent of tmux and of Claude Code's registry name.

**Architecture:** A host-local `peer_labels` table (keyed by Claude Code `sessionId`, with a host-wide revision counter) is joined into the peers inventory; every live registry entry gets exactly one inventory row carrying `label`, `suffix` and `address = <host>/<label>:<suffix>`; `peers.Resolve` resolves labels first and tmux names as a fallback, refusing (`not_ready`) when the inventory may be incomplete. Occupancy of a label is decided on registry-entry liveness with an explicit "unknown" class. Helper (virtual peer) names follow the address by rewriting the helper's own registry file in place, gated by the revision.

**Tech Stack:** Go 1.25 (`net/http`, `database/sql` + `modernc.org/sqlite`, `hash/fnv`), existing `internal/peers` / `internal/module/peers` / `internal/store` / `cmd/pdx` packages, `go test -race`.

**Spec:** `docs/specs/2026-09-14-peer-address-v2-spec.md` v3 (amends `docs/specs/2026-09-13-peer-bridge-spec.md` v3.2). Read §3 before any task; the plan argues from it.

## Global Constraints

- Every task: TDD (failing test → run → implement → pass → commit). One commit per task, message prefix `feat(peers):` / `test(peers):` / `docs:`.
- Run tests from the worktree root: `cd /Users/wake/Workspace/wake/purdex/.claude/worktrees/peer-address-v2 && go test -race -count=1 ./internal/peers/... ./internal/module/peers/... ./internal/store/... ./cmd/pdx/...`. Every task ends with that whole set green, plus `go vet ./...`.
- `cmd/pdx` must not import `internal/module/peers` (CLI binary must not pull the daemon package); shared wire types live in `internal/peers` (a leaf).
- User label rule (spec §3.1): `^[a-z0-9][a-z0-9-]{1,31}$`; reserved `cc`, `tmux`. Default label: `"_" + enc(sessionId)`, `enc` = FNV-1a 64 over the UTF-8 bytes of `sessionId`, `mod 36⁶`, base36 `0-9a-z`, left-padded with `0` to 6 chars. Suffix: `san(tmux)-san(cc)` inside tmux, `san(cc)` otherwise; `san` keeps `[A-Za-z0-9_.-]`, replaces other bytes with `_`, truncates to 32, empty ⇒ `_`; complete suffix wire grammar `^[A-Za-z0-9_.-]{1,65}$`.
- Registry classes (spec §3.3): **confirmed dead** = inbox `ENOENT`, `kill(pid,0)` ⇒ `ESRCH`, or start time read and ≠ `procStart`; **unknown** = anything else that is not live (unreadable, undecodable, schema, pid mismatch, procStart unparsable, non-ENOENT stat error, classification failure, start time unreadable); `EPERM` from `kill` means alive.
- Error bodies use `ipeers.APIError` with wire key `error` (never `code`).
- Alpha: no data migration; `CREATE TABLE IF NOT EXISTS` only.
- Existing tests that encode the old address forms (`alias/cc:<name>` for outside rows, `Resolve(records, session)` two-arg, session-code tier) must be **updated to the new contract**, not deleted — keep their intent, change the expectation.
- Phase gate: P4a is Tasks 1–10 (one PR); P4b is Tasks 11–14 (second PR). Do not start Task 11 before the P4a PR is merged.

---

## File structure

**New**
- `internal/peers/label.go` — pure label/suffix functions (Task 1)
- `internal/peers/label_test.go`
- `internal/store/peer_label.go` — `PeerLabelStore` (Task 3)
- `internal/store/peer_label_test.go`
- `internal/module/peers/labels.go` — `LabelStore` interface, `labelMu`, `/api/peers/self*` handlers, claim/release logic (Task 7)
- `internal/module/peers/labels_test.go`

**Modified**
- `internal/peers/registry.go` — `ReadRegistryDiag`, `Diagnosis`, `DefaultLiveness.PidAlive` EPERM (Task 2)
- `internal/peers/record.go` — `PeerRecord` fields, `LabelInfo`, entry rows (Task 4)
- `internal/peers/envelope.go` — `DaemonVersion`, `UnknownRegistryFiles` (Task 5)
- `internal/module/peers/module.go` — `New(audit, labels)`, `localEnvelope` (Task 5)
- `cmd/pdx/main.go` — pass `meta.PeerLabels()` (Task 5)
- `internal/module/peers/module_test.go` — fixture wires the label store (Task 5)
- `internal/peers/address.go` — `Resolve` v2 (Task 6)
- `internal/module/peers/send.go` — `Resolve` call + `not_ready` mapping (Task 6)
- `cmd/pdx/msg.go` — `name`, `whoami` (Task 8)
- `cmd/pdx/peers.go` — LABEL column, entry indentation, daemon versions (Task 9)
- `internal/module/peers/e2e_test.go` — label e2e (Task 10)
- `CLAUDE.md` — "Peer addresses" section (Task 10)
- `internal/peers/wire.go` — `WireFrom.Address/AddressRev`, `ErrAddressInvalid` (Task 11)
- `internal/peers/ccuds/registry_write.go` — `RewriteRegistryName` (Task 12)
- `internal/module/peers/helpers.go` — `appliedRev`, `ApplyAddress`, `Name` (Task 13)
- `internal/module/peers/deliver.go`, `send.go`, `reply.go` — wire the address (Task 13)
- `docs/specs/2026-09-13-peer-bridge-spec.md` — pointers (Task 14)

---

# Phase P4a — Address protocol end to end

### Task 1: Label primitives (`internal/peers/label.go`)

**Files:**
- Create: `internal/peers/label.go`
- Test: `internal/peers/label_test.go`

**Interfaces:**
- Produces:
  - `func ValidateUserLabel(s string) error` — nil, or wraps `ErrLabelInvalid` / `ErrLabelReserved`
  - `var ErrLabelInvalid, ErrLabelReserved error`
  - `func IsDefaultLabel(s string) bool` — `^_[0-9a-z]{6}$`
  - `func DefaultLabel(sessionID string) string`
  - `func Sanitize(s string) string` — `san`
  - `func Suffix(tmuxSessionName, ccName string) string`
  - `func SplitSession(session string) (head, rest string)` — at first `:`
  - `const LabelSourceUser = "user"`, `LabelSourceDefault = "default"`
  - `var suffixWirePattern = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,65}$`)` and `func ValidSuffix(s string) bool`

- [ ] **Step 1: Write the failing tests**

```go
// internal/peers/label_test.go
package peers

import (
	"errors"
	"strings"
	"testing"
)

func TestValidateUserLabel(t *testing.T) {
	ok := []string{"ab", "a1", "purdex-tester", "purdex-tester-2", "0abc", strings.Repeat("a", 32)}
	for _, s := range ok {
		if err := ValidateUserLabel(s); err != nil {
			t.Errorf("%q: unexpected error %v", s, err)
		}
	}
	bad := []string{"", "a", "-ab", "Ab", "a b", "a_b", "中文", "a:b", "a/b", strings.Repeat("a", 33), "_k3x9qz"}
	for _, s := range bad {
		if err := ValidateUserLabel(s); !errors.Is(err, ErrLabelInvalid) {
			t.Errorf("%q: got %v, want ErrLabelInvalid", s, err)
		}
	}
	for _, s := range []string{"cc", "tmux"} {
		if err := ValidateUserLabel(s); !errors.Is(err, ErrLabelReserved) {
			t.Errorf("%q: got %v, want ErrLabelReserved", s, err)
		}
	}
}

// Golden vectors: FIXED outputs computed once from the definition (FNV-1a
// 64 over the UTF-8 bytes, mod 36^6, base36 0-9a-z, 6 digits, left-padded
// with '0') with an independent implementation, and frozen here. Any
// change to the derivation is a wire change and must update these on
// purpose. "pad-39" is a vector whose value is < 36^5, so its rendering
// starts with '0' — that is the padding path.
func TestDefaultLabel_Golden(t *testing.T) {
	cases := map[string]string{
		"":                                     "_j4ux45",
		"fa5d4c07-d9d9-4184-9e13-e491f2f4bf7c": "_you08b",
		"96c7a06c-4006-4a12-b163-de7fc00e1af0": "_v0h7yo",
		"pad-39":                               "_0hkg69",
	}
	for in, want := range cases {
		got := DefaultLabel(in)
		if got != want {
			t.Errorf("DefaultLabel(%q) = %q, want %q", in, got, want)
		}
		if len(got) != 7 || got[0] != '_' || !IsDefaultLabel(got) {
			t.Errorf("DefaultLabel(%q) = %q: not 7 chars / not default form", in, got)
		}
	}
	if DefaultLabel("pad-39")[1] != '0' {
		t.Error("padding vector does not start with '0' — padding path not exercised")
	}
}

func TestSanitize(t *testing.T) {
	cases := map[string]string{
		"":                       "_",
		"mt0":                    "mt0",
		"purdex-49":              "purdex-49",
		"a b":                    "a_b",
		"側欄":                     "______", // 2 runes × 3 bytes, byte-wise
		"a:b/c":                  "a_b_c",
		strings.Repeat("z", 40):  strings.Repeat("z", 32),
		"A.B_C-D":                "A.B_C-D",
	}
	for in, want := range cases {
		if got := Sanitize(in); got != want {
			t.Errorf("Sanitize(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSuffix(t *testing.T) {
	if got := Suffix("mt0", "purdex-49"); got != "mt0-purdex-49" {
		t.Errorf("got %q", got)
	}
	if got := Suffix("", "purdex-49"); got != "purdex-49" {
		t.Errorf("outside tmux: got %q", got)
	}
	long := Suffix(strings.Repeat("a", 40), strings.Repeat("b", 40))
	if len(long) != 65 || !ValidSuffix(long) {
		t.Errorf("65-char bound: len %d valid %v", len(long), ValidSuffix(long))
	}
	if ValidSuffix("") || ValidSuffix(strings.Repeat("a", 66)) || ValidSuffix("a:b") {
		t.Error("ValidSuffix accepted an invalid value")
	}
}

func TestSplitSession(t *testing.T) {
	cases := []struct{ in, head, rest string }{
		{"purdex-tester", "purdex-tester", ""},
		{"purdex-tester:purdex-3f", "purdex-tester", "purdex-3f"},
		{"tmux:mt0", "tmux", "mt0"},
		{"a:b:c", "a", "b:c"},
		{":x", "", "x"},
	}
	for _, c := range cases {
		h, r := SplitSession(c.in)
		if h != c.head || r != c.rest {
			t.Errorf("SplitSession(%q) = %q,%q want %q,%q", c.in, h, r, c.head, c.rest)
		}
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test -race -count=1 ./internal/peers/ -run 'TestValidateUserLabel|TestDefaultLabel|TestSanitize|TestSuffix|TestSplitSession'`
Expected: FAIL — `undefined: ValidateUserLabel` etc.

- [ ] **Step 3: Implement**

```go
// internal/peers/label.go
package peers

import (
	"errors"
	"fmt"
	"hash/fnv"
	"regexp"
	"strings"
)

// Label rules (Peer Address v2 spec §3.1).
const (
	LabelSourceUser    = "user"
	LabelSourceDefault = "default"

	// LabelReservedCC / LabelReservedTmux are refused as labels and
	// short-circuited by Resolve so "cc:<x>" and "tmux:<x>" never parse as
	// label+suffix.
	LabelReservedCC   = "cc"
	LabelReservedTmux = "tmux"

	sanitizeMax   = 32
	defaultLabelN = 6
	labelSpace    = 36 * 36 * 36 * 36 * 36 * 36 // 36^6
	base36Digits  = "0123456789abcdefghijklmnopqrstuvwxyz"
)

var (
	ErrLabelInvalid  = errors.New("label invalid")
	ErrLabelReserved = errors.New("label reserved")

	userLabelPattern    = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{1,31}$`)
	defaultLabelPattern = regexp.MustCompile(`^_[0-9a-z]{6}$`)
	suffixWirePattern   = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,65}$`)
)

// ValidateUserLabel applies the user label rule and the reserved words.
func ValidateUserLabel(s string) error {
	if s == LabelReservedCC || s == LabelReservedTmux {
		return fmt.Errorf("%w: %q", ErrLabelReserved, s)
	}
	if !userLabelPattern.MatchString(s) {
		return fmt.Errorf("%w: must match ^[a-z0-9][a-z0-9-]{1,31}$", ErrLabelInvalid)
	}
	return nil
}

// IsDefaultLabel reports whether s has the "_xxxxxx" default form.
func IsDefaultLabel(s string) bool { return defaultLabelPattern.MatchString(s) }

// DefaultLabel derives the unnamed label of a conversation from its Claude
// Code sessionId: "_" + base36(FNV-1a-64(sessionId) mod 36^6), 6 digits,
// zero-padded. Deterministic across resumes and daemon restarts.
func DefaultLabel(sessionID string) string {
	h := fnv.New64a()
	_, _ = h.Write([]byte(sessionID))
	n := h.Sum64() % labelSpace
	out := make([]byte, defaultLabelN)
	for i := defaultLabelN - 1; i >= 0; i-- {
		out[i] = base36Digits[n%36]
		n /= 36
	}
	return "_" + string(out)
}

// Sanitize is the suffix component sanitizer: keeps [A-Za-z0-9_.-],
// replaces every other byte with '_', truncates to 32 bytes; "" ⇒ "_".
func Sanitize(s string) string {
	if s == "" {
		return "_"
	}
	b := make([]byte, 0, len(s))
	for i := 0; i < len(s) && len(b) < sanitizeMax; i++ {
		c := s[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', c == '_', c == '.', c == '-':
			b = append(b, c)
		default:
			b = append(b, '_')
		}
	}
	return string(b)
}

// Suffix is the display suffix: san(tmux)-san(cc) inside tmux, san(cc)
// outside (tmuxSessionName == "").
func Suffix(tmuxSessionName, ccName string) string {
	if tmuxSessionName == "" {
		return Sanitize(ccName)
	}
	return Sanitize(tmuxSessionName) + "-" + Sanitize(ccName)
}

// ValidSuffix is the wire grammar a receiver checks on from.address's
// suffix part.
func ValidSuffix(s string) bool { return suffixWirePattern.MatchString(s) }

// SplitSession splits the <session> half of an address at its first ':'
// into head and rest; rest is "" when there is no ':'.
func SplitSession(session string) (head, rest string) {
	if i := strings.IndexByte(session, ':'); i >= 0 {
		return session[:i], session[i+1:]
	}
	return session, ""
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test -race -count=1 ./internal/peers/`
Expected: PASS (all existing tests still pass — nothing else changed yet).

- [ ] **Step 5: Commit**

```bash
git add internal/peers/label.go internal/peers/label_test.go
git commit -m "feat(peers): label primitives — user label rule, default label, suffix sanitizer"
```

---

### Task 2: Registry diagnosis (`ReadRegistryDiag`)

**Files:**
- Modify: `internal/peers/registry.go`
- Test: `internal/peers/registry_test.go` (add; keep existing `ReadRegistry` tests — the wrapper keeps `skipped` = dead + unknown so they stay valid)

**Interfaces:**
- Produces:
  ```go
  type UnknownFile struct { Path string; PID int; Alive bool; Reason string }
  type Diagnosis struct { Dead int; Unknown []UnknownFile }
  func (d Diagnosis) BlockingUnknown() []string   // paths of Unknown with Alive
  func ReadRegistryDiag(dir string, live Liveness) (entries []Entry, diag Diagnosis, err error)
  ```
  `ReadRegistry` stays and returns `skipped = diag.Dead + len(diag.Unknown)`.
- `DefaultLiveness().PidAlive` now returns true on `EPERM` (spec §3.3). `Liveness.Stat` errors: `ENOENT` ⇒ dead, any other error ⇒ unknown (`errors.Is(err, fs.ErrNotExist)`).

- [ ] **Step 1: Write the failing tests** (append to `registry_test.go`; reuse its `writeRegistry`/`allTrueLiveness` helpers — read the file first to match their names)

```go
func TestReadRegistryDiag_Classes(t *testing.T) {
	dir := t.TempDir()
	// live
	writeFixture(t, dir, "100.json", validRegistryJSON(100, "/tmp/100.sock"))
	// confirmed dead: pid not alive
	writeFixture(t, dir, "200.json", validRegistryJSON(200, "/tmp/200.sock"))
	// confirmed dead: socket ENOENT
	writeFixture(t, dir, "300.json", validRegistryJSON(300, "/tmp/missing.sock"))
	// unknown: undecodable, pid (from filename) alive
	writeFixture(t, dir, "400.json", "{")
	// unknown: undecodable, pid dead ⇒ not blocking
	writeFixture(t, dir, "500.json", "{")
	// unknown: pid mismatch (file says 601, name says 600), 600 alive
	writeFixture(t, dir, "600.json", validRegistryJSON(601, "/tmp/600.sock"))
	// not a candidate
	writeFixture(t, dir, ".700.json.tmp", "{")
	writeFixture(t, dir, "800.deadbeef.key", "{}")

	live := allTrueLiveness(wantProcStart)
	live.PidAlive = func(pid int) bool { return pid != 200 && pid != 500 }
	live.Stat = func(p string) error {
		if p == "/tmp/missing.sock" {
			return os.ErrNotExist
		}
		return nil
	}

	entries, diag, err := ReadRegistryDiag(dir, live)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].PID != 100 {
		t.Fatalf("entries = %+v, want only pid 100", entries)
	}
	if diag.Dead != 2 {
		t.Errorf("Dead = %d, want 2", diag.Dead)
	}
	if len(diag.Unknown) != 3 {
		t.Fatalf("Unknown = %+v, want 3", diag.Unknown)
	}
	blocking := diag.BlockingUnknown()
	want := []string{filepath.Join(dir, "400.json"), filepath.Join(dir, "600.json")}
	sort.Strings(blocking)
	if !reflect.DeepEqual(blocking, want) {
		t.Errorf("BlockingUnknown = %v, want %v", blocking, want)
	}
	// The wrapper keeps the old contract: skipped = dead + unknown.
	_, skipped, _ := ReadRegistry(dir, live)
	if skipped != 5 {
		t.Errorf("ReadRegistry skipped = %d, want 5", skipped)
	}
}

func TestReadRegistryDiag_StatNonENOENTIsUnknown(t *testing.T) {
	dir := t.TempDir()
	writeFixture(t, dir, "100.json", validRegistryJSON(100, "/tmp/100.sock"))
	live := allTrueLiveness(wantProcStart)
	live.Stat = func(string) error { return errors.New("EACCES") }
	_, diag, _ := ReadRegistryDiag(dir, live)
	if diag.Dead != 0 || len(diag.Unknown) != 1 || !diag.Unknown[0].Alive {
		t.Fatalf("diag = %+v, want one alive unknown", diag)
	}
}

func TestReadRegistryDiag_StartMismatchIsDead_StartErrorIsUnknown(t *testing.T) {
	dir := t.TempDir()
	writeFixture(t, dir, "100.json", validRegistryJSON(100, "/tmp/100.sock"))
	off := allTrueLiveness(wantProcStart.Add(time.Minute))
	_, diag, _ := ReadRegistryDiag(dir, off)
	if diag.Dead != 1 || len(diag.Unknown) != 0 {
		t.Fatalf("mismatch: diag = %+v, want Dead=1", diag)
	}
	bad := allTrueLiveness(wantProcStart)
	bad.Info = nil
	bad.StartTime = func(int) (time.Time, error) { return time.Time{}, errors.New("ps failed") }
	_, diag, _ = ReadRegistryDiag(dir, bad)
	if diag.Dead != 0 || len(diag.Unknown) != 1 {
		t.Fatalf("start error: diag = %+v, want one unknown", diag)
	}
}

func TestDefaultLiveness_PidAlive_Probe(t *testing.T) {
	// killProbe is the package-level seam DefaultLiveness uses (injected
	// here so the test is deterministic on any host and fails before the
	// change: the old code returned err == nil only).
	orig := killProbe
	t.Cleanup(func() { killProbe = orig })
	cases := map[error]bool{nil: true, syscall.EPERM: true, syscall.ESRCH: false}
	for probeErr, want := range cases {
		killProbe = func(int, syscall.Signal) error { return probeErr }
		if got := DefaultLiveness().PidAlive(4242); got != want {
			t.Errorf("probe %v ⇒ alive %v, want %v", probeErr, got, want)
		}
	}
}

func TestReadRegistryDiag_PidOutOfRange(t *testing.T) {
	dir := t.TempDir()
	writeFixture(t, dir, "99999999999999999999.json", "{}") // overflows int
	writeFixture(t, dir, "4294967296.json", "{}")           // > MaxRegistryPID
	_, diag, _ := ReadRegistryDiag(dir, allTrueLiveness(wantProcStart))
	if len(diag.Unknown) != 2 || diag.Unknown[0].Alive || diag.Unknown[1].Alive {
		t.Fatalf("diag = %+v, want two non-alive unknowns", diag)
	}
}
```

`MaxRegistryPID = 1<<31 - 1` is a new exported constant in `registry.go`; a filename pid above it (or that fails `Atoi`) is *unknown, not alive* — never probed.

(`writeFixture` is the file helper `registry_test.go` already has. `validRegistryJSON(pid, inbox)` does not exist yet: add it to `registry_test.go` as a small helper that renders a well-formed registry JSON with that pid, `sessionId: "sid-<pid>"`, `procStart` = `wantProcStart` in `ProcStartLayout`, and `messagingSocketPath: inbox`.)

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test -race -count=1 ./internal/peers/ -run 'TestReadRegistryDiag|TestDefaultLiveness_PidAliveEPERM'`
Expected: FAIL — undefined `ReadRegistryDiag`; EPERM test fails (pid 1 reported dead).

- [ ] **Step 3: Implement** — restructure the loop body of `ReadRegistry` into `ReadRegistryDiag`; every `skipped++` becomes either `diag.Dead++` or `diag.unknown(path, pid, reason)`:

```go
// UnknownFile is a registry candidate whose sessionId the daemon could not
// see (spec §3.3 "unknown"). Alive is kill(pid,0) on the FILENAME pid.
type UnknownFile struct {
	Path   string
	PID    int
	Alive  bool
	Reason string
}

// Diagnosis classifies every rejected registry candidate (spec §3.3).
type Diagnosis struct {
	Dead    int           // confirmed dead: proven not a live session
	Unknown []UnknownFile // could not be classified; Alive ones block claims and mark the inventory partial
}

// BlockingUnknown lists the paths of unknown files whose pid is alive.
func (d Diagnosis) BlockingUnknown() []string {
	var out []string
	for _, u := range d.Unknown {
		if u.Alive {
			out = append(out, u.Path)
		}
	}
	return out
}

func (d *Diagnosis) unknown(path string, pid int, alive bool, reason string) {
	d.Unknown = append(d.Unknown, UnknownFile{Path: path, PID: pid, Alive: alive, Reason: reason})
}

// ReadRegistry is ReadRegistryDiag with the diagnosis collapsed to a count.
func ReadRegistry(dir string, live Liveness) (entries []Entry, skipped int, err error) {
	entries, diag, err := ReadRegistryDiag(dir, live)
	return entries, diag.Dead + len(diag.Unknown), err
}

// ReadRegistryDiag parses every "<pid>.json" in dir, returns the live
// entries and a Diagnosis of the rest. Classes (spec §3.3):
//   - confirmed dead: inbox ENOENT; pid not alive; start time read and
//     different from procStart.
//   - unknown: everything else that is not live — unreadable, undecodable,
//     failing schema, filename/pid mismatch, procStart unparsable, a
//     non-ENOENT stat error, classification failure (D9), start time
//     unreadable. Alive is decided on the FILENAME pid.
func ReadRegistryDiag(dir string, live Liveness) (entries []Entry, diag Diagnosis, err error) {
	dirEntries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, Diagnosis{}, nil
		}
		return nil, Diagnosis{}, err
	}
	for _, de := range dirEntries {
		if de.IsDir() {
			continue
		}
		name := de.Name()
		m := registryFilenamePattern.FindStringSubmatch(name)
		if m == nil {
			continue
		}
		path := filepath.Join(dir, name)
		expectedPID, atoiErr := strconv.Atoi(m[1])
		if atoiErr != nil || expectedPID <= 0 || expectedPID > MaxRegistryPID {
			diag.unknown(path, 0, false, "pid out of range") // never probed: not alive
			continue
		}
		unknown := func(reason string) { diag.unknown(path, expectedPID, live.PidAlive(expectedPID), reason) }

		data, ok := ReadRegistryCandidate(path)
		if !ok {
			unknown("unreadable")
			continue
		}
		var wire registryFile
		if err := json.Unmarshal(data, &wire); err != nil {
			unknown("undecodable")
			continue
		}
		if wire.PID == 0 || wire.SessionID == "" || wire.ProcStart == "" || wire.Inbox == "" {
			unknown("missing required field")
			continue
		}
		if wire.PID != expectedPID {
			unknown("pid does not match filename")
			continue
		}
		procStart, err := ParseProcStart(wire.ProcStart)
		if err != nil {
			unknown("procStart unparsable")
			continue
		}
		if statErr := live.Stat(wire.Inbox); statErr != nil {
			if errors.Is(statErr, fs.ErrNotExist) {
				diag.Dead++
			} else {
				unknown("inbox stat: " + statErr.Error())
			}
			continue
		}
		if !live.PidAlive(wire.PID) {
			diag.Dead++
			continue
		}
		entryLive := live
		isProxy := false
		if live.Info != nil {
			info, infoErr := live.Info(wire.PID)
			if infoErr != nil || len(info.Argv) == 0 {
				warnUnclassifiableOnce(wire.PID, infoErr)
				unknown("unclassifiable process")
				continue
			}
			isProxy = IsProxyProcess(info)
			entryLive.StartTime = func(int) (time.Time, error) { return info.StartTime, nil }
		}
		startTime, err := entryLive.StartTime(wire.PID)
		if err != nil {
			unknown("start time unreadable")
			continue
		}
		if !startTime.Truncate(time.Second).Equal(procStart.Truncate(time.Second)) {
			diag.Dead++
			continue
		}
		entries = append(entries, Entry{ /* unchanged field copy */ })
	}
	return entries, diag, nil
}
```

Also change `DefaultLiveness().PidAlive` to go through an injectable probe:

```go
// MaxRegistryPID bounds a filename pid before it is probed (spec §3.3).
const MaxRegistryPID = 1<<31 - 1

// killProbe is kill(2); a package-level seam so the EPERM rule is testable.
var killProbe = syscall.Kill

// in DefaultLiveness:
PidAlive: func(pid int) bool {
	err := killProbe(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM) // EPERM: exists, not ours
},
```

and delete the now-unused `isLive` (its logic moved inline; keep a comment pointing at spec §3.3). Update the doc comment on `Liveness.Stat` to say "a non-ENOENT error is 'unknown', not 'dead'". Note: `helpers.go` has its own `defaultPidAlive` with the same semantics — leave it.

- [ ] **Step 4: Run the whole peers package**

Run: `go test -race -count=1 ./internal/peers/...`
Expected: PASS. If an existing `TestReadRegistry_StatError` expected a generic stat error to count as skipped: it still does (unknown counts in `skipped`).

- [ ] **Step 5: Commit**

```bash
git add internal/peers/registry.go internal/peers/registry_test.go
git commit -m "feat(peers): registry diagnosis — live / confirmed dead / unknown classes, EPERM is alive"
```

---

### Task 3: Label store (`internal/store/peer_label.go`)

**Files:**
- Create: `internal/store/peer_label.go`
- Modify: `internal/store/meta.go` (migration)
- Test: `internal/store/peer_label_test.go`

**Interfaces:**
- Produces:
  ```go
  type PeerLabel struct { SessionID string; Label string /* "" after release */; Rev int64; SetAt time.Time }
  func (m *MetaStore) PeerLabels() *PeerLabelStore
  func (s *PeerLabelStore) Snapshot() ([]PeerLabel, error)
  func (s *PeerLabelStore) Claim(sessionID, label string, now time.Time) (PeerLabel, error)
  func (s *PeerLabelStore) Release(sessionID string, now time.Time) (PeerLabel, bool, error) // false ⇒ no row existed
  ```
  `Claim` runs one transaction: `DELETE FROM peer_labels WHERE label = ? AND session_id <> ?` (the caller has already proven that holder not live), bump `peer_label_seq`, upsert the row. `Release` sets `label = NULL` and bumps `rev` in one transaction; a session with no row returns `(PeerLabel{}, false, nil)`.

- [ ] **Step 1: Write the failing tests**

```go
// internal/store/peer_label_test.go
package store

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPeerLabels_ClaimReleaseRev(t *testing.T) {
	ms, err := OpenMeta(":memory:")
	require.NoError(t, err)
	defer ms.Close()
	ls := ms.PeerLabels()
	now := time.UnixMilli(1000)

	a, err := ls.Claim("sid-a", "tester", now)
	require.NoError(t, err)
	assert.Equal(t, "tester", a.Label)
	assert.Equal(t, int64(1), a.Rev)

	// Re-claiming a different label for the same session replaces it and bumps rev.
	a2, err := ls.Claim("sid-a", "tester-2", now)
	require.NoError(t, err)
	assert.Equal(t, int64(2), a2.Rev)
	rows, _ := ls.Snapshot()
	require.Len(t, rows, 1)
	assert.Equal(t, "tester-2", rows[0].Label)

	// Another session takes "tester-2": the old row (caller proved it not live) is evicted.
	b, err := ls.Claim("sid-b", "tester-2", now)
	require.NoError(t, err)
	assert.Equal(t, int64(3), b.Rev)
	rows, _ = ls.Snapshot()
	require.Len(t, rows, 1)
	assert.Equal(t, "sid-b", rows[0].SessionID)

	// Release keeps the row with a NULL label and a higher rev.
	rel, ok, err := ls.Release("sid-b", now)
	require.NoError(t, err)
	assert.True(t, ok)
	assert.Equal(t, "", rel.Label)
	assert.Equal(t, int64(4), rel.Rev)
	rows, _ = ls.Snapshot()
	require.Len(t, rows, 1)
	assert.Equal(t, "", rows[0].Label)

	// Releasing an unknown session is a no-op.
	_, ok, err = ls.Release("nobody", now)
	require.NoError(t, err)
	assert.False(t, ok)

	// Two released rows do not collide on UNIQUE(label).
	_, err = ls.Claim("sid-c", "x1", now)
	require.NoError(t, err)
	_, _, err = ls.Release("sid-c", now)
	require.NoError(t, err)
	rows, _ = ls.Snapshot()
	assert.Len(t, rows, 2)
}

func TestPeerLabels_RevSurvivesReopen(t *testing.T) {
	path := t.TempDir() + "/meta.db"
	ms, err := OpenMeta(path)
	require.NoError(t, err)
	_, err = ms.PeerLabels().Claim("sid", "a1", time.Now())
	require.NoError(t, err)
	require.NoError(t, ms.Close())

	ms2, err := OpenMeta(path)
	require.NoError(t, err)
	defer ms2.Close()
	row, err := ms2.PeerLabels().Claim("sid", "a2", time.Now())
	require.NoError(t, err)
	assert.Equal(t, int64(2), row.Rev)
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test -race -count=1 ./internal/store/ -run TestPeerLabels`
Expected: FAIL — `ms.PeerLabels undefined`.

- [ ] **Step 3: Implement**

In `migrateMetaDB` (after the `peer_messages` indexes):

```go
	// peer_labels: Peer Address v2 (spec §3.3). One user label per
	// conversation (sessionId), one conversation per label; label is NULL
	// after a release so the row keeps carrying rev. peer_label_seq is
	// the host-wide strictly increasing revision.
	if _, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS peer_labels (
			session_id TEXT PRIMARY KEY,
			label      TEXT UNIQUE,
			rev        INTEGER NOT NULL,
			set_at     INTEGER NOT NULL
		)
	`); err != nil {
		return err
	}
	if _, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS peer_label_seq (
			id  INTEGER PRIMARY KEY CHECK (id = 1),
			rev INTEGER NOT NULL
		)
	`); err != nil {
		return err
	}
	if _, err := db.Exec(`INSERT OR IGNORE INTO peer_label_seq (id, rev) VALUES (1, 0)`); err != nil {
		return err
	}
```

```go
// internal/store/peer_label.go
package store

import (
	"database/sql"
	"time"
)

// PeerLabel is one peer_labels row. Label is "" when the row was released.
type PeerLabel struct {
	SessionID string
	Label     string
	Rev       int64
	SetAt     time.Time
}

// PeerLabelStore persists Peer Address v2 labels on the shared meta DB.
type PeerLabelStore struct{ db *sql.DB }

// PeerLabels returns the label store backed by this MetaStore's DB.
func (m *MetaStore) PeerLabels() *PeerLabelStore { return &PeerLabelStore{db: m.db} }

// Snapshot returns every row, released ones included (Label "").
func (s *PeerLabelStore) Snapshot() ([]PeerLabel, error) {
	rows, err := s.db.Query(`SELECT session_id, label, rev, set_at FROM peer_labels ORDER BY session_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]PeerLabel, 0)
	for rows.Next() {
		var (
			p     PeerLabel
			label sql.NullString
			setAt int64
		)
		if err := rows.Scan(&p.SessionID, &label, &p.Rev, &setAt); err != nil {
			return nil, err
		}
		p.Label = label.String
		p.SetAt = time.UnixMilli(setAt)
		out = append(out, p)
	}
	return out, rows.Err()
}

// nextRev bumps peer_label_seq inside tx and returns the new value.
func nextRev(tx *sql.Tx) (int64, error) {
	var rev int64
	err := tx.QueryRow(`UPDATE peer_label_seq SET rev = rev + 1 WHERE id = 1 RETURNING rev`).Scan(&rev)
	return rev, err
}

// Claim gives label to sessionID: evicts any other row holding label (the
// caller has proven that holder is not live), bumps the revision and
// upserts, all in one transaction.
func (s *PeerLabelStore) Claim(sessionID, label string, now time.Time) (PeerLabel, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return PeerLabel{}, err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`DELETE FROM peer_labels WHERE label = ? AND session_id <> ?`, label, sessionID); err != nil {
		return PeerLabel{}, err
	}
	rev, err := nextRev(tx)
	if err != nil {
		return PeerLabel{}, err
	}
	if _, err := tx.Exec(`
		INSERT INTO peer_labels (session_id, label, rev, set_at) VALUES (?, ?, ?, ?)
		ON CONFLICT(session_id) DO UPDATE SET label = excluded.label, rev = excluded.rev, set_at = excluded.set_at
	`, sessionID, label, rev, now.UnixMilli()); err != nil {
		return PeerLabel{}, err
	}
	if err := tx.Commit(); err != nil {
		return PeerLabel{}, err
	}
	return PeerLabel{SessionID: sessionID, Label: label, Rev: rev, SetAt: now}, nil
}

// Release clears sessionID's label (row kept, rev bumped). ok is false
// when no row existed; nothing is written then.
func (s *PeerLabelStore) Release(sessionID string, now time.Time) (PeerLabel, bool, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return PeerLabel{}, false, err
	}
	defer tx.Rollback()
	var exists int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM peer_labels WHERE session_id = ?`, sessionID).Scan(&exists); err != nil {
		return PeerLabel{}, false, err
	}
	if exists == 0 {
		return PeerLabel{}, false, nil
	}
	rev, err := nextRev(tx)
	if err != nil {
		return PeerLabel{}, false, err
	}
	if _, err := tx.Exec(`UPDATE peer_labels SET label = NULL, rev = ?, set_at = ? WHERE session_id = ?`, rev, now.UnixMilli(), sessionID); err != nil {
		return PeerLabel{}, false, err
	}
	if err := tx.Commit(); err != nil {
		return PeerLabel{}, false, err
	}
	return PeerLabel{SessionID: sessionID, Label: "", Rev: rev, SetAt: now}, true, nil
}
```

- [ ] **Step 4: Run tests**

Run: `go test -race -count=1 ./internal/store/`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/store/meta.go internal/store/peer_label.go internal/store/peer_label_test.go
git commit -m "feat(store): peer_labels + peer_label_seq — claim/release with host-wide revision"
```

---

### Task 4: Peer record — labels, suffix, entry rows

**Files:**
- Modify: `internal/peers/record.go`
- Test: `internal/peers/record_test.go` (update outside-row expectations; add new tests)

**Interfaces:**
- Produces:
  ```go
  type LabelInfo struct { Label string /* "" ⇒ default */; Rev int64 }
  // PeerRecord gains:
  RowKind     string `json:"row_kind"`      // "session" | "entry"
  Label       string `json:"label"`         // "" when no cc agent
  LabelSource string `json:"label_source"`  // user | default | ""
  LabelRev    int64  `json:"label_rev"`     // 0 when no row
  Suffix      string `json:"suffix"`        // "" when no cc agent
  // BuildInput gains:
  Labels map[string]LabelInfo // by sessionId; absent ⇒ default label, rev 0
  ```
- Address rules (spec §3.4): cc rows ⇒ `<alias>/<label>:<suffix>`; session rows without a cc agent ⇒ `<alias>/tmux:<session_name>`; proxy rows ⇒ `<alias>/cc:<registry name>` (unchanged, unresolvable by design).
- **Suffix source (plan delta to spec §3.1/§3.4, recorded in the PR):** the suffix is always derived from the **entry's own registry `tmux` field** — `Suffix(e.TmuxSessionName(), e.Name)` — for session rows and entry rows alike, so a self record built from an entry (Task 7) renders the same address the listing shows. A session row whose agent is only the owner fallback (no entry) uses `Suffix(s.Name, "")`.
- Entry rows: **every** live non-proxy entry not consumed by a session row gets a row of `RowKind: "entry"` — whether or not its tmux session is listed (this replaces rule 5's `sessionNames` exclusion). Sorted by peer name then pid, after the session rows.
- Also produces `func EntryRecord(alias, hostID string, e Entry, proxy bool, info LabelInfo) PeerRecord` — the one function that builds an entry row; `Build` calls it and Task 7 calls it for self/holder responses.

- [ ] **Step 1: Write the failing tests** (add to `record_test.go`)

```go
func TestBuild_LabelsAndAddresses(t *testing.T) {
	in := BuildInput{
		HostID: "h:1", Alias: "mini-lab",
		Sessions: []SessionSummary{{Code: "c1", Name: "mt0", Cwd: "/w"}, {Code: "c2", Name: "shell"}},
		Owners: map[string]Owner{
			"c1": {AgentType: "cc", SessionID: "sid-1", TmuxPaneID: "%1"},
		},
		Entries: []Entry{
			{PID: 10, SessionID: "sid-1", Name: "purdex-49", Tmux: "mt0:@1.%1", Inbox: "/s/10"},
			{PID: 20, SessionID: "sid-2", Name: "purdex-3f", Tmux: "", Inbox: "/s/20"}, // Desktop
		},
		Labels: map[string]LabelInfo{"sid-1": {Label: "purdex-dev", Rev: 7}},
	}
	recs := Build(in)
	byAddr := map[string]PeerRecord{}
	for _, r := range recs {
		byAddr[r.Address] = r
	}
	dev, ok := byAddr["mini-lab/purdex-dev:mt0-purdex-49"]
	if !ok {
		t.Fatalf("no dev row; addresses: %v", keys(byAddr))
	}
	if dev.RowKind != "session" || dev.Label != "purdex-dev" || dev.LabelSource != LabelSourceUser || dev.LabelRev != 7 || dev.Suffix != "mt0-purdex-49" {
		t.Errorf("dev row = %+v", dev)
	}
	want := "mini-lab/" + DefaultLabel("sid-2") + ":purdex-3f"
	desk, ok := byAddr[want]
	if !ok {
		t.Fatalf("no desktop row %q; addresses: %v", want, keys(byAddr))
	}
	if desk.RowKind != "entry" || desk.LabelSource != LabelSourceDefault || desk.LabelRev != 0 || !desk.Deliverable {
		t.Errorf("desktop row = %+v", desk)
	}
	shell, ok := byAddr["mini-lab/tmux:shell"]
	if !ok || shell.Label != "" || shell.Suffix != "" || shell.LabelSource != "" {
		t.Errorf("shell row = %+v (ok=%v)", shell, ok)
	}
}

func TestBuild_EntryRow_NonOwnerEntryInsideListedSession(t *testing.T) {
	// Two live processes of DIFFERENT conversations in one tmux session:
	// the owner is consumed by the session row; the other gets an entry row.
	in := BuildInput{
		Alias:    "a",
		Sessions: []SessionSummary{{Code: "c1", Name: "mt0"}},
		Owners:   map[string]Owner{"c1": {AgentType: "cc", SessionID: "sid-1", TmuxPaneID: "%1"}},
		Entries: []Entry{
			{PID: 10, SessionID: "sid-1", Name: "n1", Tmux: "mt0:@1.%1", Inbox: "/s/10"},
			{PID: 11, SessionID: "sid-9", Name: "n9", Tmux: "mt0:@1.%2", Inbox: "/s/11"},
		},
	}
	recs := Build(in)
	if len(recs) != 2 {
		t.Fatalf("got %d rows, want 2: %+v", len(recs), recs)
	}
	if recs[1].RowKind != "entry" || recs[1].Agent.PID != 11 || recs[1].SessionName != "" || !recs[1].Deliverable {
		t.Errorf("entry row = %+v", recs[1])
	}
	// The suffix comes from the entry's own tmux field, so an entry row
	// inside tmux reads like its session row would.
	if recs[1].Suffix != "mt0-n9" || recs[1].Address != "a/"+DefaultLabel("sid-9")+":mt0-n9" {
		t.Errorf("entry row suffix/address = %q %q", recs[1].Suffix, recs[1].Address)
	}
}

func TestEntryRecord_MatchesBuild(t *testing.T) {
	e := Entry{PID: 11, SessionID: "sid-9", Name: "n9", Tmux: "mt0:@1.%2", Inbox: "/s/11", Cwd: "/w"}
	info := LabelInfo{Label: "purdex-tester", Rev: 3}
	one := EntryRecord("a", "h:1", e, false, info)
	all := Build(BuildInput{HostID: "h:1", Alias: "a", Entries: []Entry{e}, Labels: map[string]LabelInfo{"sid-9": info}})
	if len(all) != 1 || !reflect.DeepEqual(all[0], one) {
		t.Errorf("EntryRecord ≠ Build row:\n%+v\n%+v", one, all)
	}
	p := EntryRecord("a", "h:1", e, true, info)
	if p.Agent.Type != "proxy" || p.Deliverable || p.Reason != "proxy" || p.Address != "a/cc:n9" || p.Label != "" {
		t.Errorf("proxy entry record = %+v", p)
	}
}

func TestBuild_SameConversationTwoProcesses_TwoRowsSameLabel(t *testing.T) {
	// Same sessionId twice, no pane tiebreak: session row is ambiguous
	// (fallback agent) AND both entries get entry rows — three rows with
	// the same label; Resolve (Task 6) reports them ambiguous.
	in := BuildInput{
		Alias:    "a",
		Sessions: []SessionSummary{{Code: "c1", Name: "mt0"}},
		Owners:   map[string]Owner{"c1": {AgentType: "cc", SessionID: "sid-1", TmuxPaneID: "%9"}},
		Entries: []Entry{
			{PID: 10, SessionID: "sid-1", Name: "n1", Tmux: "mt0:@1.%1", Inbox: "/s/10"},
			{PID: 11, SessionID: "sid-1", Name: "n1", Tmux: "mt0:@1.%2", Inbox: "/s/11"},
		},
	}
	recs := Build(in)
	if len(recs) != 3 {
		t.Fatalf("got %d rows, want 3", len(recs))
	}
	for _, r := range recs {
		if r.Label != DefaultLabel("sid-1") {
			t.Errorf("row %s label = %q", r.Address, r.Label)
		}
	}
	if recs[0].Reason != "ambiguous" || recs[0].Deliverable {
		t.Errorf("session row = %+v", recs[0])
	}
}

func keys(m map[string]PeerRecord) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
```

Also update existing tests: every expectation of `alias/cc:<name>` for a non-proxy outside row becomes `alias/<DefaultLabel(sid)>:<Sanitize(name)>`; `TestBuild_EntryConsumedByRule4_NeverAlsoAppearsAsOutsideRow` still holds; `TestBuild_CC_PaneMatchWrongSessionID_InboxDeadAndOutsideRow` — the "outside row" is now an entry row with `RowKind: "entry"`; `TestBuild_JSON_EveryRecordHasCoreKeys` gains `row_kind`, `label`, `label_source`, `label_rev`, `suffix`; `TestBuild_GoldenMlabReproduction` addresses change accordingly. Proxy rows keep `alias/cc:<name>`.

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test -race -count=1 ./internal/peers/ -run 'TestBuild_'`
Expected: FAIL — unknown fields / wrong addresses.

- [ ] **Step 3: Implement**

```go
// LabelInfo is what the label store knows about a conversation: the user
// label ("" ⇒ the default label applies) and the row revision.
type LabelInfo struct {
	Label string
	Rev   int64
}

// in PeerRecord, after Address:
	RowKind     string `json:"row_kind"`     // session | entry
	Label       string `json:"label"`        // "" when the row has no cc agent
	LabelSource string `json:"label_source"` // user | default | ""
	LabelRev    int64  `json:"label_rev"`
	Suffix      string `json:"suffix"`

// in BuildInput:
	Labels map[string]LabelInfo // by sessionId; absent ⇒ default label, rev 0

// applyLabel fills Label/LabelSource/LabelRev/Suffix/Address for a row
// whose agent is a cc conversation sid (session rows and entry rows).
func applyLabel(rec *PeerRecord, alias string, info LabelInfo, sid, tmuxName, ccName string) {
	if info.Label != "" {
		rec.Label, rec.LabelSource = info.Label, LabelSourceUser
	} else {
		rec.Label, rec.LabelSource = DefaultLabel(sid), LabelSourceDefault
	}
	rec.LabelRev = info.Rev
	rec.Suffix = Suffix(tmuxName, ccName)
	rec.Address = alias + "/" + rec.Label + ":" + rec.Suffix
}
```

In `buildSessionRecord`: initial `Address: in.Alias + "/tmux:" + s.Name`, `RowKind: "session"`. In every branch that sets a cc agent call `applyLabel`: for the deliverable branches `applyLabel(&rec, in, owner.SessionID, entry.TmuxSessionName(), entry.Name)` with the chosen entry; for the `ownerFallbackAgent` branches (inbox_dead/ambiguous, no entry) `applyLabel(&rec, in, owner.SessionID, s.Name, "")` (suffix `san(mt0)-_`). `not_cc` / `no_agent` / unresolved rows keep the `tmux:` address and empty label fields.

Replace `buildOutsideRecords` with `buildEntryRecords`: drop the `sessionNames` skip; for each non-consumed entry append `EntryRecord(in.Alias, in.HostID, e, e.IsProxy || in.ProxyPIDs[e.PID], in.Labels[e.SessionID])`. Keep the sort. Remove the now-unused `sessionNames` map from `Build`.

```go
// EntryRecord is the row of one live registry entry that no session row
// consumed (Peer Address v2 spec §3.4). Task 7 also uses it to answer
// whoami/claim/release straight from the validated entry and label row,
// so the address it renders must be identical to the listing's.
func EntryRecord(alias, hostID string, e Entry, proxy bool, info LabelInfo) PeerRecord {
	agent := agentInfoFromEntry(e)
	rec := PeerRecord{
		Host: alias, HostID: hostID, RowKind: "entry",
		Cwd: e.Cwd, Agent: agent, Deliverable: true,
	}
	if proxy {
		agent.Type, agent.Status = "proxy", "proxy"
		rec.Address = alias + "/cc:" + e.Name
		rec.Deliverable, rec.Reason = false, "proxy"
		return rec
	}
	applyLabel(&rec, alias, info, e.SessionID, e.TmuxSessionName(), e.Name)
	return rec
}
```

(`applyLabel` therefore takes `(rec *PeerRecord, alias string, info LabelInfo, sid, tmuxName, ccName string)`; `buildSessionRecord` passes `in.Alias, in.Labels[owner.SessionID]`.)

- [ ] **Step 4: Run the peers package, then migrate the module tests this change breaks**

Run: `go test -race -count=1 ./internal/peers/...`
Expected: PASS after the expectation updates listed in Step 1. Also in `record_test.go`: line ~48 expects the shell session's address `alias/<name>` — now `alias/tmux:<name>`; line ~553 (`TestBuild_TwoSessionsSameOwnerSessionID_EntryConsumedOnce`) resolves `"cc:purdex-1"` — that form is retired in Task 6; change the assertion to find the winning session row directly (`RowKind == "session" && Deliverable && Agent.PID == 100`, no `Resolve` call), and note the ambiguous second session row now carries the same default label as the winner.

Then run `go test -race -count=1 ./internal/module/peers/...` and migrate the tests whose premise was "a live entry has no row while its owner lookup is unresolved" — with entry rows the entry IS found, which is the intended v2 behaviour:
- `send_test.go` ~764, case `"origin in a partial inventory is not_ready"`: the origin now resolves through its entry row; change the expectation to the send proceeding to the remote fetch (mirror the table's happy-path case). The not_ready guard returns as a new case in Task 5 (unknown registry file).
- `deliver_test.go` `TestDeliver_PartialInventoryIsNotReady`: subtests "owner lookup fails ⇒ not_ready" and the one after it now deliver (`200 delivered`); keep the subtest where the tuple is genuinely missing from a complete inventory (`target_gone`).
- `reply_test.go` `TestReply_PartialInventoryIsNotReady`: the replier resolves through its entry row ⇒ the reply is forwarded.
- `e2e_test.go` `TestE2E_PartialOriginInventoryKeepsHelper`: same premise; re-express it with an unknown registry file (an undecodable `<alive pid>.json` in `regDir`) so the inventory is partial for the v2 reason and the helper is kept.
- Any `"/cc:"` address expectation in `internal/module/peers/*_test.go` and `cmd/pdx/*_test.go` for a non-proxy row becomes `alias/<DefaultLabel(sid)>:<Suffix(tmux, name)>`.

Run: `go test -race -count=1 ./internal/peers/... ./internal/module/peers/... ./cmd/pdx/...`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/peers/record.go internal/peers/record_test.go
git commit -m "feat(peers): peer records carry label/suffix/address v2; every live entry gets a row"
```

---

### Task 5: Inventory — label snapshot, diagnosis ⇒ partial, daemon version, module wiring

**Files:**
- Modify: `internal/peers/envelope.go`, `internal/module/peers/module.go`, `cmd/pdx/main.go`, `internal/module/peers/module_test.go` (fixture)
- Create: `internal/module/peers/labels.go` (just the `LabelStore` interface for now; handlers come in Task 7)
- Test: `internal/module/peers/module_test.go` (add)

**Interfaces:**
- Produces:
  ```go
  // internal/peers/envelope.go — both Envelope and HostResult gain:
  DaemonVersion        string   `json:"daemon_version"`
  UnknownRegistryFiles []string `json:"unknown_registry_files"` // never null
  // internal/module/peers/labels.go
  type LabelStore interface {
      Snapshot() ([]store.PeerLabel, error)
      Claim(sessionID, label string, now time.Time) (store.PeerLabel, error)
      Release(sessionID string, now time.Time) (store.PeerLabel, bool, error)
  }
  func New(audit AuditStore, labels LabelStore) *Module   // signature change
  ```
- `localEnvelope`: uses `ReadRegistryDiag`; `partial = len(unresolved) > 0 || len(diag.BlockingUnknown()) > 0 || labelsErr != nil`; `UnknownRegistryFiles = diag.BlockingUnknown()` (empty slice, not nil); `DaemonVersion = buildinfo.Version`; labels map from `m.labels.Snapshot()` (nil `m.labels` ⇒ treated as an empty snapshot, not an error). `fetchHostResult` copies `DaemonVersion` and `UnknownRegistryFiles` through; `allEnvelope`'s local row too.

- [ ] **Step 1: Write the failing tests** (in `module_test.go`; use the existing `newTestModuleWith` fixture, `writeRegistryFixture`, `allLiveLiveness`)

```go
func TestLocalEnvelope_LabelsJoinedAndVersion(t *testing.T) {
	f := newTestModuleWith(t, fixtureOpts{ /* one tmux session "mt0" owned by cc sid-1 with a live entry pid 10; see existing tests for the shape */ })
	_, err := f.labels.Claim("sid-1", "purdex-dev", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	env := f.m.localEnvelope(context.Background(), "h:1", "a")
	if env.DaemonVersion != buildinfo.Version {
		t.Errorf("daemon_version = %q", env.DaemonVersion)
	}
	if env.UnknownRegistryFiles == nil || len(env.UnknownRegistryFiles) != 0 {
		t.Errorf("unknown_registry_files = %#v, want empty non-nil", env.UnknownRegistryFiles)
	}
	rec := f.m.rowByInbox(env, "/…/10.sock") // or iterate env.Peers
	if rec.Label != "purdex-dev" || rec.LabelSource != "user" || rec.LabelRev != 1 {
		t.Errorf("row = %+v", rec)
	}
}

func TestLocalEnvelope_UnknownRegistryFileMarksPartial(t *testing.T) {
	f := newTestModuleWith(t, fixtureOpts{ /* as above */ })
	// An undecodable file named after a pid the fake liveness says is alive.
	writeRegistryFixture(t, f.registryDir, "4242.json", "{")
	env := f.m.localEnvelope(context.Background(), "h:1", "a")
	if !env.Partial {
		t.Fatal("partial = false, want true")
	}
	if len(env.UnknownRegistryFiles) != 1 || !strings.HasSuffix(env.UnknownRegistryFiles[0], "4242.json") {
		t.Errorf("unknown_registry_files = %v", env.UnknownRegistryFiles)
	}
}

func TestLocalEnvelope_LabelStoreFailureIsPartial(t *testing.T) {
	f := newTestModuleWith(t, fixtureOpts{ /* as above */ })
	f.m.labels = failingLabels{} // Snapshot returns an error
	env := f.m.localEnvelope(context.Background(), "h:1", "a")
	if !env.Partial {
		t.Fatal("partial = false, want true on label store failure")
	}
	for _, r := range env.Peers {
		if r.Agent != nil && r.Agent.Type == "cc" && r.LabelSource != "default" {
			t.Errorf("row %s label_source = %q, want default", r.Address, r.LabelSource)
		}
	}
	if !f.logs.contains("label store") {
		t.Error("expected one log line about the label store")
	}
}

type failingLabels struct{}

func (failingLabels) Snapshot() ([]store.PeerLabel, error) { return nil, errors.New("boom") }
func (failingLabels) Claim(string, string, time.Time) (store.PeerLabel, error) {
	return store.PeerLabel{}, errors.New("boom")
}
func (failingLabels) Release(string, time.Time) (store.PeerLabel, bool, error) {
	return store.PeerLabel{}, false, errors.New("boom")
}

// writeFailingLabels reads fine but cannot write (Task 7 uses it for the
// claim/release write-failure rows of the matrix).
type writeFailingLabels struct{ real *store.PeerLabelStore }

func (w writeFailingLabels) Snapshot() ([]store.PeerLabel, error) { return w.real.Snapshot() }
func (writeFailingLabels) Claim(string, string, time.Time) (store.PeerLabel, error) {
	return store.PeerLabel{}, errors.New("disk full")
}
func (writeFailingLabels) Release(string, time.Time) (store.PeerLabel, bool, error) {
	return store.PeerLabel{}, false, errors.New("disk full")
}

// Registry unknowns must never become a verdict on a tuple: a target
// whose own registry file is unreadable, while its conversation still has
// a fallback row (owner resolution names it), is not target_gone.
func TestDeliver_UnknownRegistryFileIsNotReady(t *testing.T) {
	e := newDeliverEnv(t, inTmux) // the env deliver_test.go already uses: one tmux session owned by cc targetSID
	writeRegistryFixture(t, e.regDir, strconv.Itoa(targetPID)+".json", targetRegistryJSONInTmux(e.targetSock, "foo:@1.%1"))
	// Now corrupt the target's OWN file: its pid is alive (fake liveness) ⇒ an alive unknown.
	writeRegistryFixture(t, e.regDir, strconv.Itoa(targetPID)+".json", "{")
	status, body := e.deliver(e.validRequest()) // the env's helper for a well-formed request to targetPID
	e.assertAPIError(status, body, 503, ipeers.ErrNotReady)
	// The audit row says not_ready / "inventory partial", exactly like the owner-unresolved case.
}

func TestReply_UnknownRegistryFileIsNotReady(t *testing.T) {
	// Same shape on the reply path (model on TestReply_PartialInventoryIsNotReady):
	// the replier's own file is garbage ⇒ audited not_ready, helper kept.
}
```

(Adapt the fixture setup to the file's existing conventions — read `module_test.go`, `deliver_test.go` (`newDeliverEnv`, `envOpts`, `inTmux`) and `reply_test.go` (`newReplyEnv`) first; the fake liveness must report pid 4242 alive for the second test, so pick a liveness fake whose `PidAlive` is all-true, which `allLiveLiveness` already is.)

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test -race -count=1 ./internal/module/peers/ -run TestLocalEnvelope_`
Expected: FAIL to compile (`f.labels`, `env.DaemonVersion`, `New` arity).

- [ ] **Step 3: Implement**

`internal/module/peers/labels.go`:

```go
package peers

import (
	"time"

	"github.com/wake/purdex/internal/store"
)

// LabelStore is the peer_labels table (Peer Address v2 spec §3.3):
// *store.PeerLabelStore in production, a fake in tests. nil means "no
// store": every conversation has its default label and claims fail with
// store_unavailable.
type LabelStore interface {
	Snapshot() ([]store.PeerLabel, error)
	Claim(sessionID, label string, now time.Time) (store.PeerLabel, error)
	Release(sessionID string, now time.Time) (store.PeerLabel, bool, error)
}
```

`module.go`:
- `Module` gains `labels LabelStore` and `labelMu sync.Mutex`.
- `New(audit AuditStore, labels LabelStore)` stores it.
- `localEnvelope`: replace `ReadRegistry` with `ReadRegistryDiag`; after owner resolution:

```go
	labels, labelsErr := m.labelSnapshot()
	if labelsErr != nil {
		m.logf("peers: inventory: label store unavailable, reporting default labels: %v", labelsErr)
	}
	unknown := diag.BlockingUnknown()
	if unknown == nil {
		unknown = []string{}
	}
	partial := len(unresolved) > 0 || len(unknown) > 0 || labelsErr != nil
	…
	peerRecords := ipeers.Build(ipeers.BuildInput{ …, Labels: labels })
	return ipeers.Envelope{HostID: hostID, OK: true, Partial: partial, Peers: peerRecords,
		DaemonVersion: buildinfo.Version, UnknownRegistryFiles: unknown}
```

```go
// labelSnapshot reads the label table into Build's map. A nil store is an
// empty map; a read error is returned so the caller can mark partial.
func (m *Module) labelSnapshot() (map[string]ipeers.LabelInfo, error) {
	out := map[string]ipeers.LabelInfo{}
	if m.labels == nil {
		return out, nil
	}
	rows, err := m.labels.Snapshot()
	if err != nil {
		return out, err
	}
	for _, r := range rows {
		out[r.SessionID] = ipeers.LabelInfo{Label: r.Label, Rev: r.Rev}
	}
	return out, nil
}
```

- `writeError` envelopes and every `HostResult` literal get `UnknownRegistryFiles: []string{}`; `allEnvelope` local row and `fetchHostResult` success row copy `DaemonVersion` and `UnknownRegistryFiles` from the env (bound the remote list: keep at most 32 paths, each through `boundRemoteText`).
- `cmd/pdx/main.go:263`: `var labels peersmod.LabelStore; if meta != nil { labels = meta.PeerLabels() }; c.AddModule(peersmod.New(audit, labels))`.
- Fixture: `newTestModuleWith` builds the `Module` with a **struct literal** (`module_test.go:187`), not `New` — add `labels: f.labels` to that literal, where `moduleFixture` gains `labels *store.PeerLabelStore` = `meta.PeerLabels()` from the same in-memory meta the audit fake uses. The two `New(nil)` calls at `module_test.go:1501` and `:1518` become `New(nil, nil)`. Add `logSink.contains(substr string) bool` if it does not exist.
- `deliver.go` step 7 and `reply.go` step 3: an inventory with `len(env.UnknownRegistryFiles) > 0` is treated as partial **even when a candidate row exists** — `findTarget` returning a detail ⇒ `503 not_ready` (`detailInventoryPartial`), never `target_gone` (which would reap the sender's helper); `findReplier` likewise ⇒ audited `not_ready`, helper kept. A label-store failure alone (partial without unknown files) does not change these paths — it hides no entries. Concretely, in `deliver.go`: `if !candidate && env.Partial || len(env.UnknownRegistryFiles) > 0 { refuse(503, not_ready, detailInventoryPartial) }`; mirror in `reply.go`. Update both step comments.

- [ ] **Step 4: Run tests**

Run: `go test -race -count=1 ./internal/module/peers/... ./cmd/pdx/... && go vet ./...`
Expected: PASS (the `/cc:` address migrations were done in Task 4).

- [ ] **Step 5: Commit**

```bash
git add internal/peers/envelope.go internal/module/peers/module.go internal/module/peers/labels.go internal/module/peers/module_test.go cmd/pdx/main.go
git commit -m "feat(peers): inventory joins labels, registry unknowns mark partial, envelope carries daemon_version"
```

---

### Task 6: `Resolve` v2 and `/send` mapping

**Files:**
- Modify: `internal/peers/address.go`, `internal/module/peers/send.go`
- Test: `internal/peers/address_test.go` (rewrite the tier tests), `internal/module/peers/send_test.go` (add the `not_ready` case — find the existing send tests' fixture)

**Interfaces:**
- Produces:
  ```go
  var ErrResolveNotReady = errors.New("inventory partial: the label may be missing")
  var ErrLegacyCC = errors.New("cc: addresses were removed; run pdx peers --all to see the new addresses")  // wraps ErrNotFound via fmt.Errorf("%w: %w") in Resolve
  func Resolve(records []PeerRecord, session string, partial bool) (PeerRecord, error)
  ```
  (`ErrNotReady` is already a **string** constant in `wire.go`; the sentinel must be named `ErrResolveNotReady`.)

- [ ] **Step 1: Write the failing tests** — replace the tier tests in `address_test.go`:

```go
func labelRecord(label, source, sessionName string, pid int) PeerRecord {
	return PeerRecord{SessionName: sessionName, Label: label, LabelSource: source,
		Agent: &AgentInfo{Type: "cc", PID: pid}, Deliverable: true}
}

func TestResolve_LabelTier(t *testing.T) {
	recs := []PeerRecord{labelRecord("purdex-dev", "user", "mt0", 1), labelRecord("_abc123", "default", "", 2)}
	for _, in := range []string{"purdex-dev", "purdex-dev:whatever-suffix", "_abc123", "_abc123:x"} {
		got, err := Resolve(recs, in, false)
		if err != nil {
			t.Fatalf("%q: %v", in, err)
		}
		if (in[0] == '_' && got.Agent.PID != 2) || (in[0] != '_' && got.Agent.PID != 1) {
			t.Errorf("%q resolved to pid %d", in, got.Agent.PID)
		}
	}
}

func TestResolve_LabelShadowsTmuxName_TmuxFormBypasses(t *testing.T) {
	recs := []PeerRecord{labelRecord("mt4", "user", "mt0", 1), labelRecord("_zzzzzz", "default", "mt4", 2)}
	got, _ := Resolve(recs, "mt4", false)
	if got.Agent.PID != 1 {
		t.Errorf("bare mt4 resolved to pid %d, want the label holder 1", got.Agent.PID)
	}
	got, _ = Resolve(recs, "tmux:mt4", false)
	if got.Agent.PID != 2 {
		t.Errorf("tmux:mt4 resolved to pid %d, want 2", got.Agent.PID)
	}
	if _, err := Resolve(recs, "tmux:", false); !errors.Is(err, ErrNotFound) {
		t.Errorf("tmux: empty ⇒ %v", err)
	}
}

func TestResolve_TmuxFallback_OnlyWhenComplete(t *testing.T) {
	recs := []PeerRecord{{SessionName: "shell"}} // no cc agent, no label
	if got, err := Resolve(recs, "shell", false); err != nil || got.SessionName != "shell" {
		t.Fatalf("complete: %+v %v", got, err)
	}
	if _, err := Resolve(recs, "shell", true); !errors.Is(err, ErrResolveNotReady) {
		t.Fatalf("partial: got %v, want ErrResolveNotReady", err)
	}
	if _, err := Resolve(recs, "shell:x", false); !errors.Is(err, ErrNotFound) {
		t.Fatalf("suffix on a tmux fallback: got %v, want ErrNotFound", err)
	}
}

func TestResolve_CCShortCircuit(t *testing.T) {
	recs := []PeerRecord{labelRecord("cc", "user", "", 1)} // cannot exist, but the resolver must not care
	for _, partial := range []bool{false, true} {
		_, err := Resolve(recs, "cc:foo", partial)
		if !errors.Is(err, ErrNotFound) || !errors.Is(err, ErrLegacyCC) {
			t.Errorf("partial=%v: %v", partial, err)
		}
	}
}

func TestResolve_Ambiguous_SameLabel(t *testing.T) {
	recs := []PeerRecord{labelRecord("_abc123", "default", "mt0", 1), labelRecord("_abc123", "default", "", 2)}
	_, err := Resolve(recs, "_abc123", false)
	var amb *AmbiguousError
	if !errors.As(err, &amb) || len(amb.Candidates) != 2 {
		t.Fatalf("got %v", err)
	}
}

func TestResolve_ProxyAndUnlabelledExcluded(t *testing.T) {
	recs := []PeerRecord{
		{Label: "x1", Agent: &AgentInfo{Type: "proxy"}},
		{SessionName: "x1"}, // no label; tier 2 would match
	}
	got, err := Resolve(recs, "x1", false)
	if err != nil || got.Agent != nil {
		t.Fatalf("got %+v %v, want the tmux row via tier 2", got, err)
	}
}
```

Delete `TestResolve_CodeTier` and the `cc:` tier tests (their behaviour is gone by spec); keep any ambiguity-shape tests, adapting them to labels.

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test -race -count=1 ./internal/peers/ -run TestResolve`
Expected: FAIL — wrong arity / undefined sentinels.

- [ ] **Step 3: Implement** `Resolve`:

```go
var (
	ErrNotFound        = errors.New("peer not found")
	ErrResolveNotReady = errors.New("inventory partial: the label may be missing")
	ErrLegacyCC        = errors.New("cc: addresses were removed; run pdx peers --all to see the new addresses")
)

// Resolve implements Peer Address v2 spec §3.2 for ONE host's records.
func Resolve(records []PeerRecord, session string, partial bool) (PeerRecord, error) {
	if session == "" {
		return PeerRecord{}, ErrNotFound
	}
	head, rest := SplitSession(session)
	switch head {
	case LabelReservedCC:
		return PeerRecord{}, fmt.Errorf("%w: %w", ErrNotFound, ErrLegacyCC)
	case LabelReservedTmux:
		if rest == "" {
			return PeerRecord{}, ErrNotFound
		}
		return resolveTier(records, session, func(r PeerRecord) bool { return r.SessionName == rest })
	}
	// Tier 1: label, over every row; the typed suffix is ignored.
	rec, err := resolveTier(records, session, func(r PeerRecord) bool {
		return r.Label != "" && r.Label == head && (r.Agent == nil || r.Agent.Type != "proxy")
	})
	if !errors.Is(err, ErrNotFound) {
		return rec, err
	}
	if partial {
		return PeerRecord{}, ErrResolveNotReady
	}
	// Tier 2: bare tmux session name, complete inventory only.
	if rest != "" {
		return PeerRecord{}, ErrNotFound
	}
	return resolveTier(records, session, func(r PeerRecord) bool { return r.SessionName == head })
}
```

`send.go` step 6:

```go
	target, err := ipeers.Resolve(rows, session, env.Partial)
	if err != nil {
		var amb *ipeers.AmbiguousError
		switch {
		case errors.As(err, &amb):
			… unchanged …
		case errors.Is(err, ipeers.ErrResolveNotReady):
			detail := fmt.Sprintf("peer inventory on %q is partial; retry, or address the tmux session as tmux:<name>", entry.Alias)
			m.logf("peers: send refused (%s): %s", ipeers.ErrNotReady, detail)
			writeWireError(w, http.StatusServiceUnavailable, ipeers.APIError{Error: ipeers.ErrNotReady, Detail: detail, Partial: true}) // refuseUnaudited cannot set Partial
		case errors.Is(err, ipeers.ErrLegacyCC):
			refuseUnaudited(http.StatusNotFound, ipeers.ErrPeerNotFound, err.Error())
		default:
			refuseUnaudited(http.StatusNotFound, ipeers.ErrPeerNotFound, fmt.Sprintf("no session %q on %q", session, entry.Alias))
		}
		return
	}
```

`ipeers.APIError` gains `Partial bool `json:"partial,omitempty"`` in this task (Task 7 adds the other fields). Also update `msgUsage` in `cmd/pdx/msg.go` to `<host>/<label>[:<suffix>] | <host>/tmux:<name>` (text only; the `name`/`whoami` verbs come in Task 8). Add send tests (in `send_test.go`, same table/fixture as the existing resolution cases): a fake fetch returning `Partial: true` with no matching label ⇒ `503`, body `{"error":"not_ready","partial":true,…}` (decode the JSON and assert `Partial`); `To: "b/cc:foo"` ⇒ `404 peer_not_found` with the legacy hint in `detail`; `To: "b/tmux:foo"` ⇒ resolves the tmux row.

- [ ] **Step 4: Run tests**

Run: `go test -race -count=1 ./internal/peers/... ./internal/module/peers/... ./cmd/pdx/...`
Expected: PASS (fix any test that still calls the two-arg `Resolve`).

- [ ] **Step 5: Commit**

```bash
git add internal/peers/address.go internal/peers/address_test.go internal/module/peers/send.go internal/module/peers/send_test.go cmd/pdx/msg.go
git commit -m "feat(peers): Resolve v2 — label tier, tmux: form, partial ⇒ not_ready, cc: retired"
```

---

### Task 7: Self routes — whoami, claim, release

**Files:**
- Modify: `internal/module/peers/labels.go` (handlers + logic), `internal/module/peers/module.go` (`RegisterRoutes`), `internal/peers/wire.go` (`APIError` fields, request types, error codes)
- Test: `internal/module/peers/labels_test.go`, `internal/module/peers/policy_test.go` (add the denial cases)

**Interfaces:**
- Produces (in `internal/peers/wire.go`):
  ```go
  const (
      ErrCodeLabelInvalid  = "label_invalid"
      ErrCodeLabelReserved = "label_reserved"
      ErrLabelTaken        = "label_taken"
      ErrStoreUnavailable  = "store_unavailable"
  )
  type SelfRequest struct { OriginInbox string `json:"origin_inbox"` }
  type ClaimLabelRequest struct { OriginInbox string `json:"origin_inbox"`; Label string `json:"label"` }
  // APIError gains (Partial was added in Task 6):
  Holder     *PeerRecord `json:"holder,omitempty"`      // label_taken
  LiveLabels []string    `json:"live_labels,omitempty"` // label_taken
  Skipped    []string    `json:"skipped,omitempty"`     // not_ready (claim)
  ```
  Routes: `POST /api/peers/self`, `PUT /api/peers/self/label`, `DELETE /api/peers/self/label`. All three: admin only (`requireAdmin`), answer a `PeerRecord` on 200.
- Produces (in `labels.go`): `func (m *Module) proxyPIDs() map[int]bool`, `func findOriginEntry(entries []ipeers.Entry, proxyPIDs map[int]bool, inbox string) (ipeers.Entry, bool)`.
- Consumes: `ReadRegistryDiag`, `LabelStore`, `ipeers.EntryRecord` (Task 4), `ValidateUserLabel`.
- **Response construction rule:** every 200 body and the `label_taken` holder are built with `ipeers.EntryRecord(alias, hostID, entry, false, info)` from the **validated entry and the label row the handler already holds** — never by re-reading the inventory (`localEnvelope`), which could degrade to default labels or fail after a successful commit. `whoami`/`claim`/`release` therefore always answer an `entry`-shaped record whose address is identical to the listing's (Task 4's suffix rule).
- **Lock rule:** `labelMu` is held from the registry diagnosis through the store write and the construction of the response value; it is released before JSON encoding. Nothing mutable is re-read after unlock.

- [ ] **Step 1: Write the failing tests** — `labels_test.go`, using the fixture from Task 5 and real HTTP through `f.m.RegisterRoutes` + `middleware.WithPrincipal(admin)` as the existing handler tests do (read `send_test.go` for the pattern):

```go
// Scenario fixture: registry has live entries pid 10 (sid-1, tmux
// "mt0:@1.%1", registry name "n10", inbox /…/10.sock) and pid 20 (sid-2,
// no tmux — the Desktop stand-in — registry name "n20", inbox /…/20.sock).
// newLabelFixture returns *labelFixture with: m *Module, labels
// *store.PeerLabelStore, registryDir string, live *labelLiveness
// (markDead/revive, all pids alive by default, modelled on e2eLiveness),
// and helpers:
//   self(req ipeers.SelfRequest) (int, []byte)
//   claim(inbox, label string) (int, []byte)
//   release(inbox string) (int, []byte)
//   inbox(pid int) string
//   assertAPIError(status int, body []byte, wantStatus int, wantCode string) ipeers.APIError
// decodeRecord(t, status, body) fails the test unless status == 200 and
// body decodes as ipeers.PeerRecord.

func TestSelf_Whoami(t *testing.T) {
	f := newLabelFixture(t)
	status, body := f.self(ipeers.SelfRequest{OriginInbox: f.inbox(20)})
	rec := decodeRecord(t, status, body)
	want := "a/" + ipeers.DefaultLabel("sid-2") + ":n20"
	if rec.Address != want || rec.LabelSource != "default" || rec.RowKind != "entry" || rec.Agent.PID != 20 {
		t.Errorf("record = %+v, want address %s", rec, want)
	}
	// A session inside tmux renders the same address the listing shows.
	status, body = f.self(ipeers.SelfRequest{OriginInbox: f.inbox(10)})
	rec = decodeRecord(t, status, body)
	if rec.Suffix != "mt0-n10" {
		t.Errorf("tmux session suffix = %q", rec.Suffix)
	}
	status, body = f.self(ipeers.SelfRequest{OriginInbox: "/nope.sock"})
	f.assertAPIError(status, body, 400, ipeers.ErrOriginUnknown)
	status, body = f.self(ipeers.SelfRequest{})
	f.assertAPIError(status, body, 400, ipeers.ErrOriginUnknown)
}

func TestClaim_Matrix(t *testing.T) {
	f := newLabelFixture(t)
	cases := []struct {
		label      string
		wantStatus int
		wantCode   string
	}{
		{"Bad Label", 400, ipeers.ErrCodeLabelInvalid},
		{"cc", 400, ipeers.ErrCodeLabelReserved},
		{"tmux", 400, ipeers.ErrCodeLabelReserved},
		{"_abc123", 400, ipeers.ErrCodeLabelInvalid},
		{"purdex-tester", 200, ""},
	}
	for _, c := range cases {
		status, body := f.claim(f.inbox(20), c.label)
		if c.wantStatus == 200 {
			rec := decodeRecord(t, status, body)
			if rec.Label != c.label || rec.LabelSource != "user" || rec.LabelRev != 1 || rec.Address != "a/purdex-tester:n20" {
				t.Errorf("%q: %+v", c.label, rec)
			}
			continue
		}
		f.assertAPIError(status, body, c.wantStatus, c.wantCode)
	}
	// Same label again: 200, rev unchanged.
	status, body := f.claim(f.inbox(20), "purdex-tester")
	rec := decodeRecord(t, status, body)
	if rec.LabelRev != 1 {
		t.Errorf("re-claim bumped rev to %d", rec.LabelRev)
	}
	// Another live session: taken, with holder + live_labels (the caller's
	// own live label is listed too — spec §3.3 says every held label).
	status, body = f.claim(f.inbox(10), "purdex-dev")
	decodeRecord(t, status, body)
	status, body = f.claim(f.inbox(10), "purdex-tester")
	ae := f.assertAPIError(status, body, 409, ipeers.ErrLabelTaken)
	if ae.Holder == nil || ae.Holder.Agent.PID != 20 || ae.Holder.Address != "a/purdex-tester:n20" {
		t.Errorf("taken holder = %+v", ae.Holder)
	}
	if !reflect.DeepEqual(ae.LiveLabels, []string{"purdex-dev", "purdex-tester"}) {
		t.Errorf("live_labels = %v", ae.LiveLabels)
	}
	// Holder dies ⇒ claim succeeds, old row evicted, caller's previous label replaced.
	f.live.markDead(20)
	status, body = f.claim(f.inbox(10), "purdex-tester")
	rec = decodeRecord(t, status, body)
	if rec.Agent.PID != 10 || rec.Label != "purdex-tester" || rec.LabelRev != 3 {
		t.Errorf("take-over: %+v", rec)
	}
	rows, _ := f.labels.Snapshot()
	if len(rows) != 1 || rows[0].SessionID != "sid-1" || rows[0].Label != "purdex-tester" {
		t.Errorf("rows after take-over = %+v", rows)
	}
	// The dead one comes back (resume): whoami shows the default label.
	f.live.revive(20)
	status, body = f.self(ipeers.SelfRequest{OriginInbox: f.inbox(20)})
	rec = decodeRecord(t, status, body)
	if rec.LabelSource != "default" || rec.LabelRev != 0 {
		t.Errorf("resumed holder = %+v, want default label, rev 0", rec)
	}
}

func TestClaim_NotReadyOnUnknownLiveFile(t *testing.T) {
	f := newLabelFixture(t)
	writeRegistryFixture(t, f.registryDir, "4242.json", "{") // pid 4242 alive per fake
	status, body := f.claim(f.inbox(20), "purdex-tester")
	ae := f.assertAPIError(status, body, 503, ipeers.ErrNotReady)
	if len(ae.Skipped) != 1 || !strings.HasSuffix(ae.Skipped[0], "4242.json") {
		t.Errorf("skipped = %v", ae.Skipped)
	}
	f.live.markDead(4242) // now the unknown file belongs to a dead pid: ignored
	status, body = f.claim(f.inbox(20), "purdex-tester")
	decodeRecord(t, status, body)
	// Release has no completeness requirement.
	writeRegistryFixture(t, f.registryDir, "4243.json", "{")
	status, body = f.release(f.inbox(20))
	decodeRecord(t, status, body)
}

func TestClaim_OriginMustBeLiveNonProxy(t *testing.T) {
	f := newLabelFixture(t)
	f.live.markDead(20)
	status, body := f.claim(f.inbox(20), "purdex-tester")
	f.assertAPIError(status, body, 400, ipeers.ErrOriginUnknown)
	// A helper (proxy) entry cannot name itself: register one via the
	// fixture's helper manager (Acquire) and present its inbox.
	h := f.spawnHelper(t) // fixture helper: Acquire a fake helper, return its *helper
	status, body = f.claim(h.sock, "purdex-tester")
	f.assertAPIError(status, body, 400, ipeers.ErrOriginUnknown)
}

func TestClaim_StoreFailures(t *testing.T) {
	f := newLabelFixture(t)
	f.m.labels = failingLabels{} // read fails
	status, body := f.claim(f.inbox(20), "purdex-tester")
	f.assertAPIError(status, body, 503, ipeers.ErrStoreUnavailable)
	status, body = f.self(ipeers.SelfRequest{OriginInbox: f.inbox(20)})
	f.assertAPIError(status, body, 503, ipeers.ErrStoreUnavailable)

	f.m.labels = writeFailingLabels{real: f.labels} // read ok, write fails
	status, body = f.claim(f.inbox(20), "purdex-tester")
	f.assertAPIError(status, body, 503, ipeers.ErrStoreUnavailable)
	status, body = f.release(f.inbox(20))
	f.assertAPIError(status, body, 503, ipeers.ErrStoreUnavailable)
	if rows, _ := f.labels.Snapshot(); len(rows) != 0 {
		t.Errorf("rows written despite failure: %+v", rows)
	}
	// whoami only reads: still fine.
	status, body = f.self(ipeers.SelfRequest{OriginInbox: f.inbox(20)})
	decodeRecord(t, status, body)
}

func TestRelease(t *testing.T) {
	f := newLabelFixture(t)
	status, body := f.claim(f.inbox(20), "purdex-tester")
	decodeRecord(t, status, body)
	status, body = f.release(f.inbox(20))
	rec := decodeRecord(t, status, body)
	if rec.LabelSource != "default" || rec.LabelRev != 2 || rec.Label != ipeers.DefaultLabel("sid-2") {
		t.Errorf("released = %+v", rec)
	}
	// Release with no row: 200, default, rev 0, nothing written.
	status, body = f.release(f.inbox(10))
	rec = decodeRecord(t, status, body)
	if rec.LabelRev != 0 {
		t.Errorf("no-row release = %+v", rec)
	}
	if rows, _ := f.labels.Snapshot(); len(rows) != 1 {
		t.Errorf("rows = %+v, want only sid-2's released row", rows)
	}
}

func TestClaim_ConcurrentSameLabel_OneWins(t *testing.T) {
	f := newLabelFixture(t)
	var wg sync.WaitGroup
	results := make([]int, 2)
	for i, pid := range []int{10, 20} {
		wg.Add(1)
		go func(i, pid int) {
			defer wg.Done()
			results[i], _ = f.claim(f.inbox(pid), "purdex-tester")
		}(i, pid)
	}
	wg.Wait()
	sort.Ints(results)
	if results[0] != 200 || results[1] != 409 {
		t.Fatalf("statuses = %v, want [200 409]", results)
	}
}

func TestSelfRoutes_DenyHostPrincipal(t *testing.T) {
	for _, c := range []struct{ method, path string }{
		{"POST", "/api/peers/self"}, {"PUT", "/api/peers/self/label"}, {"DELETE", "/api/peers/self/label"},
	} {
		r := httptest.NewRequest(c.method, c.path, nil)
		if HostRoutePolicy(r) {
			t.Errorf("%s %s allowed for a host principal", c.method, c.path)
		}
	}
	// And the handlers themselves refuse a host principal in depth.
	f := newLabelFixture(t)
	status, _ := f.doAs(middleware.Principal{Kind: middleware.PrincipalHost, Alias: "x", HostID: "x:1"}, "POST", "/api/peers/self", ipeers.SelfRequest{OriginInbox: f.inbox(20)})
	if status != 403 {
		t.Errorf("host principal got %d", status)
	}
}
```

Write `newLabelFixture` in `labels_test.go`: builds `newTestModuleWith` with the two-entry registry (`writeRegistryFixture` + the registry JSON helper `deliver_test.go` uses, one with `"tmux":"mt0:@1.%1"` and one without), a liveness fake `labelLiveness` with `markDead`/`revive` (all pids alive by default; model it on `e2eLiveness` in `e2e_test.go`), fake sessions `[{Code:"c1", Name:"mt0"}]` with owner `sid-1`, and the helpers listed in the comment above (`doAs` builds the request with `middleware.WithPrincipal`; `self`/`claim`/`release` call it with the admin principal). `spawnHelper` calls `f.m.helpers.Acquire(ctx, someOriginKey, "x/y")` on the fixture's fake helper and returns the `*helper`.

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test -race -count=1 ./internal/module/peers/ -run 'TestSelf|TestClaim|TestRelease'`
Expected: FAIL to compile.

- [ ] **Step 3: Implement** in `labels.go`. One internal function per verb returns `(rec ipeers.PeerRecord, apiErr *ipeers.APIError, status int)`; the three handlers only decode, call it, and encode after the lock is gone.

```go
// findOriginEntry attributes inbox to a live, non-proxy registry entry
// (entry attribution, spec §3.6 — not send.go's deliverable-row rule).
func findOriginEntry(entries []ipeers.Entry, proxyPIDs map[int]bool, inbox string) (ipeers.Entry, bool) {
	for _, e := range entries {
		if e.Inbox == inbox && !e.IsProxy && !proxyPIDs[e.PID] {
			return e, true
		}
	}
	return ipeers.Entry{}, false
}

// proxyPIDs is the helper manager's pid set, or empty without a manager.
func (m *Module) proxyPIDs() map[int]bool {
	if m.helpers == nil {
		return map[int]bool{}
	}
	return m.helpers.ProxyPIDs()
}

// labelRows indexes a store snapshot by session id.
func labelRows(rows []store.PeerLabel) map[string]store.PeerLabel {
	out := make(map[string]store.PeerLabel, len(rows))
	for _, r := range rows {
		out[r.SessionID] = r
	}
	return out
}

func infoOf(row store.PeerLabel, ok bool) ipeers.LabelInfo {
	if !ok {
		return ipeers.LabelInfo{}
	}
	return ipeers.LabelInfo{Label: row.Label, Rev: row.Rev}
}

type selfResult struct {
	rec    ipeers.PeerRecord
	err    *ipeers.APIError
	status int
}

func fail(status int, code, detail string) selfResult {
	return selfResult{status: status, err: &ipeers.APIError{Error: code, Detail: detail}}
}

// origin reads the registry and attributes inbox. Shared by all three verbs.
func (m *Module) origin(inbox string) (entries []ipeers.Entry, diag ipeers.Diagnosis, proxies map[int]bool, e ipeers.Entry, res selfResult, ok bool) {
	if inbox == "" {
		return nil, ipeers.Diagnosis{}, nil, ipeers.Entry{}, fail(http.StatusBadRequest, ipeers.ErrOriginUnknown, "origin_inbox is empty (CLAUDE_CODE_MESSAGING_SOCKET unset?)"), false
	}
	if m.labels == nil {
		return nil, ipeers.Diagnosis{}, nil, ipeers.Entry{}, fail(http.StatusServiceUnavailable, ipeers.ErrStoreUnavailable, "label store is not available"), false
	}
	entries, diag, err := ipeers.ReadRegistryDiag(m.registryDir, m.liveness)
	if err != nil {
		return nil, diag, nil, ipeers.Entry{}, fail(http.StatusServiceUnavailable, ipeers.ErrNotReady, "registry read failed: "+err.Error()), false
	}
	proxies = m.proxyPIDs()
	e, found := findOriginEntry(entries, proxies, inbox)
	if !found {
		return nil, diag, nil, ipeers.Entry{}, fail(http.StatusBadRequest, ipeers.ErrOriginUnknown, "origin_inbox is not a live Claude Code session on this host"), false
	}
	return entries, diag, proxies, e, selfResult{}, true
}

func (m *Module) whoami(inbox string) selfResult {
	m.labelMu.Lock()
	defer m.labelMu.Unlock()
	_, _, _, e, res, ok := m.origin(inbox)
	if !ok {
		return res
	}
	rows, err := m.labels.Snapshot()
	if err != nil { // never print a default label as if it were the truth
		return fail(http.StatusServiceUnavailable, ipeers.ErrStoreUnavailable, "label store read failed")
	}
	snap := m.configSnapshot()
	row, has := labelRows(rows)[e.SessionID]
	return selfResult{status: http.StatusOK, rec: ipeers.EntryRecord(snap.alias, snap.hostID, e, false, infoOf(row, has))}
}

func (m *Module) claim(inbox, label string) selfResult {
	if err := ipeers.ValidateUserLabel(label); err != nil {
		code := ipeers.ErrCodeLabelInvalid
		if errors.Is(err, ipeers.ErrLabelReserved) {
			code = ipeers.ErrCodeLabelReserved
		}
		return fail(http.StatusBadRequest, code, err.Error())
	}
	m.labelMu.Lock()
	defer m.labelMu.Unlock()
	entries, diag, proxies, e, res, ok := m.origin(inbox)
	if !ok {
		return res
	}
	if blocking := diag.BlockingUnknown(); len(blocking) > 0 {
		r := fail(http.StatusServiceUnavailable, ipeers.ErrNotReady, "registry has unreadable files for live processes; a label cannot be proven free")
		r.err.Skipped = blocking
		return r
	}
	rows, err := m.labels.Snapshot()
	if err != nil {
		return fail(http.StatusServiceUnavailable, ipeers.ErrStoreUnavailable, "label store read failed")
	}
	liveEntry := map[string]ipeers.Entry{} // sid ⇒ one live entry (first seen)
	for _, le := range entries {
		if !le.IsProxy && !proxies[le.PID] {
			if _, seen := liveEntry[le.SessionID]; !seen {
				liveEntry[le.SessionID] = le
			}
		}
	}
	var liveLabels []string
	var holder *store.PeerLabel
	for i := range rows {
		row := &rows[i]
		if row.Label == "" {
			continue
		}
		if _, live := liveEntry[row.SessionID]; !live {
			continue
		}
		liveLabels = append(liveLabels, row.Label)
		if row.Label == label {
			holder = row
		}
	}
	sort.Strings(liveLabels)
	snap := m.configSnapshot()
	if holder != nil && holder.SessionID != e.SessionID {
		he := liveEntry[holder.SessionID]
		hrec := ipeers.EntryRecord(snap.alias, snap.hostID, he, false, ipeers.LabelInfo{Label: holder.Label, Rev: holder.Rev})
		r := fail(http.StatusConflict, ipeers.ErrLabelTaken, fmt.Sprintf("%q is held by a live session", label))
		r.err.Holder, r.err.LiveLabels = &hrec, liveLabels
		return r
	}
	var row store.PeerLabel
	if holder != nil { // already ours: no write, rev unchanged
		row = *holder
	} else {
		row, err = m.labels.Claim(e.SessionID, label, m.now())
		if err != nil {
			m.logf("peers: claim %q for %s: %v", label, e.SessionID, err)
			return fail(http.StatusServiceUnavailable, ipeers.ErrStoreUnavailable, "label store write failed")
		}
	}
	return selfResult{status: http.StatusOK, rec: ipeers.EntryRecord(snap.alias, snap.hostID, e, false, ipeers.LabelInfo{Label: row.Label, Rev: row.Rev})}
}

func (m *Module) release(inbox string) selfResult {
	m.labelMu.Lock()
	defer m.labelMu.Unlock()
	_, _, _, e, res, ok := m.origin(inbox)
	if !ok {
		return res
	}
	row, had, err := m.labels.Release(e.SessionID, m.now())
	if err != nil {
		m.logf("peers: release for %s: %v", e.SessionID, err)
		return fail(http.StatusServiceUnavailable, ipeers.ErrStoreUnavailable, "label store write failed")
	}
	info := ipeers.LabelInfo{}
	if had {
		info.Rev = row.Rev
	}
	snap := m.configSnapshot()
	return selfResult{status: http.StatusOK, rec: ipeers.EntryRecord(snap.alias, snap.hostID, e, false, info)}
}

// handlers: decode → call → encode (outside labelMu).
func (m *Module) handleSelf(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if !requireAdmin(w, r) {
		return
	}
	var req ipeers.SelfRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req); err != nil {
		writeWireError(w, http.StatusBadRequest, ipeers.APIError{Error: ipeers.ErrBadRequest, Detail: "invalid JSON body"})
		return
	}
	writeSelfResult(w, m.whoami(req.OriginInbox))
}

func (m *Module) handleClaimLabel(w http.ResponseWriter, r *http.Request) { /* same shape with ClaimLabelRequest ⇒ m.claim */ }
func (m *Module) handleReleaseLabel(w http.ResponseWriter, r *http.Request) { /* SelfRequest ⇒ m.release */ }

func writeSelfResult(w http.ResponseWriter, res selfResult) {
	if res.err != nil {
		writeWireError(w, res.status, *res.err)
		return
	}
	_ = json.NewEncoder(w).Encode(res.rec)
}
```

Register the three routes in `RegisterRoutes`. `HostRoutePolicy` needs no change (only `GET /api/peers` and `POST /deliver` pass) — the test proves it, and `requireAdmin` refuses in depth.

Note the claim path never calls `localEnvelope`: no tmux or owner resolution runs under `labelMu`, so the lock is held for one registry scan plus one SQLite transaction.

- [ ] **Step 4: Run tests**

Run: `go test -race -count=1 ./internal/module/peers/... ./internal/peers/...`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/peers/wire.go internal/module/peers/labels.go internal/module/peers/labels_test.go internal/module/peers/module.go internal/module/peers/policy_test.go
git commit -m "feat(peers): /api/peers/self routes — whoami, claim, release with live-occupancy rule"
```

---

### Task 8: CLI — `pdx msg name` / `pdx msg whoami`

**Files:**
- Modify: `cmd/pdx/msg.go`
- Test: `cmd/pdx/msg_test.go` (grammar table + rendering; look at how existing send tests fake the daemon with `httptest`)

**Interfaces:**
- Grammar: `pdx msg name <label> [--json] [--config <path>]`, `pdx msg name --release [--json] [--config <path>]`, `pdx msg whoami [--json] [--config <path>]`. `msgInvocation` gains `label string`, `release bool`. New flag `--release` valid only for `name`; `name` requires exactly one positional unless `--release` (then zero).
- Consumes: `POST /api/peers/self` (`ipeers.SelfRequest`), `PUT`/`DELETE /api/peers/self/label` (`ipeers.ClaimLabelRequest` / `ipeers.SelfRequest`), responses `ipeers.PeerRecord` / `ipeers.APIError`.

- [ ] **Step 1: Write the failing tests**

```go
func TestParseMsgInvocation_NameAndWhoami(t *testing.T) {
	cases := []struct {
		args []string
		ok   bool
		verb string
		label string
		release bool
	}{
		{[]string{"name", "purdex-tester"}, true, "name", "purdex-tester", false},
		{[]string{"name", "--release"}, true, "name", "", true},
		{[]string{"name"}, false, "", "", false},
		{[]string{"name", "a", "b"}, false, "", "", false},
		{[]string{"name", "a", "--release"}, false, "", "", false},
		{[]string{"whoami"}, true, "whoami", "", false},
		{[]string{"whoami", "x"}, false, "", "", false},
		{[]string{"send", "--release", "a/b", "t"}, false, "", "", false},
	}
	for _, c := range cases {
		inv, _, ok := parseMsgInvocation(c.args)
		if ok != c.ok || (ok && (inv.verb != c.verb || inv.label != c.label || inv.release != c.release)) {
			t.Errorf("%v ⇒ ok=%v inv=%+v", c.args, ok, inv)
		}
	}
}

func TestRunMsgWhoami_Text(t *testing.T) {
	srv := fakeDaemon(t, func(w http.ResponseWriter, r *http.Request) { // helper: httptest server + config file
		if r.Method != "POST" || r.URL.Path != "/api/peers/self" {
			t.Errorf("%s %s", r.Method, r.URL.Path)
		}
		json.NewEncoder(w).Encode(ipeers.PeerRecord{
			Host: "air", HostID: "air:9k2m4q", Address: "air/purdex-tester:purdex-3f",
			Label: "purdex-tester", LabelSource: "user", LabelRev: 7,
			Agent: &ipeers.AgentInfo{Type: "cc", SessionID: "fa5d4c07-0000", PID: 76973},
		})
	})
	var out, errb bytes.Buffer
	code := runMsgCmd([]string{"whoami", "--config", srv.cfgPath}, envWith("CLAUDE_CODE_MESSAGING_SOCKET", "/tmp/x.sock"), &out, &errb)
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errb.String())
	}
	want := "address:  air/purdex-tester:purdex-3f\nlabel:    purdex-tester (user, rev 7)\nhost:     air (air:9k2m4q)\nsession:  fa5d4c07-0000 pid 76973\n"
	if out.String() != want {
		t.Errorf("got:\n%s\nwant:\n%s", out.String(), want)
	}
}

func TestRunMsgName_TakenRendersLiveLabels(t *testing.T) {
	srv := fakeDaemon(t, func(w http.ResponseWriter, r *http.Request) {
		var req ipeers.ClaimLabelRequest
		json.NewDecoder(r.Body).Decode(&req)
		if r.Method != "PUT" || r.URL.Path != "/api/peers/self/label" || req.Label != "purdex-tester" {
			t.Errorf("%s %s %+v", r.Method, r.URL.Path, req)
		}
		w.WriteHeader(409)
		json.NewEncoder(w).Encode(ipeers.APIError{Error: "label_taken", Detail: `"purdex-tester" is held by a live session`, LiveLabels: []string{"purdex-dev", "purdex-tester"}})
	})
	var out, errb bytes.Buffer
	code := runMsgCmd([]string{"name", "purdex-tester", "--config", srv.cfgPath}, envWith("CLAUDE_CODE_MESSAGING_SOCKET", "/tmp/x.sock"), &out, &errb)
	if code != 1 {
		t.Fatalf("exit %d", code)
	}
	want := "pdx msg: label_taken: \"purdex-tester\" is held by a live session\n  purdex-dev\n  purdex-tester\n"
	if errb.String() != want {
		t.Errorf("stderr:\n%s\nwant:\n%s", errb.String(), want)
	}
}

func TestRunMsgName_Release_UsesDelete(t *testing.T) {
	srv := fakeDaemon(t, func(w http.ResponseWriter, r *http.Request) {
		var req ipeers.SelfRequest
		json.NewDecoder(r.Body).Decode(&req)
		if r.Method != "DELETE" || r.URL.Path != "/api/peers/self/label" || req.OriginInbox != "/tmp/x.sock" {
			t.Errorf("%s %s %+v", r.Method, r.URL.Path, req)
		}
		json.NewEncoder(w).Encode(ipeers.PeerRecord{Address: "air/_k3x9qz:purdex-3f", Label: "_k3x9qz", LabelSource: "default", LabelRev: 2, Host: "air", HostID: "air:1"})
	})
	var out, errb bytes.Buffer
	code := runMsgCmd([]string{"name", "--release", "--config", srv.cfgPath}, envWith("CLAUDE_CODE_MESSAGING_SOCKET", "/tmp/x.sock"), &out, &errb)
	if code != 0 || !strings.HasPrefix(out.String(), "released: air/_k3x9qz:purdex-3f\n") {
		t.Fatalf("exit %d out %q err %q", code, out.String(), errb.String())
	}
}

func TestRunMsgName_NoSocketEnv(t *testing.T) {
	var out, errb bytes.Buffer
	code := runMsgCmd([]string{"name", "x1"}, envWith("CLAUDE_CODE_MESSAGING_SOCKET", ""), &out, &errb)
	if code != 1 || !strings.Contains(errb.String(), "pdx msg: origin_unknown: CLAUDE_CODE_MESSAGING_SOCKET is unset") {
		t.Fatalf("exit %d err %q", code, errb.String())
	}
	code = runMsgCmd([]string{"whoami"}, envWith("CLAUDE_CODE_MESSAGING_SOCKET", ""), &out, &errb)
	if code != 1 {
		t.Fatalf("whoami exit %d", code)
	}
}

func TestRunMsgWhoami_JSONPassthrough(t *testing.T) {
	raw := `{"address":"air/x1:y","label":"x1"}` + "\n"
	srv := fakeDaemon(t, func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, raw) })
	var out, errb bytes.Buffer
	code := runMsgCmd([]string{"whoami", "--json", "--config", srv.cfgPath}, envWith("CLAUDE_CODE_MESSAGING_SOCKET", "/tmp/x.sock"), &out, &errb)
	if code != 0 || out.String() != raw {
		t.Fatalf("exit %d out %q", code, out.String())
	}
}

func TestRunMsgName_NotReadyRendersSkipped(t *testing.T) {
	srv := fakeDaemon(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(503)
		json.NewEncoder(w).Encode(ipeers.APIError{Error: "not_ready", Detail: "registry has unreadable files", Skipped: []string{"/r/4242.json"}})
	})
	var out, errb bytes.Buffer
	runMsgCmd([]string{"name", "x1", "--config", srv.cfgPath}, envWith("CLAUDE_CODE_MESSAGING_SOCKET", "/tmp/x.sock"), &out, &errb)
	if errb.String() != "pdx msg: not_ready: registry has unreadable files\n  /r/4242.json\n" {
		t.Errorf("stderr %q", errb.String())
	}
}
```

`fakeDaemon(t, handler)` and `envWith(k, v)` are small helpers to add to `msg_test.go` if the file has no equivalent: `fakeDaemon` starts an `httptest.Server` with the handler and writes a `config.toml` (`bind`/`port` from the server URL, `token = "t"`) into `t.TempDir()`, returning `struct{ cfgPath string }`; `envWith` returns `func(string) string`. If `msg_test.go` already fakes the daemon differently (read it first), use that and drop these two.

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test -race -count=1 ./cmd/pdx/ -run 'TestParseMsgInvocation_NameAndWhoami|TestRunMsgWhoami|TestRunMsgName'`
Expected: FAIL.

- [ ] **Step 3: Implement**
- `msgUsage` gains the two lines: `pdx msg name <label> | --release [--json] [--config <path>]` and `pdx msg whoami [--json] [--config <path>]`.
- `parseMsgInvocation`: parse `--release` into `hasRelease`; verbs `name` (positional arity 1 without `--release`, 0 with; reject `--mode/--tail/--timeout`) and `whoami` (arity 0; reject the same flags and `--release`); every other verb rejects `--release`.
- `runMsgName` / `runMsgWhoami`: read the socket env exactly as `runMsgSend`; `config.Load`; request via `doPeersRequest` (`PUT`/`DELETE`/`POST`, `peersRequestTimeout`); `--json` ⇒ `writeMsgJSONPassthrough`; non-200 ⇒ `decodeMsgAPIError` + `renderMsgAPIError` (extend it: `label_taken` prints `live_labels` one per line indented two spaces after the generic line; `not_ready` prints `skipped` the same way); 200 ⇒ decode `ipeers.PeerRecord`:

```go
func renderSelfRecord(rec ipeers.PeerRecord, stdout io.Writer) {
	fmt.Fprintf(stdout, "address:  %s\n", sanitizeCell(rec.Address))
	fmt.Fprintf(stdout, "label:    %s (%s, rev %d)\n", sanitizeCell(rec.Label), sanitizeCell(rec.LabelSource), rec.LabelRev)
	fmt.Fprintf(stdout, "host:     %s (%s)\n", sanitizeCell(rec.Host), sanitizeCell(rec.HostID))
	if rec.Agent != nil {
		fmt.Fprintf(stdout, "session:  %s pid %d\n", sanitizeCell(rec.Agent.SessionID), rec.Agent.PID)
	}
}
```

`name` prints `named: <address>` then the record block; `--release` prints `released: <address>` then the block; `whoami` prints the block only. Wire the two verbs into `runMsgCmd`'s `switch inv.verb` (`case "name": return runMsgName(...)`, `case "whoami": return runMsgWhoami(...)`) — the switch's default is unreachable only because the parser accepts exactly the verbs it lists.

- [ ] **Step 4: Run tests**

Run: `go test -race -count=1 ./cmd/pdx/...`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add cmd/pdx/msg.go cmd/pdx/msg_test.go
git commit -m "feat(pdx): msg name / msg whoami"
```

---

### Task 9: `pdx peers` — LABEL column, entry rows, daemon versions

**Files:**
- Modify: `cmd/pdx/peers.go`
- Test: `cmd/pdx/peers_test.go` (find the existing table tests and extend)

- [ ] **Step 1: Write the failing test**

```go
func TestFormatPeersTable_LabelColumnAndEntryIndent(t *testing.T) {
	env := peers.Envelope{OK: true, DaemonVersion: "1.0.0-alpha.342", Peers: []peers.PeerRecord{
		{Address: "a/purdex-dev:mt0-x", RowKind: "session", Label: "purdex-dev", LabelSource: "user", Agent: &peers.AgentInfo{Type: "cc", PeerName: "x", Status: "idle"}, Deliverable: true, Cwd: "/w"},
		{Address: "a/_k3x9qz:y", RowKind: "entry", Label: "_k3x9qz", LabelSource: "default", Agent: &peers.AgentInfo{Type: "cc", PeerName: "y", Status: "busy"}, Deliverable: true, Cwd: "/w"},
		{Address: "a/tmux:shell", RowKind: "session", Reason: "no_agent"},
	}}
	got := formatPeersTable(env)
	for _, want := range []string{
		"ADDRESS\tLABEL\tAGENT", // header (tabwriter expands; assert on the flushed text below instead)
	} {
		_ = want
	}
	lines := strings.Split(strings.TrimRight(got, "\n"), "\n")
	if !strings.HasPrefix(lines[0], "ADDRESS") || !strings.Contains(lines[0], "LABEL") {
		t.Errorf("header: %q", lines[0])
	}
	if !strings.HasPrefix(lines[1], "a/purdex-dev:mt0-x") || !strings.Contains(lines[1], "purdex-dev ") {
		t.Errorf("session row: %q", lines[1])
	}
	if !strings.HasPrefix(lines[2], "  a/_k3x9qz:y") || !strings.Contains(lines[2], "_k3x9qz*") {
		t.Errorf("entry row (indented, default marked): %q", lines[2])
	}
	if !strings.Contains(lines[3], "-") { // empty label renders "-"
		t.Errorf("shell row: %q", lines[3])
	}
	if !strings.Contains(got, "daemon 1.0.0-alpha.342") {
		t.Errorf("version trailer missing:\n%s", got)
	}
}
```

Extend the `--all` table test the same way: a trailer line per host `<alias>  daemon <version>` (and the existing `(unreachable: …)` lines stay). **Plan delta to spec §3.6** (record in the P4a PR): the daemon version is printed as a trailer line, not a per-host header, because both tables are one `tabwriter` block and a header row per host would break column alignment.

- [ ] **Step 2: Run to verify failure** — `go test -race -count=1 ./cmd/pdx/ -run TestFormatPeers`

- [ ] **Step 3: Implement**: add `labelField(rec)` (`"-"` when empty; `label + "*"` when `LabelSource == "default"`), indent `rec.Address` with two spaces when `RowKind == "entry"`, insert the LABEL column after ADDRESS in both tables, append `fmt.Fprintf(&buf, "daemon %s\n", sanitizeCell(resp.DaemonVersion))` to the local table (after the partial line) and `<alias>  daemon <version>` per OK host to the all-table trailer.

- [ ] **Step 4: Run** — `go test -race -count=1 ./cmd/pdx/...` ⇒ PASS.

- [ ] **Step 5: Commit**

```bash
git add cmd/pdx/peers.go cmd/pdx/peers_test.go
git commit -m "feat(pdx): peers table shows labels, entry rows and daemon versions"
```

---

### Task 10: P4a e2e, the R2-1 scenario, CLAUDE.md, PR

**Files:**
- Modify: `internal/module/peers/e2e_test.go`, `CLAUDE.md`

- [ ] **Step 1: Write the failing e2e tests** (extend `TestE2E_TwoDaemons` or add `TestE2E_Labels` reusing its setup):

```go
func TestE2E_Labels(t *testing.T) {
	// Setup identical to TestE2E_TwoDaemons (A: origin in mt1; B: target in foo).
	…
	// 1. B's target claims "purdex-tester" through B's own /self/label.
	status, body := b.do("PUT", "/api/peers/self/label", b.admin, ipeers.ClaimLabelRequest{OriginInbox: targetSock, Label: "purdex-tester"})
	if status != 200 {
		t.Fatalf("claim: %d %s", status, body)
	}
	// 2. A sends by label; delivered to target.sock; to_address is the v2 address.
	sent := a.sendOK(ipeers.SendRequest{To: "b/purdex-tester", Text: "ping", OriginInbox: originSock})
	if sent.ToAddress != "b/purdex-tester:foo-"+e2eTargetName || sent.To != targetTo {
		t.Errorf("to = %s %+v", sent.ToAddress, sent.To)
	}
	target.recv("step 2")
	// 3. tmux: form and bare tmux name still deliver; cc: does not.
	a.sendOK(ipeers.SendRequest{To: "b/tmux:foo", Text: "x", OriginInbox: originSock})
	target.recv("step 3a")
	a.sendOK(ipeers.SendRequest{To: "b/foo", Text: "x", OriginInbox: originSock})
	target.recv("step 3b")
	st, raw := a.send(ipeers.SendRequest{To: "b/cc:" + e2eTargetName, Text: "x", OriginInbox: originSock})
	a.assertAPIError(st, raw, 404, ipeers.ErrPeerNotFound, "cc: form")
	// 4. Native reply from target through B's helper reaches origin (unchanged path; from-name is still v1 here).
	…
	// 5. R2-1 (spec §3.3): a Desktop session on B holds the LABEL "foo" while
	//    B also has a tmux session named foo. While everything is readable the
	//    label shadows the tmux name; when the holder's own registry file
	//    becomes unreadable, a bare "b/foo" must be 503 not_ready — never a
	//    tier-2 delivery to the tmux session — and "b/tmux:foo" still delivers.
	holderSock := filepath.Join(root, "holder.sock")
	holder := startFakeInbox(t, holderSock)
	const holderPID, holderSID = 31337, "holder-sid"
	writeRegistryFixture(t, regDir, strconv.Itoa(holderPID)+".json", e2eRegistryJSON(holderPID, holderSID, "holder-1", "", holderSock)) // no tmux: entry row on B
	st, raw = b.do("PUT", "/api/peers/self/label", b.admin, ipeers.ClaimLabelRequest{OriginInbox: holderSock, Label: "foo"})
	if st != 200 {
		t.Fatalf("holder claim: %d %s", st, raw)
	}
	a.sendOK(ipeers.SendRequest{To: "b/foo", Text: "to-label", OriginInbox: originSock})
	holder.recv("step 5a: label shadows tmux name")
	target.none("step 5a")
	writeRegistryFixture(t, regDir, strconv.Itoa(holderPID)+".json", "{") // holder unreadable, pid still alive per fake liveness
	st, raw = a.send(ipeers.SendRequest{To: "b/foo", Text: "x", OriginInbox: originSock})
	a.assertAPIError(st, raw, 503, ipeers.ErrNotReady, "partial bare name")
	target.none("step 5b: no tier-2 delivery while the label holder is unknown")
	a.sendOK(ipeers.SendRequest{To: "b/tmux:foo", Text: "x", OriginInbox: originSock})
	target.recv("step 5c")
}
```

Check `e2eLiveness.liveness()` — if it derives `PidAlive` from `dead` only, the unknown file for pid 31337 is "alive" as required; otherwise mark it alive explicitly. `e2eRegistryJSON` with an empty `tmux` argument must produce an entry outside tmux (check its template; add an `omitempty`-style branch if it always writes the field).

- [ ] **Step 2: Run** — this is an integration test over Tasks 1–9: if it passes immediately, that is the expected outcome (do not manufacture a red run); if it fails, the failure is a real integration bug — fix it in the task that owns it and note the fix in that task's commit.

- [ ] **Step 3: CLAUDE.md** — add under "開發環境" a section:

```markdown
## Peer addresses（跨主機 agent 訊息）

- 地址格式：`<host>/<label>[:<suffix>]`。**只有 `<label>` 是地址**；`:<suffix>` 是 daemon 產生的識別資訊（tmux 名-CC 名），打不打都一樣。
- label 規則：小寫英數與 `-`，2–32 字，`^[a-z0-9][a-z0-9-]{1,31}$`；`cc`、`tmux` 保留。命名慣例 `<專案>-<角色>[-<序號>]`（`purdex-tester`、`purdex-tester-2`）。
- `_xxxxxx` 開頭＝尚未命名（由 sessionId 導出的預設值）。
- 指令：`pdx msg name <label>`（命名自己）、`pdx msg name --release`、`pdx msg whoami`（看自己的地址）、`pdx msg send <host>/<label> "<text>"`；`pdx peers --all` 看所有主機的 session。
- `<host>/tmux:<tmux session 名>` 是不經 label 的 fallback。`cc:<name>` 形式已移除。
- 被要求「成為 X」時：先 `pdx msg name X`，再 `pdx msg whoami` 回報地址；`label_taken` 時從回應的 live_labels 挑一個沒撞的。
```

- [ ] **Step 4: Full gate**

Run: `go test -race -count=1 ./... && go vet ./... && make build`
Expected: PASS.

- [ ] **Step 5: Commit and open the P4a PR**

```bash
git add internal/module/peers/e2e_test.go CLAUDE.md
git commit -m "test(peers): label e2e incl. partial-inventory guard; docs: peer addresses in CLAUDE.md"
git push -u origin worktree-peer-address-v2
gh pr create --title "Peer Address v2 (P4a): session labels, Resolve v2, self routes, CLI" --body-file - <<'EOF'
Implements docs/specs/2026-09-14-peer-address-v2-spec.md §4 P4a.
- label primitives, registry diagnosis (live/dead/unknown), peer_labels store with host-wide rev
- inventory: labels joined, every live entry has a row, unknown registry files ⇒ partial, daemon_version
- Resolve v2: label tier, tmux: form, partial ⇒ not_ready, cc: retired
- /api/peers/self, /self/label (PUT/DELETE); pdx msg name / whoami; pdx peers columns
- CLAUDE.md "Peer addresses"
Spec deltas (§3.4/§3.6): PeerRecord carries label_rev; the suffix is derived from the entry's own tmux field for session and entry rows alike; pdx peers prints daemon versions as a trailer line.
Deployment: both hosts. from-name / helper names stay v1 until P4b.

https://claude.ai/code/session_01QDPyP9TxbfJnePDocfwQJZ
EOF
```

Then the CLAUDE.md review process (codex standard + 3 adversarial), fixes, merge (regular merge), bump PR, deploy both hosts — outside this plan's task list, per project CLAUDE.md.

---

# Phase P4b — Wire display and helper renames

### Task 11: Wire — `from.address` / `from.address_rev`

**Files:**
- Modify: `internal/peers/wire.go`, `internal/peers/record.go` (`WireAddress`)
- Test: `internal/peers/wire_test.go`, `internal/peers/record_test.go` (`WireAddress` on a labelled row ⇒ `label:suffix`, on a no-agent row ⇒ `""`)

**Interfaces:**
- Produces:
  ```go
  // WireFrom gains:
  Address    string `json:"address,omitempty"`     // "<label>:<suffix>"; "" from a v1 sender
  AddressRev int64  `json:"address_rev,omitempty"`
  var ErrAddressInvalid = errors.New("address invalid")   // ValidationCode ⇒ ErrBadAddress
  func ValidateWireAddress(s string) error                 // "" ok; head = user or default label; rest = "" or ValidSuffix
  func (r PeerRecord) WireAddress() string                 // Label + ":" + Suffix, "" when Label == ""  (put in record.go)
  ```

- [ ] **Step 1: Failing tests**

```go
func TestValidateWireAddress(t *testing.T) {
	ok := []string{"", "purdex-tester", "purdex-tester:purdex-3f", "_k3x9qz:mt0-purdex-49", "a1:" + strings.Repeat("x", 65)}
	for _, s := range ok {
		if err := ValidateWireAddress(s); err != nil {
			t.Errorf("%q: %v", s, err)
		}
	}
	bad := []string{"cc:foo", "tmux:mt0", "Purdex:x", "purdex-tester:", "purdex-tester:a b", "a1:" + strings.Repeat("x", 66), "_k3:x", "purdex/x:y"}
	for _, s := range bad {
		err := ValidateWireAddress(s)
		if !errors.Is(err, ErrAddressInvalid) || ValidationCode(err) != ErrBadAddress {
			t.Errorf("%q: %v (code %s)", s, err, ValidationCode(err))
		}
	}
}

func TestDeliverRequest_Validate_Address(t *testing.T) {
	req := validDeliverRequest() // existing helper in wire_test.go, or build one
	req.From.Address = "cc:foo"
	if err := req.Validate(); ValidationCode(err) != ErrBadAddress {
		t.Errorf("got %v", err)
	}
	req.From.Address, req.From.AddressRev = "purdex-tester:x", -1
	if err := req.Validate(); err == nil {
		t.Error("negative address_rev accepted")
	}
}
```

- [ ] **Step 2: Run** — FAIL.

- [ ] **Step 3: Implement**

```go
var ErrAddressInvalid = errors.New("address invalid")

// ValidateWireAddress checks from.address (spec §3.5): "" is a v1 sender;
// otherwise head is a user label or a default label and rest, when
// present, matches the suffix wire grammar. Reserved heads never pass.
func ValidateWireAddress(s string) error {
	if s == "" {
		return nil
	}
	head, rest := SplitSession(s)
	if !IsDefaultLabel(head) {
		if err := ValidateUserLabel(head); err != nil {
			return fmt.Errorf("%w: head: %w", ErrAddressInvalid, err)
		}
	}
	if strings.Contains(s, ":") && !ValidSuffix(rest) {
		return fmt.Errorf("%w: suffix must match %s", ErrAddressInvalid, suffixWirePattern)
	}
	return nil
}
```

In `ValidationCode`: `case errors.Is(err, ErrAddressInvalid): return ErrBadAddress`. In `Validate`, after the label checks: `if err := ValidateWireAddress(r.From.Address); err != nil { return err }` and `if r.From.AddressRev < 0 { return errors.New("from.address_rev must be >= 0") }`. `PeerRecord.WireAddress()` in `record.go`.

- [ ] **Step 4: Run** — `go test -race -count=1 ./internal/peers/...` ⇒ PASS.

- [ ] **Step 5: Commit** — `git commit -m "feat(peers): from.address / address_rev on the wire, validated syntactically"`

---

### Task 12: `ccuds.RewriteRegistryName`

**Files:**
- Modify: `internal/peers/ccuds/registry_write.go`
- Test: `internal/peers/ccuds/registry_write_test.go`

**Interfaces:**
- Produces: `func RewriteRegistryName(dir string, pid int, name string, nameSince int64) error` — reads `<dir>/<pid>.json` via `peers.ReadRegistryCandidate`, decodes into `map[string]json.RawMessage` (every other field's bytes pass through untouched — `map[string]any` would round-trip large integers through `float64`), replaces only `name` and `nameSince`, writes `<dir>/.<pid>.json.tmp` (0644, `O_EXCL`), fsync, `os.Rename` over `<pid>.json`. Any failure removes the temp file and returns the error; the original file is untouched.

- [ ] **Step 1: Failing test**

```go
func TestRewriteRegistryName(t *testing.T) {
	dir := t.TempDir()
	created, err := WriteRegistry(dir, RegistryEntry{PID: 4242, SessionID: "s", Name: "a/old", ProcStart: "Sun Sep 13 18:57:56 2026", Inbox: "/tmp/x.sock", Version: "2.1.270"}, "tok")
	if err != nil {
		t.Fatal(err)
	}
	// Inject an unknown field with an integer above 2^53 so a float64
	// round-trip would be caught, then rewrite.
	before, _ := os.ReadFile(created[0])
	before = []byte(strings.Replace(string(before), `"pid":4242,`, `"pid":4242,"bigUnknown":9007199254740993,`, 1))
	if err := os.WriteFile(created[0], before, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := RewriteRegistryName(dir, 4242, "a/new:x", 1700000000000); err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(created[0])
	var b, a map[string]json.RawMessage
	json.Unmarshal(before, &b)
	json.Unmarshal(after, &a)
	if string(a["name"]) != `"a/new:x"` || string(a["nameSince"]) != `1700000000000` {
		t.Errorf("after = %s", after)
	}
	for k, v := range b {
		if k == "name" || k == "nameSince" {
			continue
		}
		if string(a[k]) != string(v) {
			t.Errorf("field %s changed byte-wise: %s → %s", k, v, a[k])
		}
	}
	if string(a["bigUnknown"]) != "9007199254740993" {
		t.Errorf("large integer damaged: %s", a["bigUnknown"])
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 2 { // json + key, no temp left
		t.Errorf("dir has %d entries", len(entries))
	}
	// Missing file: error, nothing created.
	if err := RewriteRegistryName(dir, 9999, "n", 1); err == nil {
		t.Error("expected error for a missing file")
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 2 {
		t.Errorf("temp file leaked")
	}
}
```

- [ ] **Step 2: Run** — FAIL. **Step 3: Implement** as described (reuse `writeExclusive`'s open flags for the temp; `filepath.Join(dir, "."+strconv.Itoa(pid)+".json.tmp")`). **Step 4: Run** ⇒ PASS.

- [ ] **Step 5: Commit** — `git commit -m "feat(ccuds): RewriteRegistryName — atomic in-place rename of a helper's registry file"`

---

### Task 13: Helper renames, wrapper from-name, address on send/reply

**Files:**
- Modify: `internal/module/peers/helpers.go`, `internal/module/peers/deliver.go`, `internal/module/peers/send.go`, `internal/module/peers/reply.go`
- Test: `internal/module/peers/helpers_test.go`, `internal/module/peers/deliver_test.go`

**Interfaces:**
- Produces (helpers.go):
  ```go
  // helper gains:
  //   appliedRev int64 — the address_rev of the request whose address the
  //                      instance currently carries; -1 (revUnapplied) when
  //                      the instance was spawned by a v1 request and no v2
  //                      address has been applied yet.
  const revUnapplied int64 = -1

  // Acquire gains the admitting request's revision: rev is stored on the
  // instance at creation, under the same lock that admits it, so a spawn
  // whose waiter cancelled still carries the revision that named it.
  // A v1 request passes revUnapplied.
  func (m *helperManager) Acquire(waitCtx context.Context, key ipeers.OriginKey, name string, rev int64) (*helper, error)

  // ApplyAddress renames instance h to name when rev is newer than the
  // instance's applied revision (or when nothing v2 was ever applied);
  // returns the name to use for THIS request's wrapper. Runs entirely
  // under m.mu — the flip to stopping (Release) and the rename cannot
  // interleave, so a rename can never recreate a file cleanup unlinked —
  // and the file rewrite happens under the lock too (one small file; the
  // trade-off is recorded here: other origins' Acquire/Release/ProxyPIDs
  // wait for the rewrite).
  func (m *helperManager) ApplyAddress(h *helper, name string, rev int64) string
  // Name returns h.name under the lock.
  func (m *helperManager) Name(h *helper) string
  ```
  Rules (spec §3.5, plus the plan-review amendments recorded in the P4b PR):
  1. `m.helpers[h.key] != h || h.state != helperReady` ⇒ return `h.name`, no change.
  2. `h.appliedRev != revUnapplied && rev <= h.appliedRev` ⇒ return `h.name`.
  3. `prev := h.appliedRev; h.appliedRev = rev` (advance **even when the name is equal** — the A→B→A guard).
  4. If `name != h.name`: `ccuds.RewriteRegistryName(m.registryDir, h.pid, name, m.now().UnixMilli())`; on error `m.log(...rename...)`, `h.appliedRev = prev`, return `h.name`; on success `h.name = name`.
  5. Return `h.name`.
- `send.go`: `wireFromRecord` fills `Address: rec.WireAddress(), AddressRev: rec.LabelRev`.
- `deliver.go` step 9/10:
  ```go
  spawnName, rev := principal.Alias+"/"+req.From.SessionName, revUnapplied // v1 sender
  if req.From.Address != "" {
      spawnName, rev = principal.Alias+"/"+req.From.Address, req.From.AddressRev
  }
  h, err := m.helpers.Acquire(waitCtx, req.From.Key(), spawnName, rev)
  …
  name := m.helpers.Name(h)
  if req.From.Address != "" {
      name = m.helpers.ApplyAddress(h, spawnName, rev)
  }
  // BuildFrame(... FromName: name ...)
  ```
  Never read `h.name` directly in `deliver.go`/`reply.go`; log lines use `m.helpers.Name(h)`.
- `reply.go`: `wireFromRecord(snap.hostID, replier, declared)` already carries the address (Task 11 field fill) — nothing else to do there beyond the `Name(h)` log change.

- [ ] **Step 1: Failing tests** (`helpers_test.go`, using the fake helper fixture that writes real registry files — check `proxyhelpertest` `Variant` Normal does; the manager's `registryDir` is the fixture's). Update every existing `Acquire(ctx, key, name)` call in the module's tests to pass `revUnapplied`.

```go
var applyKey = ipeers.OriginKey{HostID: "a:1", AgentSessionID: "sid-apply", PID: 1, ProcStart: e2eCCProcStart}

func registryName(t *testing.T, dir string, pid int) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, strconv.Itoa(pid)+".json"))
	if err != nil {
		return "<missing>"
	}
	var m map[string]json.RawMessage
	json.Unmarshal(data, &m)
	var name string
	json.Unmarshal(m["name"], &name)
	return name
}

func TestApplyAddress_RenameInPlaceAndMonotonic(t *testing.T) {
	f := newTestModuleWith(t, fixtureOpts{})
	h, err := f.m.helpers.Acquire(context.Background(), applyKey, "a/purdex-x:s1", 10)
	if err != nil {
		t.Fatal(err)
	}
	sock, pid := h.sock, h.pid
	if h.appliedRev != 10 {
		t.Fatalf("appliedRev after spawn = %d, want 10 (stored at admission)", h.appliedRev)
	}
	// Same rev again: no-op.
	if got := f.m.helpers.ApplyAddress(h, "a/purdex-x:s1", 10); got != "a/purdex-x:s1" {
		t.Errorf("got %q", got)
	}
	// Newer rev, new name: rewritten in place, same socket and pid.
	if got := f.m.helpers.ApplyAddress(h, "a/purdex-y:s1", 20); got != "a/purdex-y:s1" || registryName(t, f.registryDir, pid) != "a/purdex-y:s1" {
		t.Errorf("rename: got %q file %q", got, registryName(t, f.registryDir, pid))
	}
	if h.sock != sock || h.pid != pid {
		t.Error("instance changed")
	}
	// Older rev: ignored.
	if got := f.m.helpers.ApplyAddress(h, "a/purdex-z:s1", 15); got != "a/purdex-y:s1" {
		t.Errorf("older rev applied: %q", got)
	}
	// A→B→A: (A,30) same name advances rev; late (B,25) must not win.
	f.m.helpers.ApplyAddress(h, "a/purdex-y:s1", 30)
	if got := f.m.helpers.ApplyAddress(h, "a/purdex-b:s1", 25); got != "a/purdex-y:s1" {
		t.Errorf("A→B→A hole: %q", got)
	}
	// Released ⇒ skipped, and the file cleanup is not undone.
	f.m.helpers.Release(h, "test")
	if got := f.m.helpers.ApplyAddress(h, "a/purdex-q:s1", 99); got != "a/purdex-y:s1" {
		t.Errorf("rename on a released instance: %q", got)
	}
	if registryName(t, f.registryDir, pid) != "<missing>" {
		t.Error("rename after release recreated the registry file")
	}
}

func TestApplyAddress_LegacySpawnTakesFirstV2AddressEvenAtRevZero(t *testing.T) {
	f := newTestModuleWith(t, fixtureOpts{})
	h, _ := f.m.helpers.Acquire(context.Background(), applyKey, "a/mt1", revUnapplied)
	// A v2 sender whose session has never had a label row (rev 0) must still
	// be able to name the helper once.
	if got := f.m.helpers.ApplyAddress(h, "a/_k3x9qz:mt1-n", 0); got != "a/_k3x9qz:mt1-n" {
		t.Errorf("first v2 address at rev 0 not applied: %q", got)
	}
	// From then on the monotonic rule holds: rev 0 again is ignored.
	if got := f.m.helpers.ApplyAddress(h, "a/other:x", 0); got != "a/_k3x9qz:mt1-n" {
		t.Errorf("second rev-0 request applied: %q", got)
	}
}

func TestApplyAddress_SpawnKeepsRevWhenWaiterCancels(t *testing.T) {
	// The waiter that admitted the spawn (rev 10, name X) leaves before the
	// helper is ready; a later request at rev 5 must not rename it.
	f := newTestModuleWith(t, fixtureOpts{variant: proxyhelpertest.Slow /* or whatever variant delays ready; read proxyhelpertest/fake.go */})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := f.m.helpers.Acquire(ctx, applyKey, "a/x:s", 10); err == nil {
		t.Fatal("expected the cancelled waiter to fail")
	}
	h, err := f.m.helpers.Acquire(context.Background(), applyKey, "a/y:s", 5) // joins the in-flight spawn
	if err != nil {
		t.Fatal(err)
	}
	if got := f.m.helpers.ApplyAddress(h, "a/y:s", 5); got != "a/x:s" {
		t.Errorf("rev 5 renamed a rev-10 instance: %q", got)
	}
}

func TestApplyAddress_RewriteFailureRollsBack(t *testing.T) {
	f := newTestModuleWith(t, fixtureOpts{})
	h, _ := f.m.helpers.Acquire(context.Background(), applyKey, "a/x:s", 1)
	os.Remove(filepath.Join(f.registryDir, strconv.Itoa(h.pid)+".json")) // make the rewrite fail
	if got := f.m.helpers.ApplyAddress(h, "a/y:s", 5); got != "a/x:s" {
		t.Errorf("got %q", got)
	}
	if h.appliedRev != 1 {
		t.Errorf("appliedRev = %d after a failed rewrite, want 1", h.appliedRev)
	}
	if !f.logs.contains("rename") {
		t.Error("no log line for the failed rewrite")
	}
}
```

`deliver_test.go` (read `newDeliverEnv`/`envOpts` first; each case sends a real `/deliver` and reads the frame from the env's fake inbox):
- (a) `From.Address: "purdex-tester:x", AddressRev: 3` ⇒ wrapper `from-name="a/purdex-tester:x"`, helper registry file named so; a second request with `AddressRev: 4, Address: "purdex-tester-2:x"` renames (same socket path in the wrapper's `from`); a third with `AddressRev: 2` **and a fresh msg_id** does not rename (dedup must not be what stops it) and is still `200 delivered` with the frame received.
- (b) a request without `Address` (v1) names the helper `a/<session_name>`; a later one with `Address: "_k3x9qz:mt1-n", AddressRev: 0` renames it (legacy ⇒ first v2 address).
- (c) `Address: "cc:foo"` ⇒ `400 bad_address`, unaudited.
- (d) rewrite failure during delivery: remove the helper's registry file between two requests; the second request is `200 delivered`, the frame arrives, the wrapper carries the **old** name, the log has the rename failure, and the instance's `appliedRev` is unchanged.

- [ ] **Step 2: Run** — FAIL. **Step 3: Implement** per the interface block. **Step 4: Run** the module package ⇒ PASS (update e2e step assertions for `from-name` to the v2 form: `a/<label>:<suffix>` of the origin — `TestE2E_TwoDaemons` currently expects `"a/mt1"`; it becomes `"a/"+ipeers.DefaultLabel(e2eOriginSID)+":mt1-"+e2eOriginName`).

- [ ] **Step 5: Commit** — `git commit -m "feat(peers): helper names follow the address (in-place rewrite, rev-gated); from-name v2"`

---

### Task 14: P4b e2e, spec pointers, PR

- [ ] **Step 1**: Extend `TestE2E_Labels`: after the target claims `purdex-tester`, A's send arrives with `from-name = "a/<A origin address>"`; the target's native reply reaches A and A's helper for the replier is named `b/purdex-tester:foo-<name>`; the target re-claims `purdex-tester-2` and replies again ⇒ the **same** helper on A (same socket path) is now named `b/purdex-tester-2:…` and its registry file says so. Then the stale-revision guard, with dedup ruled out: POST to A's `/deliver` directly (bearer = B's outbound token for A) a request carrying the **old** `from.address` (`purdex-tester:…`) and the **old** `address_rev`, but a **fresh** `msg_id` ⇒ `200 delivered`, the origin inbox receives the frame, and the helper's name and registry file still say `purdex-tester-2`. Also the reverse-direction limit (spec §3.5 Freshness): after A's origin claims a label, B's helper for A keeps its old name until A sends again — assert it is unchanged after B's reply, then changed after A's next send.
- [ ] **Step 2**: In `docs/specs/2026-09-13-peer-bridge-spec.md` add under §4.1, §4.2, §4.4, §4.5, §4.8 one line each: `> Amended by 2026-09-14-peer-address-v2-spec.md (labels; see its §3.x).`
- [ ] **Step 3**: `go test -race -count=1 ./... && go vet ./... && make build` ⇒ PASS.
- [ ] **Step 4**: Commit `test(peers): P4b e2e — from-name v2 and helper rename; docs: spec pointers`, push, `gh pr create` titled "Peer Address v2 (P4b): from.address on the wire, helper renames" with the same footer. Then review rounds, merge, bump, deploy both hosts, and run spec §5 acceptance (P4b column).

---

## Self-review (done while writing)

- **Spec coverage**: §3.1 → T1; §3.2 → T6; §3.3 (diagnosis, store, occupancy, claim matrix, release, inventory read of labels) → T2/T3/T5/T7; §3.4 (entry rows, fields, daemon_version) → T4/T5; §3.5 → T11/T12/T13; §3.6 (routes, errors, CLI, `pdx peers`) → T7/T8/T9; §3.7 → T10; §3.9 → no code (docs in PR body); §4 tests list → spread across tasks; the R2-1 named test → T10 step 5. `LabelRev` on `PeerRecord` is a plan-level addition the spec's §3.5 needs (`address_rev` must come from the record); note it in the P4a PR description as a §3.4 field addition.
- **Placeholders**: the `newLabelFixture`, `fakeDaemon`, `envWith`, `validRegistryJSON` helpers are named but their bodies are "model on the existing helper X" — acceptable because each names the file and pattern to copy; implementers must read those files first.
- **Type consistency**: `ReadRegistryDiag` returns `(entries, Diagnosis, error)` everywhere; `Resolve(records, session, partial)` everywhere; `LabelStore` methods match `store.PeerLabelStore`; `ApplyAddress(h, name, rev) string` in T13 tests and interface; error string constants `ErrLabelTaken` etc. live in `wire.go` while the sentinel errors `ErrLabelInvalid`/`ErrLabelReserved` live in `label.go` — the wire string constants are therefore named `ErrCodeLabelInvalid` / `ErrCodeLabelReserved` (plus `ErrLabelTaken`, `ErrStoreUnavailable`) so they never collide with the sentinels; T7/T8 already use those names.

## Plan review disposition (codex `task-mu1dulow-tnozhu`, plan v1 → v2)

| # | Sev | Finding | Disposition |
|---|---|---|---|
| 1 | Blocker | T6/T7 test code did not compile (`labelRecord` params, `decodeRecord(t, f.claim(...))`) | fixed — signatures and two-step calls |
| 2 | Major | fixture builds `&Module{}`, `New(nil)` callers unlisted | fixed — T5 lists `module_test.go:187/1501/1518`, `main.go:263` |
| 3 | Major | entry rows change existing partial-inventory tests; migration unassigned | fixed — T4 lists every test to migrate; `not_ready` guards re-expressed in T5 with unknown registry files |
| 4 | Major | registry unknown could still yield `target_gone` / `replier_unknown` | fixed — T5: unknown files ⇒ `not_ready` even with a candidate; tests added |
| 5 | Major | self responses rebuilt from a second inventory read | fixed — T7 builds every response with `EntryRecord` from the validated entry + label row; suffix derived from the entry's tmux field (T4) so listing and whoami agree |
| 6 | Major | `/send` `not_ready` lacked `partial: true` | fixed — T6 writes the body directly; `APIError.Partial` moved to T6 |
| 7 | Major | spawn revision not stored atomically | fixed — `Acquire(…, name, rev)` stores `appliedRev` at admission (T13) |
| 8 | Major | legacy helper vs v2 default label rev 0 | fixed — `revUnapplied` (-1): first v2 address always applies (T13); spec §3.5 amended |
| 9 | Major | race / failure delivery tests missing | fixed — T13: release-then-apply asserts no file recreation; deliver_test (d) asserts frame delivered on rewrite failure; all under `m.mu` so ordering tests are the race tests |
| 10 | Major | replay test defeated by dedup | fixed — T13 (a) and T14 use a fresh `msg_id` with the old `address_rev` |
| 11 | Minor | `labelMu` held across inventory build | fixed — T7 never calls `localEnvelope` under the lock; encode after unlock |
| 12 | Minor | R2-1 scenario not actually built | fixed — T10 step 5 claims `foo` on a Desktop holder first, asserts shadowing, then the unknown |
| 13 | Minor | EPERM test not deterministic | fixed — `killProbe` seam; `MaxRegistryPID` |
| 14 | Minor | golden vectors not frozen | fixed — four fixed vectors incl. a `_0…` padding case |
| 15 | Minor | spec ambiguity: which unknowns mark inventory partial | fixed — spec §3.3 amended to "unknown files whose pid is alive" everywhere; T5 uses `BlockingUnknown()` |
| 16 | Minor | `map[string]any` damages large integers | fixed — `map[string]json.RawMessage` (T12) |
| 17 | Minor | output strings / version placement inconsistent | fixed — `released: <address>`; trailer recorded as a spec §3.6 delta |
| 18 | Minor | T11 Files, T13 `someKey`, T8 empty tests | fixed |
| O | — | omissions: write-failure fakes, `--json`, `not_ready.skipped` rendering, dispatcher wiring, PID range, per-task migration responsibility, integration tests not forced red | all added (T5 `writeFailingLabels`, T7 store-failure test, T8 tests, T2 `MaxRegistryPID`, T4 migration list, T10/T14 wording) |
