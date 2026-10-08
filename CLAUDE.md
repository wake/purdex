# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## 專案概述

**Purdex** — tmux session 的遠端管理工具，含 Go daemon + React SPA + Electron shell。支援 Terminal（tmux）與 exec（Nexen headless execution）兩種模式。（原名 tmux-box，2026-04 更名）

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

- **日常地址是 `<host>/<name>`**，`<name>` 是 pdx 替這個對話配的**虛擬名**（Peer Address v5）：
  `<基底>-<ref 前兩碼>`，例如 ref `_q34psn`、基底 `purdex-b0` → `mlab/purdex-b0-q3`。
  基底取這個對話**第一次**被 daemon 看到時的名字（當時的 CLI 名字；沒有就用記過的名字；
  執行體用工作目錄名），**配一次就終生不變**：`/rename`、Claude Code 每次啟動換名都不影響；
  接力（relay）後的新 session 沿用前任的名字（後綴不重算）；手動 `/clear` 是新對話，重新配。
  `pdx peers` 與 `pdx msg whoami` 會連同 ref 一起顯示成 `<host>/<name> [<ref>]`。
- **Claude Code 自己的 session 名字（CLI 名字）不是地址，不路由**：拿它送會回 `peer_not_found`；
  它若剛好是某個活著的對話的 CLI 名字，detail 會附 `did you mean <host>/<虛擬名>?`。
  地址一律問 `pdx msg whoami`（自己的）／`pdx peers --all`（別人的），不要從 CLI 名字推。
  例外：對方主機的 daemon 還是舊版（列沒有虛擬名）時，對它照舊用 CLI 名字定址。
- **`pdx msg send` 接受三種寫法**：
  - `pdx msg send mlab/purdex-b0-q3 "..."` —— 日常型，不必引號。
  - `pdx msg send "mlab/purdex-b0-q3 [q34psn]" "..."` —— 表格看到什麼就整串貼上。
    **括號形式的 name 會拿去跟 ref 核對**，對不上就**拒送**（`name_mismatch`），
    不是「以 ref 為準」照送 —— 那個 name 是給人看的檢查碼。含空白，一定要引號。
  - `pdx msg send mlab/_q34psn "..."` —— 精確型；兩個對話剛好配到同一個虛擬名
    （同名會回 `ambiguous`，附候選）時用它，跨時間交接用它也行。
  - `<host>/tmux:<tmux session 名>` 仍是位置型 fallback，但它跟著 tmux 名走，改名就失效。
- **ref 是 `_` 加 6 位 base36**（`^_[0-9a-z]{6}$`），由該對話的 sessionId 導出（純函數，
  resume 與 daemon 重啟都不變，改名也不變；接力後舊 ref 經 lineage 照樣送得到）。表格括號裡印的是
  **去掉底線**的 6 碼，當地址打時要把 `_` 補回去。agent 算不出自己的 ref（拿不到 sessionId），只能問 `pdx msg whoami`。
- **虛擬名一定通過 routable 規則**：`^[a-z0-9][a-z0-9-]{1,63}$`，且**不得剛好是 6 碼 base36**
  （否則會遮蔽別人的 ref）。取不到合格的基底（例如 CLI 名字含大寫或符號）就**不配名**，那一列照樣送得到，
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
2. 依據討論完成方案撰寫 spec，按 phase 切分；**切分尺度：一個 phase ＝ 一個 PR ≤ 800 行 diff 或 ≤ 20 檔**，超過再拆
3. spec **預設不單獨派 codex review**；只有 phase 切分或介面契約有爭議時才單獨審一輪
4. 依據定稿的 spec 撰寫 plan
5. 委派 codex 審閱 plan，**prompt 附上 spec 路徑一起審**（spec 與 plan 合併為一輪）
6. 依據 plan 使用自己的 subagent 進行開發
7. PR & 委派 codex 兩輪 review（見下節）；修完 review 問題後**只 re-review 增量**，不重跑整支 branch
8. 確認完成後進行 PR merge，完成後清理 worktree 並關閉
9. 獨立一個 bump PR 以更新 `VERSION` + `CHANGELOG.md` 並 merge；**bump PR 與純搬移 PR 不派 codex**（純搬移用逐宣告位元組比對證明）
10. 更新 main branch 對齊 origin/main

### PR Review 兩輪制 (委派 Codex 進行)

模型一律 `--model gpt-5.6-sol`、effort 維持 config 的 `low`（不要拉高，Pro 週配額燒很快）。

**第一輪 R1：標準 code review**（`/codex:review --base <ref>`，內建 reviewer，跨模型差異化檢查）

**第二輪 R2：「攻擊 → critic 反駁」串行兩次**（不再三平行；依據 ICML 2026 Adversarial Review：agent 數不是變因，「必須引用證據才能反對」才是）
1. **攻擊方** `/codex:adversarial-review`：找 bug / 安全漏洞 / race / 邊界條件；focus 末段附一句「另外列出過大檔案 / SRP 違反（低優先，獨立一節）」，原本的「檔案體質」視角併進來
2. **critic 反駁方** `/codex:adversarial-review`：focus 內嵌 R1 ＋ 攻擊方的 findings 清單與 spec 路徑，要求**逐條**判定「同意／有證據反對（必引 file:line）／疑慮」，**禁止新增沒有證據的 finding**。critic 同時是 spec drift 防線（原「防守方」的職責）
3. 攻擊方與 critic 對**同一個 critical** 互不同意時，才用 `--model gpt-6-astra` 派一次仲裁；其他情況不用 astra

兩段 focus 模板在 `~/.claude/skills/codex-dispatch/SKILL.md`。輪詢 `/codex:status` → `/codex:result <job-id>` 讀回 3 份輸出。Focus text 越具體越好（指定檔案 / 具體風險點 / 設計疑問）。

**增量 re-review**：修完問題後，修正未 commit → `--scope working-tree`；已 commit → `--base <上一輪審過的 sha>`。re-review 只回 Important 以上，不回 nit。

**停止條件**：R1 無 critical / P1，且 critic 對剩餘 findings 沒有「有證據的反對」→ 直接 ship。第三輪只在第二輪**新出現** critical 時才跑。

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

**confidence 門檻**：codex 回傳的 `confidence`（adversarial 0–1 / 內建 reviewer `confidence_score`）**< 0.6 不進主表**，另列「待驗證」區；主 Claude 先用測試或重現驗證，證實才升進主表，證偽就丟。沒有 confidence 欄位的輸出視為 0.6 照常進表。

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

## Tab-hosted 元件檢查清單

- [ ] 任何新的 tab-hosted 元件：必須跨 tab 切換存活的 state（草稿文字、捲動位置、選取範圍）— 以 unmount/remount 測試。`useTabAlivePool` 預設 `keepAliveCount: 0`，且 `lib/pane-weight.ts` 的 light 白名單之外的 pane（含 execution / worker）切走就會 unmount，元件內 `useState` 會歸零；state 要放在元件外（例：`lib/nex/worker-draft-memory.ts`、`transcript-scroll-memory.ts`），並用真的 `TabContent` 寫切走再切回的回歸測試（範例：`components/execution/ExecutionView.tab-switch.test.tsx`）。
