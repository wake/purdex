package client

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/url"
	"time"

	"github.com/wake/purdex/internal/wwd/wire"
)

// ErrClaimLost: wwd no longer accepts the claim (409): the job ended or was given to someone else. The caller stops and discards.
var ErrClaimLost = errors.New("wwd no longer accepts this claim")

// LocksBusyError: RegisterLocal was refused because another job on the host holds a lock the run needs (409 with the keys).
type LocksBusyError struct{ Keys []string }

func (e *LocksBusyError) Error() string { return "locks busy: " + joinKeys(e.Keys) }

func joinKeys(keys []string) string {
	b := bytes.Buffer{}
	for i, k := range keys {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString(k)
	}
	return b.String()
}

// logUploadTimeout bounds one log upload: a log is up to tens of megabytes, which the default call timeout would not allow.
const logUploadTimeout = 60 * time.Second

// RegisterLocal is POST /v1/local-runs (local socket only): it registers a fast-lane run, which wwd records as a running job and
// answers with the claim to renew and to report the result with. A refusal because a lock is held is a *LocksBusyError.
func (c *Client) RegisterLocal(ctx context.Context, req wire.LocalRunRequest) (wire.LocalRunResponse, error) {
	raw, err := c.send(ctx, http.MethodPost, "/v1/local-runs", nil, req, maxBody)
	if err != nil {
		var se *StatusError
		if errors.As(err, &se) && se.Code == http.StatusConflict {
			if b, derr := wire.Decode[wire.LocksBusy]([]byte(se.Body)); derr == nil {
				return wire.LocalRunResponse{}, &LocksBusyError{Keys: b.Keys}
			}
		}
		return wire.LocalRunResponse{}, err
	}
	r, err := wire.Decode[wire.LocalRunResponse](raw)
	if err != nil {
		return wire.LocalRunResponse{}, errBad(err)
	}
	return r, nil
}

// Heartbeat is POST /v1/jobs/{id}/heartbeat: it renews the claim; Cancel in the answer asks the holder to stop. A claim wwd no
// longer accepts is ErrClaimLost.
func (c *Client) Heartbeat(ctx context.Context, id, claimID string) (wire.HeartbeatResponse, error) {
	if err := checkID(id); err != nil {
		return wire.HeartbeatResponse{}, err
	}
	raw, err := c.send(ctx, http.MethodPost, "/v1/jobs/"+url.PathEscape(id)+"/heartbeat", nil, wire.HeartbeatRequest{ClaimID: claimID}, maxBody)
	if err != nil {
		return wire.HeartbeatResponse{}, claimErr(err)
	}
	r, err := wire.Decode[wire.HeartbeatResponse](raw)
	if err != nil {
		return wire.HeartbeatResponse{}, errBad(err)
	}
	return r, nil
}

// PostResult is POST /v1/jobs/{id}/result. It is idempotent for a claim; a claim wwd no longer accepts is ErrClaimLost.
func (c *Client) PostResult(ctx context.Context, id string, r wire.ResultRequest) error {
	if err := checkID(id); err != nil {
		return err
	}
	if err := r.Validate(); err != nil {
		return err
	}
	_, err := c.send(ctx, http.MethodPost, "/v1/jobs/"+url.PathEscape(id)+"/result", nil, r, maxBody)
	return claimErr(err)
}

// PutLog is PUT /v1/jobs/{id}/log?claim_id=…: the job's log, stored when the job ends. A claim wwd no longer accepts is
// ErrClaimLost.
func (c *Client) PutLog(ctx context.Context, id, claimID string, log []byte) error {
	if err := checkID(id); err != nil {
		return err
	}
	if !wire.ValidClaimID(claimID) {
		return errors.New("invalid claim id")
	}
	q := url.Values{"claim_id": {claimID}}
	_, err := c.sendRaw(ctx, http.MethodPut, "/v1/jobs/"+url.PathEscape(id)+"/log", q, bytes.NewReader(log), "text/plain; charset=utf-8", logUploadTimeout, maxBody)
	return claimErr(err)
}

// claimErr turns a 409 into ErrClaimLost and leaves other errors alone.
func claimErr(err error) error {
	var se *StatusError
	if errors.As(err, &se) && se.Code == http.StatusConflict {
		return ErrClaimLost
	}
	return err
}

func errBad(err error) error { return errors.Join(ErrBadResponse, err) }
