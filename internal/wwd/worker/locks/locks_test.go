package locks

import (
	"errors"
	"fmt"
	"math/rand"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func mustAcquire(t *testing.T, tb *Table, owner string, keys ...string) *Hold {
	t.Helper()
	h, err := tb.Acquire(owner, keys, nil)
	if err != nil {
		t.Fatalf("Acquire(%s, %v): %v", owner, keys, err)
	}
	return h
}

func TestAcquireAndRelease(t *testing.T) {
	tb := New()
	h1 := mustAcquire(t, tb, "j_1", "db", "redis")
	if h, ok := tb.Holder("db"); !ok || h != "j_1" {
		t.Fatalf("holder = %q %v", h, ok)
	}
	_, err := tb.Acquire("j_2", []string{"redis", "other", "db"}, nil)
	var busy *BusyError
	if !errors.As(err, &busy) || len(busy.Conflicts) != 2 || busy.Conflicts[0].Key != "db" || busy.Conflicts[1].Key != "redis" || busy.Conflicts[0].Holder != "j_1" {
		t.Fatalf("err = %v", err)
	}
	if _, held := tb.Holder("other"); held {
		t.Fatal("a refused acquire must register nothing, not even the free key")
	}
	if tb.Owners() != 1 {
		t.Fatalf("owners = %d", tb.Owners())
	}
	h1.Release()
	h2 := mustAcquire(t, tb, "j_2", "redis", "other", "db")
	h2.Release()
	h2.Release() // twice is fine
	if len(tb.Held()) != 0 || tb.Owners() != 0 {
		t.Fatalf("table not empty: %v %d", tb.Held(), tb.Owners())
	}
	var nilHold *Hold
	nilHold.Release() // a nil hold is a no-op
}

func TestAcquireValidation(t *testing.T) {
	tb := New()
	for name, tc := range map[string]struct {
		owner string
		keys  []string
	}{
		"empty owner":  {"", []string{"a"}},
		"empty key":    {"j", []string{""}},
		"nul key":      {"j", []string{"a\x00"}},
		"newline key":  {"j", []string{"a\nb"}},
		"repeated key": {"j", []string{"a", "a"}},
	} {
		if h, err := tb.Acquire(tc.owner, tc.keys, nil); err == nil || h != nil {
			t.Errorf("%s: accepted", name)
		}
	}
	if tb.Owners() != 0 {
		t.Fatal("a rejected acquire left an owner behind")
	}
	mustAcquire(t, tb, "j", "a")
	if _, err := tb.Acquire("j", []string{"b"}, nil); !errors.Is(err, ErrAlreadyHeld) {
		t.Fatalf("second acquire by the same owner: %v", err)
	}
}

func TestOwnerWithNoKeysHoldsNothing(t *testing.T) {
	tb := New()
	h1 := mustAcquire(t, tb, "j_1")
	mustAcquire(t, tb, "j_2", "a")
	if tb.Owners() != 2 || len(tb.Held()) != 1 {
		t.Fatalf("owners %d held %v", tb.Owners(), tb.Held())
	}
	h1.Release()
	if tb.Owners() != 1 {
		t.Fatalf("owners = %d", tb.Owners())
	}
}

func TestFailedRecordReleasesTheKeys(t *testing.T) {
	tb := New()
	boom := errors.New("queue unreachable")
	if _, err := tb.Acquire("j_1", []string{"db"}, func() error { return boom }); !errors.Is(err, boom) {
		t.Fatalf("err = %v", err)
	}
	if len(tb.Held()) != 0 || tb.Owners() != 0 {
		t.Fatalf("a failed record left the keys held: %v", tb.Held())
	}
	mustAcquire(t, tb, "j_2", "db")
}

func TestPanickingRecordReleasesTheKeys(t *testing.T) {
	tb := New()
	func() {
		defer func() {
			if recover() == nil {
				t.Error("the panic was swallowed")
			}
		}()
		tb.Acquire("j_1", []string{"db"}, func() error { panic("boom") })
	}()
	if len(tb.Held()) != 0 || tb.Owners() != 0 {
		t.Fatalf("a panicking record left the keys reserved forever: %v", tb.Held())
	}
	mustAcquire(t, tb, "j_2", "db")
}

func TestKeysAreReservedWhileTheJobIsBeingRecorded(t *testing.T) {
	tb := New()
	recording, finish := make(chan struct{}), make(chan struct{})
	done := make(chan error, 1)
	go func() {
		_, err := tb.Acquire("j_1", []string{"db"}, func() error { close(recording); <-finish; return nil })
		done <- err
	}()
	<-recording
	// the record is in progress (a call to the queue, say): the key is already taken, and the table is not locked
	var busy *BusyError
	if _, err := tb.Acquire("j_2", []string{"db"}, nil); !errors.As(err, &busy) {
		t.Fatalf("a start slipped in while the job was being recorded: %v", err)
	}
	if h, _ := tb.Holder("db"); h != "j_1" {
		t.Fatalf("holder = %q", h)
	}
	close(finish)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestReleaseOwnerDuringRecordMeansTheJobMustNotStart(t *testing.T) {
	tb := New()
	recording, finish := make(chan struct{}), make(chan struct{})
	done := make(chan error, 1)
	go func() {
		_, err := tb.Acquire("j_1", []string{"db"}, func() error { close(recording); <-finish; return nil })
		done <- err
	}()
	<-recording
	tb.ReleaseOwner("j_1") // a cancel arrives while the job is being recorded
	mustAcquire(t, tb, "j_2", "db")
	close(finish)
	if err := <-done; !errors.Is(err, ErrReleased) {
		t.Fatalf("a record that finished after its release must not count as acquired: %v", err)
	}
	if h, _ := tb.Holder("db"); h != "j_2" {
		t.Fatalf("holder = %q, want j_2 untouched", h)
	}
}

func TestLateFailureOfAnOlderAcquireDoesNotReleaseANewerOne(t *testing.T) {
	tb := New()
	recording, finish := make(chan struct{}), make(chan struct{})
	done := make(chan error, 1)
	go func() {
		_, err := tb.Acquire("j_1", []string{"db"}, func() error { close(recording); <-finish; return errors.New("queue failed") })
		done <- err
	}()
	<-recording
	tb.ReleaseOwner("j_1")
	mustAcquire(t, tb, "j_1", "db") // the same owner again
	close(finish)
	if err := <-done; err == nil {
		t.Fatal("want the record's error")
	}
	if h, ok := tb.Holder("db"); !ok || h != "j_1" {
		t.Fatalf("the older acquire's failure released the newer one's keys: %q %v", h, ok)
	}
}

func TestLateReleaseOfAnOlderHoldDoesNotFreeANewerOne(t *testing.T) {
	tb := New()
	old := mustAcquire(t, tb, "j_1", "db") // attempt 1
	old.Release()
	mustAcquire(t, tb, "j_1", "db") // attempt 2 of the same job id
	old.Release()                   // the first attempt's cleanup arrives late
	if h, ok := tb.Holder("db"); !ok || h != "j_1" {
		t.Fatalf("a stale Release freed the newer hold: %q %v", h, ok)
	}
	var busy *BusyError
	if _, err := tb.Acquire("j_2", []string{"db"}, nil); !errors.As(err, &busy) {
		t.Fatalf("another owner got the key held by the newer attempt: %v", err)
	}
}

func TestConflictsDoesNotRegister(t *testing.T) {
	tb := New()
	mustAcquire(t, tb, "j_1", "b")
	got := tb.Conflicts([]string{"c", "b", "a"})
	if len(got) != 1 || got[0].Key != "b" || got[0].Holder != "j_1" {
		t.Fatalf("conflicts = %v", got)
	}
	if tb.Owners() != 1 {
		t.Fatal("Conflicts registered something")
	}
}

// Under -race: many starts compete for overlapping keys; no key is ever held by two owners at once, and every owner that got
// its keys saw them exclusively.
func TestConcurrentStartsNeverShareAKey(t *testing.T) {
	tb := New()
	const keys = 6
	var inside [keys]atomic.Int32
	var violations atomic.Int32
	var wins atomic.Int32
	var wg sync.WaitGroup
	for g := 0; g < 24; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			rng := rand.New(rand.NewSource(int64(g)))
			for i := 0; i < 200; i++ {
				var set []int
				for k := 0; k < keys; k++ {
					if rng.Intn(3) == 0 {
						set = append(set, k)
					}
				}
				names := make([]string, len(set))
				for j, k := range set {
					names[j] = fmt.Sprint("key", k)
				}
				h, err := tb.Acquire(fmt.Sprintf("j_%d_%d", g, i), names, nil)
				if err != nil {
					continue
				}
				wins.Add(1)
				for _, k := range set {
					if inside[k].Add(1) != 1 {
						violations.Add(1)
					}
				}
				time.Sleep(time.Duration(rng.Intn(50)) * time.Microsecond)
				for _, k := range set {
					inside[k].Add(-1)
				}
				h.Release()
			}
		}(g)
	}
	wg.Wait()
	if violations.Load() != 0 {
		t.Fatalf("%d keys were held by two owners at once", violations.Load())
	}
	if wins.Load() == 0 {
		t.Fatal("nobody ever got its keys")
	}
	if len(tb.Held()) != 0 || tb.Owners() != 0 {
		t.Fatalf("table not empty: %v %d", tb.Held(), tb.Owners())
	}
}
