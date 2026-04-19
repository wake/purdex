package agent

import (
	"testing"
	"time"
)

func TestTestObserversRegisterSignalDeregister(t *testing.T) {
	m := New(nil) // AgentEventStore allowed to be nil; observers don't touch it
	ch := m.registerTestObserver("__pdx_test_aaaa1111")

	go m.signalTestStage("__pdx_test_aaaa1111", testStageReceived)

	select {
	case stage := <-ch:
		if stage != testStageReceived {
			t.Fatalf("got stage %v, want testStageReceived", stage)
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatal("timed out waiting for stage")
	}

	m.deregisterTestObserver("__pdx_test_aaaa1111")

	// After deregister, signalTestStage must be a no-op (no panic from send on nil chan etc.)
	m.signalTestStage("__pdx_test_aaaa1111", testStageBroadcast)
}

func TestSignalTestStageUnknownNonceIsNoOp(t *testing.T) {
	m := New(nil)
	// Must not panic, must not hang.
	m.signalTestStage("__pdx_test_zzzz9999", testStageReceived)
}
