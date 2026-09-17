# Phase D — Pairing UI and Return-Path Verification

**Date:** 2026-09-18  
**Status:** In Progress  
**Owner:** TDD Implementation

## Overview

Phase D completes the pairing UI flow by adding:
1. **D1:** SPA Pair page with drift detection
2. **D2:** Daemon verify endpoint with constant-time comparison
3. **D3:** SPA verify button + daemon coordinator

Enables users to see which hosts have already established return-path credentials and verify pending inbound pairings.

---

## Phase D1: SPA Pair Page with Drift Display

### Requirements

#### Pair.tsx Component
- **Route:** `/pair`
- **Layout:** Two-section horizontal (or vertical on mobile)
  - **Section A:** "Hosts with token" — list hosts where `has_token: true`
  - **Section B:** "Peer-listing-only hosts" — list hosts where `has_inbound_token: false`
- **Drift Detection:**
  - On mount, call `GET /api/peers/self/envelope`
  - Compare returned `alias` against stored `our_alias` in store
  - Display indicator if mismatch (drift detected)
- **Adopt Button:**
  - Label: "Adopt new alias"
  - Action: POST `/api/peers/<host_id>/adopt` with `{ alias: <new_alias> }`
  - Disabled if no drift detected

#### usePairStore (Zustand)
- `ourAlias: string | null` — current known alias for this peer
- `hostList: HostWithTokenStatus[]` — from `/api/peers/hosts`
- `fetchHostsWithTokenStatus(): Promise<void>` — GET `/api/peers/hosts`, populate list
- `setOurAlias(alias: string): void` — update `ourAlias`
- `adoptAlias(hostId: string, newAlias: string): Promise<void>` — POST and update store

#### Types
```typescript
interface HostWithTokenStatus {
  host_id: string
  name: string
  has_token: boolean
  has_inbound_token: boolean
}

interface PeerEnvelope {
  host_id: string
  alias: string  // our_alias as seen by daemon
}
```

---

## Phase D2: Daemon Verify Endpoint

### Requirements

#### Endpoint: `POST /api/peers/verify`
- **Authentication:** Bearer token in `Authorization` header
- **Validation:**
  - Extract token from header
  - Constant-time compare token against `inbound_token` stored in peer's state
  - Token match → return `200 { verified: true, our_alias: "<alias>" }`
  - No match or missing header → return `401 { error: "unauthorized" }`
- **Security:** Timing-safe comparison to prevent token brute-force attacks

#### Test Coverage
- ✅ Valid token → 200 + verified: true + our_alias
- ✅ Invalid token → 401
- ✅ Missing Authorization header → 401
- ✅ Constant-time compare (mutation test: flipping a bit in token still takes same time)

---

## Phase D3: SPA Verify Button + Daemon Coordinator

### Requirements

#### Pair.tsx: Verify Button
- In Section B (peer-listing-only), show "Verify inbound" button
- Disabled if `has_inbound_token: false`
- On click:
  - Show loading state
  - Call `handleVerifyPeer(hostId, inboundToken)`
  - Display result: "Verified ✓" or "Unverified ✗"

#### usePairStore: Verify Method
- `verifyPeer(hostId: string, inboundToken: string): Promise<boolean>`
  - Call `POST https://<host>/api/peers/verify` with `Authorization: Bearer <inboundToken>`
  - Return `true` if response is `{ verified: true }`
  - Return `false` on error

#### Daemon: handleVerifyPeer (Coordinator)
- Coordinates multi-host verification
- Future: delegates to remote peers via return-path WS
- Current phase: local only

---

## Testing Strategy

### Phase D1 Tests (Pair.test.tsx)
```typescript
describe("Pair page", () => {
  it("loads and fetches /api/peers/hosts on mount", ...)
  it("displays hosts with has_token:true in Section A", ...)
  it("displays hosts with has_inbound_token:false in Section B", ...)
  it("calls /api/peers/self/envelope and detects drift", ...)
  it("adopts new alias on button click", ...)
})
```

### Phase D2 Tests (verify_test.go)
```go
func TestVerifyEndpoint(t *testing.T) {
  t.Run("valid token returns 200", ...)
  t.Run("invalid token returns 401", ...)
  t.Run("missing header returns 401", ...)
  t.Run("constant-time compare", ...) // mutation test
}
```

### Phase D3 Tests (Pair.test.tsx, verify_test.go)
```typescript
it("calls peer's /api/peers/verify with inbound token", ...)
it("verify button disabled when has_inbound_token is false", ...)
it("shows verified/unverified state on completion", ...)
```

---

## Deliverables

- ✅ Phase D1: Pair.tsx + usePairStore + tests
- ✅ Phase D2: daemon verify endpoint + tests
- ✅ Phase D3: Pair verify button + coordinator + tests
- ✅ All tests passing
- ✅ SPA lint/build successful
- ✅ Daemon binary built successfully

---

## Success Criteria

1. **Pair page renders** with two sections (tokens / listing-only)
2. **Drift detection works** — alias mismatch displays correctly
3. **Adopt button** updates alias on server
4. **Verify endpoint** implements constant-time comparison
5. **Verify button** calls remote peer's verify endpoint
6. **All tests passing** (Vitest, Go testing)
