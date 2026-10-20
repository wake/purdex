package wwdalerts

import (
	"fmt"
	"sync"
	"testing"
	"time"
)

// Transactions that read and then write take the write lock when they begin, so two connections at once wait for each other (the busy
// timeout) instead of one failing with SQLITE_BUSY when it tries to go from reading to writing.
func TestConcurrentLedgerTransactionsDoNotFailWithBusy(t *testing.T) {
	e := newEnv(t)
	var eps []wakeEp
	for i := 0; i < 8; i++ {
		ep := mkEp("mlab", fmt.Sprintf("k%d", i), t0)
		if _, err := e.m.store.claimWake(ep, e.now, wakeThrottle, 0); err != nil {
			t.Fatal(err)
		}
		eps = append(eps, ep)
	}
	var wg sync.WaitGroup
	errs := make(chan error, 400)
	for g := 0; g < 6; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 40; i++ {
				switch (g + i) % 3 {
				case 0:
					errs <- e.m.store.markWakeFailed(eps, e.now.Add(time.Duration(i)*time.Second))
				case 1:
					_, err := e.m.store.stillPending(eps)
					errs <- err
				default:
					_, err := e.m.store.claimWake(mkEp("mlab", fmt.Sprintf("n%d-%d", g, i), t0), e.now, wakeThrottle, 0)
					errs <- err
				}
			}
		}(g)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("a ledger transaction failed: %v", err)
		}
	}
}
