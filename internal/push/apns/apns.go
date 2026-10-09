// Package apns is the push module's client of Apple's push service: the provider token (ES256 JWT) and one HTTP/2 request
// per notification (push spec docs/specs/2026-10-09-push-spec.md §7). It knows nothing about devices or triggers.
package apns

import (
	"context"
	"crypto/ecdsa"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/wake/purdex/internal/push/apnskey"
)

// tokenLifetime: Apple wants a provider token renewed no more often than every 20 minutes and rejects one older than an
// hour; 40 minutes sits between.
const tokenLifetime = 40 * time.Minute

// Signer makes and caches the provider token.
type Signer struct {
	key apnskey.Key
	now func() time.Time

	mu      sync.Mutex
	token   string
	madeAt  time.Time
	haveTok bool
}

func NewSigner(key apnskey.Key, now func() time.Time) *Signer {
	if now == nil {
		now = time.Now
	}
	return &Signer{key: key, now: now}
}

// Token is the cached JWT while it is younger than 40 minutes, else a new one.
func (s *Signer) Token() (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.haveTok && s.now().Sub(s.madeAt) < tokenLifetime {
		return s.token, nil
	}
	now := s.now()
	header, _ := json.Marshal(map[string]string{"alg": "ES256", "kid": s.key.KeyID})
	claims, _ := json.Marshal(map[string]any{"iss": s.key.TeamID, "iat": now.Unix()})
	enc := base64.RawURLEncoding
	signing := enc.EncodeToString(header) + "." + enc.EncodeToString(claims)
	sum := sha256.Sum256([]byte(signing))
	r, sg, err := ecdsa.Sign(rand.Reader, s.key.Private, sum[:])
	if err != nil {
		return "", errors.New("apns: cannot sign the provider token")
	}
	sig := make([]byte, 64) // r || s, 32 bytes each
	r.FillBytes(sig[:32])
	sg.FillBytes(sig[32:])
	s.token, s.madeAt, s.haveTok = signing+"."+enc.EncodeToString(sig), now, true
	return s.token, nil
}

// Invalidate drops the cached token (APNs said it was expired or invalid).
func (s *Signer) Invalidate() {
	s.mu.Lock()
	s.haveTok = false
	s.mu.Unlock()
}

// DefaultHosts are Apple's push hosts by the device's env.
var DefaultHosts = map[string]string{
	"sandbox":    "https://api.sandbox.push.apple.com",
	"production": "https://api.push.apple.com",
}

// Class is what one answer means for the sender (spec §7).
type Class int

const (
	OK          Class = iota // 200
	Remove                   // the device token is dead: 410, 400 BadDeviceToken / DeviceTokenNotForTopic, Unregistered
	JWTRejected              // 403 ExpiredProviderToken / InvalidProviderToken: renew the token and try once more
	RetryLater               // 429, 5xx, a network error: one retry after a pause
	Rejected                 // any other answer: record it, no retry
)

// Headers are the per-notification request headers.
type Headers struct {
	Topic      string
	CollapseID string
	Expiration time.Time
}

// Result is one send's outcome. Reason is APNs's `reason`; Err describes a failure that never got an answer. Neither ever
// holds the device token (a transport error from net/http would, in its URL, so it is replaced).
type Result struct {
	Class  Class
	Status int
	Reason string
	APNsID string
	Err    string
}

// Client sends notifications. One Client (and so one connection pool) serves all devices.
type Client struct {
	HTTP   *http.Client
	Hosts  map[string]string // env -> base URL; DefaultHosts when nil
	Signer *Signer
	Now    func() time.Time
}

func (c *Client) timeout() time.Duration { return 10 * time.Second }

func (c *Client) hosts() map[string]string {
	if c.Hosts != nil {
		return c.Hosts
	}
	return DefaultHosts
}

// Send delivers payload to one device token.
func (c *Client) Send(ctx context.Context, env, token string, h Headers, payload []byte) Result {
	base, ok := c.hosts()[env]
	if !ok {
		return Result{Class: Rejected, Err: "unknown env"}
	}
	jwt, err := c.Signer.Token()
	if err != nil {
		return Result{Class: Rejected, Err: err.Error()}
	}
	ctx, cancel := context.WithTimeout(ctx, c.timeout())
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(base, "/")+"/3/device/"+token, strings.NewReader(string(payload)))
	if err != nil {
		return Result{Class: Rejected, Err: "cannot build the request"}
	}
	req.Header.Set("Authorization", "bearer "+jwt)
	req.Header.Set("Apns-Topic", h.Topic)
	req.Header.Set("Apns-Push-Type", "alert")
	req.Header.Set("Apns-Priority", "10")
	if !h.Expiration.IsZero() {
		req.Header.Set("Apns-Expiration", strconv.FormatInt(h.Expiration.Unix(), 10))
	}
	if h.CollapseID != "" {
		req.Header.Set("Apns-Collapse-Id", h.CollapseID)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return Result{Class: RetryLater, Err: "network error"} // never err.Error(): a *url.Error carries the URL, i.e. the token
	}
	defer resp.Body.Close()
	var body struct {
		Reason string `json:"reason"`
	}
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	_ = json.Unmarshal(raw, &body)
	return Result{Class: classify(resp.StatusCode, body.Reason), Status: resp.StatusCode, Reason: body.Reason, APNsID: resp.Header.Get("apns-id")}
}

func classify(status int, reason string) Class {
	switch {
	case status == 200:
		return OK
	case status == 410, reason == "Unregistered", reason == "BadDeviceToken", reason == "DeviceTokenNotForTopic":
		return Remove
	case status == 403 && (reason == "ExpiredProviderToken" || reason == "InvalidProviderToken"):
		return JWTRejected
	case status == 429, status >= 500:
		return RetryLater
	default:
		return Rejected
	}
}
