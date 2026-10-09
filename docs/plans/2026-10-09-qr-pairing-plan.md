# Phone pairing by QR code — plan

Spec: `docs/specs/2026-10-09-qr-pairing-spec.md`. Three steps (QP-1 splits into 1a / 1b / 1c); the iOS work runs in
parallel in purdex-ios.

Rules for every task: TDD (failing test first), one commit per task, mutation check before release, only the affected
packages, a full `go test ./...` / `vitest run --maxWorkers=3` only before merge with the slot from purdex-1f.
**Secrets:** tests generate their own tokens; no admin token, device token or hash is printed in tests, logs, PR text or
messages; logs show a device id, never a token. Review per PR: codex R1 + R2 (attack → critic).

## QP-1 daemon: devices module + scope

1. **Token + store.** `internal/devices`: `NewToken()` (`pdxd_` + 32 hex), `Hash`. Module `internal/module/devices`:
   `devices.db` (0600), the table of spec §3.1 with `profile_id` and `use_by` (= created_at + `use_within`, default 15
   min, 60 s–20 min); `Mint`, `List`, `RevokeID`,
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
   device closes all of its connections; the principal comes from the request context, so a bearer-authenticated WS
   registers like a ticket one. Tests: revoke closes one connection of each kind, opened by bearer and by ticket; admin
   connections untouched.
5. **Scope.** `deviceAllowed` = the exact pattern strings of spec §3.3 (the iOS list, purdex-ios 2026-10-09), plus the
   one sub-rule for the nex engine mount `/api/nex/`: allowed only for `GET` with a path matching
   `^/api/nex/v1/executions/[^/]+/(prelude|events)$`. `newOuterHandler(c, routes *http.ServeMux, inner http.Handler, allow)`:
   `main.go` passes its mux as both; the existing tests pass `nil` routes and their wrappers as `inner`. The scope
   middleware sits after `TokenAuth` in the general chain: for a device principal, `_, pattern := routes.Handler(r)`
   (nil `routes` → refuse), allow only `deviceAllowed[pattern]`, otherwise 403 `device_forbidden`; then
   `inner.ServeHTTP` as today. Admin requests never consult `routes`. Tests: **every allowed
   pattern is really registered** (for each, a synthetic request whose `mux.Handler` pattern equals it, built with the
   real modules in the nex on/off and push ready/soft-failed/off configurations); **the allowed set equals the pinned
   iOS list**; a device token on `fs` list/read/write, `PUT /api/config`, `/api/dev/*`, restart, section DELETE,
   device management, `GET /api/profiles`, a team mutation not in the list → 403; the nex mount: the two read paths
   pass, a `POST` to an engine route and any other engine `GET` → 403 (nex on), and with nex off the mount's
   unavailable handler is never reached by a device; methodless WS patterns handled (the pattern string as
   registered); nil `routes` refuses a device and leaves admin requests alone; a wrapper `inner` still serves.
6. **Ownership.** push: registrations made by a device principal store its device id; device principals see / delete
   only their own (others 404); registering an APNs token already registered by the admin or another device moves it
   to the caller. profiles: a device principal reads and writes only its `profile_id` (else 404); a token with no
   `profile_id` reaches no profile. Tests for both, cross-device, including the move.
7. **Device tab append (spec §5.2).** `internal/module/profiles`: `handlePutSection` refuses a device principal before
   the store when the section is not `tabs.<ws>` or `clientId` ≠ `c_` + the device id's 12 hex (403
   `device_append_only`); `PutSection` gains an optional guard `func(row sectionRow, in Section) error` called only on
   the live-row path, after the schema gate and the `row.Rev == baseRev` check, right before the conditional `UPDATE`;
   the `!found` and tombstone paths refuse when a guard is set. The device guard: fingerprint and ordinal equal the row's;
   the append rule on the two payloads (decode with `json.Unmarshal` into `any`; `order` a strict prefix extension with
   fresh, non-repeating ids; stored `tabs` entries `reflect.DeepEqual`; exactly one object per new id; other top-level
   members equal; then gate 4: `hash` equals `profilehash.Sum(payload)`, else 400 `hash_mismatch`). Admin writes pass
   no guard and are unchanged. Tests: append one / two tabs → applied, event sent; each refusal (other profile 404;
   `settings` / `workspaces` section; foreign `clientId`; missing / tombstoned section; changed fingerprint; changed
   ordinal; reordered, removed or edited existing tab; repeated or reused id; non-object new entry; changed extra
   top-level member; the stored row's hash resent; a wrong hash); stale `baseRev` → 409 with payload; a Mac write
   landing between the device's read and write → the device gets 409, nothing appended over it; equal hash →
   converged.
8. **Canonical hash, Go port.** `internal/profilehash`: `Sum(raw json.RawMessage) (string, error)` — the canonical form
   of `spa/src/lib/profile/hash.ts` (keys sorted by UTF-16 code units at every depth, as JS `sort()` does; arrays in
   order; strings escaped exactly as `JSON.stringify`; numbers formatted as ECMAScript `Number#toString`; `-0` → `0`;
   a lone surrogate or anything the port cannot reproduce → error), SHA-256 lowercase hex. Shared fixtures
   `spa/src/lib/profile/__fixtures__/canonical-hash.json` (inputs as JSON text + expected hashes: key order, nested
   arrays, integers, floats, 1e21 / 1e-7 boundaries, unicode, escapes, the `__proto__` key, a real tabs payload):
   a vitest pins `hash.ts` to it and a Go test (reading the same file by relative path) pins the port.
9. **Management routes.** `POST /api/devices` (with `profile_id?`, `use_within_s?` 60–1200), `GET /api/devices`,
   `DELETE /api/devices/{id}`, `DELETE /api/devices?pairing_id=`, `PUT /api/devices/self`; capability `devices.v1`.
   Tests: admin only (device → 403); self only for its own id; list never shows token or hash; `use_within_s` bounds;
   revoke → connections closed (task 4).
10. **Registration.** `cmd/pdx/main.go` registers the module unconditionally.

Split: QP-1a = tasks 1, 2, 9, 10; QP-1b = tasks 3–6; QP-1c = tasks 7–8.

## QP-2 daemon: pairing entries

1. **Pairing entries.** `internal/module/hosttransfer`: a pairing kind in the store (separate from transfers);
   `POST /api/host-transfer/pairings` (admin) with strict row validation (spec §4.1: exact keys, `v == 1`,
   `kind == "pair"`, `pdxd_` token, one `pairingId` and one `profile` across rows) and `expires_in_s` (60–600, default
   600; out of range → 400); `POST
   /api/host-transfer/pairings/claim` (no bearer; the route enforces tailnet / loopback source itself; exempt from
   `TokenAuth` and the device scope by exact path in `http_chain.go`; takes only pairing entries, turns the entry into a
   tombstone, returns rows); `GET` / `DELETE /api/host-transfer/pairings/{code}` (admin). Tests: strict validation
   (unknown key, wrong prefix, mixed pairing ids, v 2 → 400); claim happy path; second claim 404; **a transfer code →
   404 and still redeemable**; **claim from a non-tailnet source with `allow = []` → 403**; status before / after claim.
2. **Claim limiter, per source.** A limiter of its own keyed by the source address (10 failures a minute; over it →
   429, a right code not taken); the transfer limiter stays as it is and claim never touches it. Tests: 10 bad claims
   from one address lock that address only — another address still claims, an admin redeem still works; the window
   resets.
3. **Capacity and tombstones.** The 16-code cap counts only takeable entries (transfers and unclaimed pairings);
   tombstones live until the original expiry, at most 64, oldest dropped first. Tests: 16 claimed pairings → a 17th
   create (pairing or transfer) still succeeds; the 65th tombstone drops the oldest (its status → 404).
4. **Delete outcomes.** `DELETE` under the store lock: unclaimed → removed, 204; tombstone → 409 `claimed`; none →
   404. Tests: each answer; claim and delete racing (run both concurrently many times: exactly one of "claim returned
   rows" / "delete returned 204").

## QP-3 SPA: pairing dialog + paired phones

1. **QR.** A QR generator dependency (SVG, no network); `QrCode` component. Test: SVG for a string; nothing fetched.
2. **Mint + package.** `spa/src/lib/pairing.ts`: deadline D = now + 10 min; mint on every host with a token (pairing id
   shared; `profile_id` on the SOT host; `use_within_s` = (D − now) + 300 at each call); **abort and revoke all when
   the SOT host fails**; build rows; create the pairing entry with `expires_in_s` = D − now (< 60 → abort and revoke);
   poll status every 2 s; on close without a seen claim / at expiry: `DELETE` the entry and revoke on every host that
   minted **only on 204**; 409 → 「已配對」; 404 or a failed request → revoke nothing, and the dialog says
   「配對碼已失效；手機若已完成配對，會出現在「已配對的手機」」. Tests: rows exact shape; the durations (a slow
   mint shortens the entry, never the tokens' margin; < 60 s left aborts); SOT failure aborts and revokes; another host
   failing is left out and reported; closed after a seen claim sends no DELETE; close → 204 → revokes; close → 409
   (claimed between polls) → no revoke, 「已配對」; close → 404 / network error → no revoke, the 404 text; no token in
   logs.
3. **Dialog.** `PairPhoneDialog.tsx` from the Hosts page: profile picker (default master profile), relay picker
   (default active host), included / left-out hosts, QR + code + countdown, 「已配對」. i18n. Tests: defaults; states.
4. **Paired phones.** A Hosts-page section: rows by `pairing_id` across hosts; 撤銷 on every host; unreachable host →
   "not revoked yet", retried on connect (pending revocations, device-local). Tests: grouping; revoke; retry.
5. **Phone-built tab fixture.** `spa/src/lib/profile/__fixtures__/phone-tab.json`: a `tabs.<ws>` payload with one tab
   appended as purdex-ios builds it (agreed with purdex-ios before this task; it pins the same file in its tests). A
   vitest: the Mac's tabs guard accepts it and applying it puts the tab at the end of the workspace. (Lands in QP-1c
   if that merges first.)
6. **Screenshot gate:** the dialog with a QR and 「已配對」, and the paired-phones list, zh-TW, to the lead.

## Follow-ups

- The settings keys the phone applies (appearance) are listed in purdex-ios.
- Switching the phone to another profile = scan a new QR (v1).
- A Mac with an unpushed change to a workspace's tabs meets a conflict when the phone appended there meanwhile; settle
  an append-only SOT side automatically (rebase the local change on it) — a separate issue, opened when QP-1b merges.

## Plan review fold-in (codex `task-mv0shz3e-31lcty`, 2026-10-09)

| # | Finding | Fold-in |
|---|---------|---------|
| 1 | critical: claim cannot leave a non-pair payload unconsumed | Separate pairing entries; claim takes only them (spec §4) |
| 2 | critical: empty `allow` = open to all | Claim enforces tailnet / loopback itself (spec §4.2, QP-2 test) |
| 3 | critical: cannot tell close-before-scan | Status route + 2 s poll; delete entry on early close (spec §2, §4.3); the close race: second review #1 |
| 4 | critical: two windows apply twice | Superseded: no tab requests; the phone appends itself under the daemon's append rule (spec §5.2) |
| 5 | critical: two Macs apply twice | Superseded as #4: one CAS write on the SOT, no Mac applies anything |
| 6 | important: `POST /api/ws-ticket` missing | Allowed (spec §3.3) |
| 7 | important: conversation WS not closed on revoke | One connection registry for every WS kind (QP-1 task 4) |
| 8 | important: ticket principal plumbing | QP-1 task 3 |
| 9 | important: ServeMux cannot list routes | Default-deny via `mux.Handler(r)`; test that each allowed pattern is registered |
| 10 | important: conditional modules | Configurations nex on/off, push ready/soft-failed/off in the test |
| 11 | important: outer-mux routes | Stated: health and peers never see a device principal |
| 12 | important: methodless WS patterns | Pattern string as `mux.Handler` returns it |
| 13 | important: misclassification not caught | Allowed set pinned to the iOS list; iOS confirms create / send-keys |
| 14 | important: push routes too broad | Ownership: a device sees / deletes only its own registrations |
| 15 | important: token not bound to the profile | `profile_id` on the token; reads / appends only for it |
| 16 | important: unused-expiry races | `use_by` checked at lookup; first use in one statement; sweep only deletes dead rows |
| 17 | important: constant-time claim | Dropped: lookup by hash of a 128-bit random token |
| 18 | important: `kind == "pair"` is not enough | Strict row schema at create |
| 19 | important: SPA event wiring | Superseded: the Macs get the phone's tab by ordinary profile sync |
| 20 | important: parked master | Superseded: no Mac-side apply; ordinary sync already handles a parked master |
| 21 | important: leader / attachment lifecycle | Superseded: no consumer |
| 22 | important: SOT mint failure | Abort and revoke |
| 23 | important: zero workspaces | Superseded: the phone appends only into an existing `tabs.<ws>` section |
| 24 | important: host replacement rules | Spec §7: daemon-id match replaces entirely; duplicates: first wins |

## Second plan review fold-in (codex `task-mv0sqg4y-wmqxpq`, 2026-10-09)

Tab requests were dropped in the same revision (the phone appends under a daemon rule, spec §5.2), which settles #2 and
#3 by removing what they were about.

| # | Finding | Fold-in |
|---|---------|---------|
| 1 | critical: close races a claim between polls; DELETE answered 204 either way, so the Mac could revoke a claimed phone | DELETE says what happened (204 removed / 409 claimed / 404 none), decided under the claim lock; the Mac revokes only on 204; unclaimed tokens die at `use_by` (spec §4.3, QP-2 task 4, QP-3 task 2) |
| 2 | critical: an expired lease's claimant can delete the new claimant's request | Moot: no tab requests |
| 3 | critical: no confirmation boundary between `writeMasterWorld` and the daemon | Moot: no tab requests; the phone's own CAS write is the confirmation |
| 4 | important: bearer-less claim shares the store-wide limiter, so anyone on the tailnet can lock out the admin redeem | Claim has its own limiter, per source address; redeem's is untouched (spec §4.2, QP-2 task 2) |
| 5 | important: claimed entries kept to expiry fill the shared 16-code cap | Tombstones do not count toward the cap; at most 64, oldest dropped (spec §4.1, §4.3, QP-2 task 3) |
| 6 | important: `mux.Handler` wiring — `newOuterHandler` takes an `http.Handler`, tests pass wrappers, and `Handler` returns two values | `newOuterHandler(c, routes *http.ServeMux, inner http.Handler, allow)`; `_, pattern := routes.Handler(r)`; nil routes refuse devices (spec §3.3, QP-1 task 5) |

## Third review fold-in (codex `task-mv0t6ul0-wqmv7l`, incremental, 2026-10-09)

| # | Finding | Fold-in |
|---|---------|---------|
| 1 | important: the daemon not checking the device's hash is worse than one corrective write — a Mac fast-forwards on an equal hash without pulling (`sync-state.ts` `finish`), so the tab is never seen and later overwritten | Gate 4: the daemon recomputes the hash with a Go port of `hash.ts`, pinned to shared fixtures; mismatch → 400 `hash_mismatch` (spec §5.2, QP-1 tasks 7–8) |
| 2 | important: `use_by` counts from each mint, the entry's 10 minutes from packaging; a slow mint could leave a claimable code with dead tokens | One deadline D; tokens get `use_within_s` = (D − now) + 300, the entry `expires_in_s` = D − now; < 60 s left aborts (spec §2, §3.4, §4.1, QP-1 task 9, QP-2 task 1, QP-3 task 2) |
| 3 | important: an evicted tombstone turns a successful pairing into "unconfirmed" in the dialog | A 404 never says "not paired": the dialog points to 已配對的手機, whose `first_used_at` does not depend on a tombstone (spec §4.3, QP-3 task 2) |

## iOS review fold-in (purdex-ios `air26/_6cf3ft`, 2026-10-09)

| # | Point | Fold-in |
|---|-------|---------|
| 1 | The allow-list missed routes the App uses: session create and send-keys, hostconfig, relay/self, provenance, transcript, three nex reads; roster / relay-quota / profile list / fs unused | Exact list in spec §3.3 (with the nex engine-mount sub-rule, since `/api/nex/` is one pattern for the whole engine); the unused ones left out |
| 1′ | The App opens WebSockets with a bearer header, not a ticket | WS principal from the request context, bearer or ticket (spec §3.2, QP-1 task 4) |
| 2 | clientId must come from the SOT row's device id | Spec §7 |
| 3 | Host replacement keeps the phone's local record id | Spec §7 |
| 4 | Unreachable rows vs `host_id` mismatch | Unverified (retry 5 min after the claim, then 「請在桌機重新配對」) vs failed (spec §7) |
| 5 | Re-pairing must be able to take over an APNs registration | A registered APNs token moves to the caller (spec §3.3, QP-1 task 6) |
| 6 | 401 → stop reconnecting, mark revoked | Spec §7; a revoked host offers 「移除」 (lead ruling: removing a dead entry is not host management) |
| 7 | Pending tabs: drop when the section is gone? | Yes; also when the workspace is gone (spec §5.2) |
| 8 | §5.1 "never writes" contradicts §5.2 | Reworded |
| A | A device token can still run commands through sessions + send-keys | Stated in spec §3.3 ("what R7 is and is not"); revoking is the stop |
| D | Default label | The model name, renamable on the phone (lead ruling) |
| 9 | First real pairing (2026-10-09): the App lists profiles on connect and got 403, leaving the phone at 「連線中」 with empty lists | `GET /api/profiles` allowed; for a device it lists only the profile bound to its token (none if it has none) (spec §3.3) |

## QP-1b fold-in (implementation, 2026-10-09)

| # | Finding | Change |
|---|---------|--------|
| 1 | Per-handler registration of connections would miss any route added later | The outer chain wraps the ResponseWriter of a device's WebSocket handshake (`ConnRegistry.Track`) and tracks the hijacked connection; revoke by id / pairing closes them (spec §3.2, §3.4; QP-1 task 4) |
| 2 | A device ticket is a snapshot taken up to 30 s earlier | Redemption re-checks the device through `Refresher`: revoked or unknown → 401 before the handler; live → the current principal (QP-1 task 3) |
| 3 | An unscoped device token reaching the daemon before the allow-list exists | Interim scope in QP-1b-i, replaced in QP-1b-ii by `deviceAllowed` (QP-1 task 5) |
| 4 | QP-1b-ii leaves no way for a phone to write a profile before the §5.2 guard exists | Until QP-1c a device PUT of any section is 403 `device_append_only`; QP-1c replaces it with the guard (QP-1 tasks 6–7) |
| 5 | The `push_devices` table is deployed | `owner_device_id` is added by an idempotent migration; existing rows belong to no phone (QP-1 task 6) |
| 6 | Revoking a phone left its push registrations, so a lost phone kept receiving approval and agent content (attack, high) | Revocation also deletes them: `devices.RevokeFeed` (devices module) + push subscription, reconcile at Start, liveness check at register, cache pruned even if the DB write fails, no retry to a dropped registration; the one in-flight APNs request cannot be recalled (spec §3.4) |
| 7 | The user decided the phone may change the relay quota and the team size cap (2026-10-09 20:2x) | `deviceAllowed` gains `PUT /api/team/relay-quota` and `PUT /api/team/max-members`; spec §3.3 team line updated, relay quota removed from "left out"; `GET /api/team/roster` stays out (QP-1b-iii) |
