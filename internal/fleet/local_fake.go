package fleet

import "sync"

// FakeLocal is a LocalSource for tests and for use before the real sampler is wired.
// With nothing set it reports nothing (Fast/Slow return false).
type FakeLocal struct {
	mu      sync.Mutex
	fast    *LocalFast
	slow    *LocalSlow
	starts  int
	stops   int
	changed chan struct{}
}

func NewFakeLocal() *FakeLocal {
	return &FakeLocal{changed: make(chan struct{}, 1)}
}

func (f *FakeLocal) SetFast(v LocalFast) {
	f.mu.Lock()
	f.fast = &v
	f.mu.Unlock()
	select {
	case f.changed <- struct{}{}:
	default: // already signalled: coalesce
	}
}

func (f *FakeLocal) SetSlow(v LocalSlow) {
	f.mu.Lock()
	f.slow = &v
	f.mu.Unlock()
}

func (f *FakeLocal) Start() { f.mu.Lock(); f.starts++; f.mu.Unlock() }
func (f *FakeLocal) Stop()  { f.mu.Lock(); f.stops++; f.mu.Unlock() }

func (f *FakeLocal) Starts() int { f.mu.Lock(); defer f.mu.Unlock(); return f.starts }
func (f *FakeLocal) Stops() int  { f.mu.Lock(); defer f.mu.Unlock(); return f.stops }

func (f *FakeLocal) Fast() (LocalFast, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.fast == nil {
		return LocalFast{}, false
	}
	return *f.fast, true
}

func (f *FakeLocal) Slow() (LocalSlow, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.slow == nil {
		return LocalSlow{}, false
	}
	return *f.slow, true
}

func (f *FakeLocal) Changed() <-chan struct{} { return f.changed }
