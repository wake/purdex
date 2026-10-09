# Cross-host team members (simplified) — spec + plan (v5, final)

Date: 2026-10-09. Coordinator: purdex-1f (`mlab/_jq7pua`). Line: A (daemon + CLI); App: interface line (88).
Replaces plan v3 Phase P4b / P4c (`docs/specs/2026-10-06-lead-team-relay-plan-v3.md:713-1172`) for cross-host members. Cross-host member **relay** (P6-7a / P6-7b) stays out of scope (§12).
Review: codex `task-mv0sfcmr-2ebpfr` on v1 (17 findings, 6 critical) `task-mv0tskxu-o566yt` on v2 (2 critical, 9 important) and `task-mv0uhc5j-39z57l` on v3 (2 critical, 3 important), `task-mv0upzkd-co2465` on v4 (no critical, 2 important) — every one is answered in §13.

## 1. User decisions (2026-10-09 — do not reopen)

| # | Decision |
|---|---|
| X-U1 | Only two scenarios: **(a)** a lead **spawns** a member on another host; **(b)** a lead **adopts / releases** a running session on another host (kill and team end follow). |
| X-U2 | **The lead's daemon is the team's only source of truth.** Every change starts on the lead's side: recorded there first, then sent as a command to the member host's daemon, which applies it and acks. |
| X-U3 | The member host keeps **one member record per local session**, changed only by the lead's commands. The member's mod reads its local daemon; nothing broadcasts or polls. **A member never needs to ask the lead about an unknown situation.** |
| X-U4 | The reverse direction reports **only facts that happened on the member host** (the member session ended; it moved to a new session id). |
| X-U5 | Both directions: **outbox + op id idempotency + resend**; a release sent while the host is offline is delivered when it is back. **No multi-daemon synchronisation.** |
| X-U6 | Done now, in parallel with P6 (the A line goes to two seats). |
| X-U7 | (asked 2026-10-09) A spawn on M may use **only the directories M's daemon configured** for that lead host; adopting a running session needs none. |
| X-U8 | (asked 2026-10-09) A `spawn` or `adopt` not delivered within **10 minutes is void**; the lead re-runs it later if still wanted. `release` / `kill` / `end` keep queuing until delivered. |

## 2. Facts on main this stands on (alpha.643)

- Pairing: per-pair `pdxp_` bearer tokens (`internal/config/config.go:46-58`); the receiver maps the inbound token to a host principal (`internal/middleware/peer_auth.go:73-106`); host principals reach only `GET /api/peers` and `POST /api/peers/deliver` (`internal/module/peers/policy.go:37-50`). Outbound clients are endpoint-specific, no-redirect, size-capped (`client.go:15-68`); the message path re-fetches the receiver's inventory and refuses on `env.HostID != targetHostID` (`send.go:383-400`). `/deliver` has a private per-host rate limit before decode (`deliver.go:169-183`, `limits.go:56-74`). Host aliases can be deleted and re-created for another host (`hosts.go:533-587`).
- The in-process `Sender` forwards `To: "<other alias>/<ref>"` to the paired host and the receiver presents the sender with a reply-capable socket (`send.go:297-314`, `:591-605`; `deliver.go:285-338`).
- Every "one role per session" gate reads only local tables: lead create (`handler.go:248-267`), lead approve / unattended (`team_store.go:291`, `:389`), local adopt (`adopt_store.go:81-94`), relay target role on `cleared` (`relay_store_report.go:86-119`), `relayRole` (`relay_handler.go:44-53`, lead checked first).
- Local member transitions are guarded CAS: release `active → released` (`release_store.go:21-27`), gone `active → gone` (`team_store_members.go:154-168`), kill `active|gone → killed` (`:139-151`), keyed by the row's `spawn_op` (adopted rows: the approval id).
- Seats: one shared rule `seatsExpr` (active members + running spawns without a member row) used by spawn, adopt, `PUT /api/team/max-members` and the roster (`max_members_store.go`, alpha.643).
- Notices: one `notice_pending` column per row, given up after 10 min, need the lead's live inbox (`notify.go:79-125`); the P6-1′ handover notice is best effort with no row (`notice.go:11-15`).
- Spawn runner: roots from the local team's grant (`spawn_tmux.go:28-52,153-165`); finish writes `team_members` and the first task, then moves the op (`spawn_register.go:126-176`).
- Reports and tasks are validated and stored in the caller's local transaction and answer synchronously (`report_handler.go:22-78`, `task_store.go:575-607`).

## 3. Model

Lead host **L**, member host **M**, paired both ways. Each membership on L has a stable **member key** `mk`: the adopt command id or the spawn op id. Every command and fact carries `mk` (except team-level ones), a UUID v4 `id`, and `to_host_id`.

```
L (source of truth)                                        M (member host)
team_members row (host_id=M, mk) ──commands outbox──▶ POST /api/peers/team/commands ─▶ remote_members row (mk)
                                 ◀──facts outbox───── POST /api/peers/team/facts    ◀─ local events
```

### 3.1 The rules every exchange follows

1. **Target by host id.** An outbox row stores the destination `host_id`. Each attempt resolves the **live** peer entry by host id (not alias); no entry → the host is unpaired (§3.4). The request carries `to_host_id`; the receiver answers 409 `wrong_host` if it is not its own host id (permanent). The response carries the receiver's `host_id`; a mismatch is treated as `wrong_host`.
2. **Write with the cause.** L inserts a command in the **same transaction** as the change that causes it (approve, release, kill, `EndTeam`, the `cleared` that moves the lead, spawn accept). M inserts a fact in the same transaction as the local change that causes it.
3. **Idempotent by id and content.** The receiver stores `{id, kind, body_hash, outcome}` in the applying transaction, **refusals included**. The same id with the same hash answers the stored outcome; with another hash 409 `id_conflict`.
4. **Apply the answer in one transaction.** The sender marks the entry done **and** applies the outcome (§4, §5) in one transaction; a crash before it re-sends, and the stored outcome comes back.
5. **Monotonic.** Every row change on either side is a CAS from the states the tables in §4.2 / §5.2 allow; an outcome or fact whose CAS finds another state is ignored (logged once), never forced.
6. **Pump.** One pump per side, FIFO **per peer host**; first attempt right after commit; backoff 30 s doubling to a 10 min cap, forever, except the expiry of X-U8 (§3.3). Classification: 2xx → done; transport error / 5xx / 429 → transient; **401 (the peer's `PeerAuth` no longer knows our token — plain text, before any route) → transient for 10 min from the first one, then `unpaired_by_peer` (§3.2)**; 404 or a non-JSON 403 → `unsupported` (route missing: an older daemon); JSON 4xx → permanent refusal, applied per kind as the outcome tables of §4.2 / §5.2 say.
7. **Per-kind capability.** M's inventory (`GET /api/peers` envelope) gains `team: {kinds: ["adopt", …], allow_team: bool}` — `allow_team` **as it applies to the asking host principal**. L checks it **before** creating an adopt approval or accepting a spawn (refuse at once: 409 `remote_unsupported` / `host_not_allowed`), and before enqueuing any other kind (an unsupported kind is refused to the lead, never queued). A JSON 400 `unsupported_kind` later (a downgrade) is a permanent refusal of that entry — M cannot have applied it — so FIFO is never blocked by it; a 404 (route missing) blocks the host's queue, which is correct: nothing else could apply either.
8. **Transport.** `HostCaller` keeps plan v3 P4b-3's protections: no redirects, 16 MiB response cap, the token read from the live entry at each call, 10 s timeout. Both new routes have a body cap (64 KiB) and a per-host rate limit before decode (the `/deliver` limiter made reusable).

### 3.2 Unpairing

Removing the peer entry for the other host is final for every team relation with it, **on that side, locally**:
- On L: every **live** remote row on that host (`joining`, `active`, `releasing`, `killing`) → `gone{unpaired}`; terminal rows are not rewritten; its outbox entries → dropped; its pending forwarded spawns → `failed{unpaired}`.
- On M: every **live** `remote_members` row whose lead host is that host → `ended{unpaired}` (self relay back on); its fact entries → dropped. **No notice** is sent for it: the lead host is no longer bound, so there is no sender the member could reply to; the session sees `relay status member:false` at the mod's next role check, and any `pdx msg` / `pdx report` to the lead fails with the unpaired host.
- **The other side learns it only when it next calls** (rule 6: a 401 that lasts 10 min → `unpaired_by_peer`, the same local clean-up as above with reason `unpaired_by_peer`). A side that never calls — M with a live member and no local event, L with nothing to send — does not learn it: this is the accepted residual of X-U3 / X-U5 (no polling, no sync). It is visible and the user can clear it: M's Hosts page and `pdx peers hosts` list, per lead host, the local sessions that are its remote members, and unpairing or `pdx peers host allow-team <alias> off --end-members` on M ends them locally.

### 3.3 Expiry and void (X-U8)

A `spawn` or `adopt` command not done within **10 min** of its creation: L stops sending it, marks it `void`, frees the seat (row / spawn op `failed{remote_unreachable}`), the lead's wait answers exit 14, and L enqueues **`void {command_id}`** (never expires). M, on `void`: if `command_id` was applied → undo it (adopted row → `released`, spawned → kill with the spawn identity check), outcome `{undone}`; if not → insert `command_id` into `team_command_voids` (its own table, not the command log), so a later copy answers 409 `command_void` — the void table is checked **before** the command log. No cross-host clocks are compared.

## 4. L (lead host)

### 4.1 Tables

- `team_members` gains `host_id` use for remote rows (already a column) and `mk` (`ensureColumn`, = `spawn_op` for local rows); remote rows use the states of §4.2.
- `team_commands` (outbox): `id, kind, team_id, mk, host_id, body_json, body_hash, state (pending|done|void|dropped), outcome_json, attempts, next_at, created_at, updated_at`.
- `team_fact_log`: `id, kind, body_hash, outcome, at` (received facts).

### 4.2 Member row states on L (remote rows)

| From | Event | To | Command / seat |
|---|---|---|---|
| — | adopt approved (click or unattended) | `joining` | `adopt` enqueued, same tx; seat taken |
| `joining` | adopt outcome `applied` | `active` | notice `adopted` owed |
| `joining` | adopt refusal / void | `failed{code}` | seat freed |
| `joining`, `active` | lead `pdx release` | `releasing` | `release` enqueued, same tx; seat still taken |
| `releasing` | release outcome | `released` | notice `released` owed; seat freed |
| `active` | lead `pdx kill` | `killing` | `kill` enqueued; seat taken. **Only from `active`**: while `joining` or `releasing` a kill is refused 409 `command_pending` (the lead waits for the adopt / release to land, or releases a `joining` member), so a kill refusal can only ever return to `active` and never re-opens a state whose outcome was already consumed |
| `killing` | kill outcome `killed` / `gone` | `killed` / `gone` | seat freed |
| `killing` | kill refused (`host_not_allowed`, or any code but `not_your_member`) | `active` | the lead's CLI gets the refusal |
| `joining`, `active`, `releasing`, `killing` | fact `ended` (same `mk`) | `gone{reason}` | seat freed — an `ended` may arrive **before** the adopt's own response (the two directions are not ordered); the late `applied` then finds `gone` and is ignored |
| any live | team ended | `ended` | `end` enqueued per host (same tx as `EndTeam`) |
| any live | host unpaired / `unpaired_by_peer` | `gone{unpaired…}` | terminal rows unchanged |

**Permanent outcomes per kind** (rule 6): `adopt` refusal → `failed{code}`; `release` refused with any code (`not_your_member` — M has no such live row —, `wrong_host`, `unsupported_kind`, …) → `released{code}` (L's intent stands; the residual of §3.2 applies to M); `kill` refused `host_not_allowed` → back to `active`, the lead's CLI gets the code; `kill` refused `not_your_member` → `gone{code}`; any other `kill` refusal → back to `active` + the code; `end`, `lead_moved`, `void` refused → logged once, entry done.

The seat rule `seatsExpr` gains: remote rows in `joining`, `active`, `releasing`, `killing`, and forwarded spawns not yet registered. A release while `joining` follows the adopt FIFO; the adopt outcome then finds `releasing` and is ignored (rule 5), the release outcome lands later.

### 4.3 The remote adopt

- `pdx adopt <alias>/_<ref>` (or the App): L resolves the target with M's `GET /api/peers` (host principal, 3 s) and checks M's `team.kinds` contains `adopt` and `allow_team` is true for L (else 409 `remote_unsupported` / `host_not_allowed` before any approval). `AdoptPayload` gains `target_host_id`, `target_host_alias`.
- Approval as today (click or unattended, U24). **For a remote target the approval means "the user consents", not "adopted":** the approve transaction writes the `joining` row and the command; `afterApproved` kicks no notice for it.
- The CLI waits on the **membership**, not the approval: `GET /api/team/adoptions/{approval_id}` → `{state: joining|active|failed|void, code}`; exit 0 on `active`, 13 on `failed{code}`, 14 on `void` (remote unreachable for 10 min), 11 on its own bound while `joining`. A replay of the same request answers the approval and this state.

### 4.4 Notices for remote members — produced on M

The member-facing notices (adopted, released, handover after `lead_moved`, team ended) are **written by M** in the same transaction that applies the command (or the local unpairing), into M's `remote_notices(mk, kind, text, state, attempts, next_at)`, and delivered **locally** by M with the lead as the sender, through a **narrow internal seam** `deliverTeamNotice(row, kind)` — not `handleDeliver`: it skips only the inbound HTTP auth and the `Peers.Deliver` switch (a notice is part of the membership the user consented to with `AllowTeam`), and keeps everything else: the row and its lead host must still be bound (live peer entry carrying `lead_host_id`); the **text is M's own fixed template per kind** (`AdoptNoticeFmt` / `ReleaseNoticeFmt` / handover / team ended / unpaired), filled only with the row's lead address and team name (bounded 64 bytes, control characters stripped) — never free text from L; the target session is re-validated (live, same pid / proc_start as the row); the reply-capable helper is built from the row's stored lead tuple (§5.1) under the existing helper cap; one audit line; the per-host limiter. So a notice needs neither the lead's live inbox nor M's `Peers.Deliver` switch. Retried locally with the outbox backoff while the session is live and the row is still in the state the notice is about; superseded otherwise. (A member's *reply* to the lead is an ordinary peer message M → L and needs L's deliver switch, which is the lead host's own.) The P6-1′ handover notice for a remote member is this path; for local members it is unchanged.

### 4.5 Facts received on L

Route binding as §6.1. `registered` and `spawn_failed` bind to L's **pending forwarded spawn op** `{mk = spawn op id, host_id == principal.HostID, team_id}`; every other fact binds to the member row with `host_id == principal.HostID` and its `mk`. `registered` → spawn op `done`, member row `active` (created now), the first task created **on L** (§7); `spawn_failed` → spawn op `failed`, seat freed; `ended` → §4.2; `moved` → reserved (§12).

### 4.6 One role per session (L's own sessions)

Unchanged for L's local sessions. A remote member's session is on M; its role gates are M's (§5.3).

## 5. M (member host)

### 5.1 Tables

- `remote_members`: `mk PRIMARY KEY, member_session_id, ref, team_id, team_name, lead_host_id, lead_session_id, lead_ref, lead_address, lead_title, lead_pid, lead_proc_start, origin (adopted|spawned), state, pid, proc_start, pane_id, tmux_session, cwd, title, model, effort, created_at, updated_at`; unique active per `member_session_id`. The lead fields and `team_name` come from each command (`adopt` / `spawn` set them, `lead_moved` replaces them), so the notice pump can rebuild the reply-capable sender after a restart.
- `team_command_log`: `id, kind, body_hash, outcome_json, at` (applied **and refused** commands).
- `team_command_voids`: `command_id PRIMARY KEY, void_id, at` (§3.3), checked before the log.
- `remote_notices` (§4.4).
- `team_facts` (outbox): as `team_commands`, `host_id` = the lead host.

### 5.2 Remote row states on M

| From | Command / event | To | Fact |
|---|---|---|---|
| — | `adopt` (consent + live target + no role) | `active` | — |
| — | spawn registered | `active` | `registered` (same tx) |
| `active` | `release` | `released` | — |
| `active` | `kill` | `killed` / `gone` | — |
| `active` | the session is no longer live (M's sweeper) | `gone` | `ended` (same tx) |
| `active` | `end` | `ended` | — |
| `active` | `lead_moved` | `active` (lead fields updated) | — (a handover notice is owed, §4.4) |
| `active` | `void` of the creating command | `released` (adopted) / `killed` (spawned) | — |
| `active` | lead host unpaired / unknown to L | `ended{…}` | — |

### 5.3 One role per session (M's sessions)

A single gate `sessionRoleIn(tx, sid)` returns `lead | member(local) | member(remote) | none` from `teams`, `team_members` **and** `remote_members`; every gate in §2's list calls it inside its own transaction: lead create and approve (a remote member cannot lead — `member_cannot_lead`), local adopt (`adopt_already_member`), the remote `adopt` command, the relay target on `cleared`, **the self-relay approve transaction (`closeSelfRelayApprovedIn`, `team_store.go:249-277`: a session adopted remotely after opening a self-relay request has that request cancelled, as for a local adoption)**, and `relayRole` (a remote member is `member`: self relay off, `relay status member:true`).

### 5.4 Consent and roots

`PeerHost.AllowTeam bool` (default false) and `PeerHost.TeamRoots []string` on M's entry for L; CLI `pdx peers host allow-team <alias> on|off [--root <dir>]…` (canonicalised, must exist); `GET /api/peers/hosts` rows carry both; `PUT` sets them (App toggle — 88). `AllowTeam` off → `adopt`, `spawn`, `kill` refused 403 JSON `host_not_allowed`; `spawn` cwd outside `TeamRoots` (none = no spawn, X-U7) → 409 `cwd_outside_grant`. `release`, `end`, `lead_moved`, `void` need only the binding and rows of that team from that lead host.

### 5.5 Spawn on M (remote mode of the runner)

The runner takes a `rootsFor(op)` and a `finish(op)` strategy: for a forwarded op, roots = `TeamRoots` of the lead host's entry, and finish writes `remote_members` + the `registered` fact in one transaction (no local task); everything else (tmux create, launch with model / effort, 20 s registration, pane identity) is unchanged.

## 6. The two routes

### 6.1 Binding (all three routes — commands, facts, proxy — in order, as `handleDeliver`, `deliver.go:142-165`)

Admin → 403 `admin_not_allowed`; not a host principal or empty `HostID` → 403 `host_unverified`; the live entry for the principal's alias no longer carries that `HostID` → 403 `host_unverified`; `to_host_id` not ours → 409 `wrong_host`; rate limit and body cap before decode.

### 6.2 Commands (L → M): `POST /api/peers/team/commands`

`TeamCommand{id, kind, to_host_id, team_id, team_name, mk, lead:{session_id, ref, title, address, pid, proc_start}, …}` (the lead's **full origin tuple**, which M needs to present the lead as a reply-capable sender, `internal/peers/wire.go:562`, `reply.go:142`) → 200 `{id, host_id, outcome}`; kinds `adopt {target_session_id, target_ref}`, `release`, `kill`, `end`, `lead_moved {lead_session_id, lead_ref}`, `spawn {cwd, title, model, effort}`, `void {command_id}`. Outcomes: `adopt` → `{applied, member_session_id, ref, pid, proc_start, title, cwd, tmux}` or a refusal code (`adopt_target_not_found`, `adopt_target_is_lead`, `adopt_already_member`, `host_not_allowed`); `kill` → `{killed|gone}`; `spawn` → `{accepted}` (the result comes as a fact); others `{ok}`.

### 6.3 Facts (M → L): `POST /api/peers/team/facts`

`TeamFact{id, kind, to_host_id, team_id, mk, …}`; kinds `registered {member_session_id, ref, pid, proc_start, pane, title}`, `spawn_failed {reason}`, `ended {reason}`, `moved` (reserved).

## 7. Member reports and tasks (X6)

A remote member's `pdx report` and `pdx task …` calls are **proxied synchronously**: M resolves the caller locally (its own registry), finds its active `remote_members` row and forwards the request to L through `HostCaller` as `POST /api/peers/team/proxy {mk, method, path, body}` (host principal; only the report and task routes, allow-listed). L does **not** hand the forwarded request to the existing handlers (they re-resolve the caller from the body's `origin_inbox`, `task_handler.go:56`, `report_handler.go:22`, `team_handler.go:101`, and a paired host could put the lead's inbox there). Instead a proxy adapter: the route's §6.1 binding first; **any `origin_inbox` in the forwarded body or query is refused (400)**; the actor is built only from `{host_id == principal.HostID, mk}` and re-checked `active` in a live team **inside the business transaction** (the `liveMemberIn` pattern, `task_store.go:598-607`); and only an **allow-list of owner-scoped operations** runs: list my tasks (`pdx task mine`), change the status of a task I own, post a report as myself — each calling the store's owner-scoped function with that actor, never a lead operation. The answer — success or refusal — goes back to the CLI unchanged. L unreachable → the CLI gets 503 `lead_unreachable` (retry). No local task copy, no task facts. The report's up-message address is the lead's address **as M knows it**. A spawn's first task is created on L when `registered` arrives; the CLI gets its id from the spawn op.

## 8. Display

`RosterSession` gains `host_id`, `host_alias` (`""` = the lead's own host); a remote member's `address` is `<host_alias>/<ref>`; remote rows show their §4.2 state. `pdx team` gets a HOST column; a remote member's CTX / MODEL come from M's `GET /api/peers` (3 s; blank and `(主機無回應)` when unreachable). App (88): Hosts page — `allow_team` and `team_roots` (「允許 <alias> 在這台開 member」＋資料夾); team panel host badge and the `joining` / `releasing` / `killing` states; adopt card "on <host>"; spawn host picker (roots from M's `GET /api/peers/team/roots`).

## 9. Plan

Each PR ≤ 800 lines / ≤ 20 files with tests; estimates are production × 2. Deploy order: a PR that adds a route or a fact/command kind is deployed on **both** hosts before L sends it (the capability list of §3.1 rule 7 makes an early send refuse cleanly anyway).

| PR | Scope | ≈ lines / files |
|---|---|---|
| X1a | `HostCaller` (§3.1 rules 1, 6, 8: target by host id, receiver identity, classification, no redirect, cap, live token) + the reusable per-host limiter | 500 / 9 |
| X1b | `AllowTeam` + `TeamRoots` (config, CLI, hosts row / PUT) + inventory `team: {kinds, allow_team}` per principal + `GET /api/peers/team/roots` | 550 / 12 |
| X2a | M: `remote_members` + `sessionRoleIn` across all gates (§5.3) + `relayRole` + the lead-host-paired check — no route yet | 600 / 16 |
| X2b | M: `team_command_log`, `team_command_voids`, `remote_notices` (schema + the owed row written in each applying transaction), the commands route (§6.1 binding, consent, hash idempotency, refusals stored) for `adopt` / `release` / `end` / `lead_moved` / `void`; **announces no kind yet** | 800 / 14 |
| X2c | M: the facts outbox (pump shared with X3a's code) + `ended` from the sweeper + unpairing on M (§3.2), each writing its owed notice row in the same transaction | 500 / 9 |
| X3a | L: the commands outbox + pump (incl. the 401 rule) + expiry / `void` + unpairing on L | 650 / 10 |
| X3b-1 | L: the remote row state machine (§4.2, all outcome rows) + seats + release / kill / end / lead_moved enqueue in their transactions + `matchMember` / target parsing for remote aliases | 750 / 16 |
| X3b-2 | L: the facts route + `team_fact_log` + applying `ended` (and, later, the spawn facts) with the §4.2 transitions; the facts capability is announced only from this PR | 450 / 8 |
| X3c | L: remote adopt (§4.3: resolve + capability before the approval, `joining`, adoptions route, CLI wait and exit codes) | 600 / 10 |
| X3d | M: the local notice pump through `deliverTeamNotice` (§4.4), incl. the handover notice; **from this PR M announces the kinds `adopt` / `release` / `end` / `lead_moved` / `void`** (until then L can send none of them, rule 7) | 450 / 9 |
| X5 | display (§8) | 450 / 8 |
| X4a | M: the runner's remote mode (§5.5) + `kill` command + `registered` / `spawn_failed` facts | 650 / 11 |
| X4b | L: `pdx spawn --host`, the forwarded spawn op, the first task on `registered`, the CLI wait | 550 / 9 |
| X6 | the proxy route and the CLI paths (§7) | 550 / 9 |

≈ 8 150 lines in 14 PRs (v1 said 3 200 / 7 — it left out the identity, transaction, monotonic-state, role and notice work codex found). **The user's first need — the iOS session on air26 adopted into a team — is X1a → X1b → X2a → X2b → X2c → X3a → X3b-1 → X3b-2 → X3c → X3d → X5 (≈ 6 350 lines, 11 PRs; nothing is sendable before X3d announces the kinds)**; X4a / X4b (spawn) and X6 (reports / tasks across hosts; until then a remote member reports with `pdx msg send`) follow. Kill of a remote adopted member arrives with X4a (it needs M's kill path); before that a lead releases instead.

Second A-line member does these in order; the first does P6 (RQ-2b … P6-6). Shared files (`module.go` routes, `team_store.go`, `relay_handler.go`, `adopt_store.go`): the later PR rebases; the coordinator merges and deploys one at a time. air26 (alpha.627) is upgraded by the coordinator with each deploy that both hosts need.

## 10. Acceptance (after X5)

On air26: `pdx peers host allow-team mlab on`. On mlab, a lead: `pdx adopt air26/_<ref of the iOS session>` → App card shows "on air26" → approve → the air26 session reads 「你已成為 … 的 member」; `pdx team` on mlab lists it with HOST air26 and state active; its `relay status` on air26 says `member:true`. `pdx release` → it reads the release notice, `member:false`. Offline: stop air26's daemon; `pdx adopt` → after 10 min exit 14 and the seat is free; start air26 → the `void` arrives, nothing is adopted. Stop air26's daemon again with a member active; `pdx release` → `releasing`; start it → released and announced. allow-team off → `host_not_allowed` before any card. Re-pair air26's alias to another host id → queued commands answer `wrong_host` / are dropped, nothing reaches the other host.

## 11. Tests and mutation gates (each PR carries its part)

Identity: an outbox entry for host A after the alias is re-created for host B is never delivered to B (mutation: resolve by alias → red); `to_host_id` mismatch → `wrong_host`. Crash cuts (fault injection): between the business write and the enqueue (must be one tx — mutation: enqueue after commit → red); between "done" and applying the outcome (mutation: two transactions → red); between applying a fact and logging its id. Idempotency: same id + other body → `id_conflict`; a refusal replayed after its condition changed still answers the refusal. Monotonic: a release while `joining` + the adopt outcome arriving later → the row ends `released`, never `active` (mutation: unconditional `active` → red); `ended` racing `killing` → one terminal state. Membership generation: an old `ended` for a previous `mk` does not touch a re-adopted row. Role: a remote member asking to lead / being adopted locally / being a relay target → refused (mutation: `isLiveMemberIn` without `remote_members` → red). Capability: an unsupported kind is refused to the lead, never queued; a 400 `unsupported_kind` does not block the FIFO. Expiry: adopt not done in 10 min → `void`, seat freed; an ack lost after M applied → `void` undoes it (mutation: no `void` command → red). Unpairing on either side ends the relation on that side (live rows only; terminal rows unchanged); a 401 for 10 min → `unpaired_by_peer` and the same clean-up (mutation: treat 401 as transient forever → the queue-head test red). `ended` arriving before the adopt response → `gone`, the late `applied` ignored (mutation: `ended` only from `active` → red). `registered` binds to the pending spawn op. A `void` before its command → the later command answers `command_void` (void table first). Self-relay approval of a session adopted remotely meanwhile → cancelled. Proxy after the alias is re-bound → 403; proxy for a released member → refused in the transaction. Notices are delivered on M with M's `Peers.Deliver` off, with M's template text only (mutation: take text from the command → red), and the member's reply reaches the lead. Kill while `joining` / `releasing` → 409 `command_pending` (mutation: allow it → the rollback-stuck test red). Proxy with an `origin_inbox` naming the lead → 400, and no lead operation is reachable through it (mutation: forward to the existing handler → red). Notices: an offline release is announced when it lands, after more than 10 min. Transport: a 302 is not followed; a flood of fresh fact ids is rate-limited. Seats: `joining` / `releasing` / `killing` count.

## 12. Out of scope

Cross-host member relay (simplified P6-7 after P6-6; it produces `moved`); P4b's usage readings, repo inventory and automatic host choice (the lead names the host); signed grants (spec §11 "Later"); a member-host pull of team state; downgrading a member host below X2 while it has members (documented as unsupported; its rows are ended by unpairing).

## 13. Review ruling (codex `task-mv0sfcmr-2ebpfr`, all adopted)

#1 target by host id + receiver identity (§3.1-1, §6.1); #2 unpairing ends relations locally on each side and M learns L's forgetting at its next fact; downgrade below X2 unsupported (§3.2, §12); #3 a remote adopt's approval means consent, the CLI waits on the membership (§4.3); #4 every enqueue in its cause's transaction, outcomes and facts applied with their log in one transaction (§3.1-2/4, §11 crash cuts); #5 monotonic CAS state machines on both sides (§4.2, §5.2); #6 member key `mk` on every fact (§3); #7 `{id, kind, body_hash, outcome}` with refusals stored (§3.1-3); #8 `sessionRoleIn` across every gate (§5.3); #9 seat rule names the remote states (§4.2); #10 the runner's remote mode, first task on L (§5.5, §4.5); #11 reports / tasks proxied synchronously instead of one-way facts (§7); #12 durable remote notices without the 10-min give-up, handover included (§4.4); #13 per-kind capability in the inventory, unsupported kinds refused before queuing (§3.1-7); #14 `HostCaller` transport protections and the per-host limiter on both routes (§3.1-8); #15 re-estimated and re-split (§9); #16 the mutation gates of §11; #17 citations corrected in §2.

**v2 round (`task-mv0tskxu-o566yt`, all adopted):** C1 the plain 401 is classified (transient 10 min, then `unpaired_by_peer`) — §3.1-6, §3.2; C2 `ended` ends a `joining` row and the late `applied` is ignored — §4.2; I1 the silent-side residual is stated, made visible and user-clearable instead of claimed away — §3.2; I2 `registered` / `spawn_failed` bind to the pending spawn op — §4.5; I3 permanent outcomes defined per kind, `lead_moved` row on M — §4.2, §5.2; I4 `team_command_voids` checked before the log — §3.3, §5.1; I5 the self-relay approve gate joins `sessionRoleIn` — §5.3; I6 the proxy route gets the live-entry binding and the in-transaction member re-check — §6.1, §7; I7 notices are produced and delivered on M, independent of `Peers.Deliver` and of the lead's inbox — §4.4; I8 unpairing rewrites live rows only — §3.2, §4.2; I9 the facts route moves into X3b-2 with the transitions it applies — §9.

**v3 round (`task-mv0uhc5j-39z57l`, all adopted):** N1 kill only from `active`, `command_pending` otherwise, a refusal returns to `active` — §4.2; N2 commands carry the lead's full origin tuple and M delivers notices through a narrow `deliverTeamNotice` seam with its own templates — §4.4, §6.2; N3 the proxy refuses any forwarded `origin_inbox`, builds the actor from `{host_id, mk}` and runs only owner-scoped allow-listed operations — §7; N4 `sessionRoleIn` lands in X2a before any route — §9; N5 notice rows are written with the commands / unpairing from X2b / X2c, and M announces the kinds only from X3d — §9.

**v4 round (`task-mv0upzkd-co2465`, no critical, both adopted):** B1 `remote_members` stores the lead's full tuple and `team_name`, replaced by `lead_moved`, so a notice can be (re)delivered after a restart — §5.1, §4.4; B2 a relation ended by unpairing sends no notice (no bound sender to reply to); the member learns it from its role check and failing sends — §3.2, §4.4. **No critical in this round: the spec is final (stop rule).**

## 14. Corrections made during implementation (1f, 2026-10-09 → 10-10; each decided in a PR review, recorded here so the spec matches main)

Where a line below and an earlier section disagree, this section wins.

1. **Team roots for the App's spawn host picker (X1b, #2252).** The picker reads L's row in M's `GET /api/peers/hosts` (M's admin token) for `team_roots`; `GET /api/peers/team/roots` is for a host principal only.
2. **M's admin routes (X2c, #2277).** `GET /api/team/remote-members` and `POST /api/team/remote-members/end {mk}` (row → `ended{local_end}`, the `ended` fact and the member's notice in one transaction, self relay back on). `pdx peers host allow-team <alias> off --end-members` ends them all; turning `allow_team` off alone does not end existing members.
3. **Keys (X2b, #2268).** `team_command_log` and `team_command_voids` are keyed with `lead_host_id` (two lead hosts may reuse an id).
4. **D4 on L (X3b-1a, #2276).** A team that ends leaves L's remote rows as they are, under three conditions: every "live" read requires the team's `ended_at = 0`; ending the team still enqueues `end`; an `applied` / `ended` outcome that arrives after the end never makes a row `active`.
5. **Consent only withdraws (X2b, #2267).** A command never widens what `allow_team` / `team_roots` granted; a withdrawn consent refuses from then on and is re-read right before a kill's signal (X4a-1, #2308).
6. **Host scope of L's readers (X3b-1c, #2293).** Liveness, last turn, member relay, the release / kill / gone marks and the caller gates read this host's rows only; seats, task assignment, role gates and the mod's member count read both. A remote row is shown through `memberView` / the roster's remote path, never through this host's registry, usage or quota.
7. **Display (X5, #2299; #2335).** The roster lists remote rows in `joining` / `releasing` / `killing` too, with `host_id`, `host_alias`, `context_unavailable`. A remote member's context, model and title come from M's `GET /api/peers` through a 10 s cache (a roster build never waits; `GET /api/team` refreshes stale hosts first, ≤ 3 s); a failed read keeps the last titles. A remote row's `tmux_session` is the session name only (written split from the pane, and read through `tmuxName` for rows written before).
8. **Facts capability (X3b-2, #2306; X4a-3, #2318; X4b-1, #2323).** L announces `team.fact_kinds`; M's facts pump holds a fact whose kind L does not announce (not settled, no attempt limit; an L without `fact_kinds` gets none). L answers an unannounced kind `400 unsupported_kind` and **does not store that refusal** in `team_fact_log` — the one exception to §3.1 rule 3, so a fact replayed after L upgrades is applied, not answered from a stored refusal.
9. **Forwarded spawn (X4a-2, #2314; X4b, #2323 / #2328; #2337).** M caps what one lead host holds at 16 (running forwarded ops + active remote members). The member is launched with M's own `team.member_command`; the lead chooses only cwd (under the roots granted to it, re-resolved at create and launch), title, model and effort. L keeps its forwarded ops in `remote_spawns` (not `spawn_ops`, whose boot resume would restart a runner L does not own). `end` also reaches a host that holds only a running forwarded op, and aborts it on M (`failed{abandoned}`, its tmux session killed; no `spawn_failed` fact). Residue: #2341 (a session created in the window before a crash).
10. **Proxy (X6, #2329 / #2332).** The proxy answer is HTTP 200 carrying `{host_id, status, body}`; any key containing `origin` in the forwarded query, path or body is refused; only three owner-scoped calls run (post a report, list my tasks, set my task's status).
11. **Team appearance (TR-1, #2290; #2288).** `PUT /api/team/appearance` edits a live team's name, label and colour on L; the `team.appearance` command carries them to M's rows (#2288).
