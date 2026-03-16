# Phase 2: Stream 模式 Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 實作 Claude Code stream-json 互動模式 — daemon 管理 `claude -p` 子程序，SPA 以結構化 web UI 渲染對話（markdown、程式碼高亮、tool call、權限提示），支援 term ↔ stream 模式切換。

**Architecture:** Daemon 新增 StreamManager 管理 `claude -p` 子程序生命週期，透過 WebSocket `/ws/stream/:session` 雙向中繼 NDJSON。SPA 新增 ConversationView 渲染結構化對話、StreamInput 處理使用者輸入、PermissionPrompt 和 AskUserQuestion 處理 control_request。TopBar 提供模式切換。Phosphor Icons 統一圖示。

**Tech Stack:**
- Daemon: Go（現有 + 新增 internal/stream/）
- SPA: React / Zustand / react-markdown / rehype-highlight / @phosphor-icons/react
- 協定參考: `/STREAM_JSON_PROTOCOL.md`

**前置條件:** Phase 1 完成（daemon + terminal mode 可用）

**命名:** CLI=`tbox`, module=`github.com/wake/tmux-box`, config=`~/.config/tbox/`

---

## File Structure

### 新增 Go 檔案

```
internal/
└── stream/
    ├── manager.go              # StreamManager — claude -p 子程序管理
    ├── manager_test.go
    ├── session.go              # StreamSession — 單一 stream 連線
    └── session_test.go
```

### 修改 Go 檔案

```
internal/server/server.go           # 加入 /ws/stream/{session}, /api/sessions/{id}/mode
internal/server/session_handler.go  # 加入 SwitchMode handler
internal/store/store.go             # 加入 GetSession(id)
cmd/tbox/main.go                    # 注入 StreamManager
```

### 新增 SPA 檔案

```
spa/src/
├── components/
│   ├── TopBar.tsx              # 上方工具列（session 名稱、模式切換、interrupt、model）
│   ├── TopBar.test.tsx
│   ├── ConversationView.tsx    # stream 模式主畫面（訊息列表 + 自動捲動）
│   ├── ConversationView.test.tsx
│   ├── MessageBubble.tsx       # 單則訊息（user / assistant）
│   ├── MessageBubble.test.tsx
│   ├── ToolCallBlock.tsx       # 工具呼叫區塊（可摺疊）
│   ├── ToolCallBlock.test.tsx
│   ├── PermissionPrompt.tsx    # Allow / Deny 按鈕
│   ├── PermissionPrompt.test.tsx
│   ├── AskUserQuestion.tsx     # 選項元件（radio / checkbox）
│   ├── AskUserQuestion.test.tsx
│   ├── StreamInput.tsx         # 底部訊息輸入框
│   └── StreamInput.test.tsx
├── stores/
│   ├── useStreamStore.ts       # stream 模式狀態（messages, pending, control_requests）
│   └── useStreamStore.test.ts
└── lib/
    ├── stream-ws.ts            # stream WebSocket 連線管理
    └── stream-ws.test.ts
```

### 修改 SPA 檔案

```
spa/src/App.tsx                     # 加入 TopBar、ConversationView 切換、layout 重構
spa/src/components/SessionPanel.tsx # 狀態燈號、設定按鈕
spa/src/lib/api.ts                  # 加入 switchMode API
spa/src/stores/useSessionStore.ts   # 加入 mode 追蹤
spa/package.json                    # 加入 phosphor-icons, react-markdown, rehype-highlight
```

---

## Chunk 1: Go StreamManager + WebSocket

### Task 1: Store 新增 GetSession

**Files:**
- Modify: `internal/store/store.go`
- Modify: `internal/store/store_test.go`

- [ ] **Step 1: 寫測試**

在 `store_test.go` 加入：

```go
func TestGetSession(t *testing.T) {
	db := openTestDB(t)

	s := store.Session{Name: "myapp", TmuxTarget: "myapp:0", Cwd: "/tmp", Mode: "term"}
	id, _ := db.CreateSession(s)

	got, err := db.GetSession(id)
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "myapp" {
		t.Errorf("want myapp, got %s", got.Name)
	}
}

func TestGetSessionNotFound(t *testing.T) {
	db := openTestDB(t)
	_, err := db.GetSession(999)
	if err != store.ErrNotFound {
		t.Errorf("want ErrNotFound, got %v", err)
	}
}
```

- [ ] **Step 2: 確認測試失敗**

Run: `go test ./internal/store/...`
Expected: FAIL — GetSession 不存在

- [ ] **Step 3: 實作**

在 `store.go` 加入：

```go
func (s *Store) GetSession(id int64) (Session, error) {
	var sess Session
	err := s.db.QueryRow(
		"SELECT id, name, tmux_target, cwd, mode, group_id, sort_order FROM sessions WHERE id = ?", id,
	).Scan(&sess.ID, &sess.Name, &sess.TmuxTarget, &sess.Cwd, &sess.Mode, &sess.GroupID, &sess.SortOrder)
	if err != nil {
		if err == sql.ErrNoRows {
			return sess, ErrNotFound
		}
		return sess, err
	}
	return sess, nil
}
```

- [ ] **Step 4: 確認測試通過**

Run: `go test ./internal/store/...`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/store/
git commit -m "feat: add GetSession to store"
```

---

### Task 2: StreamSession — 單一 claude -p 連線

**Files:**
- Create: `internal/stream/session.go`
- Create: `internal/stream/session_test.go`

- [ ] **Step 1: 寫測試**

```go
// internal/stream/session_test.go
package stream_test

import (
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/wake/tmux-box/internal/stream"
)

func TestSessionStartStop(t *testing.T) {
	// Use "cat" as a fake claude -p (echoes stdin to stdout)
	s, err := stream.NewSession("cat", []string{}, "/tmp")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Stop()

	if !s.Running() {
		t.Error("session should be running")
	}

	s.Stop()

	if s.Running() {
		t.Error("session should not be running after Stop")
	}
}

func TestSessionSendReceive(t *testing.T) {
	s, err := stream.NewSession("cat", []string{}, "/tmp")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Stop()

	// Subscribe to output
	ch := s.Subscribe()
	defer s.Unsubscribe(ch)

	// Send a JSON line
	msg := map[string]string{"type": "user", "text": "hello"}
	data, _ := json.Marshal(msg)
	s.Send(data)

	// Should receive it back (cat echoes)
	select {
	case line := <-ch:
		if !strings.Contains(string(line), "hello") {
			t.Errorf("want line containing hello, got %s", line)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for echo")
	}
}

func TestSessionMultipleSubscribers(t *testing.T) {
	s, err := stream.NewSession("cat", []string{}, "/tmp")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Stop()

	ch1 := s.Subscribe()
	ch2 := s.Subscribe()
	defer s.Unsubscribe(ch1)
	defer s.Unsubscribe(ch2)

	msg, _ := json.Marshal(map[string]string{"test": "multi"})
	s.Send(msg)

	var wg sync.WaitGroup
	wg.Add(2)
	for _, ch := range []<-chan []byte{ch1, ch2} {
		go func(c <-chan []byte) {
			defer wg.Done()
			select {
			case <-c:
			case <-time.After(2 * time.Second):
				t.Error("timeout")
			}
		}(ch)
	}
	wg.Wait()
}
```

- [ ] **Step 2: 確認測試失敗**

Run: `go test ./internal/stream/...`
Expected: FAIL

- [ ] **Step 3: 實作**

```go
// internal/stream/session.go
package stream

import (
	"bufio"
	"io"
	"os/exec"
	"sync"
	"syscall"
	"time"
)

// StreamSession manages a single claude -p subprocess.
type StreamSession struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout io.ReadCloser

	mu          sync.RWMutex
	subscribers map[chan []byte]struct{}
	running     bool
	done        chan struct{}
}

// NewSession starts a subprocess and begins reading its stdout.
func NewSession(command string, args []string, cwd string) (*StreamSession, error) {
	cmd := exec.Command(command, args...)
	cmd.Dir = cwd

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		stdin.Close()
		return nil, err
	}

	if err := cmd.Start(); err != nil {
		stdin.Close()
		stdout.Close()
		return nil, err
	}

	s := &StreamSession{
		cmd:         cmd,
		stdin:       stdin,
		stdout:      stdout,
		subscribers: make(map[chan []byte]struct{}),
		running:     true,
		done:        make(chan struct{}),
	}

	go s.readLoop()
	return s, nil
}

func (s *StreamSession) readLoop() {
	defer func() {
		s.mu.Lock()
		s.running = false
		close(s.done)
		// Close all subscriber channels
		for ch := range s.subscribers {
			close(ch)
			delete(s.subscribers, ch)
		}
		s.mu.Unlock()
	}()

	scanner := bufio.NewScanner(s.stdout)
	scanner.Buffer(make([]byte, 1024*1024), 1024*1024) // 1MB buffer for large JSON
	for scanner.Scan() {
		line := make([]byte, len(scanner.Bytes()))
		copy(line, scanner.Bytes())

		s.mu.RLock()
		for ch := range s.subscribers {
			select {
			case ch <- line:
			default:
				// Drop if subscriber can't keep up
			}
		}
		s.mu.RUnlock()
	}
}

// Send writes a JSON line to the subprocess stdin.
func (s *StreamSession) Send(data []byte) error {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if !s.running {
		return io.ErrClosedPipe
	}
	// Append newline if not present
	if len(data) == 0 || data[len(data)-1] != '\n' {
		data = append(data, '\n')
	}
	_, err := s.stdin.Write(data)
	return err
}

// Subscribe returns a channel that receives stdout lines.
func (s *StreamSession) Subscribe() <-chan []byte {
	ch := make(chan []byte, 64)
	s.mu.Lock()
	s.subscribers[ch] = struct{}{}
	s.mu.Unlock()
	return ch
}

// Unsubscribe removes a subscriber channel.
func (s *StreamSession) Unsubscribe(ch <-chan []byte) {
	s.mu.Lock()
	// Type assertion to get the writable channel
	for c := range s.subscribers {
		if c == ch {
			delete(s.subscribers, c)
			break
		}
	}
	s.mu.Unlock()
}

// Running returns whether the subprocess is still alive.
func (s *StreamSession) Running() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.running
}

// Stop gracefully terminates the subprocess.
func (s *StreamSession) Stop() {
	s.stdin.Close()
	s.cmd.Process.Signal(syscall.SIGTERM)
	select {
	case <-s.done:
	case <-time.After(5 * time.Second):
		s.cmd.Process.Kill()
		<-s.done
	}
	s.cmd.Wait()
}

// Done returns a channel that closes when the subprocess exits.
func (s *StreamSession) Done() <-chan struct{} {
	return s.done
}
```

- [ ] **Step 4: 確認測試通過**

Run: `go mod tidy && go test ./internal/stream/... -v`
Expected: PASS (3 tests)

- [ ] **Step 5: Commit**

```bash
git add internal/stream/
git commit -m "feat: add StreamSession for claude -p subprocess management"
```

---

### Task 3: StreamManager — 多 session 管理

**Files:**
- Create: `internal/stream/manager.go`
- Create: `internal/stream/manager_test.go`

- [ ] **Step 1: 寫測試**

```go
// internal/stream/manager_test.go
package stream_test

import (
	"testing"

	"github.com/wake/tmux-box/internal/stream"
)

func TestManagerStartStop(t *testing.T) {
	mgr := stream.NewManager()
	defer mgr.StopAll()

	err := mgr.Start("test-1", "cat", []string{}, "/tmp")
	if err != nil {
		t.Fatal(err)
	}

	if !mgr.Has("test-1") {
		t.Error("should have test-1")
	}

	sess := mgr.Get("test-1")
	if sess == nil {
		t.Fatal("Get returned nil")
	}
	if !sess.Running() {
		t.Error("session should be running")
	}

	mgr.Stop("test-1")

	if mgr.Has("test-1") {
		t.Error("should not have test-1 after stop")
	}
}

func TestManagerDuplicateStart(t *testing.T) {
	mgr := stream.NewManager()
	defer mgr.StopAll()

	mgr.Start("dup", "cat", []string{}, "/tmp")
	err := mgr.Start("dup", "cat", []string{}, "/tmp")
	if err == nil {
		t.Error("want error for duplicate start")
	}
}

func TestManagerStopAll(t *testing.T) {
	mgr := stream.NewManager()

	mgr.Start("a", "cat", []string{}, "/tmp")
	mgr.Start("b", "cat", []string{}, "/tmp")

	mgr.StopAll()

	if mgr.Has("a") || mgr.Has("b") {
		t.Error("all sessions should be stopped")
	}
}
```

- [ ] **Step 2: 確認測試失敗**

Run: `go test ./internal/stream/...`
Expected: FAIL — NewManager 不存在

- [ ] **Step 3: 實作**

```go
// internal/stream/manager.go
package stream

import (
	"fmt"
	"sync"
)

// Manager tracks multiple StreamSessions by name.
type Manager struct {
	mu       sync.RWMutex
	sessions map[string]*StreamSession
}

func NewManager() *Manager {
	return &Manager{sessions: make(map[string]*StreamSession)}
}

// Start creates and tracks a new stream session.
func (m *Manager) Start(name, command string, args []string, cwd string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, exists := m.sessions[name]; exists {
		return fmt.Errorf("stream session %q already running", name)
	}

	s, err := NewSession(command, args, cwd)
	if err != nil {
		return fmt.Errorf("start stream %q: %w", name, err)
	}

	m.sessions[name] = s

	// Auto-remove when process exits
	go func() {
		<-s.Done()
		m.mu.Lock()
		delete(m.sessions, name)
		m.mu.Unlock()
	}()

	return nil
}

// Get returns the session by name, or nil.
func (m *Manager) Get(name string) *StreamSession {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.sessions[name]
}

// Has checks if a session exists.
func (m *Manager) Has(name string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	_, ok := m.sessions[name]
	return ok
}

// Stop terminates a specific session.
func (m *Manager) Stop(name string) {
	m.mu.Lock()
	s, ok := m.sessions[name]
	if ok {
		delete(m.sessions, name)
	}
	m.mu.Unlock()

	if s != nil {
		s.Stop()
	}
}

// StopAll terminates all sessions.
func (m *Manager) StopAll() {
	m.mu.Lock()
	sessions := make(map[string]*StreamSession, len(m.sessions))
	for k, v := range m.sessions {
		sessions[k] = v
	}
	m.sessions = make(map[string]*StreamSession)
	m.mu.Unlock()

	for _, s := range sessions {
		s.Stop()
	}
}
```

- [ ] **Step 4: 確認測試通過**

Run: `go test ./internal/stream/... -v`
Expected: PASS (6 tests)

- [ ] **Step 5: Commit**

```bash
git add internal/stream/
git commit -m "feat: add StreamManager for multi-session tracking"
```

---

### Task 4: WebSocket /ws/stream/ 端點

**Files:**
- Create: `internal/server/stream_handler.go`
- Create: `internal/server/stream_handler_test.go`
- Modify: `internal/server/server.go`

- [ ] **Step 1: 寫測試**

```go
// internal/server/stream_handler_test.go
package server_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/wake/tmux-box/internal/stream"
)

func TestStreamWSEcho(t *testing.T) {
	mgr := stream.NewManager()
	defer mgr.StopAll()

	// Start a "cat" session to echo input
	mgr.Start("echo-test", "cat", []string{}, "/tmp")

	handler := &streamTestHandler{mgr: mgr}
	srv := httptest.NewServer(http.HandlerFunc(handler.handle))
	defer srv.Close()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "?session=echo-test"
	ws, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer ws.Close()

	// Send a JSON message
	msg := map[string]interface{}{"type": "user", "message": map[string]string{"role": "user", "content": "hello"}}
	data, _ := json.Marshal(msg)
	ws.WriteMessage(websocket.TextMessage, data)

	// Read echo
	ws.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, reply, err := ws.ReadMessage()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(reply), "hello") {
		t.Errorf("want echo containing hello, got %s", reply)
	}
}

type streamTestHandler struct {
	mgr *stream.Manager
}

func (h *streamTestHandler) handle(w http.ResponseWriter, r *http.Request) {
	name := r.URL.Query().Get("session")
	sess := h.mgr.Get(name)
	if sess == nil {
		http.Error(w, "not found", 404)
		return
	}
	server.HandleStreamWS(w, r, sess)
}
```

注意：`HandleStreamWS` 是匯出函式（大寫開頭），因為測試檔案在 `package server_test`（外部測試包）。測試中使用 query param 而非 path param 簡化測試伺服器。

- [ ] **Step 2: 確認測試失敗**

Run: `go test ./internal/server/...`
Expected: FAIL — handleStreamWS 不存在

- [ ] **Step 3: 實作 stream_handler.go**

```go
// internal/server/stream_handler.go
package server

import (
	"encoding/json"
	"log"
	"net/http"

	"github.com/gorilla/websocket"
	"github.com/wake/tmux-box/internal/stream"
)

// HandleStreamWS bridges a WebSocket connection to a StreamSession's stdin/stdout.
func HandleStreamWS(w http.ResponseWriter, r *http.Request, sess *stream.StreamSession) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("stream ws upgrade: %v", err)
		return
	}
	defer conn.Close()

	// Subscribe to session output
	ch := sess.Subscribe()
	defer sess.Unsubscribe(ch)

	done := make(chan struct{})

	// Session stdout → WebSocket
	go func() {
		defer close(done)
		for line := range ch {
			if err := conn.WriteMessage(websocket.TextMessage, line); err != nil {
				return
			}
		}
	}()

	// WebSocket → Session stdin
	go func() {
		for {
			_, msg, err := conn.ReadMessage()
			if err != nil {
				return
			}
			sess.Send(msg)
		}
	}()

	// Wait for either direction to close
	select {
	case <-done:
	case <-sess.Done():
		conn.WriteMessage(websocket.CloseMessage,
			websocket.FormatCloseMessage(websocket.CloseNormalClosure, "session ended"))
	}
}

var streamUpgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool { return true },
}
```

注意：`terminal/relay.go` 中也有 `upgrader` 變數，但在不同 package（`terminal`），不衝突。此處用 `streamUpgrader` 命名。在 `HandleStreamWS` 中把 `upgrader.Upgrade` 改為 `streamUpgrader.Upgrade`。

- [ ] **Step 4: 在 server.go 加入路由**

在 `Server` struct 加入 `streams *stream.Manager` 欄位。

在 `New()` 函式的參數加入 `sm *stream.Manager`。

在 `routes()` 加入：

```go
s.mux.HandleFunc("/ws/stream/{session}", s.handleStream)
```

加入 method：

```go
func (s *Server) handleStream(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("session")
	sess := s.streams.Get(name)
	if sess == nil {
		http.Error(w, "stream session not found", 404)
		return
	}
	HandleStreamWS(w, r, sess)
}
```

同時修改 `SessionHandler` struct 加入 `streams *stream.Manager`，更新 `NewSessionHandler` 簽名為 `NewSessionHandler(s *store.Store, t tmux.Executor, sm *stream.Manager)`。在 `routes()` 中更新為 `NewSessionHandler(s.store, s.tmux, s.streams)`。

同時更新 `session_handler_test.go` 的 `setupHandler` 加入 `stream.NewManager()` 注入。

同時更新 `main.go`：

```go
sm := stream.NewManager()
defer sm.StopAll()

srv := server.New(cfg, st, tx, sm)
```

**注意：server.go、session_handler.go、session_handler_test.go、main.go 必須一起修改，否則無法編譯。**

- [ ] **Step 6: 確認所有測試通過**

Run: `go test ./...`
Expected: PASS

- [ ] **Step 7: Commit**

```bash
git add internal/server/ internal/stream/ cmd/tbox/main.go
git commit -m "feat: add stream WebSocket endpoint with bidirectional JSON relay"
```

---

### Task 5: Mode Switch API

**Files:**
- Modify: `internal/server/session_handler.go`
- Modify: `internal/server/session_handler_test.go`
- Modify: `internal/server/server.go`

- [ ] **Step 1: 寫測試**

在 `session_handler_test.go` 加入：

```go
func TestSwitchModeToStream(t *testing.T) {
	h := setupHandler(t)

	// Create a term session
	body, _ := json.Marshal(map[string]string{"name": "switch-test", "cwd": "/tmp", "mode": "term"})
	rec := httptest.NewRecorder()
	h.Create(rec, httptest.NewRequest("POST", "/api/sessions", bytes.NewReader(body)))
	if rec.Code != 201 {
		t.Fatalf("create: want 201, got %d", rec.Code)
	}

	// Switch to stream
	switchBody, _ := json.Marshal(map[string]string{"mode": "stream"})
	req := httptest.NewRequest("POST", "/api/sessions/1/mode", bytes.NewReader(switchBody))
	req.SetPathValue("id", "1")
	rec = httptest.NewRecorder()
	h.SwitchMode(rec, req)

	if rec.Code != 200 {
		t.Errorf("switch: want 200, got %d: %s", rec.Code, rec.Body.String())
	}

	// Verify mode changed in store
	sessions, _ := h.store.ListSessions()
	if sessions[0].Mode != "stream" {
		t.Errorf("want mode stream, got %s", sessions[0].Mode)
	}
}

func TestSwitchModeInvalidMode(t *testing.T) {
	h := setupHandler(t)

	body, _ := json.Marshal(map[string]string{"name": "test", "cwd": "/tmp", "mode": "term"})
	rec := httptest.NewRecorder()
	h.Create(rec, httptest.NewRequest("POST", "/api/sessions", bytes.NewReader(body)))

	switchBody, _ := json.Marshal(map[string]string{"mode": "invalid"})
	req := httptest.NewRequest("POST", "/api/sessions/1/mode", bytes.NewReader(switchBody))
	req.SetPathValue("id", "1")
	rec = httptest.NewRecorder()
	h.SwitchMode(rec, req)

	if rec.Code != 400 {
		t.Errorf("want 400, got %d", rec.Code)
	}
}
```

注意：`setupHandler` 需要更新以注入 `stream.Manager`。更新 `setupHandler`：

```go
func setupHandler(t *testing.T) *server.SessionHandler {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	fake := tmux.NewFakeExecutor()
	sm := stream.NewManager()
	t.Cleanup(func() { sm.StopAll() })
	return server.NewSessionHandler(db, fake, sm)
}
```

- [ ] **Step 2: 確認測試失敗**

Run: `go test ./internal/server/...`
Expected: FAIL — SwitchMode 不存在

- [ ] **Step 3: 實作 SwitchMode handler**

在 `session_handler.go` 加入：

```go
type switchModeReq struct {
	Mode string `json:"mode"`
}

func (h *SessionHandler) SwitchMode(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "invalid id", 400)
		return
	}

	var req switchModeReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid json", 400)
		return
	}

	if req.Mode != "term" && req.Mode != "stream" {
		http.Error(w, "mode must be term or stream", 400)
		return
	}

	sess, err := h.store.GetSession(id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			http.Error(w, "not found", 404)
		} else {
			http.Error(w, err.Error(), 500)
		}
		return
	}

	if sess.Mode == req.Mode {
		w.WriteHeader(200)
		json.NewEncoder(w).Encode(map[string]string{"status": "already in mode " + req.Mode})
		return
	}

	// Switching from stream → term: stop stream process
	if sess.Mode == "stream" && req.Mode == "term" {
		h.streams.Stop(sess.Name)
	}

	// Switching from term → stream: start claude -p
	if sess.Mode == "term" && req.Mode == "stream" {
		claudeArgs := []string{
			"-p", "placeholder",
			"--input-format", "stream-json",
			"--output-format", "stream-json",
			"--verbose",
		}
		if err := h.streams.Start(sess.Name, "claude", claudeArgs, sess.Cwd); err != nil {
			http.Error(w, "start stream: "+err.Error(), 500)
			return
		}
	}

	// Update mode in store
	h.store.UpdateSession(id, store.SessionUpdate{Mode: &req.Mode})

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "switched to " + req.Mode})
}
```

更新 `SessionHandler` struct 加入 `streams *stream.Manager`，更新 `NewSessionHandler` 簽名。

在 `server.go` 的 `routes()` 加入：

```go
s.mux.HandleFunc("POST /api/sessions/{id}/mode", sh.SwitchMode)
```

- [ ] **Step 4: 確認測試通過**

Run: `go test ./internal/server/...`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/server/ internal/store/
git commit -m "feat: add mode switch API (term <-> stream)"
```

---

## Chunk 2: SPA 基礎建設（Icons、TopBar、Layout）

### Task 6: 安裝 Phosphor Icons + react-markdown

**Files:**
- Modify: `spa/package.json`

- [ ] **Step 1: 安裝依賴**

```bash
cd spa
pnpm add @phosphor-icons/react react-markdown rehype-highlight highlight.js
```

- [ ] **Step 2: Commit**

```bash
git add spa/package.json spa/pnpm-lock.yaml
git commit -m "feat: add phosphor-icons, react-markdown, rehype-highlight"
```

---

### Task 7: API client 新增 switchMode

**Files:**
- Modify: `spa/src/lib/api.ts`
- Modify: `spa/src/lib/api.test.ts`

- [ ] **Step 1: 寫測試**

在 `api.test.ts` 加入：

```typescript
describe('switchMode', () => {
  it('posts mode switch', async () => {
    const spy = vi.spyOn(globalThis, 'fetch').mockResolvedValue(
      new Response(JSON.stringify({ status: 'switched to stream' }), { status: 200 })
    )
    const result = await switchMode('http://localhost:7860', 1, 'stream')
    expect(spy).toHaveBeenCalledWith(
      'http://localhost:7860/api/sessions/1/mode',
      expect.objectContaining({
        method: 'POST',
        body: JSON.stringify({ mode: 'stream' }),
      })
    )
    expect(result.status).toBe('switched to stream')
  })
})
```

- [ ] **Step 2: 確認測試失敗**

Run: `cd spa && pnpm vitest run src/lib/api.test.ts`
Expected: FAIL

- [ ] **Step 3: 實作**

在 `api.ts` 加入：

```typescript
export async function switchMode(base: string, id: number, mode: string): Promise<{ status: string }> {
  const res = await fetch(`${base}/api/sessions/${id}/mode`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ mode }),
  })
  if (!res.ok) throw new Error(`${res.status} ${res.statusText}`)
  return res.json()
}
```

- [ ] **Step 4: 確認測試通過**

Run: `cd spa && pnpm vitest run src/lib/api.test.ts`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add spa/src/lib/api.ts spa/src/lib/api.test.ts
git commit -m "feat: add switchMode API client"
```

---

### Task 8: Stream WebSocket 連線管理

**Files:**
- Create: `spa/src/lib/stream-ws.ts`
- Create: `spa/src/lib/stream-ws.test.ts`

- [ ] **Step 1: 寫測試**

```typescript
// spa/src/lib/stream-ws.test.ts
import { describe, it, expect, vi } from 'vitest'
import { parseStreamMessage, type StreamMessage } from './stream-ws'

describe('parseStreamMessage', () => {
  it('parses assistant text message', () => {
    const raw = JSON.stringify({
      type: 'assistant',
      message: {
        role: 'assistant',
        content: [{ type: 'text', text: 'Hello world' }],
        stop_reason: 'end_turn',
      },
    })
    const msg = parseStreamMessage(raw)
    expect(msg?.type).toBe('assistant')
    if (msg?.type === 'assistant') {
      expect(msg.message.content[0]).toEqual({ type: 'text', text: 'Hello world' })
    }
  })

  it('parses result message', () => {
    const raw = JSON.stringify({
      type: 'result',
      subtype: 'success',
      total_cost_usd: 0.05,
      session_id: 'abc',
    })
    const msg = parseStreamMessage(raw)
    expect(msg?.type).toBe('result')
  })

  it('parses control_request', () => {
    const raw = JSON.stringify({
      type: 'control_request',
      request_id: 'uuid-1',
      request: {
        subtype: 'can_use_tool',
        tool_name: 'Bash',
        input: { command: 'ls' },
        tool_use_id: 'tool-1',
      },
    })
    const msg = parseStreamMessage(raw)
    expect(msg?.type).toBe('control_request')
  })

  it('returns null for invalid JSON', () => {
    expect(parseStreamMessage('not json')).toBeNull()
  })
})
```

- [ ] **Step 2: 確認測試失敗**

Run: `cd spa && pnpm vitest run src/lib/stream-ws.test.ts`
Expected: FAIL

- [ ] **Step 3: 實作**

```typescript
// spa/src/lib/stream-ws.ts

// --- Message Types ---

export interface ContentBlock {
  type: 'text' | 'tool_use' | 'tool_result' | 'thinking'
  text?: string
  id?: string
  name?: string
  input?: Record<string, unknown>
  content?: string
  is_error?: boolean
  thinking?: string
  tool_use_id?: string
}

export interface AssistantMessage {
  type: 'assistant'
  message: {
    role: 'assistant'
    content: ContentBlock[]
    stop_reason: string | null
  }
}

export interface UserMessage {
  type: 'user'
  message: {
    role: 'user'
    content: ContentBlock[]
    stop_reason: string | null
  }
}

export interface ResultMessage {
  type: 'result'
  subtype: string
  total_cost_usd?: number
  session_id?: string
  duration_ms?: number
}

export interface SystemMessage {
  type: 'system'
  subtype: string
  session_id?: string
  tools?: string[]
  model?: string
  permissionMode?: string
  [key: string]: unknown
}

export interface ControlRequest {
  type: 'control_request'
  request_id: string
  request: {
    subtype: string
    tool_name?: string
    input?: Record<string, unknown>
    tool_use_id?: string
  }
}

export interface StreamEvent {
  type: 'stream_event'
  event: {
    type: string
    delta?: { type: string; text?: string }
    [key: string]: unknown
  }
}

export type StreamMessage =
  | AssistantMessage
  | UserMessage
  | ResultMessage
  | SystemMessage
  | ControlRequest
  | StreamEvent
  | { type: string; [key: string]: unknown }

export function parseStreamMessage(raw: string): StreamMessage | null {
  try {
    return JSON.parse(raw) as StreamMessage
  } catch {
    return null
  }
}

// --- Connection ---

export interface StreamConnection {
  send: (msg: object) => void
  sendControlResponse: (requestId: string, response: object) => void
  interrupt: () => void
  close: () => void
}

export function connectStream(
  url: string,
  onMessage: (msg: StreamMessage) => void,
  onClose: () => void,
  onOpen?: () => void,
): StreamConnection {
  const ws = new WebSocket(url)

  ws.onopen = () => onOpen?.()
  ws.onmessage = (e) => {
    const msg = parseStreamMessage(e.data)
    if (msg) onMessage(msg)
  }
  ws.onerror = () => {}
  ws.onclose = () => onClose()

  const sendJSON = (data: object) => {
    if (ws.readyState === WebSocket.OPEN) {
      ws.send(JSON.stringify(data))
    }
  }

  return {
    send: sendJSON,
    sendControlResponse: (requestId, response) => {
      sendJSON({
        type: 'control_response',
        response: {
          subtype: 'success',
          request_id: requestId,
          response,
        },
      })
    },
    interrupt: () => {
      sendJSON({
        type: 'control_response',
        response: { subtype: 'interrupt' },
      })
    },
    close: () => ws.close(),
  }
}
```

- [ ] **Step 4: 確認測試通過**

Run: `cd spa && pnpm vitest run src/lib/stream-ws.test.ts`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add spa/src/lib/stream-ws.ts spa/src/lib/stream-ws.test.ts
git commit -m "feat: add stream-ws message types and connection manager"
```

---

### Task 9: useStreamStore — Stream 模式狀態管理

**Files:**
- Create: `spa/src/stores/useStreamStore.ts`
- Create: `spa/src/stores/useStreamStore.test.ts`

- [ ] **Step 1: 寫測試**

```typescript
// spa/src/stores/useStreamStore.test.ts
import { describe, it, expect, beforeEach } from 'vitest'
import { useStreamStore } from './useStreamStore'

beforeEach(() => {
  useStreamStore.setState({
    messages: [],
    pendingControlRequests: [],
    isStreaming: false,
    sessionId: null,
    model: null,
    cost: 0,
  })
})

describe('useStreamStore', () => {
  it('adds assistant message', () => {
    const { addMessage } = useStreamStore.getState()
    addMessage({
      type: 'assistant',
      message: {
        role: 'assistant',
        content: [{ type: 'text', text: 'hi' }],
        stop_reason: 'end_turn',
      },
    })
    expect(useStreamStore.getState().messages).toHaveLength(1)
  })

  it('adds control request', () => {
    const { addControlRequest } = useStreamStore.getState()
    addControlRequest({
      type: 'control_request',
      request_id: 'req-1',
      request: { subtype: 'can_use_tool', tool_name: 'Bash', input: { command: 'ls' } },
    })
    expect(useStreamStore.getState().pendingControlRequests).toHaveLength(1)
  })

  it('resolves control request', () => {
    const { addControlRequest, resolveControlRequest } = useStreamStore.getState()
    addControlRequest({
      type: 'control_request',
      request_id: 'req-1',
      request: { subtype: 'can_use_tool', tool_name: 'Bash', input: {} },
    })
    resolveControlRequest('req-1')
    expect(useStreamStore.getState().pendingControlRequests).toHaveLength(0)
  })

  it('tracks streaming state', () => {
    const { setStreaming } = useStreamStore.getState()
    setStreaming(true)
    expect(useStreamStore.getState().isStreaming).toBe(true)
  })

  it('clears messages', () => {
    const { addMessage, clear } = useStreamStore.getState()
    addMessage({
      type: 'assistant',
      message: { role: 'assistant', content: [{ type: 'text', text: 'x' }], stop_reason: null },
    })
    clear()
    expect(useStreamStore.getState().messages).toHaveLength(0)
  })
})
```

- [ ] **Step 2: 確認測試失敗**

Run: `cd spa && pnpm vitest run src/stores/useStreamStore.test.ts`
Expected: FAIL

- [ ] **Step 3: 實作**

```typescript
// spa/src/stores/useStreamStore.ts
import { create } from 'zustand'
import type { StreamMessage, ControlRequest } from '../lib/stream-ws'

interface StreamState {
  messages: StreamMessage[]
  pendingControlRequests: ControlRequest[]
  isStreaming: boolean
  sessionId: string | null
  model: string | null
  cost: number

  addMessage: (msg: StreamMessage) => void
  addControlRequest: (req: ControlRequest) => void
  resolveControlRequest: (requestId: string) => void
  setStreaming: (v: boolean) => void
  setSessionInfo: (sessionId: string, model: string) => void
  addCost: (usd: number) => void
  clear: () => void
}

export const useStreamStore = create<StreamState>((set) => ({
  messages: [],
  pendingControlRequests: [],
  isStreaming: false,
  sessionId: null,
  model: null,
  cost: 0,

  addMessage: (msg) => set((s) => ({ messages: [...s.messages, msg] })),

  addControlRequest: (req) =>
    set((s) => ({ pendingControlRequests: [...s.pendingControlRequests, req] })),

  resolveControlRequest: (requestId) =>
    set((s) => ({
      pendingControlRequests: s.pendingControlRequests.filter(
        (r) => r.request_id !== requestId,
      ),
    })),

  setStreaming: (isStreaming) => set({ isStreaming }),

  setSessionInfo: (sessionId, model) => set({ sessionId, model }),

  addCost: (usd) => set((s) => ({ cost: s.cost + usd })),

  clear: () => set({ messages: [], pendingControlRequests: [], isStreaming: false, cost: 0 }),
}))
```

- [ ] **Step 4: 確認測試通過**

Run: `cd spa && pnpm vitest run src/stores/useStreamStore.test.ts`
Expected: PASS (5 tests)

- [ ] **Step 5: Commit**

```bash
git add spa/src/stores/useStreamStore.ts spa/src/stores/useStreamStore.test.ts
git commit -m "feat: add useStreamStore for stream mode state management"
```

---

### Task 10: SessionPanel 狀態燈號 + 設定按鈕（Phosphor Icons）

**Files:**
- Modify: `spa/src/components/SessionPanel.tsx`
- Modify: `spa/src/components/SessionPanel.test.tsx`

- [ ] **Step 1: 更新測試**

更新 `SessionPanel.test.tsx`，加入狀態燈號測試：

```typescript
it('shows terminal icon for term mode', () => {
  useSessionStore.setState({
    sessions: [
      { id: 1, name: 'dev', tmux_target: 'dev:0', cwd: '/tmp', mode: 'term', group_id: 0, sort_order: 0 },
    ],
    activeId: null,
  })
  render(<SessionPanel />)
  // Terminal icon should be present (Phosphor Terminal icon)
  expect(screen.getByTestId('session-icon-1')).toBeInTheDocument()
})
```

- [ ] **Step 2: 確認測試失敗**

Run: `cd spa && pnpm vitest run src/components/SessionPanel.test.tsx`
Expected: FAIL

- [ ] **Step 3: 實作**

更新 `SessionPanel.tsx`：

```tsx
import { useSessionStore } from '../stores/useSessionStore'
import { Terminal, Lightning, CircleDashed, GearSix } from '@phosphor-icons/react'

function SessionIcon({ mode, id }: { mode: string; id: number }) {
  const props = { size: 16, 'data-testid': `session-icon-${id}` }
  switch (mode) {
    case 'stream': return <Lightning {...props} weight="fill" className="text-blue-400" />
    case 'jsonl': return <CircleDashed {...props} className="text-yellow-400" />
    default: return <Terminal {...props} className="text-gray-400" />
  }
}

export default function SessionPanel() {
  const { sessions, activeId, setActive } = useSessionStore()

  return (
    <div className="w-56 bg-gray-900 border-r border-gray-800 flex flex-col">
      <div className="p-3 flex-1 overflow-y-auto">
        <h2 className="text-xs uppercase text-gray-400 mb-3">Sessions</h2>
        <div className="space-y-1">
          {sessions.length === 0 && <p className="text-sm text-gray-500">No sessions</p>}
          {sessions.map((s) => (
            <button
              key={s.id}
              onClick={() => setActive(s.id)}
              className={`w-full text-left px-2 py-1.5 rounded text-sm cursor-pointer flex items-center gap-2 ${
                activeId === s.id ? 'bg-gray-800 text-gray-100' : 'text-gray-400 hover:bg-gray-800/50'
              }`}
            >
              <SessionIcon mode={s.mode} id={s.id} />
              <span className="flex-1 truncate">{s.name}</span>
              <span className="text-xs text-gray-500">{s.mode}</span>
            </button>
          ))}
        </div>
      </div>
      {/* Settings button — fixed at bottom */}
      <div className="p-3 border-t border-gray-800">
        <button className="flex items-center gap-2 text-gray-500 hover:text-gray-300 text-sm cursor-pointer w-full">
          <GearSix size={16} />
          <span>Settings</span>
        </button>
      </div>
    </div>
  )
}
```

- [ ] **Step 4: 確認測試通過**

Run: `cd spa && pnpm vitest run src/components/SessionPanel.test.tsx`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add spa/src/components/SessionPanel.tsx spa/src/components/SessionPanel.test.tsx
git commit -m "feat: add Phosphor Icons, status indicators, and settings button to SessionPanel"
```

---

### Task 11: TopBar 元件

**Files:**
- Create: `spa/src/components/TopBar.tsx`
- Create: `spa/src/components/TopBar.test.tsx`

- [ ] **Step 1: 寫測試**

```typescript
// spa/src/components/TopBar.test.tsx
import { describe, it, expect, vi } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import TopBar from './TopBar'

describe('TopBar', () => {
  it('shows session name', () => {
    render(<TopBar sessionName="my-project" mode="term" onModeSwitch={vi.fn()} onInterrupt={vi.fn()} />)
    expect(screen.getByText('my-project')).toBeInTheDocument()
  })

  it('shows current mode', () => {
    render(<TopBar sessionName="test" mode="stream" onModeSwitch={vi.fn()} onInterrupt={vi.fn()} />)
    expect(screen.getByText('stream')).toBeInTheDocument()
  })

  it('calls onModeSwitch when toggled', () => {
    const onSwitch = vi.fn()
    render(<TopBar sessionName="test" mode="term" onModeSwitch={onSwitch} onInterrupt={vi.fn()} />)
    fireEvent.click(screen.getByTestId('mode-switch'))
    expect(onSwitch).toHaveBeenCalled()
  })

  it('shows interrupt button only in stream mode', () => {
    const { rerender } = render(
      <TopBar sessionName="test" mode="term" onModeSwitch={vi.fn()} onInterrupt={vi.fn()} />
    )
    expect(screen.queryByTestId('interrupt-btn')).toBeNull()

    rerender(
      <TopBar sessionName="test" mode="stream" onModeSwitch={vi.fn()} onInterrupt={vi.fn()} />
    )
    expect(screen.getByTestId('interrupt-btn')).toBeInTheDocument()
  })
})
```

- [ ] **Step 2: 確認測試失敗**

Run: `cd spa && pnpm vitest run src/components/TopBar.test.tsx`
Expected: FAIL

- [ ] **Step 3: 實作**

```tsx
// spa/src/components/TopBar.tsx
import { Terminal, Lightning, Stop } from '@phosphor-icons/react'

interface Props {
  sessionName: string
  mode: string
  onModeSwitch: () => void
  onInterrupt: () => void
}

export default function TopBar({ sessionName, mode, onModeSwitch, onInterrupt }: Props) {
  return (
    <div className="h-10 bg-gray-900 border-b border-gray-800 flex items-center px-3 gap-3">
      <span className="text-sm text-gray-200 font-medium truncate">{sessionName}</span>

      <div className="flex-1" />

      {/* Mode switch */}
      <button
        data-testid="mode-switch"
        onClick={onModeSwitch}
        className="flex items-center gap-1.5 px-2 py-1 rounded text-xs cursor-pointer hover:bg-gray-800"
      >
        {mode === 'stream' ? (
          <Lightning size={14} weight="fill" className="text-blue-400" />
        ) : (
          <Terminal size={14} className="text-gray-400" />
        )}
        <span className="text-gray-400">{mode}</span>
      </button>

      {/* Interrupt — stream mode only */}
      {mode === 'stream' && (
        <button
          data-testid="interrupt-btn"
          onClick={onInterrupt}
          className="flex items-center gap-1 px-2 py-1 rounded text-xs cursor-pointer text-red-400 hover:bg-gray-800"
        >
          <Stop size={14} weight="fill" />
          <span>Stop</span>
        </button>
      )}
    </div>
  )
}
```

- [ ] **Step 4: 確認測試通過**

Run: `cd spa && pnpm vitest run src/components/TopBar.test.tsx`
Expected: PASS (4 tests)

- [ ] **Step 5: Commit**

```bash
git add spa/src/components/TopBar.tsx spa/src/components/TopBar.test.tsx
git commit -m "feat: add TopBar with session name, mode switch, and interrupt button"
```

---

## Chunk 3: ConversationView + 互動元件

### Task 12: MessageBubble — 單則訊息渲染

**Files:**
- Create: `spa/src/components/MessageBubble.tsx`
- Create: `spa/src/components/MessageBubble.test.tsx`

- [ ] **Step 1: 寫測試**

```typescript
// spa/src/components/MessageBubble.test.tsx
import { describe, it, expect } from 'vitest'
import { render, screen } from '@testing-library/react'
import MessageBubble from './MessageBubble'

describe('MessageBubble', () => {
  it('renders user message', () => {
    render(<MessageBubble role="user" text="Hello Claude" />)
    expect(screen.getByText('Hello Claude')).toBeInTheDocument()
    expect(screen.getByTestId('bubble-user')).toBeInTheDocument()
  })

  it('renders assistant markdown', () => {
    render(<MessageBubble role="assistant" text="**bold** and `code`" />)
    expect(screen.getByText('bold')).toBeInTheDocument()
  })

  it('renders code block with language', () => {
    render(<MessageBubble role="assistant" text={'```python\nprint("hi")\n```'} />)
    expect(screen.getByText('print("hi")')).toBeInTheDocument()
  })
})
```

- [ ] **Step 2: 確認測試失敗**

- [ ] **Step 3: 實作**

```tsx
// spa/src/components/MessageBubble.tsx
import Markdown from 'react-markdown'
import rehypeHighlight from 'rehype-highlight'
import { User, Robot } from '@phosphor-icons/react'
import 'highlight.js/styles/github-dark.css'

interface Props {
  role: 'user' | 'assistant'
  text: string
}

export default function MessageBubble({ role, text }: Props) {
  const isUser = role === 'user'

  return (
    <div data-testid={`bubble-${role}`} className={`flex gap-3 ${isUser ? 'flex-row-reverse' : ''}`}>
      <div className={`w-7 h-7 rounded-full flex items-center justify-center flex-shrink-0 ${
        isUser ? 'bg-blue-900' : 'bg-green-900'
      }`}>
        {isUser ? <User size={14} className="text-blue-300" /> : <Robot size={14} className="text-green-300" />}
      </div>
      <div className={`max-w-[80%] rounded-lg px-4 py-2 text-sm ${
        isUser ? 'bg-blue-900/30 text-gray-200' : 'bg-gray-800/50 text-gray-200'
      }`}>
        {isUser ? (
          <p className="whitespace-pre-wrap">{text}</p>
        ) : (
          <Markdown
            rehypePlugins={[rehypeHighlight]}
            components={{
              pre: ({ children }) => <pre className="bg-gray-900 rounded p-3 my-2 overflow-x-auto text-xs">{children}</pre>,
              code: ({ className, children, ...props }) => {
                const isInline = !className
                return isInline
                  ? <code className="bg-gray-700 px-1 py-0.5 rounded text-xs" {...props}>{children}</code>
                  : <code className={className} {...props}>{children}</code>
              },
            }}
          >
            {text}
          </Markdown>
        )}
      </div>
    </div>
  )
}
```

- [ ] **Step 4: 確認測試通過**

- [ ] **Step 5: Commit**

```bash
git add spa/src/components/MessageBubble.tsx spa/src/components/MessageBubble.test.tsx
git commit -m "feat: add MessageBubble with markdown rendering and code highlight"
```

---

### Task 13: ToolCallBlock — 工具呼叫摺疊區塊

**Files:**
- Create: `spa/src/components/ToolCallBlock.tsx`
- Create: `spa/src/components/ToolCallBlock.test.tsx`

- [ ] **Step 1: 寫測試**

```typescript
// spa/src/components/ToolCallBlock.test.tsx
import { describe, it, expect } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import ToolCallBlock from './ToolCallBlock'

describe('ToolCallBlock', () => {
  it('shows tool name', () => {
    render(<ToolCallBlock name="Bash" input={{ command: 'ls -la' }} />)
    expect(screen.getByText('Bash')).toBeInTheDocument()
  })

  it('shows command for Bash tool', () => {
    render(<ToolCallBlock name="Bash" input={{ command: 'ls -la' }} />)
    expect(screen.getByText('ls -la')).toBeInTheDocument()
  })

  it('is collapsible', () => {
    render(<ToolCallBlock name="Read" input={{ file_path: '/tmp/test.go' }} result="file content here" />)
    // Result should be hidden initially
    expect(screen.queryByText('file content here')).toBeNull()
    // Click to expand
    fireEvent.click(screen.getByTestId('tool-toggle'))
    expect(screen.getByText('file content here')).toBeInTheDocument()
  })

  it('shows file path for Read/Write/Edit tools', () => {
    render(<ToolCallBlock name="Read" input={{ file_path: '/tmp/test.go' }} />)
    expect(screen.getByText('/tmp/test.go')).toBeInTheDocument()
  })
})
```

- [ ] **Step 2: 確認測試失敗**

- [ ] **Step 3: 實作**

```tsx
// spa/src/components/ToolCallBlock.tsx
import { useState } from 'react'
import { CaretRight, CaretDown, Terminal as TermIcon, File, Pencil, Globe, MagnifyingGlass } from '@phosphor-icons/react'

interface Props {
  name: string
  input: Record<string, unknown>
  result?: string
}

const toolIcons: Record<string, React.ElementType> = {
  Bash: TermIcon,
  Read: File,
  Write: Pencil,
  Edit: Pencil,
  WebFetch: Globe,
  WebSearch: Globe,
  Grep: MagnifyingGlass,
  Glob: MagnifyingGlass,
}

function toolSummary(name: string, input: Record<string, unknown>): string {
  if (name === 'Bash' && input.command) return String(input.command)
  if (['Read', 'Write', 'Edit'].includes(name) && input.file_path) return String(input.file_path)
  if (name === 'Grep' && input.pattern) return String(input.pattern)
  if (name === 'Glob' && input.pattern) return String(input.pattern)
  if (name === 'WebFetch' && input.url) return String(input.url)
  if (name === 'WebSearch' && input.query) return String(input.query)
  return ''
}

export default function ToolCallBlock({ name, input, result }: Props) {
  const [expanded, setExpanded] = useState(false)
  const Icon = toolIcons[name] || TermIcon
  const summary = toolSummary(name, input)

  return (
    <div className="border border-gray-700 rounded my-2 text-xs">
      <button
        data-testid="tool-toggle"
        onClick={() => setExpanded(!expanded)}
        className="flex items-center gap-2 w-full px-3 py-2 hover:bg-gray-800/50 cursor-pointer"
      >
        {expanded ? <CaretDown size={12} /> : <CaretRight size={12} />}
        <Icon size={14} className="text-gray-400" />
        <span className="text-gray-300 font-medium">{name}</span>
        {summary && <span className="text-gray-500 truncate flex-1 text-left">{summary}</span>}
      </button>
      {expanded && (
        <div className="px-3 py-2 border-t border-gray-700 bg-gray-900/50">
          <pre className="text-gray-400 whitespace-pre-wrap break-all">
            {result || JSON.stringify(input, null, 2)}
          </pre>
        </div>
      )}
    </div>
  )
}
```

- [ ] **Step 4: 確認測試通過**

- [ ] **Step 5: Commit**

```bash
git add spa/src/components/ToolCallBlock.tsx spa/src/components/ToolCallBlock.test.tsx
git commit -m "feat: add collapsible ToolCallBlock with tool-specific summaries"
```

---

### Task 14: PermissionPrompt — Allow / Deny

**Files:**
- Create: `spa/src/components/PermissionPrompt.tsx`
- Create: `spa/src/components/PermissionPrompt.test.tsx`

- [ ] **Step 1: 寫測試**

```typescript
// spa/src/components/PermissionPrompt.test.tsx
import { describe, it, expect, vi } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import PermissionPrompt from './PermissionPrompt'

describe('PermissionPrompt', () => {
  const defaultProps = {
    toolName: 'Bash',
    input: { command: 'rm -rf /tmp/test', description: 'Clean up temp files' },
    onAllow: vi.fn(),
    onDeny: vi.fn(),
  }

  it('shows tool name and description', () => {
    render(<PermissionPrompt {...defaultProps} />)
    expect(screen.getByText('Bash')).toBeInTheDocument()
    expect(screen.getByText('rm -rf /tmp/test')).toBeInTheDocument()
  })

  it('calls onAllow when Allow clicked', () => {
    const onAllow = vi.fn()
    render(<PermissionPrompt {...defaultProps} onAllow={onAllow} />)
    fireEvent.click(screen.getByText('Allow'))
    expect(onAllow).toHaveBeenCalled()
  })

  it('calls onDeny when Deny clicked', () => {
    const onDeny = vi.fn()
    render(<PermissionPrompt {...defaultProps} onDeny={onDeny} />)
    fireEvent.click(screen.getByText('Deny'))
    expect(onDeny).toHaveBeenCalled()
  })
})
```

- [ ] **Step 2: 確認測試失敗**

- [ ] **Step 3: 實作**

```tsx
// spa/src/components/PermissionPrompt.tsx
import { ShieldWarning, Check, X } from '@phosphor-icons/react'

interface Props {
  toolName: string
  input: Record<string, unknown>
  onAllow: () => void
  onDeny: () => void
}

export default function PermissionPrompt({ toolName, input, onAllow, onDeny }: Props) {
  const summary = input.command || input.file_path || input.url || input.pattern || ''

  return (
    <div className="border border-yellow-700/50 bg-yellow-900/10 rounded-lg p-4 my-3">
      <div className="flex items-center gap-2 mb-2">
        <ShieldWarning size={18} className="text-yellow-400" />
        <span className="text-yellow-300 text-sm font-medium">Permission Request</span>
      </div>
      <div className="text-sm text-gray-300 mb-1">
        <span className="text-gray-100 font-medium">{toolName}</span>
        {input.description && <span className="text-gray-400"> — {String(input.description)}</span>}
      </div>
      {summary && (
        <pre className="text-xs text-gray-400 bg-gray-900 rounded p-2 my-2 overflow-x-auto">
          {String(summary)}
        </pre>
      )}
      <div className="flex gap-2 mt-3">
        <button
          onClick={onAllow}
          className="flex items-center gap-1 px-3 py-1.5 rounded text-sm bg-green-800 hover:bg-green-700 text-green-100 cursor-pointer"
        >
          <Check size={14} /> Allow
        </button>
        <button
          onClick={onDeny}
          className="flex items-center gap-1 px-3 py-1.5 rounded text-sm bg-red-900 hover:bg-red-800 text-red-200 cursor-pointer"
        >
          <X size={14} /> Deny
        </button>
      </div>
    </div>
  )
}
```

- [ ] **Step 4: 確認測試通過**

- [ ] **Step 5: Commit**

```bash
git add spa/src/components/PermissionPrompt.tsx spa/src/components/PermissionPrompt.test.tsx
git commit -m "feat: add PermissionPrompt with Allow/Deny buttons"
```

---

### Task 15: AskUserQuestion — 選項元件

**Files:**
- Create: `spa/src/components/AskUserQuestion.tsx`
- Create: `spa/src/components/AskUserQuestion.test.tsx`

- [ ] **Step 1: 寫測試**

```typescript
// spa/src/components/AskUserQuestion.test.tsx
import { describe, it, expect, vi } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import AskUserQuestion from './AskUserQuestion'

const questions = [
  {
    question: 'Which format?',
    header: 'Format',
    options: [
      { label: 'Summary', description: 'Brief overview' },
      { label: 'Detailed', description: 'Full explanation' },
    ],
    multiSelect: false,
  },
]

describe('AskUserQuestion', () => {
  it('renders question text', () => {
    render(<AskUserQuestion questions={questions} onSubmit={vi.fn()} onCancel={vi.fn()} />)
    expect(screen.getByText('Which format?')).toBeInTheDocument()
  })

  it('renders options as radio buttons for single select', () => {
    render(<AskUserQuestion questions={questions} onSubmit={vi.fn()} onCancel={vi.fn()} />)
    expect(screen.getByText('Summary')).toBeInTheDocument()
    expect(screen.getByText('Detailed')).toBeInTheDocument()
  })

  it('submits selected answer', () => {
    const onSubmit = vi.fn()
    render(<AskUserQuestion questions={questions} onSubmit={onSubmit} onCancel={vi.fn()} />)
    fireEvent.click(screen.getByText('Summary'))
    fireEvent.click(screen.getByText('Submit'))
    expect(onSubmit).toHaveBeenCalledWith({ 'Which format?': 'Summary' })
  })

  it('calls onCancel', () => {
    const onCancel = vi.fn()
    render(<AskUserQuestion questions={questions} onSubmit={vi.fn()} onCancel={onCancel} />)
    fireEvent.click(screen.getByText('Cancel'))
    expect(onCancel).toHaveBeenCalled()
  })
})
```

- [ ] **Step 2: 確認測試失敗**

- [ ] **Step 3: 實作**

```tsx
// spa/src/components/AskUserQuestion.tsx
import { useState } from 'react'
import { ChatCircleDots, Check, X } from '@phosphor-icons/react'

interface Option {
  label: string
  description: string
}

interface Question {
  question: string
  header: string
  options: Option[]
  multiSelect: boolean
}

interface Props {
  questions: Question[]
  onSubmit: (answers: Record<string, string>) => void
  onCancel: () => void
}

export default function AskUserQuestion({ questions, onSubmit, onCancel }: Props) {
  const [answers, setAnswers] = useState<Record<string, string>>({})

  const handleSelect = (question: string, label: string, multiSelect: boolean) => {
    setAnswers((prev) => {
      if (!multiSelect) return { ...prev, [question]: label }
      // Multi-select: toggle
      const current = prev[question]?.split(', ').filter(Boolean) || []
      const next = current.includes(label)
        ? current.filter((l) => l !== label)
        : [...current, label]
      return { ...prev, [question]: next.join(', ') }
    })
  }

  return (
    <div className="border border-blue-700/50 bg-blue-900/10 rounded-lg p-4 my-3">
      <div className="flex items-center gap-2 mb-3">
        <ChatCircleDots size={18} className="text-blue-400" />
        <span className="text-blue-300 text-sm font-medium">Claude has a question</span>
      </div>
      {questions.map((q) => (
        <div key={q.question} className="mb-3">
          <p className="text-sm text-gray-200 mb-2">{q.question}</p>
          <div className="space-y-1">
            {q.options.map((opt) => {
              const selected = q.multiSelect
                ? answers[q.question]?.split(', ').includes(opt.label)
                : answers[q.question] === opt.label
              return (
                <button
                  key={opt.label}
                  onClick={() => handleSelect(q.question, opt.label, q.multiSelect)}
                  className={`w-full text-left px-3 py-2 rounded text-sm cursor-pointer border ${
                    selected
                      ? 'border-blue-500 bg-blue-900/30 text-blue-200'
                      : 'border-gray-700 hover:border-gray-600 text-gray-300'
                  }`}
                >
                  <span className="font-medium">{opt.label}</span>
                  <span className="text-gray-500 ml-2">{opt.description}</span>
                </button>
              )
            })}
          </div>
        </div>
      ))}
      <div className="flex gap-2 mt-3">
        <button
          onClick={() => onSubmit(answers)}
          className="flex items-center gap-1 px-3 py-1.5 rounded text-sm bg-blue-700 hover:bg-blue-600 text-blue-100 cursor-pointer"
        >
          <Check size={14} /> Submit
        </button>
        <button
          onClick={onCancel}
          className="flex items-center gap-1 px-3 py-1.5 rounded text-sm bg-gray-700 hover:bg-gray-600 text-gray-300 cursor-pointer"
        >
          <X size={14} /> Cancel
        </button>
      </div>
    </div>
  )
}
```

- [ ] **Step 4: 確認測試通過**

- [ ] **Step 5: Commit**

```bash
git add spa/src/components/AskUserQuestion.tsx spa/src/components/AskUserQuestion.test.tsx
git commit -m "feat: add AskUserQuestion with radio/checkbox selection"
```

---

### Task 16: StreamInput — 訊息輸入框

**Files:**
- Create: `spa/src/components/StreamInput.tsx`
- Create: `spa/src/components/StreamInput.test.tsx`

- [ ] **Step 1: 寫測試**

```typescript
// spa/src/components/StreamInput.test.tsx
import { describe, it, expect, vi } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import StreamInput from './StreamInput'

describe('StreamInput', () => {
  it('renders input field', () => {
    render(<StreamInput onSend={vi.fn()} disabled={false} />)
    expect(screen.getByPlaceholderText('Send a message...')).toBeInTheDocument()
  })

  it('calls onSend on submit', () => {
    const onSend = vi.fn()
    render(<StreamInput onSend={onSend} disabled={false} />)
    const input = screen.getByPlaceholderText('Send a message...')
    fireEvent.change(input, { target: { value: 'hello' } })
    fireEvent.submit(input.closest('form')!)
    expect(onSend).toHaveBeenCalledWith('hello')
  })

  it('clears input after send', () => {
    render(<StreamInput onSend={vi.fn()} disabled={false} />)
    const input = screen.getByPlaceholderText('Send a message...') as HTMLInputElement
    fireEvent.change(input, { target: { value: 'hello' } })
    fireEvent.submit(input.closest('form')!)
    expect(input.value).toBe('')
  })

  it('disables input when disabled prop is true', () => {
    render(<StreamInput onSend={vi.fn()} disabled={true} />)
    expect(screen.getByPlaceholderText('Send a message...')).toBeDisabled()
  })
})
```

- [ ] **Step 2: 確認測試失敗**

- [ ] **Step 3: 實作**

```tsx
// spa/src/components/StreamInput.tsx
import { useState } from 'react'
import { PaperPlaneRight } from '@phosphor-icons/react'

interface Props {
  onSend: (text: string) => void
  disabled: boolean
}

export default function StreamInput({ onSend, disabled }: Props) {
  const [text, setText] = useState('')

  const handleSubmit = (e: React.FormEvent) => {
    e.preventDefault()
    const trimmed = text.trim()
    if (!trimmed) return
    onSend(trimmed)
    setText('')
  }

  return (
    <form onSubmit={handleSubmit} className="border-t border-gray-800 p-3 flex gap-2">
      <input
        type="text"
        value={text}
        onChange={(e) => setText(e.target.value)}
        placeholder="Send a message..."
        disabled={disabled}
        className="flex-1 bg-gray-800 text-gray-200 rounded-lg px-4 py-2 text-sm outline-none focus:ring-1 focus:ring-blue-600 disabled:opacity-50 placeholder-gray-500"
      />
      <button
        type="submit"
        disabled={disabled || !text.trim()}
        className="px-3 py-2 bg-blue-700 hover:bg-blue-600 disabled:opacity-30 rounded-lg text-white cursor-pointer disabled:cursor-not-allowed"
      >
        <PaperPlaneRight size={16} />
      </button>
    </form>
  )
}
```

- [ ] **Step 4: 確認測試通過**

- [ ] **Step 5: Commit**

```bash
git add spa/src/components/StreamInput.tsx spa/src/components/StreamInput.test.tsx
git commit -m "feat: add StreamInput message input with send button"
```

---

### Task 17: ConversationView — 對話主畫面

**Files:**
- Create: `spa/src/components/ConversationView.tsx`
- Create: `spa/src/components/ConversationView.test.tsx`

- [ ] **Step 1: 寫測試**

```typescript
// spa/src/components/ConversationView.test.tsx
import { describe, it, expect, vi } from 'vitest'
import { render, screen } from '@testing-library/react'
import ConversationView from './ConversationView'
import { useStreamStore } from '../stores/useStreamStore'

vi.mock('../lib/stream-ws', () => ({
  connectStream: vi.fn().mockReturnValue({
    send: vi.fn(), sendControlResponse: vi.fn(), interrupt: vi.fn(), close: vi.fn(),
  }),
  parseStreamMessage: vi.fn(),
}))

describe('ConversationView', () => {
  it('renders empty state', () => {
    useStreamStore.setState({ messages: [], pendingControlRequests: [], isStreaming: false, sessionId: null, model: null, cost: 0 })
    render(<ConversationView wsUrl="ws://test" sessionName="test" />)
    expect(screen.getByText(/waiting/i)).toBeInTheDocument()
  })

  it('renders messages', () => {
    useStreamStore.setState({
      messages: [
        { type: 'assistant', message: { role: 'assistant', content: [{ type: 'text', text: 'Hello from Claude' }], stop_reason: 'end_turn' } },
      ],
      pendingControlRequests: [],
      isStreaming: false,
      sessionId: null,
      model: null,
      cost: 0,
    })
    render(<ConversationView wsUrl="ws://test" sessionName="test" />)
    expect(screen.getByText('Hello from Claude')).toBeInTheDocument()
  })
})
```

- [ ] **Step 2: 確認測試失敗**

- [ ] **Step 3: 實作**

```tsx
// spa/src/components/ConversationView.tsx
import { useEffect, useRef } from 'react'
import { useStreamStore } from '../stores/useStreamStore'
import { connectStream, type StreamConnection, type StreamMessage, type ControlRequest } from '../lib/stream-ws'
import MessageBubble from './MessageBubble'
import ToolCallBlock from './ToolCallBlock'
import PermissionPrompt from './PermissionPrompt'
import AskUserQuestion from './AskUserQuestion'
import StreamInput from './StreamInput'

interface Props {
  wsUrl: string
  sessionName: string
}

export default function ConversationView({ wsUrl, sessionName }: Props) {
  const connRef = useRef<StreamConnection | null>(null)
  const scrollRef = useRef<HTMLDivElement>(null)
  const { messages, pendingControlRequests, isStreaming, addMessage, addControlRequest, resolveControlRequest, setStreaming, setSessionInfo, addCost, clear } = useStreamStore()

  useEffect(() => {
    clear()

    const conn = connectStream(
      wsUrl,
      (msg) => {
        if (msg.type === 'system' && 'subtype' in msg && msg.subtype === 'init') {
          setSessionInfo(msg.session_id as string, msg.model as string)
          return
        }
        if (msg.type === 'control_request') {
          addControlRequest(msg as ControlRequest)
          return
        }
        if (msg.type === 'result' && 'total_cost_usd' in msg) {
          addCost((msg.total_cost_usd as number) || 0)
          setStreaming(false)
          return
        }
        if (msg.type === 'assistant' || msg.type === 'user') {
          addMessage(msg)
        }
      },
      () => setStreaming(false),
      () => setStreaming(true),
    )
    connRef.current = conn

    return () => {
      conn.close()
      connRef.current = null
    }
  }, [wsUrl])

  // Auto-scroll
  useEffect(() => {
    scrollRef.current?.scrollTo({ top: scrollRef.current.scrollHeight, behavior: 'smooth' })
  }, [messages, pendingControlRequests])

  const handleSend = (text: string) => {
    connRef.current?.send({
      type: 'user',
      message: { role: 'user', content: text },
    })
    // Add user message locally for immediate display
    addMessage({ type: 'user' as const, message: { role: 'user', content: [{ type: 'text', text }], stop_reason: null } } as StreamMessage)
    setStreaming(true)
  }

  const handleAllow = (req: ControlRequest) => {
    connRef.current?.sendControlResponse(req.request_id, {
      behavior: 'allow',
      updatedInput: req.request.input,
    })
    resolveControlRequest(req.request_id)
  }

  const handleDeny = (req: ControlRequest) => {
    connRef.current?.sendControlResponse(req.request_id, {
      behavior: 'deny',
      message: 'User denied',
    })
    resolveControlRequest(req.request_id)
  }

  const handleAskAnswer = (req: ControlRequest, answers: Record<string, string>) => {
    connRef.current?.sendControlResponse(req.request_id, {
      behavior: 'allow',
      updatedInput: {
        questions: (req.request.input as Record<string, unknown>)?.questions,
        answers,
      },
    })
    resolveControlRequest(req.request_id)
  }

  return (
    <div className="flex flex-col h-full">
      {/* Messages area */}
      <div ref={scrollRef} className="flex-1 overflow-y-auto p-4 space-y-4">
        {messages.length === 0 && !isStreaming && (
          <div className="flex items-center justify-center h-full text-gray-500 text-sm">
            Waiting for messages...
          </div>
        )}
        {messages.map((msg, i) => {
          if (msg.type === 'assistant' && 'message' in msg) {
            const content = msg.message.content
            return (
              <div key={i}>
                {content.map((block, j) => {
                  if (block.type === 'text' && block.text) {
                    return <MessageBubble key={j} role="assistant" text={block.text} />
                  }
                  if (block.type === 'tool_use' && block.name) {
                    return <ToolCallBlock key={j} name={block.name} input={block.input || {}} />
                  }
                  return null
                })}
              </div>
            )
          }
          if (msg.type === 'user' && 'message' in msg) {
            const textBlock = msg.message.content.find((b: { type: string }) => b.type === 'text')
            if (textBlock && 'text' in textBlock) {
              return <MessageBubble key={i} role="user" text={textBlock.text as string} />
            }
          }
          return null
        })}

        {/* Pending control requests */}
        {pendingControlRequests.map((req) => {
          if (req.request.tool_name === 'AskUserQuestion') {
            const questions = (req.request.input as Record<string, unknown>)?.questions as Array<{
              question: string; header: string; options: { label: string; description: string }[]; multiSelect: boolean
            }>
            return (
              <AskUserQuestion
                key={req.request_id}
                questions={questions || []}
                onSubmit={(answers) => handleAskAnswer(req, answers)}
                onCancel={() => handleDeny(req)}
              />
            )
          }
          return (
            <PermissionPrompt
              key={req.request_id}
              toolName={req.request.tool_name || 'Unknown'}
              input={req.request.input || {}}
              onAllow={() => handleAllow(req)}
              onDeny={() => handleDeny(req)}
            />
          )
        })}
      </div>

      {/* Input area */}
      <StreamInput onSend={handleSend} disabled={isStreaming} />
    </div>
  )
}
```

- [ ] **Step 4: 確認測試通過**

- [ ] **Step 5: Commit**

```bash
git add spa/src/components/ConversationView.tsx spa/src/components/ConversationView.test.tsx
git commit -m "feat: add ConversationView with messages, tool calls, and permission handling"
```

---

### Task 18: App.tsx 重構 — 整合 TopBar + 模式切換

**Files:**
- Modify: `spa/src/App.tsx`

- [ ] **Step 1: 重寫 App.tsx**

```tsx
// spa/src/App.tsx
import { useEffect, useCallback } from 'react'
import SessionPanel from './components/SessionPanel'
import TerminalView from './components/TerminalView'
import ConversationView from './components/ConversationView'
import TopBar from './components/TopBar'
import { useSessionStore } from './stores/useSessionStore'
import { switchMode } from './lib/api'

// TODO: daemonBase should come from host management (localStorage)
const daemonBase = 'http://100.64.0.2:7860'
const wsBase = daemonBase.replace(/^http/, 'ws')

export default function App() {
  const { sessions, activeId, fetch, setActive } = useSessionStore()
  const active = sessions.find((s) => s.id === activeId)

  useEffect(() => { fetch(daemonBase) }, [fetch])

  const handleModeSwitch = useCallback(async () => {
    if (!active) return
    const newMode = active.mode === 'stream' ? 'term' : 'stream'
    try {
      await switchMode(daemonBase, active.id, newMode)
      await fetch(daemonBase) // refresh sessions
    } catch (e) {
      console.error('mode switch failed:', e)
    }
  }, [active, fetch])

  const handleInterrupt = useCallback(() => {
    // ConversationView exposes interrupt via useStreamStore — not ideal but works for Phase 2
    // A proper solution would use a ref/context, but this is simpler
    const streamWs = (window as unknown as { __streamConn?: { interrupt: () => void } }).__streamConn
    streamWs?.interrupt()
  }, [])

  return (
    <div className="h-screen bg-gray-950 text-gray-200 flex">
      <SessionPanel />
      <div className="flex-1 flex flex-col h-full overflow-hidden">
        {active && (
          <TopBar
            sessionName={active.name}
            mode={active.mode}
            onModeSwitch={handleModeSwitch}
            onInterrupt={handleInterrupt}
          />
        )}
        <div className="flex-1 overflow-hidden">
          {active ? (
            active.mode === 'stream' ? (
              <ConversationView
                wsUrl={`${wsBase}/ws/stream/${encodeURIComponent(active.name)}`}
                sessionName={active.name}
              />
            ) : (
              <TerminalView
                wsUrl={`${wsBase}/ws/terminal/${encodeURIComponent(active.name)}`}
              />
            )
          ) : (
            <div className="flex items-center justify-center h-full">
              <p className="text-gray-400">Select a session</p>
            </div>
          )}
        </div>
      </div>
    </div>
  )
}
```

- [ ] **Step 2: 確認所有前端測試通過**

Run: `cd spa && pnpm vitest run`
Expected: PASS (all tests)

- [ ] **Step 3: Commit**

```bash
git add spa/src/App.tsx
git commit -m "feat: integrate TopBar, mode switching, and ConversationView into App"
```

---

## 驗收標準

Phase 2 完成後：

1. `go test -race ./...` — 全過
2. `cd spa && pnpm vitest run` — 全過
3. `make build` → `bin/tbox` 成功
4. Daemon 啟動 → SPA 連線 → Session 列表顯示（含 Phosphor Icons 狀態燈號）
5. 建立 stream session（POST API 或 mode switch）→ claude -p 啟動
6. ConversationView 渲染 CC 回應（markdown、程式碼高亮）
7. ToolCallBlock 顯示工具呼叫（可摺疊）
8. PermissionPrompt 顯示 Allow/Deny → 回傳 control_response
9. AskUserQuestion 顯示選項 → 回傳答案
10. StreamInput 可送訊息 → CC 回應
11. TopBar 可切換 term ↔ stream 模式
12. 切換後 SessionPanel 圖示更新
13. 左側底部有 Settings 按鈕（入口，無功能）
14. Interrupt 按鈕（stream 模式）
