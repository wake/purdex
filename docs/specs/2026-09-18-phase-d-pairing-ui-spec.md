# Phase D — Pairing UI and Return-Path Verification

Date: 2026-09-18 · Scope: daemon (`internal/module/peers`, `internal/config`) + SPA (`spa/src/pages`, `spa/src/stores`) · Three phases

Depends on and flows from `2026-09-17-peer-address-v4-spec.md` (§10 out-of-scope), which records the alias model Phase C established. Phase D completes it with a UI to show and verify bidirectional pairs, and gates on being able to demonstrate the binding to a human operator.

## 1. Goal

An operator can see which daemons this App holds admin tokens for, distinguish them from daemons known only via peer listings, and complete a full round-trip pair without manual key handling or a second channel.

**The constraint that unlocks the design:** An admin token for a daemon is an authorization to write config on that daemon, so the App holding such a token can execute both directions of the pairing handshake — `A add B with B's token` → `B add A with A's token` → `A set-token` — in one atomic sequence.

## 2. Context — Why D Exists

v4 (spec §7, phase C) deployed alias standardization — every daemon publishes its own `alias`, and a peer adopts it at add time unless it collides. Cross-host addresses became portable:

```
mlab/purdex-b0 [q34psn]    # v4: readable, exact, portable
```

But portability is **not the same as reachability**. A one-way pair — `A → B` verified, `B ↛ A` unverified — reports success and is useless. And the current daemon reports nothing about the return path.

**Real case: mlab ↔ air26 alias collision and asymmetry**

On mini-lab, `pdx peers host list` shows:
```
mlab    http://127.0.0.1:7860  c5n3p... (local)
air     https://air2026.ts:7860 n1x8k... (verified)
```

But when air26 runs `pdx peers host list`, it shows itself as `air26`, not `air`. Result: an address pasted from mini-lab to air26 fails, because air26 expects its own name.

The local alias is **never changed** — user's config is authoritative. But we accept the drift and show it, and offer a one-click adopt-as-local-alias button. That choice is Phase C's (spec §7.2, backward compat), and Phase D displays it and holds the UI for the adopt flow.

**The larger structural defect: half-pairing goes undetected.** An operator on mini-lab adds air26 with a token, verifies it (`verifyHost` fetches the envelope), and assumes the pair is complete. They do not know whether air26 can reply. It cannot, because nothing has told air26 about mini-lab. The status quo is silent failure — a configuration that looks completed but is not. Phase D surfaces this.

## 3. Decisions (user-confirmed, do not reopen)

| # | Decision |
|---|---|
| D1 | The Pair page lists daemons this App holds admin tokens for, plus a distinguished section for daemons known only via peer lists (no token), marked as one-way-only. |
| D2 | For each two-way-pairable host (admin token held), show: alias, host_id, token status (outbound + inbound), verified flag, alias drift (self-reported vs local), and a drift-adopt button when it differs. |
| D3 | A return-path verification button: upon click, App sends a verification request to that host's daemon, which contacts this daemon's `/api/peers/verify` to prove it can reach back. Result: a verb (✓ verified / ✗ unverified / ⏳ in progress), not a blocking modal. |
| D4 | Token rotation UI (widget to show and rotate token pairs) deferred — the mechanism (`inbound_token_prev` and `MatchInboundToken` update) is in scope, but the interaction is not. Tracked as follow-up. |
| D5 | Both handlers (`handleAddHost`, `handlePutHost`) are daemon-side and already ship. Phase D is frontend-only for blocks 1–2 (list + drift display), and adds one new `/api/peers/verify` endpoint for D3. The mechanism is simple and scoped. |

## 4. Three Phases

Each is one to three days. Spec cuts them so they can ship in one release but be tested and reviewed separately.

### 4.1 Phase D1 — List and Drift Display

**What ships:** Hosts → Pair page listing all configured hosts, distinguishing which ones have admin tokens (two-way) from those known only via peer listings (one-way). For the two-way ones, show alias, host_id, outbound-token and inbound-token status, and alias drift with a one-click adopt button.

**Why separate:** It is the foundational UI; verification depends on it. It exercises the `/api/peers/hosts` listing path (which works, no new code needed on daemon side) and the alias-matching logic the drift button reuses.

**Not in D1:**
- Return-path verification (D3)
- Token rotation (D4 deferred)

### 4.2 Phase D2 — Return-Path Verification Endpoint

**What ships:** `POST /api/peers/verify` daemon-side endpoint: a peer host calls it to prove it can reach this daemon. Signed with the peer's `inbound_token`, the call atomically:
1. Proves the peer can reach this host
2. Atomically records the inbound-token age (so we can rotate both directions without a lockout window)

Response: `{ verified: true }` or a 401/403 on auth failure.

**Why separate:** The endpoint is the blocker for verification. Once it exists and is tested, the SPA button (D3) becomes straightforward. The backend is the constraint, not the frontend.

**Not in D2:**
- The SPA button (D3 frontend)
- Token rotation transitions (D4)

### 4.3 Phase D3 — SPA Verify Button

**What ships:** On the Pair page, next to each two-way host, a button `Verify Return Path`. On click:
1. POST to this daemon's `/api/peers/verify-self/<alias>` (SPA-side coordinator; see §4.4)
2. The coordinator calls the peer's `/api/peers/verify` to ask them to reach back
3. This daemon records the verification attempt and result
4. Button shows ✓ verified / ✗ unverified / ⏳ in-progress

The button starts disabled until the peer has an `inbound_token` (they added us back).

**Why separate:** D1 and D2 are prerequisites. Once they work, D3 is a few lines of SPA glue code and a button.

## 5. Phase D1 — Hosts → Pair Page

### 5.1 Route and Data

**GET /api/peers/hosts** (already exists, no change):
```json
{
  "hosts": [
    {
      "alias": "air",
      "url": "https://air2026.ts:7860",
      "host_id": "n1x8k...",
      "verified": true,
      "has_token": true,
      "has_inbound_token": false,
      "allow_bypass": false
    },
    { ... }
  ]
}
```

**SPA store:** `useHostStore` already exists and holds host configs (name, ip, port, token). Phase D extends the Pair page to consume `/api/peers/hosts` and layer it over `useHostStore`.

### 5.2 Alias Drift Detection

When the App has an admin token for a host, it can call the host's own endpoints. On the Pair page, when listing hosts:

1. Fetch `/api/peers/hosts` to get `alias` (local config name) and display it
2. Call the host's `/api/peers/self/envelope` (if it exists; if not, skip drift display)
3. Compare local `alias` against the peer's `self_reported_alias` (from their `/envelope`)
4. If different: show `alias (drift: peer calls itself <peer_alias>)` and offer an adopt button

The adopt button calls `PUT /api/peers/hosts/<alias>` with `{"alias": <peer_alias>}`, which triggers the `sanitizeLearnedAlias` + 409 collision check (Phase C logic).

**If `/api/peers/self/envelope` does not exist** (older daemon): drift display is a nice-to-have, not a blocker. The page still works.

### 5.3 Two-Way vs One-Way Distinction

The page shows two sections:

**Section A: Configured Pairs (this App has admin tokens)**
- List every entry in `/api/peers/hosts`
- For each: show alias, host_id, `has_token` (outbound) ✓/✗, `has_inbound_token` (inbound) ✓/✗, verified flag
- Drift display (if applicable) with adopt button
- Placeholder for D3's verify button

**Section B: Known But Unconfigured (peer listings only)**
- Hosts known from `pdx peers --all` output that are NOT in `/api/peers/hosts`
- Read-only: no buttons, no modification UI
- Display: alias (from peer listing) + a note "Known via peer listing; add from Section A to pair"

The boundary is critical: Section B makes clear why "I can see this host in the table but can't message it" — it's not configured.

### 5.4 Real Case: mlab ↔ air26

**On mini-lab:**

Section A:
```
Alias          Host ID      Outbound  Inbound  Verified  Drift
mlab (local)   c5n3p...     ✓         —        —         —
air            n1x8k...     ✓         —        ✗         (peer calls itself air26)
               [adopt air26 button]
```

Clicking the button: `PUT /api/peers/hosts/air` with `{"alias": "air26"}`. If successful, the row updates to show `air26` and the drift clears.

**On air26:**

After mini-lab clicks adopt:
- mini-lab's config now says `air26`
- That change does not auto-propagate to air26
- When air26 runs `pdx peers --all`, it still lists mini-lab as `mlab`, and air26's own config still has mini-lab under the alias `mini-lab` (or whatever air26 calls it)

The adoption is **one-way local**: "from now on, I will call you air26". It does not change what air26 calls you. That is correct — each host's alias list is authoritative for that host.

But the UI makes this clear: drifts are shown with their `verified` status. The verified flag is binary — it means "I fetched the envelope and it was OK", not "we are mutually paired". D3 will add the return-path check.

## 6. Phase D2 — `/api/peers/verify` Endpoint

### 6.1 Calling It

A peer host calls this to prove it can reach us:

```bash
curl -X POST https://mlab.ts:7860/api/peers/verify \
  -H 'Authorization: Bearer <inbound_token>' \
  -H 'Content-Type: application/json' \
  -d '{"requester_alias": "air26", "requester_host_id": "..."}'
```

### 6.2 Response

On success (token matches an entry's `inbound_token`):
```json
{
  "verified": true,
  "our_alias": "mlab"
}
```

On failure (token does not match):
```
401 Unauthorized
```

**Why include `our_alias` in the response:** The peer learns how we call ourselves, which helps with their own drift detection (deferred, but this plants the seed).

### 6.3 Handler (`internal/module/peers/verify.go`)

```go
func (m *Module) handleVerify(w http.ResponseWriter, r *http.Request) {
    // Admin route; HostRoutePolicy rejects all host principals
    if !requireAdmin(w, r) {
        return
    }

    var req struct {
        RequesterAlias string `json:"requester_alias"`
        RequesterHostID string `json:"requester_host_id"`
    }
    if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
        writeJSONError(w, http.StatusBadRequest, "invalid json")
        return
    }

    m.core.CfgMu.RLock()
    ourAlias := m.core.Cfg.PeerAlias()
    token := r.Header.Get("Authorization")
    m.core.CfgMu.RUnlock()

    // Token is "Bearer <token>"; strip the prefix
    token = strings.TrimPrefix(token, "Bearer ")
    if token == "" {
        writeJSONError(w, http.StatusUnauthorized, "missing token")
        return
    }

    // Check against inbound_token (constant-time compare)
    m.core.CfgMu.RLock()
    matched := false
    for _, h := range m.core.Cfg.Peers.Hosts {
        if h.InboundToken != "" && subtle.ConstantTimeCompare([]byte(token), []byte(h.InboundToken)) == 1 {
            matched = true
            break
        }
    }
    m.core.CfgMu.RUnlock()

    if !matched {
        writeJSONError(w, http.StatusUnauthorized, "invalid token")
        return
    }

    w.Header().Set("Content-Type", "application/json")
    _ = json.NewEncoder(w).Encode(map[string]any{
        "verified": true,
        "our_alias": ourAlias,
    })
}
```

### 6.4 Routing

Add to `cmd/pdx/api.go` or the peers module:

```go
mux.HandleFunc("POST /api/peers/verify", m.handleVerify)
```

Route is admin-only (HostRoutePolicy already gates it).

## 7. Phase D3 — SPA Verify Button

### 7.1 Coordinator Endpoint (Daemon-Side)

`POST /api/peers/verify-self/<alias>`: the SPA calls this to trigger a return-path check. The daemon then calls the peer's `/api/peers/verify`.

```go
type verifyPeerRequest struct {
    // Empty body; the alias comes from the path
}

type verifyPeerResponse struct {
    Status string `json:"status"` // "verified" | "unverified" | "in_progress"
    Error string `json:"error,omitempty"`
}

func (m *Module) handleVerifyPeer(w http.ResponseWriter, r *http.Request) {
    if !requireAdmin(w, r) {
        return
    }

    alias := r.PathValue("alias")
    if alias == "" {
        writeJSONError(w, http.StatusBadRequest, "missing alias")
        return
    }

    m.core.CfgMu.RLock()
    idx := m.core.Cfg.Peers.FindPeerHostByAlias(alias)
    if idx == -1 {
        m.core.CfgMu.RUnlock()
        writeJSONError(w, http.StatusNotFound, "unknown alias")
        return
    }
    h := m.core.Cfg.Peers.Hosts[idx]
    ourAlias := m.core.Cfg.PeerAlias()
    ourHostID := m.core.Cfg.HostID
    m.core.CfgMu.RUnlock()

    if h.InboundToken == "" {
        // Peer hasn't added us back; can't verify the return path
        writeJSONError(w, http.StatusBadRequest, "peer has no inbound token yet")
        return
    }

    // Call peer's /api/peers/verify with our inbound_token
    ctx, cancel := context.WithTimeout(r.Context(), remoteFetchTimeout)
    defer cancel()

    verifyURL := h.URL + "/api/peers/verify"
    reqBody := map[string]string{
        "requester_alias": ourAlias,
        "requester_host_id": ourHostID,
    }
    bodyBytes, _ := json.Marshal(reqBody)

    req, _ := http.NewRequestWithContext(ctx, "POST", verifyURL, bytes.NewReader(bodyBytes))
    req.Header.Set("Authorization", "Bearer " + h.InboundToken)
    req.Header.Set("Content-Type", "application/json")

    resp, err := m.client.Do(req)
    if err != nil {
        writeJSONError(w, http.StatusBadGateway, "failed to reach peer: " + err.Error())
        return
    }
    defer resp.Body.Close()

    if resp.StatusCode == http.StatusOK {
        w.Header().Set("Content-Type", "application/json")
        w.WriteHeader(http.StatusOK)
        _ = json.NewEncoder(w).Encode(map[string]string{"status": "verified"})
    } else {
        writeJSONError(w, http.StatusBadGateway, "peer verification failed: " + resp.Status)
    }
}
```

Add to routing:
```go
mux.HandleFunc("POST /api/peers/verify-self/{alias}", m.handleVerifyPeer)
```

### 7.2 SPA Button

On the Pair page, in Section A (configured pairs), add a button next to each host:

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

The button is disabled when `has_inbound_token` is false (peer hasn't added us yet).

### 7.3 Store and State

Add to `usePairStore` (new store, or extend an existing one):

```ts
interface VerificationState {
  [hostAlias: string]: "verified" | "unverified" | "in_progress" | null
}

const verifyPeer = async (alias: string) => {
  setVerificationState(alias, "in_progress")
  try {
    const resp = await fetch(`/api/peers/verify-self/${alias}`, {
      method: "POST",
      headers: { "Authorization": `Bearer ${adminToken}` },
    })
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

## 8. Testing

### 8.1 Unit (Daemon)

- `handleVerify`: token matches → 200 + `verified: true`; token mismatch → 401; missing token → 401
- `handleVerifyPeer`: no inbound token → 400; peer reachable → 200 + status; peer unreachable → 502
- Constant-time token comparison (mutation: remove constant time → test must fail)

### 8.2 Unit (SPA)

- Pair page lists `/api/peers/hosts` rows with alias, host_id, token flags
- Drift detection: `self_reported_alias != local_alias` → show drift message + button
- Adopt button POSTs new alias, expects 200 or 409 (collision)
- Verify button disabled when `has_inbound_token` is false
- Verify button shows correct status (verified/unverified/in_progress)

### 8.3 Real-Machine Acceptance

1. **Pair page loads:** List shows all configured hosts with their aliases and token status
2. **Drift display:** On mlab, air is listed with drift message (if air's envelope differs). Click adopt → alias updates to air26
3. **Verify button:** On a fresh pair (outbound token set, inbound token populated by peer), click Verify Return Path → shows ✓ Verified within 2 seconds
4. **Asymmetric pair:** Outbound token set but no inbound token → button disabled with explanatory message
5. **One-way-only hosts:** In Section B, read-only listing of hosts known only via peer lists; no buttons, no verification UI

**All five must be signed off by running them or recorded as not run.**

## 9. Implementation Order (within each phase)

**D1:**
1. Create `spa/src/pages/Pair.tsx` (list + drift display)
2. Add `useHostStore` queries for `/api/peers/hosts`
3. Drift button calls `PUT /api/peers/hosts/{alias}`

**D2:**
1. Write `internal/module/peers/verify.go` (`handleVerify`)
2. Add route to `cmd/pdx/api.go`
3. Unit tests

**D3:**
1. Write `internal/module/peers/verify.go` (`handleVerifyPeer`)
2. Add route
3. SPA button + store state
4. Unit tests

## 10. Out of Scope

- **Token rotation UI (D4):** The mechanism (`inbound_token_prev`, `MatchInboundToken` update) is in scope and ships with D2/D3. The button to rotate is not. Tracked as #XXXX.
- **Alias adoption for the peer's inbound direction:** When you adopt a new local alias, the peer still sees you by your old name. Deferred as a follow-up (requires D3's verify button to be useful for bidirectional fixing).
- **Automatic drift resolution:** Showing drift is D1. One-click adoption is D1. Automatic correction is not — each side is authoritative for its own list.

## 11. Schema Changes

**None.** The `/api/peers/hosts` route and response already exist. The `/api/peers/verify` endpoint adds a new route but no database changes.

**Config change (deferred):** Token rotation will add `inbound_token_prev` to `PeerHost` when D4 ships. For now, `inbound_token` is the only field.
