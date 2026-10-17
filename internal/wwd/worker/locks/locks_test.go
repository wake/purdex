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

func TestAcquireAndRelease(t *testing.T) {
	tb := New()
	if err := tb.Acquire("j_1", []string{"db", "redis"}, nil); err != nil {
		t.Fatal(err)
	}
	if h, ok := tb.Holder("db"); !ok || h != "j_1" {
		t.Fatalf("holder = %q %v", h, ok)
	}
	err := tb.Acquire("j_2", []string{"redis", "other", "db"}, nil)
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
	tb.Release("j_1")
	if err := tb.Acquire("j_2", []string{"redis", "other", "db"}, nil); err != nil {
		t.Fatalf("after the release: %v", err)
	}
	tb.Release("j_2")
	tb.Release("j_2") // twice is fine
	if len(tb.Held()) != 0 || tb.Owners() != 0 {
		t.Fatalf("table not empty: %v %d", tb.Held(), tb.Owners())
	}
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
		if err := tb.Acquire(tc.owner, tc.keys, nil); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	if tb.Owners() != 0 {
		t.Fatal("a rejected acquire left an owner behind")
	}
	if err := tb.Acquire("j", []string{"a"}, nil); err != nil {
		t.Fatal(err)
	}
	if err := tb.Acquire("j", []string{"b"}, nil); !errors.Is(err, ErrAlreadyHeld) {
		t.Fatalf("second acquire by the same owner: %v", err)
	}
}

func TestOwnerWithNoKeysHoldsNothing(t *testing.T) {
	tb := New()
	if err := tb.Acquire("j_1", nil, nil); err != nil {
		t.Fatal(err)
	}
	if err := tb.Acquire("j_2", []string{"a"}, nil); err != nil {
		t.Fatal(err)
	}
	if tb.Owners() != 2 || len(tb.Held()) != 1 {
		t.Fatalf("owners %d held %v", tb.Owners(), tb.Held())
	}
	tb.Release("j_1")
	if tb.Owners() != 1 {
		t.Fatalf("owners = %d", tb.Owners())
	}
}

func TestFailedRecordReleasesTheKeys(t *testing.T) {
	tb := New()
	boom := errors.New("queue unreachable")
	if err := tb.Acquire("j_1", []string{"db"}, func() error { return boom }); !errors.Is(err, boom) {
		t.Fatalf("err = %v", err)
	}
	if len(tb.Held()) != 0 || tb.Owners() != 0 {
		t.Fatalf("a failed record left the keys held: %v", tb.Held())
	}
	if err := tb.Acquire("j_2", []string{"db"}, nil); err != nil {
		t.Fatalf("keys should be free again: %v", err)
	}
}

func TestKeysAreReservedWhileTheJobIsBeingRecorded(t *testing.T) {
	tb := New()
	recording := make(chan struct{})
	finish := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- tb.Acquire("j_1", []string{"db"}, func() error { close(recording); <-finish; return nil })
	}()
	<-recording
	// the record is in progress (a call to the queue, say): the key is already taken, and the table is not locked
	var busy *BusyError
	if err := tb.Acquire("j_2", []string{"db"}, nil); !errors.As(err, &busy) {
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

func TestReleaseDuringRecordMeansTheJobMustNotStart(t *testing.T) {
	tb := New()
	recording, finish := make(chan struct{}), make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- tb.Acquire("j_1", []string{"db"}, func() error { close(recording); <-finish; return nil })
	}()
	<-recording
	tb.Release("j_1") // a cancel arrives while the job is being recorded
	if err := tb.Acquire("j_2", []string{"db"}, nil); err != nil {
		t.Fatalf("the key should be free after the release: %v", err)
	}
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
		done <- tb.Acquire("j_1", []string{"db"}, func() error { close(recording); <-finish; return errors.New("queue failed") })
	}()
	<-recording
	tb.Release("j_1")
	if err := tb.Acquire("j_1", []string{"db"}, nil); err != nil { // the same owner again
		t.Fatal(err)
	}
	close(finish)
	if err := <-done; err == nil {
		t.Fatal("want the record's error")
	}
	if h, ok := tb.Holder("db"); !ok || h != "j_1" {
		t.Fatalf("the older acquire's failure released the newer one's keys: %q %v", h, ok)
	}
}

func TestConflictsDoesNotRegister(t *testing.T) {
	tb := New()
	_ = tb.Acquire("j_1", []string{"b"}, nil)
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
				owner := fmt.Sprintf("j_%d_%d", g, i)
				if err := tb.Acquire(owner, names, nil); err != nil {
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
				tb.Release(owner)
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
