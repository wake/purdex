# Member relay: a person's /relay, and relays across hosts (spec + plan)

Status: draft, 2026-10-10. Owner: A line (1f). Builds on `2026-10-06-lead-team-relay-spec.md` (U9, U13, §8.2, §8.4,
§8.7), `2026-10-10-member-relay-ask-spec-plan.md` (the ask, D1–D11), `2026-10-09-rq2-member-relay-approval-spec.md`
(the member relay gate), `2026-10-09-cross-host-team-spec-plan.md` (X-U1…X-U8, commands, facts, `moved` reserved in
§4.5 / §6.3 / §12) and plan v3 P6-7a / P6-7b (the original cross-host relay sketch, never built).

## 0. Decisions

User decisions (2026-10-10, relayed by the interface lead `mlab/purdex-88-b8`; do not reopen):

- **U-M1.** 「用 /relay 指令不應該被 member 身分擋住，這是使用者層級的權限要求。」A member's "self relay off" only
  stops relays **the mod starts by itself**. A person typing **`/relay` (or `/relay now`) in a member's session is
  always let through**, on the ordinary session's path (the approval card as today), for local and cross-host members
  alike. After it, the roster follows the lineage: the member row carries the new session and ref and is still the same
  lead's member (no release / adopt).
- **U-M2.** (88's request, the cross-host spec's §12 item) A lead can relay a member that lives on another host with
  the same `pdx relay <alias>/_<ref>`, and such a member's 70% ask (682) reaches its lead.

Derived by 1f (from U-M1, U-M2, the cited specs and main at `8c85fc194`):

- **D1. "Manual" is a property of the begin.** `RelayBeginRequest` gains `manual bool`; only the mod's `/relay` /
  `/relay now` handler (`relayNow`, `register.js`) sets it; the threshold path (`maybeBegin`) never does. It is stored
  in the `self_relay` row's payload (`SelfRelayPayload.manual`), so every later gate reads it from the row, not from a
  caller. A `pdx relay begin` typed by an agent with `--manual` still opens a card a person must approve (D2), so the
  flag grants nothing a person does not confirm.
- **D2. What "manual" lifts, and only that.** For a member (local or remote) with `manual`: the three member refusals
  are lifted — begin (`relay_handler.go:252`), the click's approve (`closeSelfRelayApprovedIn`, `team_store.go`) and
  the unattended create (`CreateSelfRelayApproved`, `store_unattended.go:53`). Unchanged: the host switch (a member's
  manual relay is judged by the **ordinary-session** switch, as for a session with no role), the one-open-op rule, the
  hold, the compact rule, the approval card. **The pause (`/relay off`) is ignored for a member's manual begin**: a
  member cannot lift it (`/relay on` is refused `member_relay_is_leads`), and the pause is about relays the mod starts.
  **Quota:** a member has no quota of its own (#2062 rule 4), so under unattended mode a member's manual relay is
  never approved automatically — it always waits for a person's click — and it never spends the lead's member pool
  (that pool pays for relays the lead runs). Without `manual` everything stays as today.
- **D3. The lead is told.** When a member's manual relay reaches `done`, the lead gets one best-effort notice,
  `[pdx team] member 由使用者手動接力：<old ref> → <new ref>` (the existing `outcomeNotice` path, which today only
  speaks for `member` ops). A failed or cancelled manual relay is not announced (the person who typed it sees it).
- **D4. A cross-host relay runs on M.** The member's mod talks only to its own daemon (X-U3), so the relay op, its
  claim / write / clear / seed, and the handoff file are all M's, exactly as a local member relay on M. L records the
  intent and learns the result by facts (X-U4: "it moved to a new session id" is the reserved `moved`).
- **D5. A lead's relay of a remote member is gated on L, applied on M.** The member pool and the `member_relay` card
  are the lead's (RQ-2), so `memberRelayGate` runs on L. Then L sends a `relay` command (X-U2: recorded on L first).
  M refuses it unless the binding holds and **`AllowTeam` is on** (it acts on a running session, like `kill`).
- **D6. A remote member's ask.** M stores the ask (its clock, its 5-minute window, D4 of the ask spec) and sends a
  `relay_ask` fact; L delivers the lead's notice naming `pdx relay <alias>/_<ref>` and keeps a mirror row so the
  notice is retried as for a local ask. No clocks are compared across hosts: the fact carries `expires_in_s`.
- **D7. Old refs keep working on L.** L keeps every previous ref of a remote row (`remote_member_refs`), so
  `<alias>/_<old ref>` still targets it (plan v3 decision 7).
- **D8. Never queue a fact the lead host cannot take.** The facts pump holds an unannounced kind at the head of the
  host's FIFO (`outbox_pump.go:223-239`), which would also hold that host's `ended` facts. So M starts a remote
  member's relay (manual begin, `relay` command, ask) **only after reading L's capabilities** (`GET /api/peers`,
  3 s, as the remote adopt does) and finding the kinds it will need (`moved` and `relay_failed`; `relay_ask` for an
  ask). Not announced or not reachable → 409 `relay_unsupported` (detail names which), and nothing is written. The
  facts themselves are enqueued later without a second read (L announced them when the relay began; a downgrade in
  between is the pump's existing held-head case).
- **D9. A `relay` command expires like `adopt` / `spawn` (X-U8).** Not done within **10 min** of its creation: L stops
  sending it and enqueues `void {command_id}`. M on that void: the command never arrived → recorded in
  `team_command_voids` (a late copy answers `command_void`), outcome `{not_applied}`; it was applied and the op is
  still `requested` (not claimed) → the op is cancelled (`remote_unreachable`), outcome `{undone}`; the op was already
  claimed → outcome `{too_late}` and the relay finishes (its `moved` / `relay_failed` follows). L: `not_applied` /
  `undone` → the `forwarded` op ends `failed{remote_unreachable}`; `too_late` → it stays `forwarded` until the fact.
  A relay delivered hours late never starts by surprise; one already under way is never cut in half.
- **D10. L's relay timers skip `forwarded`.** The unseen / stall / boot reconciliation of `relay_ops`
  (`relay_timeouts.go`, `reconcileOpsFromFrames`) act only on ops whose session is on this host; a `forwarded` op ends
  only by a fact, the command's refusal or D9. M runs those timers on its own op, and their failures reach L as
  `relay_failed`.

## 1. Flows

```
(A) manual, local member        /relay → begin{self, manual} → self_relay card (person approves; unattended: still a card)
                                → op → write → clear → cleared moves team_members (moveTeamRoles, as today) → done
                                → lead notice (D3)
(B) manual, remote member on M  /relay → M: begin{self, manual}; M reads L's caps (D8) → card on M → … → cleared:
                                M moves remote_members + enqueues fact moved{op_id:"", manual:true} (one tx) → done
                                L: moved → row session/ref replaced, old ref kept (D7), lead notice (D3)
(C) lead relays a remote member L: pdx relay air26/_ref → L gate (pool / member_relay card) → L op `forwarded`
                                + command relay{op_id} (one tx; held until the card is approved)
                                M: command → checks (§3.3) + caps (D8) → local member op id=op_id → control message
                                → claim … cleared: remote_members moved + fact moved{op_id} (one tx) → done
                                M op failed / cancelled → fact relay_failed{op_id, state, reason}
                                L: moved → op done + row moved + notice; relay_failed → op failed/cancelled + notice
(D) remote member's ask         M: mod ask → M checks caps (D8) → relay_asks row + fact relay_ask (one tx)
                                L: relay_ask → mirror ask row + lead notice (retried while open) → lead runs (C)
                                M: the relay command accepts M's open ask in the op's transaction
```

## 2. Data

**L (lead host)**
- `relay_ops` (no schema change): a forwarded relay is a row with `kind='member'`, `host_id = M's host id`,
  `session_id` / `ref` = the member's on M, `team_id`, and the new state **`forwarded`** (non-terminal; the existing
  `relay_ops_one_open` index keeps one per session). `forwarded → done | failed | cancelled` only by a fact or the
  command's refusal. `pdx relay --wait` long-polls it as any op.
- `remote_member_refs` (new table, `CREATE TABLE IF NOT EXISTS`; no migration): `(host_id, ref) PRIMARY KEY, mk, at`.
  `matchRemoteMember` (`team_handler.go:542`) falls back to it when no row of that host has the ref.
- `relay_asks` (existing): L mirrors a remote ask with `spawn_op = mk`, `session_id` = the member's session on M,
  `expires_at = received + expires_in_s`. Its existing notice retry (ask spec §3.1) and accept (in the op's
  transaction) apply unchanged.
- `team_fact_log` (existing): the three new kinds are logged like `ended`.

**M (member host)**
- `relay_ops`: a remote member's op (B, C) is an ordinary row; `team_id` is the lead host's team id (a string; M has
  no `teams` row for it). For (C) its id is L's `op_id` (one id end to end; a replayed command finds it).
- `remote_members`: `cleared` replaces `member_session_id`, `ref`, `pid`, `proc_start`, `pane_id` and `title` of the
  active row (by `mk`) in the lineage transaction; the unique-active index moves with it.
- `relay_asks`: a remote member's ask is an ordinary row with `spawn_op = mk`.
- `team_facts` (outbox): the new kinds.

## 3. Daemon

### 3.1 Begin, approve, unattended (D1, D2)

- `handleRelayBegin`: `role == roleMember && !req.Manual` → 409 `member_relay_is_leads` (as today). With `manual`:
  the state is computed as for a session with no role (ordinary switch, pause ignored); a remote member also runs D8
  before anything is written. The payload carries `manual: true`.
- `closeSelfRelayApprovedIn`: a member's row is cancelled `member_relay_is_leads` **unless its payload says manual**.
- `CreateSelfRelayApproved` (unattended): a member's manual row is never created approved; the begin falls back to an
  open card (D2). The quota spend never runs for a member.
- `moveTeamRoles` (`relay_store_report.go`): a remote member's `cleared` no longer fails with
  `ErrClearedRemoteMember`; it moves the `remote_members` row and enqueues `moved` in the same transaction (§3.2).
  The local branch is unchanged.

### 3.2 Facts M → L

`TeamFact` kinds (cross-host spec §6.3), all bound to the member row `(host_id == principal.HostID, mk)`:

| kind | body | sent when (M, same tx) | applied on L (one tx) |
|---|---|---|---|
| `moved` | `{op_id, new_session_id, new_ref, pid, proc_start, title, manual}` | the `cleared` of a remote member's op | the active row's session / ref / pid replaced, old ref → `remote_member_refs`; the `forwarded` op `op_id` (if any) → `done` with `new_session_id` / `new_ref`; after commit the done notice (`RelayDoneNoticeFmt`, or D3's for `manual`), refs written `<alias>/_<ref>` |
| `relay_failed` | `{op_id, state: failed\|cancelled, reason}` | M's op for `op_id` ends failed / cancelled (only ops that came from a `relay` command) | the `forwarded` op → that state and reason; after commit the existing failed / cancelled notice |
| `relay_ask` | `{ask_id, used_pct, window, expires_in_s}` | M stores a remote member's ask | the mirror ask (§2); the lead notice as `RelayAskNoticeFmt` with `<alias>/_<ref>` |

A `moved` whose row is not active (released, killed, gone meanwhile) is logged and ignored (rule 5); its `forwarded`
op still ends `done` (the relay happened on M). A fact for an unknown `op_id` updates the row only.

L announces the three kinds in `fact_kinds` (`team_caps.go:23`).

### 3.3 Command L → M: `relay`

`TeamCommand{kind: "relay", mk, team_id, op_id}`. M, in one transaction under `createMu`: binding (§6.1); `AllowTeam`
on, else 403 `host_not_allowed`; the `remote_members` row `mk` active, of that team and lead host, else
`not_your_member`; the member's mod protocol ≥ `MinMemberRelayModVersion`, else `relay_unsupported`; no open op for
the session, else `relay_open`; L's caps carry `moved` and `relay_failed` (D8), else `relay_unsupported`. Then it
inserts the op (id `op_id`, state `requested`), accepts an open ask of that session (as `CreateMemberRelayOp`), and
logs the command; after commit it sends the control message through the remote-notice seam (`deliverTeamNotice`,
cross-host spec §4.4: M's template, the lead as sender, the session re-validated). Outcome `{accepted}`; a replay of
the same id answers the stored outcome.

L: a refusal outcome ends the `forwarded` op `failed` with the code (the lead's `--wait` sees it). M announces
`relay` in `kinds`; L refuses `pdx relay` to a host that does not (409 `relay_unsupported`, as today) **before** the
gate spends anything.

### 3.4 L's relay create for a remote member

`handleRelayCreate` (`relay_member.go`): a remote row no longer answers `relay_unsupported` when M announces `relay`.
The liveness / mod / pid checks are M's (§3.3); L checks the row is `active` and M's caps. `CreateMemberRelayOp`
gains the remote case: the membership read accepts the remote row (`host_id = M`) and the op is inserted `forwarded`
with the `relay` command **in the same transaction** — or, when the gate holds it for a `member_relay` card
(`awaiting_approval`), the command is enqueued by the approve transaction that moves the op to `forwarded`.

### 3.5 M's ask route for a remote member

`POST /api/relay/ask` (`relay_ask.go:47`): `ErrAskRemote` becomes the D8 check; passing it, the ask is inserted as
for a local member and `relay_ask` is enqueued in the same transaction. M's sweeper expires it as a local ask; the
mirror on L expires on L's clock. The mod is unchanged (it already treats `relay_unsupported` as "stay quiet").

## 4. Mod

- `relayNow` sends `manual: true` on begin (`pdx relay begin --manual`). The `member_relay_is_leads` branch stays for
  an older daemon (it then still answers that code). Protocol version +1, so a daemon can tell.
- No other mod change: claims, write, clear, seed and the ask are the existing code paths on M.
- `pdx-team` skill: "your relay is the lead's to start" gains "— or the person's, by typing /relay".

## 5. Plan (each PR ≤ 800 lines / 20 files; the later PR rebases)

| PR | Content | Hosts | Est. |
|---|---|---|---|
| **MR-1** | D1–D3 for **local** members: `manual` on the wire and in the payload, the three gates, unattended → card, D3 notice; mod `relayNow` + protocol bump; skill line. A remote member's manual begin still answers `relay_unsupported` (detail: until MR-2). Needs `pdx setup`. | mlab, air26 | 350 / 10 |
| **MR-2** | (B): `moved` fact (M side in `cleared`, L side apply), `remote_member_refs` + `matchRemoteMember` fallback, L announces `moved` / `relay_failed`, D8 caps read for a remote member's manual begin, D3 notice on L. | both | 600 / 12 |
| **MR-3a** | (C) L side: `forwarded` state, remote case of `CreateMemberRelayOp`, `relay` command enqueue (create or approve), refusal → op failed, `moved` / `relay_failed` close the op, M's `relay` kind checked before the gate, D9 expiry + `void` outcomes, D10 timers skip `forwarded`. | both (L) | 600 / 12 |
| **MR-3b** | (C) M side: the `relay` command (§3.3), control through `deliverTeamNotice`, `relay_failed` from M's op end, M's `void` of a `relay` (D9), M announces `relay`. | both (M) | 550 / 10 |
| **MR-4** | (D): M's ask route for remote rows + `relay_ask` fact; L's mirror ask + notice; M's ask accepted by the `relay` command. | both | 450 / 10 |

Order: MR-1 → MR-2 → MR-3a → MR-3b → MR-4. MR-1 and the X-line App need nothing from 88 (the card is today's
`self_relay` card). Every PR: TDD, red commit first, mutation gates below, only affected packages, codex R1 + R2.
Deploy: mlab + air26 together (feedback_air26_upgrade_with_mlab); MR-1 and any mod change need `pdx setup` with the
two relay gates.

## 6. Tests and mutation gates

- MR-1: a member's manual begin opens a card and relays; its non-manual begin is still `member_relay_is_leads`
  (mutation: drop the `!manual` → red); approve keeps a manual member row approved and cancels a non-manual one
  (mutation: read `manual` from the request instead of the row → red); unattended with a member: card, no spend
  (mutation: spend → red); paused member + manual → allowed (mutation: honour the pause → red); host switch off →
  `self_relay_off`; `cleared` moves the member row (existing) and the done notice says 手動 (mutation: no notice → red).
- MR-2: M's `cleared` of a remote member writes the row move and `moved` in one tx (fault injection between them →
  neither); L applies `moved` once (replay → stored outcome), old ref still matches (mutation: no fallback → red); a
  `moved` for a released row moves nothing; D8: L without `moved` in caps → manual begin refused, nothing written
  (mutation: skip the caps read → red, and the FIFO test shows a held head).
- MR-3a/3b: the op and the command in one tx (mutation: enqueue after commit → red); a held card enqueues on approve
  only; M refuses with `AllowTeam` off / not active / old mod / open op / L caps missing, each → L op `failed{code}`;
  replayed command → same outcome, one op; M op failed → `relay_failed` → L op failed + notice; `--wait` returns on
  `moved` with the new ref; one id end to end; D9: void before arrival → `command_void` for a late copy, void while `requested` → op cancelled, void after the claim → `too_late` and the relay completes (mutation: cancel a claimed op → red); D10: a `forwarded` op survives L's stall timer and L's boot (mutation: let the stall rule see it → red).
- MR-4: ask stored + fact in one tx; L notice text names `<alias>/_<ref>`; the relay command accepts M's ask; L's
  mirror expires on L's clock (no cross-host time compare: the fact carries `expires_in_s` only).

## 7. Acceptance (after MR-3b, MR-4 for the ask)

mlab lead, air26 member (the iOS seat or a throwaway): (1) person types `/relay` in a local member → card → approve →
`pdx team` shows the new ref, old ref still answers `pdx msg`, the lead got the 手動 notice. (2) Same on the air26
member → card on air26 → approve → mlab's `pdx team` shows the new ref within a pump cycle. (3) `pdx relay
air26/_<ref> --wait 9m` → done with the new ref. (4) Drive the air26 member past 70% → the lead's notice names
`air26/_<ref>` → `pdx relay` within 5 min → relayed. Gates: no relay op open on either host before each deploy step.

## 8. Out of scope

A remote **lead** (a lead on M relaying itself is a local self relay and already works); relaying a member that is
`joining` / `releasing` / `killing`; moving a team between hosts; App changes beyond today's cards (88 may later label
the manual card "member").
