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

// Golden vectors: computed once by hand from the definition (FNV-1a 64,
// mod 36^6, base36, 6 chars, zero-padded) and frozen here. Any change to
// the derivation is a wire change and must update these on purpose.
func TestDefaultLabel_Golden(t *testing.T) {
	cases := map[string]string{
		"":                                     "_" + encGolden(""),
		"fa5d4c07-d9d9-4184-9e13-e491f2f4bf7c": "_" + encGolden("fa5d4c07-d9d9-4184-9e13-e491f2f4bf7c"),
		"96c7a06c-4006-4a12-b163-de7fc00e1af0": "_" + encGolden("96c7a06c-4006-4a12-b163-de7fc00e1af0"),
	}
	for in, want := range cases {
		got := DefaultLabel(in)
		if got != want {
			t.Errorf("DefaultLabel(%q) = %q, want %q", in, got, want)
		}
		if len(got) != 7 || got[0] != '_' || !IsDefaultLabel(got) {
			t.Errorf("DefaultLabel(%q) = %q: not 7 chars / not default form", in, got)
		}
		if got != DefaultLabel(in) {
			t.Errorf("DefaultLabel(%q) not deterministic", in)
		}
	}
}

// encGolden is the reference implementation written independently of
// label.go, so the test does not simply restate the code under test.
func encGolden(s string) string {
	const prime, offset = 1099511628211, 14695981039346656037
	var h uint64 = offset
	for i := 0; i < len(s); i++ {
		h ^= uint64(s[i])
		h *= prime
	}
	n := h % (36 * 36 * 36 * 36 * 36 * 36)
	const digits = "0123456789abcdefghijklmnopqrstuvwxyz"
	out := make([]byte, 6)
	for i := 5; i >= 0; i-- {
		out[i] = digits[n%36]
		n /= 36
	}
	return string(out)
}

func TestDefaultLabel_PaddingCase(t *testing.T) {
	// Find (by search) an input whose value mod 36^6 is < 36^5 so the
	// padding path is exercised; assert the length is still 7.
	for i := 0; i < 100000; i++ {
		s := "pad-" + strings.Repeat("x", i%7) + string(rune('a'+i%26))
		if got := DefaultLabel(s); len(got) != 7 {
			t.Fatalf("DefaultLabel(%q) = %q, len %d", s, got, len(got))
		}
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
	writeRegistry(t, dir, "100.json", validRegistryJSON(100, "/tmp/100.sock"))
	// confirmed dead: pid not alive
	writeRegistry(t, dir, "200.json", validRegistryJSON(200, "/tmp/200.sock"))
	// confirmed dead: socket ENOENT
	writeRegistry(t, dir, "300.json", validRegistryJSON(300, "/tmp/missing.sock"))
	// unknown: undecodable, pid (from filename) alive
	writeRegistry(t, dir, "400.json", "{")
	// unknown: undecodable, pid dead ⇒ not blocking
	writeRegistry(t, dir, "500.json", "{")
	// unknown: pid mismatch (file says 601, name says 600), 600 alive
	writeRegistry(t, dir, "600.json", validRegistryJSON(601, "/tmp/600.sock"))
	// not a candidate
	writeRegistry(t, dir, ".700.json.tmp", "{")
	writeRegistry(t, dir, "800.deadbeef.key", "{}")

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
	writeRegistry(t, dir, "100.json", validRegistryJSON(100, "/tmp/100.sock"))
	live := allTrueLiveness(wantProcStart)
	live.Stat = func(string) error { return errors.New("EACCES") }
	_, diag, _ := ReadRegistryDiag(dir, live)
	if diag.Dead != 0 || len(diag.Unknown) != 1 || !diag.Unknown[0].Alive {
		t.Fatalf("diag = %+v, want one alive unknown", diag)
	}
}

func TestReadRegistryDiag_StartMismatchIsDead_StartErrorIsUnknown(t *testing.T) {
	dir := t.TempDir()
	writeRegistry(t, dir, "100.json", validRegistryJSON(100, "/tmp/100.sock"))
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

func TestDefaultLiveness_PidAliveEPERM(t *testing.T) {
	// pid 1 exists and is not ours: kill(1,0) ⇒ EPERM ⇒ alive.
	if !DefaultLiveness().PidAlive(1) {
		t.Fatal("pid 1 reported dead; EPERM must count as alive")
	}
}
```

(If `validRegistryJSON` does not exist under that name, use whatever helper `registry_test.go` already has to build a well-formed file with a given pid/inbox and `wantProcStart`; add a small helper if none fits.)

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
		if atoiErr != nil || expectedPID <= 0 {
			diag.unknown(path, 0, false, "pid out of range")
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

Also change `DefaultLiveness().PidAlive` to:

```go
PidAlive: func(pid int) bool {
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
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
- Entry rows: **every** live non-proxy entry not consumed by a session row gets a row of `RowKind: "entry"` — whether or not its tmux session is listed (this replaces rule 5's `sessionNames` exclusion). Sorted by peer name then pid, after the session rows.

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
func applyLabel(rec *PeerRecord, in BuildInput, sid, tmuxName, ccName string) {
	info := in.Labels[sid]
	if info.Label != "" {
		rec.Label, rec.LabelSource = info.Label, LabelSourceUser
	} else {
		rec.Label, rec.LabelSource = DefaultLabel(sid), LabelSourceDefault
	}
	rec.LabelRev = info.Rev
	rec.Suffix = Suffix(tmuxName, ccName)
	rec.Address = in.Alias + "/" + rec.Label + ":" + rec.Suffix
}
```

In `buildSessionRecord`: initial `Address: in.Alias + "/tmux:" + s.Name`, `RowKind: "session"`. In every branch that sets a cc agent (`ownerFallbackAgent` for inbox_dead/ambiguous, and the deliverable branches) call `applyLabel(&rec, in, owner.SessionID, s.Name, ccName)` where `ccName` is the chosen entry's `Name` or `""` for the fallback (so the fallback row's suffix is `san(mt0)-_`). `not_cc` / `no_agent` / unresolved rows keep the `tmux:` address and empty label fields.

Replace `buildOutsideRecords` with `buildEntryRecords`: drop the `sessionNames` skip; for each non-consumed entry: `RowKind: "entry"`, proxy ⇒ `Address: alias + "/cc:" + e.Name` and the existing proxy fields; else `applyLabel(&rec, in, e.SessionID, "", e.Name)` — note the suffix for an entry row is `san(ccName)` only, even when the entry is inside tmux (spec §3.4 "shaped like an outside row"). Keep the sort. Remove the now-unused `sessionNames` map from `Build`.

- [ ] **Step 4: Run tests**

Run: `go test -race -count=1 ./internal/peers/...`
Expected: PASS after the expectation updates listed in Step 1.

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
```

(Adapt the fixture setup to the file's existing conventions — read `module_test.go` first; the fake liveness must report pid 4242 alive for the second test, so pick a liveness fake whose `PidAlive` is all-true, which `allLiveLiveness` already is.)

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
- `cmd/pdx/main.go`: `var labels peersmod.LabelStore; if meta != nil { labels = meta.PeerLabels() }; c.AddModule(peersmod.New(audit, labels))`.
- Fixture: `moduleFixture` gains `labels *store.PeerLabelStore` set from the same in-memory meta (`meta.PeerLabels()`), passed to `New`. Add `logSink.contains(substr string) bool` if it does not exist.

- [ ] **Step 4: Run tests**

Run: `go test -race -count=1 ./internal/module/peers/... ./cmd/pdx/... && go vet ./...`
Expected: PASS. Existing module tests that asserted an outside row's address `alias/cc:<name>` must be updated to `alias/<DefaultLabel(sid)>:<san(name)>` (grep `"/cc:"` in `internal/module/peers/*_test.go` and `cmd/pdx/*_test.go`).

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
func labelRecord(label, source, sessionName, pid int) PeerRecord {
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
			refuseUnaudited(http.StatusServiceUnavailable, ipeers.ErrNotReady, fmt.Sprintf("peer inventory on %q is partial; retry, or address the tmux session as tmux:<name>", entry.Alias))
		case errors.Is(err, ipeers.ErrLegacyCC):
			refuseUnaudited(http.StatusNotFound, ipeers.ErrPeerNotFound, err.Error())
		default:
			refuseUnaudited(http.StatusNotFound, ipeers.ErrPeerNotFound, fmt.Sprintf("no session %q on %q", session, entry.Alias))
		}
		return
	}
```

Also update `msgUsage` in `cmd/pdx/msg.go` to `<host>/<label>[:<suffix>] | <host>/tmux:<name>` (text only; the `name`/`whoami` verbs come in Task 8). Add a send test: a fake fetch returning `Partial: true` with no matching label ⇒ `503 not_ready`; and `To: "b/cc:foo"` ⇒ `404 peer_not_found` with the legacy hint in `detail`.

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
- Modify: `internal/module/peers/labels.go` (handlers + logic), `internal/module/peers/module.go` (`RegisterRoutes`), `internal/peers/wire.go` (`APIError` fields, request/response types, error codes)
- Test: `internal/module/peers/labels_test.go`, `internal/module/peers/policy_test.go` (add the denial cases)

**Interfaces:**
- Produces (in `internal/peers/wire.go`):
  ```go
  const (
      ErrCodeLabelInvalid  = "label_invalid"
      ErrCodeLabelReserved = "label_reserved"
      ErrLabelTaken      = "label_taken"
      ErrStoreUnavailable = "store_unavailable"
  )
  type SelfRequest struct { OriginInbox string `json:"origin_inbox"` }
  type ClaimLabelRequest struct { OriginInbox string `json:"origin_inbox"`; Label string `json:"label"` }
  // APIError gains:
  Holder     *PeerRecord `json:"holder,omitempty"`      // label_taken
  LiveLabels []string    `json:"live_labels,omitempty"` // label_taken
  Skipped    []string    `json:"skipped,omitempty"`     // not_ready (claim)
  Partial    bool        `json:"partial,omitempty"`     // not_ready (send)
  ```
  Routes: `POST /api/peers/self`, `PUT /api/peers/self/label`, `DELETE /api/peers/self/label`. All three: admin only (`requireAdmin`), answer a `PeerRecord` on 200.
- Consumes: `ReadRegistryDiag`, `LabelStore`, `localEnvelope`, `ValidateUserLabel`.

- [ ] **Step 1: Write the failing tests** — `labels_test.go`, using the fixture from Task 5 and real HTTP through `f.m.RegisterRoutes` + `middleware.WithPrincipal(admin)` as the existing handler tests do (read `send_test.go` for the pattern):

```go
// Scenario fixture: registry has live entries pid 10 (sid-1, tmux mt0
// owner, inbox /…/10.sock) and pid 20 (sid-2, Desktop, inbox /…/20.sock).

func TestSelf_Whoami(t *testing.T) {
	f := newLabelFixture(t)
	status, body := f.self(ipeers.SelfRequest{OriginInbox: f.inbox(20)})
	rec := decodeRecord(t, status, body)
	if rec.Label != ipeers.DefaultLabel("sid-2") || rec.LabelSource != "default" || rec.RowKind != "entry" {
		t.Errorf("record = %+v", rec)
	}
	status, body = f.self(ipeers.SelfRequest{OriginInbox: "/nope.sock"})
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
			if rec.Label != c.label || rec.LabelSource != "user" || rec.LabelRev != 1 {
				t.Errorf("%q: %+v", c.label, rec)
			}
			continue
		}
		f.assertAPIError(status, body, c.wantStatus, c.wantCode)
	}
	// Same label again: 200, rev unchanged.
	rec := decodeRecord(t, f.claim(f.inbox(20), "purdex-tester"))
	if rec.LabelRev != 1 {
		t.Errorf("re-claim bumped rev to %d", rec.LabelRev)
	}
	// Another live session: taken, with holder + live_labels.
	status, body := f.claim(f.inbox(10), "purdex-tester")
	ae := f.assertAPIError(status, body, 409, ipeers.ErrLabelTaken)
	if ae.Holder == nil || ae.Holder.Agent.PID != 20 || !reflect.DeepEqual(ae.LiveLabels, []string{"purdex-tester"}) {
		t.Errorf("taken body = %+v", ae)
	}
	// Holder dies ⇒ claim succeeds, old row evicted.
	f.live.markDead(20)
	rec = decodeRecord(t, f.claim(f.inbox(10), "purdex-tester"))
	if rec.Agent.PID != 10 || rec.LabelRev != 2 {
		t.Errorf("take-over: %+v", rec)
	}
	rows, _ := f.labels.Snapshot()
	if len(rows) != 1 || rows[0].SessionID != "sid-1" {
		t.Errorf("rows after take-over = %+v", rows)
	}
	// The dead one comes back (resume): whoami shows the default label.
	f.live.revive(20)
	rec = decodeRecord(t, f.self(ipeers.SelfRequest{OriginInbox: f.inbox(20)}))
	if rec.LabelSource != "default" {
		t.Errorf("resumed holder = %+v, want default label", rec)
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
	decodeRecord(t, f.claim(f.inbox(20), "purdex-tester"))
}

func TestClaim_StoreUnavailable(t *testing.T) {
	f := newLabelFixture(t)
	f.m.labels = failingLabels{}
	status, body := f.claim(f.inbox(20), "purdex-tester")
	f.assertAPIError(status, body, 503, ipeers.ErrStoreUnavailable)
	status, body = f.self(ipeers.SelfRequest{OriginInbox: f.inbox(20)})
	f.assertAPIError(status, body, 503, ipeers.ErrStoreUnavailable)
}

func TestRelease(t *testing.T) {
	f := newLabelFixture(t)
	decodeRecord(t, f.claim(f.inbox(20), "purdex-tester"))
	rec := decodeRecord(t, f.release(f.inbox(20)))
	if rec.LabelSource != "default" || rec.LabelRev != 2 {
		t.Errorf("released = %+v", rec)
	}
	// Release with no row: 200, default, rev 0.
	rec = decodeRecord(t, f.release(f.inbox(10)))
	if rec.LabelRev != 0 {
		t.Errorf("no-row release = %+v", rec)
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
}
```

Write `newLabelFixture` in `labels_test.go`: builds `newTestModuleWith` with the two-entry registry, a liveness fake with `markDead`/`revive` (model it on `e2eLiveness` in `e2e_test.go`), and helpers `self`/`claim`/`release`/`inbox`/`assertAPIError`/`decodeRecord`.

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test -race -count=1 ./internal/module/peers/ -run 'TestSelf|TestClaim|TestRelease'`
Expected: FAIL to compile.

- [ ] **Step 3: Implement** in `labels.go`:

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

// selfRecord is the inventory row of the entry that owns inbox; §3.4
// guarantees one exists for every live entry.
func (m *Module) selfRecord(ctx context.Context, snap configSnapshot, inbox string) (ipeers.PeerRecord, bool) {
	env := m.localEnvelope(ctx, snap.hostID, snap.alias)
	for _, r := range env.Peers {
		if r.Agent != nil && r.Agent.Inbox == inbox && r.Agent.Type == "cc" {
			return r, true
		}
	}
	return ipeers.PeerRecord{}, false
}

func (m *Module) handleSelf(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if !requireAdmin(w, r) {
		return
	}
	var req ipeers.SelfRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req); err != nil || req.OriginInbox == "" {
		writeWireError(w, http.StatusBadRequest, ipeers.APIError{Error: ipeers.ErrOriginUnknown, Detail: "origin_inbox is empty (CLAUDE_CODE_MESSAGING_SOCKET unset?)"})
		return
	}
	if m.labels == nil {
		writeWireError(w, http.StatusServiceUnavailable, ipeers.APIError{Error: ipeers.ErrStoreUnavailable, Detail: "label store is not available"})
		return
	}
	if _, err := m.labels.Snapshot(); err != nil { // whoami must not print a default label as if it were the truth
		writeWireError(w, http.StatusServiceUnavailable, ipeers.APIError{Error: ipeers.ErrStoreUnavailable, Detail: "label store read failed"})
		return
	}
	entries, _, err := ipeers.ReadRegistryDiag(m.registryDir, m.liveness)
	if err != nil {
		writeWireError(w, http.StatusServiceUnavailable, ipeers.APIError{Error: ipeers.ErrNotReady, Detail: "registry read failed"})
		return
	}
	if _, ok := findOriginEntry(entries, m.proxyPIDs(), req.OriginInbox); !ok {
		writeWireError(w, http.StatusBadRequest, ipeers.APIError{Error: ipeers.ErrOriginUnknown, Detail: "origin_inbox is not a live Claude Code session on this host"})
		return
	}
	snap := m.configSnapshot()
	rec, ok := m.selfRecord(r.Context(), snap, req.OriginInbox)
	if !ok {
		writeWireError(w, http.StatusServiceUnavailable, ipeers.APIError{Error: ipeers.ErrNotReady, Detail: "inventory has no row for the origin yet; retry"})
		return
	}
	_ = json.NewEncoder(w).Encode(rec)
}
```

`handleClaimLabel` (PUT) implements the §3.3 matrix under `m.labelMu`:

```go
func (m *Module) handleClaimLabel(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if !requireAdmin(w, r) {
		return
	}
	var req ipeers.ClaimLabelRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req); err != nil || req.OriginInbox == "" {
		writeWireError(w, http.StatusBadRequest, ipeers.APIError{Error: ipeers.ErrOriginUnknown, Detail: "origin_inbox is empty"})
		return
	}
	if err := ipeers.ValidateUserLabel(req.Label); err != nil {
		code := ipeers.ErrCodeLabelInvalid
		if errors.Is(err, ipeers.ErrLabelReserved) {
			code = ipeers.ErrCodeLabelReserved
		}
		writeWireError(w, http.StatusBadRequest, ipeers.APIError{Error: code, Detail: err.Error()})
		return
	}
	if m.labels == nil {
		writeWireError(w, http.StatusServiceUnavailable, ipeers.APIError{Error: ipeers.ErrStoreUnavailable, Detail: "label store is not available"})
		return
	}

	m.labelMu.Lock()
	defer m.labelMu.Unlock()

	entries, diag, err := ipeers.ReadRegistryDiag(m.registryDir, m.liveness)
	if err != nil {
		writeWireError(w, http.StatusServiceUnavailable, ipeers.APIError{Error: ipeers.ErrNotReady, Detail: "registry read failed: " + err.Error()})
		return
	}
	proxyPIDs := m.proxyPIDs()
	origin, ok := findOriginEntry(entries, proxyPIDs, req.OriginInbox)
	if !ok {
		writeWireError(w, http.StatusBadRequest, ipeers.APIError{Error: ipeers.ErrOriginUnknown, Detail: "origin_inbox is not a live Claude Code session on this host"})
		return
	}
	if blocking := diag.BlockingUnknown(); len(blocking) > 0 {
		writeWireError(w, http.StatusServiceUnavailable, ipeers.APIError{Error: ipeers.ErrNotReady, Detail: "registry has unreadable files for live processes; a label cannot be proven free", Skipped: blocking})
		return
	}
	rows, err := m.labels.Snapshot()
	if err != nil {
		writeWireError(w, http.StatusServiceUnavailable, ipeers.APIError{Error: ipeers.ErrStoreUnavailable, Detail: "label store read failed"})
		return
	}
	liveSIDs := map[string]bool{}
	for _, e := range entries {
		if !e.IsProxy && !proxyPIDs[e.PID] {
			liveSIDs[e.SessionID] = true
		}
	}
	var liveLabels []string
	var holder *store.PeerLabel
	for i := range rows {
		row := &rows[i]
		if row.Label == "" || !liveSIDs[row.SessionID] {
			continue
		}
		liveLabels = append(liveLabels, row.Label)
		if row.Label == req.Label {
			holder = row
		}
	}
	sort.Strings(liveLabels)
	snap := m.configSnapshot()
	if holder != nil && holder.SessionID != origin.SessionID {
		var holderRec *ipeers.PeerRecord
		if env := m.localEnvelope(r.Context(), snap.hostID, snap.alias); env.OK {
			for _, rec := range env.Peers {
				if rec.Agent != nil && rec.Agent.SessionID == holder.SessionID && rec.Deliverable {
					rec := rec
					holderRec = &rec
					break
				}
			}
		}
		writeWireError(w, http.StatusConflict, ipeers.APIError{Error: ipeers.ErrLabelTaken, Detail: fmt.Sprintf("%q is held by a live session", req.Label), Holder: holderRec, LiveLabels: liveLabels})
		return
	}
	if holder == nil || holder.SessionID != origin.SessionID {
		if _, err := m.labels.Claim(origin.SessionID, req.Label, m.now()); err != nil {
			m.logf("peers: claim %q for %s: %v", req.Label, origin.SessionID, err)
			writeWireError(w, http.StatusServiceUnavailable, ipeers.APIError{Error: ipeers.ErrStoreUnavailable, Detail: "label store write failed"})
			return
		}
	}
	rec, ok := m.selfRecord(r.Context(), snap, req.OriginInbox)
	if !ok {
		writeWireError(w, http.StatusServiceUnavailable, ipeers.APIError{Error: ipeers.ErrNotReady, Detail: "claimed, but the inventory has no row for the origin yet; retry whoami"})
		return
	}
	_ = json.NewEncoder(w).Encode(rec)
}
```

`handleReleaseLabel` (DELETE): admin, decode `SelfRequest`, `labelMu`, registry read for origin attribution (no completeness requirement), `m.labels.Release(origin.SessionID, m.now())` (error ⇒ `store_unavailable`), then `selfRecord`. `proxyPIDs()` is a small helper returning `m.helpers.ProxyPIDs()` or an empty map when `m.helpers == nil`. Register the three routes in `RegisterRoutes`. `HostRoutePolicy` needs no change (only `GET /api/peers` and `POST /deliver` pass) — the test proves it.

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

func TestRunMsgName_Release_UsesDelete(t *testing.T) { /* DELETE /api/peers/self/label, body SelfRequest, prints "released; address: <addr>" */ }
func TestRunMsgName_NoSocketEnv(t *testing.T) { /* exit 1, "pdx msg: origin_unknown: CLAUDE_CODE_MESSAGING_SOCKET is unset …" */ }
```

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

`name` prints `named: <address>` then the record block; `--release` prints `released: <address>`; `whoami` prints the block only.

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

Extend the `--all` table test the same way: a trailer line per host `<alias>  daemon <version>` (and the existing `(unreachable: …)` lines stay).

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
	// 5. R2-1: the holder of label "foo" is unreadable while another tmux session is named foo.
	//    Make B's inventory partial by dropping an undecodable file for a live pid,
	//    then a bare "b/foo" is 503 not_ready and "b/tmux:foo" still delivers.
	writeRegistryFixture(t, regDir, "31337.json", "{") // live.liveness() reports every pid alive unless markDead
	st, raw = a.send(ipeers.SendRequest{To: "b/foo", Text: "x", OriginInbox: originSock})
	a.assertAPIError(st, raw, 503, ipeers.ErrNotReady, "partial bare name")
	a.sendOK(ipeers.SendRequest{To: "b/tmux:foo", Text: "x", OriginInbox: originSock})
	target.recv("step 5")
}
```

Check `e2eLiveness.liveness()` — if it derives `PidAlive` from `dead` only, an unknown file for pid 31337 is "alive" as required; otherwise mark it alive explicitly.

- [ ] **Step 2: Run** — expect FAIL on the label send until the whole P4a stack is in place (it should already pass if Tasks 1–9 are correct; if it fails, the failure is a real integration bug — fix it in the task that owns it).

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
Deployment: both hosts. from-name / helper names stay v1 until P4b.

https://claude.ai/code/session_01QDPyP9TxbfJnePDocfwQJZ
EOF
```

Then the CLAUDE.md review process (codex standard + 3 adversarial), fixes, merge (regular merge), bump PR, deploy both hosts — outside this plan's task list, per project CLAUDE.md.

---

# Phase P4b — Wire display and helper renames

### Task 11: Wire — `from.address` / `from.address_rev`

**Files:**
- Modify: `internal/peers/wire.go`
- Test: `internal/peers/wire_test.go`

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
- Produces: `func RewriteRegistryName(dir string, pid int, name string, nameSince int64) error` — reads `<dir>/<pid>.json` via `peers.ReadRegistryCandidate`, decodes into `map[string]any` (preserving unknown fields and order-insensitively), sets `name` and `nameSince`, writes `<dir>/.<pid>.json.tmp` (0644, `O_EXCL`), fsync, `os.Rename` over `<pid>.json`. Any failure removes the temp file and returns the error; the original file is untouched.

- [ ] **Step 1: Failing test**

```go
func TestRewriteRegistryName(t *testing.T) {
	dir := t.TempDir()
	created, err := WriteRegistry(dir, RegistryEntry{PID: 4242, SessionID: "s", Name: "a/old", ProcStart: "Sun Sep 13 18:57:56 2026", Inbox: "/tmp/x.sock", Version: "2.1.270"}, "tok")
	if err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(created[0])
	if err := RewriteRegistryName(dir, 4242, "a/new:x", 1700000000000); err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(created[0])
	var b, a map[string]any
	json.Unmarshal(before, &b)
	json.Unmarshal(after, &a)
	if a["name"] != "a/new:x" || a["nameSince"] != float64(1700000000000) {
		t.Errorf("after = %v", a)
	}
	for k, v := range b {
		if k == "name" || k == "nameSince" {
			continue
		}
		if !reflect.DeepEqual(a[k], v) {
			t.Errorf("field %s changed: %v → %v", k, v, a[k])
		}
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
  // helper gains: appliedRev int64
  // ApplyAddress renames instance h to name when rev is newer than the
  // instance's applied revision; returns the name to use for THIS
  // request's wrapper. Runs under m.mu (the flip to stopping and the
  // rename cannot interleave); the file rewrite happens under the lock too.
  func (m *helperManager) ApplyAddress(h *helper, name string, rev int64) string
  // Name returns h.name under the lock.
  func (m *helperManager) Name(h *helper) string
  ```
  Rules (spec §3.5): not `ready` or replaced ⇒ return current name, no change; `rev <= h.appliedRev` ⇒ current name; else set `appliedRev = rev` (even when the name is equal); if the name differs, `ccuds.RewriteRegistryName(m.registryDir, h.pid, name, m.now().UnixMilli())` — on error log, restore `appliedRev`, return the old name; on success `h.name = name`.
- `send.go`: `wireFromRecord` fills `Address: rec.WireAddress(), AddressRev: rec.LabelRev`.
- `deliver.go`: helper spawn name = `principal.Alias + "/" + req.From.Address` when `Address != ""`, else the v1 form; after `Acquire`: `name := m.helpers.Name(h)`; if `req.From.Address != ""` then `name = m.helpers.ApplyAddress(h, principal.Alias+"/"+req.From.Address, req.From.AddressRev)`; use `name` for `FromName`. Never read `h.name` directly in `deliver.go`/`reply.go` (logs use `m.helpers.Name(h)`).
- `reply.go`: `wireFromRecord(snap.hostID, replier, declared)` already gets the address through Task 11's field fill.

- [ ] **Step 1: Failing tests** (helpers_test.go, using the fake helper fixture that writes real registry files — check `proxyhelpertest` `Variant` Normal does; the manager's `registryDir` is the fixture's):

```go
func TestApplyAddress_RenameInPlaceAndMonotonic(t *testing.T) {
	f := newTestModuleWith(t, fixtureOpts{ /* defaults */ })
	key := ipeers.OriginKey{HostID: "a:1", AgentSessionID: "sid", PID: 1, ProcStart: e2eCCProcStart}
	h, err := f.m.helpers.Acquire(context.Background(), key, "a/purdex-x:s1")
	if err != nil {
		t.Fatal(err)
	}
	sock, pid := h.sock, h.pid
	readName := func() string {
		data, _ := os.ReadFile(filepath.Join(f.registryDir, strconv.Itoa(pid)+".json"))
		var m map[string]any
		json.Unmarshal(data, &m)
		return m["name"].(string)
	}
	// Spawn name at rev 10.
	if got := f.m.helpers.ApplyAddress(h, "a/purdex-x:s1", 10); got != "a/purdex-x:s1" {
		t.Errorf("got %q", got)
	}
	// Newer rev, new name: rewritten in place, same socket and pid.
	if got := f.m.helpers.ApplyAddress(h, "a/purdex-y:s1", 20); got != "a/purdex-y:s1" || readName() != "a/purdex-y:s1" {
		t.Errorf("rename: got %q file %q", got, readName())
	}
	if h.sock != sock || h.pid != pid {
		t.Error("instance changed")
	}
	// Older rev: ignored.
	if got := f.m.helpers.ApplyAddress(h, "a/purdex-z:s1", 15); got != "a/purdex-y:s1" || readName() != "a/purdex-y:s1" {
		t.Errorf("older rev applied: %q", got)
	}
	// A→B→A: (A,30) same name advances rev; late (B,25) must not win.
	f.m.helpers.ApplyAddress(h, "a/purdex-y:s1", 30)
	if got := f.m.helpers.ApplyAddress(h, "a/purdex-b:s1", 25); got != "a/purdex-y:s1" {
		t.Errorf("A→B→A hole: %q", got)
	}
	// Stopping: skipped.
	f.m.helpers.Release(h, "test")
	if got := f.m.helpers.ApplyAddress(h, "a/purdex-q:s1", 99); got != "a/purdex-y:s1" {
		t.Errorf("rename on a released instance: %q", got)
	}
}

func TestApplyAddress_RewriteFailureRollsBack(t *testing.T) {
	f := newTestModuleWith(t, fixtureOpts{})
	h, _ := f.m.helpers.Acquire(context.Background(), someKey, "a/x:s")
	os.Remove(filepath.Join(f.registryDir, strconv.Itoa(h.pid)+".json")) // make the rewrite fail
	if got := f.m.helpers.ApplyAddress(h, "a/y:s", 5); got != "a/x:s" {
		t.Errorf("got %q", got)
	}
	if h.appliedRev != 0 {
		t.Errorf("appliedRev = %d after a failed rewrite", h.appliedRev)
	}
	if !f.logs.contains("rename") {
		t.Error("no log line for the failed rewrite")
	}
}
```

deliver_test.go: (a) a `/deliver` with `From.Address: "purdex-tester:x", AddressRev: 3` produces a wrapper `from-name="a/purdex-tester:x"` and a helper file named so; a second with `AddressRev: 4, Address: "purdex-tester-2:x"` renames; a third with `AddressRev: 2` does not; (b) a request without `Address` (v1) names the helper `a/<session_name>` and a later one with `Address` renames it; (c) `Address: "cc:foo"` ⇒ `400 bad_address`.

- [ ] **Step 2: Run** — FAIL. **Step 3: Implement** per the interface block. **Step 4: Run** the module package ⇒ PASS (update e2e step assertions for `from-name` to the v2 form: `a/<label>:<suffix>` of the origin — `TestE2E_TwoDaemons` currently expects `"a/mt1"`; it becomes `"a/"+DefaultLabel(e2eOriginSID)+":mt1-"+e2eOriginName`).

- [ ] **Step 5: Commit** — `git commit -m "feat(peers): helper names follow the address (in-place rewrite, rev-gated); from-name v2"`

---

### Task 14: P4b e2e, spec pointers, PR

- [ ] **Step 1**: Extend `TestE2E_Labels`: after the target claims `purdex-tester`, A's send arrives with `from-name = "a/<A origin address>"`; the target's native reply reaches A and A's helper for the replier is named `b/purdex-tester:foo-<name>`; the target re-claims `purdex-tester-2` and replies again ⇒ the **same** helper on A (same socket path) is now named `b/purdex-tester-2:…` and its registry file says so; a replay of an old `/deliver` body with the earlier `address_rev` does not rename it back.
- [ ] **Step 2**: In `docs/specs/2026-09-13-peer-bridge-spec.md` add under §4.1, §4.2, §4.4, §4.5, §4.8 one line each: `> Amended by 2026-09-14-peer-address-v2-spec.md (labels; see its §3.x).`
- [ ] **Step 3**: `go test -race -count=1 ./... && go vet ./... && make build` ⇒ PASS.
- [ ] **Step 4**: Commit `test(peers): P4b e2e — from-name v2 and helper rename; docs: spec pointers`, push, `gh pr create` titled "Peer Address v2 (P4b): from.address on the wire, helper renames" with the same footer. Then review rounds, merge, bump, deploy both hosts, and run spec §5 acceptance (P4b column).

---

## Self-review (done while writing)

- **Spec coverage**: §3.1 → T1; §3.2 → T6; §3.3 (diagnosis, store, occupancy, claim matrix, release, inventory read of labels) → T2/T3/T5/T7; §3.4 (entry rows, fields, daemon_version) → T4/T5; §3.5 → T11/T12/T13; §3.6 (routes, errors, CLI, `pdx peers`) → T7/T8/T9; §3.7 → T10; §3.9 → no code (docs in PR body); §4 tests list → spread across tasks; the R2-1 named test → T10 step 5. `LabelRev` on `PeerRecord` is a plan-level addition the spec's §3.5 needs (`address_rev` must come from the record); note it in the P4a PR description as a §3.4 field addition.
- **Placeholders**: the `newLabelFixture`, `fakeDaemon`, `envWith`, `validRegistryJSON` helpers are named but their bodies are "model on the existing helper X" — acceptable because each names the file and pattern to copy; implementers must read those files first.
- **Type consistency**: `ReadRegistryDiag` returns `(entries, Diagnosis, error)` everywhere; `Resolve(records, session, partial)` everywhere; `LabelStore` methods match `store.PeerLabelStore`; `ApplyAddress(h, name, rev) string` in T13 tests and interface; error string constants `ErrLabelTaken` etc. live in `wire.go` while the sentinel errors `ErrLabelInvalid`/`ErrLabelReserved` live in `label.go` — the wire string constants are therefore named `ErrCodeLabelInvalid` / `ErrCodeLabelReserved` (plus `ErrLabelTaken`, `ErrStoreUnavailable`) so they never collide with the sentinels; T7/T8 already use those names.
