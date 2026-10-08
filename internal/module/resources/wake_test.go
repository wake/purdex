package resourcesmod

import (
	"sync"
	"testing"
	"time"
)

func closed(ch <-chan struct{}) bool {
	select {
	case <-ch:
		return true
	default:
		return false
	}
}

func TestWake_ClosesTheOldGenerationAndInstallsANewOne(t *testing.T) {
	m := New()
	old := m.genChan()
	if closed(old) {
		t.Fatal("a fresh generation channel is already closed")
	}
	if m.genChan() != old {
		t.Fatal("genChan must return the same channel until a wake")
	}
	m.wake()
	if !closed(old) {
		t.Fatal("wake did not close the channel a poller was holding")
	}
	fresh := m.genChan()
	if fresh == old || closed(fresh) {
		t.Fatal("wake must install a new open channel")
	}
	m.wake()
	if !closed(fresh) {
		t.Fatal("the second wake did not close the second generation")
	}
}

func TestWake_ConcurrentWakersAndPollers(t *testing.T) {
	m := New()
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			for j := 0; j < 200; j++ {
				m.wake()
			}
		}()
		go func() {
			defer wg.Done()
			for j := 0; j < 200; j++ {
				ch := m.genChan()
				select {
				case <-ch:
				case <-time.After(10 * time.Millisecond):
				}
			}
		}()
	}
	wg.Wait()
}

func TestStateMu_ExcludesASecondHolder(t *testing.T) {
	m := New()
	m.stateMu.Lock()
	if m.stateMu.TryLock() {
		t.Fatal("stateMu must exclude a second holder")
	}
	m.stateMu.Unlock()
}
