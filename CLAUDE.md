# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## 專案概述

**Purdex** — tmux session 的遠端管理工具，含 Go daemon + React SPA + Electron shell。支援 Terminal、Stream（Claude Code `-p` 串流）、JSONL 三種模式。（原名 tmux-box，2026-04 更名）

- Repo: `git@github.com:wake/purdex.git`
- 主分支: `main`（v0 備份在 `v0` 分支）
- 版本: `VERSION` 檔案為 SOT，bump 時須同步 `package.json` + `spa/package.json`

## 開發環境

- **Package manager**: pnpm（不是 npm）
- **Daemon**: `100.64.0.2:7860`（Go binary `bin/pdx`）
- **SPA**: `100.64.0.2:5174`（`spa/`）
- **測試**: `cd spa && npx vitest run`
- **Lint**: `cd spa && pnpm run lint`
- **Build**: `cd spa && pnpm run build`

## Peer addresses（跨主機 agent 訊息）

- **日常地址是 `<host>/<name>`**，`<name>` 就是 Claude Code 註冊表裡這個對話自己的名字，
  例如 `mlab/purdex-b0`。`pdx peers` 與 `pdx msg whoami` 會連同 ref 一起顯示成
  `<host>/<name> [<ref>]`。名字會變，所以這個形式**不保證終生不變**（要不變的形式看下面的 ref）。
- **`pdx msg send` 接受三種寫法**：
  - `pdx msg send mlab/purdex-b0 "..."` —— 日常型，不必引號。
  - `pdx msg send "mlab/purdex-b0 [q34psn]" "..."` —— 表格看到什麼就整串貼上。
    **括號形式的 name 會拿去跟 ref 核對**，對不上就**拒送**（`name_mismatch`），
    不是「以 ref 為準」照送 —— 那個 name 是給人看的檢查碼。含空白，一定要引號。
  - `pdx msg send mlab/_q34psn "..."` —— 精確型，**改名也不會失效**，跨時間交接就用這個。
  - `<host>/tmux:<tmux session 名>` 仍是位置型 fallback，但它跟著 tmux 名走，改名就失效。
- **ref 是 `_` 加 6 位 base36**（`^_[0-9a-z]{6}$`），由該對話的 sessionId 導出（純函數，
  resume 與 daemon 重啟都不變，改名也不變）。表格括號裡印的是**去掉底線**的 6 碼，
  當地址打時要把 `_` 補回去。agent 算不出自己的 ref（拿不到 sessionId），只能問 `pdx msg whoami`。
- **name 要通過 routable 規則才能當地址**：`^[a-z0-9][a-z0-9-]{1,63}$`，且**不得剛好是 6 碼
  base36**（否則會遮蔽別人的 ref）。不合格的 name 照樣顯示，那一列也照樣送得到，
  只是只能用 ref 定址 —— `address` 本身就會印成 ref 形式。`reason` 不記這件事：
  那個欄位講的是「為什麼送不到」，而這一列送得到。
- **`title` 取代了舊的 `label`**：自由文字，≤64 bytes、可列印 UTF-8、無控制字元，**沒有保留字**
  （`cc`、`tmux` 不再被擋），而且**完全不參與定址** —— 送訊時永遠不會被解析成收件人。
- 指令：`pdx msg name <title>`（給自己取名）、`pdx msg name --release`（清掉）、
  `pdx msg whoami`（看自己的 address／ref／title；未設 title 時印 `title: (none)`）、
  `pdx msg send <address> "<text>"`；`pdx peers --all` 看所有主機的 session。
- **被賦予角色時**：`pdx msg name <專案>-<角色>`，例如 `pdx msg name purdex-tester`。
- **回應帶 `title_in_use` 時**：title 已經設好了（那是 warning 不是拒絕），但請照慣例加序號重設一次 ——
  看回應的 `live_titles` 挑下一個沒被用的序號，`pdx msg name purdex-tester-2`。
  這是慣例不是限制：重複的 title 完全能運作，只是兩個同名的 agent 在 `pdx peers` 上分不出誰是誰。
- **title 只給人和 agent 讀，用來「挑」要跟誰講話；送得到的是 address。**
  送訊一律用 address（`pdx msg whoami` 看自己的、`pdx peers --all` 看別人的）。
  被要求「成為 X」時就是：`pdx msg name X` → `pdx msg whoami` → **回報那個 address**，不是回報 X。
- **`pdx: command not found` 時**：這台機器的 daemon 是 App 裝的，執行檔在 `~/.config/pdx/bin/pdx`，但安裝流程不會把它放上 PATH。跑 `~/.config/pdx/bin/pdx path`（開發機則是 repo 的 `bin/pdx`）看診斷，它會告訴你該用 `path link`（建 `~/.local/bin/pdx`）還是 `path add-to-shell`（把 `~/.local/bin` 加進 shell 設定）。兩個指令在 Settings → Development 也有按鈕。

## 技術棧

- **Daemon**: Go / net/http / gorilla/websocket / creack/pty / modernc.org/sqlite
- **SPA**: React 19 / Vite 8 / Zustand 5 / Tailwind 4 / Vitest / Phosphor Icons / xterm.js 6
- **Electron**: electron-vite / electron-builder / contextBridge IPC
- **Icon 圖示**: 統一使用 Phosphor Icons

## 打包與更新

- **Electron 打包**：`pnpm run electron:build` → `dist/mac/`（x64）+ `dist/mac-arm64/`（ARM）
- **SPA 更新**：`.app` 啟動時偵測 Mini dev server，可達則 `loadURL`（HMR 即時），不可達則 fallback 到 bundled renderer
- **Electron 更新**：daemon `/api/dev/update/check` + `/api/dev/update/download`，Settings → Development 頁面操作（需 `PDX_DEV_MODE=1`）
- **跨機開發**：Mini（100.64.0.2）編譯，Air 執行 `.app`，SPA 改動即時生效，Electron 改動透過 dev update 機制

### Dev Update 注意事項

- **check 與 download 的來源不同**：`/check` 用 `git log` 取源碼最新 commit hash，`/download` 打包 `out/` 目錄的建置產出
- **改動後必須重新打包**：push 新 commit 後須在 Mini 跑 `pnpm run electron:build`，否則 `out/` 裡的 baked-in hash 是舊的，Air 端會無限顯示 "Update available"
- **SPA 走 HMR 不受影響**：dev server 跑著時 SPA 改動即時生效，但 Electron main/preload 改動仍需打包 + dev update

## 完整開發流程

**除非使用者授權，否則不能直推 main**，即使 hotfix 也必須走 PR + review
**TDD：先寫測試再實作**
**每個 task 獨立 commit**

1. 依照需求 / 請求提出建議方案，並且 enter worktree，以下都在 tree 中進行
2. 依據討論完成方案撰寫 spec，必須按照合適 review 大小切分 phase
3. 委派 codex 審閱 spec
4. 依據定稿的 spec 撰寫 plan
5. 委派 codex 審閱 plan
6. 依據 plan 使用自己的 subagent 進行開發
7.  PR & 委派 codex 兩輪深度 review
8. 確認完成後進行 PR merge，完成後清理 worktree 並關閉
9. 獨立一個 bump PR 以更新 `VERSION` + `CHANGELOG.md` 並 merge
10. 更新 main branch 對齊 origin/main

### PR Review 兩輪制 (委派 Codex 進行)

**第一輪：標準 code review（跨模型差異化檢查）**

**第二輪：3 個 parallel**
- 攻擊方：找 bug / 安全漏洞 / race / 邊界條件
- 防守方：驗證設計合理性 / 架構一致性 / API 邊界
- 檔案體質：過大檔案 / SRP 違反 / 職責不清

輪詢 `/codex:status` → `/codex:result <job-id>` 讀回 4 份輸出。

Focus text 越具體越好（指定檔案 / 具體風險點 / 設計疑問）。全域 CLAUDE.md 載明 Skill 設計意圖與 companion script 啟動路徑。

### Review 問題彙整

兩輪跑完後，提交所有問題項目的彙整表格，每個項目必須包含：

| 欄位 | 說明 |
|------|------|
| 嚴重性信心評分 | 對該問題確實是 bug / 設計缺陷的信心程度 |
| 關聯度 | 與當前開發階段的相關程度 |
| 複雜度 | 修復所需的工作量 |

優先處理原則（聯集，非交集）：
- **高關聯**：與當前 Phase 直接相關的問題
- **高信心**：確定是真正問題而非誤報的項目
- **低複雜**：修復成本低、可快速解決的項目

只有低關聯 + 中高複雜可以延後，其他統一優先處理。需要討論的項目先討論完再修。當下不修的問題建立 `gh issue` 追蹤。

### Issue 管理

**Labels — 兩個維度**

| 維度 | Labels | 規則 |
|------|--------|------|
| Type（必選一，互斥） | `bug` `feature` `refactor` `perf` `test` `chore` | 每個 issue 恰好一個 |
| Scope（選填，可多選） | `daemon` `spa` `electron` | 跨元件的 issue 標多個 |

**Milestones — 管時程**

- 活躍開發的 phase 建 milestone（如 `Phase 5b`），完成後 close
- 其餘放 `Backlog`，開工時再移入對應 milestone
- 不回溯建已完成 phase 的 milestone
