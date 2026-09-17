# Phase D — Pairing UI and Return-Path Verification Implementation Plan

**Date:** 2026-09-18  
**Status:** In Progress  
**Approach:** TDD (Test-Driven Development)

## Overview

Three-phase implementation following TDD flow:
1. Write test → Implement → Commit
2. Each phase builds on the previous
3. All tests must pass before proceeding

---

## Phase D1: SPA Pair Page with Drift Display

### Goal
Build the Pair page showing token status across hosts with drift detection.

### Implementation Steps

#### Step 1a: Create Types (usePairStore foundation)
- File: `spa/src/lib/pair-store.ts`
- Define `HostWithTokenStatus` interface
- Define `PeerEnvelope` interface

#### Step 1b: Create Tests (Pair.test.tsx)
- File: `spa/src/pages/Pair.test.tsx`
- Test: loads and fetches `/api/peers/hosts` on mount
- Test: displays Section A with `has_token: true` hosts
- Test: displays Section B with `has_inbound_token: false` hosts
- Test: calls `/api/peers/self/envelope` for drift detection
- Test: "Adopt new alias" button calls POST `/api/peers/<host_id>/adopt`

#### Step 1c: Create usePairStore
- File: `spa/src/lib/pair-store.ts`
- State:
  - `ourAlias: string | null`
  - `hostList: HostWithTokenStatus[]`
  - `error: string | null`
  - `loading: boolean`
- Methods:
  - `fetchHostsWithTokenStatus(): Promise<void>`
  - `setOurAlias(alias: string): void`
  - `adoptAlias(hostId: string, newAlias: string): Promise<void>`

#### Step 1d: Implement Pair.tsx Component
- File: `spa/src/pages/Pair.tsx`
- Route: `/pair`
- Layout: Two sections side-by-side
  - Section A: "Hosts with token" (has_token: true)
  - Section B: "Peer-listing-only hosts" (has_inbound_token: false)
- Drift detection:
  - On mount, fetch `/api/peers/self/envelope`
  - Compare returned `alias` with `ourAlias`
  - Show drift indicator if mismatch
- Adopt button: POST `/api/peers/<host_id>/adopt` with new alias

#### Step 1e: Verify Tests Pass
```bash
cd spa && npx vitest run Pair.test.tsx
```

#### Step 1f: Commit D1
```bash
git commit -m "feat(spa): implement Pair page with drift detection

- Add usePairStore for token status management
- Create Pair component with two-section layout
- Implement drift detection on mount
- Add adopt alias button
- Full test coverage

Co-Authored-By: Claude Haiku 4.5 <noreply@anthropic.com>"
```

---

## Phase D2: Daemon Verify Endpoint

### Goal
Implement secure verify endpoint with constant-time token comparison.

### Implementation Steps

#### Step 2a: Create Types (internal/module/peers/verify.go)
- Type: `VerifyRequest` (token from Authorization header)
- Type: `VerifyResponse` (verified: bool, our_alias: string)

#### Step 2b: Create Tests (internal/module/peers/verify_test.go)
- Test: valid token → 200 + verified: true + our_alias
- Test: invalid token → 401 + error
- Test: missing Authorization header → 401 + error
- Test: constant-time comparison (mutation test)

#### Step 2c: Implement Verify Endpoint
- File: `internal/module/peers/verify.go`
- Handler: `handleVerifyPeer(w http.ResponseWriter, r *http.Request)`
- Extract Bearer token from Authorization header
- Use `crypto/subtle.ConstantTimeCompare` for token validation
- Return 200 with verified:true + our_alias on match
- Return 401 on mismatch or missing header

#### Step 2d: Register Route
- File: `internal/core/route_handler.go` or peers module router
- Route: `POST /api/peers/verify`
- Mount handler: `router.HandleFunc("/api/peers/verify", handleVerifyPeer).Methods("POST")`

#### Step 2e: Verify Tests Pass
```bash
cd /Users/wake/Workspace/wake/purdex && go test ./internal/module/peers/... -v
```

#### Step 2f: Commit D2
```bash
git commit -m "feat(daemon): implement verify endpoint with constant-time comparison

- Add POST /api/peers/verify handler
- Extract and validate Bearer token from Authorization header
- Use crypto/subtle.ConstantTimeCompare for timing-safe validation
- Return 200 with verified status on success
- Return 401 on invalid token or missing header
- Full test coverage including mutation test

Co-Authored-By: Claude Haiku 4.5 <noreply@anthropic.com>"
```

---

## Phase D3: SPA Verify Button + Daemon Coordinator

### Goal
Complete the flow with verify button UI and coordinator handler.

### Implementation Steps

#### Step 3a: Create Tests (Pair.test.tsx)
- Test: verify button appears in Section B
- Test: verify button disabled when `has_inbound_token: false`
- Test: calls peer's `/api/peers/verify` with inbound token
- Test: displays "Verified ✓" on success
- Test: displays "Unverified ✗" on failure

#### Step 3b: Add verifyPeer to usePairStore
- Method: `verifyPeer(hostId: string, inboundToken: string): Promise<boolean>`
- Implementation:
  - POST `https://<hostName>/api/peers/verify`
  - Set Authorization header: `Bearer <inboundToken>`
  - Return `true` if response.verified === true
  - Return `false` on error

#### Step 3c: Update Pair.tsx with Verify Button
- Add verify button to Section B rows
- Button state: idle → loading → verified/unverified
- On click: call `verifyPeer(hostId, inboundToken)`
- Display result with visual indicator

#### Step 3d: Implement handleVerifyPeer (Daemon)
- File: `internal/module/peers/coordinator.go`
- Method: `handleVerifyPeer(w http.ResponseWriter, r *http.Request)`
- Extract target peer info from request
- Forward verification request to remote peer's `/api/peers/verify`
- Return verification result to SPA

#### Step 3e: Verify Tests Pass
```bash
cd spa && npx vitest run Pair.test.tsx
cd /Users/wake/Workspace/wake/purdex && go test ./internal/module/peers/... -v
```

#### Step 3f: Commit D3
```bash
git commit -m "feat(spa,daemon): add verify button and coordinator

- Add verifyPeer method to usePairStore
- Implement verify button in Pair component Section B
- Add daemon handleVerifyPeer coordinator
- Support multi-host verification flow
- Full test coverage

Co-Authored-By: Claude Haiku 4.5 <noreply@anthropic.com>"
```

---

## Final Validation

### Code Quality Checks
```bash
cd spa && pnpm run lint && pnpm run build
cd /Users/wake/Workspace/wake/purdex && go build -o bin/pdx ./cmd/pdx
```

### All Tests
```bash
cd spa && npx vitest run
cd /Users/wake/Workspace/wake/purdex && go test ./...
```

### Git Status
```bash
git log --oneline -3
git status
```

---

## Success Criteria Verification

- ✅ Pair page renders with two sections
- ✅ Drift detection displays correctly
- ✅ Adopt button updates alias on server
- ✅ Verify endpoint implements constant-time comparison
- ✅ Verify button calls remote peer endpoint
- ✅ All tests passing (Vitest, Go testing)
- ✅ Lint and build successful
- ✅ Commits follow project conventions
