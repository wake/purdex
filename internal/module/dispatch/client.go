package dispatch

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// SchemaVersion is the M0 wire-contract schema_version. Every request carries it
// and every success response is validated against it; a mismatch is an explicit
// (non-silent) reject via ErrSchemaIncompatible (contract §… / m0-contract.md §2).
const SchemaVersion = 1

// maxResponseBytes bounds how much of a Ploom response we buffer, guarding
// against a misbehaving/hostile server streaming an unbounded body.
const maxResponseBytes = 4 << 20 // 4 MiB

// defaultTimeout is the per-request timeout for Ploom calls.
const defaultTimeout = 30 * time.Second

// Sentinel errors let the consumer worker branch on wire outcomes with
// errors.Is without string matching. They mirror the m0-contract error taxonomy
// (m0-contract.md §7).
var (
	// ErrAlreadyClaimed — claim race, another daemon already claimed (409). The
	// worker skips the dispatch and does not retry.
	ErrAlreadyClaimed = errors.New("dispatch already claimed by another daemon")
	// ErrDispatchNotFound — dispatch does not exist OR does not belong to this
	// daemon (404, fail-closed: the two are indistinguishable by design). Skip.
	ErrDispatchNotFound = errors.New("dispatch not found")
	// ErrSchemaIncompatible — schema_version mismatch (400). Permanent; needs a
	// version upgrade, never retried.
	ErrSchemaIncompatible = errors.New("schema incompatible")
	// ErrUnauthorized — token invalid or lacks permission (401/403). Permanent
	// failure; the worker logs and does not retry.
	ErrUnauthorized = errors.New("unauthorized")
	// ErrAcceptedRequired — a lifecycle report (running/completed/failed) was
	// sent before accepted(seq=1) was acked (409 accepted_required). The report
	// sender must (re)send accepted, get it acked, then retry (m0-contract §4/§7).
	ErrAcceptedRequired = errors.New("accepted report must be acked before lifecycle")
)

// PloomError is returned for wire failures that do not map to a sentinel above
// (notably 5xx transient server errors, which callers may choose to retry).
type PloomError struct {
	HTTPStatus int
	Code       string
	Message    string
}

func (e *PloomError) Error() string {
	if e.Code != "" {
		return fmt.Sprintf("ploom error %d (%s): %s", e.HTTPStatus, e.Code, e.Message)
	}
	return fmt.Sprintf("ploom error %d: %s", e.HTTPStatus, e.Message)
}

// Retryable reports whether err is a transient Ploom failure worth retrying
// (5xx). Sentinel errors are never retryable.
func Retryable(err error) bool {
	var pe *PloomError
	if errors.As(err, &pe) {
		return pe.HTTPStatus >= 500
	}
	return false
}

// PendingDispatch is one entry of the E1 minimal poll list.
type PendingDispatch struct {
	DispatchID string `json:"dispatch_id"`
	IssueID    string `json:"issue_id"`
}

// ClaimResult is the outcome of an E2 claim. ExecutionID is populated only on an
// idempotent same-daemon re-claim where Ploom already knows the execution_id.
type ClaimResult struct {
	DispatchID  string
	Status      string
	ExecutionID string
}

// Issue is the issue payload returned by the E3 two-phase fetch.
type Issue struct {
	IssueID string `json:"issue_id"`
	Title   string `json:"title"`
	Body    string `json:"body"`
}

// RepoLocation echoes the S5 repo_location record (m0-contract.md §2).
type RepoLocation struct {
	ProjectID string `json:"project_id"`
	RemoteURL string `json:"remote_url,omitempty"`
	Owner     string `json:"owner,omitempty"`
	Repo      string `json:"repo,omitempty"`
	LocalDir  string `json:"local_dir"`
	IsOrigin  bool   `json:"is_origin"`
}

// DispatchDetail is the full E3 fetch payload: issue + repo_location + the
// requested sandbox_profile (which admission may only clamp downward, P.5/P.10).
type DispatchDetail struct {
	DispatchID     string       `json:"dispatch_id"`
	Issue          Issue        `json:"issue"`
	RepoLocation   RepoLocation `json:"repo_location"`
	SandboxProfile string       `json:"sandbox_profile"`
}

// Client is the Purdex-side HTTP client for the Ploom daemon endpoints
// (E1 poll / E2 claim / E3 fetch). It is safe for concurrent use.
type Client struct {
	baseURL string
	token   string
	http    *http.Client
}

// ClientOption customises a Client.
type ClientOption func(*Client)

// WithHTTPClient overrides the underlying *http.Client (e.g. in tests).
func WithHTTPClient(hc *http.Client) ClientOption {
	return func(c *Client) { c.http = hc }
}

// WithTimeout sets the per-request timeout when the default *http.Client is used.
func WithTimeout(d time.Duration) ClientOption {
	return func(c *Client) { c.http.Timeout = d }
}

// NewClient builds a Ploom client. baseURL is the Ploom origin (e.g.
// "https://ploom.example"); token is the daemon's S4 Bearer token. Both are
// injected by the caller — never hardcoded (see module wiring).
func NewClient(baseURL, token string, opts ...ClientOption) *Client {
	c := &Client{
		baseURL: strings.TrimRight(baseURL, "/"),
		token:   token,
		http:    &http.Client{Timeout: defaultTimeout},
	}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

// PollPending performs E1: GET /daemon/dispatches?status=pending. It returns the
// minimal pending list scoped to this daemon.
func (c *Client) PollPending(ctx context.Context) ([]PendingDispatch, error) {
	data, err := c.call(ctx, http.MethodGet, "/daemon/dispatches?status=pending", nil)
	if err != nil {
		return nil, err
	}
	var resp struct {
		SchemaVersion int               `json:"schema_version"`
		Dispatches    []PendingDispatch `json:"dispatches"`
	}
	if err := decodeChecked(data, &resp); err != nil {
		return nil, err
	}
	return resp.Dispatches, nil
}

// Claim performs E2: POST /daemon/dispatches/{id}/claim. On the already_claimed
// race it returns ErrAlreadyClaimed; on a missing/foreign dispatch,
// ErrDispatchNotFound.
func (c *Client) Claim(ctx context.Context, dispatchID string) (ClaimResult, error) {
	path := "/daemon/dispatches/" + url.PathEscape(dispatchID) + "/claim"
	data, err := c.call(ctx, http.MethodPost, path, nil)
	if err != nil {
		return ClaimResult{}, err
	}
	var resp struct {
		SchemaVersion int    `json:"schema_version"`
		DispatchID    string `json:"dispatch_id"`
		Status        string `json:"status"`
		ExecutionID   string `json:"execution_id"`
	}
	if err := decodeChecked(data, &resp); err != nil {
		return ClaimResult{}, err
	}
	return ClaimResult{
		DispatchID:  resp.DispatchID,
		Status:      resp.Status,
		ExecutionID: resp.ExecutionID,
	}, nil
}

// Fetch performs E3: GET /daemon/dispatches/{id}. It returns the full dispatch
// detail after a successful claim.
func (c *Client) Fetch(ctx context.Context, dispatchID string) (DispatchDetail, error) {
	path := "/daemon/dispatches/" + url.PathEscape(dispatchID)
	data, err := c.call(ctx, http.MethodGet, path, nil)
	if err != nil {
		return DispatchDetail{}, err
	}
	var resp struct {
		SchemaVersion  int          `json:"schema_version"`
		DispatchID     string       `json:"dispatch_id"`
		Issue          Issue        `json:"issue"`
		RepoLocation   RepoLocation `json:"repo_location"`
		SandboxProfile string       `json:"sandbox_profile"`
	}
	if err := decodeChecked(data, &resp); err != nil {
		return DispatchDetail{}, err
	}
	return DispatchDetail{
		DispatchID:     resp.DispatchID,
		Issue:          resp.Issue,
		RepoLocation:   resp.RepoLocation,
		SandboxProfile: resp.SandboxProfile,
	}, nil
}

// ReportResult is the E4 report response: the highest per-execution seq Ploom
// has projected. On a stale_seq (seq ≤ ack_seq) Ploom still replies 200 with the
// current ack_seq — that is not an error, just a no-op the sender treats as a
// cursor advance (m0-contract §4).
type ReportResult struct {
	AckSeq int
}

// Report performs E4: POST /daemon/dispatches/{id}/report with the given
// pre-marshalled JSON payload (built by the outbox/report sender). It returns the
// acked seq on success. On the ordering violation it returns ErrAcceptedRequired
// (409 accepted_required); on auth failure ErrUnauthorized (401/403, permanent);
// on 5xx a Retryable PloomError. A stale_seq is a normal 200 with ack_seq.
func (c *Client) Report(ctx context.Context, dispatchID string, payload []byte) (ReportResult, error) {
	path := "/daemon/dispatches/" + url.PathEscape(dispatchID) + "/report"
	data, err := c.call(ctx, http.MethodPost, path, bytes.NewReader(payload))
	if err != nil {
		return ReportResult{}, err
	}
	var resp struct {
		SchemaVersion int `json:"schema_version"`
		AckSeq        int `json:"ack_seq"`
	}
	if err := decodeChecked(data, &resp); err != nil {
		return ReportResult{}, err
	}
	return ReportResult{AckSeq: resp.AckSeq}, nil
}

// call issues one authenticated request and returns the raw body on HTTP 200, or
// a classified error otherwise.
func (c *Client) call(ctx context.Context, method, path string, body io.Reader) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, body)
	if err != nil {
		return nil, fmt.Errorf("ploom %s %s: build request: %w", method, path, err)
	}
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("X-Purdex-Schema-Version", fmt.Sprintf("%d", SchemaVersion))

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("ploom %s %s: %w", method, path, err)
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		return nil, fmt.Errorf("ploom %s %s: read body: %w", method, path, err)
	}
	if resp.StatusCode == http.StatusOK {
		return data, nil
	}
	return nil, classifyError(resp.StatusCode, data)
}

// decodeChecked unmarshals data into out and validates the response
// schema_version, rejecting a mismatch as ErrSchemaIncompatible so a version
// skew can never be silently mis-parsed (m0-contract.md §2).
func decodeChecked(data []byte, out any) error {
	if err := json.Unmarshal(data, out); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}
	var probe struct {
		SchemaVersion int `json:"schema_version"`
	}
	if err := json.Unmarshal(data, &probe); err != nil {
		return fmt.Errorf("decode schema_version: %w", err)
	}
	if probe.SchemaVersion != SchemaVersion {
		return fmt.Errorf("%w: server responded schema_version=%d (daemon supports %d)",
			ErrSchemaIncompatible, probe.SchemaVersion, SchemaVersion)
	}
	return nil
}

// classifyError maps a non-200 response to a sentinel or PloomError per the
// contract error taxonomy (m0-contract.md §7).
func classifyError(status int, data []byte) error {
	if status == http.StatusUnauthorized || status == http.StatusForbidden {
		return fmt.Errorf("%w (http %d)", ErrUnauthorized, status)
	}
	var er struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	_ = json.Unmarshal(data, &er) // best-effort; empty/garbage body → generic PloomError
	code, msg := er.Error.Code, er.Error.Message
	switch code {
	case "already_claimed":
		return fmt.Errorf("%w: %s", ErrAlreadyClaimed, msg)
	case "dispatch_not_found":
		return fmt.Errorf("%w: %s", ErrDispatchNotFound, msg)
	case "schema_incompatible":
		return fmt.Errorf("%w: %s", ErrSchemaIncompatible, msg)
	case "accepted_required":
		return fmt.Errorf("%w: %s", ErrAcceptedRequired, msg)
	}
	return &PloomError{HTTPStatus: status, Code: code, Message: msg}
}
