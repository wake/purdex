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
- **All `internal/peers` tests live in `package peers`** (internal tests). Call `RefID`, `Build`, `Resolve`, `PeerRecord` directly — **never** `peers.RefID`. `internal/module/peers` tests are `package peers` too (the module package); `cmd/pdx` tests are `package main`.
- **Build the daemon with `make build`**, never bare `go build` — the version is injected via ldflags and a bare build yields `version unknown`.
- **The repo root holds a version-controlled 2.7 MB `pdx` binary that `go build ./cmd/...` overwrites.** Every commit uses `git commit --only` with the **explicit file list printed in that task's commit step**. Never `git commit -am`, and never expand `$(git diff --name-only)` into a commit — this worktree may host parallel subagents sharing one index, and a dynamic expansion sweeps in their work.
- **Go tests:** `go test -race -count=1 ./internal/peers/...` (scope narrowed per task).
- **SPA:** `cd spa && npx vitest run`, `pnpm run lint`, `pnpm run build`.
- **Known flakes, not yours** — both redden under parallel load and pass alone. Rerun the package by itself before believing either is your change: `internal/module/agent`'s `TestConsumeSignals_GraceWindowDrop_RearmsAfterTeardown` (#1092), and `internal/agent/probe`'s `TestWatch_StopWatch_CancelsLoop` (found during Task A4; file an issue if it recurs).
- **Mutation-test any rule the task calls load-bearing.** Task A4 found that none of its prescribed tests actually pinned the rule the whole task exists for: moving the stale-version gate below tier 1 left the package green. Before you report a rule as covered, break it deliberately and confirm something goes red. If nothing does, the test you still owe is the deliverable.

### Existing test fixtures you will reuse (verified 2026-09-17)

| symbol | file | signature |
|---|---|---|
| `liveRow` | `internal/peers/address_test.go:21` | `liveRow(canonical, label, sessionName string, pid int) PeerRecord` — **Task A4 extends this**; it currently sets `Canonical` and no `PeerName` |
| `inboxDeadRow` | `internal/peers/address_test.go:35` | `inboxDeadRow(canonical, label, sessionName string) PeerRecord` |
| `mustMarshalMap` | `internal/peers/record_test.go:14` | `mustMarshalMap(t *testing.T, v any) map[string]any` |
| `formatPeersTable` | `cmd/pdx/peers.go:370` | `formatPeersTable(resp peers.Envelope) string` — returns a string, takes no writer |
| `formatHostsTable` | `cmd/pdx/peers.go:715` | `formatHostsTable(hosts []cliHostRow) string` |
| `cliHostRow` | `cmd/pdx/peers.go:560` | `{Alias, URL, HostID string; Verified, HasToken, HasInboundToken, AllowBypass bool}` |
| `runMsgSend` | `cmd/pdx/msg.go:280` | `runMsgSend(inv msgInvocation, getenv func(string) string, stdout, stderr io.Writer) int` |

`record_test.go` has **no** `buildInputWithEntry`-style helpers. Its tests construct data inline:

```go
in := BuildInput{
	Alias:    "mini-lab",
	Sessions: []SessionSummary{{Code: "s1", Name: "mt1", Cwd: "/w", TmuxInstance: "t1"}},
	Owners:   map[string]Owner{"s1": {AgentType: "cc", SessionID: "sess-x", TmuxPaneID: "%1", Status: "idle"}},
	Entries:  []Entry{{PID: 100, SessionID: "sess-x", Name: "purdex-1", Tmux: "mt1:@1.%1", Inbox: "/tmp/1.sock"}},
}
got := Build(in)
```

Follow that idiom. Do not invent helpers that other tasks would then have to match.

---

## File Structure

| File | Responsibility | Task |
|---|---|---|
| `internal/peers/ref.go` (renamed from `label.go`) | ref derivation, `RoutableName`, title grammar | A1, A2, B1 |
| `internal/peers/record.go` | `PeerRecord`, `Build`, `applyIdentity` — single writer of identity fields | A3 |
| `internal/peers/address.go` | `Resolve`, `resolveRefHead`, `ErrNameMismatch`, `hasStaleVersionRows` | A4, A6 |
| `internal/peers/wire.go` | `WireFrom.Address`, `ValidateWireAddress` | A5 |
| `internal/peers/envelope.go` | `Envelope.Alias`, `HostResult.SelfAlias` | C1, C3 |
| `cmd/pdx/peers.go` | table columns, display composition | A7, C3 |
| `cmd/pdx/msg.go` | send input forms, `whoami` block | A8 |
| `internal/module/peers/hosts.go` | alias learning and adoption | C1, C2 |
| `internal/module/peers/titles.go` (from `labels.go`) | claim/release routes | B2 |
| `spa/src/stores/usePeerStore.ts` | field renames | A3b, B3 |
| `spa/src/components/StatusBar.tsx` | display vs clipboard split | A9 |

**Ordering.** A1 → A2 are **serial**: A2 edits `ref.go`, which A1 creates by renaming `label.go`. A3 needs A1+A2. A3b needs A3. A4 needs A3. A6 needs A4 (it edits the same function). A5, A7, A8 need A3. A9 needs A3b. B1 is independent of Phase A and may run any time. B2 needs B1 **and** A3 (it renames fields A3 also touches). B3 needs B2. C1 → C2 → C3 are serial. D1 is last.

---

## Phase A — the address

### Task A1: Ref derivation at width 6

**Files:**
- Rename: `internal/peers/label.go` → `internal/peers/ref.go`; `internal/peers/label_test.go` → `internal/peers/ref_test.go`
- Test: `internal/peers/ref_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces: `RefID(sessionID string) string`, `IsRef(s string) bool`. `CanonicalID` and `IsCanonicalID` survive this task as **deprecated one-line aliases** so the tree keeps compiling; Task A3 deletes them.

- [ ] **Step 1: Rename the files, then write the failing tests**

The rename comes first so the tests have somewhere to live. It is pure `git mv`, no content change, so the red light still precedes the implementation:

```bash
git mv internal/peers/label.go internal/peers/ref.go
git mv internal/peers/label_test.go internal/peers/ref_test.go
```

Fix the path comment on each file's first line (`// internal/peers/label.go` → `ref.go`); `git mv` does not.

Then append to `internal/peers/ref_test.go` (`package peers` — no `peers.` prefix anywhere):

```go
func TestRefID_Shape(t *testing.T) {
	for _, sid := range []string{"a57f3d89-5850-4812-84f5-d24d6c561902", "", "x"} {
		got := RefID(sid)
		if !regexp.MustCompile(`^_[0-9a-z]{6}$`).MatchString(got) {
			t.Errorf("RefID(%q) = %q, want ^_[0-9a-z]{6}$", sid, got)
		}
		if !IsRef(got) {
			t.Errorf("IsRef(%q) = false, want true", got)
		}
	}
}

func TestRefID_Deterministic(t *testing.T) {
	const sid = "1ab9778a-38a4-4355-bc76-b82c9baa61b8"
	if a, b := RefID(sid), RefID(sid); a != b {
		t.Errorf("RefID not deterministic: %q != %q", a, b)
	}
}

// TestRefID_PinnedVector fails loudly if a refactor changes every address on
// every host at once. The expectation is a LITERAL on purpose: a vector that
// recomputes its own expectation asserts nothing. Fill it in at Step 4.
func TestRefID_PinnedVector(t *testing.T) {
	const sid = "1ab9778a-38a4-4355-bc76-b82c9baa61b8"
	const want = "REPLACE_AT_STEP_4"
	if got := RefID(sid); got != want {
		t.Errorf("RefID(%q) = %q, want %q", sid, got, want)
	}
}

func TestIsRef_Rejects(t *testing.T) {
	for _, s := range []string{"", "_", "_q34psn4f", "q34psn", "_Q34PSN", "_q34ps", "purdex-b0"} {
		if IsRef(s) {
			t.Errorf("IsRef(%q) = true, want false", s)
		}
	}
}
```

Add `"regexp"` to the test file's imports if it is not already there.

- [ ] **Step 2: Run the tests and watch them fail**

Run: `go test -race -count=1 ./internal/peers/ -run 'TestRefID|TestIsRef'`
Expected: FAIL — `undefined: RefID`, `undefined: IsRef`.

- [ ] **Step 3: Narrow the width**

In `ref.go`, change the two constants, replace `canonicalPattern`, and add the two functions plus the compatibility aliases:

```go
const (
	// canonicalN is 6, not 8: the ref is no longer the only way to reach a
	// conversation (v4 spec §5.1). The name covers a ref collision exactly as
	// the ref covers a name collision, so the width carries a tiebreaker's
	// budget rather than the whole address's. 36^6 ≈ 2.18e9 puts the birthday
	// probability for 100 live conversations at ≈2.3e-6.
	canonicalN     = 6
	canonicalSpace = 36 * 36 * 36 * 36 * 36 * 36 // 36^6
)

var refPattern = regexp.MustCompile(`^_[0-9a-z]{6}$`)

// IsRef reports whether s has the ref form, "_" followed by exactly 6 base36
// digits. The leading '_' is half of what keeps the ref namespace disjoint
// from the name namespace; RoutableName (Task A2) is the other half.
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

// Deprecated: use RefID / IsRef.
//
// These exist for exactly one task. Renaming the FUNCTION here and the FIELD
// in Task A3 as one change would mean a single unreviewable commit spanning
// 45 files; splitting them means this task cannot also delete the old names.
// Task A3 removes both lines along with the Canonical field.
func CanonicalID(sessionID string) string { return RefID(sessionID) }
func IsCanonicalID(s string) bool         { return IsRef(s) }
```

Delete the old `CanonicalID` body and `canonicalPattern`. **Touch no other file.** The old names still resolve, so the tree still compiles.

- [ ] **Step 4: Fill in the pinned vector**

The failing `TestRefID_PinnedVector` prints the real value in its own message:
`RefID("1ab9...") = "_xxxxxx", want "REPLACE_AT_STEP_4"`. Copy the left-hand value into `want`.

- [ ] **Step 5: Run the package and verify nothing else broke**

Run: `go test -race -count=1 ./internal/peers/ -v`
Expected: PASS. Pre-existing tests asserting an 8-digit canonical will fail — update **those assertions only** (width 8 → 6); do not change what they test.

- [ ] **Step 6: Commit**

```bash
git commit --only internal/peers/ref.go internal/peers/ref_test.go \
  -m "feat(peers): derive a 6-digit ref, with the canonical names kept as aliases"
```

---

### Task A2: `RoutableName` — the grammar that creates disjointness

**Files:**
- Modify: `internal/peers/ref.go`
- Test: `internal/peers/ref_test.go`

**Interfaces:**
- Consumes: nothing (it does not call `IsRef`; it matches the ref *shape* independently).
- Produces: `RoutableName(s string) bool`.

**Why this task exists:** the spec's first draft assumed a registry name could never look like a ref. Nothing enforces that — `registry.go:383` assigns the field raw from JSON and `internal/peers/ccuds/registry_write.go:175` writes arbitrary strings into it. This function makes the assumption true instead of assumed.

- [ ] **Step 1: Write the failing tests**

```go
func TestRoutableName_AcceptsObservedCorpus(t *testing.T) {
	// Every registry name on mini-lab, 2026-09-17.
	for _, s := range []string{
		"purdex-b0", "purdex-53", "purdex-03", "nexen-f2", "nexen-ec",
		"ai-chat-story-3a", "at-inwin-plugin-2e", "invoice-plane-89",
		"firefly-be", "csp-plugin-5e", "mlab-c8", "air19-e2", "istdc-a5", "barbox-a6",
	} {
		if !RoutableName(s) {
			t.Errorf("RoutableName(%q) = false, want true", s)
		}
	}
}

// Each of these would make a name address unparseable or ambiguous.
func TestRoutableName_RejectsAddressSyntax(t *testing.T) {
	for _, s := range []string{
		"", "a", "-lead", "has/slash", "has:colon", "has space", "has[bracket]",
		"_leading-underscore", "UPPER", "tráiler", strings.Repeat("a", 65),
	} {
		if RoutableName(s) {
			t.Errorf("RoutableName(%q) = true, want false", s)
		}
	}
}

// The whole point: a six-digit name would shadow the bare-ref input form for
// anyone copying bracket text.
func TestRoutableName_RejectsRefShaped(t *testing.T) {
	for _, s := range []string{"q34psn", "abc123", "000000", "zzzzzz"} {
		if RoutableName(s) {
			t.Errorf("RoutableName(%q) = true, want false (ref-shaped)", s)
		}
	}
	for _, s := range []string{"abc12", "abc1234"} {
		if !RoutableName(s) {
			t.Errorf("RoutableName(%q) = false, want true", s)
		}
	}
}

func TestRoutableName_BoundaryLengths(t *testing.T) {
	if !RoutableName("ab") {
		t.Error("2 chars rejected")
	}
	if !RoutableName(strings.Repeat("a", 64)) {
		t.Error("64 chars rejected")
	}
}
```

Add `"strings"` to the test imports if absent.

- [ ] **Step 2: Run the tests and watch them fail**

Run: `go test -race -count=1 ./internal/peers/ -run TestRoutableName`
Expected: FAIL — `undefined: RoutableName`.

- [ ] **Step 3: Implement**

```go
var (
	routableNamePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{1,63}$`)
	refShapedPattern    = regexp.MustCompile(`^[0-9a-z]{6}$`)
)

// RoutableName reports whether a Claude Code registry name may be used as an
// address head (v4 spec §5.2).
//
// The registry name is an unvalidated JSON string: registry.go assigns it raw
// and ccuds.RewriteRegistryName can write anything into it. Two independent
// hazards follow, and this function closes both:
//
//   - the PATTERN keeps '/', ':', ' ', '[', ']' and a leading '_' out of an
//     address head, so "<host>/<name>" always parses and can never be read as
//     a ref;
//   - the REF-SHAPED exclusion is what makes Resolve's bare-ref tier safe. The
//     table prints "[q34psn]", so an operator copying bracket text types
//     "q34psn"; without this clause a conversation named "q34psn" would
//     silently shadow another's ref, and anything able to write a registry
//     file could arrange exactly that.
//
// A failing name is still displayed. It simply never becomes an address: its
// row is reachable by ref only and carries Reason "name_unroutable".
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

### Task A3: `PeerRecord` — `Ref`, no `Suffix`, and the new `Address` (Go atom)

**Files:**
- Modify: `internal/peers/record.go`, `internal/peers/ref.go` (delete the A1 aliases), and every Go file referencing `.Canonical` / `.Suffix` / `CanonicalID` / `IsCanonicalID`
- Test: `internal/peers/record_test.go`

**Interfaces:**
- Consumes: `RefID`, `RoutableName`.
- Produces: `PeerRecord.Ref string` (json `ref`); `PeerRecord.Suffix` **deleted**; `applyIdentity(rec *PeerRecord, alias string, info LabelInfo, ref, ccName string)` replacing `applyLabel` (the `tmuxName` parameter goes — it existed only to build `Suffix`).

**This task must land atomically.** Renaming a struct field breaks the package until every reference is updated.

- [ ] **Step 1: Enumerate the blast radius before touching anything**

```bash
grep -rln '\.Canonical\|\.Suffix\|CanonicalID\|IsCanonicalID\|Suffix(' --include='*.go' . | sort | tee /tmp/a3-files.txt
wc -l /tmp/a3-files.txt
```

That list is your commit allowlist for Step 7. If a file appears that you did not change, drop it from the commit rather than committing it.

- [ ] **Step 2: Write the failing tests**

Append to `internal/peers/record_test.go` (`package peers`):

```go
// ccBuildInput is the minimal BuildInput for one deliverable cc row. It
// mirrors TestBuild_CC_OneCandidate_Deliverable's shape.
func ccBuildInput(name, sessionID string) BuildInput {
	return BuildInput{
		Alias:    "mlab",
		Sessions: []SessionSummary{{Code: "s1", Name: "mt1", Cwd: "/w", TmuxInstance: "t1"}},
		Owners: map[string]Owner{
			"s1": {AgentType: "cc", SessionID: sessionID, TmuxPaneID: "%1", Status: "idle"},
		},
		Entries: []Entry{{
			PID: 100, SessionID: sessionID, Name: name, NameSource: "derived",
			Cwd: "/w", Tmux: "mt1:@1.%1", Inbox: "/tmp/1.sock", Status: "idle",
		}},
	}
}

func TestApplyIdentity_NameAddress(t *testing.T) {
	got := Build(ccBuildInput("purdex-b0", "sess-x"))[0]
	if want := "mlab/purdex-b0"; got.Address != want {
		t.Errorf("Address = %q, want %q", got.Address, want)
	}
	if want := RefID("sess-x"); got.Ref != want {
		t.Errorf("Ref = %q, want %q", got.Ref, want)
	}
}

// A name that cannot be an address must not produce a broken one.
func TestApplyIdentity_UnroutableNameFallsBackToRef(t *testing.T) {
	for _, bad := range []string{"has/slash", "q34psn", "_underscore"} {
		got := Build(ccBuildInput(bad, "sess-y"))[0]
		if want := "mlab/" + RefID("sess-y"); got.Address != want {
			t.Errorf("name %q: Address = %q, want %q", bad, got.Address, want)
		}
		if got.Reason != "name_unroutable" {
			t.Errorf("name %q: Reason = %q, want name_unroutable", bad, got.Reason)
		}
	}
}

func TestPeerRecord_JSONKeys(t *testing.T) {
	m := mustMarshalMap(t, PeerRecord{Ref: "_abc123"})
	if _, ok := m["ref"]; !ok {
		t.Error(`marshalled record has no "ref" key`)
	}
	for _, gone := range []string{"canonical", "suffix"} {
		if _, ok := m[gone]; ok {
			t.Errorf("marshalled record still has %q key", gone)
		}
	}
}

// V8: an agentless tmux row is untouched.
func TestApplyIdentity_TmuxRowUnchanged(t *testing.T) {
	in := BuildInput{
		Alias:    "mlab",
		Sessions: []SessionSummary{{Code: "s1", Name: "aigora3", Cwd: "~", TmuxInstance: "t1"}},
	}
	got := Build(in)[0]
	if want := "mlab/tmux:aigora3"; got.Address != want {
		t.Errorf("Address = %q, want %q", got.Address, want)
	}
	if got.Ref != "" {
		t.Errorf("Ref = %q, want empty on an agentless row", got.Ref)
	}
}
```

- [ ] **Step 3: Run the tests and watch them fail**

Run: `go test -race -count=1 ./internal/peers/ -run 'TestApplyIdentity|TestPeerRecord_JSONKeys'`
Expected: FAIL — `unknown field Ref in struct literal`.

- [ ] **Step 4: Change the struct and its single writer**

In `record.go`, replace the `Canonical` field and delete `Suffix` with its comment block:

```go
	// Ref is the sessionId-derived disambiguator, "_q34psn"; "" when the row
	// has no cc agent. It is the one part of an address that cannot drift, and
	// what Resolve falls back to when a name does.
	Ref string `json:"ref"`
```

Replace `applyLabel` (`:270-306`) with:

```go
// applyIdentity fills Ref/Label/LabelSource/LabelRev/Address/Reason for a row
// whose agent is a cc conversation. It is the single writer of those fields,
// which is what makes the spec's invariant table checkable in one place.
//
// The address has two forms and RoutableName picks between them (v4 §5.2): a
// routable registry name gives "<host>/<name>", anything else gives
// "<host>/<ref>" and says why in Reason. A row is never left holding an
// address that cannot be typed back in.
//
// ccName is the registry name. The owner-fallback callers pass "" — no live
// entry stands behind those rows, so they have no name to offer, and the ref
// form matches their being unreachable anyway. "" is not a Reason-worthy
// event for them, so only a non-empty unroutable name sets one.
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

Update `WireAddress` (`:82`) to return `r.Ref` (`""` when `Ref == ""`), dropping the `":" + Suffix` half.

Update all seven `applyLabel` call sites to `applyIdentity`, dropping the tmux-name argument and passing `RefID(...)`. Delete the `Suffix` function. Check `grep -rn 'Sanitize(' --include='*.go' .` before deleting `Sanitize` — it may have other consumers.

Delete the `CanonicalID` / `IsCanonicalID` aliases from `ref.go` and fix the resulting call sites.

- [ ] **Step 5: Repair the tree against the Step 1 list**

Work through `/tmp/a3-files.txt`, not through `go build` output — an iterative guess-and-compile loop makes it impossible to tell afterwards which edits were intended. Change only `.Canonical` → `.Ref`, `CanonicalID` → `RefID`, `IsCanonicalID` → `IsRef`, and `.Suffix` removals. Then confirm:

Run: `go build ./...`
Expected: clean.

- [ ] **Step 6: Run the Go tests**

Run: `go test -race -count=1 ./internal/peers/... ./internal/module/peers/... ./cmd/pdx/...`
Expected: PASS, except assertions that pinned the old address format — update those to the new one. A test that pinned `_q34psn4f:aigora2-purdex-b0` is asserting v3 and must now assert `mlab/purdex-b0`.

- [ ] **Step 7: Commit**

```bash
# Use the allowlist from Step 1, minus anything you did not actually change.
git commit --only $(cat /tmp/a3-files.txt | tr '\n' ' ') \
  internal/peers/record_test.go \
  -m "feat(peers): address on the registry name, ref when it is not routable"
```

Verify before pushing: `git show --stat HEAD` must not list `pdx` or any file outside the list.

---

### Task A3b: SPA store follows the field rename

**Files:**
- Modify: `spa/src/stores/usePeerStore.ts`
- Test: `spa/src/stores/usePeerStore.test.ts`

**Interfaces:**
- Consumes: A3's `ref` JSON key.
- Produces: `PeerRow.ref: string`; `suffix` removed if present.

- [ ] **Step 1: Update the test fixtures to the new key**

In `usePeerStore.test.ts`, change every fixture's `canonical: '...'` to `ref: '...'` and every assertion on `.canonical` to `.ref`.

- [ ] **Step 2: Run the tests and watch them fail**

Run: `cd spa && npx vitest run src/stores/usePeerStore.test.ts`
Expected: FAIL — `ref` is undefined on the mapped row.

- [ ] **Step 3: Implement**

In `usePeerStore.ts`: rename the `canonical: string` field to `ref: string` (keeping its comment, which explains it is the discriminator rather than `labelSource`), and change the mapping `canonical: p.canonical ?? ''` to `ref: p.ref ?? ''`. Remove `suffix` from the type and mapping if present.

- [ ] **Step 4: Run and verify**

Run: `cd spa && npx vitest run src/stores/usePeerStore.test.ts && pnpm run build`
Expected: PASS; the build surfaces any other consumer of `.canonical` — fix each by rename only.

- [ ] **Step 5: Commit**

```bash
git commit --only spa/src/stores/usePeerStore.ts spa/src/stores/usePeerStore.test.ts \
  -m "refactor(spa): follow canonical to ref"
```

If Step 4 forced edits to other SPA files, add those exact paths to the command.

---

### Task A3c: `TmuxName` — keep the tmux name on screen for entry rows

**Files:**
- Modify: `internal/peers/record.go`
- Test: `internal/peers/record_test.go`

**Interfaces:**
- Consumes: nothing new.
- Produces: `PeerRecord.TmuxName string` (json `tmux_name`), display-only.

**Why this task exists.** Task A3 deleted `Suffix`, and Task A3 reported the consequence: `SessionName`
is set only on session rows (`record.go:156`), so `EntryRecord` rows now carry no tmux name at all
and §5.7's new `TMUX` column would render empty for them. Under v3 that name reached the screen
through `Suffix`. This restores it in a field that says what it is.

`TmuxName` is **display-only**. Do not add it to any `Resolve` tier. An entry row's value is the
frozen registry field, and that value going stale is v3 §2's P1 — the whole reason the tmux name is
not an identity in v4.

- [ ] **Step 1: Write the failing tests**

```go
func TestBuild_TmuxName_SessionRowUsesLiveName(t *testing.T) {
	in := ccBuildInput("purdex-b0", "sess-x") // from Task A3
	got := Build(in)[0]
	if got.TmuxName != "mt1" {
		t.Errorf("TmuxName = %q, want the live session name %q", got.TmuxName, "mt1")
	}
	if got.RowKind != "session" {
		t.Fatalf("RowKind = %q, want session", got.RowKind)
	}
}

// An entry row has no session behind it, so its tmux name comes from the
// registry file, where it was frozen at startup.
func TestBuild_TmuxName_EntryRowUsesFrozenRegistryName(t *testing.T) {
	in := BuildInput{
		Alias: "mlab",
		Entries: []Entry{{
			PID: 100, SessionID: "sess-z", Name: "purdex-b0",
			Tmux: "aigora2:@5.%5", Inbox: "/tmp/1.sock",
		}},
	}
	got := Build(in)[0]
	if got.RowKind != "entry" {
		t.Fatalf("RowKind = %q, want entry", got.RowKind)
	}
	if got.TmuxName != "aigora2" {
		t.Errorf("TmuxName = %q, want the frozen registry name %q", got.TmuxName, "aigora2")
	}
	// SessionName stays empty: tier 4 and "tmux:<name>" must not be able to
	// reach a row through a value that may already be wrong.
	if got.SessionName != "" {
		t.Errorf("SessionName = %q, want empty on an entry row", got.SessionName)
	}
}

func TestBuild_TmuxName_EmptyOutsideTmux(t *testing.T) {
	in := BuildInput{
		Alias:   "mlab",
		Entries: []Entry{{PID: 100, SessionID: "sess-z", Name: "purdex-b0", Inbox: "/tmp/1.sock"}},
	}
	if got := Build(in)[0]; got.TmuxName != "" {
		t.Errorf("TmuxName = %q, want empty for an agent outside tmux", got.TmuxName)
	}
}
```

- [ ] **Step 2: Run the tests and watch them fail**

Run: `go test -race -count=1 ./internal/peers/ -run TestBuild_TmuxName`
Expected: FAIL — `unknown field TmuxName`.

- [ ] **Step 3: Implement**

Add to `PeerRecord`:

```go
	// TmuxName is the tmux session this row's agent is in, for display only.
	// NOTHING routes on it, and that is the point: its two provenances differ
	// in how much they can be trusted, and RowKind tells them apart.
	//
	//   - row_kind "session": the daemon's live inventory name, so it tracks a
	//     rename immediately.
	//   - row_kind "entry": no session row stands behind it, so this is Claude
	//     Code's registry field, frozen when the agent started and never
	//     refreshed. It can name a session since renamed or gone — which is
	//     exactly v3 spec §2's P1, and exactly why SessionName is NOT set here
	//     and no tier may match on this field.
	//
	// "" when the agent is not in tmux at all.
	TmuxName string `json:"tmux_name"`
```

Set `TmuxName: s.Name` beside `SessionName: s.Name` in `buildSessionRecord`, and
`TmuxName: e.TmuxSessionName()` in `EntryRecord`. Leave `SessionName` alone in both.

- [ ] **Step 4: Run and verify**

Run: `go test -race -count=1 ./internal/peers/... ./internal/module/peers/... ./cmd/pdx/... `
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git commit --only internal/peers/record.go internal/peers/record_test.go \
  -m "feat(peers): carry the tmux name as display-only TmuxName"
```

---

### Task A4: `Resolve` — tiers, the shared ref helper, and the mismatch refusal

**Files:**
- Modify: `internal/peers/address.go`
- Test: `internal/peers/address_test.go`

**Interfaces:**
- Consumes: `PeerRecord.Ref`, `RoutableName`, `IsRef`.
- Produces: `ErrNameMismatch error`; `resolveRefHead(records []PeerRecord, ref string, snap ResolveSnapshot) (PeerRecord, error)`; `splitCombined(s string) (name, ref string, ok bool)`.

- [ ] **Step 1: Extend the existing `liveRow` helper**

`liveRow` at `address_test.go:21` currently takes `(canonical, label, sessionName string, pid int)` and sets no `PeerName`. Tier 1 now matches on `Agent.PeerName`, so add the parameter and update **every existing call site in the file**:

```go
// liveRow is the row shape the name and ref tiers decide on: a live cc entry
// carrying both its registry name and its ref. The label rides along and is
// deliberately never what any tier matches — that is D3, unchanged in v4.
func liveRow(ref, name, label, sessionName string, pid int) PeerRecord {
	source := ""
	if label != "" {
		source = "user"
	}
	return PeerRecord{
		SessionName: sessionName, Ref: ref, Label: label, LabelSource: source,
		Agent:       &AgentInfo{Type: "cc", PID: pid, PeerName: name},
		Deliverable: true,
	}
}
```

- [ ] **Step 2: Write the failing tests**

```go
func TestResolve_BareName(t *testing.T) {
	recs := []PeerRecord{
		liveRow("_q34psn", "purdex-b0", "", "aigora2", 1),
		liveRow("_df25d0", "nexen-f2", "", "nexen", 2),
	}
	got, err := Resolve(recs, "purdex-b0", ResolveSnapshot{})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got.Ref != "_q34psn" {
		t.Errorf("resolved %q, want the purdex-b0 row", got.Ref)
	}
}

func TestResolve_RefWithAndWithoutUnderscore(t *testing.T) {
	recs := []PeerRecord{liveRow("_q34psn", "purdex-b0", "", "aigora2", 1)}
	for _, form := range []string{"_q34psn", "q34psn"} {
		got, err := Resolve(recs, form, ResolveSnapshot{})
		if err != nil {
			t.Fatalf("Resolve(%q): %v", form, err)
		}
		if got.Ref != "_q34psn" {
			t.Errorf("Resolve(%q) landed on %q", form, got.Ref)
		}
	}
}

// RoutableName defended end to end: a six-digit registry name must not win
// the bare-ref form.
func TestResolve_RefShapedNameCannotShadow(t *testing.T) {
	recs := []PeerRecord{
		liveRow("_aaaaaa", "q34psn", "", "evil", 1),
		liveRow("_q34psn", "purdex-b0", "", "aigora2", 2),
	}
	got, err := Resolve(recs, "q34psn", ResolveSnapshot{})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got.Ref != "_q34psn" {
		t.Errorf("bare ref resolved to %q; a ref-shaped NAME shadowed it", got.Ref)
	}
}

func TestResolve_CombinedFormMatches(t *testing.T) {
	recs := []PeerRecord{liveRow("_q34psn", "purdex-b0", "", "aigora2", 1)}
	got, err := Resolve(recs, "purdex-b0 [q34psn]", ResolveSnapshot{})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got.Ref != "_q34psn" {
		t.Errorf("resolved %q", got.Ref)
	}
}

// The security property: the name in a combined address is a check digit.
func TestResolve_CombinedFormMismatchRefuses(t *testing.T) {
	recs := []PeerRecord{liveRow("_q34psn", "attacker", "", "evil", 1)}
	_, err := Resolve(recs, "trusted-name [q34psn]", ResolveSnapshot{})
	if !errors.Is(err, ErrNameMismatch) {
		t.Fatalf("Resolve err = %v, want ErrNameMismatch", err)
	}
	for _, want := range []string{"trusted-name", "attacker", "q34psn"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not name %q", err, want)
		}
	}
}

// The combined form must carry the SAME conservatisms as the bare ref tier.
func TestResolve_CombinedFormHonoursSnapshot(t *testing.T) {
	recs := []PeerRecord{liveRow("_q34psn", "purdex-b0", "", "aigora2", 1)}
	if _, err := Resolve(recs, "purdex-b0 [q34psn]", ResolveSnapshot{Partial: true, RegistryIncomplete: true}); !errors.Is(err, ErrResolveNotReady) {
		t.Errorf("err = %v, want ErrResolveNotReady", err)
	}
	empty := []PeerRecord{}
	if _, err := Resolve(empty, "purdex-b0 [q34psn]", ResolveSnapshot{Partial: true}); !errors.Is(err, ErrResolveNotReady) {
		t.Errorf("miss under Partial: err = %v, want ErrResolveNotReady", err)
	}
}

func TestResolve_AmbiguousName(t *testing.T) {
	recs := []PeerRecord{
		liveRow("_aaaaaa", "purdex-b0", "", "a", 1),
		liveRow("_bbbbbb", "purdex-b0", "", "b", 2),
	}
	var amb *AmbiguousError
	if _, err := Resolve(recs, "purdex-b0", ResolveSnapshot{}); !errors.As(err, &amb) || len(amb.Candidates) != 2 {
		t.Fatalf("err = %v, want AmbiguousError with 2 candidates", err)
	}
}

// §3.1's residual, asserted: a shared ref costs the ref form, not the name.
func TestResolve_AmbiguousRefStillReachableByName(t *testing.T) {
	recs := []PeerRecord{
		liveRow("_q34psn", "purdex-b0", "", "a", 1),
		liveRow("_q34psn", "nexen-f2", "", "b", 2),
	}
	var amb *AmbiguousError
	if _, err := Resolve(recs, "_q34psn", ResolveSnapshot{}); !errors.As(err, &amb) {
		t.Fatalf("ref err = %v, want AmbiguousError", err)
	}
	if _, err := Resolve(recs, "purdex-b0", ResolveSnapshot{}); err != nil {
		t.Errorf("name Resolve: %v, want the name to still work", err)
	}
}

func TestResolve_UnroutableNameNeverWinsTier1(t *testing.T) {
	recs := []PeerRecord{liveRow("_aaaaaa", "has/slash", "", "a", 1)}
	if _, err := Resolve(recs, "has/slash", ResolveSnapshot{}); !errors.Is(err, ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
}

func TestSplitCombined_Malformed(t *testing.T) {
	for _, s := range []string{
		"purdex-b0", "[q34psn]", "purdex-b0 [", "purdex-b0]", " [q34psn]",
		"purdex-b0 [a][b]", "purdex-b0 []",
	} {
		if _, _, ok := splitCombined(s); ok {
			t.Errorf("splitCombined(%q) reported ok; want not-combined", s)
		}
	}
}
```

- [ ] **Step 3: Run the tests and watch them fail**

Run: `go test -race -count=1 ./internal/peers/ -run 'TestResolve|TestSplitCombined'`
Expected: FAIL — `undefined: ErrNameMismatch`, `undefined: splitCombined`.

- [ ] **Step 4: Implement**

Add to `address.go`:

```go
// ErrNameMismatch is returned for the combined form "<name> [<ref>]" when the
// ref resolves to a row whose name is not the one typed.
//
// It refuses rather than delivering-with-a-warning because the name in a
// combined address is the reader's only check on the ref beside it.
// "trusted-name [attackerRef]" is precisely the string worth getting pasted,
// and a warning arrives after the message has gone. Legitimate drift has an
// explicit escape hatch: "<host>/_<ref>" asks for the ref outright.
var ErrNameMismatch = errors.New("the typed name does not match the ref's current name")

// splitCombined splits "<name> [<ref>]" into its parts. ok is false for
// anything else, including a nested or doubled bracket group — those are not
// a form this accepts, and treating them as one would let a crafted string
// choose which half gets checked.
func splitCombined(s string) (name, ref string, ok bool) {
	if !strings.HasSuffix(s, "]") {
		return "", "", false
	}
	i := strings.IndexByte(s, '[')
	if i <= 0 {
		return "", "", false
	}
	inner := s[i+1 : len(s)-1]
	if strings.ContainsAny(inner, "[]") {
		return "", "", false
	}
	name = strings.TrimSpace(s[:i])
	if name == "" || inner == "" {
		return "", "", false
	}
	return name, inner, true
}

// resolveRefHead resolves a ref with or without its leading underscore,
// applying every conservatism the bare-ref tier applies.
//
// It is shared with the combined form deliberately: a combined address
// contains a ref, and it must not acquire a weaker rule set than that same
// ref typed on its own would get.
func resolveRefHead(records []PeerRecord, ref string, snap ResolveSnapshot) (PeerRecord, error) {
	if !strings.HasPrefix(ref, "_") {
		ref = "_" + ref
	}
	if !IsRef(ref) {
		return PeerRecord{}, ErrNotFound
	}
	rec, err := resolveTier(records, ref, func(r PeerRecord) bool {
		return hasLiveEntry(r) && r.Ref == ref
	})
	if err == nil && snap.RegistryIncomplete {
		return PeerRecord{}, ErrResolveNotReady
	}
	if errors.Is(err, ErrNotFound) && snap.Partial {
		return PeerRecord{}, ErrResolveNotReady
	}
	return rec, err
}
```

Rewrite `Resolve`'s body after the `cc:` / `tmux:` switch:

```go
	// The stale-version gate sits ABOVE every tier, not before the fallback.
	// In v3 it guarded a tier-1 miss because the only thing under it was a
	// tmux-name guess. Here the tier directly under it would SUCCEED: a v3 row
	// carries a usable name in agent.peer_name, so a bare name would resolve
	// against a row whose ref the sender could never have verified. One stale
	// row condemns the batch — Resolve is called per host, so every row in it
	// came from the same daemon. "tmux:<name>" is decided above this and stays
	// available; it is the escape hatch the refusal points the caller at.
	if hasStaleVersionRows(records) {
		return PeerRecord{}, fmt.Errorf("%w: %w", ErrNotFound, ErrRemoteTooOld)
	}

	if name, ref, ok := splitCombined(session); ok {
		rec, err := resolveRefHead(records, ref, snap)
		if err != nil {
			return PeerRecord{}, err
		}
		if rec.Agent.PeerName != name {
			return PeerRecord{}, fmt.Errorf("%w: typed %q, but %s is now %q",
				ErrNameMismatch, name, ref, rec.Agent.PeerName)
		}
		return rec, nil
	}

	// Tier 1: the registry name, over live rows whose name is routable. The
	// RoutableName guard is not decoration — an unroutable name must not win a
	// tier, because it could never have produced the address being typed.
	rec, err := resolveTier(records, session, func(r PeerRecord) bool {
		return hasLiveEntry(r) && RoutableName(r.Agent.PeerName) && r.Agent.PeerName == head
	})
	if !errors.Is(err, ErrNotFound) {
		return rec, err
	}

	// Tiers 2/3: the ref, with or without its underscore. No ordering rule is
	// needed against tier 1: RoutableName forbids a ref-shaped name, so no row
	// can match both.
	if rest == "" {
		rec, err = resolveRefHead(records, head, snap)
		if !errors.Is(err, ErrNotFound) {
			return rec, err
		}
	}

	if snap.Partial {
		return PeerRecord{}, ErrResolveNotReady
	}
	// Tier 4: bare tmux session name, complete inventory only.
	if rest != "" {
		return PeerRecord{}, ErrNotFound
	}
	return resolveTier(records, session, func(r PeerRecord) bool { return r.SessionName == head })
```

Rewrite `Resolve`'s doc comment to describe these tiers — the existing one describes v3's and is now actively misleading.

- [ ] **Step 5: Run the tests and verify they pass**

Run: `go test -race -count=1 ./internal/peers/ -v`
Expected: PASS. Pre-existing v3 tier tests (`TestResolve_CanonicalTier`, `TestResolve_LabelDoesNotResolve`, …) need their `liveRow` calls updated for the new parameter and their expectations moved from 8-digit to 6-digit refs. Do not delete a conservatism test to make it pass.

- [ ] **Step 6: Commit**

```bash
git commit --only internal/peers/address.go internal/peers/address_test.go \
  -m "feat(peers): resolve by name then ref, and refuse a combined form whose name drifted"
```

---

### Task A6: Stale-version refusal, precisely

**Files:**
- Modify: `internal/peers/address.go` (`hasPreV3Rows`)
- Test: `internal/peers/address_test.go`

**Interfaces:**
- Consumes: `hasLiveEntry`.
- Produces: `hasStaleVersionRows(records []PeerRecord) bool`, replacing `hasPreV3Rows`.

Task A4 already calls `hasStaleVersionRows`; this task renames the function to match and pins the precision with tests.

- [ ] **Step 1: Write the failing tests**

```go
func TestStaleVersion_V3BatchRefusesEveryForm(t *testing.T) {
	// A v3 daemon sent "canonical"; a v4 decoder reads "ref", so Ref is empty
	// while the row is otherwise a live, usable cc row.
	recs := []PeerRecord{{
		SessionName: "aigora2",
		Agent:       &AgentInfo{Type: "cc", PID: 42, PeerName: "purdex-b0"},
	}}
	for _, form := range []string{"purdex-b0", "_q34psn", "q34psn", "purdex-b0 [q34psn]"} {
		if _, err := Resolve(recs, form, ResolveSnapshot{}); !errors.Is(err, ErrRemoteTooOld) {
			t.Errorf("Resolve(%q) err = %v, want ErrRemoteTooOld", form, err)
		}
	}
	// The escape hatch the refusal names must still work.
	if _, err := Resolve(recs, "tmux:aigora2", ResolveSnapshot{}); err != nil {
		t.Errorf("tmux form: %v, want it to resolve", err)
	}
}

// Each of these legitimately carries an empty Ref on a CURRENT daemon.
func TestStaleVersion_V4RowsNotMisjudged(t *testing.T) {
	for _, tc := range []struct {
		name string
		rec  PeerRecord
	}{
		{"owner fallback", PeerRecord{Agent: &AgentInfo{Type: "cc", PID: 0}, Reason: "inbox_dead"}},
		{"proxy", PeerRecord{Agent: &AgentInfo{Type: "proxy", PID: 7}, Reason: "proxy"}},
		{"agent null", PeerRecord{Agent: nil, Reason: "no_agent", SessionName: "aigora3"}},
	} {
		_, err := Resolve([]PeerRecord{tc.rec}, "nobody", ResolveSnapshot{})
		if errors.Is(err, ErrRemoteTooOld) {
			t.Errorf("%s: judged stale; want a plain miss", tc.name)
		}
	}
}
```

- [ ] **Step 2: Run the tests and watch them fail**

Run: `go test -race -count=1 ./internal/peers/ -run TestStaleVersion`
Expected: FAIL — `undefined: hasStaleVersionRows` if A4 left it unrenamed, or a compile error on the old name.

- [ ] **Step 3: Rename and re-comment**

```go
// hasStaleVersionRows reports whether records came from a daemon predating
// Peer Address v4 — the condition behind ErrRemoteTooOld.
//
// v4 gets the signal for free: the JSON key moved from "canonical" to "ref",
// so a v3 daemon's rows decode with Ref == "" whatever they actually sent.
//
// The hasLiveEntry conjunction is load-bearing, not caution. A CURRENT daemon
// also emits rows with an empty Ref — owner-fallback rows (inbox_dead /
// ambiguous, PID 0), proxy rows and agent:null rows — and counting those would
// declare every v4 host obsolete.
//
// Refusing matters more here than it did in v3, where what lay below was a
// tmux-name guess. A v3 row still carries a usable registry name in
// agent.peer_name, so v4's name tier would SUCCEED against it, delivering to a
// row whose ref the sender could never have verified.
func hasStaleVersionRows(records []PeerRecord) bool {
	for _, r := range records {
		if hasLiveEntry(r) && r.Ref == "" {
			return true
		}
	}
	return false
}
```

Delete `hasPreV3Rows` and any leftover call to it.

- [ ] **Step 4: Run the whole package**

Run: `go test -race -count=1 ./internal/peers/ -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git commit --only internal/peers/address.go internal/peers/address_test.go \
  -m "fix(peers): refuse a pre-v4 batch without misjudging v4's own ref-less rows"
```

---

### Task A5: Wire contract

**Files:**
- Modify: `internal/peers/wire.go`
- Test: `internal/peers/wire_test.go`

**Interfaces:**
- Consumes: `IsRef`, `ValidSuffix` (kept).
- Produces: `ValidateWireAddress` accepting the matrix below; `legacyV3Head`; `isLegacyV2Head` and `legacyV2HeadPattern` **deleted**.

**Two things Task A1 left for you, found while it ran:**

1. **`isLegacyV2Head` is now the same predicate as `IsRef`.** v2's default label was `"_"` plus exactly 6 base36 digits (`wire.go:473`) — the identical shape v4 gives a ref. Delete `isLegacyV2Head` and `legacyV2HeadPattern` and let `IsRef` cover both; keeping two names for one regex leaves a comment that contradicts the code. Say so where `IsRef` is used:

   ```go
   // IsRef also covers v2's legacy head: v2's default label was "_" plus six
   // base36 digits, the same shape a v4 ref has. That is a coincidence of
   // format, not of meaning, and it is harmless here because this function
   // only checks grammar. Routing tells them apart — a v2 or v3 peer's rows
   // carry no ref at all, so Resolve refuses the whole batch (spec §8.3)
   // rather than matching one.
   ```

2. **Between A1 and this task, an 8-digit v3 head on the wire is rejected** with `bad_address`, because A1 narrowed `IsCanonicalID` to 6 and nothing else accepted 8. `legacyV3Head` below is what closes that window. It never reached a released build — the whole branch merges at once — but do not reorder this task after C, and do not "simplify" `legacyV3Head` away.

`ValidateWireAddress`'s doc comment (`wire.go:491-498`) describes v3's three head classes and is wrong after this change. Rewrite it against the matrix.

- [ ] **Step 1: Write the failing test**

```go
func TestValidateWireAddress_Matrix(t *testing.T) {
	for _, tc := range []struct {
		name, addr string
		wantOK     bool
	}{
		{"v4 ref", "_q34psn", true},
		{"v3 canonical, one release", "_q34psn4f", true},
		{"v3 canonical with suffix", "_q34psn4f:aigora2-purdex-b0", true},
		{"v2 default head is ref-shaped, covered by IsRef", "_abc123", true},
		{"v2 user label head", "purdex-tester", true},
		{"v1 empty", "", true},
		{"garbage head", "has/slash", false},
		{"legacy suffix must still be validated", "_q34psn4f:bad suffix", false},
		{"bracket form is not a wire address", "purdex-b0 [q34psn]", false},
	} {
		err := ValidateWireAddress(tc.addr)
		if gotOK := err == nil; gotOK != tc.wantOK {
			t.Errorf("%s: ValidateWireAddress(%q) err = %v, wantOK %v", tc.name, tc.addr, err, tc.wantOK)
		}
	}
}
```

- [ ] **Step 2: Run the test and watch it fail**

Run: `go test -race -count=1 ./internal/peers/ -run TestValidateWireAddress_Matrix`
Expected: FAIL on the `_q34psn` and bracket rows.

- [ ] **Step 3: Implement**

```go
// legacyV3Head matches the 8-digit canonical id v3 used as an address head.
//
// TODO(v5): delete this arm, isLegacyV2Head and their rows in
// TestValidateWireAddress_Matrix once every peer runs v4 or later. Both are
// accepted for exactly one release so a single upgrade window does not have to
// carry two incompatibilities at once.
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
	// A v4 sender sets no suffix, but a legacy one does, and a legacy suffix is
	// still validated rather than waved through: relaxing a receiver's grammar
	// while retiring a sender's is how a field stops being checked at all.
	if strings.Contains(s, ":") && !ValidSuffix(rest) {
		return fmt.Errorf("%w: suffix must match %s", ErrAddressInvalid, suffixWirePattern)
	}
	return nil
}
```

Keep `ValidSuffix` and `suffixWirePattern`. Update `WireFrom.Address`'s comment to say a v4 sender sets `_<ref>` and no suffix.

- [ ] **Step 4: Run and verify**

Run: `go test -race -count=1 ./internal/peers/ -run 'TestValidateWireAddress|TestDeliverRequest' -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git commit --only internal/peers/wire.go internal/peers/wire_test.go \
  -m "feat(peers): accept a 6-digit ref on the wire, keeping v2/v3 heads for one release"
```

---

### Task A7: `pdx peers` table

**Files:**
- Modify: `cmd/pdx/peers.go`
- Test: `cmd/pdx/peers_test.go`

**Interfaces:**
- Consumes: `PeerRecord.Ref`, `.Address`, `.SessionName`.
- Produces: `displayAddress(rec peers.PeerRecord) string`; columns `TITLE ADDRESS AGENT STATUS DELIVERABLE TMUX CWD` (`HOST` leads in `--all`).

`formatPeersTable` **takes `peers.Envelope` and returns a string** — it does not take a writer. The header is rendered as `TITLE` even though the Go field is still `Label` until Task B2; rendering the right word here keeps the golden fixtures from churning twice.

- [ ] **Step 1: Write the failing test**

```go
func TestFormatPeersTable_V4Columns(t *testing.T) {
	env := peers.Envelope{OK: true, Peers: []peers.PeerRecord{
		{Address: "mlab/purdex-b0", Ref: "_q34psn", SessionName: "aigora2", Cwd: "~",
			Agent: &peers.AgentInfo{Type: "cc", Status: "idle"}, Deliverable: true},
		{Address: "mlab/purdex-53", Ref: "_d8dc4a", SessionName: "purdex7", Cwd: "~",
			Label: "Purdex Tester 01",
			Agent: &peers.AgentInfo{Type: "cc", Status: "busy"}, Deliverable: true},
		{Address: "mlab/_df25d0", Ref: "_df25d0", SessionName: "nexen", Cwd: "~",
			Agent: &peers.AgentInfo{Type: "cc", Status: "idle"}, Reason: "inbox_dead"},
		{Address: "mlab/tmux:aigora3", SessionName: "aigora3", Cwd: "~", Reason: "no_agent"},
	}}
	got := formatPeersTable(env)

	header := strings.SplitN(got, "\n", 2)[0]
	if !strings.HasPrefix(header, "TITLE\tADDRESS\tAGENT\tSTATUS\tDELIVERABLE\tTMUX\tCWD") {
		t.Errorf("header = %q", header)
	}
	for _, want := range []string{
		"mlab/purdex-b0 [q34psn]", "Purdex Tester 01", "inbox_dead",
		"mlab/tmux:aigora3", "aigora2",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("table lacks %q:\n%s", want, got)
		}
	}
	// A row whose address IS the ref must not get a redundant bracket.
	if strings.Contains(got, "mlab/_df25d0 [df25d0]") {
		t.Errorf("ref-address row got a redundant bracket:\n%s", got)
	}
	if strings.Contains(header, "NAME") || strings.Contains(header, "HOST") {
		t.Errorf("dropped column still present: %q", header)
	}
}
```

- [ ] **Step 2: Run the test and watch it fail**

Run: `go test -race -count=1 ./cmd/pdx/ -run TestFormatPeersTable_V4Columns`
Expected: FAIL on the header and the bracketed address.

- [ ] **Step 3: Implement**

```go
// displayAddress renders a row's address for a human: "<host>/<name> [<ref>]".
//
// The ref prints without its leading underscore — the bracket already
// separates it — and prints on EVERY row rather than only ambiguous ones: a
// reader who has to go looking for it when a name stops working has to look
// somewhere other than where they were already reading.
//
// A row whose address is already the ref (an unroutable name, or no name at
// all) gets no bracket; repeating it would suggest two different identifiers.
func displayAddress(rec peers.PeerRecord) string {
	if rec.Ref == "" || strings.HasSuffix(rec.Address, "/"+rec.Ref) {
		return rec.Address
	}
	return rec.Address + " [" + strings.TrimPrefix(rec.Ref, "_") + "]"
}
```

Change both header lines (`:373` single-host, `:461` `--all`) and both row loops: drop `HOST` from the single-host form and `NAME` from both, add `TMUX` before `CWD`, and pass every address through `displayAddress`. Leave `deliverableField` unchanged.

`TMUX` renders `TmuxName` (Task A3c), **not** `SessionName` — the latter is empty on entry rows. Mark an entry row's value, because it is frozen and may name a session that no longer exists:

```go
// tmuxField renders the TMUX cell. An entry row's tmux name comes from the
// registry file, frozen when the agent started, so it can name a session
// since renamed or gone; a session row's comes from the live inventory. The
// '?' is the difference, and a reader deciding where to attach is the one who
// needs to know it.
func tmuxField(rec peers.PeerRecord) string {
	if rec.TmuxName == "" {
		return "-"
	}
	if rec.RowKind == "entry" {
		return rec.TmuxName + "?"
	}
	return rec.TmuxName
}
```

Add a row to `TestFormatPeersTable_V4Columns`'s fixture with `RowKind: "entry"`, `TmuxName: "aigora2"` and assert the table contains `aigora2?`.

- [ ] **Step 4: Run and verify**

Run: `go test -race -count=1 ./cmd/pdx/ -run TestFormatPeers -v`
Expected: PASS. The golden fixture `wantPeersTable` (`peers_test.go:22-84`) pins the old columns — update it to the new ones.

- [ ] **Step 5: Commit**

```bash
git commit --only cmd/pdx/peers.go cmd/pdx/peers_test.go \
  -m "feat(pdx): render <host>/<name> [ref] and surface the tmux session"
```

---

### Task A8: `pdx msg send` input forms and the `whoami` block

**Files:**
- Modify: `cmd/pdx/msg.go`, `internal/peers/wire.go` (one error-code constant), `internal/module/peers/send.go`
- Test: `cmd/pdx/msg_test.go`, `internal/module/peers/send_test.go`

**Interfaces:**
- Consumes: A4's `ErrNameMismatch` and the three input forms.
- Produces: `ErrCodeNameMismatch = "name_mismatch"` in `wire.go`, returned by `/send` with the refusal's detail intact.

**Task A4 found that the refusal is currently invisible, and fixing that is part of this task.**
`ErrNameMismatch` falls into `send.go`'s `default:` arm, which answers 404 `peer_not_found` with a
generic "no session %q on %q" — discarding the typed name, the resolved name and the ref. Those three
facts are the entire point: they are how an operator tells "the peer renamed itself" from "someone
handed me a doctored address". A refusal nobody can read is not a refusal.

- [ ] **Step 0: Surface the mismatch through `/send`**

Add beside the other codes in `wire.go`:

```go
	// ErrCodeNameMismatch is /send's answer when Resolve came back with
	// ErrNameMismatch: the combined form's name is not the ref's current name.
	// The detail carries all three values, because distinguishing a peer that
	// renamed itself from an address someone doctored is the operator's call
	// and they cannot make it from the code alone.
	ErrCodeNameMismatch = "name_mismatch"
```

In `send.go`'s Resolve-error switch, add a case ahead of `default:` that maps `ErrNameMismatch` to
409 with `err.Error()` as the detail — the same status the ambiguous case uses, since both mean
"your address was understood and refused", not "not found".

Add a `send_test.go` case asserting the code and that the detail contains all three values.

- [ ] **Step 0b: Add the CLI's rendering**

`renderMsgAPIError` (`cmd/pdx/msg.go:355`) must print the detail for this code rather than a bare
`name_mismatch`, and say what to do: re-read the address, or use `<host>/_<ref>` if the rename was
expected.

`runMsgSend` is `runMsgSend(inv msgInvocation, getenv func(string) string, stdout, stderr io.Writer) int` — it takes a `getenv`, not a URL. Follow the existing send tests in `msg_test.go` for how they stand up a server and point the config at it; do not invent a `fakeSendServer`.

- [ ] **Step 1: Write the failing tests**

```go
// The CLI must not strip, split or normalise the bracket form: Resolve checks
// the typed name against the ref, and a CLI that dropped the name would
// silently disable that check.
func TestParseMsgInvocation_KeepsCombinedAddressVerbatim(t *testing.T) {
	inv, ok := parseMsgInvocation([]string{"send", "mlab/purdex-b0 [q34psn]", "hi"})
	if !ok {
		t.Fatal("parseMsgInvocation returned !ok")
	}
	if inv.target != "mlab/purdex-b0 [q34psn]" {
		t.Errorf("target = %q, want the address verbatim", inv.target)
	}
}

func TestRenderSelfRecord_UnsetTitle(t *testing.T) {
	var buf bytes.Buffer
	renderSelfRecord(peers.PeerRecord{
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

func TestRenderSelfRecord_SetTitle(t *testing.T) {
	var buf bytes.Buffer
	renderSelfRecord(peers.PeerRecord{
		Address: "mlab/purdex-53", Ref: "_d8dc4a", Label: "Purdex Tester 01",
		LabelSource: "user", LabelRev: 4,
	}, &buf)
	if got := buf.String(); !strings.Contains(got, "Purdex Tester 01 (user, rev 4)") {
		t.Errorf("set title block:\n%s", got)
	}
}
```

Check `msgInvocation`'s actual field name for the address argument before writing `inv.target`; use whatever it is called.

- [ ] **Step 2: Run the tests and watch them fail**

Run: `go test -race -count=1 ./cmd/pdx/ -run 'TestParseMsgInvocation_KeepsCombined|TestRenderSelfRecord'`
Expected: FAIL — the whoami block prints `label:       (, rev 0)`.

- [ ] **Step 3: Implement**

In `renderSelfRecord` (`msg.go:653`) replace the canonical and label lines:

```go
	fmt.Fprintf(stdout, "address:    %s\n", sanitizeCell(rec.Address))
	fmt.Fprintf(stdout, "ref:        %s\n", sanitizeCell(rec.Ref))
	if rec.Label == "" {
		fmt.Fprintln(stdout, "title:      (none)")
	} else {
		fmt.Fprintf(stdout, "title:      %s (%s, rev %d)\n",
			sanitizeCell(rec.Label), sanitizeCell(rec.LabelSource), rec.LabelRev)
	}
```

Confirm the send path passes its address argument through unchanged. If any trimming or splitting exists, remove it and say why in a comment: the bracket form's name is a check digit and the CLI is not entitled to drop it.

Update `msgUsage` to show the three forms, replacing the `_3k9f2mq4` example.

- [ ] **Step 4: Run and verify**

Run: `go test -race -count=1 ./cmd/pdx/ -v`
Expected: PASS. `msg_test.go:223` asserts the usage text contains `_3k9f2mq4` — update it to the v4 forms.

- [ ] **Step 5: Commit**

```bash
git commit --only cmd/pdx/msg.go cmd/pdx/msg_test.go \
  -m "feat(pdx): pass the combined address through verbatim and fix whoami's empty title"
```

---

### Task A9: SPA status bar — display readable, copy exact

**Files:**
- Modify: `spa/src/components/StatusBar.tsx`
- Test: `spa/src/components/StatusBar.test.tsx`

**Interfaces:**
- Consumes: A3b's `PeerRow.ref`.
- Produces: no new exports.

Open `StatusBar.test.tsx` first and reuse whatever peer-seeding fixture it already has. Do not invent `renderStatusBarWithPeer` if the file seeds the store another way.

- [ ] **Step 1: Write the failing test**

Following the file's existing rendering idiom, add:

```tsx
it('shows the name with its ref and copies the exact form', async () => {
  // seed a peer row: address 'mlab/purdex-b0', ref '_q34psn', label ''
  // (use this file's existing seeding helper)
  expect(screen.getByText(/purdex-b0 \[q34psn\]/)).toBeInTheDocument()
  await userEvent.click(screen.getByText(/purdex-b0 \[q34psn\]/))
  expect(await navigator.clipboard.readText()).toBe('mlab/purdex-b0 [q34psn]')
})

it('renders a peer that has no title', () => {
  // same seed, label: '' — the row must still appear
  expect(screen.queryByText(/purdex-b0/)).not.toBeNull()
})
```

- [ ] **Step 2: Run the test and watch it fail**

Run: `cd spa && npx vitest run src/components/StatusBar.test.tsx`
Expected: FAIL — the component returns early at `StatusBar.tsx:140` because `row.label === ''`.

- [ ] **Step 3: Implement**

Replace the `row.label === ''` guard with one on `row.ref === '' && row.address === ''`. A title never identified a row and under v4 is usually absent; the name always identifies it.

Render `` `${name} [${ref.replace(/^_/, '')}]` `` where `name` is the address's segment after the first `/`, and set the copy value to `` `${address} [${ref.replace(/^_/, '')}]` ``. When `ref` is empty or the address already ends in `/${ref}`, render and copy the address unchanged.

Add the comment, because the asymmetry reads as an inconsistency otherwise:

```tsx
// Display drops the host; the clipboard keeps it, and keeps the ref. What
// gets copied is what gets pasted into a handoff and used hours later —
// exactly the window in which a name drifts or is taken by someone else. The
// prettier string is the weaker one; it does not belong on the clipboard.
```

Render the title, when set, beside the name rather than instead of it.

- [ ] **Step 4: Run and verify**

Run: `cd spa && npx vitest run src/components/StatusBar.test.tsx && pnpm run lint`
Expected: PASS, clean lint.

- [ ] **Step 5: Commit**

```bash
git commit --only spa/src/components/StatusBar.tsx spa/src/components/StatusBar.test.tsx \
  -m "feat(spa): show the name with its ref, copy the address with its ref"
```

---

## Phase B — `label` becomes `title`

### Task B1: `ValidateTitle` and the normalized compare

**Files:**
- Modify: `internal/peers/ref.go`
- Test: `internal/peers/ref_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces: `ErrTitleInvalid error`, `ValidateTitle(s string) error`, `NormalizeTitle(s string) string`. `ValidateUserLabel` **stays** — Task A5's wire arm still calls it.

Imports needed in `ref.go`: `strings`, `unicode`, `unicode/utf8` (in addition to what is there).

- [ ] **Step 1: Write the failing tests**

```go
func TestValidateTitle_Accepts(t *testing.T) {
	for _, s := range []string{
		"Purdex Tester 01", "purdex-tester", "測試 01", "cc", "tmux", strings.Repeat("a", 64),
	} {
		if err := ValidateTitle(s); err != nil {
			t.Errorf("ValidateTitle(%q) = %v, want nil", s, err)
		}
	}
}

func TestValidateTitle_Rejects(t *testing.T) {
	for _, s := range []string{
		"", strings.Repeat("a", 65), "has\ttab", "has\nnewline", "esc\x1b[31m",
	} {
		if err := ValidateTitle(s); err == nil {
			t.Errorf("ValidateTitle(%q) = nil, want an error", s)
		}
	}
}

// The limit is BYTES, not runes: storage and the wire both measure bytes.
func TestValidateTitle_ByteBoundary(t *testing.T) {
	if err := ValidateTitle(strings.Repeat("測", 21)); err != nil { // 63 bytes
		t.Errorf("63 bytes rejected: %v", err)
	}
	if err := ValidateTitle(strings.Repeat("測", 22)); err == nil { // 66 bytes
		t.Error("66 bytes accepted, want rejected")
	}
}

func TestNormalizeTitle(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"Purdex Tester", "purdex tester"},
		{"purdex  tester", "purdex tester"},
		{"  Purdex\tTester  ", "purdex tester"},
	} {
		if got := NormalizeTitle(tc.in); got != tc.want {
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

// ErrTitleInvalid is returned by ValidateTitle.
var ErrTitleInvalid = errors.New("title invalid")

// ValidateTitle applies the title rule: 1..64 bytes of printable UTF-8.
//
// There are no reserved words. A title reaches nothing — Resolve never
// consults one — so there is nothing for "cc" or "tmux" to shadow, and
// refusing them would make the field harder to use than it is dangerous.
//
// Control characters are refused because a title is printed into a terminal
// table and into peer warnings; an ANSI escape in one is a way to rewrite
// somebody else's screen.
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

// NormalizeTitle is the form two titles are compared in when deciding whether
// to warn: case-folded, runs of whitespace collapsed, ends trimmed.
//
// "Purdex Tester" and "purdex  tester" name the same thing to a reader, so
// they must collide for the warning too — otherwise the warning misses exactly
// the near-duplicates it exists for.
func NormalizeTitle(s string) string {
	return strings.Join(strings.Fields(strings.ToLower(s)), " ")
}
```

`unicode.IsPrint` already reports false for `\t` and `\n`, which is what makes the two functions consistent: anything `NormalizeTitle` would collapse as whitespace beyond a plain space is refused before it is ever stored.

- [ ] **Step 4: Run and verify**

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
- Rename: `internal/module/peers/labels.go` → `titles.go`; `labels_test.go` → `titles_test.go`
- Modify: `internal/peers/record.go`, `internal/module/peers/*.go`, `internal/store/peer_label.go`, `cmd/pdx/peers.go`, `cmd/pdx/msg.go`, **`cmd/pdx/main.go`** (`:259` declares `var labels peersmod.LabelStore`)
- Test: the renamed tests

**Interfaces:**
- Consumes: `ValidateTitle`, `NormalizeTitle`.
- Produces: `PeerRecord.Title/TitleSource/TitleRev` (json `title`, `title_source`, `title_rev`); `Envelope.TitlesUnavailable` (json `titles_unavailable`); warning code `title_in_use` with `LiveTitles []string`.

**The SQLite table keeps its name.** `peer_labels` on disk is untouched — renaming it would be a migration, and alpha carries none. Only Go symbols and JSON keys move.

- [ ] **Step 1: Enumerate the blast radius**

```bash
grep -rln 'LabelSource\|LabelRev\|LabelInfo\|LabelStore\|ValidateUserLabel\|label_in_use\|live_labels\|labels_unavailable\|\.Label\b' --include='*.go' . | sort | tee /tmp/b2-files.txt
```

Confirm `cmd/pdx/main.go` is in the list. That file is easy to miss because it only names the type once.

- [ ] **Step 2: Write the failing test**

Open `internal/module/peers/labels_test.go` and reuse its existing module fixture — do not invent `newTestModule`/`mustClaimTitle` if it builds the module some other way. Add, in that file's idiom:

```go
// A duplicate title warns and succeeds; both holders keep their title.
// Normalisation is what makes the warning useful: "Purdex Tester" and
// "purdex  tester" are the same name to the person reading them.
func TestClaimTitle_WarnsOnNormalizedDuplicate(t *testing.T) {
	// 1. claim "Purdex Tester" for one sessionID
	// 2. claim "purdex  tester" for another
	// 3. assert the second response's warning code is "title_in_use"
	// 4. assert the warning's LiveTitles is non-empty
	// 5. assert the first holder's title is still "Purdex Tester"
}
```

Fill the body using the fixture the file already provides.

- [ ] **Step 3: Rename mechanically, then wire the normalized compare**

```bash
git mv internal/module/peers/labels.go internal/module/peers/titles.go
git mv internal/module/peers/labels_test.go internal/module/peers/titles_test.go
```

Apply throughout the Step 1 list: `LabelSourceUser`→`TitleSourceUser`, `LabelInfo`→`TitleInfo`, `LabelStore`→`TitleStore`, `PeerRecord.Label/LabelSource/LabelRev`→`Title/TitleSource/TitleRev`, `applyIdentity`'s `info.Label`→`info.Title`, `label_in_use`→`title_in_use`, `live_labels`→`live_titles`, `labels_unavailable`→`titles_unavailable`. At the **claim path only**, `ValidateUserLabel`→`ValidateTitle`; the wire's legacy arm keeps calling `ValidateUserLabel`.

In the claim path, compare through `NormalizeTitle` on both sides when computing the warning's holders.

Change nothing else. A behaviour change hidden inside a rename this size is not reviewable.

- [ ] **Step 4: Run everything**

Run: `go test -race -count=1 ./...`
Expected: PASS except the known `internal/module/agent` flake (#1092) — rerun that package alone to confirm it is the flake.

- [ ] **Step 5: Commit**

```bash
git commit --only $(cat /tmp/b2-files.txt | tr '\n' ' ') \
  internal/module/peers/titles.go internal/module/peers/titles_test.go \
  -m "refactor(peers): rename label to title, now that it names rather than tags"
```

Verify: `git show --stat HEAD` must not list `pdx`.

---

### Task B3: SPA title rename

**Files:**
- Modify: the SPA files referencing peer `label`/`labelSource`/`labelsUnavailable`
- Test: their co-located tests

**Interfaces:**
- Consumes: B2's JSON keys.
- Produces: `PeerRow.title/.titleSource/.titleRev`; `PeerEnvelopeFlags.titlesUnavailable`.

- [ ] **Step 1: Rename in the store first**

In `usePeerStore.ts`: `label`→`title`, `labelSource`→`titleSource`, `labelRev`→`titleRev`, `labelsUnavailable`→`titlesUnavailable`, and the reads `p.label`/`p.label_source`/`env.labels_unavailable` to the new keys. Rename the i18n key `peer.labels_unavailable_note`→`peer.titles_unavailable_note` in the locale files too.

- [ ] **Step 2: Let the type checker find the rest**

Run: `cd spa && pnpm run build`
Expected: FAIL, listing every consumer. Fix each by rename only.

- [ ] **Step 3: Run the suite**

Run: `cd spa && npx vitest run && pnpm run lint && pnpm run build`
Expected: PASS, clean lint, successful build.

- [ ] **Step 4: Commit**

List the exact files you touched:

```bash
git commit --only spa/src/stores/usePeerStore.ts spa/src/stores/usePeerStore.test.ts \
  <every other spa file you edited> \
  -m "refactor(spa): follow the label to title rename"
```

---

## Phase C — a host publishes its own alias

### Task C1: `Envelope.Alias`

**Files:**
- Modify: `internal/peers/envelope.go`, `internal/module/peers/module.go` (`localEnvelope`), `internal/module/peers/hosts.go` (`verifyHost` at `:199`)
- Test: `internal/peers/envelope_test.go` (**JSON shape only**), `internal/module/peers/hosts_test.go` (`verifyHost` behaviour)

**Interfaces:**
- Consumes: `config.ValidateAlias`, `config.Config.PeerAlias()`.
- Produces: `Envelope.Alias string` (json `alias`); `sanitizeLearnedAlias(alias, localAlias string) string` in `internal/module/peers`.

`localEnvelope` and the module fixtures live in `internal/module/peers`, so their tests belong there — an `internal/peers` test cannot reach them.

- [ ] **Step 1: Write the failing tests**

In `internal/peers/envelope_test.go` (`package peers`):

```go
func TestEnvelope_CarriesAliasKey(t *testing.T) {
	b, err := json.Marshal(Envelope{HostID: "mlab:278cbm", Alias: "mlab"})
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	if m["alias"] != "mlab" {
		t.Errorf("alias key = %v, want %q", m["alias"], "mlab")
	}
}
```

In `internal/module/peers/hosts_test.go`, using that file's existing module fixture:

```go
// A peer's self-reported alias is attacker-controlled, exactly like host_id.
func TestSanitizeLearnedAlias_RejectsUnsafe(t *testing.T) {
	for _, bad := range []string{"", "has/slash", "..", "esc\x1b[31m", strings.Repeat("a", 65), "mlab"} {
		if got := sanitizeLearnedAlias(bad, "mlab"); got != "" {
			t.Errorf("alias %q survived as %q", bad, got)
		}
	}
	if got := sanitizeLearnedAlias("air26", "mlab"); got != "air26" {
		t.Errorf("safe alias = %q, want %q", got, "air26")
	}
}
```

(`"mlab"` is in the reject list because `ValidateAlias` refuses an alias equal to the local one.)

- [ ] **Step 2: Run the tests and watch them fail**

Run: `go test -race -count=1 ./internal/peers/ ./internal/module/peers/ -run 'TestEnvelope_CarriesAliasKey|TestSanitizeLearnedAlias'`
Expected: FAIL — unknown field / undefined function.

- [ ] **Step 3: Implement**

In `envelope.go`, add to `Envelope`:

```go
	// Alias is what this host calls ITSELF (config PeerAlias()). A reader uses
	// it to name a newly paired peer the way that peer names itself, so an
	// address means the same string on both machines. It is self-reported and
	// therefore attacker-controlled: validate before storing, exactly as
	// host_id already is.
	Alias string `json:"alias"`
```

Set it in `localEnvelope` from the alias already passed in. In `hosts.go`, beside `validHostID`:

```go
// sanitizeLearnedAlias returns alias when it is safe to adopt as a local name
// for a peer, or "" when it is not. Same posture as validHostID: a peer's
// self-report is data, never a decision.
func sanitizeLearnedAlias(alias, localAlias string) string {
	if config.ValidateAlias(alias, localAlias) != nil {
		return ""
	}
	return alias
}
```

- [ ] **Step 4: Run and verify**

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
- Modify: `internal/module/peers/hosts.go` (`handleAddHost`, `:250-330`), `cmd/pdx/peers.go` (make the alias argument optional)
- Test: `internal/module/peers/hosts_test.go`, `cmd/pdx/peers_test.go`

**Interfaces:**
- Consumes: `sanitizeLearnedAlias`, `Envelope.Alias`.
- Produces: `addHostRequest.Alias` optional; 409 naming the colliding alias; 400 when neither side supplies one.

- [ ] **Step 1: Write the failing tests**

Using `hosts_test.go`'s existing fake-peer and module fixtures, add four cases:

| test | setup | assert |
|---|---|---|
| `TestAddHost_AdoptsPublishedAlias` | remote envelope `{HostID: "air:aaa", Alias: "air26"}`, request with no alias | stored/returned alias is `air26` |
| `TestAddHost_ExplicitAliasWins` | same remote, request alias `air` | stored alias is `air` |
| `TestAddHost_PublishedAliasCollisionIs409` | local config already has a host named `air26`; remote publishes `air26`, request has no alias | status 409, body contains `air26` |
| `TestAddHost_NoAliasAnywhereIs400` | remote publishes `""`, request has no alias | status 400 |

Write each out in the file's existing idiom rather than inventing helpers.

- [ ] **Step 2: Run the tests and watch them fail**

Run: `go test -race -count=1 ./internal/module/peers/ -run TestAddHost`
Expected: FAIL — the alias is currently required and never learned.

- [ ] **Step 3: Implement**

Reorder `handleAddHost` so `verifyHost` runs **before** the alias checks when `req.Alias == ""`, making the published alias available as a fallback. Then:

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
	// Not auto-suffixed: "air26-2" would be unportable in a new way, which is
	// the problem this phase exists to remove. The operator picks.
	if aliasTaken(alias) {
		writeJSONError(w, http.StatusConflict, fmt.Sprintf(
			"alias %q is already used by another host; pass an explicit alias for this one", alias))
		return
	}
```

Keep every existing guard — the admin-token check, the self-pairing check, and the re-check inside `UpdateConfig`. Adding a fallback must not remove a gate. Note that when `req.Token == ""` there is no `verifyHost` and therefore no published alias; that path still requires an explicit alias.

In `cmd/pdx/peers.go`, accept `pdx peers host add <url>` with the alias omitted and update `peersUsage`.

- [ ] **Step 4: Run and verify**

Run: `go test -race -count=1 ./internal/module/peers/ ./cmd/pdx/ -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git commit --only internal/module/peers/hosts.go internal/module/peers/hosts_test.go \
  cmd/pdx/peers.go cmd/pdx/peers_test.go \
  -m "feat(peers): adopt a peer's published alias when none is given"
```

---

### Task C3: Surface alias drift in `pdx peers --all`

**Files:**
- Modify: `internal/peers/envelope.go` (`HostResult`), `internal/module/peers/module.go` (`fetchHostResult`), `cmd/pdx/peers.go` (the `--all` renderer)
- Test: `cmd/pdx/peers_test.go`

**Interfaces:**
- Consumes: `Envelope.Alias`.
- Produces: `HostResult.SelfAlias string` (json `self_alias`).

**Not `pdx peers host list`.** `GET /api/peers/hosts` renders `cliHostRow` straight out of local config and never contacts anyone, so it has no live self-reported value — only a stale one. The fan-out fetches each envelope on every call, which makes `--all` the one place the comparison is current. Spec §7.4 says so; do not move it.

- [ ] **Step 1: Write the failing test**

`formatPeersTable(resp peers.Envelope) string` renders one host; find the `--all` renderer in `peers.go` (around `:449-461`) and test that one. Assert that a host whose `SelfAlias` differs from its `Alias` is marked, and that an agreeing host is not.

```go
func TestFormatPeersAll_MarksAliasDrift(t *testing.T) {
	all := peers.AllEnvelope{Hosts: []peers.HostResult{
		{Alias: "mlab", SelfAlias: "mlab", OK: true},
		{Alias: "air", SelfAlias: "air26", OK: true},
	}}
	got := renderPeersAllTable(all) // use the actual renderer's name and signature
	if !strings.Contains(got, "air26") {
		t.Errorf("drifted self alias not shown:\n%s", got)
	}
	mlabLine := lineContaining(t, got, "mlab")
	if strings.Contains(mlabLine, "!") {
		t.Errorf("agreeing host is marked: %q", mlabLine)
	}
}
```

Read the `--all` renderer's real name and signature first and use those; add a small `lineContaining` helper in the test file if one does not exist.

- [ ] **Step 2: Run the test and watch it fail**

Run: `go test -race -count=1 ./cmd/pdx/ -run TestFormatPeersAll_MarksAliasDrift`
Expected: FAIL — `unknown field SelfAlias`.

- [ ] **Step 3: Implement**

Add `SelfAlias string \`json:"self_alias"\`` to `HostResult`, populate it in `fetchHostResult` from the fetched envelope (and in `allEnvelope` for the local row, where it equals the local alias), and mark drift in the `--all` footer or host column with a trailing `!`.

```go
// SelfAlias is what the peer calls itself, shown beside the name we call it.
// Drift is surfaced, never followed: the local alias is what every address on
// this host resolves against, and silently adopting a peer's rename would move
// every address out from under whoever wrote one down.
```

- [ ] **Step 4: Run and verify**

Run: `go test -race -count=1 ./cmd/pdx/ ./internal/module/peers/ -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git commit --only internal/peers/envelope.go internal/module/peers/module.go \
  cmd/pdx/peers.go cmd/pdx/peers_test.go \
  -m "feat(pdx): show what a peer calls itself beside what we call it"
```

---

## Task D1: CLAUDE.md and the build gate

**Files:**
- Modify: `CLAUDE.md` (the "Peer addresses" section only)

**Do not edit the spec.** Its §9 acceptance items are run on two real daemons after merge, by an operator, not by this task.

- [ ] **Step 1: Rewrite the Peer addresses section**

The current text describes v3: `<host>/<canonical>[:<suffix>]`, canonical `_3k9f2mq4`, labels under `^[a-z0-9][a-z0-9-]{1,31}$`, and the `label_in_use` serial convention. Replace with v4:

- the address is `<host>/<name>`, displayed `<host>/<name> [<ref>]`;
- the three input forms from spec §5.5;
- `title` is free text; the serial convention stays, for humans, not for routing;
- an agent asked to "become X" runs `pdx msg name X`, then `pdx msg whoami`, and **reports the address**, not X;
- the `pdx: command not found` paragraph is unchanged.

- [ ] **Step 2: Build and check the version**

```bash
make build
./bin/pdx --version
```
Expected: the string from `VERSION`, not `unknown`.

- [ ] **Step 3: Run everything**

```bash
go test -race -count=1 ./...
cd spa && npx vitest run && pnpm run lint && pnpm run build
```
Expected: PASS (bar the #1092 flake).

- [ ] **Step 4: Commit**

```bash
git commit --only CLAUDE.md -m "docs: describe the v4 address model in CLAUDE.md"
```

---

## After the plan

Spec §9's real-machine acceptance is **not** a task here: it needs two deployed daemons and cannot run inside a worktree. Run it after merge and before the bump PR. §9.9 — pasting an address made on one host into the other — is the one that gates the release.
