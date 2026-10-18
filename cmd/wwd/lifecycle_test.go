package main

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

// The worker hands its jobs back when its context ends, and each ending queues the usage of the run a little later. The usage recorder
// has a context of its own and is stopped after the worker, so it still writes those rows.
func TestTheUsageRecorderStillWritesWhatTheWorkerQueuesWhileItEnds(t *testing.T) {
	rows := make(chan int, 4)
	var written atomic.Int32
	usageRun := func(ctx context.Context) {
		for {
			select {
			case <-rows:
				written.Add(1)
			case <-ctx.Done():
				for { // like daemon.UsageRecorder.Run: what is queued when it ends
					select {
					case <-rows:
						written.Add(1)
					default:
						return
					}
				}
			}
		}
	}
	workerRun := func(ctx context.Context) error {
		<-ctx.Done()
		time.Sleep(100 * time.Millisecond) // the jobs are being stopped and handed back
		rows <- 1                          // RecordRun of the last one
		return nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	svc := startServices(ctx, usageRun, workerRun)
	svc.Stop(cancel)
	if n := written.Load(); n != 1 {
		t.Fatalf("%d rows written, want the 1 the worker queued while it ended", n)
	}
}

func TestStopWaitsForTheWorkerBeforeItEndsTheUsageRecorder(t *testing.T) {
	var order []string
	done := make(chan struct{})
	usageRun := func(ctx context.Context) { <-ctx.Done(); order = append(order, "usage") }
	workerRun := func(ctx context.Context) error {
		<-ctx.Done()
		time.Sleep(50 * time.Millisecond)
		order = append(order, "worker")
		return nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	svc := startServices(ctx, usageRun, workerRun)
	go func() { svc.Stop(cancel); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Stop did not return")
	}
	if len(order) != 2 || order[0] != "worker" || order[1] != "usage" {
		t.Fatalf("order %v: the worker ends first", order)
	}
}

func TestAWorkerThatCannotStartIsReportedAndOneThatEndsWithTheContextIsNot(t *testing.T) {
	boom := errors.New("cannot start")
	ctx, cancel := context.WithCancel(context.Background())
	svc := startServices(ctx, func(context.Context) {}, func(context.Context) error { return boom })
	select {
	case err := <-svc.Err():
		if !errors.Is(err, boom) {
			t.Fatalf("err %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the worker's failure did not arrive")
	}
	svc.Stop(cancel)

	ctx2, cancel2 := context.WithCancel(context.Background())
	svc2 := startServices(ctx2, func(context.Context) {}, func(ctx context.Context) error { <-ctx.Done(); return ctx.Err() })
	svc2.Stop(cancel2)
	select {
	case err := <-svc2.Err():
		t.Fatalf("a worker that ended with the context is not a failure: %v", err)
	default:
	}
}
