package agent

import (
	"bytes"
	"log"
	"regexp"
	"strings"
	"testing"
	"time"
)

func TestStart_LogsSubstepTimingsBeforeEndpointLine(t *testing.T) {
	m := newSweepTestModule(t)
	origInterval := sweepInterval
	origOnce := sweepOnceFn
	sweepInterval = time.Hour
	sweepOnceFn = func(*Module) {}
	t.Cleanup(func() {
		sweepInterval = origInterval
		sweepOnceFn = origOnce
	})

	var buf bytes.Buffer
	prev := log.Writer()
	log.SetOutput(&buf)
	t.Cleanup(func() { log.SetOutput(prev) })

	if err := m.Start(nil); err != nil {
		t.Fatalf("Start: %v", err)
	}
	_ = m.Stop(nil)

	out := buf.String()
	re := regexp.MustCompile(`\[agent\] start: sweepOnce=\d+ms replayFromDB=\d+ms startSweep=\d+ms replayStatus=\d+ms`)
	loc := re.FindStringIndex(out)
	if loc == nil {
		t.Fatalf("no sub-step timing line in log:\n%s", out)
	}
	end := strings.Index(out, "[agent] hook event endpoint registered")
	if end < 0 || loc[0] > end {
		t.Fatalf("timing line must precede the endpoint-registered line:\n%s", out)
	}
}
