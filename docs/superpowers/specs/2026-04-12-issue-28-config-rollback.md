# Spec — Issue #28: handlePutConfig Rollback on writeConfig Failure

## Problem

`internal/core/config_handler.go:handlePutConfig` 先將請求內容套用到 `c.Cfg`（記憶體），再呼叫 `config.WriteFile` 寫入磁碟。若寫檔失敗，記憶體已更新但磁碟未更新，產生不一致：
- 目前執行的 daemon 使用新設定
- 下次重啟 daemon 從磁碟讀回舊設定
- 客戶端收到 500，不確定設定是否生效

## Scope

僅修改 `internal/core/config_handler.go` 的 `handlePutConfig` 函式與其測試。不觸及 config file format、callback 機制、其他 handler。

## Invariants

1. **原子性**：HTTP 請求完成後，記憶體 (`c.Cfg`) 與磁碟 (`c.CfgPath`) 必須一致。寫檔失敗時，記憶體狀態必須回復到請求前。
2. **鎖保護**：整段 mutation + write + rollback 必須在 `c.CfgMu.Lock()` 保護範圍內，避免其他 reader/writer 觀察到中間狀態。
3. **callback 不誤觸**：若寫檔失敗並 rollback，`NotifyConfigChange` 不得被呼叫（因為設定實際上沒變）。
4. **回傳碼語意保持**：
   - 400 — invalid JSON / invalid sizing_mode
   - 500 — writeConfig 失敗（rollback 完成後回傳）
   - 200 — 成功（記憶體與磁碟皆已更新）
5. **指標身分保留**：`c.Cfg` 指標不可被換掉，rollback 必須透過 `*c.Cfg = snapshot` 寫回原本的 struct（其他 goroutine 持有該指標）。
6. **Config 不得 in-place mutation**：codebase 不得對 `c.Cfg` 的子欄位做 in-place mutation（如 `append(c.Cfg.Stream.Presets, ...)` 或 map update）。shallow snapshot rollback 的正確性建立在此 invariant 之上；所有修改必須整個指派新值。

## Approach

在所有 mutation 之前拍 shallow snapshot：

```go
snapshot := *c.Cfg
```

Shallow copy 的安全性分析：
- `c.Cfg.Stream = *req.Stream` — 整個 struct 取代，rollback 時 `c.Cfg.Stream = snapshot.Stream` 還原整個 slice header。✅
- `c.Cfg.Detect.CCCommands = *req.Detect.CCCommands` — 整個 slice 取代。✅
- `c.Cfg.Detect.PollInterval = *req.Detect.PollInterval` — scalar。✅
- `c.Cfg.Terminal.SizingMode = req.Terminal.SizingMode` — scalar。✅
- 沒有 `append(c.Cfg.X, ...)` 之類的 in-place 操作。
- 沒有 map 欄位 mutation。

因此 shallow copy `*c.Cfg` 即可完整 rollback，**不需要 deep copy**。

寫檔失敗時：
```go
if err := config.WriteFile(c.CfgPath, *c.Cfg); err != nil {
    *c.Cfg = snapshot  // rollback
    c.CfgMu.Unlock()
    http.Error(w, "failed to save config: "+err.Error(), http.StatusInternalServerError)
    return
}
```

同時 `detectChanged` 變成「是否真的已 commit」的旗標，只有在寫檔成功後才會觸發 callback。

## Test Plan

新增測試 `TestPutConfigRollsBackOnWriteFailure`：

**Setup**：
- Core 初始配置帶入明確的 Stream.Presets、Detect.CCCommands、Terminal.SizingMode
- `c.CfgPath` 指向一個不可能寫入的路徑：在 `t.TempDir()` 內建一個普通檔案 `blocker`，然後把 `CfgPath` 設為 `filepath.Join(blocker, "config.toml")`。父路徑是檔案而非目錄，任何寫檔嘗試（包含 tmp + rename）都會得到 `ENOTDIR`，100% 跨平台、不需 chmod cleanup。

**Action**：
- PUT 一個同時修改 stream、detect、terminal 的 body
- 註冊 `OnConfigChange` callback 計數

**Assertions**：
1. `rec.Code == 500`
2. Response body 包含 "failed to save config"
3. `c.Cfg.Stream.Presets` 等於初始值（未被覆蓋）
4. `c.Cfg.Detect.CCCommands` 等於初始值
5. `c.Cfg.Detect.PollInterval` 等於初始值
6. `c.Cfg.Terminal.SizingMode` 等於初始值
7. callback 呼叫次數為 0
8. `c.Cfg` 指標身分未變（不是被整個換掉）— 透過比對操作前後 `c.Cfg` 指標相等驗證
9. **recovery 測試**：rollback 後，將 `c.CfgPath` 改為正常可寫路徑後再發一次 PUT，必須 200 且狀態正確套用（確認 state 沒被破壞）

**Cleanup**：無需額外處理。`t.TempDir()` 的 auto-cleanup 會清掉整個目錄（包含 blocker 檔與其下的失敗 config path）。

## Edge Cases

- **既有測試**：`TestPutConfigUpdatesStreamAndPersists`、`TestPutConfigDetectCCCommandsTriggersOnConfigChange` 等必須仍然通過。
- **鎖順序**：實作時保持現有手動 `Lock` / `Unlock` 模式（不改為 defer Unlock），因為 callback 呼叫發生在 Unlock 之後。

## Out of Scope

- writeConfig 本身的原子性（fsync、tmp + rename）— 由 `internal/config` 負責，不在本次修改範圍
- 其他 handler（如 host-config 等）的 rollback — issue #28 僅針對 `handlePutConfig`
- deep copy/reflection-based snapshot — 目前 mutation pattern 不需要
