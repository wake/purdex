# Phone pairing by QR code (hosts + profile) — spec

Date: 2026-10-09 · Owner: interface line lead (`mlab/purdex-88-b8`) · Status: draft for plan review

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
| R7 | A phone token cannot use arbitrary-path file access (and the other routes in §3.3). | purdex-ios brief §4a (user, 2026-10-07) |

Facts this spec stands on (main @ 2026-10-09, measured):
- The daemon knows **one** token for its general routes, `Cfg.Token` (`cmd/pdx/http_chain.go`, `internal/middleware/middleware.go`); a peer's `pdxp_` token is accepted only on `/api/peers*` (`internal/middleware/peer_auth.go`, policy `internal/module/peers/policy.go`). There is no per-device token, no scope, no revocation on the general routes.
- Host transfer (`internal/module/hosttransfer`): the Mac uploads opaque host rows to a relay daemon (`POST /api/host-transfer`, admin token), gets an 8-char Crockford code (40 bits, 10 min, one-time, 16 live codes, 10 failed redeems per minute per daemon), and the receiver redeems it (`POST /api/host-transfer/redeem`, **admin token of the relay**). The rows carry the sharer's admin tokens (`spa/src/lib/host-transfer-plan.ts`). Kept in memory only.
- Profiles (`internal/module/profiles`): sections `workspaces`, `tabs.<ws>`, `settings` on the SOT (dev host) daemon; writes are whole-section CAS with `clientId`, `baseRev`, `hash`, `fingerprint`, `ordinal` (`handler_sections.go`); a change emits the `profile` host event. A wrong fingerprint / ordinal answers a schema 409 or locks other clients out.
- No QR library in `spa/package.json`; the Share hosts dialog (`spa/src/components/hosts/ShareHostsDialog.tsx`) shows the code as text.

## 2. Flow

1. **Mac, "配對手機"** (Hosts page): the person picks the profile (default: this Mac's master profile) and the relay
   (default: the active host, as Share hosts does).
2. **Mint.** For every host that has a token on this Mac, the Mac calls `POST /api/devices` on that host with its admin
   token → a fresh device token for the phone (§3), all sharing one `pairing_id` (a UUID the Mac makes) and label
   「手機（配對中）」. A host that fails is left out and listed in the dialog; zero hosts → nothing is shown.
3. **Package.** The Mac uploads to the relay, through the existing `POST /api/host-transfer`, a payload of **pair rows**
   (§4.1) — one per host: name, address, daemon id, look, the **device token**, plus a `profile` pointer — and gets a
   code.
4. **Show.** The dialog shows the QR (`purdex://pair?v=1&relay=<ip:port>&code=<code>`), the code as text under it (for
   typing), and the 10-minute countdown. Closing the dialog before a scan revokes the minted tokens (§3.4).
5. **Phone, scan.** The phone parses the QR and calls the relay's **`POST /api/host-transfer/claim`** `{code}` — no
   token: the code is the credential (§4.2). It gets the pair rows.
6. **Verify and add.** For each row the phone calls `GET /api/info` on that host with the row's device token, keeps the
   rows whose `host_id` matches `daemon_id`, adds them (replacing a host it already had with the same daemon id), and
   sets its own name on each: `PUT /api/devices/self {label: "<device name>"}`. Rows that fail are shown, not added.
7. **Profile.** From the row whose daemon id is the profile's SOT, the phone reads the profile (§5) and keeps following
   it.
8. **Push.** The phone re-registers for push (push spec §4) on every host that announces `push.v1`, now with the device
   token.

## 3. Device tokens (daemon)

### 3.1 Store

New module `devices`, `devices.db` in the data dir (owner-only permissions), one table:

```sql
CREATE TABLE IF NOT EXISTS device_tokens (
  id TEXT PRIMARY KEY,            -- "d_" + 12 hex
  pairing_id TEXT NOT NULL,       -- the Mac's pairing UUID, the same on every host of one pairing
  label TEXT NOT NULL,
  token_hash TEXT NOT NULL UNIQUE,-- SHA-256 hex of the token; the token itself is never stored
  created_at INTEGER NOT NULL,
  created_by TEXT NOT NULL,       -- client label of the Mac that minted it
  first_used_at INTEGER NOT NULL DEFAULT 0,
  last_used_at INTEGER NOT NULL DEFAULT 0,
  revoked_at INTEGER NOT NULL DEFAULT 0
);
```

- Token format `pdxd_` + 32 hex (16 bytes, `crypto/rand`). Returned **once**, by `POST /api/devices`.
- **Unused expiry:** a token never used within 15 minutes of its creation is revoked by a sweep (a QR that was never
  scanned leaves nothing behind).
- `last_used_at` is written at most once a minute per token (no write per request).

### 3.2 Authentication

- The general chain's `TokenAuth` accepts the admin token as today, **or** a live (not revoked) device token, matched
  by hashing the bearer and looking the hash up (constant-time compare of the hash). The request then carries a
  device principal (id, pairing id).
- WebSocket tickets: a ticket minted by a device principal carries the same principal.
- `/api/peers*` is unchanged (device tokens are not peer tokens).
- An empty `Cfg.Token` (today: everything allowed) is unchanged; device tokens add nothing there.

### 3.3 Scope (R7)

Every route the daemon registers is classified in one table, `deviceRoutes`, as **allowed** or **denied** for a device
principal; a device request to a denied route answers 403 `device_forbidden`. A test enumerates every registered route
(method + pattern) and fails when one is not in the table, so a new route is never open to phones by default.

Allowed in v1 (what the iOS App uses): `GET /api/info`, `GET /api/health`; sessions list and per-session reads and
terminal / mirror WS; host events WS; conversations (`/api/conversations/*`, `/ws/conversations`); team approvals list /
get / decide, `GET /api/team/roster`, `GET /api/team`, unattended `GET`/`PUT`, `PUT /api/team/relay-quota`; push
(`/api/push/*`); profiles **read** (`GET /api/profiles*`) and `POST /api/profiles/{id}/tab-requests` (§5.2);
`PUT /api/devices/self`; session create / send-keys if the phone's quick-new-tab uses them today.

Denied: everything else, explicitly including the `fs` module (arbitrary paths), `PUT /api/config`, `/api/dev/*`,
daemon restart, peers host management, host-transfer **create**, device-token management (`/api/devices` other than
`self`), profile section writes and deletes, hostconfig writes other than those listed.

### 3.4 Management routes (admin token only)

- `POST /api/devices` `{pairing_id, label, client}` → `{id, token, pairing_id, label, created_at}`.
- `GET /api/devices` → rows without the token or its hash (`id`, `pairing_id`, `label`, `created_at`, `created_by`,
  `first_used_at`, `last_used_at`, `revoked_at`).
- `DELETE /api/devices/{id}` and `DELETE /api/devices?pairing_id=<uuid>` → revoke (idempotent, 204). A revoked token
  stops working on its next request; open WebSockets of that principal are closed.
- `PUT /api/devices/self` `{label}` (device token) → sets its own label (1–64 printable runes).
- Capability `devices.v1`.

## 4. Pairing transport (daemon)

### 4.1 Pair rows

The host-transfer payload stays opaque to the daemon. The Mac writes rows of this shape (the iOS parser accepts only
`v: 1`):

```json
{ "v": 1, "kind": "pair",
  "name": "mlab", "ip": "100.64.0.2", "port": 7860, "daemonId": "<host id>", "look": { },
  "token": "pdxd_…", "deviceId": "d_…", "pairingId": "<uuid>",
  "profile": { "hostDaemonId": "<SOT host id>", "profileId": "p_…", "name": "<profile name>" } }
```

`profile` is the same on every row of one pairing.

### 4.2 Claim

- `POST /api/host-transfer/claim` `{code}` — **no bearer**: the code is the credential. Same store, same one-time take,
  same 10 failed attempts per minute per daemon (shared with redeem), same answers (`invalid_code` 404, `rate_limited`
  429). It answers only payloads whose rows are all `kind: "pair"` (a Share-hosts payload with admin tokens is never
  handed out without the relay's admin token: 404 `invalid_code`).
- The general chain's IP whitelist still applies (tailnet only, R1). The route is exempt from `TokenAuth` and from the
  device scope (it has no principal).
- `redeem` (admin) is unchanged.

## 5. Profile, one way (R4–R6)

### 5.1 Reading

The phone reads, on the SOT host with its device token, `GET /api/profiles/{id}` (sections `workspaces`, `tabs.<ws>`,
`settings`), re-reads a section when the `profile` host event names it, and on every return to the foreground. It keeps
the Mac's order. From `settings` it applies only the keys it understands (appearance); the rest is ignored. It never
writes a section.

### 5.2 Adding a tab (R5) — a request the Mac applies

The phone does not write profile sections (it would have to reproduce the Mac's hash, fingerprint and ordinal; a
mistake locks other clients out). Instead:

- `POST /api/profiles/{id}/tab-requests` `{request_id, workspace_id, host_id, session_code, session_name}` (device
  token): stored in a small table on the SOT daemon (idempotent by `request_id`, at most 200 open per profile,
  dropped after 7 days unapplied); emits the `profile.tab_request` host event.
- The Mac App attached to that profile takes open requests (on connect and on the event), appends a tab for that
  session **at the end of that workspace** through its normal profile write, then `DELETE`s the request (admin). If the
  workspace no longer exists, it uses the first workspace. A request for a session the Mac already shows as a tab in
  that workspace is just deleted.
- The phone shows its new tab at once, locally; when the Mac's profile write arrives it becomes the synced tab (matched
  by host id + session code).
- No Mac online: the request waits; nothing is lost.

### 5.3 Hiding (R6)

Phone-only: closing a synced tab adds its tab id to the phone's hidden set (device-local); synced tabs in the set are
not shown, also after later profile updates; "show hidden tabs" lists them and unhides one or all. A tab the phone added
and the Mac has not applied yet is simply removed (and its request deleted if still open).

## 6. Mac App

- Hosts page: **配對手機** next to Share hosts → the pairing dialog (§2 steps 1–4): profile and relay pickers, the list
  of hosts that will be included (and those left out, with why), the QR, the code, the countdown; closing early revokes.
- Hosts page: **已配對的手機** — one row per `pairing_id` across hosts (label, hosts it reaches, last used), **撤銷**
  revokes it on every host (`DELETE ?pairing_id=`); a host that is not reachable is shown as "not revoked yet" and
  retried when it connects.
- The tab-request consumer (§5.2).
- QR rendering: a small QR library (generates an SVG locally; nothing leaves the machine).

## 7. iOS (contract only)

Scan; parse `purdex://pair?v=1&relay=&code=`; claim; verify each row with `/api/info`; add hosts, set label; read and
follow the profile; tab requests for added tabs; hidden tabs; re-register push with the device tokens. The hidden
developer-mode manual entry stays for development.

## 8. Delivery plan

1. **QP-1 daemon: devices module** — store, auth integration, route classification table + exhaustive test, management
   routes, `self`, unused-expiry sweep, ticket principal, WS close on revoke. (~750 lines incl. tests)
2. **QP-2 daemon: claim + tab requests** — `POST /api/host-transfer/claim`, the tab-request table, routes and event.
   (~450)
3. **QP-3 SPA: pairing dialog + paired phones list.** (~600)
4. **QP-4 SPA: tab-request consumer.** (~300)
5. iOS in parallel against §2, §4.1, §5, §7.

## 9. Acceptance

- Unit: token hashing and lookup; revoked / unused-expired tokens refused; every registered route classified; a device
  token on `fs` / `PUT /api/config` / section PUT → 403; claim refuses a non-pair payload; claim is one-time and
  rate-limited; tab requests idempotent and capped.
- Real devices: on the Mac, 配對手機 for mlab + a26 → scan on the iPhone 8 → both hosts appear with their colors, the
  profile's workspaces and tabs appear; the phone's tokens are listed under 已配對的手機; a tab added on the phone
  appears at the end of the same workspace on the Mac; closing a synced tab on the phone hides it there only and
  "show hidden tabs" brings it back; 撤銷 on the Mac → the phone gets 401 on both hosts while the Mac keeps working.
