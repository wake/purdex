# Purdex Project Hub & Issue Orchestration（大改版）

> 一句話：把 Purdex 從「tmux session 管理工具」升級成「以專案為中心的開發控制面」——
> 從 repo 清單一鍵啟動工作環境，到看板拖曳自動觸發 issue 執行佇列。

## Problem Statement

**How might we** 讓 Purdex 成為跨平台（Gitea + GitHub）、跨主機（air + mlab）的單一開發控制面，
使「選專案 → 啟環境 → 認 tab → 看板驅動執行」成為一條順暢的工作流，而不是散落在多個工具與終端機之間？

## 背景現狀（盤點結論）

| 面向 | 現狀 | 缺口 |
|------|------|------|
| Session 組織 | 按 Host 分組（`sessions: Record<hostId, Session[]>`） | 無 project 層；無 repo 清單 |
| Tab 模型 | Workspace 內平面 list；支援 pin/lock/split；動態 title+icon+燈號 | 上方 tab 跨專案混雜難識別；無分組維度 |
| Session 建立 API | `POST /api/sessions` 支援 `cwd` + `mode` | **不支援初始啟動命令**（建立後空白） |
| Quick Commands | v2 Phase 1 data model 已 ship（global + per-host + slot binding） | 無「命令模板」概念；無 project 綁定 |
| Git/Repo 整合 | 無；僅 fs.search 預留 `workspace-projectPath` | 全新領域 |
| Issue 管理 | 無內建；散落 Gitea + GitHub 原生介面 | 無聚合看板；無 session 連動；無執行佇列 |
| JSONL 模式 | 互動模式於 `09cf8b1a`(2026-04-02, PR #151) **移除**；僅保留 `ParseJSONL` 歷史解析 | 需復活為可對話的獨立模式 |
| 資料儲存 | daemon `~/.config/pdx/*.db`（SQLite, modernc） | 看板需新 db + migration + 備份 |

## Recommended Direction

採 **以 Project 為中心的四層重構**，分四條支線推進，第一條先動工：

**核心模型轉變**：引入 `Project` 一級實體，作為 repo ↔ 多主機路徑 ↔ 啟動模板 ↔ issue 來源 的錨點。
Project 不取代既有 Workspace，而是新增的組織維度；左側導航 by project，上方 tab 可跨 project 聚合。

四條支線：

1. **① Project Hub + 一鍵啟動**（首個動工）
   - Gitea + GitHub repo 清單聚合（唯讀拉取）
   - 慣例目錄 clone 偵測（`~/Workspace/{org}/{repo}`）
   - **repo → 多主機路徑映射**（一個 repo 在 air / mlab 可有不同 path）
   - 一鍵啟動：選 repo + 選主機 + 選**命令模板** → daemon 在該 path 起 tmux session + 注入啟動命令
   - 需擴充 `POST /api/sessions` 支援初始命令注入（複用既有 `send-keys` 基礎設施）

2. **② 雙層 Tab 識別**
   - **左側**：by project/workspace 的 1:1 樹狀總覽（一次看到所有 tab）
   - **上方**：作業情境（working set）的 tab 列，**可跨專案**，視覺上以 project 色標/角標區分來源
   - 待決：作業情境是否沿用 Workspace 命名或獨立新詞（open question）

3. **③ 聯邦看板 + 執行佇列**（B+ 架構，終極目標）
   - **issue 內容 SOT 留 Gitea/GitHub**，Purdex 唯讀聚合 + 回寫 comment/close
   - **欄位狀態 / 佇列順序 / 執行記錄存 Purdex**（新 SQLite db）
   - 儲存層抽象為 provider interface（含 GitHub/Gitea/local provider），預留 C' 升級路
   - **拖曳到 todo → 自動 enqueue → 啟動環境 + 執行任務 → 結果回報到 issue comment**
   - 執行佇列建議長在既有 codex broker dispatcher / launch registry 體系之上

4. **④ JSONL 互動模式復活**
   - 從 `09cf8b1a` 前的 commit 取回設計（JournalWatcher 監聽 + send-keys 注入）
   - 可對話（非僅檢視）：tmux 內跑 CC TUI，前端結構化渲染 + send-keys 注入訊息
   - 復用現有 `ParseJSONL`（`internal/history/history.go`）+ ConversationView 元件

## Key Assumptions to Validate

- [ ] **儲存留存破例**：看板欄位狀態是不可重建的真實資料，必須做 schema migration + 備份，
      違反 `feedback_no_alpha_migration`（僅適用衍生狀態）。→ spec 中明確標記；db 損失上界 = 只丟工作流狀態，issue 本體不丟。
- [ ] **多主機路徑映射可由 daemon 持有**：一個 Project 對應 `{air: path, mlab: path}`，
      啟動時依目標主機解析。→ 驗證跨主機 session 建立 + path 解析流程。
- [ ] **啟動命令注入可複用 send-keys**：`internal/tmux/executor.go:248` 已有 send-keys；
      session 建立後注入命令 vs. 建立時帶命令二選一。→ POC 驗證時序（避免 race）。
- [ ] **拖曳觸發在 daemon 內是本地事件**：B+ 下拖曳發生在 Purdex，直接 enqueue 零 webhook。
      → 確認看板狀態變更 → 佇列觸發的事件路徑。
- [ ] **Gitea + GitHub API 聚合可行**：兩平台 issue list/comment/close API 差異可被 provider 抽象吸收。
      → 各拉一個 repo 驗證 read + write 最小閉環。
- [ ] **JSONL 模式設計仍適用**：`09cf8b1a` 前的 JournalWatcher 設計從未實作（僅 spec），
      需確認 fsnotify 監聽 + send-keys 注入在現行架構下可行。

## MVP Scope（① Project Hub + 一鍵啟動）

**In：**
- Project 一級實體 data model（id / name / repo source / 多主機路徑映射 / 命令模板 ref）
- Gitea + GitHub repo 清單拉取（唯讀，憑證複用既有 tea/gh 設定）
- clone 狀態偵測（慣例目錄 `~/Workspace/{org}/{repo}` 存在性檢查）
- 命令模板（全域共用，Quick Commands 機制延伸）
- 一鍵啟動：選 repo + 主機 + 模板 → `POST /api/sessions`（擴充初始命令）→ 起 tmux + 注入
- Projects Settings 模組（掛 settings-contribution-registry）

**Out（後續支線）：**
- 看板 / issue 聚合（③）
- 執行佇列自動觸發（③ 終極目標）
- 雙層 tab 視覺重構（②）
- JSONL 互動模式（④）

## Not Doing（and Why）

- **不自建 issue SOT（不走 C'）** —— B+ 已選定。issue 內容留原平台，保住 PR 連動 / gh CLI / 跨平台可見性；
  provider interface 預留升級，真要 C' 再加 local provider，不重寫。
- **不引入現成 kanban 服務（不走 C）** —— 多養一個服務 + 三方雙向同步 + 自動化隔兩層 webhook，
  對單人工具是最差複雜度交換；且斷開與 codex broker dispatcher 的整合。
- **不一次做完四條支線** —— 並發 session 風險（記憶 `feedback_concurrent_session_safety`）；
  ① 先行驗證 Project 模型，再逐條推進。
- **不在 ① 階段碰 Workspace 改名** —— 「作業情境命名」是 open question，留到 ② 才需定，避免提前 churn。
- **不在原平台 label 存欄位狀態** —— 工作流狀態是 orchestrator 私有營運狀態，
  寄存在別人家會綁死兩套 label 語意 + 偵測機制。

## Open Questions

- 上方 tab 的「作業情境」要沿用 Workspace 命名，還是獨立新詞（Desk / Context / Set）？→ ② 階段定。
- 命令模板 vs. repo 內慣例檔（`.purdex.json` / `package.json` scripts）：先做全域模板，
  是否之後支援讀 repo 慣例檔自動產生選項？→ ① 做模板，慣例檔列 ② 後評估。
- 多主機路徑映射的 SOT：存在 Project 實體裡（daemon db），還是 per-host 設定？→ spec 階段定。
- 執行結果「回報到 issue」的格式：comment 全文 vs. 摘要 + 連結？→ ③ 階段定。

## 建議下一步

進入 ① 的 spec-driven development：先就「Project 實體 + 多主機路徑映射 + 命令模板 + 一鍵啟動 API 擴充」
寫 spec，按 review 大小切 phase，委派 codex 審 spec → plan → TDD 實作。
