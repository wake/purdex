package convfeed

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sync"
	"testing"
	"time"
)

// memFile is an in-memory transcript: random access, a size, and a writer that
// appends, replaces or truncates it, as Claude Code does to the real file.
type memFile struct {
	mu sync.Mutex
	b  []byte
	// onRead, when set, runs before each ReadAt (a test's hook, e.g. to cancel a context).
	onRead func(n int)
	reads  int
}

func newMem(lines ...[]byte) *memFile {
	m := &memFile{}
	m.Append(lines...)
	return m
}

// Append adds whole lines (each gets its newline).
func (m *memFile) Append(lines ...[]byte) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, l := range lines {
		m.b = append(m.b, l...)
		m.b = append(m.b, '\n')
	}
}

// AppendRaw adds bytes as they are (a line may stay unterminated).
func (m *memFile) AppendRaw(b []byte) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.b = append(m.b, b...)
}

// Set replaces the whole content (a rewrite in place when the identity stays).
func (m *memFile) Set(b []byte) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.b = append([]byte(nil), b...)
}

func (m *memFile) Size() (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return int64(len(m.b)), nil
}

func (m *memFile) ReadAt(p []byte, off int64) (int, error) {
	m.mu.Lock()
	hook := m.onRead
	m.reads++
	n := m.reads
	m.mu.Unlock()
	if hook != nil {
		hook(n)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if off >= int64(len(m.b)) {
		return 0, io.EOF
	}
	c := copy(p, m.b[off:])
	if c < len(p) {
		return c, io.EOF
	}
	return c, nil
}

func (m *memFile) bytes() []byte {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]byte(nil), m.b...)
}

func src(m *memFile, id string, live bool) Source { return Source{File: m, Identity: id, Live: live} }

func refresh(t testing.TB, e *Entry, s Source) RefreshResult {
	t.Helper()
	r, err := e.Refresh(context.Background(), s)
	if err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	return r
}

// ---- row builders (the CC 2.1.292 shapes the ccnorm tests use) ----

type obj = map[string]any

const (
	sidA = "7e7f214b-c4e3-48cd-ab15-62a3471bd4fd"
	t0   = 1791378000000
)

func at(s float64) string {
	return time.UnixMilli(t0 + int64(s*1000)).UTC().Format("2006-01-02T15:04:05.000Z")
}

func row(o obj) []byte {
	b, err := json.Marshal(o)
	if err != nil {
		panic(err)
	}
	return b
}

func common(typ, uuid string, sec float64) obj {
	return obj{
		"type": typ, "uuid": uuid, "timestamp": at(sec), "isSidechain": false,
		"userType": "external", "entrypoint": "cli", "cwd": "/work/x",
		"sessionId": sidA, "version": "2.1.292", "gitBranch": "HEAD",
	}
}

func userRow(uuid string, sec float64, text string) []byte {
	o := common("user", uuid, sec)
	o["message"] = obj{"role": "user", "content": text}
	o["origin"] = obj{"kind": "human"}
	o["promptSource"] = "typed"
	o["turnOrigin"] = "human"
	o["turnPosition"] = obj{"promptIndex": 0, "turnIndex": 0}
	return row(o)
}

func assistantRow(uuid string, sec float64, block obj) []byte {
	o := common("assistant", uuid, sec)
	o["message"] = obj{
		"model": "claude-opus-5-5", "id": "msg_x", "type": "message", "role": "assistant",
		"content": []obj{block}, "stop_reason": "end_turn",
	}
	o["apiBlockIndex"] = 0
	o["effort"] = "medium"
	o["perTurnEffort"] = "medium"
	return row(o)
}

func assistantText(uuid string, sec float64, text string) []byte {
	return assistantRow(uuid, sec, obj{"type": "text", "text": text})
}

func toolUseRow(uuid string, sec float64, id, command string) []byte {
	return assistantRow(uuid, sec, obj{"type": "tool_use", "id": id, "name": "Bash", "input": obj{"command": command}})
}

func toolResultRow(uuid string, sec float64, toolUseID, text string) []byte {
	o := common("user", uuid, sec)
	o["message"] = obj{"role": "user", "content": []obj{{"type": "tool_result", "tool_use_id": toolUseID, "content": text}}}
	return row(o)
}

func customTitle(s string) []byte {
	return row(obj{"type": "custom-title", "customTitle": s, "sessionId": sidA})
}

// turnsOf is how many turns the entry holds, through a window with no cap.
func turnsOf(e *Entry) int {
	return len(e.Window(1000, -1, func([]byte) bool { return true }).Turns)
}

func everything([]byte) bool { return true }

// idle is a conversation of n finished turns: user text and an assistant answer each.
func idle(n int) [][]byte {
	var lines [][]byte
	for i := 0; i < n; i++ {
		lines = append(lines, userRow(fmt.Sprintf("u%d", i), float64(i*2), fmt.Sprintf("question %d", i)))
		lines = append(lines, assistantText(fmt.Sprintf("a%d", i), float64(i*2+1), fmt.Sprintf("answer %d", i)))
	}
	return lines
}

func joinLines(lines [][]byte) []byte {
	return append(bytes.Join(lines, []byte{'\n'}), '\n')
}
