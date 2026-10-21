package store

import (
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/wake/purdex/internal/wwd/wire"
)

// #3839: RaiseAlert reads the alert's row and then writes it. A deferred transaction that has read cannot become the writer when another
// connection has written in between: SQLite answers SQLITE_BUSY at once and does not wait on the busy timeout. wwd writes wwd.db from
// several places (the minute facts, the usage records, the observations, the queue), so a rule's alert was lost now and then
// ("advisor: rule disk_falling: write: database is locked"). The transactions of the store take the write lock when they begin.

// otherWrites writes wwd.db through the store's other tables, as the rest of wwd does, until stop is closed.
func otherWrites(t *testing.T, s *Store, stop <-chan struct{}) *sync.WaitGroup {
	t.Helper()
	var wg sync.WaitGroup
	for w := 0; w < 3; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; ; i++ {
				select {
				case <-stop:
					return
				default:
				}
				// distinct stamps: a start is recorded once per millisecond, and each one is a write transaction of its own
				if err := s.RecordStartAt(time.Now().Add(time.Duration(w*1000000+i)*time.Millisecond), "v", ""); err != nil {
					t.Errorf("other writer: %v", err)
					return
				}
			}
		}()
	}
	return &wg
}

func TestRaiseAlertWhileOtherWritersWriteNeverFailsWithBusy(t *testing.T) {
	s := openAlerts(t)
	stop := make(chan struct{})
	others := otherWrites(t, s, stop)

	const rounds = 400
	for i := 0; i < rounds; i++ {
		a := wire.Alert{Key: fmt.Sprintf("disk_falling:%d", i%5), Kind: wire.AlertDiskLow, Level: wire.AlertPush, Detail: fmt.Sprintf("%d GB", i)}
		if _, err := s.RaiseAlert(a, at0.Add(time.Duration(i)*time.Second)); err != nil {
			close(stop)
			others.Wait()
			t.Fatalf("round %d: %v", i, err)
		}
	}
	close(stop)
	others.Wait()
}

func TestClearAlertWhileOtherWritersWriteNeverFailsWithBusy(t *testing.T) {
	s := openAlerts(t)
	stop := make(chan struct{})
	others := otherWrites(t, s, stop)
	defer func() { close(stop); others.Wait() }()

	for i := 0; i < 400; i++ {
		if _, err := s.RaiseAlert(diskLow("1 GB"), at0.Add(time.Duration(i)*time.Second)); err != nil {
			t.Fatalf("raise %d: %v", i, err)
		}
		if _, err := s.ClearAlert(diskLow("").Key, at0.Add(time.Duration(i)*time.Second)); err != nil {
			t.Fatalf("clear %d: %v", i, err)
		}
	}
}
