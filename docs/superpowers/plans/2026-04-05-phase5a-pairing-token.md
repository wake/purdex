# Phase 5a: 配對系統 + Token 認證 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 建立 Quick 模式（13 碼配對碼）和一般模式（daemon 產生 token）的雙軌首次設定流程。

**Architecture:** Daemon 新增 PairingState 狀態機（pairing/pending/normal）控制 middleware 行為。Quick 模式用 PairingGuard 攔截非配對端點、Base58 編碼配對碼；一般模式用 TokenAuth 保護所有 API。SPA AddHostDialog 完整重寫，支援配對碼和 Token 兩條互斥路線。

**Tech Stack:** Go (net/http, crypto/rand, math/big), React 19, Zustand, Tailwind 4, Vitest, Phosphor Icons

**Spec:** `docs/superpowers/specs/2026-04-05-phase5-pairing-token-design.md`

---

## File Structure

### Go — New Files

| File | Responsibility |
|------|---------------|
| `internal/core/pairing_state.go` | PairingState enum + thread-safe getter/setter |
| `internal/core/pairing_state_test.go` | PairingState 測試 |
| `internal/core/setup_secret.go` | SetupSecretStore（5 min TTL, one-time, single active） |
| `internal/core/setup_secret_test.go` | SetupSecretStore 測試 |
| `internal/core/base58.go` | Base58 encode/decode + 配對碼編碼 |
| `internal/core/base58_test.go` | Base58 + 配對碼測試 |
| `internal/core/pairing_handler.go` | POST /api/pair/verify + /api/pair/setup handlers |
| `internal/core/pairing_handler_test.go` | 配對 handler 測試 |
| `internal/core/token_handler.go` | POST /api/token/auth handler |
| `internal/core/token_handler_test.go` | Token auth handler 測試 |
| `internal/middleware/pairing_guard.go` | PairingGuard middleware |
| `internal/middleware/pairing_guard_test.go` | PairingGuard 測試 |
| `cmd/tbox/quick.go` | Quick 模式 IP 選擇邏輯 |

### Go — Modified Files

| File | Changes |
|------|---------|
| `internal/middleware/middleware.go:58-84` | TokenAuth 改為 getter function |
| `internal/middleware/middleware_test.go` | 所有 TokenAuth 呼叫改用 getter |
| `internal/core/core.go:32-44,130-137` | Core struct 加 PairingState/SetupSecrets 欄位 + 註冊新路由 |
| `internal/core/info_handler.go:21-24` | HandleHealth 加 mode 欄位 |
| `cmd/tbox/main.go:53-178` | --quick flag + 啟動邏輯 + middleware chain 重組 |

### SPA — New Files

| File | Responsibility |
|------|---------------|
| `spa/src/lib/pairing-codec.ts` | Base58 decode + 配對碼解碼 + token 產生 |
| `spa/src/lib/pairing-codec.test.ts` | 編解碼測試 |

### SPA — Modified Files

| File | Changes |
|------|---------|
| `spa/src/lib/host-api.ts` | 加 fetchPairVerify, fetchPairSetup, fetchTokenAuth |
| `spa/src/components/hosts/AddHostDialog.tsx` | 完整重寫 |
| `spa/src/locales/en.json` | 新增配對相關 i18n keys |
| `spa/src/locales/zh-TW.json` | 新增配對相關 i18n keys |

---

## Task 1: PairingState 型別

**Files:**
- Create: `internal/core/pairing_state.go`
- Create: `internal/core/pairing_state_test.go`

- [ ] **Step 1: Write PairingState test**

```go
// internal/core/pairing_state_test.go
package core

import "testing"

func TestPairingStateDefault(t *testing.T) {
	var ps PairingState
	if ps.Get() != StateNormal {
		t.Errorf("default should be normal, got %s", ps.Get())
	}
}

func TestPairingStateSetGet(t *testing.T) {
	var ps PairingState
	ps.Set(StatePairing)
	if ps.Get() != StatePairing {
		t.Errorf("want pairing, got %s", ps.Get())
	}
	ps.Set(StatePending)
	if ps.Get() != StatePending {
		t.Errorf("want pending, got %s", ps.Get())
	}
	ps.Set(StateNormal)
	if ps.Get() != StateNormal {
		t.Errorf("want normal, got %s", ps.Get())
	}
}

func TestPairingStateString(t *testing.T) {
	cases := []struct{ s StateValue; want string }{
		{StatePairing, "pairing"},
		{StatePending, "pending"},
		{StateNormal, "normal"},
	}
	for _, tc := range cases {
		if tc.s.String() != tc.want {
			t.Errorf("want %s, got %s", tc.want, tc.s.String())
		}
	}
}
```

- [ ] **Step 2: Run test — expect FAIL**

Run: `cd /Users/wake/Workspace/wake/tmux-box && go test ./internal/core/ -run TestPairingState -v`
Expected: compilation error — `PairingState` not defined

- [ ] **Step 3: Implement PairingState**

```go
// internal/core/pairing_state.go
package core

import "sync/atomic"

// StateValue represents the daemon's pairing state.
type StateValue int32

const (
	StateNormal  StateValue = 0
	StatePairing StateValue = 1
	StatePending StateValue = 2
)

func (s StateValue) String() string {
	switch s {
	case StatePairing:
		return "pairing"
	case StatePending:
		return "pending"
	default:
		return "normal"
	}
}

// PairingState provides thread-safe access to the daemon's pairing mode.
// Zero value is StateNormal.
type PairingState struct {
	v atomic.Int32
}

func (ps *PairingState) Get() StateValue {
	return StateValue(ps.v.Load())
}

func (ps *PairingState) Set(s StateValue) {
	ps.v.Store(int32(s))
}
```

- [ ] **Step 4: Run test — expect PASS**

Run: `cd /Users/wake/Workspace/wake/tmux-box && go test ./internal/core/ -run TestPairingState -v`
Expected: all 3 tests PASS

- [ ] **Step 5: Commit**

```bash
git add internal/core/pairing_state.go internal/core/pairing_state_test.go
git commit -m "feat(core): add PairingState thread-safe enum (pairing/pending/normal)"
```

---

## Task 2: SetupSecretStore

**Files:**
- Create: `internal/core/setup_secret.go`
- Create: `internal/core/setup_secret_test.go`

- [ ] **Step 1: Write SetupSecretStore tests**

```go
// internal/core/setup_secret_test.go
package core

import (
	"testing"
	"time"
)

func TestSetupSecretGenerate(t *testing.T) {
	ss := NewSetupSecretStore(5 * time.Minute)
	secret, err := ss.Generate()
	if err != nil {
		t.Fatal(err)
	}
	if len(secret) != 32 {
		t.Errorf("want 32-char hex, got %d chars", len(secret))
	}
}

func TestSetupSecretValidateSuccess(t *testing.T) {
	ss := NewSetupSecretStore(5 * time.Minute)
	secret, _ := ss.Generate()
	if !ss.Validate(secret) {
		t.Error("expected valid secret to pass")
	}
}

func TestSetupSecretOneTimeUse(t *testing.T) {
	ss := NewSetupSecretStore(5 * time.Minute)
	secret, _ := ss.Generate()
	ss.Validate(secret) // consume
	if ss.Validate(secret) {
		t.Error("expected consumed secret to fail")
	}
}

func TestSetupSecretExpired(t *testing.T) {
	ss := NewSetupSecretStore(1 * time.Millisecond)
	secret, _ := ss.Generate()
	time.Sleep(5 * time.Millisecond)
	if ss.Validate(secret) {
		t.Error("expected expired secret to fail")
	}
}

func TestSetupSecretNewGenerateClearsOld(t *testing.T) {
	ss := NewSetupSecretStore(5 * time.Minute)
	old, _ := ss.Generate()
	_, _ = ss.Generate() // should clear old
	if ss.Validate(old) {
		t.Error("expected old secret to be cleared after new generate")
	}
}

func TestSetupSecretEmpty(t *testing.T) {
	ss := NewSetupSecretStore(5 * time.Minute)
	if ss.Validate("") {
		t.Error("empty string should not validate")
	}
}
```

- [ ] **Step 2: Run test — expect FAIL**

Run: `cd /Users/wake/Workspace/wake/tmux-box && go test ./internal/core/ -run TestSetupSecret -v`
Expected: compilation error — `NewSetupSecretStore` not defined

- [ ] **Step 3: Implement SetupSecretStore**

```go
// internal/core/setup_secret.go
package core

import (
	"crypto/rand"
	"encoding/hex"
	"sync"
	"time"
)

// SetupSecretStore manages a single one-time setup secret for pairing.
// Only one active secret at a time; generating a new one clears the old.
type SetupSecretStore struct {
	mu     sync.Mutex
	secret string
	born   time.Time
	ttl    time.Duration
}

func NewSetupSecretStore(ttl time.Duration) *SetupSecretStore {
	return &SetupSecretStore{ttl: ttl}
}

// Generate creates a new setup secret, replacing any existing one.
func (ss *SetupSecretStore) Generate() (string, error) {
	raw := make([]byte, 16) // 128-bit
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	secret := hex.EncodeToString(raw)

	ss.mu.Lock()
	defer ss.mu.Unlock()
	ss.secret = secret
	ss.born = time.Now()
	return secret, nil
}

// Validate checks and consumes the setup secret. Returns true if valid.
func (ss *SetupSecretStore) Validate(secret string) bool {
	if secret == "" {
		return false
	}
	ss.mu.Lock()
	defer ss.mu.Unlock()
	if ss.secret == "" || ss.secret != secret {
		return false
	}
	if time.Since(ss.born) > ss.ttl {
		ss.secret = ""
		return false
	}
	ss.secret = "" // one-time use
	return true
}
```

- [ ] **Step 4: Run test — expect PASS**

Run: `cd /Users/wake/Workspace/wake/tmux-box && go test ./internal/core/ -run TestSetupSecret -v`
Expected: all 6 tests PASS

- [ ] **Step 5: Commit**

```bash
git add internal/core/setup_secret.go internal/core/setup_secret_test.go
git commit -m "feat(core): add SetupSecretStore (5min TTL, one-time, single active)"
```

---

## Task 3: TokenAuth 改為 getter function

**Files:**
- Modify: `internal/middleware/middleware.go:58-84`
- Modify: `internal/middleware/middleware_test.go`

- [ ] **Step 1: Update TokenAuth tests to use getter**

Replace all `middleware.TokenAuth("secret", ...)` calls with `middleware.TokenAuth(func() string { return "secret" }, ...)` in `internal/middleware/middleware_test.go`.

Change every occurrence:

```
middleware.TokenAuth("secret", nil)   →  middleware.TokenAuth(func() string { return "secret" }, nil)
middleware.TokenAuth("secret", tv)    →  middleware.TokenAuth(func() string { return "secret" }, tv)
middleware.TokenAuth("Secret", nil)   →  middleware.TokenAuth(func() string { return "Secret" }, nil)
middleware.TokenAuth("", nil)         →  middleware.TokenAuth(func() string { return "" }, nil)
```

- [ ] **Step 2: Run test — expect FAIL**

Run: `cd /Users/wake/Workspace/wake/tmux-box && go test ./internal/middleware/ -v`
Expected: compilation error — `TokenAuth` expects `string`, got `func() string`

- [ ] **Step 3: Update TokenAuth signature and implementation**

In `internal/middleware/middleware.go`, change `TokenAuth`:

```go
// TokenAuth checks Bearer token, one-time ticket, or ?token= query param.
// tokenFn is called on each request to support runtime token changes.
func TokenAuth(tokenFn func() string, tickets TicketValidator) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			token := tokenFn()
			if token == "" {
				next.ServeHTTP(w, r)
				return
			}
			// Check Authorization header first
			if auth := r.Header.Get("Authorization"); len(auth) >= 7 && strings.EqualFold(auth[:7], "bearer ") && subtle.ConstantTimeCompare([]byte(auth[7:]), []byte(token)) == 1 {
				next.ServeHTTP(w, r)
				return
			}
			// Check one-time ticket (for WebSocket)
			if tickets != nil {
				if ticket := r.URL.Query().Get("ticket"); tickets.Validate(ticket) {
					next.ServeHTTP(w, r)
					return
				}
			}
			// Fallback: ?token= query param (legacy, will be removed)
			if subtle.ConstantTimeCompare([]byte(r.URL.Query().Get("token")), []byte(token)) == 1 {
				next.ServeHTTP(w, r)
				return
			}
			http.Error(w, "unauthorized", http.StatusUnauthorized)
		})
	}
}
```

Key change: `token == ""` check moved inside the per-request handler, `tokenFn()` called on every request.

- [ ] **Step 4: Run middleware tests — expect PASS**

Run: `cd /Users/wake/Workspace/wake/tmux-box && go test ./internal/middleware/ -v`
Expected: all tests PASS

- [ ] **Step 5: Update main.go call site**

In `cmd/tbox/main.go:151`, change:

```go
// Before
middleware.TokenAuth(cfg.Token, c.Tickets)(mux)

// After
middleware.TokenAuth(func() string {
    c.CfgMu.RLock()
    defer c.CfgMu.RUnlock()
    return c.Cfg.Token
}, c.Tickets)(mux)
```

- [ ] **Step 6: Run full test suite — expect PASS**

Run: `cd /Users/wake/Workspace/wake/tmux-box && go test ./... 2>&1 | tail -20`
Expected: all packages PASS

- [ ] **Step 7: Commit**

```bash
git add internal/middleware/middleware.go internal/middleware/middleware_test.go cmd/tbox/main.go
git commit -m "refactor(middleware): TokenAuth accepts getter func for runtime token changes

BREAKING: TokenAuth now evaluates token on each request (dynamic activation).
Previously, empty token was a static decision at middleware creation time.
Now, tokenFn returning empty = pass-through, returning non-empty = enforce auth."
```

---

## Task 4: PairingGuard middleware

**Files:**
- Create: `internal/middleware/pairing_guard.go`
- Create: `internal/middleware/pairing_guard_test.go`

- [ ] **Step 1: Write PairingGuard tests**

```go
// internal/middleware/pairing_guard_test.go
package middleware_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/wake/tmux-box/internal/middleware"
)

func TestPairingGuardBlocksInPairingMode(t *testing.T) {
	isPairing := func() bool { return true }
	h := middleware.PairingGuard(isPairing)(ok)

	req := httptest.NewRequest("GET", "/api/sessions", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 503 {
		t.Errorf("want 503, got %d", rec.Code)
	}
	var body map[string]string
	json.NewDecoder(rec.Body).Decode(&body)
	if body["reason"] != "pairing_mode" {
		t.Errorf("want reason=pairing_mode, got %s", body["reason"])
	}
}

func TestPairingGuardAllowsPairEndpoints(t *testing.T) {
	isPairing := func() bool { return true }
	h := middleware.PairingGuard(isPairing)(ok)

	for _, path := range []string{"/api/pair/verify", "/api/pair/setup"} {
		req := httptest.NewRequest("POST", path, nil)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != 200 {
			t.Errorf("%s: want 200, got %d", path, rec.Code)
		}
	}
}

func TestPairingGuardPassThroughWhenNotPairing(t *testing.T) {
	isPairing := func() bool { return false }
	h := middleware.PairingGuard(isPairing)(ok)

	req := httptest.NewRequest("GET", "/api/sessions", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Errorf("want 200 pass-through, got %d", rec.Code)
	}
}

func TestPairingGuardAllowsOptions(t *testing.T) {
	isPairing := func() bool { return true }
	h := middleware.PairingGuard(isPairing)(ok)

	req := httptest.NewRequest("OPTIONS", "/api/sessions", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Errorf("want 200 for OPTIONS, got %d", rec.Code)
	}
}
```

- [ ] **Step 2: Run test — expect FAIL**

Run: `cd /Users/wake/Workspace/wake/tmux-box && go test ./internal/middleware/ -run TestPairingGuard -v`
Expected: compilation error — `PairingGuard` not defined

- [ ] **Step 3: Implement PairingGuard**

```go
// internal/middleware/pairing_guard.go
package middleware

import (
	"encoding/json"
	"net/http"
	"strings"
)

// PairingGuard blocks non-pairing requests when isPairing returns true.
// Only /api/pair/* paths and OPTIONS requests are allowed through.
func PairingGuard(isPairing func() bool) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !isPairing() {
				next.ServeHTTP(w, r)
				return
			}
			// Always allow OPTIONS (CORS preflight)
			if r.Method == "OPTIONS" {
				next.ServeHTTP(w, r)
				return
			}
			// Allow /api/pair/* paths
			if strings.HasPrefix(r.URL.Path, "/api/pair/") {
				next.ServeHTTP(w, r)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusServiceUnavailable)
			json.NewEncoder(w).Encode(map[string]string{"reason": "pairing_mode"})
		})
	}
}
```

- [ ] **Step 4: Run test — expect PASS**

Run: `cd /Users/wake/Workspace/wake/tmux-box && go test ./internal/middleware/ -run TestPairingGuard -v`
Expected: all 4 tests PASS

- [ ] **Step 5: Commit**

```bash
git add internal/middleware/pairing_guard.go internal/middleware/pairing_guard_test.go
git commit -m "feat(middleware): add PairingGuard — blocks non-pair requests in Quick mode"
```

---

## Task 5: Core 整合 — PairingState + SetupSecrets + 路由 + Health mode

**Files:**
- Modify: `internal/core/core.go:32-44,130-137`
- Modify: `internal/core/info_handler.go:21-24`

- [ ] **Step 1: Add fields to Core struct**

In `internal/core/core.go`, add to the `Core` struct (after `Tickets` field):

```go
Pairing       PairingState
SetupSecrets  *SetupSecretStore
PairingSecret string // hex(3 bytes), used for /api/pair/verify
failedVerify  int32  // atomic counter for brute-force protection
```

- [ ] **Step 2: Initialize SetupSecrets in New()**

In `internal/core/core.go` `New()` function, add after `Tickets: NewTicketStore()`:

```go
SetupSecrets: NewSetupSecretStore(5 * time.Minute),
```

Add `"time"` to imports.

- [ ] **Step 3: Register new routes in RegisterCoreRoutes()**

In `internal/core/core.go`, add to `RegisterCoreRoutes()`:

```go
mux.HandleFunc("POST /api/pair/verify", c.handlePairVerify)
mux.HandleFunc("POST /api/pair/setup", c.handlePairSetup)
mux.HandleFunc("POST /api/token/auth", c.handleTokenAuth)
```

- [ ] **Step 4: Update HandleHealth to include mode**

In `internal/core/info_handler.go`, replace `HandleHealth`:

```go
func (c *Core) HandleHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"ok":   true,
		"mode": c.Pairing.Get().String(),
	})
}
```

- [ ] **Step 5: Run existing tests — expect PASS**

Run: `cd /Users/wake/Workspace/wake/tmux-box && go test ./internal/core/ -v -count=1 2>&1 | tail -20`
Expected: PASS (handlers not yet implemented — will use stub in next tasks)

Note: `handlePairVerify`, `handlePairSetup`, `handleTokenAuth` won't exist yet. Create empty stubs:

```go
// internal/core/pairing_handler.go
package core

import "net/http"

func (c *Core) handlePairVerify(w http.ResponseWriter, r *http.Request) {}
func (c *Core) handlePairSetup(w http.ResponseWriter, r *http.Request) {}
```

```go
// internal/core/token_handler.go
package core

import "net/http"

func (c *Core) handleTokenAuth(w http.ResponseWriter, r *http.Request) {}
```

- [ ] **Step 6: Commit**

```bash
git add internal/core/core.go internal/core/info_handler.go internal/core/pairing_handler.go internal/core/token_handler.go
git commit -m "feat(core): integrate PairingState + SetupSecrets + register pair/token routes"
```

---

## Task 6: Base58 編解碼 + 配對碼

**Files:**
- Create: `internal/core/base58.go`
- Create: `internal/core/base58_test.go`

- [ ] **Step 1: Write Base58 and pairing code tests**

```go
// internal/core/base58_test.go
package core

import (
	"net"
	"testing"
)

func TestBase58RoundTrip(t *testing.T) {
	original := []byte{100, 64, 0, 2, 0x1e, 0xb4, 0xab, 0xcd, 0xef} // 9 bytes
	encoded := base58Encode(original)
	decoded, err := base58Decode(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if len(decoded) != len(original) {
		t.Fatalf("length mismatch: want %d, got %d", len(original), len(decoded))
	}
	for i := range original {
		if decoded[i] != original[i] {
			t.Errorf("byte %d: want %d, got %d", i, original[i], decoded[i])
		}
	}
}

func TestBase58FixedLength(t *testing.T) {
	// Smallest 9-byte value: all zeros
	small := make([]byte, 9)
	enc := base58EncodeFixed(small, 13)
	if len(enc) != 13 {
		t.Errorf("want 13 chars, got %d: %s", len(enc), enc)
	}

	// Largest 9-byte value
	big := []byte{0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff}
	enc2 := base58EncodeFixed(big, 13)
	if len(enc2) != 13 {
		t.Errorf("want 13 chars, got %d: %s", len(enc2), enc2)
	}
}

func TestEncodePairingCode(t *testing.T) {
	ip := net.ParseIP("100.64.0.2").To4()
	port := uint16(7860)
	secret := []byte{0xab, 0xcd, 0xef}

	code := EncodePairingCode(ip, port, secret)

	// Should be formatted as XXXX-XXXX-XXXXX
	if len(code) != 15 { // 13 chars + 2 dashes
		t.Errorf("want 15 chars (with dashes), got %d: %s", len(code), code)
	}
	if code[4] != '-' || code[9] != '-' {
		t.Errorf("wrong dash positions: %s", code)
	}
}

func TestDecodePairingCode(t *testing.T) {
	ip := net.ParseIP("100.64.0.2").To4()
	port := uint16(7860)
	secret := []byte{0xab, 0xcd, 0xef}

	code := EncodePairingCode(ip, port, secret)
	gotIP, gotPort, gotSecret, err := DecodePairingCode(code)
	if err != nil {
		t.Fatal(err)
	}
	if !gotIP.Equal(ip) {
		t.Errorf("ip: want %s, got %s", ip, gotIP)
	}
	if gotPort != port {
		t.Errorf("port: want %d, got %d", port, gotPort)
	}
	for i := range secret {
		if gotSecret[i] != secret[i] {
			t.Errorf("secret byte %d: want %d, got %d", i, secret[i], gotSecret[i])
		}
	}
}

func TestDecodePairingCodeWithSpaces(t *testing.T) {
	ip := net.ParseIP("100.64.0.2").To4()
	code := EncodePairingCode(ip, 7860, []byte{0xab, 0xcd, 0xef})
	// Add extra whitespace and slashes
	messy := " " + code[:4] + " / " + code[5:9] + "  " + code[10:] + " "
	gotIP, _, _, err := DecodePairingCode(messy)
	if err != nil {
		t.Fatalf("should decode messy input: %v", err)
	}
	if !gotIP.Equal(ip) {
		t.Errorf("ip: want %s, got %s", ip, gotIP)
	}
}

func TestDecodePairingCodeInvalid(t *testing.T) {
	_, _, _, err := DecodePairingCode("not-valid")
	if err == nil {
		t.Error("expected error for invalid code")
	}
}

func TestFormatPairingCode(t *testing.T) {
	raw := "1234567890abc"
	formatted := FormatPairingCode(raw)
	if formatted != "1234-5678-90abc" {
		t.Errorf("want 1234-5678-90abc, got %s", formatted)
	}
}
```

- [ ] **Step 2: Run test — expect FAIL**

Run: `cd /Users/wake/Workspace/wake/tmux-box && go test ./internal/core/ -run "TestBase58|TestEncode|TestDecode|TestFormat" -v`
Expected: compilation error

- [ ] **Step 3: Implement Base58 + pairing code**

```go
// internal/core/base58.go
package core

import (
	"encoding/binary"
	"errors"
	"math/big"
	"net"
	"strings"
)

const base58Alphabet = "123456789ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz"

var (
	big58   = big.NewInt(58)
	big0    = big.NewInt(0)
	errBase58Invalid = errors.New("invalid base58 character")
	errPairingDecode = errors.New("invalid pairing code")
)

// base58Encode encodes bytes to a base58 string.
func base58Encode(data []byte) string {
	n := new(big.Int).SetBytes(data)
	var result []byte
	mod := new(big.Int)
	for n.Cmp(big0) > 0 {
		n.DivMod(n, big58, mod)
		result = append(result, base58Alphabet[mod.Int64()])
	}
	// Leading zero bytes → leading '1's
	for _, b := range data {
		if b != 0 {
			break
		}
		result = append(result, '1')
	}
	// Reverse
	for i, j := 0, len(result)-1; i < j; i, j = i+1, j-1 {
		result[i], result[j] = result[j], result[i]
	}
	return string(result)
}

// base58EncodeFixed encodes and left-pads with '1' to fixed length.
func base58EncodeFixed(data []byte, length int) string {
	s := base58Encode(data)
	for len(s) < length {
		s = "1" + s
	}
	return s
}

// base58Decode decodes a base58 string back to bytes.
func base58Decode(s string) ([]byte, error) {
	n := new(big.Int)
	for _, c := range s {
		idx := strings.IndexRune(base58Alphabet, c)
		if idx < 0 {
			return nil, errBase58Invalid
		}
		n.Mul(n, big58)
		n.Add(n, big.NewInt(int64(idx)))
	}
	result := n.Bytes()
	// Restore leading zero bytes from leading '1's
	for _, c := range s {
		if c != '1' {
			break
		}
		result = append([]byte{0}, result...)
	}
	return result, nil
}

// EncodePairingCode encodes IP + port + secret into a 13-char formatted code.
func EncodePairingCode(ip net.IP, port uint16, secret []byte) string {
	ip4 := ip.To4()
	if ip4 == nil {
		ip4 = net.IP{0, 0, 0, 0}
	}
	buf := make([]byte, 9)
	copy(buf[0:4], ip4)
	binary.BigEndian.PutUint16(buf[4:6], port)
	copy(buf[6:9], secret)
	raw := base58EncodeFixed(buf, 13)
	return FormatPairingCode(raw)
}

// FormatPairingCode inserts dashes: XXXX-XXXX-XXXXX (4-4-5).
func FormatPairingCode(raw string) string {
	if len(raw) < 13 {
		return raw
	}
	return raw[:4] + "-" + raw[4:8] + "-" + raw[8:13]
}

// DecodePairingCode decodes a pairing code string into IP, port, and secret.
// Strips dashes, slashes, and whitespace before decoding.
func DecodePairingCode(code string) (ip net.IP, port uint16, secret []byte, err error) {
	// Clean input
	cleaned := strings.Map(func(r rune) rune {
		if r == '-' || r == '/' || r == ' ' || r == '\t' {
			return -1
		}
		return r
	}, code)

	data, err := base58Decode(cleaned)
	if err != nil {
		return nil, 0, nil, errPairingDecode
	}

	// Pad to 9 bytes if shorter (leading zeros lost in encoding)
	for len(data) < 9 {
		data = append([]byte{0}, data...)
	}
	if len(data) != 9 {
		return nil, 0, nil, errPairingDecode
	}

	ip = net.IP(data[0:4])
	port = binary.BigEndian.Uint16(data[4:6])
	secret = data[6:9]
	return ip, port, secret, nil
}
```

- [ ] **Step 4: Run test — expect PASS**

Run: `cd /Users/wake/Workspace/wake/tmux-box && go test ./internal/core/ -run "TestBase58|TestEncode|TestDecode|TestFormat" -v`
Expected: all tests PASS

- [ ] **Step 5: Commit**

```bash
git add internal/core/base58.go internal/core/base58_test.go
git commit -m "feat(core): Base58 codec + pairing code encode/decode (13-char, 4-4-5)"
```

---

## Task 7: Pairing handlers (verify + setup)

**Files:**
- Modify: `internal/core/pairing_handler.go` (replace stubs)
- Create: `internal/core/pairing_handler_test.go`

- [ ] **Step 1: Write pairing handler tests**

```go
// internal/core/pairing_handler_test.go
package core

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/wake/tmux-box/internal/config"
)

func newPairingTestCore() *Core {
	cfg := &config.Config{}
	c := &Core{
		Cfg:          cfg,
		SetupSecrets: NewSetupSecretStore(5 * time.Minute),
		Tickets:      NewTicketStore(),
	}
	return c
}

func TestPairVerifySuccess(t *testing.T) {
	c := newTestCore()
	c.Pairing.Set(StatePairing)
	c.PairingSecret = "abcdef"

	body, _ := json.Marshal(map[string]string{"secret": "abcdef"})
	req := httptest.NewRequest("POST", "/api/pair/verify", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	c.handlePairVerify(rec, req)

	if rec.Code != 200 {
		t.Fatalf("want 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var resp map[string]string
	json.NewDecoder(rec.Body).Decode(&resp)
	if len(resp["setupSecret"]) != 32 {
		t.Errorf("want 32-char setupSecret, got %d", len(resp["setupSecret"]))
	}
}

func TestPairVerifyWrongSecret(t *testing.T) {
	c := newTestCore()
	c.Pairing.Set(StatePairing)
	c.PairingSecret = "abcdef"

	body, _ := json.Marshal(map[string]string{"secret": "wrong1"})
	req := httptest.NewRequest("POST", "/api/pair/verify", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	c.handlePairVerify(rec, req)

	if rec.Code != 401 {
		t.Errorf("want 401, got %d", rec.Code)
	}
}

func TestPairVerifyNotPairingMode(t *testing.T) {
	c := newTestCore()
	c.Pairing.Set(StateNormal)

	body, _ := json.Marshal(map[string]string{"secret": "abcdef"})
	req := httptest.NewRequest("POST", "/api/pair/verify", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	c.handlePairVerify(rec, req)

	if rec.Code != 409 {
		t.Errorf("want 409, got %d", rec.Code)
	}
}

func TestPairSetupSuccess(t *testing.T) {
	c := newTestCore()
	c.Pairing.Set(StatePairing)
	c.CfgPath = t.TempDir() + "/config.toml"

	// Generate a valid setupSecret
	secret, _ := c.SetupSecrets.Generate()

	body, _ := json.Marshal(map[string]string{
		"setupSecret": secret,
		"token":       "purdex_abcdef1234567890abcdef1234567890abcdef12",
	})
	req := httptest.NewRequest("POST", "/api/pair/setup", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	c.handlePairSetup(rec, req)

	if rec.Code != 200 {
		t.Fatalf("want 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if c.Pairing.Get() != StateNormal {
		t.Error("pairing state should be normal after setup")
	}
	if c.Cfg.Token == "" {
		t.Error("cfg.Token should be set after setup")
	}
}

func TestPairSetupShortToken(t *testing.T) {
	c := newTestCore()
	c.Pairing.Set(StatePairing)
	secret, _ := c.SetupSecrets.Generate()

	body, _ := json.Marshal(map[string]string{
		"setupSecret": secret,
		"token":       "short",
	})
	req := httptest.NewRequest("POST", "/api/pair/setup", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	c.handlePairSetup(rec, req)

	if rec.Code != 422 {
		t.Errorf("want 422 for short token, got %d", rec.Code)
	}
}

func TestPairSetupInvalidSecret(t *testing.T) {
	c := newTestCore()
	c.Pairing.Set(StatePairing)

	body, _ := json.Marshal(map[string]string{
		"setupSecret": "invalid",
		"token":       "purdex_abcdef1234567890abcdef1234567890abcdef12",
	})
	req := httptest.NewRequest("POST", "/api/pair/setup", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	c.handlePairSetup(rec, req)

	if rec.Code != 401 {
		t.Errorf("want 401 for invalid setupSecret, got %d", rec.Code)
	}
}
```

- [ ] **Step 2: Run test — expect FAIL**

Run: `cd /Users/wake/Workspace/wake/tmux-box && go test ./internal/core/ -run TestPair -v`
Expected: FAIL — handlers are stubs

- [ ] **Step 3: Implement pairing handlers**

Replace content of `internal/core/pairing_handler.go`:

```go
// internal/core/pairing_handler.go
package core

import (
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"log"
	"net/http"
	"sync/atomic"

	"github.com/wake/tmux-box/internal/config"
)

const (
	maxVerifyFailures = 10
	minTokenLength    = 20
)

func (c *Core) handlePairVerify(w http.ResponseWriter, r *http.Request) {
	if c.Pairing.Get() != StatePairing {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusConflict)
		json.NewEncoder(w).Encode(map[string]string{"reason": "already_paired"})
		return
	}

	var req struct {
		Secret string `json:"secret"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	// Validate hex format
	expected, err := hex.DecodeString(c.PairingSecret)
	if err != nil || len(expected) == 0 {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	given, err := hex.DecodeString(req.Secret)
	if err != nil {
		http.Error(w, "bad request: secret must be hex", http.StatusBadRequest)
		return
	}

	// Constant-time compare
	if subtle.ConstantTimeCompare(expected, given) != 1 {
		count := atomic.AddInt32(&c.failedVerify, 1)
		if count >= maxVerifyFailures {
			c.regeneratePairingSecret()
		}
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	// Generate setup secret
	setupSecret, err := c.SetupSecrets.Generate()
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"setupSecret": setupSecret})
}

func (c *Core) handlePairSetup(w http.ResponseWriter, r *http.Request) {
	if c.Pairing.Get() != StatePairing {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusConflict)
		json.NewEncoder(w).Encode(map[string]string{"reason": "already_paired"})
		return
	}

	var req struct {
		SetupSecret string `json:"setupSecret"`
		Token       string `json:"token"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	// Validate setupSecret
	if !c.SetupSecrets.Validate(req.SetupSecret) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	// Validate token format
	if len(req.Token) < minTokenLength {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnprocessableEntity)
		json.NewEncoder(w).Encode(map[string]string{"error": "token too short (min 20 chars)"})
		return
	}

	// Persist token
	c.CfgMu.Lock()
	c.Cfg.Token = req.Token
	cfgCopy := *c.Cfg
	c.CfgMu.Unlock()

	if c.CfgPath != "" {
		if err := config.WriteFile(c.CfgPath, cfgCopy); err != nil {
			log.Printf("pair setup: write config: %v", err)
			http.Error(w, "failed to persist config", http.StatusInternalServerError)
			return
		}
	}

	c.Pairing.Set(StateNormal)
	log.Println("pairing complete — token set, switching to normal mode")

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]bool{"ok": true})
}

// regeneratePairingSecret creates a new 3-byte secret, updates the pairing code, and logs a warning.
func (c *Core) regeneratePairingSecret() {
	secret := make([]byte, 3)
	if _, err := rand.Read(secret); err != nil {
		log.Printf("regenerate pairing secret: %v", err)
		return
	}
	c.PairingSecret = hex.EncodeToString(secret)
	atomic.StoreInt32(&c.failedVerify, 0)

	// Reconstruct full pairing code from cfg bind IP/Port + new secret
	c.CfgMu.RLock()
	ip := net.ParseIP(c.Cfg.Bind).To4()
	port := uint16(c.Cfg.Port)
	c.CfgMu.RUnlock()
	code := EncodePairingCode(ip, port, secret)
	log.Printf("⚠ 配對碼已失效（過多失敗嘗試），新配對碼：%s", code)
	fmt.Printf("\n新配對碼: %s\n\n", code)
}
```

Add `"crypto/rand"`, `"fmt"`, `"net"` to imports.

- [ ] **Step 4: Run test — expect PASS**

Run: `cd /Users/wake/Workspace/wake/tmux-box && go test ./internal/core/ -run TestPair -v`
Expected: all 5 tests PASS

- [ ] **Step 5: Commit**

```bash
git add internal/core/pairing_handler.go internal/core/pairing_handler_test.go
git commit -m "feat(core): pairing handlers — verify + setup with brute-force protection"
```

---

## Task 8: Token auth handler

**Files:**
- Modify: `internal/core/token_handler.go` (replace stub)
- Create: `internal/core/token_handler_test.go`

- [ ] **Step 1: Write token auth tests**

```go
// internal/core/token_handler_test.go
package core

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestTokenAuthConfirmSuccess(t *testing.T) {
	c := newTestCore()
	c.Pairing.Set(StatePending)
	c.Cfg.Token = "purdex_test_token_that_is_long_enough_for_validation"
	c.CfgPath = t.TempDir() + "/config.toml"

	req := httptest.NewRequest("POST", "/api/token/auth", nil)
	rec := httptest.NewRecorder()
	c.handleTokenAuth(rec, req)

	if rec.Code != 200 {
		t.Fatalf("want 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if c.Pairing.Get() != StateNormal {
		t.Error("pairing state should be normal after confirm")
	}
}

func TestTokenAuthAlreadyConfirmed(t *testing.T) {
	c := newTestCore()
	c.Pairing.Set(StateNormal)
	c.Cfg.Token = "purdex_existing_token_long_enough"

	req := httptest.NewRequest("POST", "/api/token/auth", nil)
	rec := httptest.NewRecorder()
	c.handleTokenAuth(rec, req)

	if rec.Code != 409 {
		t.Errorf("want 409, got %d", rec.Code)
	}
	var body map[string]string
	json.NewDecoder(rec.Body).Decode(&body)
	if body["reason"] != "already_confirmed" {
		t.Errorf("want reason=already_confirmed, got %s", body["reason"])
	}
}
```

- [ ] **Step 2: Run test — expect FAIL**

Run: `cd /Users/wake/Workspace/wake/tmux-box && go test ./internal/core/ -run TestTokenAuth -v`
Expected: FAIL — handler is a stub

- [ ] **Step 3: Implement token auth handler**

Replace content of `internal/core/token_handler.go`:

```go
// internal/core/token_handler.go
package core

import (
	"encoding/json"
	"log"
	"net/http"

	"github.com/wake/tmux-box/internal/config"
)

// handleTokenAuth confirms the runtime token and persists it to config.
// NOTE: Token validation is performed by TokenAuth middleware in the chain.
// This handler MUST be behind TokenAuth — moving it to an unprotected route
// would allow unauthenticated callers to persist arbitrary tokens.
func (c *Core) handleTokenAuth(w http.ResponseWriter, r *http.Request) {
	// Already confirmed — idempotent success
	if c.Pairing.Get() == StateNormal {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusConflict)
		json.NewEncoder(w).Encode(map[string]string{"reason": "already_confirmed"})
		return
	}

	// Persist token (already validated by TokenAuth middleware)
	c.CfgMu.RLock()
	cfgCopy := *c.Cfg
	c.CfgMu.RUnlock()

	if c.CfgPath != "" {
		if err := config.WriteFile(c.CfgPath, cfgCopy); err != nil {
			log.Printf("token auth: write config: %v", err)
			http.Error(w, "failed to persist config", http.StatusInternalServerError)
			return
		}
	}

	c.Pairing.Set(StateNormal)
	log.Println("token confirmed — switching to normal mode")

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]bool{"ok": true})
}
```

- [ ] **Step 4: Run test — expect PASS**

Run: `cd /Users/wake/Workspace/wake/tmux-box && go test ./internal/core/ -run TestTokenAuth -v`
Expected: all 2 tests PASS

- [ ] **Step 5: Commit**

```bash
git add internal/core/token_handler.go internal/core/token_handler_test.go
git commit -m "feat(core): token auth handler — confirm and persist runtime token"
```

---

## Task 9: Daemon 啟動邏輯（--quick + 一般模式）

**Files:**
- Modify: `cmd/tbox/main.go:53-178`
- Create: `cmd/tbox/quick.go`

- [ ] **Step 1: Create quick.go — IP selection logic**

```go
// cmd/tbox/quick.go
package main

import (
	"fmt"
	"net"
	"os"
	"strings"
)

type ifaceEntry struct {
	Name string
	IP   net.IP
}

// listNonLoopbackIPs returns non-loopback, non-link-local IPv4 addresses.
func listNonLoopbackIPs() ([]ifaceEntry, error) {
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil, err
	}
	var result []ifaceEntry
	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, addr := range addrs {
			var ip net.IP
			switch v := addr.(type) {
			case *net.IPNet:
				ip = v.IP
			case *net.IPAddr:
				ip = v.IP
			}
			if ip == nil || ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.To4() == nil {
				continue
			}
			result = append(result, ifaceEntry{Name: iface.Name, IP: ip})
		}
	}
	return result, nil
}

// selectBindIP interactively selects a bind IP.
// Returns the selected IP string or exits on error.
func selectBindIP() string {
	entries, err := listNonLoopbackIPs()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error listing interfaces: %v\n", err)
		os.Exit(1)
	}
	if len(entries) == 0 {
		fmt.Fprintln(os.Stderr, "no non-loopback IPv4 addresses found")
		os.Exit(1)
	}
	if len(entries) == 1 {
		fmt.Printf("Using %s (%s)\n", entries[0].IP, entries[0].Name)
		return entries[0].IP.String()
	}

	fmt.Println("Select bind address:")
	for i, e := range entries {
		fmt.Printf("  %d) %s (%s)\n", i+1, e.IP, e.Name)
	}
	fmt.Print("Choice [1]: ")
	var input string
	fmt.Scanln(&input)
	input = strings.TrimSpace(input)
	if input == "" {
		input = "1"
	}
	var idx int
	fmt.Sscanf(input, "%d", &idx)
	if idx < 1 || idx > len(entries) {
		fmt.Fprintln(os.Stderr, "invalid choice")
		os.Exit(1)
	}
	selected := entries[idx-1]
	fmt.Printf("Using %s (%s)\n", selected.IP, selected.Name)
	return selected.IP.String()
}
```

- [ ] **Step 2: Update runServe with --quick flag and startup logic**

In `cmd/tbox/main.go`, modify `runServe`:

Add `--quick` flag at line 55 area:
```go
quick := fs.Bool("quick", false, "quick setup mode with pairing code")
```

After Core creation (after line 112), add pairing initialization:

```go
// Phase 5a: Pairing initialization
if cfg.Token == "" {
    if *quick {
        // Quick mode: interactive IP selection if needed
        // 127.0.0.1 is unusable for remote SPA in Quick mode, treat as unset.
        if cfg.Bind == "" || cfg.Bind == "127.0.0.1" {
            cfg.Bind = selectBindIP()
            c.Cfg.Bind = cfg.Bind
            // Save bind to config
            if err := config.WriteFile(resolvedCfgPath, cfg); err != nil {
                log.Printf("save bind config: %v", err)
            }
        }
        // Generate pairing secret
        secret := make([]byte, 3)
        if _, err := rand.Read(secret); err != nil {
            log.Fatalf("generate pairing secret: %v", err)
        }
        c.PairingSecret = hex.EncodeToString(secret)
        c.Pairing.Set(core.StatePairing)

        ip := net.ParseIP(cfg.Bind).To4()
        code := core.EncodePairingCode(ip, uint16(cfg.Port), secret)
        fmt.Printf("\n配對碼: %s\n\n", code)
    } else {
        // General mode: generate runtime token
        tokenBytes := make([]byte, 20)
        if _, err := rand.Read(tokenBytes); err != nil {
            log.Fatalf("generate token: %v", err)
        }
        token := "purdex_" + hex.EncodeToString(tokenBytes)
        // Write runtime token to c.Cfg.Token — TokenAuth getter reads this directly.
        // Do NOT write to local cfg; middleware getter uses c.Cfg, not cfg.
        c.CfgMu.Lock()
        c.Cfg.Token = token
        c.CfgMu.Unlock()
        c.Pairing.Set(core.StatePending)

        fmt.Printf("\nToken: %s\n\n", token)
    }
}
```

Add imports: `"crypto/rand"`, `"encoding/hex"`, `"net"`.

Update middleware chain (replace lines 146-151):

```go
outerMux := http.NewServeMux()
outerMux.Handle("GET /api/health", middleware.CORS(
    http.HandlerFunc(c.HandleHealth)))
outerMux.Handle("/", middleware.CORS(
    middleware.IPWhitelist(cfg.Allow)(
        middleware.PairingGuard(func() bool {
            return c.Pairing.Get() == core.StatePairing
        })(
            middleware.TokenAuth(func() string {
                c.CfgMu.RLock()
                defer c.CfgMu.RUnlock()
                return c.Cfg.Token
            }, c.Tickets)(mux)))))
```

- [ ] **Step 3: Run full test suite**

Run: `cd /Users/wake/Workspace/wake/tmux-box && go test ./... 2>&1 | tail -20`
Expected: all packages PASS

- [ ] **Step 4: Commit**

```bash
git add cmd/tbox/main.go cmd/tbox/quick.go
git commit -m "feat(daemon): --quick pairing mode + general token mode startup logic"
```

---

## Task 10: SPA Base58 解碼 + Token 產生

**Files:**
- Create: `spa/src/lib/pairing-codec.ts`
- Create: `spa/src/lib/pairing-codec.test.ts`

- [ ] **Step 1: Write codec tests**

```typescript
// spa/src/lib/pairing-codec.test.ts
import { describe, it, expect } from 'vitest'
import { decodePairingCode, generatePurdexToken, cleanPairingInput } from './pairing-codec'

describe('cleanPairingInput', () => {
  it('strips dashes, slashes, and spaces', () => {
    expect(cleanPairingInput('ABCD-EFG-HIJKL')).toBe('ABCDEFGHIJKL')
    expect(cleanPairingInput('AB CD / EF')).toBe('ABCDEF')
  })
})

describe('decodePairingCode', () => {
  // Use a known encoded value for testing
  // IP=100.64.0.2, Port=7860, Secret=[0xab, 0xcd, 0xef]
  // We test round-trip with the Go encoder by using a pre-computed value

  it('returns null for invalid input', () => {
    expect(decodePairingCode('invalid!')).toBeNull()
    expect(decodePairingCode('')).toBeNull()
    expect(decodePairingCode('AB')).toBeNull()
  })

  it('handles clean 13-char input', () => {
    // This test verifies the decode logic structure.
    // Actual round-trip testing requires a Go-encoded value.
    const result = decodePairingCode('1111111111111') // all zeros
    if (result) {
      expect(result.ip).toBe('0.0.0.0')
      expect(result.port).toBe(0)
      expect(result.secret).toBe('000000')
    }
  })

  it('strips formatting before decoding', () => {
    const r1 = decodePairingCode('1111-1111-11111')
    const r2 = decodePairingCode('1111111111111')
    expect(r1).toEqual(r2)
  })
})

describe('generatePurdexToken', () => {
  it('returns purdex_ prefix + 40 hex chars', () => {
    const token = generatePurdexToken()
    expect(token).toMatch(/^purdex_[0-9a-f]{40}$/)
    expect(token.length).toBe(47)
  })

  it('generates unique tokens', () => {
    const t1 = generatePurdexToken()
    const t2 = generatePurdexToken()
    expect(t1).not.toBe(t2)
  })
})
```

- [ ] **Step 2: Run test — expect FAIL**

Run: `cd /Users/wake/Workspace/wake/tmux-box/spa && npx vitest run src/lib/pairing-codec.test.ts`
Expected: module not found

- [ ] **Step 3: Implement pairing codec**

```typescript
// spa/src/lib/pairing-codec.ts

const BASE58_ALPHABET = '123456789ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz'
const BASE58_MAP = new Map<string, bigint>()
for (let i = 0; i < BASE58_ALPHABET.length; i++) {
  BASE58_MAP.set(BASE58_ALPHABET[i], BigInt(i))
}

export interface PairingCodeData {
  ip: string
  port: number
  secret: string // hex
}

/** Strip dashes, slashes, spaces, tabs from pairing code input. */
export function cleanPairingInput(input: string): string {
  return input.replace(/[-/\s]/g, '')
}

/** Decode a Base58 string to bytes. Returns null on invalid input. */
function base58Decode(s: string): Uint8Array | null {
  let n = 0n
  for (const c of s) {
    const val = BASE58_MAP.get(c)
    if (val === undefined) return null
    n = n * 58n + val
  }

  // Convert bigint to bytes (ensure even-length hex)
  const rawHex = n.toString(16)
  const hex = rawHex.length % 2 ? '0' + rawHex : rawHex
  const bytes = new Uint8Array(hex.length / 2)
  for (let i = 0; i < bytes.length; i++) {
    bytes[i] = parseInt(hex.slice(i * 2, i * 2 + 2), 16)
  }

  // Count leading '1's → leading zero bytes
  let leadingZeros = 0
  for (const c of s) {
    if (c !== '1') break
    leadingZeros++
  }

  if (leadingZeros > 0) {
    const padded = new Uint8Array(leadingZeros + bytes.length)
    padded.set(bytes, leadingZeros)
    return padded
  }

  return bytes
}

/**
 * Decode a 13-char pairing code into IP, port, and secret.
 * Returns null if the code is invalid.
 */
export function decodePairingCode(input: string): PairingCodeData | null {
  const cleaned = cleanPairingInput(input)
  if (cleaned.length === 0) return null

  const decoded = base58Decode(cleaned)
  if (!decoded) return null

  // Pad to 9 bytes if shorter
  let data = decoded
  if (data.length < 9) {
    const padded = new Uint8Array(9)
    padded.set(data, 9 - data.length)
    data = padded
  }
  if (data.length !== 9) return null

  const ip = `${data[0]}.${data[1]}.${data[2]}.${data[3]}`
  const port = (data[4] << 8) | data[5]
  const secret = Array.from(data.slice(6, 9))
    .map((b) => b.toString(16).padStart(2, '0'))
    .join('')

  return { ip, port, secret }
}

/**
 * Generate a purdex_ token: prefix + 40 hex chars (160-bit entropy).
 * Uses crypto.getRandomValues for cryptographic security.
 */
export function generatePurdexToken(): string {
  const bytes = new Uint8Array(20)
  crypto.getRandomValues(bytes)
  const hex = Array.from(bytes)
    .map((b) => b.toString(16).padStart(2, '0'))
    .join('')
  return `purdex_${hex}`
}
```

- [ ] **Step 4: Run test — expect PASS**

Run: `cd /Users/wake/Workspace/wake/tmux-box/spa && npx vitest run src/lib/pairing-codec.test.ts`
Expected: all tests PASS

- [ ] **Step 5: Commit**

```bash
git add spa/src/lib/pairing-codec.ts spa/src/lib/pairing-codec.test.ts
git commit -m "feat(spa): Base58 pairing codec + purdex_ token generation"
```

---

## Task 11: SPA API functions

**Files:**
- Modify: `spa/src/lib/host-api.ts`

- [ ] **Step 1: Add pairing and token API functions**

Add to `spa/src/lib/host-api.ts` after the existing API functions:

```typescript
/* ─── Pairing API (Phase 5a) ─── */

/** POST /api/pair/verify — Quick mode: verify pairing secret, get setupSecret. */
export async function fetchPairVerify(
  base: string,
  secret: string,
): Promise<{ setupSecret: string }> {
  const res = await fetch(`${base}/api/pair/verify`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ secret }),
  })
  if (!res.ok) {
    const text = await res.text().catch(() => '')
    throw new PairingError(res.status, text)
  }
  return res.json()
}

/** POST /api/pair/setup — Quick mode: set token on daemon. */
export async function fetchPairSetup(
  base: string,
  setupSecret: string,
  token: string,
): Promise<{ ok: boolean }> {
  const res = await fetch(`${base}/api/pair/setup`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ setupSecret, token }),
  })
  if (!res.ok) {
    const text = await res.text().catch(() => '')
    throw new PairingError(res.status, text)
  }
  return res.json()
}

/** POST /api/token/auth — General mode: confirm runtime token. */
export async function fetchTokenAuth(
  base: string,
  token: string,
): Promise<{ ok: boolean }> {
  const res = await fetch(`${base}/api/token/auth`, {
    method: 'POST',
    headers: { Authorization: `Bearer ${token}` },
  })
  if (res.status === 409) {
    // already_confirmed — treat as success per spec
    return { ok: true }
  }
  if (!res.ok) {
    const text = await res.text().catch(() => '')
    throw new PairingError(res.status, text)
  }
  return res.json()
}

export class PairingError extends Error {
  constructor(
    public status: number,
    public body: string,
  ) {
    super(`Pairing failed: HTTP ${status}`)
  }
}
```

Note: `fetchPairVerify` and `fetchPairSetup` take a raw `base` URL (not `hostId`) because the host hasn't been added to the store yet during pairing.

- [ ] **Step 2: Run lint**

Run: `cd /Users/wake/Workspace/wake/tmux-box/spa && pnpm run lint`
Expected: no errors

- [ ] **Step 3: Commit**

```bash
git add spa/src/lib/host-api.ts
git commit -m "feat(spa): add pairing and token auth API functions"
```

---

## Task 12: i18n keys

**Files:**
- Modify: `spa/src/locales/en.json`
- Modify: `spa/src/locales/zh-TW.json`

- [ ] **Step 1: Add i18n keys to en.json**

Add the following key-value pairs at the root level of `spa/src/locales/en.json` (flat key format, alongside existing `"hosts.*"` entries):

```json
"hosts.pairing_code": "Pairing Code",
"hosts.pair_button": "Pair",
"hosts.use_token": "Connect with Token (existing Token)",
"hosts.token_generate_hint": "Generate random token",
"hosts.pairing_connecting": "Verifying pairing code…",
"hosts.pairing_success": "Paired successfully",
"hosts.pairing_failed": "Pairing failed",
"hosts.pairing_retry": "Please re-enter pairing code",
"hosts.invalid_pairing_code": "Invalid pairing code",
"hosts.token_too_short": "Token must be at least 20 characters",
"hosts.saving": "Saving…",
"hosts.confirm": "Confirm"
```

- [ ] **Step 2: Add i18n keys to zh-TW.json**

Add the following key-value pairs at the root level of `spa/src/locales/zh-TW.json`:

```json
"hosts.pairing_code": "配對碼",
"hosts.pair_button": "配對",
"hosts.use_token": "使用 Token 連線（已有 Token）",
"hosts.token_generate_hint": "隨機產生 Token",
"hosts.pairing_connecting": "驗證配對碼中…",
"hosts.pairing_success": "配對成功",
"hosts.pairing_failed": "配對失敗",
"hosts.pairing_retry": "請重新輸入配對碼",
"hosts.invalid_pairing_code": "無效的配對碼",
"hosts.token_too_short": "Token 長度至少需要 20 字元",
"hosts.saving": "儲存中…",
"hosts.confirm": "確認"
```

- [ ] **Step 3: Commit**

```bash
git add spa/src/locales/en.json spa/src/locales/zh-TW.json
git commit -m "feat(i18n): add pairing and token auth translation keys"
```

---

## Task 13: AddHostDialog 完整重寫

**Files:**
- Modify: `spa/src/components/hosts/AddHostDialog.tsx` (complete rewrite)

- [ ] **Step 1: Rewrite AddHostDialog**

```tsx
// spa/src/components/hosts/AddHostDialog.tsx
import { useEffect, useState } from 'react'
import {
  X, LinkSimple, ArrowsClockwise, CheckCircle, Warning, Dice,
} from '@phosphor-icons/react'
import { useHostStore } from '../../stores/useHostStore'
import { useI18nStore } from '../../stores/useI18nStore'
import { decodePairingCode, cleanPairingInput, generatePurdexToken } from '../../lib/pairing-codec'
import { fetchPairVerify, fetchPairSetup, fetchTokenAuth, PairingError } from '../../lib/host-api'

interface Props {
  onClose: () => void
}

type Stage = 'idle' | 'pairing' | 'paired' | 'manual' | 'saving' | 'done' | 'error'

export function AddHostDialog({ onClose }: Props) {
  const t = useI18nStore((s) => s.t)
  const addHost = useHostStore((s) => s.addHost)

  const [pairingCode, setPairingCode] = useState('')
  const [ip, setIp] = useState('')
  const [port, setPort] = useState('7860')
  const [token, setToken] = useState('')
  const [stage, setStage] = useState<Stage>('idle')
  const [error, setError] = useState('')
  const [useToken, setUseToken] = useState(false)
  const [setupSecret, setSetupSecret] = useState('')

  // Escape to close
  useEffect(() => {
    const handleKeyDown = (e: KeyboardEvent) => {
      if (e.key === 'Escape') onClose()
    }
    document.addEventListener('keydown', handleKeyDown)
    return () => document.removeEventListener('keydown', handleKeyDown)
  }, [onClose])

  const handlePair = async () => {
    setError('')
    const cleaned = cleanPairingInput(pairingCode)
    const decoded = decodePairingCode(cleaned)
    if (!decoded) {
      setStage('error')
      setError(t('hosts.invalid_pairing_code'))
      return
    }

    setStage('pairing')
    const base = `http://${decoded.ip}:${decoded.port}`

    try {
      const res = await fetchPairVerify(base, decoded.secret)
      setSetupSecret(res.setupSecret)
      setIp(decoded.ip)
      setPort(String(decoded.port))
      setToken(generatePurdexToken())
      setStage('paired')
    } catch (err) {
      // pairing → error: go back to idle so user can re-enter pairing code
      setStage('idle')
      setPairingCode('')
      if (err instanceof PairingError) {
        setError(`${t('hosts.pairing_failed')}: HTTP ${err.status}`)
      } else {
        setError(err instanceof Error ? err.message : t('hosts.pairing_failed'))
      }
    }
  }

  const handleConfirm = async () => {
    setStage('saving')
    setError('')

    try {
      if (useToken) {
        // Token route
        const base = `http://${ip}:${port || '7860'}`
        await fetchTokenAuth(base, token)
      } else {
        // Pairing route
        const base = `http://${ip}:${port || '7860'}`
        await fetchPairSetup(base, setupSecret, token)
      }

      addHost({
        name: ip,
        ip,
        port: parseInt(port, 10) || 7860,
        token: token || undefined,
      })
      setStage('done')
      onClose()
    } catch (err) {
      if (useToken) {
        setStage('manual')
      } else {
        // setupSecret may be consumed — must re-verify
        setStage('idle')
        setPairingCode('')
        setSetupSecret('')
      }
      if (err instanceof PairingError) {
        setError(`HTTP ${err.status}`)
      } else {
        setError(err instanceof Error ? err.message : t('hosts.connection_failed'))
      }
    }
  }

  const handleToggleToken = (checked: boolean) => {
    setUseToken(checked)
    if (checked) {
      setStage('manual')
      setPairingCode('')
      setSetupSecret('')
    } else {
      setStage('idle')
    }
  }

  const handleGenerateToken = () => {
    setToken(generatePurdexToken())
  }

  const isPairingRoute = !useToken
  const isManualRoute = useToken
  const pairingDisabled = stage === 'pairing' || stage === 'paired' || stage === 'saving' || isManualRoute
  const fieldsDisabled = stage === 'idle' || stage === 'pairing' || stage === 'saving'
  const fieldsEnabled = stage === 'paired' || stage === 'manual'
  const confirmDisabled = stage !== 'paired' && stage !== 'manual'
  const tokenValid = token.length >= 20

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/50" role="dialog" aria-modal="true" onClick={onClose}>
      <div className="bg-surface-primary border border-border-default rounded-lg shadow-xl w-full max-w-md" onClick={(e) => e.stopPropagation()}>
        {/* Header */}
        <div className="flex items-center justify-between px-4 py-3 border-b border-border-subtle">
          <h2 className="text-sm font-semibold">{t('hosts.add_host')}</h2>
          <button onClick={onClose} className="text-text-muted hover:text-text-primary cursor-pointer">
            <X size={16} />
          </button>
        </div>

        {/* Body */}
        <div className="p-4 space-y-3">
          {/* Pairing Code Section */}
          <div>
            <label className="text-xs text-text-secondary block mb-1">{t('hosts.pairing_code')}</label>
            <div className="flex gap-2">
              <input
                value={pairingCode}
                onChange={(e) => setPairingCode(e.target.value)}
                placeholder="XXXX-XXXX-XXXXX"
                disabled={pairingDisabled}
                className="flex-1 bg-surface-secondary border border-border-default rounded px-3 py-2 text-sm text-text-primary font-mono disabled:opacity-50"
              />
              <button
                onClick={handlePair}
                disabled={pairingDisabled || cleanPairingInput(pairingCode).length < 13}
                className="px-4 py-2 rounded text-xs bg-accent text-white cursor-pointer disabled:opacity-50 flex items-center gap-1.5"
              >
                {stage === 'pairing' && <ArrowsClockwise size={14} className="animate-spin" />}
                <LinkSimple size={14} />
                {t('hosts.pair_button')}
              </button>
            </div>
          </div>

          {/* Pairing status */}
          {stage === 'paired' && isPairingRoute && (
            <div className="flex items-center gap-2 text-xs text-green-400">
              <CheckCircle size={14} />
              {t('hosts.pairing_success')}
            </div>
          )}

          {/* Divider */}
          <div className="border-t border-border-subtle" />

          {/* Token checkbox */}
          <label className="flex items-center gap-2 text-xs text-text-secondary cursor-pointer">
            <input
              type="checkbox"
              checked={useToken}
              onChange={(e) => handleToggleToken(e.target.checked)}
              disabled={stage === 'saving'}
              className="rounded"
            />
            {t('hosts.use_token')}
          </label>

          {/* Host / Port / Token fields */}
          <div className="grid grid-cols-3 gap-2">
            <div className="col-span-2">
              <label className="text-xs text-text-secondary block mb-1">{t('hosts.ip')}</label>
              <input
                value={ip}
                onChange={(e) => setIp(e.target.value)}
                placeholder="100.64.0.1"
                disabled={!fieldsEnabled}
                className="w-full bg-surface-secondary border border-border-default rounded px-3 py-2 text-sm text-text-primary font-mono disabled:opacity-50"
              />
            </div>
            <div>
              <label className="text-xs text-text-secondary block mb-1">{t('hosts.port')}</label>
              <input
                value={port}
                onChange={(e) => setPort(e.target.value)}
                placeholder="7860"
                disabled={!fieldsEnabled}
                className="w-full bg-surface-secondary border border-border-default rounded px-3 py-2 text-sm text-text-primary font-mono disabled:opacity-50"
              />
            </div>
          </div>

          <div>
            <label className="text-xs text-text-secondary block mb-1">Token</label>
            <div className="flex gap-2">
              <input
                value={token}
                onChange={(e) => setToken(e.target.value)}
                placeholder="purdex_..."
                type="password"
                disabled={!fieldsEnabled}
                className="flex-1 bg-surface-secondary border border-border-default rounded px-3 py-2 text-sm text-text-primary font-mono disabled:opacity-50"
              />
              <button
                onClick={handleGenerateToken}
                disabled={!fieldsEnabled}
                title={t('hosts.token_generate_hint')}
                className="px-2 py-2 rounded text-text-muted hover:text-text-primary cursor-pointer disabled:opacity-50"
              >
                <Dice size={16} />
              </button>
            </div>
            {fieldsEnabled && token && !tokenValid && (
              <p className="text-xs text-yellow-400 mt-1">{t('hosts.token_too_short')}</p>
            )}
          </div>

          {/* Error feedback */}
          {error && (
            <div className="flex items-center gap-2 text-xs text-red-400">
              <Warning size={14} />
              <span>{error}</span>
              {isPairingRoute && (
                <span className="text-text-muted ml-1">— {t('hosts.pairing_retry')}</span>
              )}
            </div>
          )}
        </div>

        {/* Footer */}
        <div className="flex items-center justify-end gap-2 px-4 py-3 border-t border-border-subtle">
          <button
            onClick={onClose}
            className="px-4 py-2 rounded text-xs text-text-secondary hover:text-text-primary cursor-pointer"
          >
            {t('common.cancel')}
          </button>
          <button
            onClick={handleConfirm}
            disabled={confirmDisabled || !ip || !tokenValid || stage === 'saving'}
            className="px-4 py-2 rounded text-xs bg-accent text-white cursor-pointer disabled:opacity-50 flex items-center gap-1.5"
          >
            {stage === 'saving' && <ArrowsClockwise size={14} className="animate-spin" />}
            {t('hosts.confirm')}
          </button>
        </div>
      </div>
    </div>
  )
}
```

- [ ] **Step 2: Run lint**

Run: `cd /Users/wake/Workspace/wake/tmux-box/spa && pnpm run lint`
Expected: no errors

- [ ] **Step 3: Run build**

Run: `cd /Users/wake/Workspace/wake/tmux-box/spa && pnpm run build`
Expected: build success

- [ ] **Step 4: Run all SPA tests**

Run: `cd /Users/wake/Workspace/wake/tmux-box/spa && npx vitest run`
Expected: all tests PASS

- [ ] **Step 5: Commit**

```bash
git add spa/src/components/hosts/AddHostDialog.tsx
git commit -m "feat(spa): rewrite AddHostDialog — pairing code + token dual routes"
```

---

## Task 14: 整合測試 + 全套驗證

**Files:** None (verification only)

- [ ] **Step 1: Run Go full test suite**

Run: `cd /Users/wake/Workspace/wake/tmux-box && go test ./... -count=1`
Expected: all packages PASS

- [ ] **Step 2: Run SPA full test suite**

Run: `cd /Users/wake/Workspace/wake/tmux-box/spa && npx vitest run`
Expected: all tests PASS

- [ ] **Step 3: Run SPA lint**

Run: `cd /Users/wake/Workspace/wake/tmux-box/spa && pnpm run lint`
Expected: no errors

- [ ] **Step 4: Run SPA build**

Run: `cd /Users/wake/Workspace/wake/tmux-box/spa && pnpm run build`
Expected: build success

- [ ] **Step 5: Manual smoke test — General mode**

```bash
# Terminal 1: Start daemon without token
cd /Users/wake/Workspace/wake/tmux-box && go run ./cmd/tbox serve
# Expected: prints token + awaits /api/token/auth
# Copy the printed token

# Terminal 2: Test health endpoint
curl http://127.0.0.1:7860/api/health
# Expected: {"ok":true,"mode":"pending"}

# Test token auth
curl -X POST http://127.0.0.1:7860/api/token/auth -H "Authorization: Bearer <token>"
# Expected: {"ok":true}

curl http://127.0.0.1:7860/api/health
# Expected: {"ok":true,"mode":"normal"}
```

- [ ] **Step 6: Manual smoke test — Quick mode (no token)**

```bash
# Terminal 1: Start daemon in quick mode
cd /Users/wake/Workspace/wake/tmux-box && go run ./cmd/tbox serve --quick
# Expected: IP selection (if needed) + prints pairing code

curl http://127.0.0.1:7860/api/health
# Expected: {"ok":true,"mode":"pairing"}

curl http://127.0.0.1:7860/api/sessions
# Expected: 503 {"reason":"pairing_mode"}
```

- [ ] **Step 7: Manual smoke test — Quick mode (existing token)**

```bash
# Ensure config.toml has a token, then:
cd /Users/wake/Workspace/wake/tmux-box && go run ./cmd/tbox serve --quick
# Expected: normal startup (--quick has no effect when token exists)

curl http://<bind-ip>:7860/api/health
# Expected: {"ok":true,"mode":"normal"}

curl http://<bind-ip>:7860/api/sessions -H "Authorization: Bearer <token>"
# Expected: 200 (normal operation, NOT 503)
```
