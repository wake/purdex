# Phone pairing by QR code (hosts + profile) — spec

Date: 2026-10-09 · Owner: interface line lead (`mlab/purdex-88-b8`) · Status: final for implementation after three plan
reviews (`task-mv0shz3e-31lcty`, 24 findings; `task-mv0sqg4y-wmqxpq`, 6; `task-mv0t6ul0-wqmv7l`, 3) and the purdex-ios
review — tables at the end of the plan. Tab requests are dropped: the phone appends to its profile's tabs section itself,
under a daemon append-only rule (§5.2).

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
- Profiles (`internal/module/profiles`): sections `workspaces`, `tabs.<ws>`, `settings` on the SOT (dev host) daemon; whole-section CAS writes (`clientId` = `c_` + 12 hex, `baseRev`, `hash`, `fingerprint`, `ordinal`); a `profile` host event per section change. The daemon treats a payload as opaque JSON and never checks `hash` (`handler_sections.go`); `PutSection` replaces a live row only by `UPDATE … WHERE rev = baseRev` after its schema gate (`sections.go`). A `tabs.<ws>` payload is `{order: [tab id…], tabs: {id: tab}}`, built for every workspace, empty ones included (`spa/src/lib/profile/sections.ts` `buildTabsSection`, `buildDocument`). In the SPA, a Mac with an unpushed change to a section the SOT has moved meets a conflict the user answers (`executor.ts`, `lock-conflict`).
- Go's `ServeMux.Handler(r)` returns `(handler, pattern)` for the route a request would be dispatched to; `ServeMux` cannot list its patterns. `newOuterHandler(c, mux http.Handler, allow)` takes the inner mux as a plain `http.Handler`, and its tests pass wrappers (`cmd/pdx/http_chain.go`, `http_chain_test.go`).
- The nex module mounts its embedded engine under the methodless prefix `/api/nex/` (`internal/module/nex/module.go`):
  one pattern for every engine route, reads and writes alike; only a few nex routes have patterns of their own
  (`GET /api/nex/v1/executions`, the POSTs beside it).
- A Mac fast-forwards a section without pulling when the event's hash equals the hash it holds
  (`spa/src/lib/profile/sync-state.ts`, `finish`): a write carrying a wrong hash can go unseen.
- Host transfer's failed-attempt limiter is one window for the whole store (`hosttransfer/store.go`); redeem reaches it only after the admin token check (`handler.go`). Its 16-code cap counts every entry in the map.
- No QR library in `spa/package.json`.

## 2. Flow

1. **Mac, 「配對手機」** (Hosts page): the person picks the profile (default: this Mac's master profile) and the relay
   (default: the active host).
2. **Mint.** The Mac makes a `pairing_id` (UUID) and a **deadline** D = now + 10 min, and, for every host it has a token
   for, calls `POST /api/devices` with its admin token and `use_within_s` = (D − now) + 300 → a device token for the phone
   (§3). Durations, not instants, so no host's clock matters: every token's `use_by` lands about 5 minutes after D. On
   the profile's **SOT host** the token is bound to that profile (`profile_id`); on the other hosts it is bound to none.
   **If the SOT host fails, the pairing is aborted** (everything minted is revoked) — the phone could not read the
   profile. Other hosts that fail are left out and listed.
3. **Package.** The Mac creates a **pairing entry** on the relay, `POST /api/host-transfer/pairings` (§4.1), with one pair
   row per minted host and `expires_in_s` = D − now, and gets a code. Less than 60 s left → abort and revoke. So the
   entry always dies before any of its tokens' `use_by`, at least 5 minutes before.
4. **Show.** The dialog shows the QR (`purdex://pair?v=1&relay=<ip:port>&code=<code>`), the code as text, and the
   countdown, and polls `GET /api/host-transfer/pairings/{code}` (§4.3) every 2 s: once claimed it shows 「已配對」 and
   closing the dialog revokes nothing. Closing without having seen a claim (or the countdown running out) calls
   `DELETE /api/host-transfer/pairings/{code}`, and **only a 204** (an unclaimed entry was removed, so no phone can
   claim it any more) is followed by revoking the minted tokens (`DELETE /api/devices?pairing_id=` on each host); a
   409 `claimed` shows 「已配對」, and a 404 or a failed request revokes nothing (§4.3).
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
  use_by INTEGER NOT NULL,         -- created_at + use_within (default 15 min, at most 20): a token first used after this is refused
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
  the WS handlers register each connection with the request's principal — however it authenticated, bearer header
  (what the iOS App uses today) or ticket — so revocation closes it either way.
- `/api/peers*` is unchanged; an empty `Cfg.Token` is unchanged (device tokens add nothing there).

### 3.3 Scope: default-deny allow-list (R7)

- A device principal may reach **only** the patterns in `deviceAllowed` (exact pattern strings as registered); every
  other route answers 403 `device_forbidden`. The check runs after auth: the scope middleware holds the
  `*http.ServeMux` the modules register on (`routes`) and asks `routes.Handler(r)` for the pattern the mux itself would
  dispatch to — the same matching, wildcards and method rules as the dispatch — and the request then goes on to the
  inner handler exactly as today. Without `routes` (nil) every device request is refused. A new route is therefore
  denied to phones until it is added.
- `deviceAllowed` v1 is exactly what the iOS App uses (purdex-ios grepped its sources, 2026-10-09; a test pins the
  set):
  - host: `GET /api/info`, `POST /api/ws-ticket`, `GET /api/hostconfig` (quick-new-tab's projects × commands; it holds
    no token);
  - WS: host events, terminal and mirror (`/ws/terminal/{code}`, `?mirror=1`), conversations
    (`/ws/conversations/claude/{sid}`);
  - sessions: `POST /api/sessions`, `POST /api/sessions/{code}/send-keys` (quick new tab; also chat send, interrupt, the
    AskUserQuestion fallback and the terminal shortcut keys), `GET /api/sessions/{code}/provenance`,
    `GET /api/sessions/{code}/transcript`;
  - conversations: `GET /api/conversations/claude/{sid}` (with its query forms) and `…/subagents/{agent_id}`;
  - team: `GET /api/team/approvals/{id}`, `POST /api/team/approvals/{id}/decide`, `GET` and `PUT /api/team/unattended`,
    `POST /api/relay/self`;
  - nex (reads only): `GET /api/nex/v1/executions`; and on the engine mount `/api/nex/` — one pattern for the whole
    embedded engine — **only** `GET` with a path matching `/api/nex/v1/executions/{id}/prelude` or
    `/api/nex/v1/executions/{id}/events` (the one place the scope looks past the pattern; every other engine request
    from a device → 403);
  - push: `POST`, `GET /api/push/devices`, `DELETE /api/push/devices/{device_id}`;
  - profiles: `GET /api/profiles/{id}`, `GET /api/profiles/{id}/sections/{section}`,
    `PUT /api/profiles/{id}/sections/{section}` (held to §5.2);
  - devices: `PUT /api/devices/self`.

  Left out on purpose: `fs` (the App's old fallback for daemons without a transcript API — a daemon with `devices.v1`
  always has it), `POST /api/host-transfer/redeem` (developer-mode entry, admin token), `GET /api/profiles` (the phone
  uses the profile from the QR), team roster / relay quota (unused today). `GET /api/health` and `/api/peers*` sit on
  the outer mux and never see a device principal.
- **What R7 is and is not.** The allow-list keeps a phone token away from arbitrary-path file access, configuration and
  management routes. It is not a sandbox: creating a session and sending keys is the App's purpose, so a phone token can
  run commands on the host like the person at the Mac. Revoking is how a lost phone is stopped.
- **Ownership inside allowed routes:**
  - push: a registration made with a device token records that device id; a device principal lists and deletes only its
    own registrations (others are invisible / 404). Registering an APNs token that is already registered (by the admin
    token in developer mode, or by an earlier pairing's device) **moves** the registration to the caller — whoever holds
    the APNs token is that phone — so re-pairing never leaves the phone without pushes;
  - profiles: a device principal reads only its `profile_id` (other ids → 404) and writes only there, only by the
    append rule of §5.2; a token with no `profile_id` reads and writes no profile.

### 3.4 Management routes (admin token only)

- `POST /api/devices` `{pairing_id, profile_id?, label, client, use_within_s?}` (`use_within_s` 60–1200, default 900) →
  `{id, token, pairing_id, profile_id, label, created_at, use_by}`.
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

`POST /api/host-transfer/pairings` `{rows: [...], expires_in_s?}` (60–600, default 600) → `{code, expiresAt}`. Every
row must be exactly:

```json
{ "v": 1, "kind": "pair",
  "name": "mlab", "ip": "100.64.0.2", "port": 7860, "daemonId": "<host id>", "look": { },
  "token": "pdxd_…", "deviceId": "d_…", "pairingId": "<uuid>",
  "profile": { "hostDaemonId": "<SOT host id>", "profileId": "p_…", "name": "<profile name>" } }
```

— no other fields (a row with any unknown key, a token not starting `pdxd_`, mixed `pairingId`s or `profile`s, or
`v != 1` → 400). Same limits as transfers (32 rows, 64 KiB, 10 minutes); the 16 live codes are shared with transfers
and count only entries that can still be taken (a claimed entry's status tombstone, §4.3, does not count).

### 4.2 Claim (no bearer)

`POST /api/host-transfer/pairings/claim` `{code}`:
- **Tailnet or loopback source only**, checked by the route itself whatever `IPWhitelist`'s `allow` says (100.64.0.0/10,
  fd7a:115c:a1e0::/48, 127.0.0.0/8, ::1); anything else → 403. Exempt from `TokenAuth` and from the device scope (exact
  path match in `http_chain.go`).
- Takes only pairing entries (a transfer code answers 404 `invalid_code` and is **not** consumed). One-time: the take
  marks the entry claimed and returns the rows; a second claim → 404.
- **Its own failed-attempt limiter, per source address**: 10 failed claims per minute per address; over it → 429, and
  even a right code is then not taken. Redeem's limiter is not touched, so claim traffic (which carries no bearer) can
  never lock out the admin's Share-hosts redeem; and one tailnet node can only lock out itself. (Brute force stays
  hopeless: 40-bit codes, 10 guesses a minute per node.)

### 4.3 Status and delete (admin)

- `GET /api/host-transfer/pairings/{code}` → `{claimed: bool, claimedAt?, expiresAt}`. A claim turns the entry into a
  **status tombstone** (rows dropped) until its original expiry. Tombstones do not count toward the 16 live codes and are
  bounded at 64 (oldest dropped first). Unknown, expired or dropped → 404.
- `DELETE /api/host-transfer/pairings/{code}` is decided under the same lock as claim, and says what happened:
  - **204** — an unclaimed entry was removed; no phone can claim it any more. Only this answer lets the Mac revoke.
  - **409 `claimed`** — the claim came first; nothing removed. The Mac shows 「已配對」.
  - **404** — no such entry (expired, dropped, never was). The Mac revokes nothing: an entry that expired unclaimed
    leaves tokens nobody received, and they die unused at `use_by` (at least 5 minutes after the entry, §2), while
    revoking on a 404 could cut off a phone whose claim the Mac never saw. The dialog then says 「配對碼已失效；手機若已
    完成配對，會出現在「已配對的手機」」 — never "not paired": the paired-phones list (a token's `first_used_at`) is the
    answer that does not depend on a tombstone surviving.

## 5. Profile, one way (R4–R6)

### 5.1 Reading

The phone reads, on the SOT host with its device token, `GET /api/profiles/{id}` (its `profile_id` only), re-reads a
section when the `profile` host event names it, and on every return to the foreground. It keeps the Mac's order. From
`settings` it applies only the keys it understands (appearance). It writes no section except the append of §5.2.

### 5.2 Adding a tab (R5) — the phone appends to its tabs section

The phone writes the one thing it may: the `tabs.<ws>` section of its profile, with the ordinary CAS
`PUT /api/profiles/{id}/sections/tabs.<ws>`, its tab added at the end. The daemon holds every device write to four
gates; failing one of 1–3 → 403 `device_append_only`, gate 4 → 400 `hash_mismatch`; nothing written:

1. **Where.** The token's own `profile_id` (other ids → 404); a section named `tabs.<ws>` that exists and is live (a
   device never creates, recreates or deletes a section; `DELETE …/sections/{section}` is outside its allow-list);
   `clientId` = `c_` + the 12 hex of the device id, so every device write names its device.
2. **Shape unchanged.** `fingerprint` and `ordinal` equal the stored row's.
3. **Append only.** Both payloads are objects with an `order` array of strings and a `tabs` object. The new `order` is
   the stored `order` followed by one or more new ids (no repeats; none already in the stored `order` or `tabs`); the
   new `tabs` holds exactly the stored entries, each deep-equal to its stored value as parsed JSON, plus one JSON object
   per new id; every other top-level member is unchanged.
4. **Hash verified.** `hash` equals the SHA-256 of the payload's canonical form, computed by the daemon with a Go port
   of the SPA's `hash.ts` (object keys sorted at every depth, arrays in order, strings and numbers as `JSON.stringify`
   writes them). A payload the port cannot reproduce exactly (say, a lone surrogate) is refused the same way. This gate
   exists because a Mac fast-forwards, without pulling, a section whose announced hash equals the one it holds (§1): a
   device write with a stale or wrong hash would be invisible to the Macs and later overwritten. Admin writes are not
   checked (unchanged).

Gates 2–4 run inside `PutSection`, on the live row whose `rev` equals `baseRev`, right before the conditional
`UPDATE … WHERE rev = baseRev` — so the row that was checked is the row that is replaced. Everything else is the ordinary
CAS: a stale `baseRev` gets 409 `conflict` with the SOT payload (the phone re-reads and retries, at most 4 times), an
equal hash is `converged`, and an applied write emits the usual `profile` event.

The phone's half (purdex-ios #43): it builds the tab the way the Mac's builder does (a tmux-session tab; host id = the
wire id) — a shared fixture, a tabs payload with a phone-built tab, is pinned on both sides: purdex-ios builds it, and an
SPA test proves the Mac's tabs guard accepts it and applies it (a tab the guard refused would lock the section on every
Mac). It sends the stored fingerprint and ordinal back unchanged, hashes with the Mac's canonical form, and before its
first write checks that its canonical hash of the fetched payload equals the stored hash (otherwise it does not write).

The Macs get the tab by ordinary sync (the `profile` event → pull); no Mac has to be online when the phone writes. A Mac
holding an unpushed change to the same workspace's tabs when the append lands meets the ordinary conflict choice, exactly
as with two Macs (follow-up: settle an append-only SOT side automatically). The phone shows its added tab at once and it
becomes the synced tab when the write is applied (same tab id). SOT host unreachable: the tab stays pending on the
phone, shown at the end of its workspace, and is written on reconnect or return to the foreground. A pending tab whose
`tabs.<ws>` section is gone, or whose workspace is no longer in `workspaces`, is dropped (the Mac removed that
workspace; there is nowhere to append).

### 5.3 Hiding (R6)

Phone-only: closing a synced tab adds its tab id to the phone's hidden set (device-local); hidden synced tabs stay
hidden after later profile updates; "show hidden tabs" lists them and unhides one or all. A tab the phone added is,
once written, a synced tab like any other: closing it hides it (the phone never removes a tab from the profile). One not
written yet is simply dropped.

## 6. Mac App

- Hosts page: **配對手機** beside Share hosts → the pairing dialog (§2 steps 1–4): profile and relay pickers, included /
  left-out hosts, the QR, the code, the countdown, 「已配對」 once claimed; closing without a seen claim deletes the
  entry and revokes only on a 204 (§4.3).
- Hosts page: **已配對的手機** — one row per `pairing_id` across hosts (label, hosts it reaches, last used), **撤銷**
  revokes on every host; an unreachable host shows "not revoked yet" and is retried when it connects.
- Nothing new for the phone's tabs: they arrive by ordinary profile sync (§5.2).
- QR rendering by a small local library (SVG; nothing leaves the machine).

## 7. iOS (contract only)

- **Scan** in the App (AVFoundation; the phone must be on the tailnet — the App says so when the relay is unreachable);
  parse `purdex://pair?v=1&relay=&code=` (other versions → 「請更新 App」); claim over `http://<relay>`.
- **Verify** each row: `GET /api/info` with its token.
  - `host_id` equals `daemonId` → add.
  - `host_id` missing or different → **failed**, not added.
  - Unreachable (network error) → **unverified**: kept and retried for 5 minutes after the claim (every token's
    `use_by` is at least that far off, §2); still unreachable then → 「請在桌機重新配對」. A token first used after its
    `use_by` never works.
- **Replace, don't merge:** a row whose daemon id the phone already has replaces that host entirely (address, token, look,
  name — the phone does no host management, R3), keeping the phone's local record id so the profile's master reference,
  the push registration and the hidden tabs stay attached; two rows with one daemon id → the first wins, the rest are
  reported.
- **clientId** for §5.2 = `c_` + the 12 hex of the SOT row's `deviceId` (kept per host record); a developer-mode host
  (admin token, no device id) keeps a random one.
- **Label**: `PUT /api/devices/self` with the model name (「iPhone 8」) by default; the person may rename it on the phone.
- **Revoked** (401 on a device token): stop reconnecting to that host and mark it 「已撤銷」; a revoked host — and only a
  revoked one — offers 「移除」 (removing a dead entry is not host management).
- Read and follow the profile; append added tabs under §5.2 (canonical-hash self-check first, 409 → re-read and retry at
  most 4 times; pending tabs as in §5.2); hidden tabs; re-register push with the device tokens. The developer-mode
  manual entry stays.

## 8. Delivery plan

1. **QP-1 daemon: devices module + scope** — store, first-use rule, auth, ticket principal, WS connection registry,
   default-deny allow-list via `ServeMux.Handler`, ownership rules (push, profiles), the device append rule with the Go
   canonical hash (§5.2), management routes. (Split: QP-1a store+auth+routes / QP-1b tickets+WS registry+scope+ownership
   / QP-1c append+hash.)
2. **QP-2 daemon: pairing entries** — create / claim / status / delete with tailnet enforcement, row validation, the
   per-source claim limiter, tombstones outside the live cap, delete outcomes. (~350)
3. **QP-3 SPA: pairing dialog + paired phones.** (~600)
4. iOS in parallel against §2, §4, §5, §7.

## 9. Acceptance

- Unit: first-use rule (used / unused past `use_by` / revoked), tickets return principals, every allowed pattern is
  registered and nothing else is reachable with a device token (pinned set; the nex engine mount answers only the two
  read paths), push and profile ownership (a re-registered APNs token moves to the caller), WS principals by bearer and
  by ticket, the append rule (each gate refused, a stale or wrong hash → 400, the Go canonical form equal to `hash.ts`
  on shared fixtures, a stale `baseRev` → 409, a Mac write racing the device write), `use_within_s` and
  `expires_in_s` bounds, the phone-built tab fixture accepted by the Mac's guard, claim refuses transfer codes
  without consuming them, claim enforces tailnet with an empty `allow`, the claim limiter is per source and leaves
  redeem alone, pairing rows strictly validated, status after claim, tombstones outside the live cap, delete outcomes
  (204 / 409 / 404, claim and delete racing), revoke closes every WS kind.
- Real devices: 配對手機 for mlab + a26 → scan on the iPhone 8 → both hosts with their colors, the profile's workspaces and
  tabs; the dialog shows 「已配對」; a tab added on the phone appears at the end of the same workspace on the Mac (also
  when the Mac was offline at the time); closing a
  synced tab on the phone hides it there only and "show hidden tabs" brings it back; 撤銷 → the phone gets 401 on both
  hosts (and its open views close) while the Mac keeps working.
