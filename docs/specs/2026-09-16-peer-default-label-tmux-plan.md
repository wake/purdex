# Plan — Default peer label from the tmux session name

Spec: `2026-09-16-peer-default-label-tmux-spec.md` v2
Date: 2026-09-16
Branch: `worktree-peer-default-label`
Baseline: `origin/main` @ `1.0.0-alpha.362`; `go test ./internal/peers/...
./internal/module/peers/...` green before task 1.

One phase (spec §7), five tasks, **strictly serial** — each task changes a
signature the next one calls. Every task is TDD: the tests in its "Tests
first" list are written and seen to fail before the implementation, and each
task ends in its own commit.

## Conventions for every task

- Work in `/Users/wake/Workspace/wake/purdex/.claude/worktrees/worktree-peer-default-label`.
  Every `Bash` call is prefixed `cd <that path> && `.
- Commit with `git commit --only <files>` naming exactly the files that task
  touched. Never `git add -A`.
- Go only; no SPA, no Electron (spec §5).
- `go test ./internal/peers/... ./internal/module/peers/...` must be green at
  the end of each task, not only at the end of the plan.
- Do not touch `DefaultLabel`, its golden vectors, or `IsDefaultLabel`
  (spec §4): the hash form survives as the fallback.

---

## Task 1 — `SanitizeLabel`

**File:** `internal/peers/label.go` (+ `label_test.go`)

```go
// SanitizeLabel derives a user-label-shaped string from a tmux session
// name (spec §3.1). ok is false when the name cannot yield one.
func SanitizeLabel(name string) (label string, ok bool)
```

Steps, in order: fold `A-Z`→`a-z`; every byte outside `[a-z0-9-]` → `-`
(byte-wise, so a multi-byte rune becomes one `-` per byte); collapse runs of
`-`; trim leading/trailing `-`; truncate to 32 bytes; trim a trailing `-`
again; `ok=false` when the result is under 2 bytes or is `cc` / `tmux`.

**Tests first** (`label_test.go`, table-driven):

| input | expect |
|---|---|
| `purdex1` | `purdex1`, ok |
| `AI-Chat4` | `ai-chat4`, ok |
| `my_proj.2` | `my-proj-2`, ok |
| `my proj 2` | `my-proj-2`, ok (documented lossy collision, spec §3.1) |
| `--lead--` | `lead`, ok |
| `a` | not ok (under 2 bytes) |
| `` | not ok |
| `專案` | not ok (collapses to empty) |
| `cc` / `tmux` | not ok (reserved) |
| `CC` | not ok (folds to a reserved word) |
| 33 `a`s | 32 `a`s, ok |
| 32 `a`s + `-x` | 32 `a`s, ok (truncate lands on `-`, second trim removes it) |
| `a-` ×20 | truncated then trimmed, ok, and matches the regexp |

Plus a property-style assertion: for a fixed corpus of ~30 inputs (including
every row above, control bytes, `:`/`/`/`.`/`_`, emoji, and a 200-byte name),
every `ok` result satisfies `ValidateUserLabel` — the regexp validates the
output rather than the construction being trusted.

**Commit:** `feat(peers): SanitizeLabel derives a label from a tmux name`

---

## Task 2 — `ResolveDefaultLabels`

**File:** `internal/peers/label.go` (+ `label_test.go`)

```go
// DefaultLabels maps a live conversation's sessionId to its resolved
// default label. For() falls back to the v2 hash for any session the map
// does not cover, so a nil map behaves exactly as Peer Address v2 did.
type DefaultLabels map[string]string

func (d DefaultLabels) For(sessionID string) string

// ResolveDefaultLabels applies spec §3.3 over the live, non-proxy entries.
func ResolveDefaultLabels(entries []Entry, proxyPIDs map[int]bool,
    labels map[string]LabelInfo) DefaultLabels
```

`For`: returns `d[sessionID]` when present and non-empty, else
`DefaultLabel(sessionID)`.

`ResolveDefaultLabels`:

1. population = entries with `!(e.IsProxy || proxyPIDs[e.PID])`; group by
   `SessionID`.
2. per session: collect the distinct `TmuxSessionName()` values of its
   entries. Exactly one distinct value **and** `SanitizeLabel` ok ⇒ that is
   its `candidate`; otherwise the session has none (rule 1). An entry outside
   tmux reports `""`, which `SanitizeLabel` rejects.
3. `userLabels` = `{sid: labels[sid].Label}` for sids **in the population**
   with a non-empty label. Sessions outside the population (dead rows) are
   ignored — dead labels are inert (spec §4 row 5).
4. a candidate survives when: no other session in the population has the same
   candidate (rule 2 — user-labelled sessions count as competitors), and no
   **other** session in the population holds it as a user label (rule 3).
5. the returned map holds only surviving candidates. Sessions that fell back
   are simply absent, so `For` yields their hash.

Determinism: the result must not depend on entry order — assert this with a
test that shuffles the input.

**Tests first** (`label_test.go`), each a small `[]Entry` fixture:

1. one session, tmux `purdex1` ⇒ `purdex1`.
2. two entries, same sid, same tmux name ⇒ `purdex1` (one conversation, two
   processes is not a conflict).
3. two entries, same sid, tmux `a1` and `a2` ⇒ absent (rule 1).
4. two sids, both tmux `purdex1`, both unnamed ⇒ both absent (rule 2).
5. two sids, both tmux `purdex1`, one user-labelled `foo` ⇒ **both absent**
   (rule 2 counts the labelled one; this is the codex Blocker).
6. sid A tmux `purdex1`; sid B elsewhere holds user label `purdex1` ⇒ A
   absent (rule 3).
7. sid A tmux `purdex1`; a **dead** session (not in entries) holds user label
   `purdex1` ⇒ A gets `purdex1` (inert).
8. sid A tmux `purdex1` and A itself holds user label `purdex1` ⇒ A gets
   `purdex1` (rule 3's "other" — the release case).
9. entry outside tmux (`Tmux: ""`) ⇒ absent.
10. proxy entry in tmux `purdex1` + a real entry in tmux `purdex1`, different
    sids ⇒ the real one gets `purdex1` (the proxy is not in the population);
    covered for both `IsProxy` and `proxyPIDs`.
11. two sids whose different tmux names sanitize to the same candidate ⇒ both
    absent.
12. `For` on a nil map, an absent sid, and an empty-string value ⇒ the hash.

**Commit:** `feat(peers): resolve default labels from tmux session names`

---

## Task 3 — Wire it into `Build`

**Files:** `internal/peers/record.go`, `internal/peers/record_test.go`

Signature changes:

```go
func applyLabel(rec *PeerRecord, alias string, info LabelInfo,
    defaultLabel, tmuxName, ccName string)          // sid -> defaultLabel

func EntryRecord(alias, hostID string, e Entry, proxy bool,
    info LabelInfo, defaults DefaultLabels) PeerRecord
```

- `applyLabel` no longer calls `DefaultLabel`; it uses the `defaultLabel`
  it is given. This is the only place that decided a default before, so
  after this task `grep -n 'DefaultLabel(' internal/peers/record.go` must
  return nothing.
- `Build` computes `defaults := ResolveDefaultLabels(in.Entries,
  in.ProxyPIDs, in.Labels)` once, before the join, and passes
  `defaults.For(sid)` at every `applyLabel` call and `defaults` to every
  `EntryRecord` call. `BuildInput` is unchanged.
- The owner-fallback branches (`inbox_dead`, `ambiguous`) keep passing the
  owner's sid through `defaults.For`, which yields the hash because no live
  entry backs them (spec §3.2) — no branch is special-cased.

**Tests first** (`record_test.go`):

1. a session row whose single live entry is in tmux `purdex1` gets
   `label: "purdex1"`, `label_source: "default"`, and
   `address: "mini-lab/purdex1:purdex1-<ccname>"`.
2. an entry row (not consumed by any session) in tmux `bb2` gets `bb2`.
3. an `inbox_dead` session row keeps the hash label (the owner has no live
   entry) even though the session is named `purdex1` — and a live entry row
   for another sid in that same tmux session still gets `purdex1`.
4. an `ambiguous` session row keeps the hash label.
5. a user-labelled session still renders its user label with
   `label_source: "user"` (unchanged).
6. a proxy row still renders `alias/cc:<name>` with no label (unchanged).
7. the two-agents-one-session case end to end through `Build`: both rows
   carry hash labels.

Existing `record_test.go` expectations that assert a hash default for a row
that now derives one from tmux are updated **one by one**, each with the
reason visible in the diff; a test whose fixture has no `Tmux` field must
keep its hash expectation untouched.

**Commit:** `refactor(peers): Build renders defaults from the resolver`

---

## Task 4 — Wire it into the self routes

**Files:** `internal/module/peers/labels.go`, `labels_test.go`

- add `func labelInfos(rows []store.PeerLabel) map[string]ipeers.LabelInfo`
  next to the existing `labelRows`.
- `whoami`: after its existing `Snapshot()`, compute
  `defaults := ipeers.ResolveDefaultLabels(entries, proxies,
  labelInfos(rows))` and pass `defaults` to `EntryRecord`. `origin()`
  already returns `entries` and `proxies`; today `whoami` discards both —
  it must stop discarding them.
- `claim`: same map (it already snapshots), passed to **both** `EntryRecord`
  calls — the 409 `label_taken` holder record and the 200 record.
- `release`: gains a `Snapshot()` **before** the write. A read error is
  `store_unavailable` and **no write is attempted** (spec §3.4). On success,
  the post-release record is rendered with the defaults computed from that
  snapshot; rule 3's "other" is what lets the caller's own
  about-to-be-released label not block its own candidate.

**Tests first** (`labels_test.go`):

1. `whoami` from an agent in tmux `purdex1`, unnamed ⇒ `label: "purdex1"`,
   `label_source: "default"`, and the full address matches.
2. **Cross-path consistency**: one fixture (entries + sessions + labels) run
   through both `Build` and `whoami`; assert the two produce byte-identical
   `address` and `label` for the same live entry. This is the test spec §3.2
   exists for — write it before the implementation and watch it fail.
3. `claim` 409: the holder record's address uses the resolved default when
   the holder is unnamed.
4. `release` by an agent in tmux `purdex1` that had claimed `purdex1` ⇒ the
   returned record's label is `purdex1` with `label_source: "default"`.
5. `release` when `Snapshot()` fails ⇒ 503 `store_unavailable`, and the fake
   store records **no** `Release` call.
6. `whoami` for an agent outside tmux ⇒ still the hash label.

**Commit:** `fix(peers): self routes render the same defaults the listing does`

---

## Task 5 — Fleet-wide expectations, the two pinned limits, and docs

**Files:** `internal/module/peers/{send,reply,e2e,module}_test.go`,
`cmd/pdx/peers_test.go`, `internal/module/peers/helpers_test.go` (or the
nearest existing home for a helper-rename test), `CLAUDE.md`

1. Run the full suite, list every failure, and update each expectation
   individually. A failure that is *not* "a hash default became a
   tmux-derived one" is a bug in tasks 1–4 and is fixed there, not absorbed
   into an expectation.
2. New test (spec §6.5): a default-label **head** change delivered at an
   unchanged `label_rev` does not rename the peer's local helper —
   `ApplyAddress` ignores `rev <= appliedRev`
   (`internal/module/peers/helpers.go`). The test documents the accepted
   limit so a future change to it is deliberate.
3. New test (spec §6.6): `pdx peers` renders a tmux-derived default with the
   `*` marker, so it is distinguishable on screen from a user label of the
   same shape (`labelField`, `cmd/pdx/peers.go`).
4. `CLAUDE.md`, "Peer addresses" section: replace
   "`_xxxxxx` 開頭＝尚未命名（由 sessionId 導出的預設值）" with the two-form
   rule — the default is the tmux session name when it names exactly one live
   agent, `_xxxxxx` otherwise — and add the one-line semantic: default label
   ＝ 位置（那個 tmux session 裡的 agent），user label ＝ 對話。

**Commit:** `test(peers): fleet expectations for tmux-derived defaults` +
`docs: CLAUDE.md peer address defaults` (two commits; item 4 is docs-only).

---

## Verification before the PR

```
go test ./internal/... ./cmd/...
go vet ./...
go build ./...
```

Then, on this host (spec §6.7) — evidence pasted into the PR body, not
summarized:

```
pdx peers --all                       # tmux-derived labels, still marked *
pdx msg whoami                        # my own address agrees with the listing
pdx msg send mini-lab/<tmux name> "…" # delivers
```

The live checks run against a daemon **built from this branch**
(`go build -o bin/pdx ./cmd/pdx` in the worktree, run on a spare port), not
the mlab production daemon: this branch is not deployed and the running
daemon is alpha.360.

## Risks

| Risk | Mitigation |
|---|---|
| Task 3/4 diverge and the listing disagrees with `whoami` | Task 4 test 2 is the tripwire, written before the implementation |
| An expectation update hides a real regression | Task 5 item 1: any failure that is not the expected shape is fixed, not absorbed |
| `ResolveDefaultLabels` becomes order-dependent | Task 2 shuffle test |
| The upgrade moves every address at once (spec §4.1) | Accepted, documented; the live check in Verification is what confirms the new form works before merge |
