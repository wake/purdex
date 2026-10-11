# Grant another host's directories from the lead's team panel (spec + plan, rev 4)

Owner: A line (1f) for the daemon phases, interface line (88) for the App phase. Builds on
`docs/specs/2026-10-09-cross-host-team-spec-plan.md` (§5.4 grant model, §14).

rev 4 (codex review of rev 3, `task-mv49okwm-itfami`: rev-1 findings resolved; 1 critical + 5 important new): the
pending-remove guard also covers remote **adopt**; live-state predicates are exact; the remove check takes the writer lock
first; a root-only 401 escalates through the existing unpair path; the lead's check is lexical and says so (symlink
spellings fall to the member host's pre-check); the reconciler drains each host on its own worker.

rev 3 (user 2026-10-12): a removal **is refused** while a member of this lead runs under the directory — the
authoritative check is on the **lead host**, in one `team.db` write transaction that also serializes with remote spawn
acceptance (no config lock involved, so codex rev-1 C3 / C6 do not arise); the member host keeps a best-effort pre-check
outside its config lock. Everything else of rev 2 stands.

rev 2 (codex review of rev 1, `task-mv48mnhb-2ng5z6`, 24 findings / 3 critical): the design is narrowed so the critical
problems do not arise instead of being patched — (a) the member host does **not** refuse a removal while a member runs
under the root (same as today's admin route; the App warns before sending), so the handler never touches `team.db`
inside the config lock and needs no serialization with spawn / adopt; (b) a change is a convergent set-membership request
("path P present / absent"), an explicit exception to base §3.1-3 with no receiver log; (c) delivery is a small
reconciler, not the generic outbox pump (no pump-interface change, no 401 escalation of its own); (d) reachability is
classified, the cache is rev-monotonic, rows keep the member host's canonical path, routes get the full peer admission
chain.

## 1. User decisions (2026-10-12, do not reopen)
- In the lead's team panel a directory on another host is written `air26:/Users/wake/Workspace/wake/aigora-ios`
  (the lead host's alias for that host, a colon, an absolute path). Plain paths stay the lead host's own team roots.
- The grant's scope stays **the whole lead host** (today's `PeerHost.TeamRoots` on the member host's entry for the lead
  host). The panel says so.
- **Offline is not a failure.** Adding or removing while the other host is offline is recorded on the lead host's daemon
  and applied when that host is reachable again, even with the App closed; the other host checks the path then and a
  refusal is shown on that row.
- Accepted trade-off: the lead host can widen its own grant on the member host within an existing `allow_team` consent.
- (user 2026-10-12, final) Removing a directory while a member of this lead runs under it is **refused**: the lead (or
  the user by hand) must end that session first, then remove. The refusal names the members.

## 2. What exists (facts on main 8dc9cc15d)
- Grant: `PeerHost.AllowTeam`, `TeamRoots` (canonical real paths), `TeamRootsRev` in the member host's config
  (`internal/config/config.go:44-113`; `CanonicalTeamRoots` :88-113 → `not_absolute` / `not_found` / `not_a_directory` /
  `too_many`; `MaxTeamRoots = 16`).
- Edited only by the admin token: `PUT /api/peers/hosts/{alias}` (`internal/module/peers/hosts.go`: `requireAdmin`
  :247-256, `applyRootsChange` :176-203, `removalCandidates` :131-155 — cleaned spelling and real path, a dangling link
  lexically; writes inside `Core.UpdateConfig` :728-741). No in-use check on removal.
- Peer read of one's own roots: `GET /api/peers/team/roots` (`internal/module/peers/team_caps.go:56-85`) answers
  `{host_id, roots}` (no rev); JSON 403 when consent is off (:71-77).
- Spawn check: `resolveUnderRoots` (`internal/module/team/spawn_remote.go:75-90`) at accept; `underOpRoots` (:96-103)
  re-resolves the entry at create / launch; `checkLaunchPane` after. So a root removed after acceptance stops the launch.
- Peer route admission: `HostRoutePolicy` (`internal/module/peers/policy.go:31-37`) refuses host principals on routes not
  listed; `BindHostPrincipal` (`bind.go:21-50`); commands apply a body cap before decode and `wrong_host` 409 for a
  foreign `to_host_id` (`internal/module/team/commands_handler.go:36-86`); base §3.1-8 per-host rate limit.
- Transport: `HostCaller.Call` (`internal/module/peers/hostcaller.go:~85-175`) — POST with `to_host_id`, live host-id
  resolution, no redirect, response cap, answer `host_id` check; a JSON 4xx keeps only `error` / `detail` (detail cut at
  200 bytes, `hostcaller.go:154`).
- Host lookup used by remote spawn / adopt: alias first (case-insensitive, `config.go:215`), then host id
  (`spawn_remote_lead.go:78`, `adopt_remote.go:54`).
- `spawn_ops` has its own `lead_host_id` column (`migrate.go:105`); not needed by this design.

## 3. Design

### 3.1 Member host (phase XR-1): `POST /api/peers/team/roots-change`
- Host principal only; added to `HostRoutePolicy`; `BindHostPrincipal`; body cap (64 KiB) before decode; the per-host
  rate limit of base §3.1-8; `to_host_id` not this host → 409 `wrong_host` (as commands). Owned by the team module next
  to `CommandsRoute` (it has `Core.UpdateConfig`).
- Body `{id, to_host_id, op, path}`: `op` ∈ `add` | `remove` | `read`; `path` absolute and clean for add / remove,
  empty for read (else 400 `bad_request`).
- One `UpdateConfig` closure that reads and writes **only config** (no `team.db`, so no lock-order question):
  1. the caller's own entry (by the bound principal's host id) missing or `AllowTeam` off → 403
     `{error: "host_not_allowed"}`;
  2. `remove`: `removalCandidates` on the entry; absent → unchanged (not an error);
  3. `add`: `CanonicalTeamRoots([path])` → 400 `{error: "bad_root", detail}`; already present → unchanged; would exceed
     16 → 400 `bad_root` / `too_many`;
  4. changed → write, `TeamRootsRev++`; unchanged → no write, rev unchanged.
- Answer 200 `{id, host_id, op, result: "added" | "removed" | "unchanged", path: <canonical for add / the matched
  stored root for remove / "" for read>, team_roots, team_roots_rev}`.
- **Idempotency — exception to base §3.1-3** (stated in the base spec too): the request is "path P present / absent",
  so a replay converges to the same membership whatever happened in between; the answer reports the state at that
  moment. There is no receiver-side log (config.toml and `team.db` cannot share a transaction, and none is needed for a
  convergent request). The lead never reuses an id (a UUID per row).
- **In-use pre-check (best effort, defence in depth)** for `remove`, run **before** `UpdateConfig` (never inside it: the
  closure must not touch `team.db`, codex rev-1 C3): `remote_members` of the caller's host id with `state = 'active'`
  and this lead's `spawn_ops` with `state = 'running' AND lead_host_id = ?` whose real cwd is within the removed root and not within
  another root the entry keeps → 409 `{error: "roots_in_use", detail: "<ref>,<ref>,…"}` (refs / op ids, comma list,
  fits the 200-byte detail the transport keeps). It is not atomic with the write; the lead's check (§3.2) is the
  authoritative one, and a spawn accepted in the gap is still stopped at create / launch by `underOpRoots`.
- Every change (not `unchanged`, not `read`) logs `team roots: <alias> (<host id>) added|removed <path>, rev N`.
- `GET /api/peers/team/roots` also answers `team_roots_rev` (additive).
- Caps: `teamCapsFor` gains `roots_change: true` (a capability boolean, not a team-command kind).
- Base-spec amendments (one PR with XR-1): §14.5 「Consent only withdraws」 → 「`allow_team` only withdraws; under it the
  lead host may add / remove its roots (user 2026-10-12)」; §3.1-3 gains the exception above.

### 3.2 Lead host (phase XR-2): record, deliver, report
- **Table** `host_root_changes` (`team.db`, new migration; the deployed-schema rule applies once shipped):
  `id TEXT PK, host_id TEXT NOT NULL, op TEXT NOT NULL CHECK (op IN ('add','remove')), path TEXT NOT NULL,
  state TEXT NOT NULL CHECK (state IN ('pending','done','refused','dropped')), code TEXT NOT NULL DEFAULT '',
  detail TEXT NOT NULL DEFAULT '', canonical TEXT NOT NULL DEFAULT '', first_401_at INTEGER NOT NULL DEFAULT 0,
  created_at, updated_at`; index `(host_id, state)`.
- **Cache** `host_roots_seen`: `host_id PK, roots_json, rev, seen_at`. Written only with `rev >= stored rev`
  (`INSERT … ON CONFLICT DO UPDATE … WHERE excluded.rev >= rev`), so a late, older answer never overwrites a newer one.
- **Reconciler** (a coordinator plus **one worker per host** with pending rows, at most one call in flight per host, so
  an offline host — `HostCaller.Call` can wait ~12 s — never delays another; started / stopped with the team module;
  woken on insert and every 60 s):
  - for each host with pending rows, in rowid order, one `HostCaller.Call` per row (POST roots-change, `op` / `path`
    of the row): 200 → in **one transaction** row `done` + `canonical` + cache upsert; JSON 4xx (`host_not_allowed`,
    `bad_root`, `wrong_host`, `bad_request`) → row `refused` with `code` / `detail`; anything else (transport, 5xx,
    429, 401, 404 / unsupported) → stop this host for this pass, the row stays pending (order is kept: a later row of
    the host is never sent before an earlier pending one);
  - a host whose caps (`TeamCaps`, cached 30 s) lack `roots_change` is skipped (rows stay pending);
  - rows whose host is no longer paired → `dropped`; `scanUnpaired`'s host set includes `host_root_changes`.
  - **401 owner**: a 401 stamps `first_401_at` on the host's head row (kept across restarts, cleared by any non-401
    answer); after 10 min of continuous 401 the worker calls the existing `unpairHost(host, unpaired_by_peer)` — the same
    escalation as the command pump (base §3.1-6), now also dropping the host's pending `host_root_changes` rows in its
    transaction — so a host with only root rows pending is still unpaired, not left pending forever.
  - The reconciler records the last result class per host in memory: `ok`, `offline` (transport / 5xx / 429 / timeout),
    `consent_off` (403 `host_not_allowed`), `unsupported` (caps lack `roots_change`, or 404), `unauthorized` (401).
- **Routes** on the lead host (ordinary token):
  - `POST /api/team/host-roots` `{host, op: "add"|"remove", path, client}` — `client` must be the App claim
    `{kind: "app", label}` (as `PUT /api/team/roots`, `roots_handler.go:21-28`; told, not enforced). `host` resolves as
    remote spawn does (alias case-insensitive, then host id) to a **paired, verified** entry, else 400 `unknown_host`;
    `path` absolute and clean, else 400 `bad_root` / `not_absolute`. Inserts a pending row, wakes the reconciler,
    answers 202 with the row.
  - **Remove target**: `path` must be a root in the cache (the member host's canonical spelling, which the App lists) or
    the path of a pending `add` row of that host; else 400 `not_a_root`. The App never offers anything else.
  - **Remove is refused while in use (authoritative for lexical containment)**: one `team.db` write transaction that
    **takes the writer lock first** (a write-first statement, as `AcceptRemoteSpawn` does, or `BEGIN IMMEDIATE`) and then
    looks for members of **this lead host** on that host whose cwd is within `path` by path components and not within
    another root it believes the host keeps (the cache minus `path`, plus pending adds; no cache → none, the stricter
    answer): `team_members` of that host id with `state IN ('joining','active','releasing','killing')` whose team has
    `teams.ended_at = 0`, and `remote_spawns` of that host id with `state = 'running'`. Any → 409 `{error:
    "roots_in_use", uses: [{ref | op_id, title, cwd}]}`, no row is written. The App shows 「這個資料夾還有 member 在用：…。
    先結束它們才能移除。」. The lead cannot resolve the member host's symlinks, so a member whose recorded cwd uses another
    spelling of the same directory is not seen here; the member host's pre-check (§3.1) refuses that removal and the row
    shows it.
  - **Pending-remove guard** on both ways a member of this lead appears on that host, each inside its existing write
    transaction after the writer lock is held: remote spawn acceptance (`AcceptRemoteSpawn`) and remote adopt approval
    (`adoptApprovedIn`, before the `joining` row is inserted) refuse a cwd within a path that has a **pending remove** row
    for that host → 409 `root_being_removed`. With the writer lock taken first on every side, SQLite serializes them:
    either the removal is refused (a member exists) or the spawn / adopt is (a removal is pending). Lexical, as above.
  - A member host refusal `roots_in_use` (its pre-check found a member the lead did not know of) lands on the row as
    `refused` with the refs in `detail`.
  - `GET /api/team/host-roots` — for every paired host: `{host_id, alias, status (the class above, or "unknown" before
    the first contact), roots, rev, seen_at, changes}`; `changes` are the host's `pending`, `refused` and `dropped` rows
    (done rows are reflected in `roots`). Before answering it runs one `read` per paired host **in parallel** with a
    2 s overall bound (each success updates cache and status; a host that does not answer in time keeps its cache and
    last status). No claim needed.
  - `POST /api/team/host-roots/dismiss` `{id}` — a `refused` / `dropped` row is deleted; a pending one → 409
    `pending` (to undo a pending change, send the inverse op). No claim needed.
- **What the App shows per host** (derived): the cached `roots` as 已生效; each pending row as
  `等 <alias> 上線` (status offline / unknown), `<alias> 需要更新` (unsupported), `<alias> 的配對失效` (unauthorized), or
  `套用中` (ok); each refused row as `失敗：<reason>` with a dismiss button; a status `consent_off` shows
  「<alias> 沒有允許 <lead host> 開 member」 for the host.

### 3.3 App (phase XR-3, interface line)
- Team panel root editor: a line `alias:/abs/path` whose alias is a paired peer alias (aliases never contain `:` or `/`)
  goes to §3.2; anything else stays the per-team local roots. Rows show the §3.2 states; the remote section says
  "<alias> 的授權整台 <lead host> 共用（所有 team）".
- Removing a remote root that is in use: the 409 `roots_in_use` from §3.2 is shown on that row with the member list —
  「這個資料夾還有 member 在用：<title>（<ref>）…。先結束它們才能移除。」; nothing is queued.
- Out of scope: iOS (no root editor), the member host's own `TeamConsent` page (unchanged; both edit the same entry — an
  admin whole-set PUT may replace remote changes, last writer wins, as for any two admins).

## 4. Compatibility and order
- XR-1 and XR-2 can ship in either order: until the member host announces `roots_change`, the lead keeps rows pending
  (shown as `需要更新`). XR-3 merges after XR-1 + XR-2 run on both hosts.
- `team_roots_rev` on the roots GET is additive; old leads ignore it.

## 5. Plan
### XR-1 (member host; one PR, est. ≤ 500 lines)
1. Tests first (`internal/module/team/roots_change_test.go`, through the real middleware chain): non-host principal →
   refused by policy; foreign `to_host_id` → 409 `wrong_host`; oversize body refused before decode; rate limit;
   `allow_team` off / no entry → 403; `bad_request` shapes (op unknown, path missing, relative, both); add missing /
   file / 17th → 400 detail; add existing → 200 unchanged, rev unchanged; add via symlink → canonical path answered and
   stored; remove absent → unchanged; remove by symlink spelling and by a dangling link → removed; replay of the same id
   after an admin change in between → answers the current state (convergence, no error); remove under an active
   member of this lead → 409 `roots_in_use` with its ref, nothing written; a member of another lead host, a finished
   member, or one still covered by another kept root → not counted; the pre-check runs outside `UpdateConfig` (a test
   holding `CfgMu` while the handler runs the pre-check proves no nesting); read → state, no write; log
   line on change only; caps carry `roots_change`; roots GET carries `team_roots_rev`; a spawn accepted before a removal
   fails at launch (existing `underOpRoots`, one test to pin it).
2. Share one root-mutation implementation with `PUT /api/peers/hosts` (export from peers or move to `internal/config`;
   no copy).
3. Amend the base spec (§14.5, §3.1-3 exception).
4. Mutation: drop the `AllowTeam` check → the 403 test goes red; bump rev on `unchanged` → red.
5. `go test -race ./internal/module/team/... ./internal/module/peers/... ./internal/config/...`, `make lint`.

### XR-2a (lead host: store + reconciler; one PR)
1. Migration + store: insert / pending-by-host in rowid order / settle done+cache in one tx / refuse / drop / dismiss;
   cache monotonic (older rev ignored, equal rev refreshes `seen_at`); CHECK constraints.
2. Reconciler with a fake caller: per-host order kept when the head fails; 200 / each JSON refusal / each non-settling
   class; caps without `roots_change` → skipped; unpaired host → rows dropped; done + cache atomic (crash between is
   impossible by construction — test that a failing cache write rolls the row back); two hosts independent (one offline
   does not delay the other — a host blocked ~12 s in a call); start / wake / stop lifecycle; status class per host;
   401: `first_401_at` set and survives a restart, cleared by a non-401, after 10 min → `unpairHost` runs and the
   host's root rows are dropped (also when no team command is pending).
3. Mutation: send a later row after a failed head → order test red; write cache without the rev guard → late-answer test
   red.

### XR-2b (lead host: routes; one PR)
1. POST: claim required; host by alias (case-insensitive), by host id, unknown, unverified entry (no host id) → 400;
   path checks; 202 row; reconciler woken.
2. Remove target: cached root → ok; pending add's path → ok; anything else → 400 `not_a_root`.
   Remove in use: each live state (`joining`, `active`, `releasing`, `killing`) under the path → 409 with `uses`, no
   row; `failed` / released / killed / gone, a member of an ended team (`ended_at ≠ 0`), a member on another host, one
   covered by another cached root → allowed; a `running` remote spawn under it → 409; no cache → stricter answer.
   Guards: pending remove row covering the cwd → `AcceptRemoteSpawn` and `adoptApprovedIn` each 409
   `root_being_removed`; after the row settles → allowed. Races (barrier on both orders, writer lock taken first on each
   side): remove vs spawn-accept and remove vs adopt-approve → exactly one wins, never `SQLITE_BUSY`. Mutation: drop the
   adopt guard → its race test red; count `failed` as live → red; SELECT before taking the writer lock → the race test
   flakes red under `-count=50`.
3. GET: parallel reads bounded at 2 s total (three fake hosts: fast, slow beyond 2 s, refusing) — the fast one updates,
   the slow one keeps cache, the answer comes in ≤ ~2 s; status classes; changes filter.
4. dismiss: refused / dropped → deleted; pending → 409; unknown id → 404.
5. `go test -race` on touched packages, `make lint`; full `go test ./...` at release (lead).

### XR-3 (SPA; interface line)
Brief from 88 after XR-1 / XR-2 merge; the contract is §3.2 and §3.3.

## 6. Out of scope
- Per-team scope on the member host; turning `allow_team` on from the lead; ending the members from the refusal
  (the user or the lead does it, then removes again); #2453 (closed 2026-10-12).
