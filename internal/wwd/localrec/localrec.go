// Package localrec records fast-lane runs with the local wwd: it is the fastlane.Recorder that talks to POST /v1/local-runs and then
// to the ordinary claim routes (heartbeat, log, result) with the claim wwd answered with (wwd spec §5.2, §4.2).
package localrec

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"slices"
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
	var full *client.SlotBusyError
	switch {
	case errors.As(err, &full):
		return nil, fmt.Errorf("%w: %s", fastlane.ErrSlotBusy, full)
	case errors.As(err, &busy):
		return nil, fmt.Errorf("%w: %s", fastlane.ErrLocksBusy, busy.Held())
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
	log, err := readLog(o.LogPath)
	if err != nil {
		return err
	}
	if log != nil {
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
	err = h.api.PostResult(ctx, h.resp.ID, wire.ResultRequest{
		ClaimID: h.resp.ClaimID, State: state, Exit: o.Exit, Summary: &summary, StartedAt: ptr(o.StartedAt), FinishedAt: ptr(o.FinishedAt),
		Reason: resultReason(state, o.Reason),
	})
	if errors.Is(err, client.ErrClaimLost) {
		return fastlane.ErrStale
	}
	return err
}

// resultReason is the reason a run may post with its result: only a failed run has one, and only the words a worker may write
// (wire.ResultReasons: oom, timeout, infra). The origin's own words — lost, queue_timeout — are not accepted from the runner; a
// lost run is marked by the origin when the lease lapses.
func resultReason(state wire.JobState, reason string) string {
	if state != wire.StateFailed || !slices.Contains(wire.ResultReasons, reason) {
		return ""
	}
	return reason
}

func ptr(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	u := t.UTC()
	return &u
}

// readLog is the log at path, its last maxLog bytes when it is longer. No path, no file or an empty file is no log (nil, nil); a
// log that exists and cannot be read is an error, which Finish passes on so that the result is not posted without it.
func readLog(path string) ([]byte, error) {
	if path == "" {
		return nil, nil
	}
	f, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if st.Size() == 0 {
		return nil, nil
	}
	size := st.Size()
	if size > maxLog {
		if _, err := f.Seek(size-maxLog, io.SeekStart); err != nil {
			return nil, err
		}
		size = maxLog
	}
	b := make([]byte, size)
	if _, err := io.ReadFull(f, b); err != nil {
		return nil, err
	}
	return b, nil
}
