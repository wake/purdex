# Plan: Issue #26 — handleTerminal cfgMu race 修復

- **Spec**: `docs/superpowers/specs/2026-04-12-issue-26-cfgmu-race-fix.md`
- **Issue**: #26
- **Branch**: `worktree-issue-26-cfgmu-race`
- **方法**: TDD（紅 → 綠 → refactor）

## 任務分解

### Step 1: 撰寫失敗的 race regression 測試 (Red)

**檔案**: `internal/module/session/service_test.go`

**動作**:
1. 新增測試 `TestHandleTerminalWS_NoConfigRace`，依 spec §5.2 骨架
2. 確認 import 包含 `httptest`, `sync`, `core`, `config`, `tmux`, `store`
3. 執行 `go test -race ./internal/module/session/ -run TestHandleTerminalWS_NoConfigRace`
4. **預期**: race detector 命中 (DATA RACE on `Cfg.Terminal.SizingMode`) → 測試 fail
5. 若不命中，調整 reader/writer goroutine 數量或 inject `runtime.Gosched()` 提高碰撞機率

**驗證標準**:
- 測試在未修復的程式碼上**必須 fail**（race detector 命中）
- 若直接 pass，代表沒測到實際 race，需重新檢查觸發路徑

### Step 2: 套用修復 (Green)

**檔案**: `internal/module/session/service.go`

**動作**: 修改 `HandleTerminalWS` 第 134-138 行：

```diff
- // Determine sizing mode from config (default to "auto" if no config).
- sizingMode := "auto"
- if m.core != nil && m.core.Cfg != nil {
-     sizingMode = m.core.Cfg.Terminal.GetSizingMode()
- }
+ // Snapshot sizing mode under read lock to avoid race with handlePutConfig
+ // (config.go writes Terminal.SizingMode under CfgMu.Lock).
+ sizingMode := "auto"
+ if m.core != nil && m.core.Cfg != nil {
+     m.core.CfgMu.RLock()
+     sizingMode = m.core.Cfg.Terminal.GetSizingMode()
+     m.core.CfgMu.RUnlock()
+ }
```

**驗證標準**:
- `go test -race ./internal/module/session/ -run TestHandleTerminalWS_NoConfigRace` **通過**
- `go test ./internal/module/session/...` 全綠
- `go test -race ./internal/module/session/...` 全綠

### Step 3: 全套件 race + lint 驗證

**動作**:
1. `go build ./...` — 編譯通過
2. `go vet ./...` — 無警告
3. `gofmt -l internal/module/session/` — 無輸出
4. `go test -race ./...` — 全綠

**驗證標準**: 上述命令全部通過。任一 fail 必須回頭修。

### Step 4: 提交

- Commit subject: `fix: lock CfgMu when reading sizing mode in HandleTerminalWS`
- Body 提到 `Closes #26`
- 兩個檔案：`service.go` + `service_test.go`

### Step 5: PR + 兩輪 review

- 走標準 PR 流程
- 第一輪：`code-review:code-review` skill
- 第二輪：3 個 parallel agent（攻擊 / 防守 / 檔案大小）
- 修高信心 / 低複雜度 / 測試相關問題

### Step 6: Merge + bump

- Merge PR
- Update `VERSION` + `CHANGELOG.md`
- Push

## 風險與 Mitigation

| 風險 | Mitigation |
|------|------------|
| Race test 在 CI 不穩定 | 增加 reader/writer 迭代次數確保 race 視窗夠大；race detector 對碰撞很敏感 |
| `httptest.NewRecorder` 與 ws upgrade 互動產生 panic | 已驗證：upgrader 在非 Hijacker 上回 error，不 panic（relay.go:42-45） |
| Cfg = nil 路徑被測試 dirty 觸發 | nil guard 保留，無破壞風險 |
| 測試 leak goroutine | writer goroutine 用 `stop` channel 收斂，readers 都 wait 完才 close stop |

## 不做的事 (Out of Scope)

- 不改寫 writer 端 (`config_handler.go`)
- 不抽 `snapshotSizingMode()` helper
- 不改 `agent/module.go` Init() 那段（單執行緒初始化）
- 不引入 atomic.Pointer
- 不動 `BuildTerminalRelay` / `buildTerminalRelayArgs`（已純函式化）

## 預估改動規模

- `service.go`: +5 / -1 行
- `service_test.go`: +60 行（含 imports）
- 總計：~65 行
