# Phone pairing by QR code — plan

Spec: `docs/specs/2026-10-09-qr-pairing-spec.md`. Four PRs; the iOS work runs in parallel in purdex-ios.

Rules for every task: TDD (failing test first), one commit per task, mutation check before release, only the affected
packages (`go test -race ./internal/module/devices/...` etc.; vitest on the touched files), a full `go test ./...` /
`vitest run --maxWorkers=3` only before merge with the slot from purdex-1f. **Secrets:** tests generate their own
tokens; no admin token, device token or hash is printed in tests, logs, PR text or messages; logs show a device id, never
a token. Review per PR: codex R1 + R2 (attack → critic).

## QP-1 daemon: devices module

1. **Token + store.** `internal/devices`: `NewToken()` (`pdxd_` + 32 hex), `Hash(token)` (SHA-256 hex). Module
   `internal/module/devices`: `devices.db` (0600; the dir pattern of `profiles.db`), the table of spec §3.1, `Mint`,
   `List`, `RevokeID`, `RevokePairing`, `SetLabel`, `Lookup(hash)`, `TouchUsed` (first / last used, last at most once a
   minute per id, in memory then flushed). Tests: format; hash stability; mint returns the token once and stores only the
   hash; lookup of revoked → miss; touch throttling.
2. **Unused expiry.** A sweep every minute revokes tokens with `first_used_at == 0` older than 15 minutes. Tests with a
   fake clock.
3. **Auth.** `internal/middleware` `TokenAuth` gains a device lookup (`func(hash string) (Principal, bool)`), tried only
   when the bearer is not the admin token and starts with `pdxd_`; the principal goes into the request context. Empty
   `Cfg.Token` behaviour unchanged. WS tickets minted by a device principal carry it. Tests: admin ok; device ok; revoked
   → 401; unknown `pdxd_` → 401; empty admin token unchanged; ticket carries the principal.
4. **Scope.** `deviceRoutes` table (method + pattern → allowed / denied) and a middleware after auth: device principal on
   a denied route → 403 `device_forbidden`. **Exhaustive test:** build the real mux the way `cmd/pdx` does (with every
   module registered), list every registered pattern, and fail on any pattern missing from the table. Tests for the
   explicitly denied ones (fs list / read / write, `PUT /api/config`, `/api/dev/*`, restart, section PUT / DELETE, device
   management) and a sample of the allowed ones.
5. **Routes.** `POST /api/devices`, `GET /api/devices`, `DELETE /api/devices/{id}`, `DELETE /api/devices?pairing_id=`,
   `PUT /api/devices/self`; capability `devices.v1`. Revoke closes the principal's open WebSockets (the host-events and
   terminal hubs gain a "close where principal = id" call). Tests: management requires admin (device → 403); self only
   for its own id; revoke closes a WS; list never contains a token or hash.
6. **Registration.** `cmd/pdx/main.go` registers the module unconditionally (it does nothing until a token is minted).

## QP-2 daemon: claim + tab requests

1. **Claim.** `internal/module/hosttransfer`: `POST /api/host-transfer/claim` `{code}` without a bearer — exempt from
   `TokenAuth` and from the device scope in `cmd/pdx/http_chain.go` (exact path match), still behind the IP whitelist;
   shares the store's one-time take and the failed-attempt limiter with redeem; answers only payloads whose every row has
   `kind == "pair"` (otherwise 404 `invalid_code`, and the payload stays claimable by redeem until it expires). Tests:
   happy path; second claim → 404; a share-hosts payload → 404 and still redeemable; failed attempts count toward the
   shared limit; a bearer is ignored.
2. **Tab requests.** `internal/module/profiles`: table `tab_requests(profile_id, request_id, workspace_id, host_id,
   session_code, session_name, created_at, PRIMARY KEY(profile_id, request_id))`; `POST
   /api/profiles/{id}/tab-requests` (device or admin; idempotent; ≤ 200 open per profile → 429; validation of ids and
   lengths), `GET /api/profiles/{id}/tab-requests` and `DELETE /api/profiles/{id}/tab-requests/{request_id}` (admin);
   7-day expiry in the existing sweep or a new one; host event `profile.tab_request` `{profile_id, request_id}`. Add the
   three routes to `deviceRoutes` (POST allowed, GET / DELETE denied). Tests: idempotent post; cap; expiry; event; device
   cannot list or delete.

## QP-3 SPA: pairing dialog + paired phones

1. **QR.** Add a QR generator dependency (SVG output, no network); a `QrCode` component. Test: renders an SVG for a
   string; nothing is fetched.
2. **Mint + package.** `spa/src/lib/pairing.ts`: for each host with a token, `POST /api/devices` (pairing id shared);
   build pair rows (spec §4.1) with the profile pointer; upload through the existing host-transfer create; on any later
   failure or on cancel, revoke what was minted (`DELETE ?pairing_id=` on each host). Tests: rows shape; a host failing to
   mint is left out and reported; cancel revokes on every host that minted; no token is logged.
3. **Dialog.** `PairPhoneDialog.tsx` from the Hosts page (beside Share hosts): profile picker (default master
   profile), relay picker (default active host), included / left-out hosts, QR + code + countdown, close-before-scan
   revokes. i18n. Tests: defaults; close revokes; expiry shows "expired" and revokes.
4. **Paired phones.** A section on the Hosts page: rows by `pairing_id` across hosts (`GET /api/devices` on each), label,
   hosts, last used; 撤銷 → `DELETE ?pairing_id=` on each; an unreachable host shows "not revoked yet" and is retried
   when it connects (a small pending-revocations list, device-local). Tests: grouping; revoke; retry on reconnect.
5. **Screenshot gate:** the dialog with a QR and the paired-phones list, zh-TW, to the lead.

## QP-4 SPA: tab-request consumer

1. On connecting to the SOT host of the attached profile and on `profile.tab_request`, `GET` open requests; for each:
   skip-and-delete when that workspace already has a tab for that host + session; else append a tmux-session tab at the
   end of the workspace (first workspace if it is gone) through the normal profile write path, then `DELETE` the
   request. One consumer per window is enough (idempotent by request id; a request already deleted by another window is
   a 404 to ignore). Tests: append at end; duplicate skipped; missing workspace → first; two windows apply once.

## Follow-ups

- The phone's settings keys it understands (appearance) are listed in purdex-ios, not here.
- Profile switching on the phone after pairing (a new QR picks another profile) — v1 is "scan again".
