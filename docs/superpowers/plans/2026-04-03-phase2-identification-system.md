# Phase 2: 識別系統 — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Daemon 產生穩定的 Host ID 並透過 API 傳遞給 SPA；加入 tmux Instance ID 以偵測 server 重啟；PaneContent 擴充 cachedName 和 tmuxInstance 使 tab 在斷線後仍可顯示名稱。

**Architecture:** Daemon 啟動時產生 `hostname:6-char-code` 格式的 Host ID 並持久化到 config.toml。`/api/info` 回傳 host_id + tmux_instance。SPA 的 AddHostDialog 從 daemon 取得 host ID 作為 store key（取代 SPA 端 generateId）。PaneContent session kind 新增 `tmuxInstance` 和 `cachedName`，使 tab 在 session 消失或 host 斷線後仍能顯示有意義的名稱。

**Tech Stack:** Go (daemon) / React + Zustand (SPA) / Vitest

**Scope:** 拆為 2 個 PR：
- **PR A（Daemon）**：Tasks 1-4 — host ID 產生 + `/api/info` 擴充 + tmux instance
- **PR B（SPA）**：Tasks 5-10 — HostStore 改用 daemon ID + PaneContent 擴充

---

## 檔案結構

```
── Daemon（PR A）──
internal/config/config.go          — Config struct 加 HostID 欄位
internal/config/hostid.go          — NEW: EnsureHostID() 產生 + 持久化
internal/config/hostid_test.go     — NEW: 測試
internal/core/info_handler.go      — /api/info 回傳 host_id + tmux_instance
internal/core/info_handler_test.go — 測試更新
internal/tmux/executor.go          — Executor 加 ServerPID() 方法
internal/tmux/fake_executor.go     — FakeExecutor 加 ServerPID()
cmd/tbox/main.go                   — 啟動時呼叫 EnsureHostID

── SPA（PR B）──
spa/src/types/tab.ts               — PaneContent session 加 tmuxInstance + cachedName
spa/src/lib/api.ts                 — Session type 不變（tmux_instance 在 /api/info，不在 session）
spa/src/stores/useHostStore.ts     — HostInfo 加 host_id + tmux_instance；addHost 改接受 id 參數
spa/src/components/hosts/AddHostDialog.tsx — 連線後 fetch /api/info 取 host_id
spa/src/lib/pane-labels.ts         — session label fallback 用 cachedName
spa/src/hooks/useMultiHostEventWs.ts — session 更新時同步 cachedName 到 tab
spa/src/components/hosts/SessionsSection.tsx — 建 tab 時設 cachedName + tmuxInstance
spa/src/components/SessionPanel.tsx — 建 tab 時設 cachedName + tmuxInstance
```

---

## PR A — Daemon

### Task 1: Host ID 產生與持久化

**Files:**
- Modify: `internal/config/config.go:46-58` — Config struct
- Create: `internal/config/hostid.go`
- Create: `internal/config/hostid_test.go`

- [ ] **Step 1: Config struct 加 HostID 欄位**

在 `internal/config/config.go` 的 Config struct 加入：

```go
type Config struct {
    HostID       string         `toml:"host_id"        json:"host_id"`
    Bind         string         `toml:"bind"           json:"bind"`
    // ... 其餘不動
}
```

放在第一個欄位（重要性高）。`defaults()` 不設預設值（空字串 = 尚未產生）。

- [ ] **Step 2: 寫 EnsureHostID 測試**

```go
// internal/config/hostid_test.go
package config_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BurntSushi/toml"
	"github.com/wake/tmux-box/internal/config"
)

func TestEnsureHostID_GeneratesWhenEmpty(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	cfg := config.Config{DataDir: dir}

	id, err := config.EnsureHostID(&cfg, path)
	if err != nil {
		t.Fatal(err)
	}

	// Format: hostname:6-char-code
	parts := strings.SplitN(id, ":", 2)
	if len(parts) != 2 {
		t.Fatalf("expected hostname:code format, got %q", id)
	}
	if len(parts[1]) != 6 {
		t.Fatalf("expected 6-char code, got %q (len %d)", parts[1], len(parts[1]))
	}
	if cfg.HostID != id {
		t.Fatalf("cfg.HostID not updated: want %q, got %q", id, cfg.HostID)
	}

	// Verify persisted to file
	var saved config.Config
	data, _ := os.ReadFile(path)
	toml.Unmarshal(data, &saved)
	if saved.HostID != id {
		t.Fatalf("persisted HostID: want %q, got %q", id, saved.HostID)
	}
}

func TestEnsureHostID_PreservesExisting(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	cfg := config.Config{HostID: "mlab:abc123", DataDir: dir}

	id, err := config.EnsureHostID(&cfg, path)
	if err != nil {
		t.Fatal(err)
	}
	if id != "mlab:abc123" {
		t.Fatalf("want preserved mlab:abc123, got %q", id)
	}

	// File should NOT be written (no change)
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("file should not be created when HostID already set")
	}
}

func TestEnsureHostID_AppendsToExistingConfig(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	os.WriteFile(path, []byte("bind = \"0.0.0.0\"\nport = 9090\n"), 0644)

	cfg, _ := config.Load(path)
	_, err := config.EnsureHostID(&cfg, path)
	if err != nil {
		t.Fatal(err)
	}

	// Verify existing config preserved
	var saved config.Config
	data, _ := os.ReadFile(path)
	toml.Unmarshal(data, &saved)
	if saved.Bind != "0.0.0.0" {
		t.Fatalf("bind lost: want 0.0.0.0, got %q", saved.Bind)
	}
	if saved.Port != 9090 {
		t.Fatalf("port lost: want 9090, got %d", saved.Port)
	}
	if saved.HostID == "" {
		t.Fatal("HostID should be set")
	}
}
```

- [ ] **Step 3: 執行測試確認失敗**

Run: `cd /Users/wake/Workspace/wake/tmux-box && go test ./internal/config/ -run TestEnsureHostID -v`
Expected: FAIL — `EnsureHostID` not defined

- [ ] **Step 4: 實作 EnsureHostID**

```go
// internal/config/hostid.go
package config

import (
	"crypto/rand"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/BurntSushi/toml"
)

// EnsureHostID generates a stable host ID if not already set, persists it to the
// config file, and updates cfg.HostID in place. Format: "hostname:6-char-code".
// If cfg.HostID is already set, returns it unchanged without writing.
func EnsureHostID(cfg *Config, cfgPath string) (string, error) {
	if cfg.HostID != "" {
		return cfg.HostID, nil
	}

	hostname := shortHostname()
	code := randomCode(6)
	cfg.HostID = hostname + ":" + code

	if err := writeConfigFile(cfgPath, *cfg); err != nil {
		return cfg.HostID, fmt.Errorf("persist host_id: %w", err)
	}
	return cfg.HostID, nil
}

// shortHostname returns the hostname without domain suffix.
func shortHostname() string {
	name, err := os.Hostname()
	if err != nil {
		return "unknown"
	}
	if i := strings.IndexByte(name, '.'); i > 0 {
		name = name[:i]
	}
	return strings.ToLower(name)
}

// randomCode generates a random alphanumeric string of the given length.
func randomCode(n int) string {
	const chars = "0123456789abcdefghijklmnopqrstuvwxyz"
	buf := make([]byte, n)
	rand.Read(buf)
	for i := range buf {
		buf[i] = chars[buf[i]%36]
	}
	return string(buf)
}

// writeConfigFile serialises the config to TOML and writes it atomically.
func writeConfigFile(path string, cfg Config) error {
	if err := os.MkdirAll(strings.TrimSuffix(path, "/config.toml"), 0755); err != nil {
		// best-effort — parent dir might already exist
	}
	tmp := path + ".tmp"
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	if err := toml.NewEncoder(f).Encode(cfg); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, path)
}

// GetTmuxInstance returns the tmux server's "pid:startTime" identifier.
// Returns empty string if tmux is not running.
func GetTmuxInstance() string {
	out, err := exec.Command("tmux", "display-message", "-p", "#{pid}:#{start_time}").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}
```

- [ ] **Step 5: 執行測試確認通過**

Run: `cd /Users/wake/Workspace/wake/tmux-box && go test ./internal/config/ -run TestEnsureHostID -v`
Expected: All 3 tests PASS

- [ ] **Step 6: Commit**

```bash
git add internal/config/config.go internal/config/hostid.go internal/config/hostid_test.go
git commit -m "feat(daemon): add host ID generation + persistence to config"
```

---

### Task 2: 啟動時初始化 Host ID

**Files:**
- Modify: `cmd/tbox/main.go:60-71`

- [ ] **Step 1: 在 config 載入後呼叫 EnsureHostID**

在 `runServe()` 的 config 載入後（line 63 後）加入：

```go
	// 1b. Ensure stable host ID
	resolvedCfgPath := *cfgPath
	if resolvedCfgPath == "" {
		resolvedCfgPath = filepath.Join(cfg.DataDir, "config.toml")
	}
	hostID, err := config.EnsureHostID(&cfg, resolvedCfgPath)
	if err != nil {
		log.Printf("host_id: %v (continuing with generated ID)", err)
	}
	log.Printf("host_id: %s", hostID)
```

同時移除後面 lines 99-104 的 `resolvedCfgPath` 重複宣告（已提前到上方）。

- [ ] **Step 2: 執行 daemon build 確認編譯通過**

Run: `cd /Users/wake/Workspace/wake/tmux-box && go build ./cmd/tbox/`
Expected: Build succeeds

- [ ] **Step 3: Commit**

```bash
git add cmd/tbox/main.go
git commit -m "feat(daemon): initialize host ID on startup"
```

---

### Task 3: `/api/info` 回傳 host_id + tmux_instance

**Files:**
- Modify: `internal/core/info_handler.go:25-34`
- Modify: `internal/core/info_handler_test.go`

- [ ] **Step 1: 更新 info_handler_test.go 加入新欄位斷言**

在 `TestInfoEndpoint` 加入：

```go
	// Phase 2: must contain host_id and tmux_instance
	assert.Contains(t, body, "host_id")
	assert.Contains(t, body, "tmux_instance")
```

- [ ] **Step 2: 執行測試確認失敗**

Run: `go test ./internal/core/ -run TestInfoEndpoint -v`
Expected: FAIL — body 缺少 `host_id` 和 `tmux_instance`

- [ ] **Step 3: 更新 handleInfo 回傳新欄位**

```go
func (c *Core) handleInfo(w http.ResponseWriter, r *http.Request) {
	c.CfgMu.RLock()
	hostID := c.Cfg.HostID
	c.CfgMu.RUnlock()

	info := map[string]string{
		"host_id":       hostID,
		"tmux_instance": config.GetTmuxInstance(),
		"tbox_version":  Version,
		"tmux_version":  getTmuxVersion(),
		"os":            runtime.GOOS,
		"arch":          runtime.GOARCH,
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(info)
}
```

加入 `"github.com/wake/tmux-box/internal/config"` import。

- [ ] **Step 4: 更新 TestInfoEndpoint 建立 Core 時設 HostID**

```go
func TestInfoEndpoint(t *testing.T) {
	fakeTmux := tmux.NewFakeExecutor()

	c := New(CoreDeps{
		Config: &config.Config{HostID: "test-host:abc123"},
		Tmux:   fakeTmux,
	})

	mux := http.NewServeMux()
	c.RegisterCoreRoutes(mux)

	req := httptest.NewRequest("GET", "/api/info", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)

	var body map[string]any
	err := json.NewDecoder(rec.Body).Decode(&body)
	require.NoError(t, err)

	assert.Equal(t, "test-host:abc123", body["host_id"])
	assert.Contains(t, body, "tmux_instance")
	assert.Contains(t, body, "tbox_version")
	assert.Contains(t, body, "tmux_version")
	assert.NotEmpty(t, body["os"])
	assert.NotEmpty(t, body["arch"])
}
```

- [ ] **Step 5: 執行測試確認通過**

Run: `go test ./internal/core/ -run TestInfoEndpoint -v`
Expected: PASS

- [ ] **Step 6: 執行全部 Go 測試確認無 regression**

Run: `go test ./...`
Expected: All PASS

- [ ] **Step 7: Commit**

```bash
git add internal/core/info_handler.go internal/core/info_handler_test.go
git commit -m "feat(daemon): /api/info returns host_id + tmux_instance"
```

---

### Task 4: handleGetConfig 隱藏 host_id

**Files:**
- Modify: `internal/core/config_handler.go:14-24`
- Modify: `internal/core/config_handler_test.go`

`GET /api/config` 回傳的 config 應隱藏 `host_id`（與 token 一樣是敏感欄位，不應讓 SPA 任意讀取或修改）。

- [ ] **Step 1: 寫測試斷言 host_id 被隱藏**

在 `config_handler_test.go` 加入測試（參照既有 TestGetConfig 模式）：

```go
func TestGetConfig_RedactsHostID(t *testing.T) {
	c := New(CoreDeps{
		Config: &config.Config{
			HostID: "mlab:secret",
			Token:  "tok123",
			Bind:   "0.0.0.0",
		},
	})

	req := httptest.NewRequest("GET", "/api/config", nil)
	rec := httptest.NewRecorder()
	c.handleGetConfig(rec, req)

	var body map[string]any
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&body))
	assert.Empty(t, body["host_id"], "host_id should be redacted")
	assert.Empty(t, body["token"], "token should be redacted")
	assert.Equal(t, "0.0.0.0", body["bind"])
}
```

- [ ] **Step 2: 執行測試確認失敗**

Run: `go test ./internal/core/ -run TestGetConfig_RedactsHostID -v`
Expected: FAIL — `host_id` not empty

- [ ] **Step 3: 在 handleGetConfig 加入 HostID 隱藏**

```go
func (c *Core) handleGetConfig(w http.ResponseWriter, r *http.Request) {
	c.CfgMu.RLock()
	defer c.CfgMu.RUnlock()

	cfg := *c.Cfg
	cfg.Token = ""
	cfg.HostID = ""  // 加入此行

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(cfg)
}
```

- [ ] **Step 4: 執行測試確認通過**

Run: `go test ./internal/core/ -run TestGetConfig_RedactsHostID -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/core/config_handler.go internal/core/config_handler_test.go
git commit -m "fix(daemon): redact host_id from GET /api/config response"
```

---

## PR B — SPA

### Task 5: PaneContent 擴充 tmuxInstance + cachedName

**Files:**
- Modify: `spa/src/types/tab.ts:26`
- Modify: `spa/src/types/tab.test.ts`

- [ ] **Step 1: 擴充 PaneContent session kind**

```typescript
export type PaneContent =
  | { kind: 'new-tab' }
  | { kind: 'session'; hostId: string; sessionCode: string; mode: 'terminal' | 'stream'; cachedName: string; tmuxInstance: string }
  | { kind: 'dashboard' }
  // ... 其餘不動
```

兩個新欄位都是 **required**（alpha 不向下相容，既有 persisted tabs 直接遺棄）。

- [ ] **Step 2: Commit**

```bash
git add spa/src/types/tab.ts
git commit -m "feat(spa): expand PaneContent session with cachedName + tmuxInstance"
```

---

### Task 6: HostInfo 擴充 + HostStore addHost 接受 id 參數

**Files:**
- Modify: `spa/src/stores/useHostStore.ts`
- Modify: `spa/src/stores/useHostStore.test.ts`

- [ ] **Step 1: 擴充 HostInfo 型別**

```typescript
export interface HostInfo {
  host_id: string
  tmux_instance: string
  tbox_version: string
  tmux_version: string
  os: string
  arch: string
}
```

- [ ] **Step 2: addHost 改接受 optional id 參數**

```typescript
addHost: (opts: { id?: string; name: string; ip: string; port: number; token?: string }) => string
```

實作改為：

```typescript
addHost: (opts) => {
  const id = opts.id ?? generateId()
  const order = get().hostOrder.length
  const host: HostConfig = { id, name: opts.name, ip: opts.ip, port: opts.port, token: opts.token, order }
  // ... 其餘不動
}
```

- [ ] **Step 3: 更新相關測試**

更新 `useHostStore.test.ts` 中 addHost 的測試，加入帶 `id` 的 case：

```typescript
it('addHost with explicit id uses provided id', () => {
  const { result } = renderHook(() => useHostStore())
  let id: string
  act(() => { id = result.current.addHost({ id: 'mlab:abc123', name: 'Test', ip: '1.2.3.4', port: 7860 }) })
  expect(id!).toBe('mlab:abc123')
  expect(result.current.hosts['mlab:abc123']).toBeDefined()
})
```

- [ ] **Step 4: 執行測試**

Run: `cd /Users/wake/Workspace/wake/tmux-box/spa && npx vitest run src/stores/useHostStore.test.ts`
Expected: All PASS

- [ ] **Step 5: Commit**

```bash
git add spa/src/stores/useHostStore.ts spa/src/stores/useHostStore.test.ts
git commit -m "feat(spa): HostInfo adds host_id/tmux_instance + addHost accepts explicit id"
```

---

### Task 7: AddHostDialog 從 daemon 取得 Host ID

**Files:**
- Modify: `spa/src/components/hosts/AddHostDialog.tsx`
- Modify: `spa/src/lib/host-api.ts`

- [ ] **Step 1: 確認 fetchInfo 已存在於 host-api.ts**

`fetchInfo(hostId)` 已存在，但需要在 AddHostDialog 中**新 host 尚無 hostId 時**直接用 base URL fetch。加入一個 helper：

```typescript
// host-api.ts — 新增
export async function fetchInfoByBase(base: string): Promise<Response> {
  return fetch(`${base}/api/info`)
}
```

- [ ] **Step 2: 更新 AddHostDialog 連線流程**

在 health check 成功後，fetch `/api/info` 取得 `host_id`：

```typescript
// 現有 health check 成功後，加入：
const infoRes = await fetchInfoByBase(base)
if (infoRes.ok) {
  const info = await infoRes.json()
  const daemonHostId = info.host_id  // "mlab:abc123"
  // 用 daemon 回報的 ID 作為 store key
  const id = useHostStore.getState().addHost({
    id: daemonHostId || undefined,  // fallback 到 generateId
    name: hostName || daemonHostId?.split(':')[0] || 'Host',
    ip, port, token,
  })
}
```

具體修改需讀取 AddHostDialog 的完整程式碼後調整。核心原則：
- health check 成功 → fetch `/api/info` → 用 `host_id` 作為 store key
- `/api/info` 失敗或無 `host_id` → fallback 到 SPA generateId
- 如果 `host_id` 已在 store 中（重複加入同一台 daemon） → 提示使用者

- [ ] **Step 3: Commit**

```bash
git add spa/src/lib/host-api.ts spa/src/components/hosts/AddHostDialog.tsx
git commit -m "feat(spa): AddHostDialog fetches daemon host_id on connect"
```

---

### Task 8: pane-labels 使用 cachedName fallback

**Files:**
- Modify: `spa/src/lib/pane-labels.ts:22-25`
- Modify: `spa/src/lib/pane-labels.test.ts`（如果存在）

- [ ] **Step 1: 更新 getPaneLabel session 分支**

```typescript
case 'session': {
  const session = sessionStore.getByCode(content.sessionCode)
  return session?.name ?? content.cachedName ?? content.sessionCode
}
```

新增 `content.cachedName` 作為中間 fallback：
1. session 在 store 中 → 用 live name
2. session 不在 store（斷線/刪除）→ 用 cachedName
3. 都沒有 → 用 sessionCode

- [ ] **Step 2: 寫測試驗證 cachedName fallback**

```typescript
it('session uses cachedName when session not in store', () => {
  const content: PaneContent = { kind: 'session', hostId: 'h', sessionCode: 'abc', mode: 'terminal', cachedName: 'my-session' }
  const label = getPaneLabel(content, { getByCode: () => undefined }, ws, t)
  expect(label).toBe('my-session')
})
```

- [ ] **Step 3: 執行測試**

Run: `npx vitest run src/lib/pane-labels.test.ts`

- [ ] **Step 4: Commit**

```bash
git add spa/src/lib/pane-labels.ts spa/src/lib/pane-labels.test.ts
git commit -m "feat(spa): pane label uses cachedName fallback for disconnected sessions"
```

---

### Task 9: 建立 session tab 時設定 cachedName + tmuxInstance

**Files:**
- Modify: `spa/src/components/hosts/SessionsSection.tsx`
- Modify: `spa/src/components/SessionPanel.tsx`

所有建立 session tab 的地方都需要加入 `cachedName` 和 `tmuxInstance`。

- [ ] **Step 1: 找到所有建立 session tab 的位置**

搜尋 `kind: 'session'` 的 PaneContent 建構：
- `SessionsSection.tsx` handleOpen
- `SessionPanel.tsx`（如果有直接建 tab 的邏輯）
- 其他可能的位置

- [ ] **Step 2: 更新 SessionsSection handleOpen**

```typescript
const handleOpen = (session: Session, mode: string) => {
  const tabId = useTabStore.getState().openSingletonTab({
    kind: 'session',
    hostId,
    sessionCode: session.code,
    mode: mode as 'terminal' | 'stream',
    cachedName: session.name,
    tmuxInstance: useHostStore.getState().runtime[hostId]?.info?.tmux_instance,
  })
  // ... 其餘不動
}
```

- [ ] **Step 3: 更新其他建 tab 的位置（如 SessionPanel）**

同樣模式：加入 `cachedName: session.name` 和 `tmuxInstance`。

- [ ] **Step 4: Commit**

```bash
git add spa/src/components/hosts/SessionsSection.tsx spa/src/components/SessionPanel.tsx
git commit -m "feat(spa): set cachedName + tmuxInstance when creating session tabs"
```

---

### Task 10: WS session 更新時同步 cachedName

**Files:**
- Modify: `spa/src/hooks/useMultiHostEventWs.ts`
- Modify: `spa/src/stores/useTabStore.ts`

當 WS 推送 sessions 更新時，需要同步更新 tab 中的 cachedName（session 可能被 rename）。

- [ ] **Step 1: useTabStore 加入 updatePaneContent helper**

```typescript
updateSessionCache: (hostId: string, sessionCode: string, cachedName: string) =>
  set((state) => {
    let changed = false
    const tabs = { ...state.tabs }
    for (const [id, tab] of Object.entries(tabs)) {
      const primary = getPrimaryPane(tab.layout)
      if (primary.content.kind === 'session' &&
          primary.content.hostId === hostId &&
          primary.content.sessionCode === sessionCode &&
          primary.content.cachedName !== cachedName) {
        // Shallow clone the layout path to update cachedName
        tabs[id] = {
          ...tab,
          layout: updatePaneInLayout(tab.layout, primary.id, {
            ...primary.content,
            cachedName,
          }),
        }
        changed = true
      }
    }
    return changed ? { tabs } : state
  }),
```

此 helper 需要一個 `updatePaneInLayout` 輔助函式。如果已有類似函式可複用，否則新增。

- [ ] **Step 2: useMultiHostEventWs sessions handler 加入 cachedName 同步**

```typescript
if (event.type === 'sessions') {
  try {
    const data: Session[] = JSON.parse(event.value)
    useSessionStore.getState().replaceHost(hostId, data)
    // Sync cachedName for existing tabs
    for (const s of data) {
      useTabStore.getState().updateSessionCache(hostId, s.code, s.name)
    }
  } catch { /* ignore */ }
  return
}
```

- [ ] **Step 3: 寫測試驗證 cachedName 同步**

- [ ] **Step 4: Commit**

```bash
git add spa/src/stores/useTabStore.ts spa/src/hooks/useMultiHostEventWs.ts
git commit -m "feat(spa): sync cachedName on WS session updates"
```

---

### Task 11: 全面驗證

- [ ] **Step 1: Go 測試**

Run: `go test ./...`
Expected: All PASS

- [ ] **Step 2: SPA 測試**

Run: `cd spa && npx vitest run`
Expected: All PASS（除既有 TerminalView 失敗）

- [ ] **Step 3: SPA Lint + Build**

Run: `cd spa && pnpm run lint && pnpm run build`
Expected: No new errors, build success

---

## 延後項目（不在本 Phase）

| 項目 | 歸屬 Phase |
|------|-----------|
| tmux instance 比對 → session 綁定失效 UI | Phase 4（錯誤 UI） |
| HostStore 的 DEFAULT_ID 遷移 | 低優先（alpha 階段，使用者重新加 host 即可） |
| tmux_instance 在 session broadcast 中附帶（目前只在 /api/info） | 按需加入 |
