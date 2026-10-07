package agent

import (
	"bytes"
	"log"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/wake/purdex/internal/store"
)

func newCloseTestSink(t *testing.T) (*hookTraceSink, *store.TraceStore) {
	t.Helper()
	events, err := store.OpenAgentEvent(":memory:")
	if err != nil {
		t.Fatalf("open agent event store: %v", err)
	}
	t.Cleanup(func() { events.Close() })
	traces, err := events.Traces()
	if err != nil {
		t.Fatalf("traces: %v", err)
	}
	sink := newHookTraceSink(traces)
	t.Cleanup(sink.Close)
	return sink, traces
}

func closeTestRecord(id string) store.TraceRecord {
	now := time.Now().UnixNano()
	return store.TraceRecord{
		Chain: store.TraceChain{ChainID: id, StartedAt: now, CompletedAt: now, TerminalStatus: "ok"},
		Steps: []store.TraceStep{{StepID: id + "-s", ChainID: id, Seq: 1, Kind: TraceStepTrigger, PayloadJSON: []byte("{}"), BeforeJSON: []byte("{}"), AfterJSON: []byte("{}"), CreatedAt: now}},
	}
}

// captureLog redirects the std logger into a buffer (tests here are not
// parallel, so swapping the global logger is safe).
func captureLog(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	log.SetOutput(&buf)
	t.Cleanup(func() { log.SetOutput(os.Stderr) })
	return &buf
}

// T1: Enqueue after Close must not panic, must drop + count, and log once.
func TestHookTraceSink_EnqueueAfterClose_DropsWithoutPanic(t *testing.T) {
	sink, _ := newCloseTestSink(t)
	buf := captureLog(t)
	sink.Close()

	sink.Enqueue(closeTestRecord("late-1"))
	sink.FlushForTest()

	if got := sink.dropped.Load(); got != 1 {
		t.Fatalf("dropped = %d, want 1", got)
	}
	sink.Enqueue(closeTestRecord("late-2"))
	if got := strings.Count(buf.String(), "sink closed: dropping trace records from now on"); got != 1 {
		t.Fatalf("one-shot log count = %d, want 1; log=%q", got, buf.String())
	}
}
