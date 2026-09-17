# Peer Address v4 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the address `mini-lab/_q34psn4f:aigora2-purdex-b0` with `mlab/purdex-b0 [q34psn]` — a readable everyday address backed by an exact ref — and make it portable across hosts.

**Architecture:** The registry name becomes the address head, but only when it passes a new `RoutableName` grammar; that grammar is what makes the name and ref namespaces disjoint, so neither can shadow the other. The ref (`_` + 6 base36 from the sessionId) shrinks from 8 and stops being the sole address. `label` becomes a free-text `title` that routes nothing. A host publishes its own alias so an address means the same thing on both machines.

**Tech Stack:** Go (net/http, modernc.org/sqlite), React 19 + Zustand 5 + Vitest, pnpm.

**Spec:** `docs/specs/2026-09-17-peer-address-v4-spec.md` — read it before Task 1. The plan implements it; where they disagree, the spec wins and the plan is the bug.

## Global Constraints

- **Ref grammar:** `^_[0-9a-z]{6}$` — `_` + exactly 6 base36 digits, zero-padded, from `FNV-1a-64(sessionId) mod 36^6`.
- **Routable-name grammar:** `^[a-z0-9][a-z0-9-]{1,63}$` **and not** `^[0-9a-z]{6}$`.
- **Title grammar:** ≤64 bytes, printable UTF-8, no control characters. No reserved words.
- **Build the daemon with `make build`**, never bare `go build` — the version is injected via ldflags and a bare build yields `version unknown`.
- **The repo root holds a version-controlled 2.7 MB `pdx` binary that `go build ./cmd/...` overwrites.** Every commit must use `git commit --only <explicit files>`. Never `git commit -am`.
- **Go tests:** `go test -race -count=1 ./internal/peers/...` (scope narrowed per task).
- **SPA:** `cd spa && npx vitest run`, `pnpm run lint`, `pnpm run build`.
- **Known flake, not yours:** `internal/module/agent`'s `TestConsumeSignals_GraceWindowDrop_RearmsAfterTeardown` reddens intermittently under parallel load and passes alone (#1092).
- **Every task is its own commit.** Parallel subagents in this worktree share one git index — `--only` with explicit paths is what keeps them from sweeping each other's files in.

---

## File Structure

| File | Responsibility | Task |
|---|---|---|
| `internal/peers/ref.go` (renamed from `label.go`) | ref derivation, `RoutableName`, title grammar, `SplitSession` | A1, A2, B1 |
| `internal/peers/record.go` | `PeerRecord`, `Build`, `applyIdentity` — the single writer of identity fields | A3 |
| `internal/peers/address.go` | `Resolve` and its tiers, `ErrNameMismatch`, `hasStaleVersionRows` | A4, A6 |
| `internal/peers/wire.go` | `WireFrom.Address`, `ValidateWireAddress` | A5 |
| `internal/peers/envelope.go` | `Envelope.Alias`, `HostResult.SelfAlias` | C1 |
| `cmd/pdx/peers.go` | table columns, display composition | A7 |
| `cmd/pdx/msg.go` | send input forms, `whoami` block | A8 |
| `internal/module/peers/hosts.go` | alias learning and adoption | C2 |
| `internal/module/peers/titles.go` (renamed from `labels.go`) | claim/release routes | B2 |
| `internal/store/peer_label.go` | title store (table name unchanged on disk) | B2 |
| `spa/src/stores/usePeerStore.ts` | field renames | A3, B3 |
| `spa/src/components/StatusBar.tsx` | display vs clipboard split | A9 |

---

## Phase A — the address

### Task A1: Ref derivation at width 6

**Files:**
- Rename: `internal/peers/label.go` → `internal/peers/ref.go`
- Rename: `internal/peers/label_test.go` → `internal/peers/ref_test.go`
- Test: `internal/peers/ref_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces: `RefID(sessionID string) string`, `IsRef(s string) bool`. Both replace `CanonicalID` / `IsCanonicalID`, which are deleted.

- [ ] **Step 1: Write the failing tests**

```go
// TestRefID_Shape pins the grammar every other tier depends on.
func TestRefID_Shape(t *testing.T) {
	for _, sid := range []string{
		"a57f3d89-5850-4812-84f5-d24d6c561902",
		"",
		"x",
	} {
		got := peers.RefID(sid)
		if !regexp.MustCompile(`^_[0-9a-z]{6}$`).MatchString(got) {
			t.Errorf("RefID(%q) = %q, want ^_[0-9a-z]{6}$", sid, got)
		}
		if !peers.IsRef(got) {
			t.Errorf("IsRef(%q) = false, want true", got)
		}
	}
}

// TestRefID_Deterministic is the property a resume and a daemon restart rest on.
func TestRefID_Deterministic(t *testing.T) {
	const sid = "1ab9778a-38a4-4355-bc76-b82c9baa61b8"
	if a, b := peers.RefID(sid), peers.RefID(sid); a != b {
		t.Errorf("RefID not deterministic: %q != %q", a, b)
	}
}

// TestRefID_PinnedVector fails loudly if a refactor changes every address on
// every host at once. The expectation is a LITERAL on purpose: a vector that
// recomputes its own expectation asserts nothing. Fill it in at Step 4 and
// change it only alongside a deliberate format change.
func TestRefID_PinnedVector(t *testing.T) {
	const sid = "1ab9778a-38a4-4355-bc76-b82c9baa61b8"
	const want = "REPLACE_AT_STEP_4"
	if got := peers.RefID(sid); got != want {
		t.Errorf("RefID(%q) = %q, want %q", sid, got, want)
	}
}

// TestIsRef_Rejects guards the namespace boundary RoutableName also defends.
func TestIsRef_Rejects(t *testing.T) {
	for _, s := range []string{
		"", "_", "_q34psn4f", "q34psn", "_Q34PSN", "_q34ps", "_q34psn7x", "purdex-b0",
	} {
		if peers.IsRef(s) {
			t.Errorf("IsRef(%q) = true, want false", s)
		}
	}
}
```

- [ ] **Step 2: Run the tests and watch them fail**

Run: `go test -race -count=1 ./internal/peers/ -run 'TestRefID|TestIsRef'`
Expected: FAIL — `undefined: peers.RefID`, `undefined: peers.IsRef`.

- [ ] **Step 3: Rename the file and narrow the width**

`git mv internal/peers/label.go internal/peers/ref.go` and
`git mv internal/peers/label_test.go internal/peers/ref_test.go`, then in `ref.go`:

```go
const (
	// canonicalN is 6, not 8: the ref is no longer the only way to reach a
	// conversation (v4 §5.1). The name covers a ref collision exactly as the
	// ref covers a name collision, so the width carries a tiebreaker's budget
	// rather than the whole address's. 36^6 ≈ 2.18e9 puts the birthday
	// probability for 100 live conversations at ≈2.3e-6.
	canonicalN     = 6
	canonicalSpace = 36 * 36 * 36 * 36 * 36 * 36 // 36^6
)

var refPattern = regexp.MustCompile(`^_[0-9a-z]{6}$`)

// IsRef reports whether s has the ref form, "_" followed by exactly 6
// base36 digits. The leading '_' is half of what keeps the ref namespace
// disjoint from the name namespace; RoutableName is the other half.
func IsRef(s string) bool { return refPattern.MatchString(s) }

// RefID derives a conversation's ref from its Claude Code sessionId:
// "_" + base36(FNV-1a-64(sessionId) mod 36^6), 6 digits, zero-padded. Pure
// and deterministic across resumes and daemon restarts.
func RefID(sessionID string) string {
	h := fnv.New64a()
	_, _ = h.Write([]byte(sessionID))
	n := h.Sum64() % canonicalSpace
	out := make([]byte, canonicalN)
	for i := canonicalN - 1; i >= 0; i-- {
		out[i] = base36Digits[n%36]
		n /= 36
	}
	return "_" + string(out)
}
```

Delete `CanonicalID`, `IsCanonicalID` and `canonicalPattern`. Compilation will break across the tree — that is expected and Task A3 repairs it; to keep this task independently testable, apply a mechanical `CanonicalID` → `RefID` and `IsCanonicalID` → `IsRef` rename at every call site now (`gofmt -r` or editor rename), changing nothing else.

- [ ] **Step 4: Fill in the pinned vector**

The failing `TestRefID_PinnedVector` now prints the real value in its own error
message: `RefID("1ab9...") = "_xxxxxx", want "REPLACE_AT_STEP_4"`. Copy the
value from the left-hand side into the `want` constant. No scratch code and no
extra run — the assertion that is about to pass is the one that produced the
number.

- [ ] **Step 5: Run the tests and verify they pass**

Run: `go test -race -count=1 ./internal/peers/ -run 'TestRefID|TestIsRef' -v`
Expected: PASS, four tests.

- [ ] **Step 6: Commit**

```bash
git commit --only internal/peers/ref.go internal/peers/ref_test.go \
  $(git diff --name-only | grep -E '\.go$' | tr '\n' ' ') \
  -m "feat(peers): derive a 6-digit ref, replacing the 8-digit canonical id"
```

---

### Task A2: `RoutableName` — the grammar that creates disjointness

**Files:**
- Modify: `internal/peers/ref.go`
- Test: `internal/peers/ref_test.go`

**Interfaces:**
- Consumes: `IsRef` from A1.
- Produces: `RoutableName(s string) bool`.

**Why this task exists:** the first draft of the spec assumed a registry name could never look like a ref. Nothing enforces that — `registry.go:383` assigns the field raw from JSON and `internal/peers/ccuds/registry_write.go:175` writes arbitrary strings into it. This function is what makes the assumption true instead of assumed.

- [ ] **Step 1: Write the failing tests**

```go
func TestRoutableName_AcceptsObservedCorpus(t *testing.T) {
	// Every name in ~/.claude/sessions on mini-lab, 2026-09-17.
	for _, s := range []string{
		"purdex-b0", "purdex-53", "purdex-03", "nexen-f2", "nexen-ec",
		"ai-chat-story-3a", "at-inwin-plugin-2e", "invoice-plane-89",
		"firefly-be", "csp-plugin-5e", "mlab-c8", "air19-e2", "istdc-a5",
		"barbox-a6",
	} {
		if !peers.RoutableName(s) {
			t.Errorf("RoutableName(%q) = false, want true", s)
		}
	}
}

// TestRoutableName_RejectsAddressSyntax: each of these would make a name
// address unparseable or ambiguous against the address grammar.
func TestRoutableName_RejectsAddressSyntax(t *testing.T) {
	for _, s := range []string{
		"", "-lead", "has/slash", "has:colon", "has space", "has[bracket]",
		"_leading-underscore", "UPPER", "tráiler",
		strings.Repeat("a", 65),
	} {
		if peers.RoutableName(s) {
			t.Errorf("RoutableName(%q) = true, want false", s)
		}
	}
}

// TestRoutableName_RejectsRefShaped is the whole point: a six-digit name
// would shadow the bare-ref input form for anyone copying bracket text.
func TestRoutableName_RejectsRefShaped(t *testing.T) {
	for _, s := range []string{"q34psn", "abc123", "000000", "zzzzzz"} {
		if peers.RoutableName(s) {
			t.Errorf("RoutableName(%q) = true, want false (ref-shaped)", s)
		}
	}
	// One character either side of the ref width stays routable.
	for _, s := range []string{"abc12", "abc1234"} {
		if !peers.RoutableName(s) {
			t.Errorf("RoutableName(%q) = false, want true", s)
		}
	}
}

func TestRoutableName_BoundaryLengths(t *testing.T) {
	if !peers.RoutableName("ab") {
		t.Error("RoutableName(2 chars) = false, want true")
	}
	if !peers.RoutableName(strings.Repeat("a", 64)) {
		t.Error("RoutableName(64 chars) = false, want true")
	}
	if peers.RoutableName("a") {
		t.Error("RoutableName(1 char) = true, want false")
	}
}
```

- [ ] **Step 2: Run the tests and watch them fail**

Run: `go test -race -count=1 ./internal/peers/ -run TestRoutableName`
Expected: FAIL — `undefined: peers.RoutableName`.

- [ ] **Step 3: Implement**

```go
var routableNamePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{1,63}$`)
var refShapedPattern = regexp.MustCompile(`^[0-9a-z]{6}$`)

// RoutableName reports whether a Claude Code registry name may be used as
// an address head (v4 spec §5.2).
//
// The registry name is an unvalidated JSON string: registry.go assigns it
// raw, and ccuds.RewriteRegistryName can write anything into it. Two
// independent hazards follow, and this function closes both:
//
//   - the PATTERN keeps '/' ':' ' ' '[' ']' and a leading '_' out of an
//     address head, so "<host>/<name>" always parses and can never be read
//     as a ref;
//   - the REF-SHAPED exclusion is what makes Resolve's bare-ref tier safe.
//     The table prints "[q34psn]", so an operator copying bracket text
//     types "q34psn"; without this clause a conversation named "q34psn"
//     would silently shadow the ref of another, and anything able to write
//     a registry file could arrange exactly that.
//
// A name that fails is still displayed. It simply never becomes an address:
// its row is reachable by ref only, and carries Reason "name_unroutable".
func RoutableName(s string) bool {
	return routableNamePattern.MatchString(s) && !refShapedPattern.MatchString(s)
}
```

- [ ] **Step 4: Run the tests and verify they pass**

Run: `go test -race -count=1 ./internal/peers/ -run TestRoutableName -v`
Expected: PASS, four tests.

- [ ] **Step 5: Commit**

```bash
git commit --only internal/peers/ref.go internal/peers/ref_test.go \
  -m "feat(peers): add RoutableName, the grammar that keeps names out of the ref namespace"
```

---

### Task A3: `PeerRecord` — `Ref`, no `Suffix`, and the new `Address`

**Files:**
- Modify: `internal/peers/record.go` (field block at `:34-70`, `applyLabel` at `:270-306`, `WireAddress` at `:82`)
- Modify: every file referencing `.Canonical` or `.Suffix` (45 and 33 files respectively — mechanical)
- Modify: `spa/src/stores/usePeerStore.ts`
- Test: `internal/peers/record_test.go`, `spa/src/stores/usePeerStore.test.ts`

**Interfaces:**
- Consumes: `RefID`, `RoutableName` from A1/A2.
- Produces: `PeerRecord.Ref string` (json `ref`), `PeerRecord.Address` in the forms `<host>/<name>` | `<host>/_<ref>` | `<host>/tmux:<session>`; `PeerRecord.Suffix` deleted; `applyLabel` renamed `applyIdentity` with signature `applyIdentity(rec *PeerRecord, alias string, info LabelInfo, ref, ccName string)` — note `tmuxName` is dropped, it existed only to build `Suffix`.

**This task must land atomically.** Renaming a struct field breaks the package until every reference is updated; a partial commit does not compile.

- [ ] **Step 1: Write the failing tests**

```go
// TestApplyIdentity_NameAddress: the everyday case.
func TestApplyIdentity_NameAddress(t *testing.T) {
	recs := peers.Build(buildInputWithEntry(t, "purdex-b0", "sid-1"))
	got := findBySessionID(t, recs, "sid-1")
	if want := "mlab/purdex-b0"; got.Address != want {
		t.Errorf("Address = %q, want %q", got.Address, want)
	}
	if got.Ref != peers.RefID("sid-1") {
		t.Errorf("Ref = %q, want %q", got.Ref, peers.RefID("sid-1"))
	}
}

// TestApplyIdentity_UnroutableNameFallsBackToRef is A2's payoff: a name
// that cannot be an address must not produce a broken one.
func TestApplyIdentity_UnroutableNameFallsBackToRef(t *testing.T) {
	for _, bad := range []string{"has/slash", "q34psn", "_underscore", ""} {
		recs := peers.Build(buildInputWithEntry(t, bad, "sid-2"))
		got := findBySessionID(t, recs, "sid-2")
		want := "mlab/" + peers.RefID("sid-2")
		if got.Address != want {
			t.Errorf("name %q: Address = %q, want %q", bad, got.Address, want)
		}
		if got.Reason != "name_unroutable" && bad != "" {
			t.Errorf("name %q: Reason = %q, want name_unroutable", bad, got.Reason)
		}
	}
}

// TestPeerRecord_NoSuffixField pins the deletion: a consumer parsing the
// old key must fail loudly rather than read "".
func TestPeerRecord_JSONKeys(t *testing.T) {
	b, err := json.Marshal(peers.PeerRecord{Ref: "_abc123"})
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	if _, ok := m["ref"]; !ok {
		t.Error(`marshalled record has no "ref" key`)
	}
	for _, gone := range []string{"canonical", "suffix"} {
		if _, ok := m[gone]; ok {
			t.Errorf("marshalled record still has %q key", gone)
		}
	}
}

// TestApplyIdentity_TmuxRowUnchanged guards V8.
func TestApplyIdentity_TmuxRowUnchanged(t *testing.T) {
	recs := peers.Build(buildInputWithAgentlessSession(t, "aigora3"))
	got := findBySessionName(t, recs, "aigora3")
	if want := "mlab/tmux:aigora3"; got.Address != want {
		t.Errorf("Address = %q, want %q", got.Address, want)
	}
	if got.Ref != "" {
		t.Errorf("Ref = %q, want empty on an agentless row", got.Ref)
	}
}
```

Reuse the existing helpers in `record_test.go` for `buildInputWithEntry` / `findBySessionID`; if their current signatures do not take a registry name, extend them rather than duplicating.

- [ ] **Step 2: Run the tests and watch them fail**

Run: `go test -race -count=1 ./internal/peers/ -run 'TestApplyIdentity|TestPeerRecord_JSONKeys'`
Expected: FAIL — `unknown field Ref`, `undefined: applyIdentity`.

- [ ] **Step 3: Change the struct and the single writer**

In `record.go`, replace the `Canonical` field and delete `Suffix`:

```go
	// Ref is the sessionId-derived disambiguator, "_q34psn"; "" when the
	// row has no cc agent. It is the one part of an address that cannot
	// drift, and the one Resolve falls back to when a name does.
	Ref string `json:"ref"`
```

Delete the `Suffix` field and its long comment; `RowKind` already carries the provenance split the comment described, and the readable half of the address is now the name.

Replace `applyLabel` with:

```go
// applyIdentity fills Ref/Label/LabelSource/LabelRev/Address/Reason for a
// row whose agent is a cc conversation. It is the single writer of those
// fields, which is what makes the spec's invariant table checkable in one
// place.
//
// The address has two forms and the choice between them is RoutableName's
// (v4 §5.2): a routable registry name gives "<host>/<name>", anything else
// gives "<host>/<ref>" and says why in Reason. A row is never left with an
// address that cannot be typed back in.
//
// ccName is the registry name. Callers on the owner-fallback paths pass ""
// — no live entry stands behind those rows, so they have no name to offer
// and get the ref form, which matches their being unreachable anyway.
func applyIdentity(rec *PeerRecord, alias string, info LabelInfo, ref, ccName string) {
	rec.Ref = ref
	if info.Label != "" {
		rec.Label, rec.LabelSource = info.Label, LabelSourceUser
	} else {
		rec.Label, rec.LabelSource = "", ""
	}
	rec.LabelRev = info.Rev
	if RoutableName(ccName) {
		rec.Address = alias + "/" + ccName
		return
	}
	rec.Address = alias + "/" + ref
	if ccName != "" && rec.Reason == "" {
		rec.Reason = "name_unroutable"
	}
}
```

Update `WireAddress` (`:82`) to return `r.Ref` (or `""` when `Ref == ""`), dropping the `":" + Suffix` half — Task A5 covers the wire contract that consumes it.

Update all seven `applyLabel` call sites to `applyIdentity`, dropping the `s.Name` / tmux argument and passing `RefID(...)` where they passed `CanonicalID(...)`. Delete the `Suffix` function and `sanitizeMax`/`Sanitize` if nothing else uses them — check with `grep -rn "peers.Sanitize\|\.Suffix" --include='*.go' .` first; `Sanitize` may have other consumers.

- [ ] **Step 4: Repair the rest of the tree mechanically**

Run `go build ./... 2>&1 | head -50` and fix each `.Canonical` → `.Ref` and each `.Suffix` removal. Change nothing else — a behaviour change smuggled into a rename is invisible to review.

- [ ] **Step 5: Mirror the rename in the SPA store**

In `spa/src/stores/usePeerStore.ts`: `canonical` → `ref` on both the type and the mapping (`p.canonical ?? ''` becomes `p.ref ?? ''`), and delete `suffix` if present. Update `usePeerStore.test.ts` fixtures to the new keys.

- [ ] **Step 6: Run the tests and verify they pass**

Run: `go test -race -count=1 ./internal/peers/... ./internal/module/peers/...`
Run: `cd spa && npx vitest run src/stores/usePeerStore.test.ts`
Expected: PASS. Go failures outside `internal/peers` mean Step 4 is incomplete.

- [ ] **Step 7: Commit**

```bash
# The grep drops the version-controlled `pdx` binary at the repo root: any
# `go build ./...` overwrites it, and --only would otherwise commit 2.7 MB
# of artefact alongside the change.
git commit --only $(git diff --name-only | grep -Ev '^pdx$' | tr '\n' ' ') \
  -m "feat(peers): address on the registry name, ref when it is not routable"
```

---

### Task A4: `Resolve` — the tiers, and the refusal on a name/ref mismatch

**Files:**
- Modify: `internal/peers/address.go`
- Test: `internal/peers/address_test.go`

**Interfaces:**
- Consumes: `PeerRecord.Ref`, `RoutableName`, `IsRef`.
- Produces: `ErrNameMismatch` (a package-level `error`), `Resolve` accepting the three input forms.

- [ ] **Step 1: Write the failing tests**

```go
func TestResolve_BareName(t *testing.T) {
	recs := []peers.PeerRecord{liveRow("purdex-b0", "_q34psn"), liveRow("nexen-f2", "_df25d0")}
	got, err := peers.Resolve(recs, "purdex-b0", peers.ResolveSnapshot{})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got.Ref != "_q34psn" {
		t.Errorf("resolved %q, want the purdex-b0 row", got.Ref)
	}
}

func TestResolve_RefWithAndWithoutUnderscore(t *testing.T) {
	recs := []peers.PeerRecord{liveRow("purdex-b0", "_q34psn")}
	for _, form := range []string{"_q34psn", "q34psn"} {
		got, err := peers.Resolve(recs, form, peers.ResolveSnapshot{})
		if err != nil {
			t.Fatalf("Resolve(%q): %v", form, err)
		}
		if got.Ref != "_q34psn" {
			t.Errorf("Resolve(%q) landed on %q", form, got.Ref)
		}
	}
}

// TestResolve_RefShapedNameCannotShadow is A2 defended end to end: even if a
// registry file carries a six-digit name, it must not win the bare-ref form.
func TestResolve_RefShapedNameCannotShadow(t *testing.T) {
	recs := []peers.PeerRecord{
		liveRow("q34psn", "_aaaaaa"), // an attacker-shaped name
		liveRow("purdex-b0", "_q34psn"),
	}
	got, err := peers.Resolve(recs, "q34psn", peers.ResolveSnapshot{})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got.Ref != "_q34psn" {
		t.Errorf("bare ref resolved to %q; the ref-shaped NAME shadowed it", got.Ref)
	}
}

func TestResolve_CombinedFormMatches(t *testing.T) {
	recs := []peers.PeerRecord{liveRow("purdex-b0", "_q34psn")}
	got, err := peers.Resolve(recs, "purdex-b0 [q34psn]", peers.ResolveSnapshot{})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got.Ref != "_q34psn" {
		t.Errorf("resolved %q", got.Ref)
	}
}

// TestResolve_CombinedFormMismatchRefuses is the security property: the name
// in a combined address is a check digit, not decoration.
func TestResolve_CombinedFormMismatchRefuses(t *testing.T) {
	recs := []peers.PeerRecord{liveRow("attacker", "_q34psn")}
	_, err := peers.Resolve(recs, "trusted-name [q34psn]", peers.ResolveSnapshot{})
	if !errors.Is(err, peers.ErrNameMismatch) {
		t.Fatalf("Resolve err = %v, want ErrNameMismatch", err)
	}
	for _, want := range []string{"trusted-name", "attacker", "q34psn"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not name %q", err, want)
		}
	}
}

func TestResolve_AmbiguousName(t *testing.T) {
	recs := []peers.PeerRecord{liveRow("purdex-b0", "_aaaaaa"), liveRow("purdex-b0", "_bbbbbb")}
	_, err := peers.Resolve(recs, "purdex-b0", peers.ResolveSnapshot{})
	var amb *peers.AmbiguousError
	if !errors.As(err, &amb) || len(amb.Candidates) != 2 {
		t.Fatalf("Resolve err = %v, want AmbiguousError with 2 candidates", err)
	}
}

// TestResolve_AmbiguousRefStillReachableByName is §3.1's residual, asserted.
func TestResolve_AmbiguousRefStillReachableByName(t *testing.T) {
	recs := []peers.PeerRecord{liveRow("purdex-b0", "_q34psn"), liveRow("nexen-f2", "_q34psn")}
	var amb *peers.AmbiguousError
	if _, err := peers.Resolve(recs, "_q34psn", peers.ResolveSnapshot{}); !errors.As(err, &amb) {
		t.Fatalf("ref Resolve err = %v, want AmbiguousError", err)
	}
	if _, err := peers.Resolve(recs, "purdex-b0", peers.ResolveSnapshot{}); err != nil {
		t.Errorf("name Resolve: %v, want the name to still work", err)
	}
}

// TestResolve_UnroutableNameNeverWinsTier1
func TestResolve_UnroutableNameNeverWinsTier1(t *testing.T) {
	recs := []peers.PeerRecord{liveRow("has/slash", "_aaaaaa")}
	if _, err := peers.Resolve(recs, "has/slash", peers.ResolveSnapshot{}); !errors.Is(err, peers.ErrNotFound) {
		t.Errorf("Resolve err = %v, want ErrNotFound", err)
	}
}
```

`liveRow(name, ref)` is a helper to add: a `PeerRecord` with `Ref: ref` and
`Agent: &AgentInfo{Type: "cc", PID: 1, PeerName: name}`.

- [ ] **Step 2: Run the tests and watch them fail**

Run: `go test -race -count=1 ./internal/peers/ -run TestResolve`
Expected: FAIL — `undefined: peers.ErrNameMismatch`, plus wrong-tier failures.

- [ ] **Step 3: Implement the tiers**

Add before the existing tier logic in `Resolve`:

```go
// ErrNameMismatch is returned for the combined form "<name> [<ref>]" when
// the ref resolves to a row whose name is not the one typed.
//
// It refuses rather than delivering-with-a-warning because the name in a
// combined address is the reader's only check on the ref beside it.
// "trusted-name [attackerRef]" is precisely the string worth getting pasted,
// and a warning arrives after the message has gone. Legitimate drift has an
// explicit escape hatch: "<host>/_<ref>" asks for the ref outright.
var ErrNameMismatch = errors.New("the name does not match the ref's current name")

// splitCombined splits "<name> [<ref>]" into its parts. ok is false when s
// is not in that form, in which case it is one of the plain forms.
func splitCombined(s string) (name, ref string, ok bool) {
	i := strings.IndexByte(s, '[')
	if i <= 0 || !strings.HasSuffix(s, "]") {
		return "", "", false
	}
	name = strings.TrimSpace(s[:i])
	ref = s[i+1 : len(s)-1]
	if name == "" || ref == "" {
		return "", "", false
	}
	return name, ref, true
}
```

In `Resolve`, after the `cc:` / `tmux:` switch:

```go
	if name, ref, ok := splitCombined(session); ok {
		if !strings.HasPrefix(ref, "_") {
			ref = "_" + ref
		}
		rec, err := resolveTier(records, session, func(r PeerRecord) bool {
			return hasLiveEntry(r) && r.Ref == ref
		})
		if err != nil {
			return rec, err
		}
		if rec.Agent.PeerName != name {
			return PeerRecord{}, fmt.Errorf("%w: typed %q, ref %s is now %q",
				ErrNameMismatch, name, ref, rec.Agent.PeerName)
		}
		return rec, nil
	}

	// Tier 1: the registry name, over live rows whose name is routable.
	// The RoutableName guard is not decoration: an unroutable name must not
	// win a tier, because it could never have produced the address the
	// caller is holding.
	rec, err := resolveTier(records, session, func(r PeerRecord) bool {
		return hasLiveEntry(r) && RoutableName(r.Agent.PeerName) && r.Agent.PeerName == head
	})
	if !errors.Is(err, ErrNotFound) {
		return rec, err
	}

	// Tier 2/3: the ref, with or without its underscore. Safe without an
	// ordering rule because RoutableName forbids a ref-shaped name, so no
	// row can match both tiers.
	wantRef := head
	if !strings.HasPrefix(wantRef, "_") {
		wantRef = "_" + wantRef
	}
	if IsRef(wantRef) {
		rec, err = resolveTier(records, session, func(r PeerRecord) bool {
			return hasLiveEntry(r) && r.Ref == wantRef
		})
		if err == nil && snap.RegistryIncomplete {
			return PeerRecord{}, ErrResolveNotReady
		}
		if !errors.Is(err, ErrNotFound) {
			return rec, err
		}
	}
```

Then the existing stale-version check, `snap.Partial` check and tier 4 (bare tmux name) follow unchanged. Update `Resolve`'s doc comment to describe the new tiers — the old one describes v3's and is now actively misleading.

- [ ] **Step 4: Run the tests and verify they pass**

Run: `go test -race -count=1 ./internal/peers/ -run TestResolve -v`
Expected: PASS, including every pre-existing v3 conservatism test.

- [ ] **Step 5: Commit**

```bash
git commit --only internal/peers/address.go internal/peers/address_test.go \
  -m "feat(peers): resolve by name, by ref, and refuse a combined form whose name drifted"
```

---

### Task A5: Wire contract

**Files:**
- Modify: `internal/peers/wire.go` (`ValidateWireAddress` at `:499`, `WireFrom.Address` doc at `:238`)
- Test: `internal/peers/wire_test.go`

**Interfaces:**
- Consumes: `IsRef`.
- Produces: `ValidateWireAddress` accepting the four classes in the table below.

- [ ] **Step 1: Write the failing test**

```go
func TestValidateWireAddress_Matrix(t *testing.T) {
	for _, tc := range []struct {
		name, addr string
		wantOK     bool
	}{
		{"v4 ref", "_q34psn", true},
		{"v3 canonical, accepted one release", "_q34psn4f", true},
		{"v2 user label head", "purdex-tester", true},
		{"v1 empty", "", true},
		{"v3 canonical with suffix", "_q34psn4f:aigora2-purdex-b0", true},
		{"garbage", "has/slash", false},
		{"bracket form is not a wire address", "purdex-b0 [q34psn]", false},
	} {
		err := peers.ValidateWireAddress(tc.addr)
		if gotOK := err == nil; gotOK != tc.wantOK {
			t.Errorf("%s: ValidateWireAddress(%q) err = %v, wantOK %v",
				tc.name, tc.addr, err, tc.wantOK)
		}
	}
}
```

- [ ] **Step 2: Run the test and watch it fail**

Run: `go test -race -count=1 ./internal/peers/ -run TestValidateWireAddress_Matrix`
Expected: FAIL on the bracket-form and `_q34psn` rows.

- [ ] **Step 3: Implement**

```go
// legacyV3Head matches the 8-digit canonical id v3 used as an address head.
//
// TODO(v5): delete this arm, isLegacyV2Head, and their rows in
// TestValidateWireAddress_Matrix once every peer runs v4 or later. Both are
// accepted for exactly one release so that a single upgrade window does not
// have to carry two incompatibilities at once.
var legacyV3Head = regexp.MustCompile(`^_[0-9a-z]{8}$`)

func ValidateWireAddress(s string) error {
	if s == "" {
		return nil
	}
	head, rest := SplitSession(s)
	if !IsRef(head) && !legacyV3Head.MatchString(head) && !isLegacyV2Head(head) {
		if err := ValidateUserLabel(head); err != nil {
			return fmt.Errorf("%w: head: %w", ErrAddressInvalid, err)
		}
	}
	_ = rest // a legacy suffix is tolerated; v4 senders do not set one
	return nil
}
```

Delete `ValidSuffix` and `suffixWirePattern`, and the suffix arm that used them. Update `WireFrom.Address`'s comment to say it is `_<ref>` from a v4 sender.

- [ ] **Step 4: Run the tests and verify they pass**

Run: `go test -race -count=1 ./internal/peers/ -run 'TestValidateWireAddress|TestDeliverRequest'`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git commit --only internal/peers/wire.go internal/peers/wire_test.go \
  -m "feat(peers): accept a 6-digit ref on the wire, with v2/v3 heads for one release"
```

---

### Task A6: Stale-version refusal, precisely

**Files:**
- Modify: `internal/peers/address.go` (`hasPreV3Rows`)
- Test: `internal/peers/address_test.go`

**Interfaces:**
- Consumes: `hasLiveEntry`.
- Produces: `hasStaleVersionRows(records []PeerRecord) bool` (renamed from `hasPreV3Rows`).

**Why the precision matters:** the JSON key changed from `canonical` to `ref`, so every row from a v3 daemon decodes with `Ref == ""`. But so do three kinds of legitimate **v4** row — owner-fallback (`inbox_dead` / `ambiguous`, `Agent.PID == 0`), proxy, and `agent: null`. Only the `hasLiveEntry` conjunction tells them apart.

- [ ] **Step 1: Write the failing tests**

```go
func TestHasStaleVersionRows_V3Batch(t *testing.T) {
	recs := []peers.PeerRecord{{
		Agent: &peers.AgentInfo{Type: "cc", PID: 42, PeerName: "purdex-b0"},
		Ref:   "", // a v3 daemon sent "canonical"; v4 does not read that key
	}}
	if _, err := peers.Resolve(recs, "purdex-b0", peers.ResolveSnapshot{}); !errors.Is(err, peers.ErrRemoteTooOld) {
		t.Fatalf("Resolve err = %v, want ErrRemoteTooOld", err)
	}
}

// TestHasStaleVersionRows_V4RowsNotMisjudged: each of these legitimately
// carries an empty Ref on a current daemon.
func TestHasStaleVersionRows_V4RowsNotMisjudged(t *testing.T) {
	for _, tc := range []struct {
		name string
		rec  peers.PeerRecord
	}{
		{"owner fallback", peers.PeerRecord{
			Agent: &peers.AgentInfo{Type: "cc", PID: 0}, Reason: "inbox_dead"}},
		{"proxy", peers.PeerRecord{
			Agent: &peers.AgentInfo{Type: "proxy", PID: 7}, Reason: "proxy"}},
		{"agent null", peers.PeerRecord{
			Agent: nil, Reason: "no_agent", SessionName: "aigora3"}},
	} {
		recs := []peers.PeerRecord{tc.rec}
		_, err := peers.Resolve(recs, "nobody", peers.ResolveSnapshot{})
		if errors.Is(err, peers.ErrRemoteTooOld) {
			t.Errorf("%s: judged stale; want a plain miss", tc.name)
		}
	}
}

// A v3 batch must still honour the explicit tmux form — it is the escape
// hatch the refusal points the caller at.
func TestHasStaleVersionRows_TmuxFormStillResolves(t *testing.T) {
	recs := []peers.PeerRecord{{
		Agent:       &peers.AgentInfo{Type: "cc", PID: 42, PeerName: "purdex-b0"},
		SessionName: "aigora2",
	}}
	if _, err := peers.Resolve(recs, "tmux:aigora2", peers.ResolveSnapshot{}); err != nil {
		t.Errorf("tmux form: %v", err)
	}
}
```

- [ ] **Step 2: Run the tests and watch them fail**

Run: `go test -race -count=1 ./internal/peers/ -run TestHasStaleVersionRows`
Expected: FAIL — the combined-form and name tiers reach the refusal differently than v3's single tier did.

- [ ] **Step 3: Rename and re-comment**

```go
// hasStaleVersionRows reports whether records came from a daemon predating
// Peer Address v4 — the condition behind ErrRemoteTooOld.
//
// The signal is the spec's invariant read backwards, and v4 gets it for
// free: the JSON key moved from "canonical" to "ref", so a v3 daemon's rows
// decode with Ref == "" no matter what they actually sent.
//
// The hasLiveEntry conjunction is load-bearing, not caution. A CURRENT
// daemon also emits rows with an empty Ref — owner-fallback rows
// (inbox_dead / ambiguous, PID 0), proxy rows, and agent:null rows — and
// counting those would declare every v4 host obsolete.
//
// Refusing matters more here than it did in v3, where the fallback was a
// tmux-name guess. A v3 row still carries a usable registry name in
// agent.peer_name, so v4's name tier would SUCCEED against it — delivering
// to a row whose ref the sender could never have verified.
func hasStaleVersionRows(records []PeerRecord) bool {
	for _, r := range records {
		if hasLiveEntry(r) && r.Ref == "" {
			return true
		}
	}
	return false
}
```

Move the call so it is reached from the name tier's miss as well as the ref tier's, and keep it ahead of the `snap.Partial` check for the reason v3 records: "retry, the inventory is partial" is advice that can never come true against an old daemon.

- [ ] **Step 4: Run the tests and verify they pass**

Run: `go test -race -count=1 ./internal/peers/ -v`
Expected: PASS, whole package.

- [ ] **Step 5: Commit**

```bash
git commit --only internal/peers/address.go internal/peers/address_test.go \
  -m "fix(peers): refuse a pre-v4 batch without misjudging v4's own ref-less rows"
```

---

### Task A7: `pdx peers` table

**Files:**
- Modify: `cmd/pdx/peers.go` (`formatPeersTable` at `:359-373`, the `--all` variant at `:449-461`, `deliverableField` at `:536`)
- Test: `cmd/pdx/peers_test.go`

**Interfaces:**
- Consumes: `PeerRecord.Ref`, `.Address`, `.SessionName`, `.Reason`.
- Produces: the column set `TITLE ADDRESS AGENT STATUS DELIVERABLE TMUX CWD` (with `HOST` leading in `--all`).

Note: this task renders the column header as `TITLE` even though the field is still `Label` until Phase B. Renaming the Go symbol is B2's job; rendering the right word is this task's, and doing it here keeps the golden files from churning twice.

- [ ] **Step 1: Write the failing golden test**

```go
func TestFormatPeersTable_Columns(t *testing.T) {
	resp := peersResponse{Peers: []ipeers.PeerRecord{
		{Address: "mlab/purdex-b0", Ref: "_q34psn", SessionName: "aigora2", Cwd: "~",
			Agent: &ipeers.AgentInfo{Type: "cc", Status: "idle"}, Deliverable: true},
		{Address: "mlab/purdex-53", Ref: "_d8dc4a", SessionName: "purdex7", Cwd: "~",
			Label: "Purdex Tester 01",
			Agent: &ipeers.AgentInfo{Type: "cc", Status: "busy"}, Deliverable: true},
		{Address: "mlab/_df25d0", Ref: "_df25d0", SessionName: "nexen", Cwd: "~",
			Agent: &ipeers.AgentInfo{Type: "cc", Status: "idle"}, Reason: "inbox_dead"},
		{Address: "mlab/tmux:aigora3", SessionName: "aigora3", Cwd: "~", Reason: "no_agent"},
	}}
	var buf bytes.Buffer
	formatPeersTable(&buf, resp)
	out := buf.String()

	if !strings.HasPrefix(out, "TITLE\tADDRESS\tAGENT\tSTATUS\tDELIVERABLE\tTMUX\tCWD") {
		t.Errorf("header = %q", strings.SplitN(out, "\n", 2)[0])
	}
	for _, want := range []string{
		"mlab/purdex-b0 [q34psn]",
		"Purdex Tester 01",
		"inbox_dead",
		"mlab/tmux:aigora3",
		"aigora2",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("table does not contain %q:\n%s", want, out)
		}
	}
	for _, gone := range []string{"NAME\t", "HOST\t"} {
		if strings.Contains(out, gone) {
			t.Errorf("table still has the %q column:\n%s", gone, out)
		}
	}
}
```

- [ ] **Step 2: Run the test and watch it fail**

Run: `go test -race -count=1 ./cmd/pdx/ -run TestFormatPeersTable_Columns`
Expected: FAIL on the header and on the bracketed address.

- [ ] **Step 3: Implement**

Add the display composer beside the table code:

```go
// displayAddress renders a row's address for a human: "<host>/<name> [<ref>]".
//
// The ref prints without its leading underscore because the bracket already
// separates it, and prints on EVERY row rather than only on ambiguous ones:
// a reader who has to go looking for it when a name stops working has to
// look in a different place than the one they were already reading.
func displayAddress(rec ipeers.PeerRecord) string {
	if rec.Ref == "" || strings.HasSuffix(rec.Address, "/"+rec.Ref) {
		return rec.Address
	}
	return rec.Address + " [" + strings.TrimPrefix(rec.Ref, "_") + "]"
}
```

Change both header lines and both row loops: drop `HOST` from the single-host form and `NAME` from both, add `TMUX` (from `rec.SessionName`) before `CWD`, and pass every address through `displayAddress`. Leave `deliverableField` alone — `yes` / `<reason>` is already the shape §5.7 specifies.

- [ ] **Step 4: Run the tests and verify they pass**

Run: `go test -race -count=1 ./cmd/pdx/ -run TestFormatPeers -v`
Expected: PASS. Update any pre-existing golden fixtures that pinned the old columns.

- [ ] **Step 5: Commit**

```bash
git commit --only cmd/pdx/peers.go cmd/pdx/peers_test.go \
  -m "feat(pdx): render <host>/<name> [ref] and surface the tmux session"
```

---

### Task A8: `pdx msg send` input forms and the `whoami` block

**Files:**
- Modify: `cmd/pdx/msg.go` (`renderSelfRecord` at `:653`, the send path's address handling)
- Test: `cmd/pdx/msg_test.go`

**Interfaces:**
- Consumes: `Resolve`'s three forms (A4).
- Produces: no new exported symbols; `pdx msg send` passes the address through unchanged so `Resolve` sees the bracket form intact.

- [ ] **Step 1: Write the failing tests**

```go
func TestRunMsgSend_AcceptsCombinedForm(t *testing.T) {
	// The CLI must not strip, split or normalise the bracket form — Resolve
	// checks the name against the ref, and a CLI that discarded the name
	// would silently disable that check.
	srv := fakeSendServer(t)
	defer srv.Close()
	exit := runMsgSend(msgInvocation{args: []string{"mlab/purdex-b0 [q34psn]", "hi"}}, srv.URL, io.Discard, io.Discard)
	if exit != 0 {
		t.Fatalf("exit = %d", exit)
	}
	if got := srv.lastToAddress(); got != "mlab/purdex-b0 [q34psn]" {
		t.Errorf("sent to %q, want the address verbatim", got)
	}
}

func TestRenderSelfRecord_UnsetTitle(t *testing.T) {
	var buf bytes.Buffer
	renderSelfRecord(ipeers.PeerRecord{
		Address: "mlab/purdex-53", Ref: "_d8dc4a", Host: "mlab", HostID: "mlab:278cbm",
	}, &buf)
	out := buf.String()
	if strings.Contains(out, "(, rev 0)") {
		t.Errorf("unset title still renders as an empty tuple:\n%s", out)
	}
	if !strings.Contains(out, "(none)") {
		t.Errorf("unset title does not say so:\n%s", out)
	}
	if !strings.Contains(out, "_d8dc4a") {
		t.Errorf("ref line missing:\n%s", out)
	}
}
```

- [ ] **Step 2: Run the tests and watch them fail**

Run: `go test -race -count=1 ./cmd/pdx/ -run 'TestRunMsgSend_AcceptsCombinedForm|TestRenderSelfRecord_UnsetTitle'`
Expected: FAIL — the whoami block prints `label:       (, rev 0)`.

- [ ] **Step 3: Implement**

In `renderSelfRecord`, replace the canonical and label lines:

```go
	fmt.Fprintf(stdout, "address:    %s\n", sanitizeCell(rec.Address))
	fmt.Fprintf(stdout, "ref:        %s\n", sanitizeCell(rec.Ref))
	if rec.Label == "" {
		fmt.Fprintf(stdout, "title:      (none)\n")
	} else {
		fmt.Fprintf(stdout, "title:      %s (%s, rev %d)\n",
			sanitizeCell(rec.Label), sanitizeCell(rec.LabelSource), rec.LabelRev)
	}
```

For the send path, confirm the address argument reaches the request body unmodified. If any trimming or splitting exists, remove it and note why in a comment: the bracket form's name is a check digit and the CLI is not entitled to drop it.

Update `peersUsage` and `msgUsage` to show the three forms from §5.5.

- [ ] **Step 4: Run the tests and verify they pass**

Run: `go test -race -count=1 ./cmd/pdx/ -v`
Expected: PASS. Update usage-string assertions that pinned `_3k9f2mq4`.

- [ ] **Step 5: Commit**

```bash
git commit --only cmd/pdx/msg.go cmd/pdx/msg_test.go \
  -m "feat(pdx): pass the combined address through verbatim and fix whoami's empty title"
```

---

### Task A9: SPA status bar — display readable, copy exact

**Files:**
- Modify: `spa/src/components/StatusBar.tsx` (`:140`, `:346-370`)
- Test: `spa/src/components/StatusBar.test.tsx`

**Interfaces:**
- Consumes: `usePeerStore`'s `ref` (A3) and `address`.
- Produces: no new exports.

- [ ] **Step 1: Write the failing test**

```tsx
it('displays the name with its ref and copies the exact form', async () => {
  renderStatusBarWithPeer({ address: 'mlab/purdex-b0', ref: '_q34psn', label: '' })
  expect(screen.getByText(/purdex-b0 \[q34psn\]/)).toBeInTheDocument()
  await userEvent.click(screen.getByText(/purdex-b0 \[q34psn\]/))
  expect(await navigator.clipboard.readText()).toBe('mlab/purdex-b0 [q34psn]')
})

it('renders a peer with no title', () => {
  renderStatusBarWithPeer({ address: 'mlab/purdex-b0', ref: '_q34psn', label: '' })
  expect(screen.queryByTestId('peer-id')).not.toBeNull()
})
```

- [ ] **Step 2: Run the test and watch it fail**

Run: `cd spa && npx vitest run src/components/StatusBar.test.tsx`
Expected: FAIL — the component returns early because `row.label === ''`.

- [ ] **Step 3: Implement**

Replace the `row.label === ''` guard with `row.ref === '' && row.address === ''`: a title was never what identified a row, and under v4 it is usually absent. Render `` `${name} [${ref.replace(/^_/, '')}]` `` where `name` is the address's segment after the first `/`, and set the copy value to `` `${address} [${ref.replace(/^_/, '')}]` ``.

Add the comment that explains the asymmetry, because it will read as an inconsistency otherwise:

```tsx
// Display drops the host, the clipboard keeps it — and keeps the ref.
// What gets copied is what gets pasted into a handoff and used hours
// later, which is exactly the window in which a name drifts or is taken
// by someone else. The prettier string is the weaker one; it does not
// belong on the clipboard.
```

Render the title, when set, beside the name rather than instead of it.

- [ ] **Step 4: Run the tests and verify they pass**

Run: `cd spa && npx vitest run src/components/StatusBar.test.tsx && pnpm run lint`
Expected: PASS, no lint errors.

- [ ] **Step 5: Commit**

```bash
git commit --only spa/src/components/StatusBar.tsx spa/src/components/StatusBar.test.tsx \
  -m "feat(spa): show the name with its ref, copy the address with its ref"
```

---

## Phase B — `label` becomes `title`

### Task B1: `ValidateTitle` and normalized collision compare

**Files:**
- Modify: `internal/peers/ref.go`
- Test: `internal/peers/ref_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces: `ValidateTitle(s string) error`, `NormalizeTitle(s string) string`. `ValidateUserLabel` stays for one release — Task A5's wire arm still calls it.

- [ ] **Step 1: Write the failing tests**

```go
func TestValidateTitle_Accepts(t *testing.T) {
	for _, s := range []string{
		"Purdex Tester 01", "purdex-tester", "測試 01", "cc", "tmux",
		strings.Repeat("a", 64),
	} {
		if err := peers.ValidateTitle(s); err != nil {
			t.Errorf("ValidateTitle(%q) = %v, want nil", s, err)
		}
	}
}

func TestValidateTitle_Rejects(t *testing.T) {
	for _, s := range []string{
		"", strings.Repeat("a", 65), "has\ttab", "has\nnewline", "esc\x1b[31m",
	} {
		if err := peers.ValidateTitle(s); err == nil {
			t.Errorf("ValidateTitle(%q) = nil, want an error", s)
		}
	}
}

// The 64 is BYTES, not runes: a multi-byte title must be measured the way
// the storage and the wire measure it.
func TestValidateTitle_ByteBoundary(t *testing.T) {
	if err := peers.ValidateTitle(strings.Repeat("測", 21)); err != nil { // 63 bytes
		t.Errorf("63 bytes rejected: %v", err)
	}
	if err := peers.ValidateTitle(strings.Repeat("測", 22)); err == nil { // 66 bytes
		t.Error("66 bytes accepted, want rejected")
	}
}

func TestNormalizeTitle(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"Purdex Tester", "purdex tester"},
		{"purdex  tester", "purdex tester"},
		{"  Purdex\tTester  ", "purdex tester"},
	} {
		if got := peers.NormalizeTitle(tc.in); got != tc.want {
			t.Errorf("NormalizeTitle(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
```

- [ ] **Step 2: Run the tests and watch them fail**

Run: `go test -race -count=1 ./internal/peers/ -run 'TestValidateTitle|TestNormalizeTitle'`
Expected: FAIL — undefined.

- [ ] **Step 3: Implement**

```go
const titleMaxBytes = 64

// ValidateTitle applies the title rule: 1..64 bytes of printable UTF-8.
//
// There are no reserved words. A title reaches nothing — Resolve never
// consults one — so there is nothing for "cc" or "tmux" to shadow, and
// refusing them would only make the field harder to use than it is
// dangerous.
func ValidateTitle(s string) error {
	if s == "" {
		return fmt.Errorf("%w: empty", ErrTitleInvalid)
	}
	if len(s) > titleMaxBytes {
		return fmt.Errorf("%w: %d bytes, max %d", ErrTitleInvalid, len(s), titleMaxBytes)
	}
	if !utf8.ValidString(s) {
		return fmt.Errorf("%w: not valid UTF-8", ErrTitleInvalid)
	}
	for _, r := range s {
		if !unicode.IsPrint(r) {
			return fmt.Errorf("%w: contains a non-printable character", ErrTitleInvalid)
		}
	}
	return nil
}

// NormalizeTitle is the form two titles are compared in when deciding
// whether to warn: case-folded, with runs of whitespace collapsed to one
// space and the ends trimmed. "Purdex Tester" and "purdex  tester" name the
// same thing to a reader, so they must collide for the warning too —
// otherwise the warning misses exactly the near-duplicates it exists for.
func NormalizeTitle(s string) string {
	return strings.Join(strings.Fields(strings.ToLower(s)), " ")
}
```

Add `ErrTitleInvalid` beside the existing error vars.

- [ ] **Step 4: Run the tests and verify they pass**

Run: `go test -race -count=1 ./internal/peers/ -run 'TestValidateTitle|TestNormalizeTitle' -v`
Expected: PASS, four tests.

- [ ] **Step 5: Commit**

```bash
git commit --only internal/peers/ref.go internal/peers/ref_test.go \
  -m "feat(peers): add the free-text title grammar and its normalized compare"
```

---

### Task B2: Rename `label` → `title` across Go

**Files:**
- Rename: `internal/module/peers/labels.go` → `titles.go` (and its test)
- Modify: `internal/peers/record.go`, `internal/peers/ref.go`, `internal/module/peers/*.go`, `internal/store/peer_label.go`, `cmd/pdx/*.go` — 18 files carry the symbols
- Test: the renamed tests plus `internal/module/peers/titles_test.go`

**Interfaces:**
- Consumes: `ValidateTitle`, `NormalizeTitle` from B1.
- Produces: `PeerRecord.Title/TitleSource/TitleRev` (json `title`, `title_source`, `title_rev`); `Envelope.TitlesUnavailable` (json `titles_unavailable`); `SelfWarning.Code == "title_in_use"` with `LiveTitles []string`.

**The SQLite table keeps its name.** `peer_labels` on disk is untouched: renaming it would be a migration, and alpha carries no migration obligation (`feedback_no_alpha_migration`). Only Go symbols and JSON keys move.

- [ ] **Step 1: Write the failing test**

```go
func TestClaimTitle_WarnsOnNormalizedDuplicate(t *testing.T) {
	m := newTestModule(t)
	mustClaimTitle(t, m, "sid-1", "Purdex Tester")
	resp := mustClaimTitle(t, m, "sid-2", "purdex  tester")
	if resp.Warning == nil || resp.Warning.Code != "title_in_use" {
		t.Fatalf("warning = %+v, want title_in_use", resp.Warning)
	}
	if len(resp.Warning.LiveTitles) == 0 {
		t.Error("live_titles is empty; an agent cannot pick the next serial")
	}
	// Both keep their titles: a duplicate warns, it never refuses.
	if got := titleOf(t, m, "sid-1"); got != "Purdex Tester" {
		t.Errorf("first holder's title = %q, want it untouched", got)
	}
}
```

- [ ] **Step 2: Run the test and watch it fail**

Run: `go test -race -count=1 ./internal/module/peers/ -run TestClaimTitle`
Expected: FAIL — undefined helpers / `label_in_use` code.

- [ ] **Step 3: Rename mechanically, then wire the normalized compare**

`git mv internal/module/peers/labels.go internal/module/peers/titles.go` (and the test), then apply throughout: `ValidateUserLabel`→`ValidateTitle` **at the claim path only** (the wire's legacy arm keeps calling the old one), `LabelSourceUser`→`TitleSourceUser`, `LabelInfo`→`TitleInfo`, `LabelStore`→`TitleStore`, `PeerRecord.Label/LabelSource/LabelRev`→`Title/TitleSource/TitleRev`, `label_in_use`→`title_in_use`, `live_labels`→`live_titles`, `labels_unavailable`→`titles_unavailable`, `applyIdentity`'s `info.Label`→`info.Title`.

In the claim path, compare with `NormalizeTitle` on both sides when computing the warning's holders.

Change nothing else. A behaviour change hidden in a rename of this size is not reviewable.

- [ ] **Step 4: Run the full suite**

Run: `go test -race -count=1 ./...`
Expected: PASS except the known `internal/module/agent` flake (#1092) — rerun that package alone to confirm it is the flake and not your change.

- [ ] **Step 5: Commit**

```bash
# The grep drops the version-controlled `pdx` binary at the repo root — see
# Task A3's commit step for why.
git commit --only $(git diff --name-only | grep -Ev '^pdx$' | tr '\n' ' ') \
  -m "refactor(peers): rename label to title, now that it names rather than tags"
```

---

### Task B3: SPA title rename

**Files:**
- Modify: the 30 files under `spa/src` referencing `label`/`labelSource`/`canonical` peer fields
- Test: their co-located tests

**Interfaces:**
- Consumes: B2's JSON keys.
- Produces: `PeerRow.title`, `.titleSource`, `.titleRev`; `PeerEnvelopeFlags.titlesUnavailable`.

- [ ] **Step 1: Update the store type and mapping first**

In `usePeerStore.ts`: `label`→`title`, `labelSource`→`titleSource`, `labelRev`→`titleRev`, `labelsUnavailable`→`titlesUnavailable`, and the `p.label`/`p.label_source`/`env.labels_unavailable` reads to the new keys. Update the i18n key `peer.labels_unavailable_note`→`peer.titles_unavailable_note` and its entry in the locale files.

- [ ] **Step 2: Run the type check and let it find the rest**

Run: `cd spa && pnpm run build`
Expected: FAIL, listing every consumer. Fix each; change nothing but names.

- [ ] **Step 3: Run the tests**

Run: `cd spa && npx vitest run && pnpm run lint && pnpm run build`
Expected: PASS, clean lint, successful build.

- [ ] **Step 4: Commit**

```bash
git commit --only $(git diff --name-only -- spa | tr '\n' ' ') \
  -m "refactor(spa): follow the label to title rename"
```

---

## Phase C — a host publishes its own alias

### Task C1: `Envelope.Alias`

**Files:**
- Modify: `internal/peers/envelope.go`, `internal/module/peers/module.go` (`localEnvelope`), `internal/module/peers/hosts.go` (`verifyHost` at `:199`)
- Test: `internal/peers/envelope_test.go`, `internal/module/peers/hosts_test.go`

**Interfaces:**
- Consumes: `config.Config.PeerAlias()`.
- Produces: `Envelope.Alias string` (json `alias`); `verifyHost` returning an envelope whose `Alias` is validated or blanked.

- [ ] **Step 1: Write the failing tests**

```go
func TestLocalEnvelope_CarriesSelfAlias(t *testing.T) {
	m := newTestModule(t, withPeerAlias("mlab"))
	env := m.localEnvelope(context.Background(), "mlab:278cbm", "mlab")
	if env.Alias != "mlab" {
		t.Errorf("Alias = %q, want %q", env.Alias, "mlab")
	}
}

// A peer's self-reported alias is attacker-controlled, exactly like host_id.
func TestVerifyHost_RejectsUnsafeAlias(t *testing.T) {
	for _, bad := range []string{"has/slash", "..", "esc\x1b[31m", strings.Repeat("a", 65), ""} {
		env := envelopeFromFakeHost(t, bad)
		got := sanitizeLearnedAlias(env.Alias, "mlab")
		if got != "" {
			t.Errorf("alias %q survived sanitising as %q", bad, got)
		}
	}
}
```

- [ ] **Step 2: Run the tests and watch them fail**

Run: `go test -race -count=1 ./internal/peers/ ./internal/module/peers/ -run 'TestLocalEnvelope_CarriesSelfAlias|TestVerifyHost_RejectsUnsafeAlias'`
Expected: FAIL — undefined field / helper.

- [ ] **Step 3: Implement**

In `envelope.go`:

```go
	// Alias is what this host calls ITSELF (config PeerAlias()). A reader
	// uses it to name a newly paired peer the way that peer names itself,
	// so an address means the same string on both machines. It is
	// self-reported and therefore attacker-controlled: validate before
	// storing, exactly as host_id already is.
	Alias string `json:"alias"`
```

Set it in `localEnvelope` from the alias already passed in. Add beside `validHostID`:

```go
// sanitizeLearnedAlias returns alias when it is safe to adopt as a local
// name for a peer, or "" when it is not. It is the same posture
// validHostID takes: a peer's self-report is data, never a decision.
func sanitizeLearnedAlias(alias, localAlias string) string {
	if config.ValidateAlias(alias, localAlias) != nil {
		return ""
	}
	return alias
}
```

Have `verifyHost` blank `env.Alias` through this helper before returning.

- [ ] **Step 4: Run the tests and verify they pass**

Run: `go test -race -count=1 ./internal/peers/ ./internal/module/peers/ -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git commit --only internal/peers/envelope.go internal/peers/envelope_test.go \
  internal/module/peers/module.go internal/module/peers/hosts.go internal/module/peers/hosts_test.go \
  -m "feat(peers): publish a host's own alias in its envelope"
```

---

### Task C2: Adopt the published alias when pairing

**Files:**
- Modify: `internal/module/peers/hosts.go` (`handleAddHost` at `:250-330`)
- Modify: `cmd/pdx/peers.go` (make the `alias` argument optional in `pdx peers host add`)
- Test: `internal/module/peers/hosts_test.go`, `cmd/pdx/peers_test.go`

**Interfaces:**
- Consumes: `sanitizeLearnedAlias`, `Envelope.Alias` from C1.
- Produces: `addHostRequest.Alias` optional; a 409 body naming both aliases on collision.

- [ ] **Step 1: Write the failing tests**

```go
func TestAddHost_AdoptsPublishedAlias(t *testing.T) {
	m := newTestModule(t)
	remote := fakePeerHost(t, envelope{HostID: "air:aaa", Alias: "air26"})
	resp := mustAddHost(t, m, addHostRequest{URL: remote.URL, Token: "t"})
	if resp.Alias != "air26" {
		t.Errorf("Alias = %q, want the published %q", resp.Alias, "air26")
	}
}

func TestAddHost_ExplicitAliasWins(t *testing.T) {
	m := newTestModule(t)
	remote := fakePeerHost(t, envelope{HostID: "air:aaa", Alias: "air26"})
	resp := mustAddHost(t, m, addHostRequest{Alias: "air", URL: remote.URL, Token: "t"})
	if resp.Alias != "air" {
		t.Errorf("Alias = %q, want the explicit %q", resp.Alias, "air")
	}
}

// A collision is not auto-suffixed: "air26-2" would be unportable in a new
// way, which is the problem this phase exists to remove.
func TestAddHost_PublishedAliasCollisionIs409(t *testing.T) {
	m := newTestModule(t, withExistingHost("air26"))
	remote := fakePeerHost(t, envelope{HostID: "air:bbb", Alias: "air26"})
	code, body := addHost(t, m, addHostRequest{URL: remote.URL, Token: "t"})
	if code != http.StatusConflict {
		t.Fatalf("status = %d, want 409", code)
	}
	if !strings.Contains(body, "air26") {
		t.Errorf("409 body does not name the colliding alias: %s", body)
	}
}

// No published alias and no explicit one: the operator must name it.
func TestAddHost_NoAliasAnywhereIs400(t *testing.T) {
	m := newTestModule(t)
	remote := fakePeerHost(t, envelope{HostID: "air:aaa", Alias: ""})
	if code, _ := addHost(t, m, addHostRequest{URL: remote.URL, Token: "t"}); code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", code)
	}
}
```

- [ ] **Step 2: Run the tests and watch them fail**

Run: `go test -race -count=1 ./internal/module/peers/ -run TestAddHost`
Expected: FAIL — the alias is currently required and never learned.

- [ ] **Step 3: Implement**

Reorder `handleAddHost`: run `verifyHost` **before** the alias checks when `req.Alias == ""`, so the published alias is available to fall back to. Then:

```go
	alias := req.Alias
	if alias == "" {
		alias = sanitizeLearnedAlias(env.Alias, localAlias)
	}
	if alias == "" {
		writeJSONError(w, http.StatusBadRequest,
			"no alias given and the peer published none; pass one explicitly")
		return
	}
	if err := config.ValidateAlias(alias, localAlias); err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}
	if cfg.Peers.FindPeerHostByAlias(alias) != -1 {
		writeJSONError(w, http.StatusConflict, fmt.Sprintf(
			"alias %q is already used by another host; pass an explicit alias for this one", alias))
		return
	}
```

Keep every existing guard — the admin-token check, the self-pairing check, and the re-check inside `UpdateConfig`. Adding a fallback must not remove a gate.

In `cmd/pdx/peers.go`, allow `pdx peers host add <url>` with the alias omitted, and update `peersUsage`.

- [ ] **Step 4: Run the tests and verify they pass**

Run: `go test -race -count=1 ./internal/module/peers/ ./cmd/pdx/ -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git commit --only internal/module/peers/hosts.go internal/module/peers/hosts_test.go \
  cmd/pdx/peers.go cmd/pdx/peers_test.go \
  -m "feat(peers): adopt a peer's published alias when none is given"
```

---

### Task C3: Surface alias drift

**Files:**
- Modify: `internal/peers/envelope.go` (`HostResult`), `internal/module/peers/module.go` (`fetchHostResult`), `cmd/pdx/peers.go` (`formatHostsTable` at `:712`)
- Test: `cmd/pdx/peers_test.go`

**Interfaces:**
- Consumes: `Envelope.Alias`.
- Produces: `HostResult.SelfAlias string` (json `self_alias`); a `SELF_ALIAS` column.

- [ ] **Step 1: Write the failing test**

```go
func TestFormatHostsTable_MarksAliasDrift(t *testing.T) {
	var buf bytes.Buffer
	formatHostsTable(&buf, []hostRow{
		{Alias: "air", SelfAlias: "air26"},
		{Alias: "mlab", SelfAlias: "mlab"},
	})
	out := buf.String()
	if !strings.Contains(out, "SELF_ALIAS") {
		t.Errorf("no SELF_ALIAS column:\n%s", out)
	}
	if !strings.Contains(out, "air26") {
		t.Errorf("drifted self alias not shown:\n%s", out)
	}
	// A row that agrees must not be marked; the marker is for the exception.
	agreeing := out[strings.Index(out, "mlab"):]
	if strings.Contains(agreeing, "!") {
		t.Errorf("agreeing row is marked:\n%s", out)
	}
}
```

- [ ] **Step 2: Run the test and watch it fail**

Run: `go test -race -count=1 ./cmd/pdx/ -run TestFormatHostsTable_MarksAliasDrift`
Expected: FAIL — `unknown field SelfAlias`.

- [ ] **Step 3: Implement**

Add `SelfAlias` to `HostResult` and to `hostRow`, populate it in `fetchHostResult` from the fetched envelope, and add the column. Mark drift with a trailing `!` on the `SELF_ALIAS` cell only when it is non-empty and differs from `ALIAS`.

Add the comment that stops a later reader from "fixing" this into an auto-follow:

```go
// SelfAlias is what the peer calls itself, shown beside the name we call
// it. Drift is surfaced, never followed: the local alias is what every
// address on this host resolves against, and silently adopting a peer's
// rename would move every address out from under whoever wrote one down.
```

- [ ] **Step 4: Run the tests and verify they pass**

Run: `go test -race -count=1 ./cmd/pdx/ -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git commit --only internal/peers/envelope.go internal/module/peers/module.go \
  cmd/pdx/peers.go cmd/pdx/peers_test.go \
  -m "feat(pdx): show what a peer calls itself beside what we call it"
```

---

## Final task: documentation and the version gate

### Task D1: Update CLAUDE.md and build

**Files:**
- Modify: `CLAUDE.md` (the "Peer addresses" section)
- Modify: `docs/specs/2026-09-17-peer-address-v4-spec.md` (tick §9's acceptance items as they are run)

- [ ] **Step 1: Rewrite the Peer addresses section**

The current text describes v3: `<host>/<canonical>[:<suffix>]`, canonical `_3k9f2mq4`, labels with the `^[a-z0-9][a-z0-9-]{1,31}$` grammar and the `label_in_use` serial convention. Replace with v4's model: the address is `<host>/<name>`, displayed `<host>/<name> [<ref>]`; the three input forms; `title` as free text with the serial convention retained for human disambiguation; and the instruction that an agent asked to "become X" reports its **address**, not X.

- [ ] **Step 2: Build and verify the daemon**

```bash
make build
./bin/pdx --version
```
Expected: the version string from `VERSION`, not `unknown`.

- [ ] **Step 3: Run everything**

```bash
go test -race -count=1 ./...
cd spa && npx vitest run && pnpm run lint && pnpm run build
```

- [ ] **Step 4: Commit**

```bash
git commit --only CLAUDE.md docs/specs/2026-09-17-peer-address-v4-spec.md \
  -m "docs: describe the v4 address model in CLAUDE.md"
```

---

## After the plan

Real-machine acceptance is **§9 of the spec**, not a step here, because it needs two daemons deployed and cannot be run by a subagent in a worktree. Run it after merge and before the bump PR. §9.9 — pasting an address made on one host into the other — is the one that gates the release.
