package resourcesmod

import (
	"testing"

	"github.com/wake/purdex/internal/resources"
)

// BenchmarkTx_OfferPosition is one transaction (begin, two statements, commit)
// on the WAL file database: the time a combined operation holds stateMu.
func BenchmarkTx_OfferPosition(b *testing.B) {
	s, err := openLeaseStore(b.TempDir() + "/resources.db")
	if err != nil {
		b.Fatal(err)
	}
	defer s.Close()
	if _, _, _, err := s.CreateAttached(txPos("pos_00000001", "s1", 10), txRow("a", "s1", "", resources.CallQueue, "x", "/w", 10)); err != nil {
		b.Fatal(err)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		b.StopTimer()
		if _, err := s.db.Exec(`UPDATE resource_positions SET state = 'queued'`); err != nil {
			b.Fatal(err)
		}
		b.StartTimer()
		if ok, err := s.OfferPosition("pos_00000001", int64(1000+i), "normal", "flat", ""); err != nil || !ok {
			b.Fatal(ok, err)
		}
	}
}
