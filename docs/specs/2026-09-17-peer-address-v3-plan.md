# Peer Address v3 — implementation plan

Spec: `2026-09-17-peer-address-v3-spec.md` · Branch: `worktree-worktree-peer-address-v3`

**Ten tasks**, one commit each, TDD (failing test first). T1/T2 are independent of everything and of
each other; T3 onwards are strictly ordered.

## Two rules that shaped the task boundaries

**1. A task that changes a shared type owns every consumer of it.** The first draft of this plan
split "change `APIError.Candidates`" from "fix the CLI that reads it", and "remove `ErrLabelTaken`"
from "fix the CLI that references it". Both would have produced a commit that does not compile. Any
task touching `internal/peers/wire.go` therefore carries its `cmd/pdx` consumers and their test
fixtures in the same commit.

**2. T5 must precede T6** (spec §5.2). Relaxing the duplicate-label claim before tier 1 stops
matching labels would open a window where two conversations legitimately hold one label and every
send to it resolves ambiguously. Ordering them also keeps `git bisect` out of that window.

## Task list

| # | Task | Files | Depends on |
|---|---|---|---|
| T1 | `CanonicalID` / `IsCanonicalID` | `internal/peers/label.go` | — |
| T2 | Widen `ValidateWireAddress` | `internal/peers/wire.go` | — |
| T3 | `Canonical`, `applyLabel`, `WireAddress`, and the v2 test migration | `internal/peers/record.go`, `internal/module/peers/labels.go` | T1 |
| T4 | Live suffix for session rows | `internal/peers/record.go` | T3 |
| T5 | `Resolve` tier 1 → canonical | `internal/peers/address.go` | T3 |
| T6 | Claim warns; self envelope **+ its CLI and e2e consumers** | `labels.go`, `wire.go`, `cmd/pdx/msg.go` | T5 |
| T7 | `AmbiguousCandidate` **+ its producer and CLI consumer** | `wire.go`, `send.go`, `cmd/pdx/msg.go` | T6 |
| T8 | CLI presentation (no shared types) | `cmd/pdx/peers.go`, `cmd/pdx/msg.go`, `send.go` | T7 |
| T9 | Delete the dead symbols + sweep | `label.go`, `module.go` | T3–T8 |
| T10 | SPA + CLAUDE.md | `spa/src/…`, `CLAUDE.md` | T9 |

---

### T1 — `CanonicalID` / `IsCanonicalID`

Additive; nothing calls it yet.

**Tests first** (`internal/peers/label_test.go`):
- Deterministic for a fixed sessionId; differs across sessionIds.
- Matches `^_[0-9a-z]{8}$` for a table including `""`, a very long string, and non-UTF8 bytes.
- `IsCanonicalID` accepts 8 digits; rejects 6, **7**, a bare label, `""`, uppercase, a hyphen.
- Disjointness both ways against `ValidateUserLabel`.

**Implementation**: FNV-1a-64 + base36 as `DefaultLabel`, width 8. Leave `DefaultLabel` alone; T9
removes it.

---

### T2 — Widen `ValidateWireAddress`

**Tests first** (`internal/peers/wire_test.go`):
- Accepts 8-digit, 6-digit and user-label heads, with and without a suffix.
- **Rejects a 7-digit head.** Still rejects 5, 9, uppercase, `cc:`, `tmux:`, an empty explicit suffix.
- A `DeliverRequest` with an 8-digit `from.address` passes `Validate()`.

**Implementation**: replace the `IsDefaultLabel(head)` arm with
`^_([0-9a-z]{6}|[0-9a-z]{8})$`. Comment why 7 is excluded and why 6 still passes (v2 senders).

---

### T3 — `Canonical`, `applyLabel`, `WireAddress`, and the v2 test migration

The core semantic change, and the largest task. It is large because the type change and every
assertion that pins the old behaviour have to move together.

**`WireAddress()` must change here, and this is the finding that matters most.** It returns
`Label + ":" + Suffix` (`record.go:54`) and `wireFromRecord` puts it in the outbound `from.address`
(`send.go:160`). Once `Label` means the user label, a v3 sender would announce itself at a
label-based address — **which compiles cleanly and is simply wrong**. It becomes
`Canonical + ":" + Suffix`. Update `record_test.go:965`.

**Tests first** (`internal/peers/record_test.go`):
- Spec §4.5's table on every live-cc row: `address == host+"/"+canonical+":"+suffix`,
  `canonical != ""`, `label_source == "user"` iff `label != ""`.
- No label → `label == ""`, `label_source == ""`.
- With a label → `label` is it, `label_source == "user"`, **address still uses the canonical**.
  This is the test that pins D3.
- `canonical == ""` on `agent: null` rows, which keep `<host>/tmux:<name>`; proxy rows keep
  `<host>/cc:<name>`; owner-fallback rows still render an address.
- `label_rev` passes through unchanged (spec §4.4).
- `WireAddress()` is canonical-based.

**Existing assertions to migrate or delete in this same commit** — these pin v2 and go red the
moment `applyLabel` changes:
- `internal/peers/record_test.go`: 367, 830, 837, 874, the whole tmux-default section 976–1226, and
  **1244** (which asserts a user label changes the address — directly contradicts D3), plus 965.
- `internal/module/peers/module_test.go`: 1663, 1687, 1700 (default-label behaviour on label-store
  failure).

**Implementation**:
- `PeerRecord.Canonical string \`json:"canonical"\``.
- `applyLabel(rec, alias, info, canonical, tmuxName, ccName)`: sets `Canonical`; `Label`/`LabelSource`
  are the user label or `""`/`""`; head is always the canonical.
- All 7 `record.go` call sites (205, 211, 216, 229, 234, 239, 321) pass `CanonicalID(sessionID)`.
- **Remove the three `defaults := ipeers.ResolveDefaultLabels(...)` locals in
  `internal/module/peers/labels.go` (134, 206, 255) in this commit.** Leaving them would be an
  unused local and Go will not compile. This is why T9 cannot be the first place the function's
  callers disappear.

---

### T4 — Live suffix for session rows

**Tests first**: a session row whose registry `tmux` name differs from the live inventory renders
the **live** name (the P1 regression test); an `entry` row with no session row keeps the registry
value.

**Implementation**: `record.go:216` and `234` pass `s.Name`. Add the `PeerRecord.Suffix` doc comment
required by spec §5.4.

---

### T5 — `Resolve` tier 1 → canonical

**Tests first** (`internal/peers/address_test.go`):
- A canonical resolves. **A label does not** — falls through both tiers to `ErrNotFound`. Pins D3.
- Two rows sharing a canonical → `*AmbiguousError`.
- `tmux:<name>` and bare-tmux tier 2 still resolve; `cc:` still `ErrLegacyCC`.
- `Partial` / `RegistryIncomplete` unchanged for the canonical arm.

**Also in this task, flagged by T3**: `internal/module/peers/e2e_test.go`'s
`LabelAmbiguityUnderUnknownFile` was kept green through T3 by giving the twin conversations a shared
*user label* as the ambiguous head, with a code comment saying T5 must switch it back. Switch that
head to the canonical here; the X1 rule it exercises is unchanged. Also finish
`record_test.go`'s `…ThreeRowsOneCanonical`, whose `Resolve` half T3 deferred because tier 1 was
still matching labels.

**Implementation**: predicate becomes `hasLiveEntry(r) && r.Canonical == head`. Update the `Resolve`
doc comment including spec §5.1's accepted-conservatism note.

---

### T6 — Claim warns; the self envelope, with its CLI and e2e consumers

Everything that reads a self-route 200 moves in this commit. Splitting it was the first draft's
mistake: `cmd/pdx/msg.go:378` and `:380` reference `ErrLabelTaken` and `APIError.LiveLabels` at
compile time.

**Tests first**:
- `internal/module/peers/labels_test.go`: claiming a label a live session holds → **200**, label
  set, envelope carries `warning.code == "label_in_use"`, the other holder, and `live_labels`; the
  first holder's label, canonical and address are untouched; re-claiming as `purdex-tester-2`
  succeeds with **no** warning; claiming with unreadable registry files no longer 503s.
- `whoami` and `release` return the same envelope with `warning` absent.
- `cmd/pdx/msg_test.go`: the self fixtures at 835, 859, 886, 927, 939 become envelopes; `name` on a
  duplicate prints the warning and **exits 0**.
- `internal/module/peers/e2e_test.go:1153` decodes the envelope, not a bare record.

**Implementation**:
- `wire.go`: add `SelfResponse{Peer, Warning}` and `SelfWarning{Code, Detail, Holders, LiveLabels}`;
  remove `ErrLabelTaken`; move `Holder`/`LiveLabels` off `APIError`.
- `labels.go`: drop the duplicate refusal and the `BlockingUnknown` gate (spec §4.2);
  `writeSelfResult` encodes the envelope.
- `cmd/pdx/msg.go`: `doSelfRequest` (654, 681) decodes the envelope; warning rendering replaces the
  `ErrLabelTaken` branch. `--json` passthrough (667) needs no change — it emits the raw body.

---

### T7 — `AmbiguousCandidate`, with its producer and CLI consumer

**Tests first**: an ambiguous resolve produces candidates carrying address, agent name, pid and cwd;
`send.go` still answers 409 and never picks one. Update `internal/peers/wire_test.go:238`,
`internal/module/peers/send_test.go:700`, `internal/module/peers/e2e_test.go:1040`.

**Implementation**: `APIError.Candidates` → `[]AmbiguousCandidate{Address, AgentName, PID, Cwd}`
(`wire.go:287`); producer at `send.go:313–318`; **CLI consumer at `cmd/pdx/msg.go:374`**, where
`sanitizeCell(c)` stops compiling the moment the element type changes.

**Also in this task: `AddressRev` stops following `LabelRev`.** `wireFromRecord` does
`AddressRev: rec.LabelRev` (`send.go:161`). A v3 address cannot change, so its revision is 0
permanently; sending the *label's* revision there claims an address change that did not happen.
Send 0. Spec §4.4 — an earlier draft of which contradicted itself here, and T3's implementer
stopped and reported rather than picking one of the two sentences. Assert that a v3 `from.address`
carries `address_rev: 0` even after the origin has claimed and re-claimed a label.

---

### T8 — CLI presentation

No shared types; safe on its own.

**Tests first** (`cmd/pdx/peers_test.go`, `cmd/pdx/msg_test.go`), asserted on rendered output:
- `pdx peers`: `LABEL` first, blank when unset, no `*`; `--all` keeps `HOST` first.
- `pdx msg whoami` prints `canonical:`.
- `pdx msg name` prints both the label set and the unchanged address.
- `pdx msg send` ambiguity: one line per candidate with all four fields.

**Implementation**: `peers.go` column order and the `*` removal (514); `msg.go` `renderSelfRecord`
(631), `runMsgName` (719), ambiguity rendering, usage strings (41, 294); `peerNotFoundHint`
(`send.go:39`).

---

### T9 — Delete the dead symbols

**Tests**: none new; proven by compilation, `go vet` and the suite. Delete the old default-label
suite in `internal/peers/label_test.go` (70, 230, 253).

**Implementation**: remove everything in spec §4.3. Sync `Envelope.LabelsUnavailable`'s comment
(spec §6.1) and the stale `localEnvelope` comment about labels affecting addresses.

**Definition of done is a sweep, not a feeling.** This must return nothing outside
`docs/`:

```
rg 'DefaultLabel|ResolveDefaultLabels|DefaultLabels|IsDefaultLabel|LabelSourceDefault' \
   --glob '!docs/**'
```

T3 already cleared `reply_test.go:239`, `send_test.go:477` and `e2e_test.go:1024`. As of T3 the
series survives in: `internal/peers/label.go`, `internal/peers/label_test.go`, `cmd/pdx/peers.go`
(the `*` marker, T8), `cmd/pdx/msg_test.go`, and **`internal/module/peers/helpers_test.go`** — the
last of which the first draft of this plan missed.

**Leftovers reported by T6, to clear here:** `Module.origin()`'s `diag` return value has no consumer
left (claim was the last one; all three call sites now discard it), and `APIError.Skipped` has no
producer left (the removed `BlockingUnknown` branch was the only one) although
`renderMsgAPIError`'s `not_ready` path still reads it.

**Also sweep for indirect dead code, which the symbol grep cannot see.** T3 found that
`labelInfos` in `internal/module/peers/labels.go` lost its only caller and would have survived a
name-based sweep; it deleted it there. `go build` and `go vet` do not flag an unused unexported
function, and neither `staticcheck` nor `deadcode` is installed on this machine. So for each
function remaining in `internal/peers/label.go` after the deletions, grep the repo for its name and
confirm it has a caller. The file is small by then; this is a handful of greps, not a tooling
project.

---

### T10 — SPA + CLAUDE.md

Gate satisfied: #1085 merged as alpha.365; `origin/main` is merged into this branch.

**Seven files** — spec §8.1 has the inventory. The only behavioural change is
`RenamePopover.tsx:132`; the rest are fixtures and one comment, and they must move together or that
test stops matching.

**Tests first**: `RenamePopover.test.tsx:483` and `usePeerStore.test.ts:131` assert
`labelSource: ''`. They stay, but must additionally assert `canonical === ''`, because under v3
`label_source: ''` no longer implies "no agent" on its own (spec §8.1).

- `RenamePopover.tsx:132`: the default-label marker goes.
- `usePeerStore.ts:28`: comment → `// user | ''`. Line 93 passes through — **do not touch**.
- Fixtures: `usePeerStore.test.ts` :20 :82 :131, `RenamePopover.test.tsx` :224 :333 :483,
  `StatusBar.test.tsx` :69, `usePeerInfo.test.ts` :20, `host-lifecycle.test.ts` :933.
- `CLAUDE.md` "Peer addresses": rewrite per spec §8, including the three-bullet convention block.
- Gates: `cd spa && npx vitest run && pnpm run lint && pnpm run build`.

---

## Per-task definition of done

`go build ./...`, `go test ./...`, `go vet ./...` all clean **at that commit**, plus the task's own
new tests failing before the implementation and passing after. T9 adds the symbol sweep; T10 adds
the SPA gates.

The baseline was verified green before T1: build, vet and all six affected packages pass on
`origin/main` merged into this branch.

## Execution notes

- One commit per task. Subagents run sequentially except T1 and T2, which may run in parallel and
  must then commit with `git commit --only <files>` so neither sweeps the other's work in.
- Every subagent Bash call must be prefixed
  `cd /Users/wake/Workspace/wake/purdex/.claude/worktrees/worktree-peer-address-v3 && `.
- Do not widen scope into spec §10's deferred items. If a task appears to require one, stop and
  report rather than improvising.
