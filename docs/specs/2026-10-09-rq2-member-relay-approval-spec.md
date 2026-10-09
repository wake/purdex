# RQ-2 — the `member_relay` approval: state machine (#2062 R4) — small spec

Date: 2026-10-09. Coordinator: purdex-1f (`mlab/_jq7pua`). Line: A (daemon + CLI); App card: interface line (88).
Parent: `docs/specs/2026-10-09-relay-quota-spec-plan.md` §3.4 (this is the "own small spec" it requires). Builds on plan v3 P6-2b as re-cut by "2026-10-09 現況對齊（P6）" §4 and ruled in §6 (`docs/specs/2026-10-06-lead-team-relay-plan-v3.md`). Lands after P6-2b-2, before P6-3a (§6 Q5).
Review: codex `task-mv0qures-y4l1ub` (17 findings) and the incremental `task-mv0rapcf-5y2z93` (4 important); rulings in §10.

## 1. What is decided (do not reopen)

- R4 (user, 2026-10-09): a member has no quota of its own; the **lead's chain** has a `member_pool_left` (default 0, set only in the App). While unattended mode is on, every member relay the lead starts spends 1 from that pool; at 0 it waits for a person. The lead's own relays spend `self_left`; the two never mix.
- R2 + U13: the rule acts only while unattended is on **and** `relay_quota.rule` is on. R2's 「使用者在場時照舊人工核准」 means "as without the quota": for a self relay that is a person's click; **for a member relay it is no approval at all** — U13's last line, a user decision: 「lead 要求 member 接力（pdx relay）不需要核准，由 lead 自己安排」 (`docs/specs/2026-10-06-lead-team-relay-spec.md:61`), and U9 (`:46`). The parent spec §3.4 says the same ("Unattended off: member relays need no approval and consume nothing"). Rule off → nothing is spent, and a held row is approved by the next sweep.
- A person's click never spends (R2). No `pdx` command approves, denies or cancels a row, or changes a pool (R1, skill).
- Control message and notices go through the PL-1d1 sender (§6 Q1).

## 2. Facts on main this stands on (alpha.640)

- `relayTransitions` (`internal/module/team/relay_store_report.go:14`) is **kind-agnostic**: `awaiting_approval → claimed | cancelled`, `requested → claimed | cancelled | failed`; checked by state only (`:194`).
- Self relay close → op: `afterClose` + `opReportForClosedRow` (`relay_handler.go:391`), a **second** transaction after the row's close; a failure there is only logged (`:407`) and repaired at the session's next begin or at boot (`reconcileRelays`, `relay_report.go:285`, run from Start only).
- The winner point `announceClosed` (`module.go:563`) runs `broadcast`, `wake(row id)`, `unhold`, `afterClose` for every won close, and `afterApproved` **only when the row is approved**. It wakes the approval's id, not an op's.
- Self approve order (`closeSelfRelayApprovedIn`, `team_store.go:245-260`): the row's CAS (`state = 'open'`, `store.go:456`) first, the spend only after it changed one row, in the same transaction.
- The sweeper (`sweeper.go:80-90`) closes an open row `timeout` when `DeadlineAt <= now` (checked first), `abandoned` when `LeaseUntil <= now`, and `abandoned` when its `Origin.SessionID` is not live — for every kind.
- A click's `Close` has no `UnexpiredAt` (`handler.go:478`): a click that lands after the deadline but before the next tick wins (`sweeper_test.go:128` accepts it). Automatic closes set `UnexpiredAt = now`.
- Auto approval: `sweepUnattended` / `autoApprove` over `team.AutoApprovable` kinds (`unattended.go:231`, `:316`; `internal/team/wire_unattended.go:36`); `withQuotaRule` marks only `self_relay` (`quota.go:284`); held set `heldQuota` + `UnattendedView.held`, filtered to `self_relay` (`quota.go:291-333`).
- `ReleaseMember` refuses while any relay op of the session is non-terminal (`release_store.go:21`); `kill` refuses only `claimed|writing|written` (`team_handler.go:220`).
- `RelayOp.RequestID` is documented as the self relay's approval id (`internal/team/wire_relay.go:74`); `RelayOpByRequest(row.ID)` and `Get(op.RequestID)` link the two (`relay_handler.go:395`, `:465`).

## 3. States and the one invariant

| Object | States used here |
|---|---|
| member op (`relay_ops`, kind `member`) | `awaiting_approval` → `requested` → `claimed` → … (P6) ; or `awaiting_approval` → `cancelled` |
| `member_relay` row (`approvals`) | `open` → `approved` \| `denied` \| `timeout` \| `cancelled` \| `abandoned` |

**Invariant:** a member op is `awaiting_approval` **iff** its `member_relay` row is `open`. Every write that changes one changes the other **in the same transaction** (§4.1, §4.3, §4.4). A member op created without a row starts at `requested` (P6-2b-1, unchanged).

**Only the row moves an awaiting op.** A report on a member op that is `awaiting_approval` (`POST /api/relay/ops/{id}/report`, whatever state it names) is refused 409 `bad_transition` with the op, before any row is touched: the report handler's `closeAsWithOp` path (`relay_report.go:89-109`, `module.go:537-553`) is **not** taken for this kind. Nothing legitimate reports on such an op — the member has not been sent the control message yet. The map entries `awaiting_approval → requested | cancelled` are used only by the row's own close transactions.

**Write lock first.** Every transaction in §4.1, §4.3 and §4.4 takes SQLite's write lock before its first read (`BEGIN IMMEDIATE`, or the repo's no-op write as `closeSelfRelayApprovedIn` does, `team_store.go:245-256`; helper at `task_store.go:112-123`), so its re-checks and its writes see one state and a concurrent `EndTeam` / kill / release serialises before or after it, never between.

**Link:** the row's id is a fresh UUID v4 the daemon generates in the create transaction; the op's `RequestID` is that id (the `wire_relay.go:74` comment widens to "the self relay's or member relay's approval id"). A member op created `requested` has `RequestID = ""`.

## 4. Transitions

### 4.1 Create (`POST /api/team/relays`, under `createMu`, in `CreateMemberRelayOp`'s one transaction — alignment §4 item 1)

After P6-2b-1's checks and its membership confirmation, the **gate**:

| unattended | rule | lead's pool | Result (same transaction) | After commit |
|---|---|---|---|---|
| off | any | any | op `requested`, nothing spent | `sendMemberControl(op)` |
| on | off | any | op `requested`, nothing spent | `sendMemberControl(op)` |
| on | on | ≥ 1 | pool spent (§4.6); op `requested` | `announceSpend(lead)`, `sendMemberControl(op)` |
| on | on | 0 | op `awaiting_approval` with `RequestID = row.ID`, **and** the `member_relay` row `open` | broadcast `opened`; `holdForQuota(row)` at once (like a self relay's exhausted begin) |

- Any failure inside the transaction (op insert, row insert, spend) rolls back all of it: no op, no row, pool unchanged.
- HTTP, unchanged from P6-2b: 201 `{op}` for a new op whatever its state; 200 with the same op on a replay of the same id **for the same target**; `id_conflict` for the same id and another target. The lead reads the op's state.

### 4.2 The `member_relay` row

- `Origin` = the lead's live origin at create. `Payload` = `MemberRelayPayload{op_id, team_id, lead_ref, lead_title, member_session_id, member_ref, member_title, used_percentage}` (the card's text; nothing in it is trusted at approve).
- `DeadlineAt = now + 600 s` (`SelfRelayDeadlineS`). It is enforced by the sweeper, so it can be late by one tick, and a click that lands in that tick wins — the same as every kind today (§2); RQ-2 does not tighten it.
- **`LeaseUntil = DeadlineAt`**: nobody renews it — the lead waits on the *op*, not the row, and in unattended mode may not be waiting at all. With the deadline branch checked first, such a row closes `timeout`, never `abandoned` (§2; test pins it).
- **Liveness is the team's, not the origin's.** The sweeper's "origin not live → abandoned" branch (`sweeper.go:89`) does **not** apply to `member_relay`: the origin is the lead session at create, which a lead's own relay ends while the row is rightly still open. Instead, a `member_relay` row is closed `abandoned` when **its op's team is no longer live** (one indexed read per open `member_relay` row per tick; such rows are few). A gone or killed *member* is the approve-time re-check's job (§4.3).
- `team.AutoApprovable(KindMemberRelay) = true`. `withQuotaRule` marks a `member_relay` close `SpendQuota` exactly as a `self_relay` one (Auto + rule on); the store spends the **pool** for this kind, never `self_left`.

### 4.3 Approve — one transaction (`CloseMemberRelayApproved`)

Both a person's click (`handleDecide`) and an automatic approve (`autoApprove`, from the switch-on, tick and boot sweeps) run, in **one** write transaction, in this order (mirroring `closeSelfRelayApprovedIn`):

1. **Re-check:** the op is `awaiting_approval`; its team is live; the op's session still has an `active` member row in that team. On failure the row is closed `cancelled` with `close_reason` `member_gone` (member row not active) or `team_ended` (team not live) and the op → `cancelled{abandoned}`, in this same transaction, nothing spent; not retried by the sweeps (like `adoptRefusedError`). This close is itself a CAS on `state = 'open'`: if it changes no row, someone else closed first — roll back, not won.
2. **Close the row** `approved` by CAS on `state = 'open'`. Zero rows → roll back, **not won, nothing spent** (a deny or timeout committed first).
3. **Spend** (only when `c.SpendQuota`): §4.6. Zero rows → `ErrQuotaExhausted`, roll back the whole transaction (row back to `open`), the row is held (`autoApprove` already holds on that error).
4. **Op `awaiting_approval → requested`**, `updated_at = now`. Any error → roll back all four steps.

After commit (`announceClosed` → `afterApproved`): `announceSpend(lead)` if spent; `sendMemberControl(op)`; `rosterChanged()`. `afterClose` is a no-op for `member_relay` (its op already moved; it must **never** report `claimed`).

### 4.4 Deny, timeout, cancelled, abandoned — also one transaction

Every other close of a `member_relay` row moves its op **in the close's own transaction**: `denied → cancelled{denied}`, `timeout → cancelled{timeout}`, `cancelled | abandoned → cancelled{abandoned}`. Nothing is spent; no control message. The generic close path (`closeAs` / `closeExpired`) gets a per-kind in-transaction step for this kind; a failure of the op write rolls the close back (the row stays `open`, the next tick or click retries). So unlike self relays there is no second transaction to lose, and no runtime gap that only a restart repairs.
- Who closes: a person (deny), the sweeper (timeout; team gone → abandoned), the re-check (§4.3 step 1). No agent path closes a `member_relay` row in RQ-2.

### 4.5 The state machine change

`relayTransitions` becomes **per kind** for the one state that differs:
- self op: `awaiting_approval → claimed | cancelled` (unchanged);
- member op: `awaiting_approval → requested | cancelled`; **`awaiting_approval → claimed` is `bad_transition`** — a member must not claim (and so relay) before the approval; the claim route (P6-2b-2) answers 409 `bad_transition` with the op.

Every other row of the map is shared by both kinds.

### 4.6 The pool spend

`spendPoolIn(tx, teamID, at)`, the sibling of `spendSelfQuotaIn` (`quota.go:241`): resolve the team's **current** lead session (`teams.lead_session_id`) on the same transaction, walk to its chain root with `chainRootIn`, then one statement `UPDATE relay_quotas SET member_pool_left = member_pool_left - 1, updated_at = ?, rev = rev + 1 WHERE root_session_id = ? AND member_pool_left >= 1`. Zero rows → `ErrQuotaExhausted`. After commit the spend is announced as a self spend is (`markSpent` / `takeSpent` / `announceSpend`, `team.relay_quota` event with the new numbers and `rev`).

### 4.7 Waking the op's waiters

Every committed change of a member op's state wakes `op.ID`'s waiters, **whatever path made it**: create, claim, report, approve (§4.3), every close of §4.4, the re-check, the boot reconciliation. P6-2b-2 puts the wake at one choke point after the commit (not in the HTTP report handler only), and RQ-2's paths go through it. `announceClosed` keeps waking the row's id as today.

### 4.8 The lead's wait (P6-5) — what RQ-2 adds to P6-5's contract

P6-5's CLI stays as planned (`pdx relay <ref> [--wait <dur>]`, default 0, cap 9 min, waits until a terminal state or the bound; plan v3 P6-5). RQ-2 adds only:
- while the op is `awaiting_approval` the CLI prints `等待核准：member 額度用完（無人值守）`;
- exit **10** `cancelled{denied}`; exit **11** `cancelled{timeout}`, **and** the bound reached while the op is still `awaiting_approval` (not 0: the relay has not started; the op is left as is, the user may still approve it and the member then relays without the lead waiting); exit **12** any other `cancelled` (unchanged). This supersedes the parent §3.4's "exit 0 on approval, 12 on deny/cancel".
- Run it in the foreground with Bash `timeout: 600000`, like `pdx lead request`.

## 5. Interactions

- **Release** while the op is `awaiting_approval`: refused `relay_open` (the existing non-terminal guard). The user denies the row first, then releases. Accepted; the release note says so.
- **Kill** while awaiting: allowed (kill's guard). The row stays until a decision or its deadline; the approve-time re-check closes it `cancelled{member_gone}`. A stale card for at most ten minutes is accepted.
- **The team ends** while awaiting: the sweeper closes the row `abandoned` within a tick (§4.2).
- **The lead relays** while awaiting: the row stays open (no lease, no origin liveness); the pool's chain root is unchanged; `spendPoolIn` and `sendMemberControl` resolve the team's **current** lead at approve time, never the row's `Origin`.
- **Switches change while a row is open:** unattended turned off → the row waits for a click (no sweep), as a self relay row does; rule turned off (unattended on) → the next tick approves it without spending; pool raised → the next tick approves it and spends 1.
- **Two member relays race for the last unit:** exactly one is spent (guarded `UPDATE`); the other opens a row (create) or stays held (sweep).
- **An automatic approve races a deny or a timeout:** whichever commits its CAS first wins; the loser rolls back and spends nothing (§4.3 step 2).

## 6. Contracts this puts on the P6 PRs (written into plan v3 with this spec)

- **P6-2b-1:** the row id / `RequestID` link (§3); `CreateMemberRelayOp`'s gate seam; `sendMemberControl` idempotent on the op id.
- **P6-2b-2:** the op wake choke point (§4.7).
- **P6-4b:** the 60 s "unseen" claim timer runs from the op's **entry to `requested`** — `updated_at + 60 s` while `state = 'requested' AND seen_at = 0` (plan v3's column is `seen_at INTEGER NOT NULL DEFAULT 0`, its seen CAS is `seen_at = 0`); no write other than the transition into `requested` may touch `updated_at` of a requested, unseen op (`seen` may, since the unseen timer then no longer applies). Not `created_at`: an op approved after nine minutes would fail `member_unresponsive` at once. P6-4b's test includes an approved-late op. (Plan v3's P6-4 branch A and the alignment §3 row 11 said `created_at + 60 s`; this replaces it.)
- **P6-4a:** the boot **re-send of the control message** for `requested` member ops is RQ-2's (§7 item 3), since RQ-2 lands first; P6-4a keeps its frame-based reconciliation and #1735 and reuses RQ-2's re-send instead of building a second one.
- **P6-5:** §4.8.

## 7. Restart reconciliation (`reconcileRelays`, extended)

1. Open `member_relay` row whose op is terminal, or member op `awaiting_approval` whose row is closed: impossible by the §3 invariant; if found, logged, and repaired toward the row (row closed → op as §4.4; op terminal → row `abandoned`).
2. Open `member_relay` row, op `awaiting_approval`: the §4.3 step-1 re-check runs (team / member gone → `cancelled`); otherwise it stays open. The held set is rebuilt by the boot sweep when unattended is on.
3. Member op `requested`: `sendMemberControl` again if the team's current lead session is live (idempotent: the claim is a CAS); otherwise left to P6-4b's claim timeout.

## 8. Display and events

- `opened` / `closed` approval events as for every kind; `team.relay_quota` on every pool spend; `rosterChanged()` after approve.
- `UnattendedView.held` lists held `member_relay` rows beside `self_relay` ones (the filter at `quota.go:328` takes both kinds).
- **SPA (88), before RQ-2's daemon is deployed:** `member_relay` in `APPROVAL_KINDS`, a parser for `MemberRelayPayload`, a card 「`<lead_title>` 要幫 member `<member_title>` 接力（context NN%）；member 額度用完，要核准嗎？」 with approve / deny through the existing decide route, and the same card in the held list. An older SPA skips the row (PL-2a), so the deploy gate is: card merged and fast-forwarded → then RQ-2's daemon.

## 9. Tests and mutation gates

Tests:
- Create: each row of the §4.1 table; fault injection after the op insert, after the row insert and after the spend → nothing persisted, pool unchanged; replay of the same id and target → 200 and the same op in each state; same id, other target → `id_conflict`.
- Approve: click (no spend) and auto (spend) each move the op to `requested` in the row's transaction and send one control message; fault injection on the op update → row still `open`, pool unchanged, no control; pool 0 → held at create, raised → approved within one tick, spent 1; rule off with a held row → approved by the tick, nothing spent.
- Races: auto approve paused after its read while a deny commits → row `denied`, op `cancelled{denied}`, pool unchanged; two creates for the last unit → one `requested`, one row.
- Closes: deny / timeout / team ended → op cancelled with the right reason in the same transaction (fault injection on the op write → row still `open`); lease = deadline → `timeout`; re-check with a killed member / ended team → `cancelled`, nothing spent, no control; the lead relays while awaiting → the row survives ten ticks, then approve spends from the chain root of the new lead (quota row on the old root) and the control goes to the new lead's inbox.
- Waking: a real op long-poll waiter returns at once on approve, deny, timeout and the re-check cancel.
- Transitions: a table test over both kinds × every (from, to) pair against §4.5.
- Pool: `rev` + 1 and one `team.relay_quota` event per spend; spend keyed by the chain root.
- Held set: after deny / timeout / re-check cancel / approve the id is no longer in `heldQuota` (internal set, not only the view).
- Claim on an `awaiting_approval` member op → 409 `bad_transition`; a `failed` or `cancelled` report on it → 409 `bad_transition`, the row still `open`, the op unchanged.
- Write lock: an approve racing `EndTeam`, a kill and a release of the member — each ordering gives either the approve (team / member still there) or the re-check's `cancelled`, never a 500 and never an approved row for a gone member.
- Boot items 1–3. `held` lists the member row.

Mutation gates (each must turn a test red): spend `self_left` instead of the pool; spend on a click; spend before the row's CAS; send the control at create while `awaiting_approval`; allow `awaiting_approval → claimed` for member ops; allow `awaiting_approval → requested` for self ops; drop `requested → failed` for member ops; move the op in a second transaction (approve or deny); let a report on an awaiting member op close its row; read before taking the write lock; skip the re-check; read the lead from the row's `Origin`; key the spend by the session id instead of the chain root; drop `rev + 1`; keep the origin-liveness branch for `member_relay`; `afterClose` reports `claimed` for an approved `member_relay` row; a close path that skips `unhold`.

## 10. Review ruling (codex `task-mv0qures-y4l1ub`, 1f)

- **#1 (critical, attended mode needs a click) — rebutted with evidence:** U13's last line and U9 (`relay-spec.md:61`, `:46`); R2's 「照舊」 is "as without the quota", which for a member relay is no approval; the parent §3.4 says so. §1 now cites them.
- **#10 (a click after the deadline wins) — wording fixed, behaviour kept:** the same for every kind today and accepted by an existing test; §4.2 no longer promises a hard edge.
- **Incremental round (`task-mv0rapcf-5y2z93`), all adopted:** I-1 a report on an awaiting member op is refused, only the row moves it (§3); I-2 write lock first (§3, §9); I-3 `seen_at = 0`, not `IS NULL` (§6); I-4 `AutoApprovable` belongs to RQ-2a (§11). No critical in that round, so no third round (stop rule).
- **Adopted:** #2 (origin liveness → team liveness, §4.2), #3 (CAS before spend, §4.3), #4 (P6-4b clock, §6), #5 / #6 (fault-injection tests, §9), #7 (row id / `RequestID`, §3), #8 (P6-5 contract, §4.8), #9 (wake choke point, §4.7), #11 (transition matrix), #12 (pool tests), #13 (HTTP replay, §4.1), #14 (boot re-send ownership, §6), #15 (every close in one transaction, §4.4), #16 (held set), #17 (fact in §2).

## 11. Size and order

Estimate ≈ 420 production + ≈ 560 test lines, ≈ 10 files — over the 800-line limit with tests, so it is planned as two PRs: **RQ-2a** — the state machine (§3 invariant, the awaiting-op report refusal and the write lock, §4.3–§4.5, §4.7 for these paths, the sweeper's team liveness, reconciliation §7, and `AutoApprovable(KindMemberRelay) = true` with `withQuotaRule`'s kind extension — dormant while nothing opens such a row) behind `KindMemberRelay` that nothing opens yet; **RQ-2b** — the create gate (§4.1), the pool spend (§4.6), the held view and events (§8). RQ-2a deploys alone (no row can open); RQ-2b only after 88's card is merged and fast-forwarded. P6-6 only after RQ-2b is deployed (§6 Q5).
