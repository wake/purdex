package main

import (
	"context"
	"path/filepath"

	"github.com/wake/purdex/internal/wwd/queue"
	"github.com/wake/purdex/internal/wwd/snapshot"
	"github.com/wake/purdex/internal/wwd/wire"
)

// queueOptions is what the origin's queue runs with. The two things that are not optional on a real host: the Pinner (without it
// a submission is stored with no pin, the snapshot route refuses to serve the job - "the commit of this job is not pinned" - and
// no peer worker can ever fetch it) and the log directory (queue.New makes it; the log operations write into it).
func queueOptions(alias, dataDir string, publish func(wire.JobEvent)) queue.Options {
	return queue.Options{Origin: alias, LogDir: filepath.Join(dataDir, "joblogs"), Pinner: gitPinner{}, Publish: publish}
}

// gitPinner is the queue's Pinner on this host: refs/wwd/<job id> in the submitter's git directory (snapshot.Pin, Unpin, ListPins).
type gitPinner struct{}

var _ queue.Pinner = gitPinner{}

func (gitPinner) Pin(ctx context.Context, gitDir, jobID, commit string) error {
	return snapshot.Pin(ctx, gitDir, jobID, commit)
}

func (gitPinner) Unpin(ctx context.Context, gitDir, jobID string) error {
	return snapshot.Unpin(ctx, gitDir, jobID)
}

func (gitPinner) ListPins(ctx context.Context, gitDir string) ([]string, error) {
	return snapshot.ListPins(ctx, gitDir)
}
