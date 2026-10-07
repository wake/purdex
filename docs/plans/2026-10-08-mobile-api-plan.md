# Mobile API Plan

Spec：`docs/specs/2026-10-08-mobile-api-spec.md`。三個 PR，每 task 獨立 commit，TDD。

## P1 transcript（`internal/module/agent` ＋新檔 `internal/transcripttail/`）
1. `transcripttail` 套件（純函式，無 HTTP）：
   - `Tail(f, n, maxBytes)`：自尾端 64 KB 區塊往回找 `\n`，回完整行、start/end offset、more。
   - `After(f, off, maxBytes)`：Seek 後讀至最後一個 `\n`。
   - 測試先行：多位元組跨區塊、無結尾換行、空檔、單行 >2 MB、`after>size`。
2. `resolveTranscriptPath(owner, home)`：provenance 路徑優先，缺則 slug 推算；`EvalSymlinks` 後須在 `<home>/.claude/projects/` 下、一般檔、`.jsonl`。測試：symlink 逃逸、非 jsonl、slug 規則（對照 `internal/conversations` 實際目錄名）。
3. Handler `GET /api/sessions/{code}/transcript`（`module.go` 掛路由）：參數驗證（tail/after 互斥、上限）、404 reasons、reset 判定、回應 JSON。測試用 httptest＋假 owner。
4. mutation test：移除邊界判斷／路徑檢查各一次，對應測試須紅。
5. 真機：mlab 37 MB 檔 `tail=800` < 1 s。

## P2 mirror（`internal/module/session`、`internal/terminal`）
1. `buildTerminalRelayArgs` 增 mirror 參數；`HandleTerminalWS` mirror 時不設 OnStart。測試：args 含 `-f ignore-size`、FakeExecutor `autoResizeCalls` 為空（各 sizing_mode）。
2. `Relay.OnWindowSize`（`func() (cols, rows uint16, err error)`＋間隔 1 s）：起始送一次、變了才送 text frame `{"type":"window",…}`；與 batcher 共用 writeMu。tmux executor 加 `WindowSize(target)`（`display-message -p`），FakeExecutor 同步。測試：首送、無變化不送、變化送、連線關閉停止 goroutine。
3. 真機：桌機開 session，mirror 連線送 resize 40x20，桌機 window 不變（`tmux display -p '#{window_width}'` 前後相同）。

## P3 capabilities（`internal/core/info_handler.go`）
`capabilities` 常數切片；測試斷言內容。P1／P2 merge 後才做。

## 流程
plan＋spec 一輪 codex review → subagent TDD → 各 PR 走 R1＋R2（攻擊→critic）→ merge＋bump → 通知 purdex-ios（`air26/_aif8mz`），deploy mlab 找 purdex-d3 `[z9ruk0]`。
