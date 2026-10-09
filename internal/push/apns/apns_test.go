package apns

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/wake/purdex/internal/push/apnskey"
)

func testKey(t *testing.T) apnskey.Key {
	t.Helper()
	k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return apnskey.Key{KeyID: "KEY1234567", TeamID: "TEAM123456", Private: k}
}

type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) now() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return c.t }
func (c *clock) add(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

func newClock() *clock { return &clock{t: time.Unix(1_700_000_000, 0)} }

func verifyJWT(t *testing.T, tok string, pub *ecdsa.PublicKey) (header, claims map[string]any) {
	t.Helper()
	parts := strings.Split(tok, ".")
	if len(parts) != 3 {
		t.Fatalf("jwt has %d parts", len(parts))
	}
	dec := func(s string) []byte {
		b, err := base64.RawURLEncoding.DecodeString(s)
		if err != nil {
			t.Fatalf("not base64url without padding: %v", err)
		}
		return b
	}
	if err := json.Unmarshal(dec(parts[0]), &header); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(dec(parts[1]), &claims); err != nil {
		t.Fatal(err)
	}
	sig := dec(parts[2])
	if len(sig) != 64 {
		t.Fatalf("signature is %d bytes, want r||s of 32 each", len(sig))
	}
	sum := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	if !ecdsa.Verify(pub, sum[:], new(big.Int).SetBytes(sig[:32]), new(big.Int).SetBytes(sig[32:])) {
		t.Fatal("signature does not verify")
	}
	return header, claims
}

func TestSigner_TokenShapeAndSignature(t *testing.T) {
	key, c := testKey(t), newClock()
	s := NewSigner(key, c.now)
	tok, err := s.Token()
	if err != nil {
		t.Fatal(err)
	}
	h, cl := verifyJWT(t, tok, &key.Private.PublicKey)
	if h["alg"] != "ES256" || h["kid"] != "KEY1234567" {
		t.Fatalf("header = %v", h)
	}
	if cl["iss"] != "TEAM123456" || int64(cl["iat"].(float64)) != c.now().Unix() {
		t.Fatalf("claims = %v", cl)
	}
}

func TestSigner_ReusedFor40MinutesThenRenewed(t *testing.T) {
	key, c := testKey(t), newClock()
	s := NewSigner(key, c.now)
	first, _ := s.Token()
	c.add(39 * time.Minute)
	if again, _ := s.Token(); again != first {
		t.Fatal("a token younger than 40 minutes must be reused")
	}
	c.add(2 * time.Minute)
	renewed, _ := s.Token()
	if renewed == first {
		t.Fatal("a token older than 40 minutes must be renewed")
	}
	_, cl := verifyJWT(t, renewed, &key.Private.PublicKey)
	if int64(cl["iat"].(float64)) != c.now().Unix() {
		t.Fatalf("renewed iat = %v", cl["iat"])
	}
}

func TestSigner_InvalidateForcesANewToken(t *testing.T) {
	key, c := testKey(t), newClock()
	s := NewSigner(key, c.now)
	first, _ := s.Token()
	c.add(time.Second) // a new iat, so a new token differs
	s.Invalidate()
	if again, _ := s.Token(); again == first {
		t.Fatal("Invalidate must force a new token")
	}
}

// A fake APNs: TLS + HTTP/2, recording what it was asked and answering with a scripted status / reason.
type fakeAPNS struct {
	*httptest.Server
	mu       sync.Mutex
	requests []recorded
	status   int
	reason   string
}

type recorded struct {
	Path   string
	Proto  int
	Header http.Header
	Body   string
	Method string
}

func newFake(t *testing.T) *fakeAPNS {
	t.Helper()
	f := &fakeAPNS{status: 200}
	f.Server = httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		f.mu.Lock()
		f.requests = append(f.requests, recorded{Path: r.URL.Path, Proto: r.ProtoMajor, Header: r.Header.Clone(), Body: string(b), Method: r.Method})
		status, reason := f.status, f.reason
		f.mu.Unlock()
		w.Header().Set("apns-id", "APNS-ID-1")
		w.WriteHeader(status)
		if reason != "" {
			_ = json.NewEncoder(w).Encode(map[string]string{"reason": reason})
		}
	}))
	f.EnableHTTP2 = true
	f.StartTLS()
	t.Cleanup(f.Close)
	return f
}

func (f *fakeAPNS) script(status int, reason string) {
	f.mu.Lock()
	f.status, f.reason = status, reason
	f.mu.Unlock()
}

func (f *fakeAPNS) last() recorded {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.requests[len(f.requests)-1]
}

func newTestClient(t *testing.T, f *fakeAPNS) (*Client, *clock) {
	t.Helper()
	c := newClock()
	cl := &Client{
		HTTP:   f.Client(),
		Hosts:  map[string]string{"sandbox": f.URL, "production": f.URL},
		Signer: NewSigner(testKey(t), c.now),
		Now:    c.now,
	}
	return cl, c
}

const devToken = "ab12cd34ab12cd34ab12cd34ab12cd34ab12cd34ab12cd34ab12cd34ab12cd34"

func TestSend_RequestShape(t *testing.T) {
	f := newFake(t)
	cl, c := newTestClient(t, f)
	res := cl.Send(context.Background(), "sandbox", devToken, Headers{Topic: "tw.protype.purdex", CollapseID: "ap1", Expiration: c.now().Add(time.Hour)}, []byte(`{"aps":{}}`))
	if res.Class != OK || res.APNsID != "APNS-ID-1" {
		t.Fatalf("result = %+v", res)
	}
	r := f.last()
	if r.Method != "POST" || r.Path != "/3/device/"+devToken {
		t.Fatalf("request = %s %s", r.Method, r.Path)
	}
	if r.Proto != 2 {
		t.Fatalf("HTTP/%d, want 2", r.Proto)
	}
	h := r.Header
	if !strings.HasPrefix(h.Get("Authorization"), "bearer ") || h.Get("Apns-Topic") != "tw.protype.purdex" || h.Get("Apns-Push-Type") != "alert" ||
		h.Get("Apns-Priority") != "10" || h.Get("Apns-Collapse-Id") != "ap1" || h.Get("Apns-Expiration") != "1700003600" {
		t.Fatalf("headers = %v", h)
	}
	if r.Body != `{"aps":{}}` {
		t.Fatalf("body = %q", r.Body)
	}
}

func TestSend_EnvPicksTheHost(t *testing.T) {
	sandbox, production := newFake(t), newFake(t)
	cl, _ := newTestClient(t, sandbox)
	cl.Hosts = map[string]string{"sandbox": sandbox.URL, "production": production.URL}
	cl.HTTP = sandbox.Client()
	// the two fakes have different CAs; use one client that trusts both by sending only to sandbox here
	_ = cl.Send(context.Background(), "sandbox", devToken, Headers{Topic: "t"}, []byte(`{}`))
	if len(sandbox.requests) != 1 || len(production.requests) != 0 {
		t.Fatalf("sandbox %d production %d", len(sandbox.requests), len(production.requests))
	}
	if res := cl.Send(context.Background(), "nowhere", devToken, Headers{Topic: "t"}, []byte(`{}`)); res.Class != Rejected {
		t.Fatalf("unknown env: %+v", res)
	}
	if DefaultHosts["sandbox"] != "https://api.sandbox.push.apple.com" || DefaultHosts["production"] != "https://api.push.apple.com" {
		t.Fatalf("default hosts = %v", DefaultHosts)
	}
}

func TestSend_AnswerClasses(t *testing.T) {
	f := newFake(t)
	cl, _ := newTestClient(t, f)
	for name, tc := range map[string]struct {
		status int
		reason string
		want   Class
	}{
		"ok":                {200, "", OK},
		"gone":              {410, "Unregistered", Remove},
		"gone no reason":    {410, "", Remove},
		"bad device token":  {400, "BadDeviceToken", Remove},
		"not for topic":     {400, "DeviceTokenNotForTopic", Remove},
		"unregistered 400":  {400, "Unregistered", Remove},
		"expired provider":  {403, "ExpiredProviderToken", JWTRejected},
		"invalid provider":  {403, "InvalidProviderToken", JWTRejected},
		"other 403":         {403, "Forbidden", Rejected},
		"too many":          {429, "TooManyRequests", RetryLater},
		"server error":      {500, "InternalServerError", RetryLater},
		"unavailable":       {503, "ServiceUnavailable", RetryLater},
		"payload too large": {413, "PayloadTooLarge", Rejected},
		"other 400":         {400, "BadTopic", Rejected},
	} {
		t.Run(name, func(t *testing.T) {
			f.script(tc.status, tc.reason)
			res := cl.Send(context.Background(), "sandbox", devToken, Headers{Topic: "t"}, []byte(`{}`))
			if res.Class != tc.want || res.Status != tc.status || res.Reason != tc.reason {
				t.Fatalf("result = %+v, want class %v", res, tc.want)
			}
		})
	}
}

func TestSend_ANetworkErrorIsRetryLaterAndNeverCarriesTheToken(t *testing.T) {
	f := newFake(t)
	cl, _ := newTestClient(t, f)
	f.Close() // nothing listens any more
	res := cl.Send(context.Background(), "sandbox", devToken, Headers{Topic: "t"}, []byte(`{}`))
	if res.Class != RetryLater || res.Status != 0 {
		t.Fatalf("result = %+v", res)
	}
	if strings.Contains(res.Reason, devToken) || strings.Contains(res.Err, devToken) {
		t.Fatalf("the device token leaked into the result: %+v", res)
	}
}

func TestSend_TheRequestHasATimeout(t *testing.T) {
	cl := &Client{HTTP: &http.Client{}}
	if cl.timeout() != 10*time.Second {
		t.Fatalf("timeout = %v", cl.timeout())
	}
}
