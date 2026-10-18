// Package localrec records fast-lane runs with the local wwd: it is the fastlane.Recorder that talks to POST /v1/local-runs and then
// to the ordinary claim routes (heartbeat, log, result) with the claim wwd answered with (wwd spec §5.2, §4.2).
package localrec

import (
	"context"
	"errors"
	"net/http"
	"os"
	"time"

	"github.com/wake/purdex/internal/wwd/client"
	"github.com/wake/purdex/internal/wwd/fastlane"
	"github.com/wake/purdex/internal/wwd/submit"
	"github.com/wake/purdex/internal/wwd/wire"
)

// maxLog is the largest log uploaded; a longer one is cut to its last maxLog bytes (the end of a log is what matters).
const maxLog = 8 << 20

// API is the part of the wwd client the recorder uses; *client.Client is one.
type API interface {
	RegisterLocal(ctx context.Context, req wire.LocalRunRequest) (wire.LocalRunResponse, error)
	Heartbeat(ctx context.Context, id, claimID string) (wire.HeartbeatResponse, error)
	PostResult(ctx context.Context, id string, r wire.ResultRequest) error
	PutLog(ctx context.Context, id, claimID string, log []byte) error
}

// Recorder registers fast-lane runs with wwd.
type Recorder struct{ API API }

var _ fastlane.Recorder = Recorder{}

// Register records the run as a running job. A lock held by another job on this host is fastlane.ErrLocksBusy.
func (r Recorder) Register(ctx context.Context, body submit.Body) (fastlane.Handle, error) {
	req := wire.LocalRunRequest{JobSubmission: body}
	if err := req.Validate(); err != nil {
		return nil, err
	}
	resp, err := r.API.RegisterLocal(ctx, req)
	var busy *client.LocksBusyError
	var se *client.StatusError
	switch {
	case errors.As(err, &busy):
		return nil, fastlane.ErrLocksBusy
	case errors.As(err, &se) && (se.Code == http.StatusNotFound || se.Code == http.StatusMethodNotAllowed || se.Code == http.StatusNotImplemented):
		return nil, fastlane.ErrUnrecorded // a wwd that does not have the route yet
	case err != nil:
		return nil, err
	}
	return &handle{api: r.API, resp: resp}, nil
}

type handle struct {
	api  API
	resp wire.LocalRunResponse
}

func (h *handle) JobID() string { return h.resp.ID }

func (h *handle) Lease() time.Duration { return time.Duration(h.resp.LeaseS) * time.Second }

func (h *handle) RenewEvery() time.Duration { return time.Duration(h.resp.RenewS) * time.Second }

func (h *handle) Heartbeat(ctx context.Context) (bool, error) {
	r, err := h.api.Heartbeat(ctx, h.resp.ID, h.resp.ClaimID)
	if errors.Is(err, client.ErrClaimLost) {
		return false, fastlane.ErrStale
	}
	return r.Cancel, err
}

// Finish stores the log and posts the result. The log goes first, so that whoever sees the job end finds it; a log that cannot be
// stored is an error, so the caller retries before the result closes the job. Both calls are idempotent for the claim, so
// the caller may retry the whole of Finish.
func (h *handle) Finish(ctx context.Context, o fastlane.Outcome) error {
	if log := readLog(o.LogPath); log != nil {
		if err := h.api.PutLog(ctx, h.resp.ID, h.resp.ClaimID, log); err != nil {
			if errors.Is(err, client.ErrClaimLost) {
				return fastlane.ErrStale
			}
			return err // the caller retries Finish: a result posted without its log would leave the job without it for good
		}
	}
	state := o.State
	if !state.ResultState() { // expired is the queue's word for a job that never ran; a run that was registered is failed
		state = wire.StateFailed
	}
	summary := o.Summary
	err := h.api.PostResult(ctx, h.resp.ID, wire.ResultRequest{
		ClaimID: h.resp.ClaimID, State: state, Exit: o.Exit, Summary: &summary, StartedAt: ptr(o.StartedAt), FinishedAt: ptr(o.FinishedAt),
	})
	if errors.Is(err, client.ErrClaimLost) {
		return fastlane.ErrStale
	}
	return err
}

func ptr(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	u := t.UTC()
	return &u
}

// readLog is the log at path, its last maxLog bytes when it is longer; nil when it cannot be read or is empty.
func readLog(path string) []byte {
	if path == "" {
		return nil
	}
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil || st.Size() == 0 {
		return nil
	}
	size := st.Size()
	if size > maxLog {
		if _, err := f.Seek(size-maxLog, 0); err != nil {
			return nil
		}
		size = maxLog
	}
	b := make([]byte, size)
	n, _ := f.Read(b)
	return b[:n]
}
