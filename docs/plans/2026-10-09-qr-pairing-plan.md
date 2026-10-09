# Phone pairing by QR code — plan

Spec: `docs/specs/2026-10-09-qr-pairing-spec.md`. Four PRs (QP-1 may split into 1a / 1b); the iOS work runs in
parallel in purdex-ios.

Rules for every task: TDD (failing test first), one commit per task, mutation check before release, only the affected
packages, a full `go test ./...` / `vitest run --maxWorkers=3` only before merge with the slot from purdex-1f.
**Secrets:** tests generate their own tokens; no admin token, device token or hash is printed in tests, logs, PR text or
messages; logs show a device id, never a token. Review per PR: codex R1 + R2 (attack → critic).

## QP-1 daemon: devices module + scope

1. **Token + store.** `internal/devices`: `NewToken()` (`pdxd_` + 32 hex), `Hash`. Module `internal/module/devices`:
   `devices.db` (0600), the table of spec §3.1 with `profile_id` and `use_by`; `Mint`, `List`, `RevokeID`,
   `RevokePairing`, `SetLabel`, `Authenticate(hash, now)` = lookup + the single-statement first-use rule +
   `last_used_at` throttled to once a minute; a sweep deleting rows that can no longer work. Tests: format; only the hash
   stored; first use before `use_by` succeeds and sticks across a restart (it is in the DB); unused past `use_by` →
   refused immediately (no sweep needed); revoked → refused; concurrent first uses both succeed once; throttled
   `last_used_at`.
2. **Auth + principal.** `TokenAuth` gains a device authenticator for `pdxd_` bearers; the principal
   `{id, pairing_id, profile_id}` goes into the request context. Empty `Cfg.Token` unchanged. Tests: admin; device;
   revoked; unknown; past `use_by`; empty admin token.
3. **Tickets carry principals.** `internal/core/ticket.go`: `Generate(principal)` records it; the validator contract
   becomes `Validate(ticket) (Principal, bool)` (validate-and-consume, atomic); `TokenAuth`'s ticket path writes the
   principal into the context; `POST /api/ws-ticket` passes the caller's principal. Tests: an admin ticket and a device
   ticket each come back with their principal; a ticket is consumed once.
4. **Connection registry.** A small registry (`internal/core` or `internal/devices`) where every WS handler registers
   `(principal id, close func)` for the life of the connection: host-events, terminal / mirror, conversations. Revoking a
   device closes all of its connections. Tests: revoke closes one connection of each kind; admin connections untouched.
5. **Scope.** `deviceAllowed` = the exact pattern strings of spec §3.3 (the iOS list, confirmed by purdex-ios — including
   whether session create / send-keys are in). A middleware on the inner mux: for a device principal, `pattern :=
   mux.Handler(r)` and allow only `deviceAllowed[pattern]`; otherwise 403 `device_forbidden`. Tests: **every allowed
   pattern is really registered** (for each, a synthetic request whose `mux.Handler` pattern equals it, built with the
   real modules in the nex on/off and push ready/soft-failed/off configurations); **the allowed set equals the pinned
   iOS list**; a device token on `fs` list/read/write, `PUT /api/config`, `/api/dev/*`, restart, section PUT / DELETE,
   device management, a team mutation not in the list → 403; methodless WS patterns handled (the pattern string as
   registered).
6. **Ownership.** push: registrations made by a device principal store its device id; device principals see / delete
   only their own (others 404). profiles: a device principal reads only its `profile_id` (else 404) and posts tab
   requests only there (QP-2). Tests for both, cross-device.
7. **Management routes.** `POST /api/devices` (with `profile_id?`), `GET /api/devices`, `DELETE /api/devices/{id}`,
   `DELETE /api/devices?pairing_id=`, `PUT /api/devices/self`; capability `devices.v1`. Tests: admin only (device → 403);
   self only for its own id; list never shows token or hash; revoke → connections closed (task 4).
8. **Registration.** `cmd/pdx/main.go` registers the module unconditionally.

If the PR runs over ~800 lines: QP-1a = tasks 1, 2, 7, 8; QP-1b = tasks 3–6.

## QP-2 daemon: pairing entries + tab requests

1. **Pairing entries.** `internal/module/hosttransfer`: a pairing kind in the store (separate from transfers; shared
   live-code limit); `POST /api/host-transfer/pairings` (admin) with strict row validation (spec §4.1: exact keys, `v ==
   1`, `kind == "pair"`, `pdxd_` token, one `pairingId` and one `profile` across rows); `POST
   /api/host-transfer/pairings/claim` (no bearer; the route enforces tailnet / loopback source itself; exempt from
   `TokenAuth` and the device scope by exact path in `http_chain.go`; takes only pairing entries, marks claimed, returns
   rows; failed attempts share the redeem limiter); `GET` / `DELETE /api/host-transfer/pairings/{code}` (admin). Tests:
   strict validation (unknown key, wrong prefix, mixed pairing ids, v 2 → 400); claim happy path; second claim 404;
   **a transfer code → 404 and still redeemable**; **claim from a non-tailnet source with `allow = []` → 403**; status
   before / after claim; delete unclaimed; limiter shared.
2. **Tab requests.** `internal/module/profiles`: table `tab_requests(profile_id, request_id, workspace_id, host_id,
   session_code, session_name, created_at, leased_to, lease_until, PRIMARY KEY(profile_id, request_id))`; `POST
   /api/profiles/{id}/tab-requests` (device for its own profile, or admin; idempotent; ≤ 200 open → 429; validation),
   `POST /api/profiles/{id}/tab-requests/claim {client_id}` (admin; only an attached client; atomic lease of the
   unleased / expired requests for 60 s), `DELETE /api/profiles/{id}/tab-requests/{request_id}` (admin); 7-day expiry;
   host event `profile.tab_request`. Add POST to `deviceAllowed`. Tests: idempotent post; cap; expiry; two claimants →
   disjoint sets; an expired lease is claimable again; a non-attached client cannot claim; event; device cannot claim or
   delete.

## QP-3 SPA: pairing dialog + paired phones

1. **QR.** A QR generator dependency (SVG, no network); `QrCode` component. Test: SVG for a string; nothing fetched.
2. **Mint + package.** `spa/src/lib/pairing.ts`: mint on every host with a token (pairing id shared; `profile_id` on the
   SOT host); **abort and revoke all when the SOT host fails**; build rows; create the pairing entry; poll status every
   2 s; on close before claim / expiry: delete the entry and revoke on every host that minted. Tests: rows exact shape;
   SOT failure aborts and revokes; another host failing is left out and reported; closed after claim revokes nothing;
   closed before claim deletes + revokes; no token in logs.
3. **Dialog.** `PairPhoneDialog.tsx` from the Hosts page: profile picker (default master profile), relay picker
   (default active host), included / left-out hosts, QR + code + countdown, 「已配對」. i18n. Tests: defaults; states.
4. **Paired phones.** A Hosts-page section: rows by `pairing_id` across hosts; 撤銷 on every host; unreachable host →
   "not revoked yet", retried on connect (pending revocations, device-local). Tests: grouping; revoke; retry.
5. **Screenshot gate:** the dialog with a QR and 「已配對」, and the paired-phones list, zh-TW, to the lead.

## QP-4 SPA: tab-request consumer

1. **Event wiring.** `spa/src/lib/profile/profile-ws-dispatch.ts`: parse `profile.tab_request` and route it to the
   consumer (today the dispatcher drops unknown shapes and has one listener). Tests: parsed and routed; malformed dropped.
2. **Consumer.** Runs only in the profile leader window while profile sync is running (the gates of `start.ts`); stops
   on losing the lease, on detach, and on switching master. Claims on start, on the event, every 60 s. For each claimed
   request: `readMasterWorld()`; skip-and-delete when the workspace already has a tab for host + session; else append at
   the end of that workspace (gone → the Mac's usual fallback), `writeMasterWorld`; `'unsettled'` → leave it for the
   lease to run out; after the write is confirmed, `DELETE`. Tests: append at end with the master shown and with it
   parked; duplicate skipped; workspace gone → fallback; zero workspaces → fallback; `'unsettled'` leaves the request;
   two windows: only the leader acts; losing the lease stops it; offline → reconnect resumes.

## Follow-ups

- The settings keys the phone applies (appearance) are listed in purdex-ios.
- Switching the phone to another profile = scan a new QR (v1).

## Plan review fold-in (codex `task-mv0shz3e-31lcty`, 2026-10-09)

| # | Finding | Fold-in |
|---|---------|---------|
| 1 | critical: claim cannot leave a non-pair payload unconsumed | Separate pairing entries; claim takes only them (spec §4) |
| 2 | critical: empty `allow` = open to all | Claim enforces tailnet / loopback itself (spec §4.2, QP-2 test) |
| 3 | critical: cannot tell close-before-scan | Status route + 2 s poll; delete entry on early close (spec §2, §4.3) |
| 4 | critical: two windows apply twice | Daemon claim lease + leader-only consumer + "already has the tab" check |
| 5 | critical: two Macs apply twice | Same lease across clients; only attached clients may claim |
| 6 | important: `POST /api/ws-ticket` missing | Allowed (spec §3.3) |
| 7 | important: conversation WS not closed on revoke | One connection registry for every WS kind (QP-1 task 4) |
| 8 | important: ticket principal plumbing | QP-1 task 3 |
| 9 | important: ServeMux cannot list routes | Default-deny via `mux.Handler(r)`; test that each allowed pattern is registered |
| 10 | important: conditional modules | Configurations nex on/off, push ready/soft-failed/off in the test |
| 11 | important: outer-mux routes | Stated: health and peers never see a device principal |
| 12 | important: methodless WS patterns | Pattern string as `mux.Handler` returns it |
| 13 | important: misclassification not caught | Allowed set pinned to the iOS list; iOS confirms create / send-keys |
| 14 | important: push routes too broad | Ownership: a device sees / deletes only its own registrations |
| 15 | important: token not bound to the profile | `profile_id` on the token; reads / requests only for it |
| 16 | important: unused-expiry races | `use_by` checked at lookup; first use in one statement; sweep only deletes dead rows |
| 17 | important: constant-time claim | Dropped: lookup by hash of a 128-bit random token |
| 18 | important: `kind == "pair"` is not enough | Strict row schema at create |
| 19 | important: SPA event wiring | QP-4 task 1 |
| 20 | important: parked master | `readMasterWorld` / `writeMasterWorld`, `'unsettled'` handling |
| 21 | important: leader / attachment lifecycle | Consumer gated like profile sync; stops on lease loss / detach / switch |
| 22 | important: SOT mint failure | Abort and revoke |
| 23 | important: zero workspaces | The Mac's usual fallback (active, first, Unsorted) |
| 24 | important: host replacement rules | Spec §7: daemon-id match replaces entirely; duplicates: first wins |
