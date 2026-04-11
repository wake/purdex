# Spec: Issue #26 — handleTerminal Cfg 讀取 race 修復

- **Issue**: [#26 fix: handleTerminal reads s.cfg without cfgMu lock](https://github.com/wake/tmux-box/issues/26)
- **日期**: 2026-04-12
- **範圍**: 單一檔案 (`internal/module/session/service.go`) + 對應測試

## 1. 背景

Issue 原文指向已不存在的 `internal/server/server.go`。經過 Phase 1.6a 重構後，相關程式碼搬到 `internal/module/session/`：

- `handleTerminal` → `HandleTerminalWS` (`service.go:123`)
- `BuildTerminalRelay` → `buildTerminalRelayArgs` (`service.go:182`，純函式，無 race)

但 race condition 仍然存在：`HandleTerminalWS` 在 `service.go:137` 讀取 `m.core.Cfg.Terminal.GetSizingMode()` 時**沒有取 `core.CfgMu.RLock()`**，而寫入端 `handlePutConfig` 在 `internal/core/config_handler.go:79` 仍然在 `CfgMu.Lock()` 下修改 `c.Cfg.Terminal.SizingMode`。

並發場景：使用者打開 terminal WS 連線的同時，另一個 client 透過 `PUT /api/config` 修改 `terminal.sizing_mode` → data race。

## 2. 問題定義

### 2.1 受影響的程式碼

```go
// internal/module/session/service.go:134-138
sizingMode := "auto"
if m.core != nil && m.core.Cfg != nil {
    sizingMode = m.core.Cfg.Terminal.GetSizingMode()  // ← 無鎖讀取
}
```

### 2.2 寫入端 (對照組，已正確上鎖)

```go
// internal/core/config_handler.go:47, 78-80
c.CfgMu.Lock()
...
if req.Terminal != nil && req.Terminal.SizingMode != "" {
    c.Cfg.Terminal.SizingMode = req.Terminal.SizingMode  // ← 持寫鎖
}
```

### 2.3 參考的正確 pattern

```go
// internal/module/stream/handler.go:177-182
m.core.CfgMu.RLock()
presets := m.core.Cfg.Stream.Presets
token := m.core.Cfg.Token
port := m.core.Cfg.Port
bind := m.core.Cfg.Bind
m.core.CfgMu.RUnlock()
```

## 3. 修復方案

### 3.1 程式碼變更

將 `HandleTerminalWS` 中的 config 讀取改為 snapshot pattern，仿照 stream/handler.go：

```go
// HandleTerminalWS attaches a WebSocket connection to the tmux session PTY relay.
func (m *SessionModule) HandleTerminalWS(w http.ResponseWriter, r *http.Request, code string) {
    info, err := m.GetSession(code)
    if err != nil { ... }
    if info == nil { ... }

    // Snapshot sizing mode under read lock to avoid race with handlePutConfig.
    sizingMode := "auto"
    if m.core != nil && m.core.Cfg != nil {
        m.core.CfgMu.RLock()
        sizingMode = m.core.Cfg.Terminal.GetSizingMode()
        m.core.CfgMu.RUnlock()
    }

    // ... rest unchanged
}
```

### 3.2 為什麼是 snapshot

`GetSizingMode()` 回傳 string（value type），複製即完成隔離；之後的 switch 邏輯不再需要持鎖。最小臨界區、無 lock 升級風險。

### 3.3 nil 防護維持

外層 `m.core != nil && m.core.Cfg != nil` 守衛保留。生產路徑透過 `Init()` 一定會把 `m.core = c` 設好且 `c.Cfg` 由 `config.Load` 載入（缺檔回 default），不會 nil；nil 保護是專為測試直接 `&SessionModule{}` 構造的情境（例如 `hooks_test.go`）所留。

## 4. 不變條件 (Invariants)

- **I1**: 任何讀取 `c.Cfg.*` 欄位的程式碼必須持有 `CfgMu` 的 read lock 或 write lock。本修復強化此 invariant。
- **I2 (reader-only)**: Reader 端的 `CfgMu` 臨界區應盡可能短 — snapshot 後立即解鎖，避免延長 writer 等待。本修復符合。
  - 註：writer 端 (`handlePutConfig`) 目前仍在持寫鎖期間呼叫 `config.WriteFile`（disk IO），這是 pre-existing 設計，out of scope。
- **I3**: 修復後 `go test -race ./internal/module/session/...` 必須通過。

## 5. 測試策略

### 5.1 Race 回歸測試（直接打 `HandleTerminalWS`）

新增測試 `TestHandleTerminalWS_NoConfigRace` (`service_test.go`)，**直接呼叫 `HandleTerminalWS` 而非抽 helper** — 否則 race regression 測到的是 helper 而不是真正的 bug 點。

關鍵觀察：racy 讀取（`m.core.Cfg.Terminal.GetSizingMode()`）發生在 `relay.HandleWebSocket(w, r)` 之前。所以即使 WS upgrade 失敗、即使 fake tmux 立即 exit，racy 讀取也已經執行 — race detector 仍能命中。

### 5.2 測試骨架

利用既有 `newTestModule(t)` helper（或仿照其建構新 helper 帶 Cfg）。需要 `core.New(CoreDeps{Config: ...})` 才能讓 `m.core.Cfg` 非 nil（既有 helper 沒帶 Config，新測試需自建一份）。

```go
func TestHandleTerminalWS_NoConfigRace(t *testing.T) {
    // 自建 Core，確保 Cfg 非 nil
    meta, err := store.OpenMeta(":memory:")
    require.NoError(t, err)
    t.Cleanup(func() { meta.Close() })

    fake := tmux.NewFakeExecutor()
    fake.AddSession("test-session", "/tmp")  // 自動分配 $0

    mod := NewSessionModule(meta)
    c := core.New(core.CoreDeps{
        Config:   &config.Config{Terminal: config.TerminalConfig{SizingMode: "auto"}},
        Tmux:     fake,
        Registry: core.NewServiceRegistry(),
    })
    require.NoError(t, mod.Init(c))

    code, err := EncodeSessionID("$0")
    require.NoError(t, err)

    stop := make(chan struct{})

    // Writer goroutine: 持續切換 SizingMode
    var writerWg sync.WaitGroup
    writerWg.Add(1)
    go func() {
        defer writerWg.Done()
        modes := []string{"auto", "terminal-first", "minimal-first"}
        for i := 0; ; i++ {
            select {
            case <-stop:
                return
            default:
            }
            c.CfgMu.Lock()
            c.Cfg.Terminal.SizingMode = modes[i%len(modes)]
            c.CfgMu.Unlock()
        }
    }()

    // Reader goroutines: 並發呼叫 HandleTerminalWS
    var readerWg sync.WaitGroup
    for i := 0; i < 50; i++ {
        readerWg.Add(1)
        go func() {
            defer readerWg.Done()
            req := httptest.NewRequest("GET", "/ws/terminal/"+code, nil)
            rec := httptest.NewRecorder()
            // WS upgrade 會失敗（rec 非 Hijacker），但 racy 讀取早已執行
            mod.HandleTerminalWS(rec, req, code)
        }()
    }

    readerWg.Wait()
    close(stop)
    writerWg.Wait()
}
```

執行：`go test -race ./internal/module/session/ -run TestHandleTerminalWS_NoConfigRace`。
- 修復前：race detector 命中
- 修復後：通過

### 5.3 既有測試

`TestBuildTerminalRelayArgs_*` (service_test.go:9-19) 純函式測試不受影響，繼續執行確保未破壞。

`module_test.go` 中的 `newTestModule` 已示範 fake executor + 測試 module 的構造，可參考重用。

### 5.4 注意

- 若 `httptest.NewRecorder` 不支援的 hijack 路徑導致過早 panic，可改用 `httptest.NewServer` + real `http.Client`，並讓 client 不升級 WS（直接 GET）— racy 讀取仍會在 server handler 被觸發。
- 不需也不應抽出 `snapshotSizingMode()` helper：那只會測到 helper 自己持鎖，無法保證 `HandleTerminalWS` 真的呼叫了它。

### 5.3 不變的測試

`TestBuildTerminalRelayArgs_*` 已存在（service_test.go:9-19）— 純函式測試不受影響，只需確認仍通過。

## 6. 範圍邊界

### 包含
- `internal/module/session/service.go` — `HandleTerminalWS` 加鎖
- `internal/module/session/service_test.go` — race regression test (`TestHandleTerminalWS_NoConfigRace`)

### 不含
- 不修 `agent/module.go:71` — 那是 `Init()` 階段在任何 request 進來之前的單執行緒初始化，無 race
- 不重構 `core.Cfg` 為 atomic.Pointer — 改動範圍過大、out of scope
- 不引入 typed config snapshot struct — out of scope

## 7. 風險與回滾

- **風險低**：純粹加鎖，行為等價於原本（讀取一個 string）
- **回滾**：直接 revert commit 即可

## 8. 驗收條件

- [ ] `go test -race ./internal/module/session/...` 通過
- [ ] `go test ./...` 全綠
- [ ] `gofmt`、`go vet` 乾淨
- [ ] PR 兩輪 review（標準 + 三方 parallel）通過
- [ ] Issue #26 在 PR 描述中以 `Closes #26` 連結
