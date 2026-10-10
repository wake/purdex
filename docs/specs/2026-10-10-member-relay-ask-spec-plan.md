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
            └─ notice to the lead (noticeToLead):
               [pdx team] member <address> [<ref>]「<title>」已用 <N>%，申請接力。
               5 分鐘內同意請執行：pdx relay _<ref>（不同意不用回覆，過期即作罷）
lead (within 5 min): pdx relay _<ref> --wait 9m
  └─ handleRelayCreate → CreateMemberRelayOp: same transaction marks the open ask accepted (op_id)
       └─ existing member relay path: gate → control message → the member claims at its next turn boundary
          (claimLater on turn.complete) → write → clear → seed → done
no approval in 5 min:  sweeper closes the ask as expired; nobody is told; the member works on
member auto-compacted while open:  ask withdrawn (compacted)
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

In order:
1. Bad body → 400 `bad_request`.
2. `request_id` already stored → the stored answer (idempotent; no second notice).
3. The session is not an active member of a live team → 409 `not_member`.
4. A remote member row → 409 `relay_unsupported` (D10).
5. A non-terminal `relay_ops` row for the session → 409 `relay_open`.
6. An open ask for the session → 200 with that ask and `"replay": true` (no second notice).
7. Insert the ask (the partial unique index decides a race: the loser re-reads and answers as 6).
8. Send the notice to the lead with `noticeToLead`, asynchronously and tracked (`goTracked`). A failed send leaves
   the ask open (the lead can still run `pdx relay`) and is logged.

Answer 200 `{id, state: "open", expires_at}`.

### 3.2 Accepting

`CreateMemberRelayOp` (the lead's `pdx relay _<ref>`, `handleRelayCreate`) runs, in its own transaction,
`UPDATE relay_asks SET state='accepted', op_id=?, closed_at=? WHERE session_id=? AND state='open'`. If the op is
not created (any refusal), the ask is untouched. An unattended-mode `awaiting_approval` op (pool at 0) still
accepts the ask: the ask's job (getting the lead to decide) is done; the op's own approval follows RQ2.

### 3.3 Expiry and withdrawal

- Sweeper liveness tick: `UPDATE relay_asks SET state='expired', closed_at=? WHERE state='open' AND expires_at <= ?`.
- `handleRelayCompacted` (trigger `auto`, an active local member): its open ask → `withdrawn` / `compacted`.
- The same sweeper tick closes an open ask whose member row is no longer active → `withdrawn` / `member_left`.

No message is sent on expiry or withdrawal (the notice already said the window).

### 3.4 The old notice

`noticeUsage` skips a member when `modProtocolAtLeast(session, MinMemberAskModVersion)` (D11). Older mods keep
today's `≥70% AND idle` notice and its arming rules unchanged.

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

## 8. Phases (each one PR, ≤ 800 lines / 20 files)

- **P1 daemon + CLI**: `relay_asks` store and retention, `POST /api/relay/ask`, accept in `CreateMemberRelayOp`,
  expiry / withdrawal (sweeper, compacted), old-notice suppression for protocol ≥ 3, constants and wire,
  `relay_ask_until` in the team view and `pdx team`, `pdx relay ask`. Deploy: daemon only.
- **P2 mod + skill**: `maybeAsk`, `VERSION = '3'`, SKILL.md, the spec drift note. Deploy: daemon (for the
  embedded mod) + `pdx setup --agent cc`.

P1 ships first: the daemon understands the route before any mod calls it, and older mods are unaffected.

## 9. Tests

P1 (Go, fake clock, fake sender):
- an ask creates the row and sends the lead exactly the notice text; a second ask (same or new request id) →
  same open ask, no second notice;
- not a member → 409 `not_member`; remote member → `relay_unsupported`; open op → `relay_open`;
- the lead's `pdx relay` → the ask `accepted` with the op id in the op's transaction; a refused op creation →
  the ask stays open;
- expiry at 300 s; compacted → `withdrawn/compacted`; member released / killed / team ended →
  `withdrawn/member_left`;
- the old notice: suppressed for a member whose `modSeen` version is 3, sent for version 2;
- retention prunes closed rows older than 30 days, never an open one;
- `relay_ask_until` present only while open.
Mutations: no accept in the op transaction / no expiry / no suppression → red.

P2 (mod tests, `register` harness):
- member at 70% on `turn.complete` → one `pdx relay ask`; `prompt.submit` is not held and the state stays idle;
- no second ask until +10 points; `relay_open` / `relay_unsupported` also wait for +10; `not_member` →
  `recheckMember`;
- an ordinary session's path is unchanged (existing tests stay green).

## 10. Risks

- The lead does not read the notice in time → the ask expires; accepted by D4.
- Notice volume: at most three per conversation (70 / 80 / 90%).
- `modSeen` is in memory: right after a daemon restart, until a member's mod says hello again, the daemon does
  not know it speaks protocol 3 and may send the old idle notice once as well. Accepted (one extra line).
