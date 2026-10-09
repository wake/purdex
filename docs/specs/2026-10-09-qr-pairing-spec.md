# Phone pairing by QR code (hosts + profile) — spec

Date: 2026-10-09 · Owner: interface line lead (`mlab/purdex-88-b8`) · Status: revised after plan review
(`task-mv0shz3e-31lcty`, 24 findings folded in — table at the end of the plan)

The phone (Purdex iOS) is paired by scanning a QR code shown by the Mac App. After the scan the phone reaches every
host the Mac has a token for, with **its own token per host** (revocable on its own), and follows one profile of the
Mac one way. The iOS part is built in purdex-ios against this contract.

## 1. Rulings

| # | Ruling | Who / when |
|---|--------|------------|
| R1 | Pairing is by a QR code shown by the Mac App; scanning it connects the phone to all the hosts and brings in a profile; the QR is chosen for one profile. Tailnet only. | User, 2026-10-07 (purdex-ios `docs/ideas/2026-10-07-ios-direction.md` I3, `docs/brief/2026-10-07-p6-daemon.md` §2) |
| R2 | **The phone's token is its own, one per host, revocable without touching the Mac.** The QR carries no token, only a one-time short-lived code. | User, 2026-10-07; reconfirmed 2026-10-09 |
| R3 | The phone does no host management: hosts come only from the QR. | User, 2026-10-07 |
| R4 | The profile flows **one way, Mac → phone**: workspaces, tabs, the appearance settings the phone understands. | User, 2026-10-09 |
| R5 | The only thing the phone writes back is **a tab it adds**, placed **at the end of the same workspace**. | User, 2026-10-09 |
| R6 | Closing, on the phone, a tab that came from the Mac **hides it on the phone only**; the phone has "show hidden tabs". | User, 2026-10-09 |
| R7 | A phone token cannot use arbitrary-path file access, nor the other routes outside its allow-list (§3.3). | purdex-ios brief §4a (user, 2026-10-07) |

Facts this spec stands on (main @ 2026-10-09, measured):
- The daemon knows **one** token for its general routes, `Cfg.Token` (`cmd/pdx/http_chain.go`, `internal/middleware/middleware.go`); a peer's `pdxp_` token is accepted only on `/api/peers*` (`internal/middleware/peer_auth.go`, `internal/module/peers/policy.go`). No per-device token, scope or revocation on the general routes. WebSockets authenticate by bearer or by a one-time ticket from `POST /api/ws-ticket` (`internal/core/core.go`, `internal/core/ticket.go`; the validator answers only a bool today).
- `IPWhitelist` allows every source when its `allow` list is empty (`internal/middleware/middleware.go`).
- Host transfer (`internal/module/hosttransfer`): opaque rows uploaded with the relay's admin token, an 8-char Crockford code (40 bits, 10 min, one-time take inside one critical section, 16 live codes, 10 failed attempts per minute per daemon), redeemed with the **relay's admin token**. The Share-hosts rows carry the sharer's admin tokens. Memory only.
- Profiles (`internal/module/profiles`): sections `workspaces`, `tabs.<ws>`, `settings` on the SOT (dev host) daemon; whole-section CAS writes (`clientId`, `baseRev`, `hash`, `fingerprint`, `ordinal`); a `profile` host event per section change. In the SPA the master profile may be parked while another is shown (`spa/src/lib/profile/master-world.ts`: `readMasterWorld`, `writeMasterWorld` → `'ok' | 'unsettled'`); profile sync runs only in the leader window, with the host connected and the attachment confirmed (`spa/src/lib/profile/start.ts`, `leader.ts`).
- Go's `ServeMux.Handler(r)` returns the pattern a request would be dispatched to; `ServeMux` cannot list its patterns.
- No QR library in `spa/package.json`.

## 2. Flow

1. **Mac, 「配對手機」** (Hosts page): the person picks the profile (default: this Mac's master profile) and the relay
   (default: the active host).
2. **Mint.** The Mac makes a `pairing_id` (UUID) and, for every host it has a token for, calls `POST /api/devices` with
   its admin token → a device token for the phone (§3). On the profile's **SOT host** the token is bound to that profile
   (`profile_id`); on the other hosts it is bound to none. **If the SOT host fails, the pairing is aborted** (everything
   minted is revoked) — the phone could not read the profile. Other hosts that fail are left out and listed.
3. **Package.** The Mac creates a **pairing entry** on the relay, `POST /api/host-transfer/pairings` (§4.1), with one pair
   row per minted host, and gets a code.
4. **Show.** The dialog shows the QR (`purdex://pair?v=1&relay=<ip:port>&code=<code>`), the code as text, and the
   countdown, and polls `GET /api/host-transfer/pairings/{code}` (§4.3) every 2 s: once claimed it shows 「已配對」 and
   closing the dialog revokes nothing. Closing **before** a claim (or the countdown running out) deletes the entry
   (`DELETE /api/host-transfer/pairings/{code}`) and revokes the minted tokens (`DELETE /api/devices?pairing_id=` on each
   host).
5. **Phone, scan.** The phone parses the QR and calls the relay's `POST /api/host-transfer/pairings/claim` `{code}` —
   no token; the code is the credential (§4.2). It gets the pair rows.
6. **Verify and add** (§7): for each row, `GET /api/info` with the row's device token; keep the rows whose `host_id`
   equals the row's `daemonId`; add them; set the phone's name on each host with `PUT /api/devices/self`.
7. **Profile** (§5) from the SOT row.
8. **Push**: re-register on every host that announces `push.v1`, now with the device token.

## 3. Device tokens (daemon)

### 3.1 Store

New module `devices`, `devices.db` in the data dir (owner-only), one table:

```sql
CREATE TABLE IF NOT EXISTS device_tokens (
  id TEXT PRIMARY KEY,             -- "d_" + 12 hex
  pairing_id TEXT NOT NULL,
  profile_id TEXT NOT NULL DEFAULT '', -- the one profile this token may read (SOT host only); '' = none
  label TEXT NOT NULL,
  token_hash TEXT NOT NULL UNIQUE, -- SHA-256 hex; the token itself is never stored
  created_at INTEGER NOT NULL,
  created_by TEXT NOT NULL,
  use_by INTEGER NOT NULL,         -- created_at + 15 min: a token first used after this is refused
  first_used_at INTEGER NOT NULL DEFAULT 0,
  last_used_at INTEGER NOT NULL DEFAULT 0,
  revoked_at INTEGER NOT NULL DEFAULT 0
);
```

- Token format `pdxd_` + 32 hex (16 bytes, `crypto/rand`), returned **once** by `POST /api/devices`.
- **First use** is decided at lookup, in one statement: `UPDATE … SET first_used_at = now WHERE id = ? AND
  first_used_at = 0 AND use_by >= now AND revoked_at = 0` — a token never used by `use_by` is refused at once (not at the
  next sweep), and a token that was used is never taken for unused. A sweep only deletes rows that can no longer work
  (unused past `use_by`, or revoked more than 30 days ago).
- `last_used_at` is written at most once a minute per token.
- The lookup is by the indexed hash. (The hash of a 128-bit random token gives an attacker nothing to time; no separate
  constant-time compare is claimed.)

### 3.2 Authentication

- `TokenAuth` accepts the admin token as today, **or**, for a bearer starting `pdxd_`, a device token that is live
  (not revoked, used or still before `use_by`). The request carries a **device principal** `{id, pairing_id,
  profile_id}`.
- **Tickets carry principals:** `POST /api/ws-ticket` records the caller's principal with the ticket; validating a
  ticket consumes it and returns the principal (the validator's contract changes from `bool` to `(Principal, bool)`), and
  the WS handlers register each connection with that principal.
- `/api/peers*` is unchanged; an empty `Cfg.Token` is unchanged (device tokens add nothing there).

### 3.3 Scope: default-deny allow-list (R7)

- A device principal may reach **only** the patterns in `deviceAllowed` (exact pattern strings as registered); every
  other route answers 403 `device_forbidden`. The check runs after auth, on the inner mux, using
  `ServeMux.Handler(r)` to learn the pattern the mux itself would dispatch to — the same matching, wildcards and
  method rules as the dispatch. A new route is therefore denied to phones until it is added.
- `deviceAllowed` v1 is exactly what the iOS App uses (the plan lists the patterns and a test pins the set):
  `GET /api/info`; `POST /api/ws-ticket`; the sessions list and the per-session reads the App uses; the terminal / mirror
  WS; the host-events WS; conversations (snapshot, increments, subagents) and `/ws/conversations`; team approvals list /
  get / decide, `GET /api/team/roster`, `GET /api/team`, unattended `GET` and `PUT`; `PUT /api/team/relay-quota`;
  `POST /api/push/devices`, `GET /api/push/devices`, `DELETE /api/push/devices/{device_id}`; profile **read** routes;
  `POST /api/profiles/{id}/tab-requests`; `PUT /api/devices/self`; plus session create / send-keys **only if** the iOS
  quick-new-tab uses them (purdex-ios confirms before QP-1 merges). `GET /api/health` and `/api/peers*` sit on the outer
  mux and never see a device principal.
- **Ownership inside allowed routes:**
  - push: a registration made with a device token records that device id; a device principal lists and deletes only its
    own registrations (others are invisible / 404);
  - profiles: a device principal reads only its `profile_id` (other ids → 404) and posts tab requests only to it; a token
    with no `profile_id` reads no profile.

### 3.4 Management routes (admin token only)

- `POST /api/devices` `{pairing_id, profile_id?, label, client}` → `{id, token, pairing_id, profile_id, label,
  created_at, use_by}`.
- `GET /api/devices` → rows without token or hash.
- `DELETE /api/devices/{id}`, `DELETE /api/devices?pairing_id=<uuid>` → revoke (idempotent, 204). Revocation closes
  every open connection of that principal — host-events, terminal and conversation WebSockets alike — through one
  registry the WS handlers register with (§3.2).
- `PUT /api/devices/self` `{label}` (device token) → its own label, 1–64 printable runes.
- Capability `devices.v1`.

## 4. Pairing entries on the relay (daemon)

Pairing entries are **separate** from Share-hosts transfers: own create route, own validation, own claim; a Share-hosts
code can never be claimed or touched without the relay's admin token.

### 4.1 Create (admin)

`POST /api/host-transfer/pairings` `{rows: [...]}` → `{code, expiresAt}`. Every row must be exactly:

```json
{ "v": 1, "kind": "pair",
  "name": "mlab", "ip": "100.64.0.2", "port": 7860, "daemonId": "<host id>", "look": { },
  "token": "pdxd_…", "deviceId": "d_…", "pairingId": "<uuid>",
  "profile": { "hostDaemonId": "<SOT host id>", "profileId": "p_…", "name": "<profile name>" } }
```

— no other fields (a row with any unknown key, a token not starting `pdxd_`, mixed `pairingId`s or `profile`s, or
`v != 1` → 400). Same limits as transfers (32 rows, 64 KiB, 16 live codes shared with transfers, 10 minutes).

### 4.2 Claim (no bearer)

`POST /api/host-transfer/pairings/claim` `{code}`:
- **Tailnet or loopback source only**, checked by the route itself whatever `IPWhitelist`'s `allow` says (100.64.0.0/10,
  fd7a:115c:a1e0::/48, 127.0.0.0/8, ::1); anything else → 403. Exempt from `TokenAuth` and from the device scope (exact
  path match in `http_chain.go`).
- Takes only pairing entries (a transfer code answers 404 `invalid_code` and is **not** consumed). One-time: the take
  marks the entry claimed and returns the rows; a second claim → 404.
- Shares the failed-attempt limiter with redeem (10 per minute per daemon).

### 4.3 Status and delete (admin)

- `GET /api/host-transfer/pairings/{code}` → `{claimed: bool, claimedAt?, expiresAt}`; a claimed entry keeps its status
  (rows dropped) until its original expiry, then disappears (404).
- `DELETE /api/host-transfer/pairings/{code}` → removes an unclaimed entry (204; claimed or unknown → 204 too).

## 5. Profile, one way (R4–R6)

### 5.1 Reading

The phone reads, on the SOT host with its device token, `GET /api/profiles/{id}` (its `profile_id` only), re-reads a
section when the `profile` host event names it, and on every return to the foreground. It keeps the Mac's order. From
`settings` it applies only the keys it understands (appearance). It never writes a section.

### 5.2 Adding a tab (R5) — a request one Mac applies

The phone does not write sections (it would have to reproduce the Mac's hash, fingerprint and ordinal). Instead:

- **Post** (device): `POST /api/profiles/{id}/tab-requests` `{request_id, workspace_id, host_id, session_code,
  session_name}` — idempotent by `request_id`; ≤ 200 open per profile (429); dropped after 7 days unapplied; emits
  `profile.tab_request` `{profile_id, request_id}`.
- **Claim** (admin): `POST /api/profiles/{id}/tab-requests/claim` `{client_id}` → the open requests not leased to
  someone else, now leased to this client for 60 s (atomic in the daemon). Only a client attached to the profile may
  claim (404 otherwise).
- **Apply** (SPA, §6): only the **profile leader window**, only while its profile sync is running (host connected,
  attachment confirmed). It reads the master world (`readMasterWorld`, which works whether the master is shown or
  parked), and for each claimed request: if that workspace already has a tab for `host_id + session_code` → just
  delete the request; else append a tmux-session tab at the end of that workspace (if the workspace is gone: the Mac's
  usual fallback for a tab with no workspace — active, else first, else Unsorted) and write it with
  `writeMasterWorld`; on `'unsettled'` keep the lease running out and try again later. After the profile write is
  confirmed, `DELETE /api/profiles/{id}/tab-requests/{request_id}` (admin).
- **Exactly-once in practice:** the claim lease stops two windows or two Macs from applying the same request at the same
  time; the "already has a tab for this session" check makes a re-apply after a crash (lease expired before the delete)
  a no-op. A Mac that applied but lost the delete simply deletes it next time.
- The phone shows its added tab at once, locally; when the profile update arrives it becomes the synced tab (matched by
  host id + session code). No Mac online: the request waits.

### 5.3 Hiding (R6)

Phone-only: closing a synced tab adds its tab id to the phone's hidden set (device-local); hidden synced tabs stay
hidden after later profile updates; "show hidden tabs" lists them and unhides one or all. A tab the phone added that no
Mac has applied yet is simply removed (its open request is left to expire; the phone does not delete requests).

## 6. Mac App

- Hosts page: **配對手機** beside Share hosts → the pairing dialog (§2 steps 1–4): profile and relay pickers, included /
  left-out hosts, the QR, the code, the countdown, 「已配對」 once claimed; close before claim deletes the entry and revokes.
- Hosts page: **已配對的手機** — one row per `pairing_id` across hosts (label, hosts it reaches, last used), **撤銷**
  revokes on every host; an unreachable host shows "not revoked yet" and is retried when it connects.
- The tab-request consumer (§5.2): listens for `profile.tab_request` (a new branch of the profile WS dispatch), claims
  on connect, on the event, and every 60 s while leader.
- QR rendering by a small local library (SVG; nothing leaves the machine).

## 7. iOS (contract only)

Scan; parse `purdex://pair?v=1&relay=&code=` (other versions → "update the App"); claim; for each row `GET /api/info`
with its token — keep the row only when `host_id` equals `daemonId` (missing or different `host_id` → shown as failed,
not added); **a row whose daemon id the phone already has replaces that host entirely** (address, token, look, name: the
phone does no host management, R3); two rows with one daemon id → the first wins, the rest are reported; set the label;
read and follow the profile; tab requests for added tabs; hidden tabs; re-register push with the device tokens. The
developer-mode manual entry stays.

## 8. Delivery plan

1. **QP-1 daemon: devices module + scope** — store, first-use rule, auth, ticket principal, WS connection registry,
   default-deny allow-list via `ServeMux.Handler`, ownership rules (push, profiles), management routes. (~800 lines incl.
   tests; split QP-1a store+auth+routes / QP-1b scope+tickets+WS registry if it runs over.)
2. **QP-2 daemon: pairing entries + tab requests** — create / claim / status / delete with tailnet enforcement, row
   validation; tab-request table, post / claim-lease / delete, event. (~550)
3. **QP-3 SPA: pairing dialog + paired phones.** (~600)
4. **QP-4 SPA: tab-request consumer** (leader-only, master world, event wiring). (~350)
5. iOS in parallel against §2, §4, §5, §7.

## 9. Acceptance

- Unit: first-use rule (used / unused past `use_by` / revoked), tickets return principals, every allowed pattern is
  registered and nothing else is reachable with a device token (pinned set), push and profile ownership, claim refuses
  transfer codes without consuming them, claim enforces tailnet with an empty `allow`, pairing rows strictly validated,
  status after claim, tab-request lease (two claimants, one wins), apply idempotent, parked master, revoke closes every
  WS kind.
- Real devices: 配對手機 for mlab + a26 → scan on the iPhone 8 → both hosts with their colors, the profile's workspaces and
  tabs; the dialog shows 「已配對」; a tab added on the phone appears at the end of the same workspace on the Mac; closing a
  synced tab on the phone hides it there only and "show hidden tabs" brings it back; 撤銷 → the phone gets 401 on both
  hosts (and its open views close) while the Mac keeps working.
