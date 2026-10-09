// internal/module/peers/hostcaller.go
package peers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/wake/purdex/internal/config"
	ipeers "github.com/wake/purdex/internal/peers"
)

// maxHostCallRespBytes caps a HostCaller answer (cross-host team spec §3.1
// rule 8): the body is the peer's own text.
const maxHostCallRespBytes = 16 * 1024 * 1024

// UnpairedByPeerAfter is how long a 401 may last, counted from the first
// one, before it is read as the peer having dropped our token (§3.1 rule 6).
const UnpairedByPeerAfter = 10 * time.Minute

// CallClass is how one HostCaller attempt ended. It is the vocabulary the
// outbox pumps (X2c / X3a) act on; HostCaller itself keeps no state.
type CallClass string

const (
	ClassDone         CallClass = "done"         // 2xx from the right host
	ClassTransient    CallClass = "transient"    // transport error, 3xx, 5xx, 429, unreadable answer: retry
	ClassUnauthorized CallClass = "unauthorized" // 401: the peer's PeerAuth does not know our token (see Escalate401)
	ClassUnsupported  CallClass = "unsupported"  // 404 or a non-JSON 403: the route is missing (an older daemon)
	ClassRefused      CallClass = "refused"      // JSON 4xx: the peer's permanent refusal (Code)
	ClassWrongHost    CallClass = "wrong_host"   // the peer is not the host id we addressed
	ClassUnpaired     CallClass = "unpaired"     // no live peer entry carries the host id
	// ClassUnpairedByPeer is only produced by Escalate401.
	ClassUnpairedByPeer CallClass = "unpaired_by_peer"
)

// CallResult is one attempt's outcome. Body is the raw 2xx JSON answer
// (for the caller's kind-specific decode); Code and Detail are the peer's
// refusal, scrubbed of our token and bounded.
type CallResult struct {
	Class  CallClass
	Status int
	Code   string
	Detail string
	Body   json.RawMessage
	Err    error
}

// Escalate401 decides what a 401 means given when the first one of the
// current run was seen: transient for the first 10 minutes, then the peer
// has unpaired us (§3.1 rule 6). The first-seen time lives with the outbox
// entry; this is only the clock rule.
func Escalate401(firstSeen, now time.Time) CallClass {
	if now.Sub(firstSeen) >= UnpairedByPeerAfter {
		return ClassUnpairedByPeer
	}
	return ClassTransient
}

// HostCaller POSTs JSON to a paired host's API (§3.1 rules 1, 6, 8). It
// addresses the peer by host id — the live entry is looked up on every
// call, so an alias deleted and re-created for another host can never
// receive an entry meant for the old one, and a rotated token is used at
// once. No redirect is followed, the answer is capped, one call has
// InterDaemonTimeout.
type HostCaller struct {
	hosts   func() []config.PeerHost
	client  *http.Client
	timeout time.Duration
}

// NewHostCaller returns a caller reading the live peer list from hosts. The
// transport policy is the caller's own: the client is always built here
// (never injected), so redirects stay off and the timeout stays on whatever
// rt is; a nil rt is the default transport.
func NewHostCaller(hosts func() []config.PeerHost, rt http.RoundTripper) *HostCaller {
	client := newDeliverClient()
	client.Transport = rt
	return &HostCaller{hosts: hosts, client: client, timeout: ipeers.InterDaemonTimeout}
}

// HostCaller returns this module's caller over the live config.
func (m *Module) HostCaller() *HostCaller {
	return NewHostCaller(func() []config.PeerHost { return m.configSnapshot().hosts }, nil)
}

// Call POSTs body to <entry.URL><path> for the peer whose host id is
// targetHostID. body must carry "to_host_id": targetHostID (rule 1: the
// receiver refuses what is not addressed to it); a mismatch is a local
// bug and nothing is sent.
func (c *HostCaller) Call(ctx context.Context, targetHostID, path string, body any) CallResult {
	entry, ok := c.entry(targetHostID)
	if !ok {
		return CallResult{Class: ClassUnpaired, Code: "unpaired", Detail: "no paired host carries that host id"}
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return CallResult{Class: ClassRefused, Code: "bad_request_local", Detail: "encode request: " + err.Error(), Err: err}
	}
	var addr struct {
		To string `json:"to_host_id"`
	}
	if json.Unmarshal(raw, &addr) != nil || addr.To != targetHostID {
		return CallResult{Class: ClassRefused, Code: "bad_request_local", Detail: "request to_host_id does not name the target host"}
	}

	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, entry.URL+path, bytes.NewReader(raw))
	if err != nil {
		return CallResult{Class: ClassRefused, Code: "bad_request_local", Detail: "build request: " + err.Error(), Err: err}
	}
	req.Header.Set("Authorization", "Bearer "+entry.Token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.client.Do(req)
	if err != nil {
		return CallResult{Class: ClassTransient, Err: fmt.Errorf("%s", boundRemote(err.Error(), entry.Token))}
	}
	defer resp.Body.Close()
	res := CallResult{Status: resp.StatusCode}

	data, err := io.ReadAll(io.LimitReader(resp.Body, maxHostCallRespBytes+1))
	if err == nil && len(data) > maxHostCallRespBytes {
		err = fmt.Errorf("response body exceeds %d bytes", maxHostCallRespBytes)
	}
	if err != nil {
		res.Class, res.Err = ClassTransient, err
		return res
	}

	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		var ans struct {
			HostID string `json:"host_id"`
		}
		if json.Unmarshal(data, &ans) != nil {
			res.Class, res.Err = ClassTransient, fmt.Errorf("decode response: not JSON")
			return res
		}
		if ans.HostID != targetHostID {
			res.Class, res.Code = ClassWrongHost, "wrong_host"
			res.Detail = "answer names " + boundRemote(ans.HostID, entry.Token)
			return res
		}
		res.Class, res.Body = ClassDone, json.RawMessage(data)
		return res
	}

	var ae ipeers.APIError
	isJSON := json.Unmarshal(data, &ae) == nil && ae.Error != ""
	if isJSON {
		res.Code = boundRemote(ae.Error, entry.Token)
		res.Detail = boundRemote(ae.Detail, entry.Token)
	}
	st := resp.StatusCode
	switch {
	case st == http.StatusUnauthorized:
		res.Class = ClassUnauthorized
	case st == http.StatusNotFound, st == http.StatusForbidden && !isJSON:
		res.Class = ClassUnsupported
	case st == http.StatusTooManyRequests, st >= 500, st < 400:
		res.Class = ClassTransient
	case !isJSON:
		// A plain 4xx other than 401/403/404 (a proxy's 400/408/409) is not
		// the app's verdict; a permanent refusal would consume a FIFO head.
		res.Class = ClassTransient
	case ae.Error == "wrong_host":
		res.Class = ClassWrongHost
	default: // JSON 4xx: the peer's permanent refusal
		res.Class = ClassRefused
	}
	return res
}

// HostCallerKey is the service-registry key of the module's HostCaller: the team module's outbox pumps send through it.
const HostCallerKey = "peers.host-caller"

// Paired says whether a live peer entry carries hostID (rule 1: by host id, never by alias).
func (c *HostCaller) Paired(hostID string) bool {
	_, ok := c.entry(hostID)
	return ok
}

// HostIDOf is the host id of the live peer entry whose alias is alias ("" when there is none or it carries no host id).
func (c *HostCaller) HostIDOf(alias string) string {
	for _, h := range c.hosts() {
		if h.Alias == alias && h.HostID != "" {
			return h.HostID
		}
	}
	return ""
}

// AliasOf is the alias of the live peer entry carrying hostID ("" when unpaired).
func (c *HostCaller) AliasOf(hostID string) string {
	if e, ok := c.entry(hostID); ok {
		return e.Alias
	}
	return ""
}

// TeamCaps fetches the paired host's GET /api/peers envelope and returns its team capabilities as they apply to us
// (rule 7). An older daemon (no "team" in the envelope) supports nothing: empty kinds, allow_team false. An unpaired
// host id, a transport failure, a non-ok envelope or an envelope naming another host is an error.
func (c *HostCaller) TeamCaps(ctx context.Context, hostID string) (ipeers.TeamCaps, error) {
	entry, ok := c.entry(hostID)
	if !ok {
		return ipeers.TeamCaps{}, fmt.Errorf("no paired host carries that host id")
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	env, err := fetchRemote(ctx, c.client, entry.URL, entry.Token)
	if err != nil {
		var se *CapsStatusError
		if errors.As(err, &se) { // the code survives, the text is bounded like any other
			return ipeers.TeamCaps{}, se
		}
		return ipeers.TeamCaps{}, fmt.Errorf("%s", boundRemote(err.Error(), entry.Token))
	}
	if env.HostID != hostID {
		return ipeers.TeamCaps{}, fmt.Errorf("host_id mismatch: got %s", boundRemote(env.HostID, entry.Token))
	}
	if !env.OK {
		return ipeers.TeamCaps{}, fmt.Errorf("peer: %s", boundRemote(env.Error, entry.Token))
	}
	if env.Team == nil {
		return ipeers.TeamCaps{Kinds: []string{}}, nil
	}
	caps := *env.Team
	if caps.Kinds == nil {
		caps.Kinds = []string{}
	}
	return caps, nil
}

// PeerRecords fetches the paired host's GET /api/peers rows (3 s): what a lead host shows of a remote member's context
// and model (cross-host team spec §8). Like TeamCaps: an unpaired host id, a transport failure, a non-ok envelope or one
// naming another host is an error.
func (c *HostCaller) PeerRecords(ctx context.Context, hostID string) ([]ipeers.PeerRecord, error) {
	entry, ok := c.entry(hostID)
	if !ok {
		return nil, fmt.Errorf("no paired host carries that host id")
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	env, err := fetchRemote(ctx, c.client, entry.URL, entry.Token)
	if err != nil {
		return nil, fmt.Errorf("%s", boundRemote(err.Error(), entry.Token))
	}
	if env.HostID != hostID {
		return nil, fmt.Errorf("host_id mismatch: got %s", boundRemote(env.HostID, entry.Token))
	}
	if !env.OK {
		return nil, fmt.Errorf("peer: %s", boundRemote(env.Error, entry.Token))
	}
	return env.Peers, nil
}

// entry is the live peer entry carrying host id — by host id only.
func (c *HostCaller) entry(hostID string) (config.PeerHost, bool) {
	if hostID == "" {
		return config.PeerHost{}, false
	}
	for _, h := range c.hosts() {
		if h.HostID == hostID {
			return h, true
		}
	}
	return config.PeerHost{}, false
}
