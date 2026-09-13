// internal/module/peers/client.go
package peers

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	ipeers "github.com/wake/purdex/internal/peers"
)

// maxRemoteBodyBytes caps a peer's /api/peers response body: large enough
// for any plausible inventory, small enough to bound one bad/malicious peer.
const maxRemoteBodyBytes = 16 * 1024 * 1024 // 16 MiB

// newRemoteClient returns the *http.Client used for every outbound peer
// fetch: a 3 second total timeout, and redirects are never followed — a
// peer's /api/peers should never issue one, so any 3xx is reported to the
// caller as an error rather than silently chased.
func newRemoteClient() *http.Client {
	return &http.Client{
		Timeout: 3 * time.Second,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

// fetchRemote GETs <baseURL>/api/peers with "Authorization: Bearer <bearer>"
// and decodes the body as an Envelope. bearer is sent only to baseURL's own
// host — no redirect is ever followed, so it cannot leak to another host.
// Any non-200 status (including every 3xx, since redirects are never
// followed) is reported as an error "HTTP <code>". The body is capped at
// maxRemoteBodyBytes; exceeding the cap, a transport failure (including a
// client timeout), or a decode failure are also errors.
func fetchRemote(ctx context.Context, client *http.Client, baseURL, bearer string) (ipeers.Envelope, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/api/peers", nil)
	if err != nil {
		return ipeers.Envelope{}, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+bearer)

	resp, err := client.Do(req)
	if err != nil {
		return ipeers.Envelope{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return ipeers.Envelope{}, fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxRemoteBodyBytes+1))
	if err != nil {
		return ipeers.Envelope{}, fmt.Errorf("read response body: %w", err)
	}
	if len(body) > maxRemoteBodyBytes {
		return ipeers.Envelope{}, fmt.Errorf("response body exceeds %d bytes", maxRemoteBodyBytes)
	}

	var env ipeers.Envelope
	if err := json.Unmarshal(body, &env); err != nil {
		return ipeers.Envelope{}, fmt.Errorf("decode response: %w", err)
	}
	return env, nil
}
