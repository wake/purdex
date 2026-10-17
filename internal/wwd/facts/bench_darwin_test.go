//go:build darwin

package facts

import (
	"testing"
	"time"
)

// BenchmarkSample is one steady-state sample of this host (the budget is the
// wwd daemon at under 1% CPU: one sample per 5 s must stay far below 50 ms).
func BenchmarkSample(b *testing.B) {
	c := NewCollector(SystemSources())
	for i := 0; i < 3; i++ { // warm: first rounds fill caches and fork the tool probes
		_, _ = c.Sample()
		time.Sleep(200 * time.Millisecond)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = c.Sample()
	}
}

func BenchmarkProcSnapshot(b *testing.B) {
	r, err := NewProcReader(DefaultSessionsDir())
	if err != nil {
		b.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		_, _, _ = r.Snapshot(time.Now())
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _, _ = r.Snapshot(time.Now())
	}
}
