# AGENTS.md

本檔提供本 repo 的代理執行規範。若與一般預設行為衝突，以本檔為準。

## 回應風格

- 直接、具體、冷靜。
- 優先用自然語言，不用 AI 腔調。
- 回答要先講結論，再補必要細節。
- 不要用誇張轉折、表演式對比、或追問式收尾。
- 不要預設加上「如果你要我可以再...」這類 follow-up bait。
- 不確定就直接說不確定，區分確認事實與推論。
- 使用繁體中文。

## 專案概述

**Purdex** — tmux session 的遠端管理工具，含 Go daemon + React SPA + Electron shell。支援 Terminal、Stream（Claude Code `-p` 串流）、JSONL 三種模式。（原名 tmux-box，2026-04 更名）

- Repo: `git@github.com:wake/purdex.git`
- 主分支: `main`（`v0` 為備份分支）
- 版本: `VERSION` 是 SOT；bump 時必須同步 `package.json` 與 `spa/package.json`

## 專案結構

- `cmd/pdx`：Go 入口，負責 CLI、daemon 與 setup 流程
- `internal/`：共用邏輯，依模組拆成 `module/`、`core/`、`store/`、`relay/` 等
- `spa/`：Vite + React 19 前端，主要程式在 `spa/src`
- `electron/`：桌面殼與 IPC 層
- `dist/`：打包輸出
- `build/`：圖示與安裝資源
- `scripts/`：建置腳本

## 開發環境

- package manager：`pnpm`
- daemon：`100.64.0.2:7860`（Go binary `bin/pdx`）
- SPA：`100.64.0.2:5174`
- 前端測試：`cd spa && npx vitest run`
- 前端 lint：`cd spa && pnpm run lint`
- 前端 build：`cd spa && pnpm run build`

## 技術棧

- daemon：Go / `net/http` / `gorilla/websocket` / `creack/pty` / `modernc.org/sqlite`
- SPA：React 19 / Vite 8 / Zustand 5 / Tailwind 4 / Vitest / Phosphor Icons / xterm.js 6
- Electron：electron-vite / electron-builder / `contextBridge`

## 建置與測試

### 根目錄

- `make build`：編譯 Go CLI 到 `bin/pdx`
- `make test`：執行全部 Go 測試，含 `-race`
- `make lint`：執行 `go vet`

### 前端

- `pnpm --prefix spa run dev`
- `pnpm --prefix spa run build`
- `pnpm --prefix spa run lint`

### Electron

- `pnpm run electron:dev`
- `pnpm run electron:build`
- `pnpm --prefix electron test`

## 程式風格

- Go 依 `gofmt` 標準，提交前跑 `gofmt ./...`
- TypeScript/TSX 使用 2 spaces、ES modules、單引號，遵守 `spa/eslint.config.js`
- React 元件與 stores 採 `PascalCase` / `useXxx`
- 工具函式檔名用 `kebab-case` 或語意清楚命名
- 圖示統一使用 Phosphor Icons

## 打包與更新

- Electron 打包：`pnpm run electron:build` → `dist/mac/` 與 `dist/mac-arm64/`
- SPA 更新：`.app` 啟動時若可達 Mini dev server 就 `loadURL`，否則 fallback 到 bundled renderer
- Electron 更新：daemon `/api/dev/update/check` + `/api/dev/update/download`
- 跨機開發：Mini 編譯，Air 執行 `.app`

### Dev Update 注意事項

- `/check` 用 `git log` 取最新 source commit hash，`/download` 打包 `out/` 產物
- source 改動後若要讓 Electron main/preload 生效，必須重新跑 `pnpm run electron:build`
- SPA 走 HMR，不受 baked-in hash 影響

## 開發流程

- 絕對不能直推 `main`
- TDD：先寫 failing tests，再實作
- 每個 task 獨立 commit
- 每個 PR merge 後，必須更新 `VERSION` + `CHANGELOG.md`
- 若使用 worktree，從最新 `origin/main` 開新 branch/worktree，不重用舊 feature branch 當實作基線

## Review 規範

### 強制委派

- **禁止代理自己進行最終 code review。**
- 任何使用者要求的 review、PR review、兩輪 review、pre-merge review，都**必須**委派 subagent 執行。
- 主代理可以做前置整理、收斂 review 範圍、彙整結果與提出修正決策，但**不得把自己的檢查當成正式 review 輸出**。

### 兩輪制

第一輪：
- 必須委派 1 個專職 review subagent 做主審。
- 目標：標準化 code review，涵蓋 CLAUDE/AGENTS 合規、bug scan、git history、PR comments、code comments。

第二輪：
- 必須平行委派 3 個 review subagent。
- 角色固定為：
- 攻擊方：找 bug / 安全漏洞 / 邊界情況
- 防守方：驗證設計合理性 / 架構一致性 / phase 邊界
- 檔案大小審查：檢查檔案過大、職責混雜、測試 seam 脆弱

### Review 完成後的強制輸出

- review 完成後，**必須直接提供一份比較表格給使用者**，不能只給散文摘要。
- 比較表格必須涵蓋以下全欄位，不可省略：

| 欄位 | 說明 |
|------|------|
| Review 輪次 | 第一輪 / 第二輪 |
| Reviewer | 主審 / 攻擊方 / 防守方 / 檔案大小審查 |
| Finding ID | 穩定識別碼，方便追蹤 |
| 檔案與行號 | 精確檔案位置 |
| 問題摘要 | 一句話描述問題 |
| 嚴重性 | high / medium / low |
| 嚴重性信心評分 | 對該問題成立的信心 |
| 關聯度 | 與當前 Phase 的相關程度 |
| 複雜度 | 修復成本 |
| 建議處置 | fix now / discuss / follow-up / accept risk |
| 決策 | 已修正 / 不修正 / 待討論 |
| 理由 | 為何做出該決策 |

- 若沒有 findings，仍**必須**提供同格式表格，並在 `問題摘要` 標示 `no finding`。

### 問題處理原則

- 優先處理高關聯、高信心、低複雜項目，三者採聯集不是交集
- 需要討論的項目先停下討論，再決定是否修
- 當下不修的問題要建立 issue 追蹤

## Issue 管理

### Labels

| 維度 | Labels | 規則 |
|------|--------|------|
| Type（必選一） | `bug` `feature` `refactor` `perf` `test` `chore` | 每個 issue 恰好一個 |
| Scope（可多選） | `daemon` `spa` `electron` | 跨元件可多選 |

### Milestones

- 活躍 phase 建 milestone，完成後 close
- 其他放 `Backlog`
- 不回溯建已完成 phase 的 milestone

## 安全與設定

- 不提交 token、`~/.config/pdx` 內容或本機資料庫
- 測試與開發優先使用假資料與 repo 內設定
- `dist/` 是輸出物，除非明確要釋出產物，勿在其上手改

## 備註

- `origin/main` 目前沒有既存 `AGENTS.md`；本檔即為將 `CLAUDE.md` 規範與 repo agent 規則整合後的版本
