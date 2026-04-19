// Package agent — self-test endpoint for the statusline pipeline (see #481).
//
// Observer semantics: one test invocation registers a single channel keyed by
// the test nonce. handleAgentStatus signals stage2 on entry and stage3 after
// Broadcast returns. The test handler consumes both within its per-stage
// deadlines and then deregisters.
package agent

type testStage int

const (
	testStageReceived  testStage = iota + 1 // stage 2 (POST handler entered)
	testStageBroadcast                      // stage 3 (WS Broadcast called)
)

func (m *Module) registerTestObserver(nonce string) chan testStage {
	ch := make(chan testStage, 2) // buffered so signalTestStage never blocks the POST handler
	m.testMu.Lock()
	m.testObservers[nonce] = ch
	m.testMu.Unlock()
	return ch
}

func (m *Module) deregisterTestObserver(nonce string) {
	m.testMu.Lock()
	delete(m.testObservers, nonce)
	m.testMu.Unlock()
}

func (m *Module) signalTestStage(nonce string, stage testStage) {
	m.testMu.Lock()
	ch := m.testObservers[nonce]
	m.testMu.Unlock()
	if ch == nil {
		return
	}
	select {
	case ch <- stage:
	default:
		// Channel full — observer already got the signal or has moved on. Drop.
	}
}
