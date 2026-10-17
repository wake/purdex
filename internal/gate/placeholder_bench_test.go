package gate

import (
	"os"
	"testing"
)

// BenchmarkWritePlaceholder is the cost the placeholder record adds inside the admit lock at each take (#3049): one small file written
// to a temp name and renamed over the record.
func BenchmarkWritePlaceholder(b *testing.B) {
	p := &Pool{Dir: b.TempDir(), Class: ClassLight, Size: 3}
	if err := os.MkdirAll(p.Dir, 0o755); err != nil {
		b.Fatal(err)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := p.writePlaceholder(1, WaiterInfo{Argv: []string{"tsc", "-b"}}, false); err != nil {
			b.Fatal(err)
		}
	}
}
