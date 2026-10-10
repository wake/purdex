# Member relay ask: a member asks its lead (spec + plan)

Status: draft, 2026-10-10. Owner: A line (1f). Builds on
`2026-10-06-lead-team-relay-spec.md` (U1, U9, U13, §8.2, §8.5),
`2026-10-08-unattended-mode-spec.md` (U23) and
`2026-10-09-rq2-member-relay-approval-spec.md` (the member relay op and its gate).

## 0. Decisions

User decisions (2026-10-10, relayed by the interface lead `mlab/purdex-88-b8`):

- **D1.** A member's automatic relay works like an ordinary session's auto relay, except that instead of
  asking the user it asks the member's **lead**.
- **D2.** Trigger: the member's usage reaches the relay threshold (70%) → at a **turn boundary**, the same
  place the ordinary auto relay asks (`turn.complete`), the member sends its lead a relay request. It is **not**
  limited to "idle" any more (today's usage notice needs `≥70% AND idle`, and a member that never idles never
  triggers it; that is how a busy member reached auto-compaction today).
- **D3.** The request does **not pause** the member: it keeps working.
- **D4.** Hold window **5 minutes**: the lead approves within 5 minutes → the relay happens at the member's next
  turn boundary. No approval within 5 minutes → treated as "no": nothing happens, and the member goes on to
  Claude Code's auto-compaction as today. No hard cap, no automatic relay by anyone else.
  (Why 5: a lead reads peer messages only at its next tool turn, and foreground waits such as
  `codex review --wait` or `pdx relay --wait` often take 2-5 minutes; since the member is not paused, waiting
  costs nothing.)
- **D5.** How the lead approves is 1f's design; the request must show in the lead's conversation like today's
  `[pdx team]` notices.
- **D6.** Quota rules unchanged: set by the user; unattended mode spends the lead's member pool as today (RQ2).

Derived by 1f (from D1-D6 and the code):

- **D7.** The lead approves with the existing **`pdx relay _<ref>`**. No new command. An open ask makes that
  relay "the answer"; the relay itself is the existing member relay op, with its approval / quota gate unchanged
  (`memberRelayGate`, RQ2 §4).
- **D8.** Re-asking follows the ordinary path's rule (`maybeBegin`): after an ask closes without a relay, the
  member asks again only when usage has grown by `REASK_POINTS` (10) and by `minGrowth` tokens. So a
  conversation asks at most at 70 / 80 / 90%.
- **D9.** After the window a lead's `pdx relay _<ref>` still works as an ordinary lead-initiated relay (U9 is
  unchanged); the ask simply no longer exists.
- **D10.** Local members only. A cross-host member's relay is unsupported today (`relay_unsupported`,
  `relay_member.go`); the daemon refuses its ask with the same code and its mod stays quiet.
- **D11.** Today's usage notice (`notice_usage.go`, `≥70% AND idle`) is **suppressed** for a member whose mod
  speaks protocol ≥ 3 (it asks for itself), and **kept** as the fallback for older mods.

## 1. Flow

```
member mod, turn.complete, usage ≥ 70%, gate passes (§4)
  └─ pdx relay ask --session S --used U --window W --request-id R      (from a timer, never in the hook)
       └─ daemon POST /api/relay/ask
            ├─ relay_asks row: state open, expires_at = now + 300 s
            └─ notice to the lead (noticeToLead); on a failed send the sweeper retries every tick until it
               is delivered or the ask closes (§3.1):
               [pdx team] member <address> [<ref>]「<title>」已用 <N>%，申請接力。
               <M> 分鐘內同意請執行：pdx relay _<ref>（不同意不用回覆，過期即作罷）
               (<M> = the minutes left when the notice is actually sent, rounded up)
lead (within 5 min): pdx relay _<ref> --wait 9m
  └─ handleRelayCreate → CreateMemberRelayOp: same transaction marks the open ask accepted (op_id)
       └─ existing member relay path: gate → control message → the member claims at its next turn boundary
          (claimLater on turn.complete) → write → clear → seed → done
no approval in 5 min:  sweeper closes the ask as expired; nobody is told; the member works on
member auto-compacted while open:  ask withdrawn (compacted); today's compaction notice is sent as before,
                                   and it is the only message (the withdrawal adds none)
member leaves the team while open: ask withdrawn (member_left)
```

## 2. Data

New table in team.db (`CREATE TABLE IF NOT EXISTS`; a new table needs no migration, per
`feedback_deployed_schema_needs_migration`):

```sql
CREATE TABLE IF NOT EXISTS relay_asks (
  id          TEXT PRIMARY KEY,          -- the mod's request id (UUID); a replay is the same ask
  team_id     TEXT NOT NULL,
  spawn_op    TEXT NOT NULL,             -- the member row's key
  session_id  TEXT NOT NULL,
  used_pct    INTEGER NOT NULL,
  window      INTEGER NOT NULL,
  state       TEXT NOT NULL CHECK (state IN ('open','accepted','expired','withdrawn')),
  reason      TEXT NOT NULL DEFAULT '',  -- withdrawn: compacted | member_left
  op_id       TEXT NOT NULL DEFAULT '',  -- accepted: the member relay op
  notified_at INTEGER NOT NULL DEFAULT 0,-- when the lead's notice was delivered; 0 = not yet (retried)
  created_at  INTEGER NOT NULL,
  expires_at  INTEGER NOT NULL,
  closed_at   INTEGER NOT NULL DEFAULT 0
);
CREATE UNIQUE INDEX IF NOT EXISTS relay_asks_one_open ON relay_asks (session_id) WHERE state = 'open';
```

Retention: closed rows older than 30 days are pruned by the existing retention loop (`runRetention`).

## 3. Daemon

### 3.1 `POST /api/relay/ask`

Body `{request_id, session_id, used_pct, window}`. Caller: the member's own mod, authenticated like the other
mod-facing `/api/relay/*` routes (`handleRelayBegin`).

The handler takes **`createMu`** (the same mutex `handleRelayCreate` holds around `CreateMemberRelayOp`,
`relay_member.go:57`), then:
1. Bad body → 400 `bad_request`.
2. `request_id` already stored → the stored answer (idempotent; no second notice).
3. One **write-first transaction** (`Store.CreateRelayAsk`, write lock taken before any read, as
   `CreateMemberRelayOp` does) that re-reads everything it decides on:
   - the session is not an active member of a live team → 409 `not_member`;
   - a remote member row → 409 `relay_unsupported` (D10);
   - a non-terminal `relay_ops` row for the session → 409 `relay_open`;
   - an open ask for the session → 200 with that ask and `"replay": true` (no second notice);
   - else insert the ask (`notified_at = 0`).
   With `createMu` held across both handlers and both transactions write-first, an ask and a lead's
   `pdx relay` for the same member are strictly ordered: whichever commits first, the other sees it
   (ask first → the relay accepts it, §3.2; relay first → the ask is refused `relay_open`).
4. After commit, deliver the notice with `noticeToLead`, asynchronously and tracked (`goTracked`). On success set
   `notified_at`. On a failed send, or when `goTracked` refuses (shutdown), `notified_at` stays 0.

Answer 200 `{id, state: "open", expires_at}`.

**Delivery retry (D5).** Each sweeper liveness tick re-sends the notice for every open, unexpired ask with
`notified_at = 0`, with the minutes left in the text. So a notice lost to a send failure or a restart reaches the
lead as soon as delivery works again, while the ask is still open. The window does not restart: it is the user's
five minutes from the request (D4).

### 3.2 Accepting

Inside `CreateMemberRelayOp`'s transaction (the lead's `pdx relay _<ref>`), **after** the membership check, the gate
and the op insert, and **before** commit: `UPDATE relay_asks SET state='accepted', op_id=?, closed_at=? WHERE
session_id=? AND state='open'`. One transaction: a failure anywhere (membership, gate, op insert, this update)
rolls back all of it — no op, no pool spend, the ask still open. A refused create leaves the ask untouched.

Unattended mode with the member pool at 0: the op is created `awaiting_approval` with its own RQ2 approval row,
and the ask is **still accepted** — its job (getting the lead to decide) is done. What follows is RQ2's: the row
may be approved (control message, relay at the next turn boundary), or denied / time out / be cancelled (the op is
cancelled). In those outcomes the ask stays `accepted`, `relay_ask_until` is absent (the ask is closed), and the
member asks again only under the ordinary re-ask rule (D8: +10 points).

### 3.3 Expiry and withdrawal

- Sweeper liveness tick, **after** the tick's existing team-end and gone-member settlement (`sweeper.go`), so a
  member that just left is already not active:
  - `UPDATE relay_asks SET state='expired', closed_at=? WHERE state='open' AND expires_at <= ?`;
  - an open ask whose member row is no longer `active` (released, killing / killed, gone, team ended) →
    `withdrawn` / `member_left`;
  - then the delivery retry of §3.1.
- `handleRelayCompacted` (trigger `auto`, an active local member): its open ask → `withdrawn` / `compacted`, in the
  same call that already sends today's compaction notice. The compaction notice stays exactly as today and is the
  only message.

Correctness does not depend on the sweeper being prompt: `CreateMemberRelayOp` accepts an ask only when it creates
an op, and it creates one only for an **active** member (its existing membership check, in the same transaction).
So release / kill / team end racing a lead's `pdx relay`:
- the member leaves first → the relay is refused as today; the ask is withdrawn at the next tick;
- the relay commits first → the ask is accepted; the existing guards then decide the release / kill (release and
  the kill claim already refuse while a relay op is open, `release_handler.go`, `team_handler.go`).
No message is sent on expiry or withdrawal.

### 3.4 The old notice

The old path must never send for a member that asks for itself, at either of its two decision points:
- `noticeUsage` skips a member when `modProtocolAtLeast(session, MinMemberAskModVersion)` (D11);
- `DisarmNotice`'s SQL also refuses while the session has an **open ask** (as it refuses for an open relay op);
- `usageNotice`'s re-check right before sending also checks the protocol and an open ask, so a notice already
  queued is dropped (and the member re-armed) when a v3 hello or an ask landed in between.
The mod hello is persisted (`relay_handler.go`, `LoadModHello` at module start, `module.go`), so after a daemon
restart the daemon still knows which members speak protocol 3. Older mods keep today's notice unchanged.

### 3.5 Constants and wire

`internal/team/wire_relay_member.go`: `RelayAskHoldS = 300`, `MinMemberAskModVersion = 3`,
`RelayAskNoticeFmt` (the text in §1), request / answer types, error codes reuse `not_member`,
`relay_unsupported`, `relay_open`.

### 3.6 Lead view

`GET /api/team` member view gains `relay_ask_until` (unix ms) while an ask is open. `pdx team` shows it in the
TASK column as `接力申請 (剩 N 分)`. The App's team panel can read the same field later (interface line).

## 4. Mod (`cmd/pdx/plugin/purdex/hooks/register.js`, protocol `VERSION = '3'`)

- `maybeBegin`: a member no longer returns at the role check; it goes to `maybeAsk($)`. Same gate as the
  ordinary path: `helloOK`, `percent ≥ threshold`, tokens grown by `minGrowth` since `floor`,
  `percent ≥ lastAskPct + REASK_POINTS`, relay state `idle` (no op being claimed / written). Runs from a timer,
  never inside the hook.
- `maybeAsk` runs `pdx relay ask …` and **does not** change the relay state, hold `prompt.submit`, show a
  waiting status or poll (D3). On 200 (open or replay), 409 `relay_open` or 409 `relay_unsupported` it sets
  `lastAskPct = percent` (no re-ask until +10). On 409 `not_member` it runs `recheckMember` (a released member
  becomes an ordinary session and uses the ordinary path). Other failures: nothing, next turn boundary tries
  again under the same gate.
- The lead's relay arrives as today's control message; the existing claim path takes it at the next turn
  boundary. `onSeedTurnDone` resets `floor` / `lastAskPct` as today.
- `/relay now` from a member keeps today's answer (`RELAY_MEMBER`: the lead relays its members).
- `hello` reports `VERSION = '3'`.

## 5. CLI

`pdx relay ask --session <sid> --used <pct> --window <tokens> [--request-id <uuid>]` (mod-internal, listed with
`pdx relay begin`): prints the answer JSON; exit 0 ok, 13 refused (the code is the last word on stderr), 20 daemon
unreachable.

## 6. Skill text (`skills/pdx-team/SKILL.md`)

- Lead: "When `[pdx team] member … 申請接力` arrives you have 5 minutes. To approve, run
  `pdx relay _<ref> --wait 9m` in the foreground (Bash `timeout: 600000`). To decline, do nothing. The member keeps
  working meanwhile." The existing "When a member's context runs high" paragraph keeps the old idle notice for
  older mods.
- Member: "Never relay yourself" stays; add "your mod asks your lead automatically at 70%; you do nothing".

## 7. Spec drift

`2026-10-06-lead-team-relay-spec.md` §8.5 (detection and lead notice) and U9 get a one-line note pointing here:
the member's mod now asks; the daemon still decides nothing and the relay is still the lead's command.

## 8. Phases and tasks (each phase one PR, ≤ 800 lines / 20 files)

**P1 daemon + CLI** (deploy: daemon only). Tasks in order, each red test first, each its own commit:
1. **Store**: `relay_asks` table and index; `CreateRelayAsk` (write-first tx with the §3.1 step-3 checks);
   `ExpireRelayAsks`, `WithdrawRelayAsks` (member not active / compacted); `MarkAskNotified`; retention of closed
   rows > 30 days. Tests: each rule; the open-op and open-ask refusals; retention never deletes an open ask.
2. **Accept in the op transaction**: the §3.2 update inside `CreateMemberRelayOp`, after the op insert. Tests:
   accepted with op id; refused create leaves the ask open; **fault injection** — make the ask update fail → no op
   row, no pool spend, ask still open; pool 0 → op `awaiting_approval` and ask accepted.
3. **Route + delivery**: `POST /api/relay/ask` under `createMu`; notice text; `notified_at`; the sweeper's retry.
   Tests: one notice per ask; replay → no second notice; sender failure → retried next tick with the minutes left;
   `goTracked` refused → retried; an expired ask is never notified; both race orders against `handleRelayCreate`
   (ask first → accepted; relay first → `relay_open`).
4. **Old notice**: the three §3.4 checks. Tests: v3 member never gets the old notice; v2 member still does;
   persisted v3 hello → restart (reload `modSeen`) → still suppressed; queued old notice dropped when an ask lands
   before it sends; `DisarmNotice` refused while an ask is open.
5. **Expiry, withdrawal, compaction**: the §3.3 sweeper steps in order after settlement; compaction withdraws and
   sends only the compaction notice. Tests: expiry at 300 s (fake clock); release / kill (`killing`) / team end
   each → `withdrawn/member_left`; the two race orders of release vs `pdx relay`; compaction with an open ask →
   ask withdrawn and exactly one message (the compaction notice).
6. **Views + CLI**: `relay_ask_until` in `GET /api/team`; `pdx team` TASK column; `pdx relay ask`.
   Tests: field present only while open; CLI exit codes.

**P2 mod + skill** (deploy: daemon for the embedded mod + `pdx setup --agent cc`): `maybeAsk`, `VERSION = '3'`,
SKILL.md, the spec drift note (§7).

P1 ships first: the daemon understands the route before any mod calls it, and older mods are unaffected.

## 9. Tests

P1: listed per task in §8. Mutations, each must turn a test red: the accept outside the op transaction; no
`createMu` in the ask handler; no delivery retry; no protocol / open-ask check in `usageNotice`; the ask sweep
before settlement.

P2 (mod tests, `register` harness):
- member at 70% on `turn.complete` → one `pdx relay ask`; `prompt.submit` is not held and the relay state stays idle;
- no second ask until +10 points; `relay_open` / `relay_unsupported` also wait for +10; `not_member` →
  `recheckMember`;
- an ordinary session's path is unchanged (existing tests stay green).

## 10. Risks

- The lead does not read the notice in time → the ask expires; accepted by D4.
- Notice volume: at most three asks per conversation (70 / 80 / 90%); a retried notice is the same ask.
- A notice delivered late (after retries) leaves the lead less than five minutes; the text says how many.

## 11. Follow-up (#2439, 2026-10-10): a long turn, and a member that never answers

The ask makes "the lead approves, the relay happens at the member's next turn boundary" the normal path, so the relay
timeouts (`relay_timeouts.go`) have to survive a long turn and explain a blocked one. Designed by 1f; the rules, for a
member op in `requested`:

| Situation | Rule |
|---|---|
| Never seen (no `seen_at`) 60 s after it entered `requested` | fail `member_unseen`; `member_blocked` if the agent status is `waiting` |
| Seen, no agent status for the member | as before: `member_unresponsive` 15 min after `seen_at` |
| Seen, the member is in a turn (`running`, or `waiting` on a prompt) | keep waiting up to `RelayBusyCapS` = 60 min after `seen_at`; **once** at `RelayStallTimeoutS` = 15 min the lead gets `RelayBusyNoticeFmt` (`noticeToLead`); at the cap fail `member_busy_timeout` (`member_blocked` if `waiting`) |
| Seen, the member is idle (its turn ended) and the op is still unclaimed `RelayIdleGraceS` = 2 min after the daemon first saw it idle | fail `member_unresponsive` (the mod did not claim) |

Notes. The agent status is read as `noticeUsage` reads it (`AgentStatus(tmux)`): `running`, `waiting`, `idle` (and
`error`, `clear`, treated like idle). `waiting` is a prompt that needs a person (a permission, a question); a dialog the
agent never reports (such as "Mods: Enable hot reloading?") shows as an unseen op with whatever status the session had, so
`member_unseen`'s message names that case. The idle count and the once-only mark of the 15 minute notice are in memory
(a restart gives the mod another two minutes and can repeat the notice once). The failures are reported to the lead by the
usual failure notice; the CLI keeps exit 14 for all of them and prints the reason on stderr. `pdx relay --wait` that runs out
while the op is `seen` / `requested` exits 0 with the op printed: it will happen at the member's turn end.
