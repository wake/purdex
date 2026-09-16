# Peer Address v3 — implementation plan

Spec: `2026-09-17-peer-address-v3-spec.md` · Branch: `worktree-worktree-peer-address-v3`

Nine tasks, one commit each, TDD (failing test first). T1/T2 are independent of everything and of
each other; T3 onwards are strictly ordered. The order is not cosmetic — see the constraint below.

## Ordering constraint (spec §5.2)

Relaxing the duplicate-label claim (T6) before tier 1 stops matching labels (T5) would open a window
where two conversations legitimately hold one label and every send to it resolves ambiguously. **T5
must precede T6.** Ordering them, rather than relying on the PR merging as a unit, also keeps
`git bisect` from landing inside that window.

## Task list

| # | Task | Files | Depends on |
|---|---|---|---|
| T1 | `CanonicalID` / `IsCanonicalID` | `internal/peers/label.go` | — |
| T2 | Widen `ValidateWireAddress` | `internal/peers/wire.go` | — |
| T3 | `PeerRecord.Canonical`, `applyLabel` rewrite | `internal/peers/record.go` | T1 |
| T4 | Live suffix for session rows | `internal/peers/record.go` | T3 |
| T5 | `Resolve` tier 1 → canonical | `internal/peers/address.go` | T3 |
| T6 | Claim warns instead of refusing; self envelope | `internal/module/peers/labels.go`, `internal/peers/wire.go` | T5 |
| T7 | `AmbiguousCandidate` on the wire | `internal/peers/wire.go`, `internal/module/peers/send.go` | T6 |
| T8 | CLI | `cmd/pdx/peers.go`, `cmd/pdx/msg.go` | T6, T7 |
| T9 | Delete the dead symbols | `internal/peers/label.go`, `internal/module/peers/module.go` | T3–T8 |
| T10 | SPA line + CLAUDE.md | `spa/src/…`, `CLAUDE.md` | T9 |

---

### T1 — `CanonicalID` / `IsCanonicalID`

Additive only; nothing calls it yet, so nothing changes behaviour.

**Tests first** (`internal/peers/label_test.go`):
- `CanonicalID` is deterministic for a fixed sessionId and differs across sessionIds.
- Output matches `^_[0-9a-z]{8}$` for a table of sessionIds including edge inputs (`""`, a very long
  string, non-UTF8 bytes).
- `IsCanonicalID` accepts an 8-digit id; rejects 6-digit, **7-digit**, a bare label, `""`, uppercase,
  and a hyphen.
- Disjointness: for a table of canonical ids, `ValidateUserLabel` returns an error for every one;
  for a table of valid user labels, `IsCanonicalID` is false for every one.

**Implementation**: same FNV-1a-64 + base36 as `DefaultLabel`, width 8 (`defaultLabelN` → a new
`canonicalN = 8`, `labelSpace` → `36^8`). Leave `DefaultLabel` in place for now; T9 removes it.

---

### T2 — Widen `ValidateWireAddress`

**Tests first** (`internal/peers/wire_test.go`):
- Accepts an 8-digit head, a 6-digit head, and a user-label head, each with and without a suffix.
- **Rejects a 7-digit head** — the case the spec called out; neither version mints one.
- Still rejects 5 and 9 digits, uppercase, `cc:`, `tmux:`, and an explicitly empty suffix.
- A `DeliverRequest` carrying an 8-digit `from.address` passes `Validate()`.

**Implementation**: replace the `IsDefaultLabel(head)` arm with `canonicalWireHead.MatchString(head)`
using `^_([0-9a-z]{6}|[0-9a-z]{8})$`. Comment must say why 7 is excluded and why 6 is still
accepted (v2 senders).

---

### T3 — `PeerRecord.Canonical` and the `applyLabel` rewrite

The core semantic change. Everything else is plumbing around it.

**Tests first** (`internal/peers/record_test.go`):
- Every live-cc row satisfies spec §4.5: `address == host+"/"+canonical+":"+suffix`;
  `canonical != ""`; `label_source == "user"` iff `label != ""`.
- A conversation with no label: `label == ""`, `label_source == ""`, address uses the canonical.
- A conversation with a label: `label` is it, `label_source == "user"`, **address still uses the
  canonical** — this is the test that pins D3.
- `canonical == ""` on `agent: null` rows; those keep `<host>/tmux:<name>`.
- Proxy rows keep `<host>/cc:<name>`; owner-fallback rows (`pid == 0`) still render an address.
- `label_rev` passes through unchanged (spec §4.4 — it is a label revision, not an address one).

**Implementation**:
- Add `Canonical string \`json:"canonical"\`` to `PeerRecord`.
- `applyLabel(rec, alias, info, canonical, tmuxName, ccName)`: set `rec.Canonical = canonical`;
  `rec.Label, rec.LabelSource = info.Label, LabelSourceUser` when `info.Label != ""`, else `"", ""`;
  head is always `canonical`.
- Update the 7 call sites in `record.go` (205, 211, 216, 229, 234, 239, 321) and the ones in
  `module.go` and `labels.go:204` to pass `CanonicalID(sessionID)` instead of `defaults.For(...)`.
  `ResolveDefaultLabels` is still called at those sites for now; its result simply stops being used
  — T9 removes the calls and the function together, so this task stays reviewable.

---

### T4 — Live suffix for session rows

**Tests first**: a session row whose registry `tmux` name differs from the live inventory name
renders the **live** name in `suffix` (the P1 regression test). An `entry` row with no session row
behind it keeps the registry value.

**Implementation**: `record.go:216` and `234` pass `s.Name` instead of
`candidates[0].TmuxSessionName()` / `paneMatches[0].TmuxSessionName()`. Add the `PeerRecord.Suffix`
doc comment required by spec §5.4 (two provenances, `row_kind` discriminates).

---

### T5 — `Resolve` tier 1 → canonical

**Tests first** (`internal/peers/address_test.go`):
- A canonical resolves to its row.
- **A label does not resolve** — falls through tier 1 and tier 2 to `ErrNotFound`. Pins D3.
- Two rows sharing a canonical → `*AmbiguousError`.
- `tmux:<name>` and bare-tmux tier 2 still resolve; `cc:` still `ErrLegacyCC`.
- `Partial` / `RegistryIncomplete` behaviour unchanged for the canonical arm.

**Implementation**: tier 1 predicate becomes `hasLiveEntry(r) && r.Canonical == head`. Update the
doc comment on `Resolve`, including the accepted-conservatism note from spec §5.1.

---

### T6 — Claim warns instead of refusing; the self envelope

**Tests first** (`internal/module/peers/labels_test.go`):
- Claiming a label a live session already holds: **200**, the label is set, the envelope carries
  `warning.code == "label_in_use"`, the other holder, and `live_labels`.
- The first holder is untouched: its label, canonical and address are unchanged.
- Re-claiming as `purdex-tester-2` succeeds with **no** warning.
- Claiming with unreadable registry files no longer 503s (the removed `BlockingUnknown` gate).
- `whoami` and `release` return the same envelope with `warning` absent.

**Implementation**:
- `wire.go`: add the envelope (`SelfResponse{Peer, Warning}`) and `SelfWarning{Code, Detail,
  Holders, LiveLabels}`. Remove `ErrLabelTaken`; move `Holder` / `LiveLabels` off `APIError`.
- `labels.go`: drop the `holder != nil && holder.SessionID != e.SessionID` refusal — compute the
  warning and continue to the write. Drop the `BlockingUnknown` gate from `claim` (spec §4.2).
  `writeSelfResult` encodes the envelope.

---

### T7 — `AmbiguousCandidate` on the wire

**Tests first**: an ambiguous resolve produces candidates carrying address, agent name, pid and cwd;
`send.go` still answers 409 and never picks one.

**Implementation**: `APIError.Candidates` becomes `[]AmbiguousCandidate`; `send.go` fills it from the
`*AmbiguousError`'s records.

---

### T8 — CLI

**Tests first** (`cmd/pdx/peers_test.go`, `cmd/pdx/msg_test.go`), all asserted on rendered output:
- `pdx peers`: `LABEL` first, blank when unset, no `*`; `--all` keeps `HOST` first.
- `pdx msg whoami`: prints `canonical:`.
- `pdx msg name`: prints both the label set and the unchanged address; on a duplicate prints the
  warning naming the other holders and **exits 0**.
- `pdx msg send` on ambiguity: one line per candidate with all four fields.

**Implementation**: `peers.go` column order and the `*` removal; `msg.go`'s `renderSelfRecord`,
`runMsgName`, ambiguity rendering, usage strings (`msg.go:41`, `294`), and `peerNotFoundHint`
(`send.go:39`).

---

### T9 — Delete the dead symbols

**Tests first**: none new — this task is proven by the suite still passing and by `go vet` /
compilation. Delete the `ResolveDefaultLabels` table from `label_test.go`.

**Implementation**: remove everything in spec §4.3, and the now-unused `ResolveDefaultLabels` calls
in `record.go` / `module.go` / `labels.go`. Sync the `Envelope.LabelsUnavailable` comment (spec
§6.1) and the stale `localEnvelope` comment about labels affecting addresses.

---

### T10 — SPA line + CLAUDE.md

**Gate first**: confirm PR #1085 has merged (spec §8.1). If not, stop and report — do not land.

- `RenamePopover.tsx:132`: the `labelSource === 'default'` marker goes.
- `usePeerStore.ts:28`: comment → `// user | ''`.
- `usePeerStore.test.ts`: fixtures drop `'default'`.
- `CLAUDE.md` "Peer addresses": rewrite per spec §8, including the three-bullet convention block.
- Gates: `cd spa && npx vitest run && pnpm run lint && pnpm run build`.

---

## Per-task definition of done

`go build ./...`, `go test ./...`, `go vet ./...` all clean, plus the task's own new tests failing
before the implementation and passing after. T10 additionally runs the SPA gates.

## Execution notes

- Each task is one commit; subagents run sequentially in this worktree except T1 and T2, which may
  run in parallel. Parallel subagents must commit with `git commit --only <files>` so they cannot
  sweep each other's work into their own commit.
- Every subagent Bash call must be prefixed `cd /Users/wake/Workspace/wake/purdex/.claude/worktrees/worktree-peer-address-v3 && `.
- Do not widen scope into spec §10's deferred items; if a task seems to require one, stop and report.
