# Peer Local Delivery Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** `pdx msg send` reaches a local peer. One entry point, one ref namespace, and an ambiguity refusal that can actually be acted on.

**Architecture:** `handleSend` grows a local branch — decided at step 3, taken at step 5 — that reuses the inventory step 4 already built, resolves with the same `Resolve`, and writes the frame straight to the target's inbox with the sender's own socket as the reply address. No helper, no HTTP hop. Phase B adds the ref that makes an ambiguity refusal usable, and the test that would have caught its absence.

**Tech Stack:** Go (net/http, modernc.org/sqlite). No SPA changes.

**Spec:** `docs/specs/2026-09-17-peer-local-delivery-spec.md` — read it before Task A1, and read **§8** first: it is the list of things the first draft got wrong, and every one of them is a way this plan could go wrong too. Where plan and spec disagree, the spec wins and the plan is the bug.

## Global Constraints

- **The repo root holds a version-controlled `pdx` binary that `go build ./cmd/...` overwrites.** Every commit uses `git commit --only` with the **explicit file list printed in that task's commit step**. Never `git commit -am`, and never expand `$(git diff --name-only)` into a commit — this worktree may host parallel subagents sharing one index.
- **Build the daemon with `make build`**, never bare `go build` — the version comes from ldflags.
- **Restart is `pdx stop && pdx start`.** There is no `restart`. Version is `pdx version`, **not** `pdx --version`.
- **Test package names.** `internal/peers` and `internal/module/peers` tests are `package peers` — call `Resolve`, `PeerRecord`, `handleSend` helpers **directly**, never `peers.X`. `cmd/pdx` tests are `package main` and **do** use `peers.X` / `ipeers.X`.
- **Go tests:** `go test -race -count=1 ./internal/...` (narrow per task).
- **Known failures, not yours — and "run it alone" means the *test*, not the package.** Measured on this tree at `8ba64c59`: `go test -race ./internal/module/agent/` fails **every time**, on `TestConsumeSignals_GraceWindowDrop_RearmsAfterTeardown` (2.02s). Only `-run TestConsumeSignals_GraceWindowDrop_RearmsAfterTeardown` passes (0.03s). #1092 calls it a flake and this plan first repeated that; at package scope it is deterministic. It cannot be your change: `go list -deps ./internal/module/agent/` shows **zero** dependency on `internal/peers` or `internal/module/peers`. Same treatment for `internal/agent/probe`'s `TestWatch_StopWatch_CancelsLoop`. **Do not try to fix either, and do not let either block a task.**
- **`gofmt -l .` is not empty on this tree and never was** — 28 files are already unformatted (`internal/agent/cc/statusline.go`, `internal/bridge/bridge.go`, `internal/module/agent/*_test.go`, …), none of them ours. Check only the files you touched, and confirm the count of 28 is unchanged before and after your edit. A task that "fixes" the other 28 has done something nobody asked for and made its own diff unreviewable.
- **Mutation-testing is a deliverable, not a bonus.** A task that reports a rule as covered without having broken it deliberately has not finished. If breaking the rule leaves the package green, the test you still owe is the work.

### The mutation list — five, numbered, and these are all of them

Referred to by id everywhere below, so "re-run the mutations" means something checkable.

| id | task | mutation | what must go red |
|---|---|---|---|
| **M1** | A2 | pass a helper socket as `BuildFrame`'s arg 2 | the top-level `Frame.From` assertion |
| **M2** | A2 | set `Wrapper.From` to a helper socket | the wrapper-attribute assertion |
| **M3** | A4 | move the pair-limit check above the audit insert | "the rate-limited refusal is audited" |
| **M4** | B1 | delete the `HasSuffix` arm of `addressWithRef` | the ref-form render case |
| **M5** | B2 | drop `Ref: c.Ref` from the candidate **population** (`send.go:335`) — keep the struct field | the same-name e2e, on the two refs differing |

**M1 and M2 must fail independently.** If one assertion covers both, you have written one test for two fields.

**M5 mutates the population, not the struct.** Deleting the field would be a compile error, which proves nothing about whether the refusal is usable — and it is the exact "it went red, so it must be covered" trap this plan warns about two bullets up.

**A mutation that does not redden is not automatically a missing test.** An earlier draft of this plan listed "apply `normalizeRemoteRows` to the local rows" as a sixth. It was dropped: that function only restamps `Host`/`HostID` and recomputes `Address` from the row, and a local row already carries those values, so it is idempotent on exactly the fixtures a test would use. A green result there would have meant nothing, and chasing it would have produced a test contorted to detect a no-op. The local branch simply does not call it; that is a code-review fact, not a behavioural one.
- **Do not write a test that pins a false statement.** v4 shipped `it('notes that titles could not be read, so addresses may be hash defaults')` — a test whose name was the falsehood, so any sweep fixing the text turned it red and got reverted. When you delete or move a field, grep every assertion on it and ask what each one still verifies.

### Facts verified against the tree at `162b6768`

| symbol | location | shape |
|---|---|---|
| `handleSend` | `internal/module/peers/send.go:190` | steps 1–8, commented; `refuseUnaudited` for 1–6, audit insert at step 7 |
| `ErrLocalTarget` | `internal/peers/wire.go:169` | used in exactly one place (`send.go:237`) plus one test (`send_test.go:641`) |
| `SendRequest` | `internal/peers/wire.go:280` | `To`, `Text`, `Mode`, `OriginInbox` — **no `MsgID`, no `HopChain`** |
| `SendResponse` | `internal/peers/wire.go:288` | `MsgID`, `ToHostID`, `ToAddress`, `To`, `Result`, `EffectiveMode`, `OneWay` |
| `AmbiguousError` | `internal/peers/address.go:77` | `{Session string; Candidates []PeerRecord}` — `PeerRecord.Ref` exists, so `c.Ref` is available |
| `AmbiguousCandidate` | `internal/peers/wire.go:357` | `Address`, `AgentName`, `PID`, `Cwd` — no `Ref` |
| `displayAddress` | `cmd/pdx/peers.go:583` | omits bracket when `Ref == "" \|\| strings.HasSuffix(Address, "/"+Ref)` |
| `msgCandidateLine` | `cmd/pdx/msg.go:428` | prints address + agent/pid/cwd; no ref |
| `m.localEnvelope` | called at `send.go:270` | already built at step 4 to attribute the origin |
| `pairKey` | `internal/module/peers/limits.go:52` | `{From, To ipeers.OriginKey}`; `OriginKey` includes `HostID` |
| write-result mapping | `internal/module/peers/deliver.go:350` | `nil` → `delivered`; `ErrPostWriteTimeout` → `delivery_uncertain`; else `socket_write_failed` |
| `BuildFrame` | `internal/peers/ccuds/frame.go:36` | `BuildFrame(msgID, fromSock, w)` — arg 2 becomes top-level `Frame.From`; `w.From` is separate, inside the content |

### Scaffolding A2 built, and three traps it found (read before A3)

A2 is committed (`dba63005`). It added test scaffolding and hit three things this plan had
under-specified. A3, A4 and B2 all touch the same harness, so they are recorded here rather than
rediscovered:

- **`sendEnv.addLocalPeer(name, sessionID string, pid int) *localPeer`** (`send_test.go:660`) — a
  real Unix listener plus a registry entry. The harness previously had **exactly one** local cc row
  (`targetSock`), which `sendEnv` uses as the *origin*, so any test needing a second local session
  must use this. Reuse it; do not build a parallel fixture.
- **`procStart` must be `targetProcStart`.** `deliverLiveness` reports `fixture76973ProcStart` for
  every pid below 900000, so any other value makes the entry look dead and the row never appears at
  all — the test then fails for a reason that has nothing to do with what it is testing.
- **`Partial` alone does not make every address form `not_ready`.** `Resolve` consults
  `snap.Partial` only *below* tiers 1–3, so a partial-inventory test must use an address that
  reaches the tmux fallback (A2 uses a bare tmux session name whose owner lookup errors). A test
  written against a **name-tier** address would pass while proving nothing — the same trap this plan
  warns about, found in the plan's own test list.
- **A successful local send inserts one `DirOut` audit row**, so the old `TestSend_LocalTarget`
  assertion `len(s.rows()) == 0` is now false. A2 dropped it rather than weakening it; A4 owns the
  audit-row count.

### File structure

| File | Responsibility | Task |
|---|---|---|
| `internal/module/peers/send.go` | `handleSend` — target resolution, the local branch | A1, A2, A3, A4 |
| `internal/peers/wire.go` | `ErrSelfTarget`, `AmbiguousCandidate.Ref`; `ErrLocalTarget` deleted | A2, A3, B1 |
| `cmd/pdx/peers.go` | `displayAddress` → caller of `addressWithRef` | B1 |
| `cmd/pdx/msg.go` | `msgCandidateLine` renders the ref | B1 |
| `internal/module/peers/e2e_test.go` | the same-name ambiguity test (§6.2) | B2 |

**Ordering: A1 → A2 → A3 → A4 → B1 → B2 → C1, strictly serial.**

An earlier draft called B1 parallelisable "because it touches different files". It does not: B1's commit list includes `internal/peers/wire.go` and `internal/module/peers/send.go`, which A2 and A3 both edit. In one worktree, parallel subagents share an index — `git commit --only` keeps one agent from committing another's files, but it does nothing about two agents editing the same file, and a test run during the overlap sees a state neither agent wrote. If B1 is genuinely wanted in parallel it needs its own worktree; inside this one it is serial.

---

## Task A1 — make `entry` unreachable after step 5 (pure refactor, no behaviour change)

**Why first.** `entry` is zero on the local path and is read 24 times after step 3 (`entry.Alias` ×14, `entry.HostID` ×5, `entry.Token` ×3, `entry.URL` ×2). Adding the branch first would mean writing the branch *and* hunting the 24 in one step, with an empty alias rendering silently into an operator-facing error as the failure mode. Doing the substitution first, with behaviour frozen, makes A2 a change to control flow only.

**This task changes no behaviour.** Every existing test must pass untouched. If a test needs editing, stop — you have changed something.

- [ ] Read `internal/module/peers/send.go:190-477` end to end before editing.
- [ ] After the step-3 entry lookup, introduce two locals: `targetAlias := entry.Alias` and `targetHostID := entry.HostID`.
- [ ] Replace **17 of the 24** reads: `entry.Alias` ×13 and `entry.HostID` ×4 — every one from step 5 onward.
- [ ] **Leave 7 alone**, and know which: the `host_unverified` guard inside step 3 reads `entry.Token`/`entry.HostID` (`send.go:252`) and `entry.Alias` (`:253`), and `entry.Token`/`entry.URL` are read by `m.fetch` (`:297`) and `m.post` (`:438`). All seven sit on branches A2 makes local-unreachable, so substituting them would be noise that hides which reads are genuinely remote-only.
- [ ] Verify mechanically, not by eye — but **compute the boundary, do not hardcode it**, because this task's own edits move every line below it:

  ```sh
  step5=$(grep -n '^	// 5\. ' internal/module/peers/send.go | cut -d: -f1)
  awk -v n="$step5" 'NR>=n' internal/module/peers/send.go | grep -c 'entry\.Alias\|entry\.HostID'
  ```

  must print `0`. Run it **after** the edit; a literal line number taken from this plan will already be stale by then.
- [ ] Run `go test -race -count=1 ./internal/module/peers/... ./cmd/pdx/...` — all green, no test file modified.
- [ ] `git status --porcelain` shows exactly one modified file.
- [ ] Commit: `git commit --only internal/module/peers/send.go -m "refactor(peers): read the send target's alias and host id through one pair of locals"`

**Acceptance:** the grep prints 0, the suite is green, and `git diff --stat` shows one file with no test changes.

---

## Task A2 — the local branch

**TDD. Write every test below and watch it fail for the stated reason before writing the branch.**

- [ ] **Red.** In `internal/module/peers/send_test.go`, add a local-delivery test: a fixture with two live local cc rows, `to` naming the second, `origin_inbox` the first. Assert 200 and that the target's fake inbox received one frame. It must fail with `local_target`.
- [ ] **Red.** Assert **both** `from`s on the received frame:
  - decode the NDJSON and assert top-level `Frame.From == "uds:"+originInbox`;
  - parse the wrapper out of `Frame.Message.Content` and assert its `from` attribute is the same string.
  Spec §4.1: these are different fields and a test on one would miss the other.
- [ ] **Red.** Assert no helper was acquired and `postDeliver` was never called (use the existing fake; assert not-invoked, not merely that the result was fine).
- [ ] **Red.** `SendResponse` for a local send: `ToHostID` is this host, `ToAddress` is the target's address, `Result` is `delivered`, `OneWay` is false.
- [ ] **Red.** A `Partial` local inventory yields `not_ready`, not a guess. A local target that does not resolve yields the same code its remote equivalent does — write this as the four-form table §6.1 specifies (`tmux:<name>`, bare tier-4, name tier, ref tier), run once local and once remote against the same rows, asserting the codes match.
- [ ] **Green.** Implement:
  - step 3: replace the `local_target` refusal with `isLocal := true`; skip the entry lookup and the `host_unverified` arm when local. Set `targetAlias = snap.alias`, `targetHostID = snap.hostID`.
  - step 5: when local, `rows = local.Peers` and take the snapshot flags from the step-4 envelope (`local.Partial`, `len(local.UnknownRegistryFiles) > 0`). **No `m.fetch`, no `normalizeRemoteRows`** (spec §4.1 says why the latter would be wrong, not merely unnecessary).
  - step 8: when local, build the frame per spec §4.1 — `BuildFrame(msgID, req.OriginInbox, Wrapper{From: "uds:"+req.OriginInbox, FromName: origin.Address, FromMode: effective, HopChain: "", Text: req.Text})` — and `m.writeFrame` to `target.Agent.Inbox`. Map the write result exactly as `deliver.go:350` does.
  - `HopChain` is `""`. `SendRequest` has no such field; if you typed `req.HopChain` it did not compile and the spec explains why.
- [ ] **Convert `TestSend_LocalTarget`, do not delete it.** It is at `send_test.go:635` and it covers three host-segment forms that all had to reach the same refusal: the local **alias**, the local **host id**, and a **case-mismatched** alias (`"MLAB"`). Those three must now all *deliver*. Rewrite it as a local-delivery table over the same three inputs, keeping its existing assertions that `fetch` and `post` were never called. Deleting it would silently drop the only coverage that `HostMatches`' three forms all route to the local branch.
- [ ] Delete `ErrLocalTarget` from `internal/peers/wire.go` once nothing references it (`grep -rn ErrLocalTarget --include='*.go' .`).
- [ ] **Mutations M1 and M2** (see the list above). Run each, confirm red, revert. They must fail **independently**; if one assertion catches both, you have one test doing the work of two.
- [ ] `go test -race -count=1 ./internal/... ./cmd/pdx/...`
- [ ] Commit: `git commit --only internal/module/peers/send.go internal/module/peers/send_test.go internal/peers/wire.go -m "feat(peers): pdx msg send delivers to a local peer"`

**Acceptance:** a local send delivers; both `from`s are the sender's socket; no helper, no HTTP; `local_target` is gone from the tree (`grep -rn local_target --include='*.go' .` returns nothing).

---

## Task A3 — refuse sending to yourself

- [ ] **Red.** Origin and target the same session ⇒ 400 `self_target`, and the fake inbox received **nothing**. Write it twice: once addressing the target by name, once by ref — the check is on the identity tuple, so a string comparison would pass the first and fail the second.
- [ ] **Green.** Add `ErrSelfTarget = "self_target"` to `internal/peers/wire.go`. Place the check **after** the existing deliverable guard at `send.go:379` (`!target.Deliverable || target.Agent == nil || target.Agent.Type != "cc"`) and **before** the step-7 audit insert. Compare `origin.Agent.SessionID/PID/ProcStart` with `target.Agent`'s; refuse `refuseUnaudited(400, ErrSelfTarget, …)` on a full match.
- [ ] **`target.Agent` is nil for a tier-4 or `tmux:` match, and dereferencing it panics.** Placing the check before that guard — the obvious reading of "after `Resolve`" — turns a `not_deliverable` refusal into a crash. `origin.Agent` needs no guard: `findOrigin` only returns a live, deliverable, non-proxy cc row. Add a test that a `tmux:<name>` address resolving to an agentless row still refuses `not_deliverable` and does not panic.
- [ ] **Red→green.** The refusal is **unaudited**: assert no audit row was inserted (it sits with the other step-6 resolution refusals, spec §4.3).
- [ ] Mixed-version check: a `cmd/pdx` test that `renderMsgAPIError` handles an unknown code (an older daemon will never send `self_target`, but a newer one will reach an older CLI) without panicking and while printing the detail.
- [ ] `go test -race -count=1 ./internal/... ./cmd/pdx/...`
- [ ] Commit: `git commit --only internal/peers/wire.go internal/module/peers/send.go internal/module/peers/send_test.go cmd/pdx/msg_test.go -m "feat(peers): refuse a local send addressed to the sender itself"`

**Acceptance:** both address forms refused, nothing written to the inbox, no audit row, CLI renders the unknown code safely.

---

## Task A4 — policy on the local path

Spec §4.3 is the contract. The first draft of it was wrong, so implement from the table, not from memory.

- [ ] **Red.** The pair rate limit applies to a local send: exceed `PairRateLimit` and assert `rate_limited`.
- [ ] **Red.** That refusal **is audited** — assert an audit row exists with the rate-limited result.
- [ ] **Red.** A local *resolution* failure is **not** audited — assert no row.
- [ ] **Red.** Exactly one audit row (`DirOut`) per successful local send, `ToHostID` = this host. Not two: one daemon made one observation (spec §4.3).
- [ ] **Red.** `--mode bypass` locally produces `FromMode: bypass` with no host entry consulted; an invalid mode is still 400.
- [ ] **Red.** No local refusal renders an empty alias — scoped to **the arms that format the target host**, i.e. the ones A1 changed from `entry.Alias` to `targetAlias`: ambiguous, not-ready, not-found, name-mismatch, remote-too-old, and the `toAddress` fallback. Assert each detail contains `snap.alias`. Do **not** extend this to validation, origin attribution, the pair limit or a write failure — none of those name the target host, and pinning an alias into their text would make the test enforce wording nobody chose.
- [ ] **Green.** Place the pair-limit check on the local path *after* the audit insert. Build the key with the constructor that already exists — `ipeers.WireTo.Key(hostID)` (`wire.go:432`), the same one `/deliver` uses at `deliver.go:265`:

  ```go
  if !m.pairs.Allow(pairKey{From: from.Key(), To: to.Key(targetHostID)}) { … }
  ```

  Do not hand-assemble an `OriginKey` or invent a `PeerRecord.Key()`.
- [ ] **Do not add a dedup check.** Spec L8: the id is minted per attempt by this handler, so it can never fire. If you believe you have written a passing test for local dedup, you have written a test that asserts nothing — reread §4.3.
- [ ] **Mutation M3.** Move the pair-limit check above the audit insert; the "refusal is audited" test must go red. Revert.
- [ ] `go test -race -count=1 ./internal/...`
- [ ] Commit: `git commit --only internal/module/peers/send.go internal/module/peers/send_test.go -m "feat(peers): apply the pair rate limit and the audit boundary to local sends"`

**Acceptance:** the five policy tests pass, the mutation reddens the audit test, and no dedup check exists on the local path.

---

## Task B1 — the ambiguity refusal carries refs

Runs **after A4**, not alongside it: B1 edits `internal/peers/wire.go` and `internal/module/peers/send.go`, both of which A2 and A3 also edit (see Ordering). Nothing about this task is logically coupled to Phase A — it is serialised only because they share files in one worktree.

- [ ] **Red.** `cmd/pdx`: a candidate with a ref renders `<address> [<ref>]`; one without renders the bare address; **one whose address already ends in its ref renders no bracket** — that is the second arm of `displayAddress` and the case a restated rule gets wrong.
- [ ] **Red.** `displayAddress` and the candidate renderer return the same string for the same `(address, ref)` input across all three cases.
- [ ] **Green.** Add `Ref string \`json:"ref,omitempty"\`` to `AmbiguousCandidate` (`internal/peers/wire.go:357`), populated `Ref: c.Ref` where the candidate is built (`send.go:335`).
- [ ] **Green.** Extract `addressWithRef(address, ref string) string` with **both** arms (spec §5.2). Make `displayAddress` a one-line caller. Use it in `msgCandidateLine`.
- [ ] **Mutation M4.** Delete the `HasSuffix` arm from `addressWithRef` — a test must go red on `mlab/_h0h3ln [h0h3ln]`. Revert.
- [ ] `go test -race -count=1 ./internal/peers/... ./internal/module/peers/... ./cmd/pdx/...`
- [ ] Commit: `git commit --only internal/peers/wire.go internal/module/peers/send.go cmd/pdx/peers.go cmd/pdx/msg.go cmd/pdx/peers_test.go cmd/pdx/msg_test.go -m "fix(peers): an ambiguity refusal names each candidate's ref"`

**Acceptance:** all three render cases pass, the two renderers agree, and dropping the `HasSuffix` arm reddens a test.

---

## Task B2 — the test that should have existed

Needs A2 and B1. This is v4 §9.7 written as an automated test, because §9.7 was the acceptance item that got skipped and it took this defect with it.

- [ ] Read `internal/module/peers/e2e_test.go:1149-1192` first. The existing ambiguity case uses `twin-1`/`twin-2` — **two processes of one conversation, with different names**, hence different addresses, hence self-distinguishing under v4. It covers the case that does not need refs.
- [ ] **Red.** Add the case that does: **two conversations sharing one registry name**, both live, on the local host. Assert:
  1. the bare name is refused `ambiguous`;
  2. the refusal carries two candidates with **identical addresses** and **different refs**;
  3. each ref, sent as `<host>/_<ref>`, delivers to its own row and not the other.
- [ ] **Mutation M5 — mutate the population, not the struct.** Remove `Ref: c.Ref` from where the candidate is built (`send.go:335`), leaving `AmbiguousCandidate.Ref` in place. Deleting the field instead would be a **compile error**, and a compile error proves nothing about whether the refusal is usable — it is the "it went red, so it must be covered" trap. This test must fail on step 2's *two refs differing*, with everything still compiling. Revert.
- [ ] Do not modify the `twin-1`/`twin-2` test. It covers something still true.
- [ ] `go test -race -count=1 ./internal/module/peers/...`
- [ ] Commit: `git commit --only internal/module/peers/e2e_test.go -m "test(peers): two conversations sharing a name are told apart by ref"`

**Acceptance:** the new e2e passes, the old one is untouched, and the mutation fails it for the right reason.

---

## Task C1 — verification sweep

- [ ] `go build ./... && make build` — the daemon builds and `pdx version` reports the ldflags version.
- [ ] `go test -race -count=1 ./...` — full suite. Rerun `internal/module/agent` and `internal/agent/probe` alone if either reddens (#1092).
- [ ] `git status --porcelain` — the repo-root `pdx` binary is **not** modified. If it is, `git checkout -- pdx` and find which step ran a bare `go build`.
- [ ] Re-run **M1–M5** in one pass and record each as red, by id. A mutation that has gone green since its own task is a regression in the test, not in the code — report it as such rather than re-deriving whether it matters.
- [ ] Confirm the spec's §6.1 list is fully covered: walk it line by line against the test names, and report any line with no test rather than assuming one exists.
- [ ] Commit any fixes with an explicit file list.

**Acceptance:** full suite green, **M1–M5 all red**, `pdx` binary unmodified, §6.1 walked with gaps named.

---

## Real-machine acceptance (spec §6.4) — after merge, on mlab

Not a task for a subagent. **Every item is run or reported as unrun** — §9.7 of v4 was skipped silently and that is exactly how this defect shipped.

1. `pdx msg send mlab/<name>` to another local session delivers, and the receiver's message header shows `from: uds:/tmp/cc-socks/<sender pid>.sock`.
2. The receiver replies natively; the reply reaches the sender.
3. Every address `pdx peers` prints is one `pdx msg send` accepts.
4. Two same-named local sessions: the refusal lists both with distinct refs, and each ref delivers to the intended one.
5. `pdx msg send <your own address>` is refused `self_target` and the inbox receives nothing.
