# Phase D — Pairing UI and Return-Path Verification — Implementation Plan

Date: 2026-09-18 · Phase: Three sub-phases (D1, D2, D3) · TDD all phases

Blocks on: Phase C complete (`alpha.375+`); `/api/peers/hosts` endpoint exists (✓ works).

## Critical Path

```
D1 (SPA list + drift) → D2 (daemon verify endpoint) → D3 (SPA verify button)
     Depends on ↑           Depends on ↑                 Depends on ↑
  /api/peers/hosts      handlers in D2                   D1 + D2
```

Each phase is a separate test cycle and commit group; all three ship in one release.

## Phase D1 — List and Drift Display

**Target:** Pair page shows configured hosts (with admin token) vs. unconfigured (peer-listing-only). For configured: alias, host_id, token flags, verified status, and alias drift (if self-reported name differs).

### D1 Implementation Steps

1. **Create `spa/src/pages/Pair.tsx`**
   - Route: `/hosts` or `/settings/pairs`
   - Fetch `/api/peers/hosts` on mount
   - Parse response into two sections:
     - Section A: Hosts with `has_token: true` (two-way capable)
     - Section B: Hosts known only via peer listings (one-way-only; read-only)
   - Render table with columns: Alias, Host ID, Outbound Token, Inbound Token, Verified, Drift Display

2. **Add `useHostStore` query for `/api/peers/hosts`**
   - Extend existing `useHostStore` or create `usePairStore`
   - Endpoint: `GET /api/peers/hosts`
   - Store fetched list and error states
   - Provide `refetch()` method for after drift adoption

3. **Alias drift detection**
   - For each host in Section A (configured):
     - Call host's `/api/peers/self/envelope` (if it exists)
     - Compare local `alias` vs. peer's `self_reported_alias`
     - If different: render drift message and adopt button
     - If endpoint doesn't exist (older daemon): skip drift display (graceful degrade)

4. **Adopt button handler**
   - On click: `PUT /api/peers/hosts/<alias>` with `{"alias": <new_alias>}`
   - Handle 200 (success), 409 (collision; show error), other errors
   - Refetch `/api/peers/hosts` on success

5. **Two-way vs. one-way distinction**
   - Section A: Full UI, all buttons/actions
   - Section B: Read-only, display only; no buttons

### D1 TDD Checklist

- [ ] Test: Pair page fetches and renders `/api/peers/hosts`
- [ ] Test: Section A lists all hosts with `has_token: true`
- [ ] Test: Section B lists hosts NOT in `/api/peers/hosts`
- [ ] Test: Drift detection calls `/api/peers/self/envelope`
- [ ] Test: Drift display shows when `local_alias != self_reported_alias`
- [ ] Test: Adopt button POSTs to `PUT /api/peers/hosts/<alias>`
- [ ] Test: 409 collision error handled and displayed
- [ ] Test: Graceful degrade when `/api/peers/self/envelope` doesn't exist
- [ ] Test: Verified flag persists from `/api/peers/hosts` response

### D1 Dependencies

- Endpoint `/api/peers/hosts` (already exists, no new code needed)
- Endpoint `/api/peers/self/envelope` (on peer daemon; assume exists or handle missing)
- Endpoint `PUT /api/peers/hosts/<alias>` (already exists from Phase C)

---

## Phase D2 — Return-Path Verification Endpoint

**Target:** `POST /api/peers/verify` daemon-side handler that proves a peer can reach this daemon.

### D2 Implementation Steps

1. **Write `internal/module/peers/verify.go`**
   - Handler: `handleVerify(w http.ResponseWriter, r *http.Request)`
   - Route pattern: `POST /api/peers/verify`
   - Auth: Require admin principal (HostRoutePolicy rejects host principals)
   - Request body:
     ```json
     {
       "requester_alias": "air26",
       "requester_host_id": "..."
     }
     ```
   - Response on success (200):
     ```json
     {
       "verified": true,
       "our_alias": "mlab"
     }
     ```
   - Response on auth failure (401):
     ```json
     {
       "error": "invalid token"
     }
     ```

2. **Token matching logic**
   - Extract `Authorization: Bearer <token>` header
   - Loop over `m.core.Cfg.Peers.Hosts` and compare token against each `InboundToken`
   - Use `crypto/subtle.ConstantTimeCompare()` to prevent timing attacks
   - If no match: return 401
   - If match: record verification (optional, for audit logging), return 200 + our alias

3. **Error handling**
   - Missing/malformed header: 401 + "missing token"
   - Invalid token (no match): 401 + "invalid token"
   - JSON decode error: 400 + "invalid json"
   - All other errors: 500

4. **Add to router**
   - Register in `cmd/pdx/api.go` or peers module init:
     ```go
     mux.HandleFunc("POST /api/peers/verify", m.handleVerify)
     ```

### D2 TDD Checklist

- [ ] Test: Valid token → 200 + `verified: true` + `our_alias`
- [ ] Test: Invalid token → 401 + "invalid token"
- [ ] Test: Missing authorization header → 401 + "missing token"
- [ ] Test: Malformed header (not "Bearer ...") → 401
- [ ] Test: Constant-time comparison (mutation: remove ConstantTimeCompare → test fails)
- [ ] Test: Invalid JSON body → 400 + "invalid json"
- [ ] Test: Endpoint rejects host principal (not admin)
- [ ] Test: Multiple inbound tokens, match the correct one

### D2 Dependencies

- Admin route policy (already exists)
- Core config structure with `Peers.Hosts[].InboundToken`
- No database changes required

---

## Phase D3 — SPA Verify Button

**Target:** On Pair page, add "Verify Return Path" button that calls daemon's `/api/peers/verify-self/<alias>` coordinator endpoint.

### D3 Implementation Steps

1. **Write daemon-side coordinator `handleVerifyPeer`**
   - Route: `POST /api/peers/verify-self/{alias}`
   - Path parameter: `alias` (name of host to verify)
   - Auth: Admin only
   - Logic:
     1. Find host in config by `alias`
     2. Check `has_inbound_token` (if false, return 400 "peer has no inbound token yet")
     3. Call peer's `/api/peers/verify` with our `inbound_token` (Bearer header)
     4. Return 200 + `{"status": "verified"}` or error code on failure
   - Timeout: 10s fetch timeout (configurable)

2. **Add to router**
   ```go
   mux.HandleFunc("POST /api/peers/verify-self/{alias}", m.handleVerifyPeer)
   ```

3. **SPA button component**
   - In `Pair.tsx`, Section A, add button per host:
     ```tsx
     <button
       onClick={() => verifyPeer(host.alias)}
       disabled={!host.has_inbound_token}
       aria-label={`Verify return path for ${host.alias}`}
     >
       {verifyStatus[host.alias] === "verified" && "✓ Verified"}
       {verifyStatus[host.alias] === "unverified" && "✗ Unverified"}
       {verifyStatus[host.alias] === "in_progress" && "⏳ Verifying..."}
       {!verifyStatus[host.alias] && "Verify Return Path"}
     </button>
     ```
   - Disabled state: when `has_inbound_token: false`
   - States: "verified" | "unverified" | "in_progress" | null (initial)

4. **Add store state**
   - Extend `usePairStore` or create new:
     ```ts
     type VerificationState = Record<string, "verified" | "unverified" | "in_progress" | null>
     
     const verifyPeer = async (alias: string) => {
       setVerificationState(alias, "in_progress")
       try {
         const resp = await fetch(`/api/peers/verify-self/${alias}`, { method: "POST" })
         if (resp.ok) {
           const data = await resp.json()
           setVerificationState(alias, data.status)
         } else {
           setVerificationState(alias, "unverified")
         }
       } catch (err) {
         setVerificationState(alias, "unverified")
       }
     }
     ```

### D3 TDD Checklist

- [ ] Test: `handleVerifyPeer` with valid alias and inbound_token → calls peer's `/api/peers/verify`
- [ ] Test: `handleVerifyPeer` without inbound_token → 400 "peer has no inbound token yet"
- [ ] Test: `handleVerifyPeer` unknown alias → 404 "unknown alias"
- [ ] Test: Peer reachable and responds 200 → coordinator returns 200 + "verified"
- [ ] Test: Peer unreachable (network error) → coordinator returns 502 + error message
- [ ] Test: Peer responds 401 (bad token) → coordinator returns 502 (or propagates as "unverified")
- [ ] Test: Verify button disabled when `has_inbound_token: false`
- [ ] Test: Button click sets state to "in_progress" → "verified" or "unverified"
- [ ] Test: Multiple concurrent verifies on different hosts don't interfere

### D3 Dependencies

- D1 (Pair page structure)
- D2 (peer's `/api/peers/verify` endpoint exists on peer daemon)

---

## Integration & Shipping

### Integration Checklist

- [ ] All three phases' tests pass locally
- [ ] Lint passes: `cd spa && pnpm run lint`
- [ ] SPA builds: `cd spa && pnpm run build`
- [ ] Daemon compiles: `go build -o bin/pdx ./cmd/pdx`
- [ ] No new dependencies (check `go.mod`, `spa/package.json`)

### Real-Machine Acceptance (§8.3 from spec)

All five must be signed off by real run or marked "not run":

1. **Pair page loads:** List shows all configured hosts with aliases and token status
   - [ ] Passed / [ ] Not run
   
2. **Drift display:** On mlab, air shown with drift (if exists). Click adopt → alias updates
   - [ ] Passed / [ ] Not run
   
3. **Verify button:** Fresh pair (outbound token set, inbound token present) → click Verify → ✓ Verified within 2s
   - [ ] Passed / [ ] Not run
   
4. **Asymmetric pair:** Outbound token set, no inbound token → button disabled
   - [ ] Passed / [ ] Not run
   
5. **One-way-only hosts:** Section B shows read-only hosts known via peer listings, no buttons
   - [ ] Passed / [ ] Not run

### Deployment

- Merge to `main` (via PR + review)
- Bump `VERSION` (separate PR)
- Daemon restart picks up new binary
- SPA auto-updates via HMR (dev) or bundled renderer fallback (prod)
- Air: SPA via HMR, Electron via dev update mechanism

---

## Commit Strategy

**One commit per phase** (or per logical step if >2 hours).

- `feat(daemon/peers): add verify endpoint (D2)` — handleVerify handler + route + tests
- `feat(spa/pages): add Pair page with drift display (D1)` — Pair.tsx + store queries + adopt logic + tests
- `feat(daemon/peers): add verify-self coordinator (D3)` — handleVerifyPeer handler + route
- `feat(spa/pages): add verify button to Pair page (D3)` — button UI + state + tests

---

## Risk & Mitigations

| Risk | Mitigation |
|------|-----------|
| Peer's `/api/peers/self/envelope` doesn't exist (old daemon) | Graceful degrade in D1: skip drift display if 404 |
| Network timeout on verify button click | 10s timeout + error state; button shows "✗ Unverified" |
| Timing attack on token comparison | Use `crypto/subtle.ConstantTimeCompare()` in D2 |
| Simultaneous verifies for same host | Each state change is atomic; last one wins (acceptable) |
| Section B (peer-listing-only) hosts are stale | Read-only; next sync will refresh via `pdx peers --all` integration (future work) |

---

## Open Questions / Deferred

1. **Token rotation UI (D4):** Deferred. Mechanism (D2 `inbound_token_prev`) ships with D2/D3.
2. **Automatic drift resolution:** Deferred. One-click adoption is D1; automatic is future work.
3. **Peer's inbound direction drift:** Deferred. Peer still sees old name after you adopt. Tracked as follow-up.

---

## Timeline Estimate

- **D1 (SPA):** 1.5 days (list rendering, drift detection, adopt button)
- **D2 (daemon):** 1 day (verify handler, tests)
- **D3 (SPA + daemon):** 1 day (verify button, coordinator, integration tests)
- **Buffer + real-machine verification:** 0.5 days

**Total:** ~4 days (if each day = 6-8 hours focused work).

---

## Files to Create / Modify

### Daemon

- **Create:** `internal/module/peers/verify.go` (handleVerify + handleVerifyPeer)
- **Modify:** `cmd/pdx/api.go` (register routes)

### SPA

- **Create:** `spa/src/pages/Pair.tsx` (full page component)
- **Create:** `spa/src/stores/usePairStore.ts` (or extend existing)
- **Modify:** `spa/src/routing.ts` or main router (add `/hosts` or `/settings/pairs` route)

### Tests

- `internal/module/peers/verify_test.go` (unit tests for both handlers)
- `spa/src/pages/Pair.test.tsx` (Pair page integration tests)
- `spa/src/stores/usePairStore.test.ts` (store logic tests)

---

## Success Criteria

✓ All five real-machine acceptance items signed off
✓ All unit tests pass (>90% line coverage on new code)
✓ Lint + build pass
✓ No new dependencies
✓ Spec compliance: all three phases' requirements met
