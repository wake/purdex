package testcmd

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/wake/purdex/internal/gate"
	"github.com/wake/purdex/internal/wwd/fastlane"
	"github.com/wake/purdex/internal/wwd/manifest"
)

// errWaitedOut: a lock stayed held for the whole queue_timeout of the task and nothing ran.
var errWaitedOut = errors.New("a lock stayed held for the whole queue timeout")

// lockPoll is how often a run that a held lock keeps back asks again.
const lockPoll = 2 * time.Second

// runWhenFree runs the fast lane, and while another job on this host holds a lock the task needs it waits here and asks again: a
// task that takes the fast lane never goes to the queue (spec D12). The wait ends with the caller (ctx) or after the task's
// queue_timeout (o.LockWait, when set, replaces it), which is the same limit a run waiting for a slot has.
func runWhenFree(ctx context.Context, o Options, task *manifest.Task, req fastlane.Request) (fastlane.Result, error) {
	limit := task.QueueTimeout
	if o.LockWait > 0 {
		limit = o.LockWait
	}
	poll := lockPoll
	if o.LockPoll > 0 {
		poll = o.LockPoll
	}
	deadline := time.Now().Add(limit)
	told := false
	for attempt := 0; ; attempt++ {
		// one deadline for the lock and the slot together: the gate waits for what is left of it, not for a whole queue_timeout
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return fastlane.Result{}, errWaitedOut
		}
		t := *task
		t.QueueTimeout = remaining
		r := req
		r.Task = &t
		if attempt > 0 && req.Body.Snapshot.Commit != "" { // waited: the record is of the directory as it is now
			r.RetakeTree = func(ctx context.Context) (string, error) { return gate.WorkingTree(ctx, o.GitBin, o.Dir) }
		}
		res, err := fastlane.Run(ctx, r)
		if !errors.Is(err, fastlane.ErrLocksBusy) && !errors.Is(err, fastlane.ErrSlotBusy) {
			return res, err
		}
		if !told {
			told = true
			fmt.Fprintf(o.Stderr, "pdx test: 等待本機名額／lock：%v\n", err)
		}
		wait := min(poll, time.Until(deadline))
		if wait <= 0 {
			return fastlane.Result{}, errWaitedOut
		}
		select {
		case <-ctx.Done():
			return fastlane.Result{}, ctx.Err()
		case <-time.After(wait):
		}
	}
}
