# tmux 多 Client 視窗尺寸修正 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 修復 `resize-window` 將 `window-size` 鎖為 `manual` 的 bug，並新增可選的 session group 配置隔離多 client 尺寸。

**Architecture:** Part 1 在 Executor 介面新增 `SetWindowOption`，引入 `restoreWindowSizing` helper 統一恢復 `window-size latest`。Part 2 在 config 新增 `session_group` 選項，`handleTerminal` 根據配置建立 grouped session 或直接 attach。

**Tech Stack:** Go / tmux CLI / vitest（前端不涉及本次改動）

**Spec:** `docs/superpowers/specs/2026-03-19-tmux-multi-client-sizing-design.md`

---

### Task 1: Executor 介面新增 `SetWindowOption`

**Files:**
- Modify: `internal/tmux/executor.go:19-33` (Executor interface)
- Modify: `internal/tmux/executor.go:164-166` (RealExecutor, 在 ResizeWindowAuto 之後新增)
- Modify: `internal/tmux/executor.go:180-318` (FakeExecutor)

- [ ] **Step 1: 在 Executor 介面新增 `SetWindowOption`**

在 `executor.go:32`（`ResizeWindowAuto` 之後）加入：

```go
SetWindowOption(target, option, value string) error
```

- [ ] **Step 2: 實作 RealExecutor.SetWindowOption**

在 `ResizeWindowAuto` 方法之後新增：

```go
func (r *RealExecutor) SetWindowOption(target, option, value string) error {
	return exec.Command("tmux", "set-window-option", "-t", target, option, value).Run()
}
```

- [ ] **Step 3: 實作 FakeExecutor.SetWindowOption**

在 FakeExecutor struct 新增欄位和方法：

```go
// struct 新增欄位
setWindowOptionCalls []struct{ Target, Option, Value string }

// 方法
func (f *FakeExecutor) SetWindowOption(target, option, value string) error {
	f.setWindowOptionCalls = append(f.setWindowOptionCalls, struct{ Target, Option, Value string }{target, option, value})
	return nil
}

func (f *FakeExecutor) SetWindowOptionCalls() []struct{ Target, Option, Value string } {
	return f.setWindowOptionCalls
}
```

- [ ] **Step 4: 確認編譯通過**

Run: `cd /Users/wake/Workspace/wake/tmux-box && go build ./...`
Expected: 無錯誤

- [ ] **Step 5: Commit**

```bash
git add internal/tmux/executor.go
git commit -m "feat: add SetWindowOption to Executor interface"
```

---

### Task 2: 新增 `restoreWindowSizing` helper 並修復 server.go

**Files:**
- Modify: `internal/server/server.go:81-102` (handleTerminal)

- [ ] **Step 1: 寫測試 — RestoreWindowSizing 呼叫 ResizeWindowAuto + SetWindowOption**

在 `internal/server/server_test.go`（如不存在則建立）新增：

```go
func TestRestoreWindowSizingCallsBothMethods(t *testing.T) {
	fakeTmux := tmux.NewFakeExecutor()

	db, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	srv := server.New(config.Config{}, db, fakeTmux, "")
	srv.RestoreWindowSizing("test:0")

	if len(fakeTmux.AutoResizeCalls()) != 1 || fakeTmux.AutoResizeCalls()[0] != "test:0" {
		t.Errorf("expected ResizeWindowAuto called with test:0, got %v", fakeTmux.AutoResizeCalls())
	}
	calls := fakeTmux.SetWindowOptionCalls()
	if len(calls) != 1 || calls[0].Target != "test:0" || calls[0].Option != "window-size" || calls[0].Value != "latest" {
		t.Errorf("expected SetWindowOption(test:0, window-size, latest), got %v", calls)
	}
}
```

注意：`RestoreWindowSizing` 匯出以便從 `server_test` package 呼叫（spec 用小寫 `restoreWindowSizing`，此為刻意偏離）。

- [ ] **Step 2: 執行測試確認失敗**

Run: `go test ./internal/server/ -run TestRestoreWindowSizing -v`
Expected: FAIL — `RestoreWindowSizing` 未定義

- [ ] **Step 3: 實作 `restoreWindowSizing` 並匯出為 `RestoreWindowSizing`**

在 `server.go` 新增（`handleTerminal` 之前）：

```go
// RestoreWindowSizing clears manual window-size set by resize-window
// and restores automatic sizing based on the latest client.
func (s *Server) RestoreWindowSizing(target string) {
	s.tmux.ResizeWindowAuto(target)
	s.tmux.SetWindowOption(target, "window-size", "latest")
}
```

- [ ] **Step 4: 修改 handleTerminal 使用 RestoreWindowSizing**

將 `server.go` 的 `s.tmux.ResizeWindowAuto(name)` 改為 `s.RestoreWindowSizing(name)`：

```go
relay.OnStart = func() {
	go func() {
		time.Sleep(1200 * time.Millisecond)
		s.RestoreWindowSizing(name)
	}()
}
```

- [ ] **Step 5: 執行測試確認通過**

Run: `go test ./internal/server/ -run TestRestoreWindowSizing -v`
Expected: PASS

- [ ] **Step 6: 執行全部測試確認無 regression**

Run: `go test ./...`
Expected: 全部 PASS

- [ ] **Step 7: Commit**

```bash
git add internal/server/server.go internal/server/server_test.go
git commit -m "fix: restore window-size to latest after resize-window -A"
```

---

### Task 3: 修復 handoff_handler.go 的所有 ResizeWindowAuto 呼叫

**Files:**
- Modify: `internal/server/handoff_handler.go:242-280`

- [ ] **Step 1: 在現有 `TestHandoffResizesPaneTooSmall` 加入 SetWindowOption 斷言**

`TestHandoffResizesPaneTooSmall` 已驗證 `ResizeWindowAuto` 被呼叫（後置清理路徑 L278）。在該測試尾部加入 `SetWindowOption` 斷言：

```go
// 在 TestHandoffResizesPaneTooSmall 尾部加入（autoCalls 斷言之後）：
swoCalls := fakeTmux.SetWindowOptionCalls()
swoFound := false
for _, c := range swoCalls {
	if c.Target == "small-session:0" && c.Option == "window-size" && c.Value == "latest" {
		swoFound = true
		break
	}
}
if !swoFound {
	t.Error("expected SetWindowOption(small-session:0, window-size, latest) after handoff cleanup")
}
```

此測試涵蓋後置清理路徑。Error paths 使用相同的 `RestoreWindowSizing` helper，由 Task 2 的測試保證正確性。

- [ ] **Step 2: 修改 handoff_handler.go — 替換所有 ResizeWindowAuto**

將以下 4 處的 `s.tmux.ResizeWindowAuto(target)` 改為 `s.RestoreWindowSizing(target)`：

L243:
```go
if didManualResize {
	s.RestoreWindowSizing(target)
}
```

L251:
```go
if didManualResize {
	s.RestoreWindowSizing(target)
}
```

L259:
```go
if didManualResize {
	s.RestoreWindowSizing(target)
}
```

L278-279:
```go
if didManualResize {
	s.RestoreWindowSizing(target)
}
```

- [ ] **Step 3: 執行測試確認通過**

Run: `go test ./internal/server/ -v`
Expected: 全部 PASS（含新斷言）

- [ ] **Step 4: Commit**

```bash
git add internal/server/handoff_handler.go internal/server/handoff_handler_test.go
git commit -m "fix: restore window-size in handoff cleanup and error paths"
```

---

### Task 4: config 新增 `session_group` 選項

**Files:**
- Modify: `internal/config/config.go:29-35`

- [ ] **Step 1: 寫測試 — IsSessionGroup 預設 false、設為 true 時返回 true**

在 `internal/config/config_test.go`（如不存在則建立）新增：

```go
func TestIsSessionGroupDefaultFalse(t *testing.T) {
	tc := config.TerminalConfig{}
	if tc.IsSessionGroup() {
		t.Error("expected IsSessionGroup() to be false by default")
	}
}

func TestIsSessionGroupTrue(t *testing.T) {
	v := true
	tc := config.TerminalConfig{SessionGroup: &v}
	if !tc.IsSessionGroup() {
		t.Error("expected IsSessionGroup() to be true when set")
	}
}
```

- [ ] **Step 2: 執行測試確認失敗**

Run: `go test ./internal/config/ -run TestIsSessionGroup -v`
Expected: FAIL — `SessionGroup` 欄位不存在

- [ ] **Step 3: 實作 — 新增 SessionGroup 欄位和方法**

```go
type TerminalConfig struct {
	AutoResize   *bool `toml:"auto_resize"   json:"auto_resize"`
	SessionGroup *bool `toml:"session_group"  json:"session_group"`
}

func (tc TerminalConfig) IsSessionGroup() bool {
	return tc.SessionGroup != nil && *tc.SessionGroup
}
```

- [ ] **Step 4: 執行測試確認通過**

Run: `go test ./internal/config/ -run TestIsSessionGroup -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/config/config.go internal/config/config_test.go
git commit -m "feat: add session_group terminal config option"
```

---

### Task 5: Executor 新增 `NewGroupedSession` 和 `ListSessionNames`

**Files:**
- Modify: `internal/tmux/executor.go`

- [ ] **Step 1: 在 Executor 介面新增方法**

```go
// NewGroupedSession creates a detached session linked to an existing session (session group).
NewGroupedSession(baseSession, newSession string) error
// ListSessionNames returns all tmux session names.
ListSessionNames() ([]string, error)
```

- [ ] **Step 2: 實作 RealExecutor**

```go
func (r *RealExecutor) NewGroupedSession(baseSession, newSession string) error {
	return exec.Command("tmux", "new-session", "-d", "-t", baseSession, "-s", newSession).Run()
}

func (r *RealExecutor) ListSessionNames() ([]string, error) {
	out, err := exec.Command("tmux", "list-sessions", "-F", "#{session_name}").Output()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			if strings.Contains(string(exitErr.Stderr), "no server running") ||
				strings.Contains(string(exitErr.Stderr), "no sessions") {
				return nil, nil
			}
		}
		return nil, fmt.Errorf("tmux list-sessions: %w", err)
	}
	var names []string
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line != "" {
			names = append(names, line)
		}
	}
	return names, nil
}
```

- [ ] **Step 3: 實作 FakeExecutor**

```go
func (f *FakeExecutor) NewGroupedSession(baseSession, newSession string) error {
	base, ok := f.sessions[baseSession]
	if !ok {
		return fmt.Errorf("session not found: %s", baseSession)
	}
	f.sessions[newSession] = TmuxSession{Name: newSession, Cwd: base.Cwd}
	return nil
}

func (f *FakeExecutor) ListSessionNames() ([]string, error) {
	names := make([]string, 0, len(f.sessions))
	for name := range f.sessions {
		names = append(names, name)
	}
	return names, nil
}
```

- [ ] **Step 4: 確認編譯通過**

Run: `go build ./...`
Expected: 無錯誤

- [ ] **Step 5: Commit**

```bash
git add internal/tmux/executor.go
git commit -m "feat: add NewGroupedSession and ListSessionNames to Executor"
```

---

### Task 6: handleTerminal 支援 session group 模式

**Files:**
- Modify: `internal/server/server.go:81-102`

- [ ] **Step 1: 寫測試 — session_group=true 時使用 grouped session**

在 `internal/server/server_test.go` 新增：

```go
func TestHandleTerminalSessionGroup(t *testing.T) {
	fakeTmux := tmux.NewFakeExecutor()
	fakeTmux.AddSession("myapp", "/tmp")

	db, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	sgTrue := true
	cfg := config.Config{
		Terminal: config.TerminalConfig{SessionGroup: &sgTrue},
	}
	srv := server.New(cfg, db, fakeTmux, "")

	// 驗證 grouped session 建立
	// 由於 handleTerminal 需要 WebSocket，這裡改為測試 helper
	// 測試 BuildRelayArgs 返回正確的 grouped session 命令
}
```

注意：`handleTerminal` 內部啟動 relay 並阻塞在 WebSocket，不適合直接單元測試。更好的方式是將 session group 邏輯抽為可測試的函數。

**替代方案**：將 relay 參數構建邏輯抽成 `buildTerminalRelay` 方法，回傳 `(cmd string, args []string, cleanup func())`，獨立測試。

```go
func TestBuildTerminalRelayWithSessionGroup(t *testing.T) {
	fakeTmux := tmux.NewFakeExecutor()
	fakeTmux.AddSession("myapp", "/tmp")

	db, _ := store.Open(filepath.Join(t.TempDir(), "test.db"))
	defer db.Close()

	sgTrue := true
	cfg := config.Config{
		Terminal: config.TerminalConfig{SessionGroup: &sgTrue},
	}
	srv := server.New(cfg, db, fakeTmux, "")

	cmd, args, cleanup, err := srv.BuildTerminalRelay("myapp")
	if err != nil {
		t.Fatal(err)
	}

	if cmd != "tmux" {
		t.Errorf("expected cmd=tmux, got %s", cmd)
	}
	if args[0] != "attach-session" {
		t.Errorf("expected args[0]=attach-session, got %s", args[0])
	}
	// args[2] should be the grouped session name matching pattern
	relaySession := args[2]
	matched, _ := regexp.MatchString(`^myapp-tbox-[0-9a-f]{8}$`, relaySession)
	if !matched {
		t.Errorf("expected relay session matching myapp-tbox-{hex8}, got %s", relaySession)
	}
	// grouped session should exist in tmux
	if !fakeTmux.HasSession(relaySession) {
		t.Error("expected grouped session to be created")
	}
	// cleanup should kill it
	cleanup()
	if fakeTmux.HasSession(relaySession) {
		t.Error("expected grouped session to be killed after cleanup")
	}
}

func TestBuildTerminalRelayWithoutSessionGroup(t *testing.T) {
	fakeTmux := tmux.NewFakeExecutor()
	fakeTmux.AddSession("myapp", "/tmp")

	db, _ := store.Open(filepath.Join(t.TempDir(), "test.db"))
	defer db.Close()

	srv := server.New(config.Config{}, db, fakeTmux, "")

	cmd, args, cleanup, err := srv.BuildTerminalRelay("myapp")
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()

	if cmd != "tmux" || args[1] != "myapp" {
		t.Errorf("expected attach-session -t myapp, got %s %v", cmd, args)
	}
}
```

- [ ] **Step 2: 執行測試確認失敗**

Run: `go test ./internal/server/ -run TestBuildTerminalRelay -v`
Expected: FAIL — `BuildTerminalRelay` 未定義

- [ ] **Step 3: 實作 `BuildTerminalRelay`**

在 `server.go` 新增：

```go
// BuildTerminalRelay returns the command, args, and cleanup function for a terminal relay.
// When session_group is enabled, it creates a grouped session for size isolation.
func (s *Server) BuildTerminalRelay(name string) (cmd string, args []string, cleanup func(), err error) {
	if !s.cfg.Terminal.IsSessionGroup() {
		return "tmux", []string{"attach-session", "-t", name}, func() {}, nil
	}

	b := make([]byte, 4)
	if _, err := rand.Read(b); err != nil {
		return "", nil, nil, fmt.Errorf("generate relay ID: %w", err)
	}
	relaySession := fmt.Sprintf("%s-tbox-%x", name, b)

	// Retry up to 3 times on name collision
	for attempt := 0; attempt < 3; attempt++ {
		err = s.tmux.NewGroupedSession(name, relaySession)
		if err == nil {
			break
		}
		b = make([]byte, 4)
		rand.Read(b)
		relaySession = fmt.Sprintf("%s-tbox-%x", name, b)
	}
	if err != nil {
		return "", nil, nil, fmt.Errorf("create grouped session: %w", err)
	}

	cleanup = func() {
		s.tmux.KillSession(relaySession)
	}

	return "tmux", []string{"attach-session", "-t", relaySession}, cleanup, nil
}
```

需要在 `server.go` 頂部加入 `"crypto/rand"` import。

- [ ] **Step 4: 修改 handleTerminal 使用 BuildTerminalRelay**

```go
func (s *Server) handleTerminal(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("session")
	if !s.tmux.HasSession(name) {
		http.Error(w, "session not found", 404)
		return
	}

	cmd, args, cleanup, err := s.BuildTerminalRelay(name)
	if err != nil {
		http.Error(w, "relay setup failed: "+err.Error(), 500)
		return
	}
	defer cleanup()

	relay := terminal.NewRelay(cmd, args, "/")
	if s.cfg.Terminal.IsAutoResize() && !s.cfg.Terminal.IsSessionGroup() {
		relay.OnStart = func() {
			go func() {
				time.Sleep(1200 * time.Millisecond)
				s.RestoreWindowSizing(name)
			}()
		}
	}
	relay.HandleWebSocket(w, r)
}
```

注意：`session_group=true` 時不執行 `auto_resize`，因為 grouped session 只有一個 client，`window-size latest` 自動生效（spec 決策）。

- [ ] **Step 5: 執行測試確認通過**

Run: `go test ./internal/server/ -run TestBuildTerminalRelay -v`
Expected: PASS

- [ ] **Step 6: 執行全部測試確認無 regression**

Run: `go test ./...`
Expected: 全部 PASS

- [ ] **Step 7: Commit**

```bash
git add internal/server/server.go internal/server/server_test.go
git commit -m "feat: handleTerminal supports session group mode"
```

---

### Task 7: 新增 `cleanupStaleRelays`

**Files:**
- Modify: `internal/server/server.go`

- [ ] **Step 1: 寫測試**

```go
func TestCleanupStaleRelays(t *testing.T) {
	fakeTmux := tmux.NewFakeExecutor()
	fakeTmux.AddSession("myapp", "/tmp")
	fakeTmux.AddSession("myapp-tbox-1a2b3c4d", "/tmp")  // stale relay
	fakeTmux.AddSession("myapp-tbox-deadbeef", "/tmp")   // stale relay
	fakeTmux.AddSession("work", "/tmp")                   // normal session
	fakeTmux.AddSession("my-tbox-project", "/tmp")        // NOT a relay (no hex suffix)

	db, _ := store.Open(filepath.Join(t.TempDir(), "test.db"))
	defer db.Close()

	srv := server.New(config.Config{}, db, fakeTmux, "")
	srv.CleanupStaleRelays()

	if fakeTmux.HasSession("myapp-tbox-1a2b3c4d") {
		t.Error("expected stale relay myapp-tbox-1a2b3c4d to be cleaned up")
	}
	if fakeTmux.HasSession("myapp-tbox-deadbeef") {
		t.Error("expected stale relay myapp-tbox-deadbeef to be cleaned up")
	}
	if !fakeTmux.HasSession("myapp") {
		t.Error("expected normal session myapp to survive cleanup")
	}
	if !fakeTmux.HasSession("work") {
		t.Error("expected normal session work to survive cleanup")
	}
	if !fakeTmux.HasSession("my-tbox-project") {
		t.Error("expected non-relay session my-tbox-project to survive cleanup")
	}
}
```

- [ ] **Step 2: 執行測試確認失敗**

Run: `go test ./internal/server/ -run TestCleanupStaleRelays -v`
Expected: FAIL

- [ ] **Step 3: 實作 `CleanupStaleRelays`**

```go
// CleanupStaleRelays removes tmux sessions created by session group mode
// that were not cleaned up (e.g., daemon crashed). Matches pattern: {name}-tbox-{8 hex chars}.
func (s *Server) CleanupStaleRelays() {
	names, err := s.tmux.ListSessionNames()
	if err != nil {
		return
	}
	re := regexp.MustCompile(`^.+-tbox-[0-9a-f]{8}$`)
	for _, name := range names {
		if re.MatchString(name) {
			s.tmux.KillSession(name)
		}
	}
}
```

需要加入 `"regexp"` import。

- [ ] **Step 4: 在 `New()` 中呼叫 `CleanupStaleRelays`**

在 `server.go` 的 `New()` 函數中，`s.resetStaleModes()` 之後加入：

```go
s.CleanupStaleRelays()
```

- [ ] **Step 5: 執行測試確認通過**

Run: `go test ./internal/server/ -run TestCleanupStaleRelays -v`
Expected: PASS

- [ ] **Step 6: 執行全部測試**

Run: `go test ./...`
Expected: 全部 PASS

- [ ] **Step 7: Commit**

```bash
git add internal/server/server.go internal/server/server_test.go
git commit -m "feat: cleanup stale relay sessions on daemon startup"
```

---

### Task 8: 全部測試 + 手動驗證

**Files:** 無新增

- [ ] **Step 1: 執行全部測試**

Run: `go test ./... -v`
Expected: 全部 PASS

- [ ] **Step 2: 手動驗證 Part 1 — resize-window 副作用修復**

```bash
# 重新編譯 daemon
go build -o bin/tbox ./cmd/tbox

# 啟動 daemon
bin/tbox serve

# 開啟瀏覽器連到某 session 的 terminal tab
# 確認 tmux show-window-options -t {session} window-size 為 latest（不是 manual）
tmux show-window-options -t "tmux box" window-size
# Expected: window-size latest
```

- [ ] **Step 3: 手動驗證 Part 2 — session group**

```bash
# 修改 config.toml
# [terminal]
# session_group = true

# 重啟 daemon，開啟瀏覽器
# 確認 tmux list-sessions 出現 {name}-tbox-{hex} 的 grouped session
tmux list-sessions

# 開第二個瀏覽器，確認兩個瀏覽器尺寸獨立
# 調整其中一個瀏覽器的視窗大小，另一個不受影響
# iTerm 也不受影響

# 關閉瀏覽器，確認 grouped session 被清理
tmux list-sessions
```

- [ ] **Step 4: Commit**（如有修正）

---

### Task 9: 建立 PR

- [ ] **Step 1: 建立 feature branch 並推送**

```bash
git checkout -b feat/multi-client-sizing
git push -u origin feat/multi-client-sizing
```

- [ ] **Step 2: 建立 PR**

```bash
gh pr create --title "fix: multi-client window sizing" --body "..."
```
