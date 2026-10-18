package main

import "context"

// services are the long-running parts that must end in order: the worker first, then whatever else makes a job's final event, then the usage recorder. The worker, when its
// context ends, stops what it runs and hands each job back, and each ending records the run's usage (RecordRun); the recorder writes
// what is queued until its own context ends. So the recorder has a context of its own, which is cancelled only after the worker is done:
// on the daemon's signal context it would have stopped together with the worker and dropped the rows the worker queues while it
// finishes (up to the loop's shutdown grace later).
type services struct {
	stopUsage  context.CancelFunc
	usageDone  chan struct{}
	workerDone chan struct{}
	workerErr  chan error
}

// startServices starts the usage recorder (usageRun, under its own context) and the worker (workerRun, under ctx). A worker that returns
// an error while ctx is still alive reports it through Err: wwd does not run half-way.
func startServices(ctx context.Context, usageRun func(context.Context), workerRun func(context.Context) error) *services {
	uctx, stopUsage := context.WithCancel(context.WithoutCancel(ctx))
	s := &services{stopUsage: stopUsage, usageDone: make(chan struct{}), workerDone: make(chan struct{}), workerErr: make(chan error, 1)}
	go func() { defer close(s.usageDone); usageRun(uctx) }()
	go func() {
		defer close(s.workerDone)
		if err := workerRun(ctx); err != nil && ctx.Err() == nil {
			s.workerErr <- err
		}
	}()
	return s
}

// Err delivers the error of a worker that could not start or had to stop.
func (s *services) Err() <-chan error { return s.workerErr }

// Stop cancels the daemon's context (cancelAll), waits for the worker to finish, then for everything else that can end a job to be quiet
// (quiesce: the HTTP servers closed with their requests answered, the queue's sweeper and the fast lane returned; nil: nothing else),
// and only then ends the usage recorder and waits for it to write what is left. A job's final event is made by any of those, and the
// row of a job whose event comes after the recorder has drained is lost; the store is closed after this returns.
func (s *services) Stop(cancelAll context.CancelFunc, quiesce func()) {
	cancelAll()
	<-s.workerDone
	if quiesce != nil {
		quiesce()
	}
	s.stopUsage()
	<-s.usageDone
}
