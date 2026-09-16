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
13. a **released** label row (`labels[sid] = LabelInfo{Label: ""}`) does not
    block a candidate — `labelSnapshot()` puts released rows in the map with
    an empty `Label` (`internal/module/peers/module.go`), so the resolver
    must treat empty as "no user label", never as a competitor named `""`.
14. one sid with **both** a proxy and a non-proxy entry, in *different* tmux
    sessions: only the non-proxy entry's name counts, so rule 1 sees one
    distinct name and the session gets its candidate. (The proxy filter runs
    before the distinct-name check, not after.)

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
  it is given. It was the only place that decided a default, so after this
  task `grep -n 'DefaultLabel(' internal/peers/record.go` returns nothing
  — but that grep is a smoke check, not the guarantee: the guarantee is
  test 3 and test 4 below, which pin what each fallback branch renders.
- `Build` computes `defaults := ResolveDefaultLabels(in.Entries,
  in.ProxyPIDs, in.Labels)` once, before the join, and passes
  `defaults.For(sid)` at every `applyLabel` call and `defaults` to every
  `EntryRecord` call. `BuildInput` is unchanged.
- **Every** branch goes through `defaults.For(owner.SessionID)` — including
  the owner-fallback branches — and no branch is special-cased. Read spec
  §3.2 before writing this: `inbox_dead` yields a hash because its owner has
  no live entry and so is not in the population, while `ambiguous` yields
  whatever the population decided, because its owner *does* have live
  entries. Those two outcomes differ, and they differ correctly; do not
  "fix" the `ambiguous` branch into a hash.

**Tests first** (`record_test.go`):

1. a session row whose single live entry is in tmux `purdex1` gets
   `label: "purdex1"`, `label_source: "default"`, and
   `address: "mini-lab/purdex1:purdex1-<ccname>"`.
2. an entry row (not consumed by any session) in tmux `bb2` gets `bb2`.
3. an `inbox_dead` session row keeps the hash label (the owner has no live
   entry) even though the session is named `purdex1` — and a live entry row
   for another sid in that same tmux session still gets `purdex1`.
4. an `ambiguous` session row renders the **same** label as the entry rows of
   its own conversation (spec §3.2): with two live entries of one sid in tmux
   `purdex1` and no competitor, all three rows read `purdex1`, and `Resolve`
   on `purdex1` is an `AmbiguousError` over the two entry rows — which is
   what it already was with the hash. A second sub-case pins the hash side:
   the same shape with a competing sid in `purdex1` ⇒ all rows fall back.
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

Call-site checklist for tasks 3+4 — `EntryRecord` has **six** callers, and
the task is not done until every one compiles against the new signature and
is covered:

| File | Count | Which |
|---|---|---|
| `internal/peers/record.go` | 1 | `buildEntryRecords` |
| `internal/peers/record_test.go` | 2 | incl. `EntryRecord_MatchesBuild`, whose fixture must feed **the same** `DefaultLabels` both paths see — otherwise the test passes while proving nothing |
| `internal/module/peers/labels.go` | 4 | `whoami` ×1, `claim` ×2 (the 409 holder and the 200 record), `release` ×1 |

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
  This adds a **label-store read** gate only. It does not add a registry
  completeness gate: v2 §3.3's "release has no completeness requirement"
  is about unreadable *registry* files not blocking a release, and that
  stays true. Lock order is unchanged — `labelMu` still spans `origin()`,
  the store access and the render, exactly as `whoami` and `claim` do.

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
   store records **no** `Release` call. The existing `failingLabels` /
   `writeFailingLabels` fakes cannot prove a negative — add a fake that
   counts `Release` calls, and assert the count is 0.
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

   Survey first, so the size is known before the editing starts (counts
   measured on this branch at plan time):

   ```
   grep -rn 'DefaultLabel(' --include='*_test.go' internal/ cmd/   # 25
   grep -rn '"_[0-9a-z]\{6\}"' --include='*_test.go' internal/ cmd/ # 33
   grep -rn 'Tmux:' --include='*_test.go' internal/ cmd/            # 21
   ```

   Only a fixture that has **both** a `Tmux` field and an assertion on a
   default label/address changes: `internal/peers/record_test.go`,
   `internal/module/peers/{labels,send,e2e}_test.go`. A hash literal in
   `deliver_test.go`, `reply_test.go`, `cmd/pdx/msg_test.go` or
   `helpers_test.go` that is just a wire-contract fixture keeps its value —
   changing one of those is a signal the change leaked past its blast
   radius. `labels_test.go`'s fixture is the one to read carefully: pid 10
   has a tmux field, pid 20 does not, so the two must diverge.
2. New test (spec §6.5): a default-label **head** change delivered at an
   unchanged `label_rev` does not rename the peer's local helper —
   `ApplyAddress` ignores `rev <= appliedRev`
   (`internal/module/peers/helpers.go`). The test documents the accepted
   limit so a future change to it is deliberate.
3. New test (spec §6.6): `pdx peers` renders a tmux-derived default with the
   `*` marker, so it is distinguishable on screen from a user label of the
   same shape (`labelField`, `cmd/pdx/peers.go`).
4. `cmd/pdx/msg.go`'s usage/help text still shows `_k3x9qz` as the example
   address. Decide explicitly: it is a rendering example, not a contract, so
   it changes to a tmux-shaped default with the hash form named as the
   fallback — and the CLI test that pins the help text changes with it.
5. `CLAUDE.md`, "Peer addresses" section: replace
   "`_xxxxxx` 開頭＝尚未命名（由 sessionId 導出的預設值）" with the two-form
   rule — the default is the tmux session name when it names exactly one live
   agent, `_xxxxxx` otherwise — and add the one-line semantic: default label
   ＝ 位置（那個 tmux session 裡的 agent），user label ＝ 對話。

**Commits (two, and this is the one task that has two):**
`test(peers): fleet expectations for tmux-derived defaults` (items 1–3) and
`docs: peer address defaults come from the tmux session name` (items 4–5,
docs and help text only).

---

## Verification before the PR

```
go test ./internal/... ./cmd/...
go vet ./...
go build ./...
```

Then the live check (spec §6.7) — evidence pasted into the PR body, not
summarized.

**It must not touch the production daemon.** `pdx serve` takes its
`data_dir` from the config it loads, and that is where it takes the pid lock
and opens the DB (`cmd/pdx/main.go`), so `--port` alone would still collide
with the running mlab daemon's data dir; and `pdx peers` / `pdx msg` derive
their base URL from the same config, so without `--config` they would talk
to production on 7860. The whole check therefore runs against a **throwaway
config with its own `data_dir`**:

```
go build -o /tmp/pdx-branch ./cmd/pdx
# config.toml under a fresh temp dir: its own data_dir, an unused port,
# its own admin token
/tmp/pdx-branch serve --config "$TMP/config.toml"
/tmp/pdx-branch peers --config "$TMP/config.toml" --all   # read-only
/tmp/pdx-branch msg   --config "$TMP/config.toml" whoami  # read-only
```

The Claude Code registry it reads is the real, shared one, so the listing is
real data — which is the point. The only write in the check is a single
`msg send` addressed **to this very session**, so the message lands in an
inbox we own and no other agent is disturbed; the production alias is never
a target. Kill the temp daemon and remove the temp dir afterwards.

## Risks

| Risk | Mitigation |
|---|---|
| Task 3/4 diverge and the listing disagrees with `whoami` | Task 4 test 2 is the tripwire, written before the implementation |
| An expectation update hides a real regression | Task 5 item 1: any failure that is not the expected shape is fixed, not absorbed |
| `ResolveDefaultLabels` becomes order-dependent | Task 2 shuffle test |
| The upgrade moves every address at once (spec §4.1) | Accepted, documented; the live check in Verification is what confirms the new form works before merge |
| The live check disturbs the production daemon or another agent | Throwaway config + own `data_dir` + own port; reads only, except one `msg send` to this session |

## Codex plan review disposition (`task-mu3waw1z-u8bm7k`)

| # | Severity | Finding | Disposition |
|---|---|---|---|
| 1 | Blocker | Task 3's rationale ("fallback branches yield a hash because no live entry backs them") is false for `ambiguous` | **Fact accepted, proposed fix rejected** — the wrong sentence was the spec's. Forcing `ambiguous` to a hash would split one conversation across two labels and break `Build`/`whoami` agreement. Spec §3.2 rewritten, spec §10 holds the argument, plan Task 3 now states both outcomes and test 4 pins them |
| 2 | Major | `EntryRecord` has 6 callers, not 3 | **Accepted** — Task 4 opens with the call-site table, incl. `EntryRecord_MatchesBuild`'s fixture trap |
| 3 | Major | Task 2 missed released label rows and the proxy+non-proxy-same-sid case | **Accepted** — cases 13 and 14 |
| 4 | Major | Task 5 understates the test surface | **Accepted** — Task 5 item 1 gains the survey greps with counts, and the rule for which files may legitimately change |
| 5 | Major | Spare-port live check still collides with production's `data_dir` and the CLI still targets 7860 | **Accepted** — Verification rewritten around a throwaway config |
| 6 | Minor | Note that release adds a label-store gate, not a registry completeness gate | **Accepted** — Task 4 |
| 7 | Minor | Task 5's two commits contradict "one commit per task" | **Accepted** — stated as the one deliberate exception |
| — | Omission | `applyLabel` call sites cannot be replaced mechanically | **Accepted** — Task 3 |
| — | Omission | `EntryRecord_MatchesBuild` fixture must feed both paths the same map | **Accepted** — Task 4 table |
| — | Omission | Existing fakes cannot prove "no `Release` call" | **Accepted** — Task 4 test 5 |
| — | Omission | `pdx msg` help text still shows `_k3x9qz` | **Accepted** — Task 5 item 4 |
