package hosttransfer

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// QP-2 (spec §4): pairing entries on the relay.

const (
	pairID  = "00000000-0000-4000-8000-00000000000a"
	pairTok = "pdxd_0123456789abcdef0123456789abcdef"
)

func pairRowJSON(mut func(m map[string]any)) map[string]any {
	m := map[string]any{
		"v": 1, "kind": "pair", "name": "mlab", "ip": "100.64.0.2", "port": 7860, "daemonId": "hostid", "look": map[string]any{},
		"token": pairTok, "deviceId": "d_0123456789ab", "pairingId": pairID,
		"profile": map[string]any{"hostDaemonId": "sot", "profileId": "p_0123456789ab", "name": "Main"},
	}
	if mut != nil {
		mut(m)
	}
	return m
}

type pairHarness struct {
	*harness
}

func newPairHarness(t *testing.T) *pairHarness { return &pairHarness{newHarness(t)} }

func (h *pairHarness) do(method, path, auth, remote string, body any) *httptest.ResponseRecorder {
	var raw []byte
	switch b := body.(type) {
	case nil:
	case string:
		raw = []byte(b)
	default:
		raw, _ = json.Marshal(b)
	}
	req := httptest.NewRequest(method, path, bytes.NewReader(raw))
	req.RemoteAddr = remote
	if auth != "" {
		req.Header.Set("Authorization", auth)
	}
	rec := httptest.NewRecorder()
	h.mux.ServeHTTP(rec, req)
	return rec
}

const tailnetSrc = "100.64.0.9:5555"

func (h *pairHarness) create(rows ...map[string]any) *httptest.ResponseRecorder {
	return h.do("POST", "/api/host-transfer/pairings", h.auth, "127.0.0.1:1", map[string]any{"rows": rows})
}
func (h *pairHarness) claim(remote, code string) *httptest.ResponseRecorder {
	return h.do("POST", "/api/host-transfer/pairings/claim", "", remote, map[string]any{"code": code})
}
func (h *pairHarness) status(code string) *httptest.ResponseRecorder {
	return h.do("GET", "/api/host-transfer/pairings/"+code, h.auth, "127.0.0.1:1", nil)
}
func (h *pairHarness) del(code string) *httptest.ResponseRecorder {
	return h.do("DELETE", "/api/host-transfer/pairings/"+code, h.auth, "127.0.0.1:1", nil)
}

func codeOf(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var b struct {
		Code      string
		ExpiresAt int64
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &b))
	require.NotEmpty(t, b.Code)
	return b.Code
}

func TestPairingCreate_StrictRowValidation(t *testing.T) {
	bad := map[string]func(m map[string]any){
		"unknown key":          func(m map[string]any) { m["extra"] = 1 },
		"v 2":                  func(m map[string]any) { m["v"] = 2 },
		"kind host":            func(m map[string]any) { m["kind"] = "host" },
		"token not pdxd":       func(m map[string]any) { m["token"] = "adm_0123456789abcdef0123456789abcdef" },
		"token wrong length":   func(m map[string]any) { m["token"] = "pdxd_0123" },
		"device id bad":        func(m map[string]any) { m["deviceId"] = "x" },
		"pairing id not uuid":  func(m map[string]any) { m["pairingId"] = "nope" },
		"missing name":         func(m map[string]any) { delete(m, "name") },
		"empty name":           func(m map[string]any) { m["name"] = "" },
		"bad ip":               func(m map[string]any) { m["ip"] = "not-an-ip" },
		"port zero":            func(m map[string]any) { m["port"] = 0 },
		"port too big":         func(m map[string]any) { m["port"] = 70000 },
		"look not object":      func(m map[string]any) { m["look"] = []int{1} },
		"look missing":         func(m map[string]any) { delete(m, "look") },
		"missing profile":      func(m map[string]any) { delete(m, "profile") },
		"profile extra key":    func(m map[string]any) { m["profile"].(map[string]any)["x"] = 1 },
		"profile id bad":       func(m map[string]any) { m["profile"].(map[string]any)["profileId"] = "p_xyz" },
		"profile name missing": func(m map[string]any) { delete(m["profile"].(map[string]any), "name") },
		"token not a string":   func(m map[string]any) { m["token"] = 5 },
		"daemon id empty":      func(m map[string]any) { m["daemonId"] = "" },
	}
	for name, mut := range bad {
		t.Run(name, func(t *testing.T) {
			h := newPairHarness(t)
			assertReason(t, h.create(pairRowJSON(mut)), http.StatusBadRequest, "bad_payload")
			assertUntouched(t, h.store)
		})
	}
	t.Run("mixed pairing ids", func(t *testing.T) {
		h := newPairHarness(t)
		other := pairRowJSON(func(m map[string]any) { m["pairingId"] = "00000000-0000-4000-8000-00000000000b" })
		assertReason(t, h.create(pairRowJSON(nil), other), http.StatusBadRequest, "bad_payload")
	})
	t.Run("mixed profiles", func(t *testing.T) {
		h := newPairHarness(t)
		other := pairRowJSON(func(m map[string]any) { m["profile"].(map[string]any)["name"] = "Other" })
		assertReason(t, h.create(pairRowJSON(nil), other), http.StatusBadRequest, "bad_payload")
	})
	t.Run("no rows", func(t *testing.T) {
		h := newPairHarness(t)
		assertReason(t, h.create(), http.StatusBadRequest, "bad_payload")
	})
	t.Run("too many rows", func(t *testing.T) {
		h := newPairHarness(t)
		var rows []map[string]any
		for i := 0; i < maxRows+1; i++ {
			rows = append(rows, pairRowJSON(nil))
		}
		assertReason(t, h.create(rows...), http.StatusBadRequest, "bad_payload")
	})
	t.Run("unknown top-level key", func(t *testing.T) {
		h := newPairHarness(t)
		rec := h.do("POST", "/api/host-transfer/pairings", h.auth, "127.0.0.1:1", map[string]any{"rows": []any{pairRowJSON(nil)}, "hosts": 1})
		assertReason(t, rec, http.StatusBadRequest, "bad_payload")
	})
	t.Run("a valid row with two hosts", func(t *testing.T) {
		h := newPairHarness(t)
		two := pairRowJSON(func(m map[string]any) { m["daemonId"] = "other"; m["deviceId"] = "d_aaaaaaaaaaaa" })
		codeOf(t, h.create(pairRowJSON(nil), two))
	})
}

func TestPairingCreate_AdminOnlyAndBodyCap(t *testing.T) {
	h := newPairHarness(t)
	for _, auth := range []string{"", "Bearer nope"} {
		rec := h.do("POST", "/api/host-transfer/pairings", auth, "127.0.0.1:1", map[string]any{"rows": []any{pairRowJSON(nil)}})
		assert.Contains(t, []int{http.StatusUnauthorized, http.StatusForbidden}, rec.Code)
	}
	assertUntouched(t, h.store)
	big := pairRowJSON(func(m map[string]any) { m["name"] = strings.Repeat("x", 70<<10) })
	assertReason(t, h.create(big), http.StatusRequestEntityTooLarge, "too_large")
}

func TestPairingCreate_ExpiresInBounds(t *testing.T) {
	cases := map[string]struct {
		in   any
		want int
	}{"default": {nil, 600}, "min": {60, 60}, "max": {600, 600}, "mid": {120, 120}, "below": {59, 0}, "wraps to 60s": {36028797018964028, 0}, "wraps to 600s": {36028797018964568, 0}, "huge": {9223372036854775807, 0}, "above": {601, 0}, "zero": {0, 0}, "negative": {-5, 0}}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			h := newPairHarness(t)
			body := map[string]any{"rows": []any{pairRowJSON(nil)}}
			if c.in != nil {
				body["expires_in_s"] = c.in
			}
			rec := h.do("POST", "/api/host-transfer/pairings", h.auth, "127.0.0.1:1", body)
			if c.want == 0 {
				assert.Equal(t, http.StatusBadRequest, rec.Code)
				return
			}
			code := codeOf(t, rec)
			st := h.status(code)
			var b struct{ ExpiresAt int64 }
			require.NoError(t, json.Unmarshal(st.Body.Bytes(), &b))
			assert.Equal(t, h.clk.now().Add(time.Duration(c.want)*time.Second).UnixMilli(), b.ExpiresAt)
		})
	}
}

func TestPairingClaim_HappyPathIsOneTime(t *testing.T) {
	h := newPairHarness(t)
	code := codeOf(t, h.create(pairRowJSON(nil)))
	rec := h.claim(tailnetSrc, code)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var b struct{ Rows []map[string]any }
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &b))
	require.Len(t, b.Rows, 1)
	assert.Equal(t, pairTok, b.Rows[0]["token"])
	assert.Equal(t, "no-store", rec.Header().Get("Cache-Control"))
	assertReason(t, h.claim(tailnetSrc, code), http.StatusNotFound, "invalid_code")
	// typed forms of the code still work (lower case, dashes) and a claimed one stays gone
	assertReason(t, h.claim(tailnetSrc, strings.ToLower(code)), http.StatusNotFound, "invalid_code")
}

func TestPairingClaim_ATransferCodeIsNotClaimedAndStaysRedeemable(t *testing.T) {
	h := newPairHarness(t)
	tcode := codeOf(t, h.create2transfer())
	assertReason(t, h.claim(tailnetSrc, tcode), http.StatusNotFound, "invalid_code")
	rec := h.redeem(tcode)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
}

// create2transfer parks a Share-hosts transfer through the ordinary route.
func (h *pairHarness) create2transfer() *httptest.ResponseRecorder { return h.harness.create(oneHost) }

func TestPairingRedeem_APairingCodeIsNotRedeemedByTheAdminRoute(t *testing.T) {
	h := newPairHarness(t)
	code := codeOf(t, h.create(pairRowJSON(nil)))
	assertReason(t, h.redeem(code), http.StatusNotFound, "invalid_code")
	// ... and was not consumed
	require.Equal(t, http.StatusOK, h.claim(tailnetSrc, code).Code)
}

func TestPairingClaim_SourceMustBeTailnetOrLoopback(t *testing.T) {
	h := newPairHarness(t)
	code := codeOf(t, h.create(pairRowJSON(nil)))
	for _, src := range []string{"8.8.8.8:1", "192.168.1.5:1", "100.63.255.255:1", "100.128.0.1:1", "[2001:db8::1]:1", "[fd7a:115c:a1e1::1]:1", "bad", ""} {
		assertReason(t, h.claim(src, code), http.StatusForbidden, "forbidden_source")
	}
	// refused sources neither take the code nor count as failures
	assert.Empty(t, h.store.claimFails)
	for _, src := range []string{"100.64.0.1:1", "100.127.255.254:1", "127.0.0.1:1", "[::1]:1", "[fd7a:115c:a1e0::5]:1", "[::ffff:100.64.0.3]:1"} {
		c := codeOf(t, h.create(pairRowJSON(nil)))
		assert.Equal(t, http.StatusOK, h.claim(src, c).Code, src)
	}
}

func TestPairingClaim_LimiterIsPerSourceAndLeavesRedeemAlone(t *testing.T) {
	h := newPairHarness(t)
	code := codeOf(t, h.create(pairRowJSON(nil)))
	bad := "ZZZZZZZZ"
	for i := 0; i < failLimit; i++ {
		assertReason(t, h.claim(tailnetSrc, bad), http.StatusNotFound, "invalid_code")
	}
	rec := h.claim(tailnetSrc, code) // a right code, over the limit: not taken
	assertReason(t, rec, http.StatusTooManyRequests, "rate_limited")
	assert.NotEmpty(t, rec.Header().Get("Retry-After"))
	// another address still claims the same code
	other := h.claim("100.64.0.77:1", code)
	require.Equal(t, http.StatusOK, other.Code, other.Body.String())
	// the admin's transfer redeem is untouched by the claim traffic
	tcode := codeOf(t, h.create2transfer())
	require.Equal(t, http.StatusOK, h.redeem(tcode).Code)
	assert.Zero(t, h.store.failures)
	// the window resets
	h.clk.advance(failWindow)
	c2 := codeOf(t, h.create(pairRowJSON(nil)))
	assert.Equal(t, http.StatusOK, h.claim(tailnetSrc, c2).Code)
}

func TestPairingClaim_RedeemFailuresDoNotCountAgainstClaim(t *testing.T) {
	h := newPairHarness(t)
	for i := 0; i < failLimit+3; i++ {
		h.redeem(wrongCode)
	}
	code := codeOf(t, h.create(pairRowJSON(nil)))
	assert.Equal(t, http.StatusOK, h.claim(tailnetSrc, code).Code)
}

func TestPairingClaim_BodyShape(t *testing.T) {
	h := newPairHarness(t)
	for _, body := range []any{"{}", `{"code":""}`, `{"code":"x","y":1}`, "not json", `{"code":"x"} trailing`} {
		assertReason(t, h.do("POST", "/api/host-transfer/pairings/claim", "", tailnetSrc, body), http.StatusBadRequest, "bad_request")
	}
	assert.Empty(t, h.store.claimFails, "a malformed body is not a guess")
}

func TestPairingStatusAndDelete(t *testing.T) {
	h := newPairHarness(t)
	code := codeOf(t, h.create(pairRowJSON(nil)))

	st := h.status(code)
	require.Equal(t, http.StatusOK, st.Code)
	assert.Contains(t, st.Body.String(), `"claimed":false`)
	assert.NotContains(t, st.Body.String(), "claimedAt")

	require.Equal(t, http.StatusOK, h.claim(tailnetSrc, code).Code)
	h.clk.advance(time.Second)
	st = h.status(code)
	require.Equal(t, http.StatusOK, st.Code)
	assert.Contains(t, st.Body.String(), `"claimed":true`)
	assert.Contains(t, st.Body.String(), "claimedAt")
	assert.NotContains(t, st.Body.String(), pairTok, "a tombstone holds no rows")

	assertReason(t, h.del(code), http.StatusConflict, "claimed")
	assertReason(t, h.del("ZZZZZZZZ"), http.StatusNotFound, "not_found")
	assertReason(t, h.status("ZZZZZZZZ"), http.StatusNotFound, "not_found")

	c2 := codeOf(t, h.create(pairRowJSON(nil)))
	assert.Equal(t, http.StatusNoContent, h.del(c2).Code)
	assertReason(t, h.claim(tailnetSrc, c2), http.StatusNotFound, "invalid_code") // no phone can claim it any more
	assertReason(t, h.status(c2), http.StatusNotFound, "not_found")

	// the tombstone dies at the original expiry
	h.clk.advance(pairingMaxTTL)
	assertReason(t, h.status(code), http.StatusNotFound, "not_found")
}

func TestPairingStatusAndDelete_NeverTouchATransfer(t *testing.T) {
	h := newPairHarness(t)
	tcode := codeOf(t, h.create2transfer())
	assertReason(t, h.status(tcode), http.StatusNotFound, "not_found")
	assertReason(t, h.del(tcode), http.StatusNotFound, "not_found")
	require.Equal(t, http.StatusOK, h.redeem(tcode).Code, "the transfer is still there")
}

func TestPairingStatusAndDelete_NeedTheAdminToken(t *testing.T) {
	h := newPairHarness(t)
	code := codeOf(t, h.create(pairRowJSON(nil)))
	for _, method := range []string{"GET", "DELETE"} {
		for _, auth := range []string{"", "Bearer nope"} {
			rec := h.do(method, "/api/host-transfer/pairings/"+code, auth, "127.0.0.1:1", nil)
			assert.Contains(t, []int{http.StatusUnauthorized, http.StatusForbidden}, rec.Code, method)
		}
	}
	assert.Equal(t, http.StatusOK, h.status(code).Code)
}

// Capacity: the 16 live codes are shared with transfers and count only entries that can still be taken.
func TestPairingCapacity_CountsOnlyTakeableEntries(t *testing.T) {
	clk := newFakeClock()
	s := newStore(clk.now, randomishGen())
	h := &pairHarness{&harness{t: t, store: s, clk: clk, mux: http.NewServeMux(), auth: "Bearer admin-token"}}
	(&Module{store: s, tokenFn: func() string { return "admin-token" }}).RegisterRoutes(h.mux)

	var codes []string
	for i := 0; i < maxLive; i++ {
		codes = append(codes, codeOf(t, h.create(pairRowJSON(nil))))
	}
	assert.Equal(t, http.StatusTooManyRequests, h.create(pairRowJSON(nil)).Code, "the 17th live code is refused")
	assert.Equal(t, http.StatusTooManyRequests, h.create2transfer().Code, "transfers share the cap")
	for _, c := range codes {
		require.Equal(t, http.StatusOK, h.claim(tailnetSrc, c).Code)
	}
	// 16 tombstones are not live codes
	codeOf(t, h.create(pairRowJSON(nil)))
	codeOf(t, h.create2transfer())
}

func randomishGen() func() (string, error) {
	var n atomic.Int64
	return func() (string, error) { return fmt.Sprintf("%08X", n.Add(1)), nil }
}

func TestPairingTombstones_AtMost64OldestDroppedFirst(t *testing.T) {
	clk := newFakeClock()
	s := newStore(clk.now, randomishGen())
	var codes []string
	for i := 0; i < maxTombstones+1; i++ {
		c, _, err := s.CreatePairing(json.RawMessage(`[{}]`), pairingMaxTTL)
		require.NoError(t, err)
		_, _, err = s.Claim(fmt.Sprintf("100.64.0.%d", i%200+1), c)
		require.NoError(t, err)
		codes = append(codes, c)
		clk.advance(time.Millisecond)
	}
	_, ok := s.Status(codes[0])
	assert.False(t, ok, "the 65th tombstone dropped the oldest")
	for _, c := range codes[1:] {
		_, ok := s.Status(c)
		assert.True(t, ok)
	}
	// dropping keeps the cap: another round still holds 64
	c, _, _ := s.CreatePairing(json.RawMessage(`[{}]`), pairingMaxTTL)
	_, _, err := s.Claim("100.64.0.5", c)
	require.NoError(t, err)
	_, ok = s.Status(codes[1])
	assert.False(t, ok)
	assert.Len(t, s.tombs, maxTombstones)
}

// Claim and delete racing: exactly one of "claim returned rows" / "delete returned 204".
func TestPairingClaimVsDelete_ExactlyOneWins(t *testing.T) {
	for round := 0; round < 300; round++ {
		clk := newFakeClock()
		s := newStore(clk.now, randomishGen())
		code, _, err := s.CreatePairing(json.RawMessage(`[{"x":1}]`), pairingMaxTTL)
		require.NoError(t, err)
		var claimed, deleted atomic.Bool
		var wg sync.WaitGroup
		wg.Add(2)
		start := make(chan struct{})
		go func() {
			defer wg.Done()
			<-start
			if rows, _, err := s.Claim("100.64.0.1", code); err == nil && len(rows) > 0 {
				claimed.Store(true)
			}
		}()
		go func() {
			defer wg.Done()
			<-start
			if s.DeletePairing(code) == nil {
				deleted.Store(true)
			}
		}()
		close(start)
		wg.Wait()
		require.NotEqual(t, claimed.Load(), deleted.Load(), "round %d: claimed=%v deleted=%v", round, claimed.Load(), deleted.Load())
	}
}

func TestClaimLimiter_TableIsBoundedAndFailsClosed(t *testing.T) {
	clk := newFakeClock()
	s := newStore(clk.now, randomishGen())
	for i := 0; i < maxClaimSources; i++ {
		_, _, err := s.Claim(fmt.Sprintf("src-%d", i), "ZZZZZZZZ")
		require.ErrorIs(t, err, ErrInvalidCode)
	}
	_, _, err := s.Claim("one-more", "ZZZZZZZZ")
	require.ErrorIs(t, err, ErrRateLimited, "a full table of live windows refuses a new source instead of growing")
	require.Len(t, s.claimFails, maxClaimSources)
	clk.advance(failWindow)
	_, _, err = s.Claim("one-more", "ZZZZZZZZ")
	require.ErrorIs(t, err, ErrInvalidCode, "expired windows are purged")
	require.Less(t, len(s.claimFails), maxClaimSources)
}

func TestPairing_StoppedStoreTakesNothing(t *testing.T) {
	clk := newFakeClock()
	s := newStore(clk.now, randomishGen())
	code, _, _ := s.CreatePairing(json.RawMessage(`[{}]`), pairingMaxTTL)
	s.Stop()
	_, _, err := s.Claim("100.64.0.1", code)
	require.ErrorIs(t, err, ErrStopped)
	_, _, err = s.CreatePairing(json.RawMessage(`[{}]`), pairingMaxTTL)
	require.ErrorIs(t, err, ErrUnavailable)
}
