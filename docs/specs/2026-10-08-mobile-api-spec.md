# Mobile API 三項小改動 Spec

來源：purdex-ios brief `docs/brief/2026-10-08-daemon-mobile-small.md`（2026-10-08 使用者指定）。
核心原則：iOS 只是多一個操作介面，**絕不改動桌機終端機的作業習慣**。

三個 phase，各一個 PR：P1 transcript → P2 mirror → P3 capabilities（P3 依賴前兩者，最後做）。

## P1 `GET /api/sessions/{code}/transcript`

模組：`internal/module/agent`（已有 `resolveSessionOwner`、`/provenance`）。

### 路徑決定（client 不送路徑）
1. `resolveSessionOwner(code)`；找不到 → 404 `{"error":"no_agent"}`。
2. `agent_type != "cc"`（Claude Code 的 provider 名稱是 `cc`；codex／opencode 等）→ 404 `{"error":"unsupported","agent_type":…}`。
3. 路徑 = owner.TranscriptPath；空則用 `~/.claude/projects/<slug(cwd)>/<session_id>.jsonl`，slug＝cwd 中非 `[A-Za-z0-9]` 的字元各換成 `-`。兩者皆缺 → 404 `no_transcript`。
4. **安全**：`EvalSymlinks` 後必須落在 `~/.claude/projects/` 之下、是一般檔、副檔名 `.jsonl`；否則 404 `no_transcript`（provenance 的路徑來自 hook，不可信，不能變成任意讀檔管道）。檔案不存在 → 404 `file_missing`。

### 參數
- `tail=<n>`：自檔尾往回取最後 n 個完整行（預設 800，上限 5000）。
- `after=<byte offset>`：取 offset 之後的完整行。`after` 與 `tail` 同時帶 → 400。
- `transcript_id=<檔名>`（選填）：client 上次拿到的 id；與現在不同 → 回 `reset:true`（見下），不回行。

### 回應 200
```json
{"transcript_id":"<session_id>.jsonl","size":N,"mtime":unixMs,
 "start_offset":A,"end_offset":B,"more":false,"reset":false,"lines":["…","…"]}
```
- `lines` 為 jsonl 原文，每元素一個完整行（不含換行）。永不回半行、不切斷 UTF-8（只在 `\n` 邊界切）。檔尾未以 `\n` 結束的最後一段視為未完成行，不回、`end_offset` 停在它之前。
- `end_offset` 供下次 `after`。`more:true` 表示因單次上限（2 MB）截斷，後面還有。tail 模式超過 2 MB 時保留**較新**的行。
- 輪替偵測：`after > size`，或 `transcript_id` 與現在不同 → `reset:true`、`lines:[]`，`start_offset`/`end_offset` 為檔尾；client 應重新 `tail`。
- 單行大於 2 MB：不略過，該行獨立成一次回應並帶 `more:true`（單行硬頂 8 MB，再大回 413 `line_too_large`）。
- 讀檔用 `Seek`＋區塊讀（tail 由尾端 64 KB 區塊往回找換行），不整檔載入；37 MB 檔 tail=800 < 1 s。

## P2 `/ws/terminal/{code}?mirror=1`

- `HandleTerminalWS` 讀 `mirror=1`：args 帶 `-f ignore-size`，**不**設 `OnStart`（不 `ResizeWindowAuto`、不 `SetWindowOption`），不論 `sizing_mode`。只影響該連線。
- client 的 `resize` 訊息照舊只改該 PTY 大小；ignore-size 的 client 不參與 window 計算，故桌機不變。
- **window 實際大小通知**（回應 purdex-ios 補充）：mirror 連線建立後及每次變化時，daemon 送 **text frame** `{"type":"window","cols":N,"rows":N}`（binary frame 仍是終端機輸出，client 以 frame 型別區分）。實作：relay 新增 `OnWindowSize` 輪詢 hook，mirror 連線每 1 s 以 `tmux display-message -p -t <target> '#{window_width} #{window_height}'` 取值，**變了才送**，連線起始必送一次。
- 一般（非 mirror）連線行為完全不變。
- 風險：ignore-size client 看到的是 window 實際大小輸出，手機以桌機欄數渲染（縮放或水平捲動，iOS 端處理）。

## P3 `/api/info` capabilities
`info["capabilities"] = []string{"transcript.v1","terminal.mirror.v1"}`（常數清單，排序固定，之後新功能都在此宣告；P3 在 P1／P2 合併後才加入對應項目）。

## 測試（TDD）
- P1：行邊界（多位元組字元跨 64 KB 區塊、無結尾換行、空檔）、tail／after 增量、輪替（after>size、transcript_id 變）、2 MB 截斷 `more`、路徑推算（slug）、路徑逃逸（symlink 出 projects、非 jsonl）、codex 404。含 mutation test：拿掉邊界判斷測試必須紅。
- P2：`buildTerminalRelayArgs` mirror 帶 `-f ignore-size` 且 OnStart 為 nil（FakeExecutor 驗證 `autoResizeCalls` 為空）；window frame 首送與變化才送。
- P3：`/api/info` 含兩個 capability。

## 非目標
codex／opencode transcript、`/api/fs/read` 的 10 MB 上限不動。

## 修訂（plan／spec codex review，job task-muxmrlf3-21vl0q）

- **owner 查詢錯誤**：用 `resolveSessionOwnerErr`；`found=false,err=nil` → 404 `no_agent`；`err != nil`（tmux／frames／timeout）→ 503 `lookup_failed`，不可當成沒有 agent。
- **After 落在行中**：offset 不在行首時丟棄到下一個 `\n` 為止，`start_offset` 為丟棄後的位置；`start_offset` 永遠在行首。
- **單行超限**：reader 回 `ErrLineTooLarge`（行 > 8 MB），handler 映射 413 `line_too_large`；2–8 MB 的單行獨立成一次回應並帶 `more:true`。
- **tail 截斷**：超過 2 MB 時回傳最新的完整行區段，`start_offset` 為該區段首行行首，`more` 表示更早還有內容。
- **路徑驗證順序**：①`Stat`（不存在 → `file_missing`）②`EvalSymlinks` ③是否落在 `<home>/.claude/projects/` 下 ④一般檔且 `.jsonl`（③④失敗 → `no_transcript`）。
- **slug**：以實際 `~/.claude/projects/` 目錄名作 fixture 驗證（含底線、點、非 ASCII、連續符號）；provenance 的 transcript_path 優先，slug 僅 fallback。
- **WindowSize**：簽章 `WindowSize(ctx, target) (cols, rows, err)`，ctx 隨 WebSocket 關閉取消（查詢卡住也要停）；target 為 session 名，attach-session 的 client 看的就是該 session 的 current window，故查 session 的 current window 即其實際畫面；interval 可注入（預設 1 s）。
- **relay 寫入**：window text frame 與 binary 輸出共用同一把 `writeMu`（提升到 HandleWebSocket 層級可見處），寫失敗與 batcher 一樣關 ptmx 喚醒兩條 goroutine。
- **Executor 介面**：`WindowSize` 加進 `tmux.Executor`，同步更新所有實作者（`RealExecutor`、`FakeExecutor`、其他測試 executor），編譯全綠為 task 完成條件。

## 修訂 2（R2 critic）

- **開檔**：驗證後以 projects 根目錄 fd 為起點逐層 `openat(O_NOFOLLOW)`（`O_DIRECTORY` 用於中介層），任一層被換成 symlink 即拒絕（`no_transcript`）；開啟後 `fstat` 必為一般檔。威脅模型：hook 提供的路徑不可信，不能藉 check→open 之間的置換（含中介目錄）讀到 projects 外的檔案。projects 根目錄本身以 `EvalSymlinks` 解析一次作為信任錨。
- **未完成尾行**：`completeEnd` 的反向掃描以 `MaxLineBytes` 為界，超過回 413 `line_too_large`。
- **已知不處理（開 issue）**：同檔名被外部替換（rename 後同名新檔）不觸發 reset——Claude Code 的輪替一律換 session id＝新檔名；若要防禦需加 inode 型 generation（client 需回傳）。

## 修訂 3（purdex-ios 實測：mirror 是唯一 client 時仍縮 window）

- **事實**（tmux 3.6a 實測）：`-f ignore-size` 只在還有其他 client 時有效；mirror 是**唯一** client 時 tmux 照樣以它的大小決定 window。連「PTY 剛好等於 window 大小」也會縮一行：150x44 的 client → 150x43 的 window，因為 client 要留 status bar 的行數；client 為 window 高度＋status 行數（150x45）才不動。原 spec 的「client 的 resize 只改自己的 PTY」在無其他 client 時不成立。
- **改動**：mirror 的 PTY 與 window 綁定：①連線前先查 window 大小，PTY 以「欄＝window 寬、列＝window 高＋status 行數」啟動；②**忽略** client 送來的 `resize`（不改 PTY）；③輪詢每次重新套用（window 或 status 變了就 `Setsize`，即使 window 大小沒變）；④查不到大小就拒絕連線（close 1011），不以猜的大小啟動。
- **status 行數**：`#{status}` 為 `off`→0、`on`→1、`2`..`5`→該數。新增 `Executor.StatusRows`。
- **對 client 的影響**：client 不必（也不該）再送 `resize`；以 window text frame 的 cols/rows 渲染。送了也會被忽略，不影響桌機。
- **驗證**：真 tmux（私有 server）端到端測試：唯一 mirror client 送 83x55 的 resize，window 仍為 150x44；拿掉串接則為 83x54（即回報的現象）。
