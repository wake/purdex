# Plan — host transfer code (H4)

Spec: `docs/specs/2026-09-23-host-ownership-spec.md` at commit `3574d836` (branch `worktree-host-ownership`) — §1,
§1.1 (decision 6: trusted relay, UI says so, no E2E) and §6. Numbers in §6 are decided (60 s / 10 failures, 16 live
codes, 32 hosts / 64 KiB, 404 `invalid_code`, 10 min TTL). Branch `worktree-host-transfer-code`, base `94210a2d`
(alpha.440). Two PRs, merged in order: **H4a** daemon, **H4b** SPA.

## H4a — daemon transfer store (§6.2, §6.3)

### Files (7)

| file | what |
|---|---|
| `internal/module/hosttransfer/store.go` | the in-memory store: create, redeem, sweep, rate limit |
| `internal/module/hosttransfer/store_test.go` | store unit tests |
| `internal/module/hosttransfer/handler.go` | `RegisterRoutes` + the two handlers |
| `internal/module/hosttransfer/handler_test.go` | handler tests over `httptest` |
| `internal/module/hosttransfer/module.go` | `core.Module`: Name, Init, Start (banner), Stop (drops every payload) |
| `cmd/pdx/main.go` | `c.AddModule(hosttransfer.New())` |
| `cmd/pdx/http_chain_test.go` | the two routes behind the real outer handler: no bearer → 401 |

Auth: every route except `GET /api/health` and `/api/peers*` goes through the `general` chain — CORS, IP whitelist,
PairingGuard, `TokenAuth` (cmd/pdx/http_chain.go:28–35). Nothing to wire; test 15 pins it.

### Store (store.go)

```go
const (
    codeTTL     = 10 * time.Minute
    maxLive     = 16
    failLimit   = 10
    failWindow  = 60 * time.Second
    genTries    = 5
    codeLen     = 8
)
type entry struct { payload json.RawMessage; expiresAt time.Time }
type Store struct {
    mu          sync.Mutex
    entries     map[string]entry          // key: canonical code
    failures    int
    windowStart time.Time                 // zero = no window open
    now         func() time.Time          // injected in tests
    gen         func() (string, error)    // injected in tests; default: 8 Crockford base32 chars from crypto/rand
}
```

- **Alphabet**: Crockford base32 `0123456789ABCDEFGHJKMNPQRSTVWXYZ` (no I, L, O, U). 8 chars = 40 bits; the default
  generator draws 5 random bytes and maps each 5-bit group (no modulo bias).
- **`normalise(raw string) string`**: upper-case; drop `-` and spaces; `I`/`L` → `1`, `O` → `0`. Nothing else is
  rewritten (a `U` or any other character just fails to match).
- **`Create(payload json.RawMessage) (code string, expiresAt time.Time, err error)`** — under `mu`: sweep expired;
  `len(entries) >= maxLive` → `ErrCapacity`; up to `genTries` calls of `gen` until the code is not a live key, else
  `ErrUnavailable`; store `{payload, now+codeTTL}`. A generator error → `ErrUnavailable`. Not rate-limited.
- **`Redeem(raw string) (payload json.RawMessage, retryAfter time.Duration, err error)`** — ONE critical section
  (§6.3): sweep expired; if a window is open and `now >= windowStart+failWindow` → close it (`failures = 0`,
  `windowStart` zero); if `failures >= failLimit` (window still open) → `ErrRateLimited` with
  `retryAfter = windowStart+failWindow-now`; normalise; look up; miss (unknown, expired-and-swept, used, or not 8
  valid chars) → open the window if none (`windowStart = now`), `failures++`, `ErrInvalidCode`; hit → delete the
  entry, return the payload. A success does NOT touch `failures` / `windowStart`.
  So the 10th failure itself still answers `invalid_code`; from then until the window ends every redeem — right code
  or wrong — answers `rate_limited`. A wrong code never touches any entry.
- **`Clear()`** (Stop): drop every entry.
- Nothing in the store logs.

### Handlers (handler.go)

- `POST /api/host-transfer` — `http.MaxBytesReader(64 KiB)`; over → 413. Decode `{"hosts": [ ... ]}` into
  `struct{ Hosts []json.RawMessage }` with `DisallowUnknownFields`; invalid JSON, `hosts` missing, 0 rows, > 32 rows, or
  any row that is not a JSON object → 400 `{"reason":"bad_payload"}`. The rows are stored as the re-marshalled array,
  opaque (the daemon does not interpret host fields). `ErrCapacity` → 429 `{"reason":"capacity"}`;
  `ErrUnavailable` → 503 `{"reason":"unavailable"}`. OK → 200 `{"code": "ABCD2345", "expiresAt": <unix ms>}`.
- `POST /api/host-transfer/redeem` — `MaxBytesReader(1 KiB)`; decode `{"code": "..."}`; invalid body → 400
  `{"reason":"bad_request"}` (does NOT count as a failure: it is not a guess). `ErrRateLimited` → 429
  `{"reason":"rate_limited"}` + `Retry-After: <ceil seconds>`; `ErrInvalidCode` → 404 `{"reason":"invalid_code"}`
  (one constant body for unknown / expired / used); OK → 200 `{"hosts": [ ... ]}`.
- Every response carries `Cache-Control: no-store`. Handlers never log a body, a code or a payload; the only log line
  is the module banner.

### Tests (TDD order; each red first)

store_test.go (injected `now` and `gen`):
1. create → redeem returns the exact payload; a second redeem of the same code → `ErrInvalidCode`.
2. TTL: redeem at `expiresAt - 1ns` succeeds; at `expiresAt` → `ErrInvalidCode`; an expired entry no longer counts
   toward capacity.
3. Normalisation: `abcd-2345`, `ABCD 2345`, lower-case, `I`/`L`/`O` substitutions all hit; a `U` misses.
4. Identical error for unknown / expired / used (same sentinel).
5. Concurrent redeem: 64 goroutines redeem one code → exactly one payload, 63 `ErrInvalidCode` (run with `-race`).
6. Brute force: 9 wrong → 10th wrong still `ErrInvalidCode`; 11th (even the RIGHT code) → `ErrRateLimited` with
   `retryAfter` = window end − now; at window end the right code succeeds; a success does not reset the counter
   (5 wrong, 1 right, 5 wrong → the next is rate-limited); the window is fixed from the FIRST failure.
7. A wrong code leaves every stored entry redeemable.
8. Capacity: 16 live → 17th `ErrCapacity`; after one is redeemed or expires → create works again.
9. Collision: a generator returning a live code 4 times then a fresh one → ok; 5 live collisions → `ErrUnavailable`;
   generator error → `ErrUnavailable`.
10. Default generator: 1000 codes are 8 chars from the alphabet (and not all equal).

handler_test.go:
11. Status and body mapping for every branch above (200 / 400 bad_payload / 413 / 429 capacity / 503 / 404
    invalid_code / 429 rate_limited + `Retry-After` / 400 bad_request), and `bad_request` does not count as a failure.
12. 32 rows ok, 33 → 400; a body just over 64 KiB → 413; a row that is a string / array → 400.
13. `Cache-Control: no-store` on every response.
14. Payload never logged: capture `log` output across create, redeem, a wrong redeem and a rate-limited redeem; the
    token string placed in the payload never appears.
15. Auth through the real outer handler (`cmd/pdx/http_chain_test.go`, `newOuterHandler` with a configured token): both
    routes without a bearer → 401, with it → reach the module.

module: Stop clears the store (a code created before Stop is invalid after a new Init) — covered by a store test of
`Clear` plus the module test that `Stop` calls it.

Gates: `go test ./internal/module/hosttransfer/... -race`, `go vet ./...`, the repo's usual `go test ./...`.

### Deploy

H4a needs the daemon on the relay host. Deploying mlab / air26 is the coordinator's call (asked before, never done
unilaterally). H4b's real-machine test needs it deployed on at least the relay.

## H4b — SPA share / receive (§6.1, §6.4)

### Open decision (asked of the coordinator, 2026-09-24): the look before H2c

§6.4.5 writes a payload's look "to the look store only where no entry exists for that `d1_…`", but the look store is
H2c's and does not exist on main. Proposed (b): until H2c, the receiver writes the look into the new `HostConfig`'s
own `name` / `colors` / `icon` / `iconWeight` — today's fallback fields (§2) — and only for rows it CREATES
(`existing` rows in overwrite mode keep their local look: overwrite replaces ip / port / token only, §6.4.3). H2c's
migration moves `HostConfig` looks into the look store, so nothing is lost. The plan below assumes (b); (a) = ignore
`look` until H2c, (c) = H4b after H2c.

### Files (≈ 16)

| file | what |
|---|---|
| `spa/src/lib/host-transfer-api.ts` (+ `.test.ts`) | `createTransfer(relayHostId, rows)`, `redeemTransfer(relayHostId, code)` over `hostFetch`; timeout (AbortController + setTimeout, the profile api's reason); result type `{kind:'ok'} \| {kind:'failed', reason}` with reasons `capacity \| rate_limited(retryAfterS) \| invalid_code \| bad_payload \| too_large \| unavailable \| unauthorized \| network \| timeout \| malformed` parsed from the daemon's `{reason}` |
| `spa/src/lib/host-transfer-plan.ts` (+ `.test.ts`) | pure: `payloadRowsOf(hosts)` (share side), `planReceive(rows, observations, local)` → preview rows with status, `commitSet(preview, picks, mode)` → the store change |
| `spa/src/stores/useHostStore.ts` (+ test in `useHostStore.test.ts`) | new action `applyHostTransfer(change)` — adds and overwrites in ONE `set()` |
| `spa/src/components/hosts/ShareHostsDialog.tsx` (+ test) | pick hosts, pick relay, trust copy, create, show code + expiry |
| `spa/src/components/hosts/ReceiveHostsDialog.tsx` (+ test) | pick relay, enter code, redeem, verify, preview, mode, confirm |
| `spa/src/components/hosts/HostSidebar.tsx` | "Receive hosts" next to "Add host"; "Share hosts" beside it (a list-level action: the dialog picks the hosts) |
| `spa/src/components/HostPage.tsx` | mounts the two dialogs (like `AddHostDialog`) |
| `spa/src/locales/en.json`, `zh-TW.json` | `hosts.transfer.*` |

### Share (§6.1, §6.2)

1. The dialog lists every local host with a token (a host without one cannot be used by the receiver — listed
   disabled with the reason); all ticked by default. Relay: a select of connected hosts (default: the active host).
2. The trust sentence (both locales) is shown BEFORE the create button, above it, always visible: "‹relay› will hold the
   access tokens of the hosts you share, readable by that host, until the code is used or expires (10 min). Only relay
   through a host you trust." zh-TW: 「‹relay› 會保存你分享的主機的存取 token，直到代碼被使用或過期（10 分鐘）；
   這段期間這台主機讀得到它們。只透過你信任的主機中轉。」
3. Rows: `{ name, ip, port, token, daemonId?, look?: { colors?, color?, icon?, iconWeight? } }` from the store
   (`daemonId` only when set; `look` fields only when set).
4. Create → the code shown as `ABCD-2345` with a copy button and "expires at ‹time›" (from `expiresAt`); the relay's
   name is shown with it (the receiver must pick the same relay). Failures in words per reason (`capacity` → "too many
   open codes on ‹relay›, wait or use another relay").

### Receive (§6.3, §6.4)

1. Relay select (connected hosts) + code input (free text; the daemon normalises). Redeem. `rate_limited` shows the
   wait (`Retry-After`); `invalid_code` says "unknown, expired or already used".
2. Rows that are not objects or lack `ip` / `port` / `token` are dropped with a count ("2 rows could not be read").
3. Verify EVERY row: `fetchInfoAt('http://ip:port', row.token)` in parallel (its own 5 s timeout), observed
   `host_id` is the truth (`""` = no id).
4. `planReceive` statuses, exactly §6.4.3: `new`, `existing` (exactly one local row with that daemonId), `mismatch`
   (payload daemonId ≠ observed), `unverified` (unreachable / 401 / empty id; a Retry button re-verifies that row),
   `duplicate` (same observed daemonId as an earlier payload row — first wins), `local-conflict` (two local rows claim
   that daemonId, or the endpoint equals a local row with a DIFFERENT daemonId). Only `new` and `existing` are
   committable. Preview: one line per row — name, endpoint, status in words, checkbox (committable only, ticked by
   default).
5. Mode: add new only (default) / overwrite the same daemon / replace all. Add-only skips `existing`; overwrite
   replaces ip / port / token of the one matching local row (local id kept; `daemonId` stays the observed one); new rows
   get a fresh id with the observed `daemonId` (set through `observeDaemonId` semantics inside the action — never a
   raw `daemonId` write that bypasses the verified-endpoint rule).
6. Confirm → `applyHostTransfer(change)` — ONE `set()`; it validates the whole change first (every overwrite target
   still exists with the expected daemonId and endpoint as planned; no new row collides with an existing endpoint);
   any mismatch or throw → store untouched and the dialog says "the host list changed, check again" (re-plan).
7. Replace-all, AFTER the atomic add/overwrite: remove every local host not in the committed set EXCEPT the relay used
   for this transfer, the current master's attach host (`masterHostId` when attached), `activeHostId` (kept) and — the
   rest of §6.4.6 — `devHostId` is cleared if removed; it refuses (before anything is written) when the kept set
   would be empty. Removals go through `deleteHostCascade(id, false)` (mark, not close — the normal delete's default)
   one by one; their undo functions are collected into ONE toast "Removed N hosts · Undo". A removal that no-ops
   (last host) is reported.
   **For review:** the add/overwrite commit is atomic; the removals are a sequence of normal deletes, each with its own
   cascade — atomicity across them is not attempted (the spec says removals go through the normal delete path).
8. Before H3 the `hosts` section still syncs: a received host is pushed by Profile Sync like any locally added host.
   Expected until H3; noted in the PR.

### Tests (TDD order)

1. api: every daemon status → reason (200 / 400 bad_payload / 413 too_large / 429 capacity / 429 rate_limited +
   `Retry-After` / 404 invalid_code / 503 / 401 / network / timeout with fake timers / malformed body).
2. planner: each status, including endpoint-equals-local-with-other-daemonId → `local-conflict`, two payload rows same
   observed id → second `duplicate`, empty observed id → `unverified`, payload without `daemonId` but verified →
   `new` / `existing` by the observed id; `commitSet` per mode.
3. store action: add + overwrite in one `set` (subscriber sees one change); a stale plan (target gone, daemonId
   changed, endpoint now taken) → store identical (`hosts`, `hostOrder`, `activeHostId`, `devHostId`, runtime); new rows
   carry the observed `daemonId` and are verified; overwrite keeps the local id and look.
4. replace-all: keeps relay, master attach host, `activeHostId`; clears a removed `devHostId`; refuses zero hosts
   with nothing written; one aggregated undo restores all removed hosts.
5. `/api/info` is called with the PAYLOAD token for every row (spy on `fetchInfoAt`).
6. dialogs: the trust copy is visible before create; code formatting; redeem errors in words; preview statuses; only
   committable rows are tickable; confirm calls the action once.
7. locale completeness (placeholders kept in `hosts.transfer.*`).

Gates: `cd spa && npx vitest run`, `pnpm run lint`, `npx tsc --noEmit -p tsconfig.app.json`.

## Real machine (spec §8 H4)

Two clients with independent host ids (A has mlab + air26, B has only mlab), daemon with H4a deployed on the relay
(mlab). A shares mlab + air26 through mlab → B enters the code → preview: mlab `existing`, air26 `new` → add-only →
air26 added, connected, verified. A second redeem of the same code → "unknown, expired or already used". 10 wrong
codes → the 11th shows the wait. Clean up: delete air26 on B.
