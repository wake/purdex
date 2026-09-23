package hosttransfer

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type harness struct {
	t     *testing.T
	store *Store
	clk   *fakeClock
	mux   *http.ServeMux
}

func newHarnessWith(t *testing.T, gen func() (string, error)) *harness {
	t.Helper()
	clk := newFakeClock()
	s := newStore(clk.now, gen)
	mux := http.NewServeMux()
	(&Module{store: s}).RegisterRoutes(mux)
	return &harness{t: t, store: s, clk: clk, mux: mux}
}

func newHarness(t *testing.T) *harness { return newHarnessWith(t, seqGen()) }

// post sends body and asserts the one header every response carries
// (test 13): Cache-Control: no-store.
func (h *harness) post(path, body string) *httptest.ResponseRecorder {
	h.t.Helper()
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	rec := httptest.NewRecorder()
	h.mux.ServeHTTP(rec, req)
	assert.Equal(h.t, "no-store", rec.Header().Get("Cache-Control"), "%s %d", path, rec.Code)
	return rec
}

func (h *harness) create(body string) *httptest.ResponseRecorder {
	h.t.Helper()
	return h.post("/api/host-transfer", body)
}

func (h *harness) redeem(code string) *httptest.ResponseRecorder {
	h.t.Helper()
	b, err := json.Marshal(map[string]string{"code": code})
	require.NoError(h.t, err)
	return h.post("/api/host-transfer/redeem", string(b))
}

// mustCreate creates body and returns the code.
func (h *harness) mustCreate(body string) string {
	h.t.Helper()
	rec := h.create(body)
	require.Equal(h.t, http.StatusOK, rec.Code, rec.Body.String())
	var out struct {
		Code string `json:"code"`
	}
	require.NoError(h.t, json.Unmarshal(rec.Body.Bytes(), &out))
	return out.Code
}

func assertReason(t *testing.T, rec *httptest.ResponseRecorder, status int, reason string) {
	t.Helper()
	assert.Equal(t, status, rec.Code)
	assert.JSONEq(t, fmt.Sprintf(`{"reason":%q}`, reason), rec.Body.String())
	assert.Equal(t, "application/json", rec.Header().Get("Content-Type"))
}

const oneHost = `{"hosts":[{"name":"mlab","ip":"100.64.0.2","port":7860,"token":"tok"}]}`

// Test 11: create.
func TestCreateOK(t *testing.T) {
	h := newHarness(t)
	rec := h.create(oneHost)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var out struct {
		Code      string `json:"code"`
		ExpiresAt int64  `json:"expiresAt"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &out))
	assert.Equal(t, "C0000000", out.Code)
	assert.Equal(t, h.clk.now().Add(codeTTL).UnixMilli(), out.ExpiresAt)
	assert.Equal(t, "application/json", rec.Header().Get("Content-Type"))
}

func TestCreateBadPayload(t *testing.T) {
	for name, body := range map[string]string{
		"not json":         `{"hosts":[`,
		"empty body":       ``,
		"hosts missing":    `{}`,
		"hosts null":       `{"hosts":null}`,
		"zero rows":        `{"hosts":[]}`,
		"hosts not array":  `{"hosts":{"ip":"x"}}`,
		"unknown field":    `{"hosts":[{"ip":"x"}],"extra":1}`,
		"row is a string":  `{"hosts":["x"]}`,
		"row is an array":  `{"hosts":[["x"]]}`,
		"row is a number":  `{"hosts":[1]}`,
		"row is null":      `{"hosts":[null]}`,
		"one bad row":      `{"hosts":[{"ip":"x"},"y"]}`,
		"trailing garbage": `{"hosts":[{"ip":"x"}]} x`,
		"two values":       `{"hosts":[{"ip":"x"}]}{"hosts":[{"ip":"y"}]}`,
		"top-level array":  `[{"ip":"x"}]`,
	} {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t)
			assertReason(t, h.create(body), http.StatusBadRequest, "bad_payload")
		})
	}
}

func TestCreateCapacity(t *testing.T) {
	h := newHarness(t)
	for i := 0; i < maxLive; i++ {
		h.mustCreate(oneHost)
	}
	assertReason(t, h.create(oneHost), http.StatusTooManyRequests, "capacity")
}

func TestCreateUnavailable(t *testing.T) {
	h := newHarnessWith(t, func() (string, error) { return "", errors.New("entropy") })
	assertReason(t, h.create(oneHost), http.StatusServiceUnavailable, "unavailable")
}

// Test 11: redeem.
func TestRedeemOKReturnsTheRows(t *testing.T) {
	h := newHarness(t)
	code := h.mustCreate(oneHost)
	rec := h.redeem(strings.ToLower(code[:4]) + "-" + code[4:])
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.JSONEq(t, oneHost, rec.Body.String())
	assert.Equal(t, "application/json", rec.Header().Get("Content-Type"))

	assertReason(t, h.redeem(code), http.StatusNotFound, "invalid_code")
}

func TestRedeemInvalidCodeIsOneConstantBody(t *testing.T) {
	h := newHarness(t)
	used := h.mustCreate(oneHost)
	require.Equal(t, http.StatusOK, h.redeem(used).Code)
	expired := h.mustCreate(oneHost)
	h.clk.advance(codeTTL)

	var bodies []string
	for _, code := range []string{wrongCode, used, expired, "x"} {
		rec := h.redeem(code)
		assertReason(t, rec, http.StatusNotFound, "invalid_code")
		bodies = append(bodies, rec.Body.String())
	}
	for _, b := range bodies[1:] {
		assert.Equal(t, bodies[0], b, "byte-identical body")
	}
}

func TestRedeemRateLimitedWithRetryAfter(t *testing.T) {
	h := newHarness(t)
	code := h.mustCreate(oneHost)
	for i := 0; i < failLimit; i++ {
		assertReason(t, h.redeem(wrongCode), http.StatusNotFound, "invalid_code")
	}
	h.clk.advance(2500 * time.Millisecond) // 57.5 s left → rounds up
	rec := h.redeem(code)
	assertReason(t, rec, http.StatusTooManyRequests, "rate_limited")
	assert.Equal(t, "58", rec.Header().Get("Retry-After"))

	h.clk.advance(57*time.Second + 400*time.Millisecond) // 0.1 s left
	rec = h.redeem(code)
	assertReason(t, rec, http.StatusTooManyRequests, "rate_limited")
	assert.Equal(t, "1", rec.Header().Get("Retry-After"))

	h.clk.advance(100 * time.Millisecond)
	assert.Equal(t, http.StatusOK, h.redeem(code).Code)
}

func TestRedeemBadRequestIsNotAFailure(t *testing.T) {
	h := newHarness(t)
	code := h.mustCreate(oneHost)
	bad := []string{
		``,
		`{`,
		`{}`,
		`{"code":""}`,
		`{"code":null}`,
		`{"code":123}`,
		`{"code":"` + code + `","x":1}`,
		`"` + code + `"`,
		`{"code":"` + code + `"} x`,
		`{"code":"` + strings.Repeat("A", redeemBodyCap) + `"}`,
	}
	for round := 0; round < 3; round++ {
		for _, body := range bad {
			assertReason(t, h.post("/api/host-transfer/redeem", body), http.StatusBadRequest, "bad_request")
		}
	}
	// 30 bad requests later, nine wrong guesses still answer invalid_code
	// and the right code still works: none of the 400s was counted.
	for i := 0; i < failLimit-1; i++ {
		assertReason(t, h.redeem(wrongCode), http.StatusNotFound, "invalid_code")
	}
	assert.Equal(t, http.StatusOK, h.redeem(code).Code)
}

// Test 12.
func rowsBody(n int) string {
	rows := make([]string, n)
	for i := range rows {
		rows[i] = fmt.Sprintf(`{"ip":"10.0.0.%d","token":"t%d"}`, i, i)
	}
	return `{"hosts":[` + strings.Join(rows, ",") + `]}`
}

func TestCreateRowLimit(t *testing.T) {
	h := newHarness(t)
	code := h.mustCreate(rowsBody(maxRows))
	rec := h.redeem(code)
	require.Equal(t, http.StatusOK, rec.Code)
	assert.JSONEq(t, rowsBody(maxRows), rec.Body.String())

	assertReason(t, h.create(rowsBody(maxRows+1)), http.StatusBadRequest, "bad_payload")
}

// paddedBody is a valid create body of exactly size bytes.
func paddedBody(t *testing.T, size int) string {
	t.Helper()
	prefix, suffix := `{"hosts":[{"ip":"x","pad":"`, `"}]}`
	body := prefix + strings.Repeat("a", size-len(prefix)-len(suffix)) + suffix
	require.Len(t, body, size)
	require.True(t, json.Valid([]byte(body)))
	return body
}

func TestCreateBodyCap(t *testing.T) {
	require.Equal(t, 65536, createBodyCap)

	h := newHarness(t)
	code := h.mustCreate(paddedBody(t, createBodyCap))
	assert.Equal(t, http.StatusOK, h.redeem(code).Code)

	assertReason(t, h.create(paddedBody(t, createBodyCap+1)), http.StatusRequestEntityTooLarge, "too_large")
}

// Test 14.
func TestNothingIsLogged(t *testing.T) {
	var buf bytes.Buffer
	prevOut, prevFlags := log.Writer(), log.Flags()
	log.SetOutput(&buf)
	t.Cleanup(func() {
		log.SetOutput(prevOut)
		log.SetFlags(prevFlags)
	})

	const secret = "tok-SECRET-4f1c9a"
	body := `{"hosts":[{"ip":"10.0.0.1","token":"` + secret + `"}]}`
	h := newHarness(t)
	code := h.mustCreate(body)
	code2 := h.mustCreate(body)
	require.Equal(t, http.StatusOK, h.redeem(code).Code)
	for i := 0; i < failLimit; i++ {
		h.redeem(wrongCode)
	}
	assert.Equal(t, http.StatusTooManyRequests, h.redeem(code2).Code)
	h.create(`{"hosts":["` + secret + `"]}`)
	h.post("/api/host-transfer/redeem", `{"code":"`+secret+`","x":1}`)
	h.create(paddedBody(t, createBodyCap+1))

	out := buf.String()
	assert.NotContains(t, out, secret)
	assert.NotContains(t, out, code)
	assert.NotContains(t, out, code2)
	assert.NotContains(t, out, wrongCode)
}
