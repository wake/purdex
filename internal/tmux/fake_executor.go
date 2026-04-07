package tmux

import (
	"fmt"
	"sync"
)

// --- Fake Executor (test double) ---

type RawKeysCall struct {
	Target string
	Keys   []string
}

type KeysCall struct {
	Target string
	Keys   string
}

type SetWindowOptionCall struct {
	Target string
	Option string
	Value  string
}

type FakeExecutor struct {
	mu                   sync.Mutex
	sessions             map[string]TmuxSession // keyed by name for O(1) lookup
	sessionOrder         []string               // insertion order of session names
	nextID               int                    // auto-incrementing ID counter
	paneCommands         map[string]string      // target → command name
	paneCommandCalls     map[string]int         // target → PaneCurrentCommand call count
	paneContents         map[string]string      // target → captured text
	paneChildren         map[string][]string    // target → child command names
	paneSizes            map[string][2]int      // target → [cols, rows]
	rawKeysCalls         []RawKeysCall
	keysCalls            []KeysCall
	autoResizeCalls      []string              // targets passed to ResizeWindowAuto
	setWindowOptionCalls []SetWindowOptionCall // calls to SetWindowOption
	alive                bool                  // whether tmux server is "alive"
	HooksOutput          string                // returned by ShowHooksGlobal
	FailSendKeys         bool                  // if true, SendKeysRaw returns an error
}

func NewFakeExecutor() *FakeExecutor {
	return &FakeExecutor{
		sessions:         make(map[string]TmuxSession),
		paneCommands:     make(map[string]string),
		paneCommandCalls: make(map[string]int),
		paneContents:     make(map[string]string),
		paneChildren:     make(map[string][]string),
		paneSizes:        make(map[string][2]int),
		alive:            true,
	}
}

// AddSession adds a session with an auto-assigned ID ($0, $1, …).
func (f *FakeExecutor) AddSession(name, cwd string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	id := fmt.Sprintf("$%d", f.nextID)
	f.nextID++
	f.sessions[name] = TmuxSession{ID: id, Name: name, Cwd: cwd}
	f.sessionOrder = append(f.sessionOrder, name)
}

// AddSessionWithID adds a session with an explicit ID (for test control).
func (f *FakeExecutor) AddSessionWithID(id, name, cwd string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sessions[name] = TmuxSession{ID: id, Name: name, Cwd: cwd}
	f.sessionOrder = append(f.sessionOrder, name)
}

func (f *FakeExecutor) ListSessions() ([]TmuxSession, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]TmuxSession, 0, len(f.sessionOrder))
	for _, name := range f.sessionOrder {
		if s, ok := f.sessions[name]; ok {
			out = append(out, s)
		}
	}
	return out, nil
}

// NewSession creates a session with an auto-assigned ID.
func (f *FakeExecutor) NewSession(name, cwd string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	id := fmt.Sprintf("$%d", f.nextID)
	f.nextID++
	f.sessions[name] = TmuxSession{ID: id, Name: name, Cwd: cwd}
	f.sessionOrder = append(f.sessionOrder, name)
	return nil
}

func (f *FakeExecutor) KillSession(name string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.sessions[name]; !ok {
		return ErrNoSession
	}
	delete(f.sessions, name)
	// Remove from insertion-order slice.
	for i, n := range f.sessionOrder {
		if n == name {
			f.sessionOrder = append(f.sessionOrder[:i], f.sessionOrder[i+1:]...)
			break
		}
	}
	return nil
}

func (f *FakeExecutor) RenameSession(oldName, newName string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	s, ok := f.sessions[oldName]
	if !ok {
		return ErrNoSession
	}
	delete(f.sessions, oldName)
	s.Name = newName
	f.sessions[newName] = s
	for i, n := range f.sessionOrder {
		if n == oldName {
			f.sessionOrder[i] = newName
			break
		}
	}
	return nil
}

func (f *FakeExecutor) HasSession(name string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	_, ok := f.sessions[name]
	return ok
}

func (f *FakeExecutor) SendKeys(target, keys string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.keysCalls = append(f.keysCalls, KeysCall{Target: target, Keys: keys})
	return nil
}

func (f *FakeExecutor) KeysSent() []KeysCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.keysCalls
}

func (f *FakeExecutor) SendKeysRaw(target string, keys ...string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.rawKeysCalls = append(f.rawKeysCalls, RawKeysCall{Target: target, Keys: keys})
	if f.FailSendKeys {
		return fmt.Errorf("send-keys: simulated failure")
	}
	return nil
}

func (f *FakeExecutor) RawKeysSent() []RawKeysCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.rawKeysCalls
}

func (f *FakeExecutor) SetPaneCommand(target, cmd string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.paneCommands[target] = cmd
}

func (f *FakeExecutor) SetPaneContent(target, content string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.paneContents[target] = content
}

func (f *FakeExecutor) SetPaneChildren(target string, cmds []string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.paneChildren[target] = cmds
}

func (f *FakeExecutor) PanePID(target string) (string, error) {
	return "fake-pid", nil
}

func (f *FakeExecutor) PaneChildCommands(target string) ([]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	cmds, ok := f.paneChildren[target]
	if !ok {
		return nil, nil // no children
	}
	return cmds, nil
}

func (f *FakeExecutor) PaneCurrentCommand(target string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.paneCommandCalls[target]++
	cmd, ok := f.paneCommands[target]
	if !ok {
		return "", fmt.Errorf("no pane command for target %q", target)
	}
	return cmd, nil
}

// PaneCommandCallCount returns how many times PaneCurrentCommand was called for a target.
func (f *FakeExecutor) PaneCommandCallCount(target string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.paneCommandCalls[target]
}

func (f *FakeExecutor) CapturePaneContent(target string, lastN int) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	content, ok := f.paneContents[target]
	if !ok {
		return "", fmt.Errorf("no pane content for target %q", target)
	}
	return content, nil
}

func (f *FakeExecutor) SetPaneSize(target string, cols, rows int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.paneSizes[target] = [2]int{cols, rows}
}

func (f *FakeExecutor) PaneSizeOf(target string) ([2]int, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	sz, ok := f.paneSizes[target]
	return sz, ok
}

func (f *FakeExecutor) PaneSize(target string) (int, int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	sz, ok := f.paneSizes[target]
	if !ok {
		return 80, 24, nil // default
	}
	return sz[0], sz[1], nil
}

func (f *FakeExecutor) ResizeWindow(target string, cols, rows int) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.paneSizes[target] = [2]int{cols, rows}
	return nil
}

func (f *FakeExecutor) ResizeWindowAuto(target string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.autoResizeCalls = append(f.autoResizeCalls, target)
	return nil
}

func (f *FakeExecutor) AutoResizeCalls() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.autoResizeCalls
}

func (f *FakeExecutor) SetWindowOption(target, option, value string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.setWindowOptionCalls = append(f.setWindowOptionCalls, SetWindowOptionCall{
		Target: target,
		Option: option,
		Value:  value,
	})
	return nil
}

func (f *FakeExecutor) SetWindowOptionCalls() []SetWindowOptionCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.setWindowOptionCalls
}

func (f *FakeExecutor) SetHookGlobal(event, command string) error { return nil }
func (f *FakeExecutor) RemoveHookGlobal(event string) error      { return nil }

func (f *FakeExecutor) ShowHooksGlobal() (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.HooksOutput, nil
}

func (f *FakeExecutor) SetAlive(v bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.alive = v
}

func (f *FakeExecutor) TmuxAlive() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.alive
}
