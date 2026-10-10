# Member relay: a person's /relay, and relays across hosts (spec + plan, v2)

Status: v2, 2026-10-10 (v1 reviewed by codex `task-mv2eamdq-nkm8gm`: 6 critical, 12 important, 2 minor — every one
answered in §9). Owner: A line (1f). Builds on `2026-10-06-lead-team-relay-spec.md` (U9, U13, §8.2, §8.4, §8.7),
`2026-10-10-member-relay-ask-spec-plan.md` (the ask, D1–D11), `2026-10-09-rq2-member-relay-approval-spec.md` (the
member relay gate, §4.3 / §4.5), `2026-10-09-cross-host-team-spec-plan.md` (X-U1…X-U8, rules §3.1, unpairing §3.2,
void §3.3, the notice seam §4.4, `moved` reserved in §4.5 / §6.3 / §12) and plan v3 P6-7a / P6-7b (never built).

## 0. Decisions

User decisions (2026-10-10, relayed by the interface lead `mlab/purdex-88-b8`; do not reopen):

- **U-M1.** 「用 /relay 指令不應該被 member 身分擋住，這是使用者層級的權限要求。」A member's "self relay off" only
  stops relays **the mod starts by itself**. A person typing **`/relay` (or `/relay now`) in a member's session is
  always let through**, on the ordinary session's path (the approval card as today), for local and cross-host members
  alike. After it, the roster follows the lineage: the member row carries the new session and ref and is still the same
  lead's member (no release / adopt).
- **U-M2.** (88's request; the cross-host spec's §12 item) A lead can relay a member that lives on another host with
  the same `pdx relay <alias>/_<ref>`, and such a member's 70% ask (682) reaches its lead.

Derived by 1f (from U-M1, U-M2, the cited specs and main at `8c85fc194`):

- **D1. "Manual" is a property of the begin.** `RelayBeginRequest` gains `manual bool`. In the mod, the shared
  `begin(...)` helper (`register.js:611`, the only place that builds the `pdx relay begin` argv) gains a `manual`
  parameter; `relayNow` (`register.js:796`) passes true, the threshold path (`maybeBegin`, `register.js:650`) false.
  The daemon stores it in the `self_relay` row's payload (`SelfRelayPayload.manual`), so every later gate reads it from
  the row, never from a caller. It is part of the begin's replay identity: the same `request_id` with another `manual`
  is `id_conflict` (`relay_handler.go:213`, the comparator at `:515`). A `pdx relay begin --manual` typed by an agent
  still opens a card a person must approve (D2), so the flag grants nothing a person does not confirm.
- **D2. What "manual" lifts, and only that.** For a member (local or remote) with `manual`, the member refusals are
  lifted at all four places: begin's first role check (`relay_handler.go:252`), begin's re-check under `createMu`
  (`relay_handler.go:306`), the click's approve (`closeSelfRelayApprovedIn`, `team_store.go`) and the unattended
  create (`CreateSelfRelayApproved`, `store_unattended.go:53`). Unchanged: the host switch (a member's manual relay is
  judged by the **ordinary-session** switch, as for a session with no role), the one-open-op rule, the hold, the
  compact rule, the approval card. **The pause (`/relay off`) is ignored for a member's manual begin**: a member cannot
  lift it (`/relay on` is refused `member_relay_is_leads`), and the pause is about relays the mod starts. **Quota:** a
  member has no quota of its own (#2062 rule 4), so under unattended mode a member's manual relay is never approved
  automatically — it always waits for a person's click — and never spends the lead's member pool (that pool pays for
  relays the lead runs). Without `manual` everything stays as today.
- **D3. The lead is told.** The self op's `team_id` is empty today, so `outcomeNotice` (`relay_reconcile.go:135-150`,
  member ops with a team only) cannot reach a lead. **The `cleared` transaction that moves a member row also writes
  that row's `team_id` onto the self op** (`relay_ops.team_id`, an existing column). `outcomeNotice` then also speaks
  for a `self` op with a `team_id` at `done`: `[pdx team] member 由使用者手動接力：<old ref> → <new ref>` (only a manual
  begin can reach `cleared` as a member, D2). A failed or cancelled manual relay is not announced (the person who typed
  it sees it). For a remote member the notice is L's, on `moved` (§3.2).
- **D4. A cross-host relay runs on M.** The member's mod talks only to its own daemon (X-U3), so the relay op, its
  claim / write / clear / seed and the handoff file are all M's, exactly as a local relay on M. **A lead's relay (C)**
  is recorded on L first and sent as a command (X-U2). **A person's relay on M (B)** is a change that happens on M,
  of the same class as the session ending: M applies it locally and reports it as a fact (X-U4 names it: "it moved to
  a new session id"). X-U3's "changed only by the lead's commands" already has that exception for `ended`; U-M1 adds
  `moved` to it. L learns of (B) only from the fact.
- **D5. A lead's relay of a remote member is gated on L, applied on M.** The member pool and the `member_relay` card
  are the lead's (RQ-2), so `memberRelayGate` runs on L. Then L sends a `relay` command. M refuses it unless the
  binding holds and **`AllowTeam` is on** (it acts on a running session, like `kill`).
- **D6. A remote member's ask.** M stores the ask (its clock, its 5-minute window) and sends a `relay_ask` fact; L
  delivers the lead's notice naming `pdx relay <alias>/_<ref>` and keeps a mirror row so the notice is retried as for
  a local ask. No clocks are compared across hosts: the fact carries `expires_in_s`, computed **when the pump sends it**
  is not possible (the body is fixed at enqueue), so the pump **drops a `relay_ask` whose ask is no longer open on M**
  instead of sending it, and L's mirror expires at `received + min(expires_in_s, 300)`. Residual: a fact delayed in
  M's queue makes L's mirror outlive M's ask by at most that delay; an approval in that gap is an ordinary lead relay
  (ask spec D9), so nothing is lost but the "accepted" mark on M's ask.
- **D7. Old refs keep working on L.** L keeps every previous ref of a remote row (`remote_member_refs`), so
  `<alias>/_<old ref>` still targets it (plan v3 decision 7).
- **D8. A fact must never hold the lead host's queue.** The facts pump holds an unannounced kind at the head of the
  host's FIFO (`outbox_pump.go:223-239`), which would also hold that host's `ended` facts. Per kind:
  - `moved`, `relay_failed` **from a lead's relay (C)**: L sent the `relay` command, so L is a version that applies
    them (MR-3a ships the command, both facts and the announcement together). No check needed.
  - `moved` **from a person's relay (B)**: U-M1 forbids refusing the relay, so nothing is checked before it. The
    entry gets the new pump policy **`drop_if_unannounced`**: when the lead host's fresh capabilities do not list the
    kind, the entry is marked `dropped` (logged once), never held. Unreachable is not "unannounced": the entry waits
    like any other. Residual on an older L: its row keeps the old session and ref (commands still reach the member by
    `mk`; `ended` still arrives); `pdx team` there shows the old ref until the member ends.
  - `relay_ask`: the ask is the mod's own (not U-M1). M reads L's capabilities (`GET /api/peers`, 3 s, as the remote
    adopt does) before writing anything; `relay_ask` not announced or L unreachable → 409 `relay_unsupported` and the
    mod stays quiet (D10 of the ask spec). The entry also gets `drop_if_unannounced` (a downgrade after the read).
- **D9. A `relay` command expires like `adopt` / `spawn` (X-U8)**, on both sides: L's outbox lists `relay` among the
  expiring kinds (`outbox_store.go:63`) and M's receiver refuses an expired copy (`commands_store.go:154`). Not done
  within **10 min**: L stops sending it and enqueues `void {command_id}`. M answers the void under `createMu` from the
  op's state at that moment: the command never arrived → recorded in `team_command_voids` (a late copy answers
  `command_void`), outcome `{not_applied}`; applied and the op still `requested` (not claimed) → the op is cancelled
  (`remote_unreachable`), outcome `{undone}`; the op claimed **or already ended (done / failed / cancelled — the
  command's ack was lost)** → outcome `{too_late}`, the relay is left alone and its fact follows or has already gone.
  L: `not_applied` / `undone` → the op ends `failed{remote_unreachable}` **if it is still `forwarded`**; `too_late` →
  nothing (the fact ends it, or already did). A relay delivered hours late never starts by surprise; one under way is
  never cut in half; a finished one is never contradicted.
- **D10. Monotonic, `forwarded` only.** On L every transition of a forwarded op is a CAS **from `forwarded`**: the
  `relay` command's refusal, `moved`, `relay_failed`, the void outcome and unpairing (D11). Whichever lands first ends
  it; the others find a terminal state and are logged and ignored (cross-host rule 5). The row's move by `moved` is
  independent of the op's state (the session did move on M). L's unseen / stall timers and boot reconciliation
  dispatch on kind and state (`relay_timeouts.go:35,49`; `relay_reconcile.go:116`, `pastClaimed`), so `forwarded`
  falls through them today; a test pins it (a later state added to those switches must not catch it).
- **D11. Unpairing ends forwarded ops.** The unpair / `unpaired_by_peer` clean-up on L (cross-host §3.2, which drops
  the host's outbox entries) also ends that host's `forwarded` ops `failed{unpaired}` in the same transaction;
  otherwise nothing would ever end them and `relay_ops_one_open` would block the next relay of that session.

## 1. Flows

```
(A) manual, local member        /relay → begin{self, manual} → self_relay card (a person approves; unattended: still a
                                card) → op → write → clear → cleared moves team_members and stamps the op's team_id
                                (one tx) → done → lead notice (D3)
(B) manual, remote member on M  /relay → M: begin{self, manual} (no capability gate) → card on M → … → cleared: M moves
                                remote_members + enqueues moved{op_id:"", manual:true} (drop_if_unannounced) (one tx)
                                L: moved → row session / ref replaced, old ref kept (D7), lead notice (D3)
(C) lead relays a remote member L: pdx relay air26/_ref → M announces `relay`? → L gate (pool / member_relay card) →
                                op `forwarded` + command relay{op_id} (one tx); a held card: approve moves
                                awaiting_approval → forwarded + enqueues (one tx), no local control message
                                M: command → checks (§3.3) → member op id=op_id → control via the notice seam → claim …
                                cleared: remote_members moved + fact moved{op_id} (one tx) → done
                                M's op failed / cancelled → fact relay_failed{op_id, state, reason}
                                L: moved → op done (from forwarded) + row moved + notice; relay_failed → op ended + notice
(D) remote member's ask         M: mod ask → caps read (D8) → relay_asks row + fact relay_ask (one tx)
                                L: relay_ask → mirror ask (by ask id) + lead notice (retried while open) → lead runs (C)
                                M: the relay command accepts M's open ask in the op's transaction
```

## 2. Data

**L (lead host)**
- `relay_ops` (no schema change): a forwarded relay is a row with `kind='member'`, `host_id = M's host id`,
  `session_id` / `ref` = the member's on M, `team_id`, and the new non-terminal state **`forwarded`**
  (`relay_ops_one_open` keeps one per session). It ends only as D10 lists. `pdx relay --wait` long-polls it as any op.
- `remote_member_refs` (new table, `CREATE TABLE IF NOT EXISTS`; no migration): `(host_id, ref) PRIMARY KEY, mk, at`.
  `matchRemoteMember` (`team_handler.go:542`) falls back to it when no row of that host has the ref.
- `relay_asks` (existing): L mirrors a remote ask with **`id` = M's ask id** (`INSERT … ON CONFLICT(id) DO NOTHING`, so
  a second fact for the same ask makes neither a second mirror nor a second notice), `spawn_op = mk`, `session_id` =
  the member's session on M, `expires_at = received + min(expires_in_s, 300)`. Its notice retry (ask spec §3.1) and
  its accept in the op's transaction apply unchanged.
- `team_fact_log` (existing): the new kinds are logged like `ended`.

**M (member host)**
- `relay_ops`: a remote member's op (B, C) is an ordinary row; `team_id` is the lead host's team id (a string; M has no
  `teams` row for it). For (C) its id is L's `op_id` (one id end to end; a replayed command finds it).
- `remote_members`: `cleared` replaces `member_session_id`, `ref`, `pid`, `proc_start`, `pane_id` and `title` of the
  active row (by `mk`) in the lineage transaction; the unique-active index moves with it.
- `relay_asks`: a remote member's ask is an ordinary row with `spawn_op = mk`.
- `team_facts` (outbox): the new kinds; `moved` from (B) and `relay_ask` carry the `drop_if_unannounced` policy (a
  column or a kind table — the implementer's choice, stated in the PR).

## 3. Daemon

### 3.1 Begin, approve, unattended (D1, D2)

- `handleRelayBegin`: both role checks (`:252`, `:306`) answer `member_relay_is_leads` only `!req.Manual`. With
  `manual`, the state is computed as for a session with no role (ordinary switch, pause ignored). The payload carries
  `manual: true`; the replay comparator includes it.
- `closeSelfRelayApprovedIn`: a member's row is cancelled `member_relay_is_leads` **unless its payload says manual**.
- `CreateSelfRelayApproved` (unattended): a member's manual row is never created approved; the begin falls back to an
  open card. The quota spend never runs for a member.
- `moveTeamRoles` (`relay_store_report.go`): a local member row that moves stamps its `team_id` on the op (D3). A
  remote member's `cleared` no longer fails with `ErrClearedRemoteMember`: it moves the `remote_members` row and
  enqueues `moved` in the same transaction (§3.2). Until MR-2 lands, a remote member's manual begin answers
  `relay_unsupported` (MR-1 keeps `ErrClearedRemoteMember`, so no relay can strand there).

### 3.2 Facts M → L

All three bind to the member row `(principal.HostID, team_id, mk)` (cross-host §4.5); each is idempotent by fact id
(the fact log) and applies in one transaction.

| kind | body | sent when (M, same tx) | applied on L |
|---|---|---|---|
| `moved` | `{op_id, new_session_id, new_ref, pid, proc_start, title, manual}` | the `cleared` of a remote member's op (B: `op_id` "", `drop_if_unannounced`) | the active row's session / ref / pid / title replaced and the old ref into `remote_member_refs` (a row no longer active: nothing); the op `op_id`, if `forwarded`, → `done` with the new session / ref (D10); after commit the done notice (`RelayDoneNoticeFmt`, or D3's for `manual`), refs written `<alias>/_<ref>` |
| `relay_failed` | `{op_id, state: failed\|cancelled, reason}` | M's op for `op_id` ends failed / cancelled (only ops from a `relay` command) | the op, if `forwarded`, → that state and reason; after commit the existing failed / cancelled notice |
| `relay_ask` | `{ask_id, used_pct, window, expires_in_s}` | M stores a remote member's ask (`drop_if_unannounced`; the pump also drops it when the ask is no longer open) | the mirror ask (§2); the lead notice as `RelayAskNoticeFmt` with `<alias>/_<ref>` |

L announces `moved` from MR-2 and `relay_failed` from MR-3a (each when its apply ships), `relay_ask` from MR-4
(`team_caps.go:21`).

### 3.3 Command L → M: `relay`

`TeamCommand{kind: "relay", mk, team_id, op_id}`. M, in one transaction under `createMu`: binding (§6.1); the age
refusal (D9); `AllowTeam` on, else 403 `host_not_allowed`; the `remote_members` row `mk` active, of that team and lead
host, else `not_your_member`; the member's mod protocol ≥ `MinMemberRelayModVersion`, else `relay_unsupported`; no
open op for the session, else `relay_open`. Then it inserts the op (id `op_id`, state `requested`), accepts an open
ask of that session (as `CreateMemberRelayOp`) and logs the command; after commit it sends the control message through
the remote-notice seam (`deliverTeamNotice`, cross-host §4.4: M's template, the lead as sender, the session
re-validated — the mod verifies a control by the daemon's `seen`, not by its sender). Outcome `{accepted}`; a replay
of the same id answers the stored outcome. M announces `relay` in `kinds` (MR-3b).

### 3.4 L's relay create for a remote member

`handleRelayCreate` (`relay_member.go`): a remote row no longer answers `relay_unsupported` when M announces `relay`
(read before the gate, so nothing is spent for a host that cannot apply it). The liveness / mod / pid checks are M's
(§3.3); L checks the row is `active`. `CreateMemberRelayOp` gains the remote case: the membership read accepts the
remote row and the op is inserted `forwarded` with the `relay` command **in the same transaction**.

**RQ-2 delta (held card).** When the gate holds the op for a `member_relay` card, the op is `awaiting_approval` as
today. RQ-2 §4.3 / §4.5 allow `awaiting_approval → requested | cancelled` and the approve then calls
`sendMemberControl`. For an op whose `host_id` is another host the approve instead moves it
`awaiting_approval → forwarded` and enqueues the `relay` command **in the approve transaction**, and the local control
branch (`relay_member.go:176`) is skipped. Deny / expiry are unchanged (`cancelled`, nothing sent).

### 3.5 M's ask route for a remote member

`POST /api/relay/ask` (handler `relay_ask.go:17`; the `ErrAskRemote` refusal at `:43-47`): the remote refusal
becomes D8's capability read; passing it, the ask is inserted as for a local member and `relay_ask` is enqueued in the
same transaction. M's sweeper expires it as a local ask. The mod is unchanged (it already treats `relay_unsupported`
as "stay quiet").

### 3.6 Pump policy `drop_if_unannounced` (D8)

`outbox_pump.attempt` (`outbox_pump.go:223`): for an entry with this policy, a fresh capability read that does not list
its kind marks the entry `dropped` (one log line) and moves on; a capability error (unreachable) backs off as today; a
`relay_ask` whose ask is no longer `open` on M is dropped before the call. Other kinds keep today's hold.

## 4. Mod

- `begin(...)` gains `manual`; `relayNow` passes true. The `member_relay_is_leads` branch stays for an older daemon.
  Protocol version +1, so a daemon can tell.
- No other mod change: claims, write, clear, seed and the ask are the existing paths on M.
- `pdx-team` skill: "your relay is the lead's to start" gains "— or the person's, by typing /relay".

## 5. Plan (each PR ≤ 800 lines / 20 files; the later PR rebases; one merged before the next starts)

| PR | Content | Est. |
|---|---|---|
| **MR-1** | D1–D3 for **local** members: `manual` on the wire, in the payload and the replay comparator; the four gates; unattended → card; the op's `team_id` stamp + D3 notice; mod `begin(manual)` + protocol bump; skill line. A remote member's manual begin answers `relay_unsupported` (until MR-2). Needs `pdx setup`. | 400 / 12 |
| **MR-2** | (B): `moveTeamRoles` remote branch + `moved` fact on M; the `drop_if_unannounced` pump policy (§3.6); L applies `moved` (row, `remote_member_refs`, `matchRemoteMember` fallback, D3 notice on L); L announces `moved`; MR-1's remote refusal lifted. | 650 / 14 |
| **MR-3a-1** | (C) L, no card: `forwarded` state, remote case of `CreateMemberRelayOp`, `relay` command enqueue, M's `relay` kind read before the gate, refusal → op failed, `moved` closes the op, `relay_failed` apply + L announces it, D10 CASes and the timer pin. | 650 / 13 |
| **MR-3a-2** | (C) L, the rest: RQ-2 held-card delta (§3.4), D9 on L (expiring kind, void, outcomes), D11 unpair clean-up. | 450 / 10 |
| **MR-3b** | (C) M: the `relay` command (§3.3) incl. the age refusal, control through `deliverTeamNotice`, `relay_failed` from M's op end, M's `void` of a `relay` (D9), M announces `relay`. | 550 / 11 |
| **MR-4** | (D): M's ask route for remote rows + caps read + `relay_ask` fact (+ drop when closed); L's mirror by ask id + notice; M's ask accepted by the `relay` command; L announces `relay_ask`. | 450 / 10 |

Order: MR-1 → MR-2 → MR-3a-1 → MR-3a-2 → MR-3b → MR-4. Every PR: TDD, red commit first, the mutation gates below,
only affected packages, codex R1 + R2. Deploy mlab + air26 together (feedback_air26_upgrade_with_mlab); a mod change
needs `pdx setup` behind the two relay gates. MR-3a-1 must not ship a `relay` command while M cannot apply it: L reads
M's `kinds` first, and M announces `relay` only from MR-3b, so (C) is inert until MR-3b is deployed on both hosts.

## 6. Tests and mutation gates

- **MR-1:** a member's manual begin opens a card and relays; non-manual is still `member_relay_is_leads` at the first
  check **and** at the re-check under `createMu` (mutations: drop either `!manual` → red); approve keeps a manual member
  row approved and cancels a non-manual one (mutation: read `manual` from the request instead of the row → red);
  unattended + member: a card, no spend (mutation: spend → red); paused member + manual → allowed (mutation: honour the
  pause → red); host switch off → `self_relay_off`; replay with another `manual` → `id_conflict` (mutation: leave it
  out of the comparator → red); `cleared` stamps `team_id` and the lead gets the 手動 notice (mutations: no stamp / no
  notice → red); a remote member's manual begin → `relay_unsupported`; mod: `/relay` sends `--manual`, the threshold
  path does not (mutation: always manual → red).
- **MR-2:** M's `cleared` of a remote member writes the row move and `moved` in one tx (fault injection between them →
  neither); L applies `moved` once (fact replay → stored outcome), old ref still matches (mutation: no fallback → red);
  `moved` for a released row moves nothing; binding: a `moved` from another host, another team or another `mk` changes
  nothing (one mutation per predicate → red); `drop_if_unannounced`: L reachable without `moved` → the entry is dropped
  and a following `ended` is delivered (mutation: hold → the `ended` test red); L unreachable → the entry waits
  (mutation: drop on error → red).
- **MR-3a-1:** op and command in one tx (mutation: enqueue after commit → red); M without `relay` in `kinds` →
  `relay_unsupported` and nothing spent; refusal → op failed; `moved` → done with the new ref, `--wait` returns it;
  `relay_failed` → failed / cancelled; **reverse arrival**: `moved` then the command's late outcome, `relay_failed`
  then a late refusal, a refusal then a late `moved` (op stays failed, row still moves) — each terminal state kept
  (mutation: unconditional state write → red); binding per predicate for `relay_failed`; a `forwarded` op survives the
  stall timer and a boot (mutation: let the timer see it → red).
- **MR-3a-2:** a held card enqueues only on approve, and no local control is sent (mutation: call `sendMemberControl` →
  red); deny → cancelled, nothing enqueued; D9 on L: expiry → void enqueued; `not_applied` / `undone` → failed only
  from `forwarded`; `too_late` → unchanged (mutation: fail on `too_late` → red); unpair → forwarded ops failed
  `unpaired` in the clean-up tx (mutation: leave them → the next relay `relay_open` test red).
- **MR-3b:** each refusal of §3.3 (age, `AllowTeam` off, not active, old mod, open op) with its code; replayed command →
  same outcome, one op; control delivered through the seam; M op failed → `relay_failed`; void: before arrival →
  `command_void` for a late copy, while `requested` → cancelled, after the claim **or after the op ended** → `too_late`
  and nothing touched (mutations: cancel a claimed op → red; answer `undone` for an ended op → red).
- **MR-4:** ask + fact in one tx; caps missing / unreachable → `relay_unsupported`, **no ask row and no outbox entry**
  (mutation: write before the read → red); L's notice names `<alias>/_<ref>`; two facts with the same `ask_id` → one
  mirror, one notice (mutation: insert by fact id → red); the pump drops a `relay_ask` whose ask closed (mutation: send
  it → red); the relay command accepts M's ask; L's mirror window ≤ 300 s from receipt.

## 7. Acceptance (after MR-3b; MR-4 for the ask)

mlab lead, air26 member (the iOS seat or a throwaway): (1) a person types `/relay` in a local member → card → approve →
`pdx team` shows the new ref, the old ref still answers `pdx msg`, the lead got the 手動 notice. (2) The same on the
air26 member → card on air26 → approve → mlab's `pdx team` shows the new ref within a pump cycle. (3) `pdx relay
air26/_<ref> --wait 9m` → done with the new ref. (4) Drive the air26 member past 70% → the lead's notice names
`air26/_<ref>` → `pdx relay` within 5 min → relayed. Gates: no relay op open on either host before each deploy step.

## 8. Out of scope

A remote **lead** (a lead on M relaying itself is a local self relay and already works); relaying a member that is
`joining` / `releasing` / `killing`; moving a team between hosts; App changes beyond today's cards (88 may later label
the manual card "member").

## 9. Review ruling (codex `task-mv2eamdq-nkm8gm` on v1; all adopted)

| # | Finding | Where it is answered |
|---|---|---|
| 1 critical | D8 refused manual relays (vs U-M1) | D8 per kind: (B) never refused, `drop_if_unannounced`; only the ask reads caps |
| 2 critical | D3 notice path needs the self op's team | D3: `cleared` stamps `team_id`; `outcomeNotice` speaks for self ops with a team |
| 3 critical | void after M's op ended (ack lost) | D9: ended → `too_late`; L acts only from `forwarded` |
| 4 critical | unpairing strands `forwarded` ops | D11 + MR-3a-2 + test |
| 5 critical | MR-2 announced `relay_failed` before its apply | §3.2: each kind announced by the PR that applies it |
| 6 critical | no age refusal for `relay` | D9: expiring kind on L and age refusal on M; MR-3b test |
| 7 important | held card vs RQ-2 state machine | §3.4 RQ-2 delta; MR-3a-2 test |
| 8 important | no monotonic CAS | D10; MR-3a-1 reverse-arrival tests |
| 9 important | ask window restarts on L | D6: drop when closed, mirror ≤ 300 s, residual stated |
| 10 important | (B) vs X-U2 / X-U3 | D4: (B) is a fact-class change (X-U4), U-M1 adds `moved` to X-U3's exception |
| 11 important | second member gate in begin | D2, §3.1, MR-1 test |
| 12 important | replay identity misses `manual` | D1, §3.1, MR-1 test |
| 13 important | no binding gates for the new facts | §3.2 binding; MR-2 / MR-3a-1 per-predicate mutations |
| 14 important | D8 coverage incomplete | D8 rewritten; MR-2 and MR-4 tests |
| 15 important | no reverse-arrival gates | MR-3a-1 |
| 16 important | `relay_ask` dedupe by ask id | §2 mirror keyed by ask id; MR-4 test |
| 17 important | MR-3a too big | split into MR-3a-1 / MR-3a-2 |
| 18 important | D10 misstated the timers | D10 corrected (dispatch by kind/state; test pins it) |
| 19 minor | mod wiring | D1, §4: `begin(manual)` |
| 20 minor | handler line | §3.5 |
