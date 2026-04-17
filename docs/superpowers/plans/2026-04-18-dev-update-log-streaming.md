# Dev Update Log Streaming (Plan B Increment)

> 前置：`2026-03-29-dev-update-auto-build` 已實作（`.build-info.json` writer + auto-build goroutine + 3s polling）。本計畫在其上加「使用者看得見 build 進度」。

**Goal:** 讓 Air 在 check 觸發 daemon auto-build 時，即時看到 build log（取代 3 秒黑盒 polling）。並提示何時需要完整 electron rebuild。

**Architecture:**
- Daemon 把 `runCombinedOutput` 換成 pipe-based spawner；同時開一條 broadcast channel 供多個 subscriber 收 log。
- 新增 `GET /api/dev/update/check/stream`（SSE）。舊 `/check`（JSON）保留給 Electron main 啟動背景檢查用。
- `/download` 不動。
- 加 `requiresFullRebuild: bool` 回傳欄位（SSE `phase:check` 事件與 JSON `/check` 回應都帶）。

**Tech:** Go `bufio.Scanner` + `sync.Mutex` / Go `net/http` Flusher for SSE / React `EventSource`.

---

### Task A — Daemon: line-streaming spawner + broadcast

**Files:** `internal/module/dev/stream.go` (new), `internal/module/dev/module.go`, `internal/module/dev/stream_test.go` (new)

- [ ] A1. 先寫測試 `stream_test.go`：
  - `TestBuildSession_FanoutToSubscribers` — 2 個 subscriber 都收到相同 sequence of events（phase-start / stdout * N / done）
  - `TestBuildSession_LateSubscriberReceivesReplay` — 在已發出 3 行後 subscribe，應先收到 3 行 replay、再收到後續
  - `TestBuildSession_ErrorPropagation` — build 失敗時 subscriber 收到 `error` event 後 channel close
  - `TestBuildSession_ConcurrentUnsubscribe` — subscribe 後立刻 unsubscribe 不 deadlock
- [ ] A2. 實作 `buildSession`：
  - `events []buildEvent` 保存完整 log（late subscriber 用）
  - `subs map[chan buildEvent]struct{}`
  - `append(ev)` — 鎖 + 加 events + fanout（`select` + default 避免 block 慢 subscriber；buffer=64）
  - `subscribe()` 回傳 `(ch, replay, unsubscribe)`
  - `finish(err)` — 最後一個 event + 關閉所有 ch
- [ ] A3. 把 `DevModule.execCmd` 改成回傳 channel 的版本；新舊都留（`execCmd` for legacy、`spawnStream` for streaming build）
- [ ] A4. `runBuild` 改成建立 `buildSession` + 呼叫 `spawnStream` 串流 stdout/stderr 進 session
- [ ] A5. `go test ./internal/module/dev/ -v` 全綠

### Task B — Daemon: SSE endpoint + requiresFullRebuild

**Files:** `internal/module/dev/handler.go`, `internal/module/dev/rebuild_detect.go` (new), 對應 test

- [ ] B1. 寫 `TestHandleCheckStream_NotStale` — 沒 source change 時，SSE 送一個 `done`（`building:false`）即收流
- [ ] B2. 寫 `TestHandleCheckStream_TriggersBuild` — source 改變時啟動 build + 串 log + 結尾 `done` 帶新 hash
- [ ] B3. 寫 `TestHandleCheckStream_LateSubscribe` — 已在 build 中進來，收到 replay + 後續
- [ ] B4. 寫 `TestRequiresFullRebuild` — package.json / electron-builder.yml / icon.icns / electron/**/Info.plist 變動 → true；純 spa/TS 變動 → false
- [ ] B5. 實作 `handleCheckStream`：`Content-Type: text/event-stream`、`Cache-Control: no-cache`、`Connection: keep-alive`、每個 event flush
- [ ] B6. 實作 `detectRequiresFullRebuild(repoRoot)`：檢查 `git diff HEAD~1 HEAD --name-only` 是否命中白名單；偵測新裝的 native module（掃 `node_modules` 有 `.node`）
- [ ] B7. `handleCheck`（JSON）也回傳 `requiresFullRebuild`
- [ ] B8. 註冊路由 `GET /api/dev/update/check/stream`
- [ ] B9. 所有測試綠

### Task C — SPA: log panel + EventSource client

**Files:** `spa/src/components/settings/DevEnvironmentSection.tsx`, `spa/src/components/settings/DevBuildLogPanel.tsx` (new), test

- [ ] C1. 在 `DevBuildLogPanel.test.tsx` 先寫測試：mock `EventSource`，驗證 stdout 事件 append 到 `<pre>`、auto-scroll、done 事件關閉
- [ ] C2. 實作 `DevBuildLogPanel`：
  - Props: `streamUrl: string`, `onDone: (result) => void`, `onError: (msg) => void`
  - 內含 `<pre>` + ref scroll 到底
  - 「複製完整 log」按鈕
- [ ] C3. 改 `DevEnvironmentSection`：
  - 當 `status === 'building'` 時改開 `<DevBuildLogPanel>`（取代 3s polling 分支）
  - 完成後維持現有 compare 流程
- [ ] C4. `requiresFullRebuild` 顯示警告 banner + 提示「請到 Mini 跑 `pnpm run electron:build`」
- [ ] C5. i18n：新增 `settings.dev.log.copy` / `settings.dev.log.rebuild_hint` 兩 key（en + zh-TW）
- [ ] C6. `npx vitest run` 綠 + `pnpm run lint` 綠

### Task D — Manual verify + PR

- [ ] D1. Mini 端 `go build -o bin/pdx ./cmd/pdx` + 重啟 daemon
- [ ] D2. `pnpm run electron:build` 產新 `out/.build-info.json` + `dist/mac*/Purdex.app`
- [ ] D3. Air：裝新 `.app`、`git push`、Settings → Development → Check → 觀察 log panel 實時滾動
- [ ] D4. 故意退一個 commit，再 Check 一次，驗證重新觸發
- [ ] D5. `code-review:code-review` skill
- [ ] D6. 3 parallel agents（attack / defense / file-size）
- [ ] D7. 處理兩輪 review → 其他用 gh issue
- [ ] D8. 開 PR、bump VERSION + CHANGELOG

---

## 非目標（本 PR 不碰）

- 自動觸發 electron rebuild（複雜度高、CP 值低）
- Signed auto-updater（留正式版）
- xterm.js 渲染 log（純 `<pre>` 足夠；未來有 ANSI 色碼需求再升級）
- `/download` 改 SSE（職責分離：SSE 只串 log、tarball 走獨立 GET）
