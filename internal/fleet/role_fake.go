package fleet

import "sync"

// FakeRole is a RoleView for tests: Set changes the state and notifies the registered callbacks in order.
type FakeRole struct {
	mu    sync.Mutex // guards state and cbs
	notif sync.Mutex // serialises notification so callbacks never run concurrently
	state RoleState
	cbs   []func(RoleState)
}

func NewFakeRole(initial RoleState) *FakeRole { return &FakeRole{state: initial} }

func (f *FakeRole) State() RoleState {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.state
}

func (f *FakeRole) OnChange(fn func(RoleState)) {
	f.mu.Lock()
	f.cbs = append(f.cbs, fn)
	f.mu.Unlock()
}

func (f *FakeRole) Set(s RoleState) {
	f.notif.Lock()
	defer f.notif.Unlock()
	f.mu.Lock()
	f.state = s
	cbs := append([]func(RoleState){}, f.cbs...)
	f.mu.Unlock()
	for _, cb := range cbs {
		cb(s)
	}
}
