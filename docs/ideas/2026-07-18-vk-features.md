# Vibe Kanban 功能盤點參照文件

> 爬取時間：2026-07-18。資料來源：官方文件 `vibekanban.com/docs`（Mintlify，`vibekanban.mintlify.dev` 為同內容鏡像）、GitHub `BloopAI/vibe-kanban` README、DeepWiki（`deepwiki.com/BloopAI/vibe-kanban`）、官方 shutdown 公告。
> 用途：作為 Ploom（控制平面）+ Purdex（執行平面）功能落差評估的標尺文件。

---

## 0. 重大現況提醒：Vibe Kanban 正在 Sunset（務必納入評估考量）

- 母公司 **bloop 已宣布關閉**，公告見 <https://www.vibekanban.com/blog/shutdown>（發布於 2026-04-10）。
- **Vibe Kanban 專案本身不會消失**：後續轉為 **open source、社群維護**（Apache 2.0），團隊承諾「未來幾週內」發布社群維運版路線圖。
- **Cloud（遠端/多人協作）服務**：公告日起 30 天內仍可用，之後**永久關閉**——包含 kanban issues、comments、projects、organizations 等雲端多人協作功能全部停止。訂閱已終止、30 天內付款已退款。
- **Local（本機單機）架構**：不受影響，將成為專案往後的**主要形態**——即回到最初「單機 SQLite + `npx vibe-kanban`」的模式。
- 關閉原因：日活躍用戶數千人，但絕大多數是免費用戶，找不到能支撐的商業模式。
- **對評估的意涵**：VK 的「多人協作 / Cloud（組織、看板即時同步、team members）」這條產品線本身正在被官方放棄，不是本來就沒做而是做了又收回。若 Ploom 要做團隊協作看板，功能對標基準應以 **VK Local（單機）** 為主，Cloud 部分僅供「參考別人怎麼設計過」而非「這是業界現役標準」。

來源：<https://www.vibekanban.com/blog/shutdown>、GitHub README（`https://github.com/BloopAI/vibe-kanban`）。

---

## 1. 產品定位總覽

- 核心主張：「Plan and review the work of AI agents faster, ship more.」定位是**規劃與審查層**，本身不是 coding agent，而是協調 10+ 種 coding agent 的協調層（coordination layer）。
- 三步工作流程：用 kanban issue 描述工作 → 建立 workspace 讓 agent 執行 → 審查 diff → 出貨。
- 安裝：單一指令 `npx vibe-kanban`，需事先完成想用的 coding agent 認證。
- 技術棧：後端 Rust（Axum + SQLx），前端 TypeScript/React，本機用 SQLite，Cloud 用 PostgreSQL + ElectricSQL 做即時同步；型別透過 `ts-rs` 從 Rust struct 自動產生 TypeScript 型別。

來源：<https://vibekanban.com/docs/index.md>、DeepWiki Overview、GitHub README。

---

## 2. 規劃 / 看板（Issue Management / Kanban）

### 2.1 看板欄位與狀態
六個預設狀態：`Backlog`、`To do`、`In progress`、`In review`、`Done`、`Cancelled`。預設只顯示前四欄，`Backlog`/`Cancelled` 需切到「All」分頁才可見。
來源：<https://vibekanban.com/docs/issue-management.md>、<https://vibekanban.com/docs/cloud/kanban-board.md>

### 2.2 建立 Issue
- 兩種入口：欄位標題上的 **+** 按鈕、篩選列的 **New Issue** 按鈕。
- 描述支援富文本（粗體、斜體、清單、行內程式碼、markdown 標題、連結），因為**描述文字會直接變成 agent 的 prompt**。
- Cloud 版可在建立時勾選「Create draft workspace immediately」，issue 建立同時直接掛上一個 workspace 開始執行。
來源：<https://vibekanban.com/docs/issue-management.md>、<https://vibekanban.com/docs/cloud/issues.md>

### 2.3 優先序
四級：`Urgent`（紅）、`High`（橙）、`Medium`（黃）、`Low`（灰）。
來源：<https://vibekanban.com/docs/issue-management.md>

### 2.4 指派與 Tag
- 可指派給團隊成員（Cloud 支援多重 assignee）。
- Tag：專案層級標籤，用於分類與篩選；另有「Settings → General → Tags」的**文字片段模板 tag**（snake_case 命名），可在任何支援 `@mention` 的輸入框（workspace prompt、follow-up 訊息）用 `@` 呼出並插入預先寫好的內容（如 bug report 模板、驗收標準清單），為**全域跨專案**共用，非專案內分類 tag。兩種「tag」概念不同，文件本身也分開描述。
來源：<https://vibekanban.com/docs/issue-management.md>、<https://vibekanban.com/docs/settings/creating-task-tags.md>

### 2.5 排序
- Manual / Priority / Created / Updated / Title（含正逆序切換）。
- **只有 Manual 模式**才能拖拉排序卡片；其他排序模式下拖拉排序會被系統依規則覆蓋。
來源：<https://vibekanban.com/docs/cloud/filtering.md>、<https://vibekanban.com/docs/cloud/kanban-board.md>

### 2.6 子任務 / 關聯
- Sub-issue：大任務拆小任務，parent/child 關係，**子任務完成不會自動完成父任務**，且子任務不能再往下巢狀。
- Issue 關聯（透過 MCP `create_issue_relationship`）：`blocking`、`related`、`has_duplicate` 三種關係類型。
來源：<https://vibekanban.com/docs/issue-management.md>、MCP 工具列表（見第 5.2 節）

### 2.7 篩選 / 搜尋
- 標題模糊搜尋（大小寫不敏感、部分比對），也支援直接輸入 Simple ID（如 `TASK-123`）跳轉。
- 篩選維度：Priority、Assignee（多選 OR 邏輯）、Tags；跨維度為 AND 邏輯。
- 篩選狀態只在當次 session 保留，切換專案或登出即重置，屬個人化設定不影響團隊視野。
來源：<https://vibekanban.com/docs/cloud/filtering.md>

### 2.8 List View
表格式列表，可看到含隱藏狀態在內的**所有** issue；依狀態分組，每組顯示狀態色點、數量、可摺疊；用於批次操作與查找 backlog/cancelled 項目。
來源：<https://vibekanban.com/docs/cloud/list-view.md>

### 2.9 看板自訂（Cloud）
- 可新增/改名/改色/重排/隱藏欄位；空欄位可刪除（issue 需先移走）。
- **不支援**：swimlane、自訂欄位（custom fields）、工作流程自動化（workflow automation）——文件明確指出這些沒有涵蓋。
來源：<https://vibekanban.com/docs/cloud/customisation.md>、<https://vibekanban.com/docs/cloud/kanban-board.md>

### 2.10 Team / Personal 分頁
Team 分頁看專案所有 issue，Personal 分頁只看指派給自己的。
來源：<https://vibekanban.com/docs/issue-management.md>

---

## 3. 任務執行（Workspaces / Execution）

> 術語演進提醒：舊文件/README 用語是 **Task → Task Attempt**（一個 task 底下可以有多次 attempt，各自對應一個 git worktree）；目前文件用語已改為 **Issue → Workspace → Session**（一個 workspace 對應一個 worktree，底下可開多個 session=多次對話嘗試）。概念上是同一機制的重新命名/擴充，本文以現行「Workspace/Session」用語為主，但保留舊稱以利對照。

### 3.1 Workspace 是什麼
「一個完成單一 coding task 的隔離環境」，類比為專屬「project room」。可在其中與 agent 對話、審查變更、透過內建瀏覽器測試應用、準備 PR。
來源：<https://vibekanban.com/docs/workspaces/index.md>

### 3.2 建立 Workspace（機制細節）
步驟：
1. 側邊欄 **+** 或指令列（`Cmd/Ctrl+K`）開啟建立畫面
2. 選 Project（可即時新建）
3. 加入 Repo（可選最近使用 / 瀏覽磁碟 / 新建），**一個 workspace 可掛多個 repo**
4. 為每個 repo 指定 target branch（合併目的地，如 `main`）
5. 描述任務
6. 選擇 coding agent
7. 建立

背後動作：
- **Git worktree 建立**：獨立工作目錄，不影響原始 repo/其他 workspace
- **Working branch 自動產生**：如 `vk/abc123-task-name`（依任務描述+分支前綴設定自動命名）
- Agent 初始化，準備接收指令
- 若專案設有 **setup script**（如 `npm install`）會自動執行一次

Target branch（合併目的地）與 working branch（實際變更發生處）是兩個不同概念。
來源：<https://vibekanban.com/docs/workspaces/creating-workspaces.md>

### 3.3 隔離安全機制
「nothing is pushed to remote until you explicitly create a PR」——worktree+branch 隔離原始 repo，遠端推送需要顯式動作才會發生。
來源：<https://vibekanban.com/docs/workspaces/index.md>

### 3.4 Session（原 Task Attempt 概念的現行實作）
- 一個 session = 「與 coding agent 的單一對話」，各自獨立對話歷史，但**共享同一份 workspace 檔案**。
- 同一 workspace 下可開**多個並行 session**（例如同時跑不同 agent、嘗試不同做法）。
- **檔案共享但 context 不共享**："Sessions share files but not conversation context"。
- **多 session 寫入衝突**：「When multiple sessions modify the same files, the last write wins」——沒有鎖定/合併機制，靠使用者自己盯 Changes 面板。
- **沒有傳統「retry/rerun」按鈕**：想重跑/換做法就是**開新 session**，新 session 拿到全新 context window，但仍存取同一批檔案。文件明確說沒有沿用舊「task attempt」用語，session 已取代之作為主要組織單位。
- 建議何時開新 session：對話快到 token 上限時、想嘗試不同解法時、想平行處理獨立子問題時。
來源：<https://vibekanban.com/docs/workspaces/sessions.md>

### 3.5 Workspace 生命週期管理
| 狀態 | 說明 |
|---|---|
| Active | 預設狀態，出現在主側邊欄 |
| Archived | 保留所有資料（對話歷史、session、備註、worktree 檔案），可隨時 unarchive；worktree 保留在磁碟上 |
| Deleted | **不可逆**；移除 workspace 資料與磁碟上的 worktree 副本，但**分支保留在 repo 內、所有 commit 都保留**，原始 repo 不受影響 |

Worktree 清理：worktree 放在平台專屬暫存目錄（macOS temp、Linux `/var/tmp`、Windows `%TEMP%`）；**啟動時自動清掉孤兒 worktree**（資料庫內找不到對應紀錄的）；可在設定中自訂儲存位置。
來源：<https://vibekanban.com/docs/workspaces/managing-workspaces.md>

### 3.6 多 Repo Workspace
- 一個 workspace 可掛多個 repo，各自維持**獨立 git 狀態**（獨立 target branch、獨立 working branch、獨立 commit 歷史、獨立 staged/unstaged 變更）。
- 提供**跨 repo 統一 diff 視圖**。
- 典型場景：全端功能（前後端同時改）、跨 repo 重構、monorepo 風格開發。
- 限制：多 session 改同檔案「last write wins」；長對話 token 消耗、多平行 session 協調容易「confusing」。
- 移除 repo 不會刪任何程式碼或分支，只是從當前 workspace 移除關聯。
來源：<https://vibekanban.com/docs/workspaces/multi-repo-sessions.md>、<https://vibekanban.com/docs/workspaces/repositories.md>

### 3.7 Git 操作（Workspace 內建）
- 分支管理：查看 working/target branch 狀態，切換 target branch
- PR：建立（含 AI 生成描述）、支援 draft PR、多 repo 時各自建 PR
- Rebase：把 target branch 變更拉進來更新 working branch，衝突可手動解決或 abort
- Merge：把 target 拉進 working branch，行為落後 target 時會提示先 rebase
- Push：有未推送 commit 時才出現按鈕
- 狀態顯示：未 commit 變更數、ahead/behind、衝突指示、repo 名稱與分支資訊
- Commit：agent 工作過程中**自動 commit**，也可透過內建終端機手動 commit
來源：<https://vibekanban.com/docs/workspaces/git-operations.md>

### 3.8 Agent 執行抽象層（Executor System，後端架構）
- 統一介面 **`StandardCodingAgentExecutor` trait**（用 `enum_dispatch` 避免動態分派開銷），支援 10+ 種 agent。
- 關鍵方法：
  - `spawn()` / `spawn_follow_up()`：啟動 agent 子行程；後者支援 session 續接
  - `apply_overrides()`：套用設定覆寫
  - `normalize_logs()`：把各 agent 原始輸出轉成統一的 `NormalizedEntry`（`AssistantMessage`、`ToolUse`、`ErrorMessage`、`Thinking` 等），即時透過 WebSocket JSON patch 推到前端
  - `get_availability_info()`：偵測是否已安裝/已認證
- `SpawnedChild` 結構含 `exit_signal`（oneshot channel，給自行管理生命週期的 agent 用）與 `CancellationToken`（可在 process 真正死亡前優雅終止 approval loop）。
- 內部子章節：3.1 trait 架構、3.2 profile/設定疊加、3.3 各 agent 實作、3.4 log normalize、3.5 Approval 系統（`ExecutorApprovalService` 管工具使用時的核准/暫停）。
來源：DeepWiki `3-executor-system`

### 3.9 支援的 Agent / Provider 清單（10 種）
1. **Claude Code** — `npx -y @anthropic-ai/claude-code` 完成登入流程後即可在 VK 選用
2. **OpenAI Codex** — `npx -y @openai/codex`；支援互動登入或 `OPENAI_API_KEY` 環境變數走 API key；`CODEX_HOME` 可指定自訂設定目錄（分工作/個人 profile）
3. **GitHub Copilot CLI** — `npx -y @github/copilot`，`/login` 認證
4. **Gemini CLI**
5. **Amp**
6. **Cursor Agent CLI**
7. **OpenCode**（SST）
8. **Factory Droid**
9. **Claude Code Router (CCR)** — 非官方第三方路由層，明確聲明「not affiliated with... Anthropic」；可依任務類型（Default/Background/Think/LongContext/WebSearch/Image）路由到不同 provider/model（如 `openrouter,moonshotai/kimi-k2-0905`），在 agent 設定內用 checkbox 開關
10. **Qwen Code**

各 agent 都是「先各自完成 CLI 認證，再啟動 `npx vibe-kanban`，建立 workspace 時即可從下拉選單選用」的統一模式；VK 本身不管理 agent 的訂閱/帳號，完全依賴各 agent CLI 自己的認證機制。
來源：<https://vibekanban.com/docs/supported-coding-agents.md>、各 `docs/agents/*.md`

### 3.10 Agent Profile / 設定（Settings → Agents）
- Profile = 可重複使用的 agent 行為設定組合（plan mode、model、permission 等級）。
- 通用參數：`append_prompt`（附加到 system instruction 的自訂文字）、環境變數（可覆寫 shell 設定，用於串接第三方 provider 如 Z.ai、OpenRouter，同時保留獨立憑證）
- Agent 專屬參數舉例：
  - Claude Code：planning mode、router 支援平行多實例、permission skip
  - Codex：sandbox 層級（read-only 到完全存取）、approval 門檻、reasoning 深度/摘要
  - Droid：自主程度、model 選擇、reasoning 強度
  - Cursor：force execution、model 指定
  - Gemini：model 版本（default/flash）、autonomous mode 切換
- Approval 層級：`untrusted`（每步都問）/`on-failure`/`on-request`/`never`
- 管理：Settings→Agents 兩欄式 finder 佈局，可新建/複製/命名（如 PRODUCTION/DEVELOPMENT）、設一個為預設、刪除（每個 agent 類型至少留一個 profile）
來源：<https://vibekanban.com/docs/settings/agent-configurations.md>

### 3.11 Repo 層自動化腳本
每個 repo 可設定三種腳本：
- **Setup script**：workspace 建立時執行一次（如 `npm install`）
- **Dev server script**：啟動預覽伺服器，**必須把 URL 印到 stdout**（如 `http://localhost:3000`）VK 才能偵測並顯示在 Preview 面板；常見框架指令舉例已列（Vite/Next `npm run dev`、CRA `npm start`、Django `python manage.py runserver`、Rails `rails server`）
- **Cleanup script**：workspace 關閉時執行；文件強調**必須 idempotent**（用 `|| true` 容錯），用途含停容器/背景行程、清產物、跑格式化工具（Prettier/ESLint/cargo fmt/Black/Ruff）
來源：<https://vibekanban.com/docs/settings/projects-repositories.md>

---

## 4. 審查 / 迴圈（Review Loop）

### 4.1 Changes 面板（Diff 檢視）
- 檔案樹：展開/摺疊、搜尋檔名、全展開/全摺疊
- Diff 顯示：綠=新增、紅=刪除、灰=不變 context，含原始/新行號
- 兩種模式：Unified（適合快速掃）、Side-by-side（適合大重構）
- 客製選項（透過指令列）：換行、是否顯示純空白變更、展開/摺疊全部
- 明確強調文件內原話：「Never blindly trust agent output. Always review changes before merging.」
來源：<https://vibekanban.com/docs/workspaces/changes.md>、<https://vibekanban.com/docs/reviewing-code.md>

### 4.2 Inline Comment 機制
- Reviewer 把滑鼠移到特定行上出現留言圖示，直接在 diff 上留言
- **留言不會逐條發送**，而是先蒐集，等使用者要送訊息給 agent 時「一起打包送出」，介面會顯示一個 badge 標示本次要送出的留言數
- 送出時可附加一段文字說明上下文
- Changes 面板亦可顯示已連結 PR 的 **GitHub review comment**（inline 顯示、每檔案 comment 數量 badge），但只顯示「已 submit」的 review，pending review 不顯示
來源：<https://vibekanban.com/docs/reviewing-code.md>、<https://vibekanban.com/docs/workspaces/changes.md>

### 4.3 送回 Agent / Follow-up / 重跑迴圈
- Agent 收到「所有 inline comment 作為 context」並逐一處理
- 若處理不如預期，文件建議留言要更具體（「explain what is wrong and suggest a fix」並指出確切位置）
- 描述整個機制為 **「review-feedback-fix loop」，會重複直到你滿意為止**
- 沒有「rerun 同一 attempt」的按鈕；如前述，「重跑」的實現方式是開新 **session**（3.4 節）
來源：<https://vibekanban.com/docs/reviewing-code.md>

### 4.4 Chat 介面（對話機制細節）
- 訊息類型四種：使用者訊息（可用鉛筆圖示編輯，**編輯會建立新對話分支，該訊息之後的內容會被取代**）、agent 回應（完整 markdown：程式碼區塊語法高亮、表格、清單、連結）、系統訊息、錯誤訊息（紅色高亮，可展開細節）
- 輸入工具列：粗體/斜體/底線/刪除線/程式碼；可貼圖或拖拉附加圖片給 agent 看
- `@` 觸發檔案 typeahead，帶檔名+完整路徑，可把檔案加入 context
- 傳送：`Cmd/Ctrl+Enter`；agent 執行中可**排隊（Queued）**下一則訊息；狀態含 Idle/Running/Queued/Sending
- Plan 導向的 agent（如開啟 planning mode）會先送出計畫請求核准，使用者可核准或要求修改後才真正動手寫程式
- Context 用量以 gauge 顯示百分比，另有任務進度指標追蹤 agent 產生的待辦清單完成度
來源：<https://vibekanban.com/docs/workspaces/chat-interface.md>

### 4.5 Slash Commands（依 agent 動態列出）
Claude Code：`/compact`、`/review`、`/security-review`、`/init`、`/pr-comments`、`/context`、`/cost`、`/release-notes`
OpenAI Codex：`/compact`、`/init`、`/status`、`/mcp`、`/model`、`/fast`
OpenCode：`/compact`、`/commands`、`/models`、`/agents`、`/status`、`/mcp`
自訂 command 也會出現在 typeahead 中，指令依 workspace 使用的 agent 自動偵測可用清單。
來源：<https://vibekanban.com/docs/workspaces/slash-commands.md>

### 4.6 VSCode / Cursor / Windsurf 擴充套件
- 擴充 ID `bloop.vibe-kanban`；VSCode 走 Marketplace，Cursor/Windsurf 因 deep link 不可靠改走 Open VSX Registry
- 三個面板：Logs（列出當前 task 的 task attempt 清單與 agent 步驟）、Diffs（side-by-side + inline comment）、Processes（即時行程狀態監控）
- 使用限制：**必須在對應 VK task 的 worktree 目錄下開啟編輯器**才會生效
- 流程：VK 建立 task → 點「Open in [Editor]」→ 編輯器在該 worktree 開啟並自動帶入 context/logs/diff/process 資料
來源：<https://vibekanban.com/docs/integrations/vscode-extension.md>

### 4.7 內建瀏覽器預覽
- 需先設定好 dev server script 並「Start dev server」，系統自動從 log 偵測 localhost URL 並載入
- 三種視窗模式：Desktop、Mobile（390×844 手機外框）、Responsive（可拖拉邊界自訂尺寸）
- Inspect Mode：十字準星工具，滑過即高亮元素，點擊選取，自動支援 React/Vue/Svelte/Astro/純 HTML（不需額外安裝），選取的元件資訊會轉入 chat 供跟 agent 討論
- DevTools：以 **Eruda**（行動裝置友善除錯 console）在 preview iframe 內運行，含 Console、Elements、Network、Resources（cookies/storage）、Sources、裝置資訊
- **沒有測試自動化能力**——文件明確指出這只是手動預覽/檢查/除錯功能，不含自動化測試
來源：<https://vibekanban.com/docs/browser-testing.md>

---

## 5. 整合

### 5.1 GitHub 整合
- 依賴 **GitHub CLI (`gh`)** 而非手動 API 設定；建 PR 時自動偵測 `gh` 是否已安裝/認證
- macOS 未裝時彈窗提供 Homebrew 安裝；Windows/Linux 提供手動安裝指示；也可預先手動 `gh auth login`（選 HTTPS 或 SSH）
- 建 PR 流程：開啟含變更的 task → 點 Create PR → 對話框預填標題（=task 標題）、描述（=task 描述）、base branch（預設 repo 預設分支，可調）→ 確認後直接在 GitHub 開 PR，回傳 PR 連結
- 文件**未描述** webhook 機制或雙向自動狀態同步；整合偏向「task 層級」而非「專案層級」持續同步
來源：<https://vibekanban.com/docs/integrations/github-integration.md>

### 5.2 Vibe Kanban 自家 MCP Server（供外部 client 呼叫）
定位：讓 Claude Desktop、Raycast、VS Code 擴充等**外部 MCP client**透過 local stdio 協定管理 VK 的組織/專案/issue/workspace/repo。完整工具清單（依文件整理，工具名稱+用途）：

**Context**
- `get_context` — 取得目前 active session 的 project/issue/workspace metadata

**Organisation**
- `list_organizations` — 列出所有可用組織
- `list_org_members` — 列出成員（含 user ID、角色、profile 資訊）

**Project**
- `list_projects` — 列出組織內專案（需 `organization_id`）

**Issue 管理**
- `list_issues` — 分頁列出 issue，可依狀態/優先序/搜尋詞/assignee/tag 篩選
- `create_issue` — 建立新 issue（標題、描述、優先序、parent 關係）
- `get_issue` — 取得 issue 詳情（含 tag、關聯、PR）
- `update_issue` — 修改 issue 屬性（含巢狀/parent 關係）
- `delete_issue` — 刪除 issue
- `list_issue_priorities` — 列出優先序列舉值（urgent/high/medium/low）

**Issue Assignee**
- `list_issue_assignees` — 列出某 issue 的 assignee
- `assign_issue` — 指派使用者
- `unassign_issue` — 取消指派

**Issue Tag**
- `list_tags` — 列出專案 tag（ID、名稱、顏色）
- `list_issue_tags` — 列出某 issue 附掛的 tag
- `add_issue_tag` — 掛上 tag
- `remove_issue_tag` — 移除 tag

**Issue 關聯**
- `create_issue_relationship` — 建立 issue 間關係（blocking / related / has_duplicate）
- `delete_issue_relationship` — 移除關係

**Repository**
- `list_repos` — 列出所有 repo
- `get_repo` — 取得 repo 詳情與腳本設定
- `update_setup_script` / `update_cleanup_script` / `update_dev_server_script` — 修改 repo 的自動化腳本

**Workspace / Session（任務執行機制的程式化介面 —— 對照第 3 節「task attempt」概念）**
- `list_workspaces` — 列出本機 workspace（可篩選）
- `update_workspace` — 修改 workspace 屬性
- `delete_workspace` — 刪除 workspace
- `link_workspace_issue` — 把 workspace 關聯到遠端 issue
- `start_workspace` — **建立 workspace 並啟動一次 coding-agent session**（即「啟動一次執行」的入口點）
- `create_session` — 在既有 workspace 內新增一個 session
- `list_sessions` — 列出 workspace 的 session
- `run_session_prompt` — 在 session 內執行 coding-agent prompt
- `get_execution` — 查詢執行狀態與結果

支援的 executor（透過此 MCP 可指定）：Claude Code、Amp、Gemini、Codex、Cursor Agent、Qwen-Code、Copilot、Droid。
關鍵用例：從規劃文字自動產生 issue、程式化建立與 issue 連結的 workspace。
來源：<https://vibekanban.com/docs/integrations/vibe-kanban-mcp-server.md>

### 5.3 連接外部 MCP Server（給 VK 內的 agent 用，方向相反）
- Settings → MCP Servers → 選擇特定 coding agent → JSON 編輯器設定
- 設定為**每個 agent 各自獨立**（非專案層級），格式：
```json
{
  "mcpServers": {
    "server_name": { "command": "executable", "args": ["arg1", "arg2"] }
  }
}
```
- 修改會**寫入該 agent 的全域設定檔並持久化**（不是只在 VK 內生效）
- 內建一鍵安裝的熱門 server：Vibe Kanban（自己）、Context7、Playwright、Exa、Chrome DevTools、Dev Manager
- 文件警告：MCP server/工具太多會「降低 agent 效能」（tool 選項過載）
來源：<https://vibekanban.com/docs/integrations/mcp-server-configuration.md>、<https://vibekanban.com/docs/settings/mcp-servers.md>

### 5.4 REST API / WebSocket（後端，主要供內部前端使用，非公開對外 API 文件）
路由前綴：`/workspaces`（生命週期+git 操作）、`/sessions`（agent 互動 context，含 follow-up attempt `CreateFollowUpAttempt`）、`/workspaces/{id}/repos`、`/workspaces/{id}/execution`（dev server 控制/cleanup/archive）、`/workspaces/{id}/links`（連結遠端雲端專案）、editor 整合（開 VS Code/Cursor 路徑）指令。
統一回應格式 `ApiResponse<T>`，成功回 200+JSON，錯誤走 `ApiError` enum 對應 HTTP 狀態碼；驗證在執行前做結構化錯誤回傳。
**WebSocket 即時串流**：`stream_workspaces_ws`（workspace 更新）、`stream_workspace_diff_ws`（特定 workspace 的即時 git diff）。
文件未見公開對外的正式 REST API 文檔頁面（此節內容來自 DeepWiki 對原始碼路由的推導，而非官方 API reference 文件頁）；MCP 才是官方對外程式化介面的主打管道。
來源：DeepWiki `10-api-reference`

### 5.5 Azure Repos 整合
- 依賴 **Azure CLI (`az`) + `az extension add --name azure-devops`**，需 `az login`
- 支援新舊 Azure DevOps URL 格式，HTTPS 與 SSH remote 皆可
- 建 PR 流程同 GitHub：自動預填標題/描述/base branch
來源：<https://vibekanban.com/docs/integrations/azure-repos-integration.md>

### 5.6 VSCode 擴充套件
見 4.6 節。

### 5.7 Webhook / 事件串流
文件中**未提及**任何對外 webhook 機制（例如 issue 狀態變更推播到第三方）。唯一的「事件串流」是內部 WebSocket（5.4）供自家前端/擴充套件消費，不是給第三方訂閱的公開 webhook。

---

## 6. 設定 / 部署

### 6.1 安裝方式
單指令：`npx vibe-kanban`。需先完成想用的 agent CLI 認證。首次啟動流程：選 coding agent、選 IDE、設通知偏好 → 可選登入 GitHub/Google（不登入則看板與團隊功能不可用）→ 登入後自動建立 personal organisation + 一個初始專案。
來源：<https://vibekanban.com/docs/getting-started.md>

### 6.2 資料庫
- **本機（Local）**：SQLite
- **Cloud / 自架 Remote**：PostgreSQL + ElectricSQL（即時同步引擎）
來源：<https://vibekanban.com/docs/self-hosting/local-development.md>、DeepWiki Overview

### 6.3 認證方式
- **Cloud**：僅 OAuth（GitHub 或 Google），只要基本 profile 權限（姓名/email/頭像）；**不支援帳號互相連結**（GitHub 帳號與 Google 帳號無法合併，只能擇一避免產生重複帳號）；**不支援跨裝置一次全部登出**，需到 OAuth provider 端撤銷；**未見 API Key 或 SSO** 支援
- **Agent 認證**：VK 完全不管，各 agent 自行認證（見 3.9 節逐一列出的 `npx -y <agent>` 流程），VK 只在建立 workspace 時提供下拉選單選擇「已認證好的」agent
來源：<https://vibekanban.com/docs/cloud/authentication.md>、<https://vibekanban.com/docs/cloud/getting-started.md>

### 6.4 遠端 / SSH Host
「Remote Access」功能：讓手機等其他裝置存取本機 host 上跑的 VK 實例，機制是**透過 VK Cloud 平台配對**（host 產生 pairing code，client 在 `cloud.vibekanban.com` 輸入配對）。要求：host 需在線登入、client 需能上網並有 VK cloud 帳號。**此功能依賴 Cloud 服務**，隨 Cloud 關閉（見第 0 節）此功能未來也會消失，需注意。
另有獨立的「Remote Deployment 走 SSH」場景（給自架 server 用）：在 Settings → Editor Integration 設定 Remote SSH Host / Remote SSH User，前提是本機到遠端 server 有 SSH 免密金鑰；設定後「Open in VSCode」按鈕會產生 `vscode://vscode-remote/ssh-remote+user@host/path` 連結，喚起本機編輯器透過 Remote-SSH 連過去。
來源：<https://vibekanban.com/docs/remote-access.md>、GitHub README「Remote Deployment」節

### 6.5 自架部署（Docker Compose）
四個核心服務 + 一個可選服務：
| 服務 | 角色 |
|---|---|
| Caddy | 反向代理，自動 HTTPS（Let's Encrypt） |
| PostgreSQL (`remote-db`) | 主資料庫 |
| ElectricSQL (`electric`) | 即時同步引擎，只在內部給 remote-server 用 |
| Remote Server | 核心後端 API + Web UI |
| （可選）relay-server | Tunnel 支援，走 `*.relay.your-domain.com` + DNS-based ACME |

需求：Linux server + Docker/Compose v2.0+、2GB RAM 最低（建議4GB）、10GB 硬碟、網域名稱。
設定步驟：clone repo → 選 OAuth（GitHub/Google，需 callback URL）或本機 email+密碼單一 admin 引導 → 產生 JWT secret 填入 `.env.remote`（含 DB 密碼、網域設定、選填 Loops email API key、Azure storage for 附件）→ 建 `docker-compose.prod.yml` 與 Caddyfile → `docker compose --env-file ../../.env.remote -f docker-compose.prod.yml up -d --build`。
維運：`pg_dump`/`psql` 備份還原；docker-compose 指令看 log；健康檢查驗證服務就緒。
自訂網域反代必設 `VK_ALLOWED_ORIGINS`，否則 Origin header 不符會被 403。
來源：<https://vibekanban.com/docs/self-hosting/deploy-docker.md>、GitHub README 環境變數表

### 6.6 本機開發環境變數（GitHub README 完整列表）
`POSTHOG_API_KEY`/`POSTHOG_API_ENDPOINT`（build-time，留空關閉分析）、`PORT`（生產是 server port，dev 是前端 port，後端用 PORT+1）、`BACKEND_PORT`、`FRONTEND_PORT`（預設 3000）、`HOST`（預設 `127.0.0.1`）、`MCP_HOST`/`MCP_PORT`（給 MCP server 連線用）、`DISABLE_WORKTREE_CLEANUP`（除錯用，關掉孤兒/過期 workspace 清理）、`VK_ALLOWED_ORIGINS`、`VK_SHARED_API_BASE`、`VK_SHARED_RELAY_API_BASE`、`VK_TUNNEL`（開啟 relay tunnel 模式）。
來源：GitHub README

### 6.7 Settings 分頁總覽
General（外觀、預設 agent、editor 偏好、git 設定、通知、tag 管理）、Projects & Repositories（見 3.11）、Organization Settings（見 6.8）、Remote Projects（雲端同步專案的統一入口，見 2.9/6.8）、Agents（見 3.10）、MCP Servers（見 5.3）。快捷鍵 `Cmd/Ctrl+K` 開設定對話框。
來源：<https://vibekanban.com/docs/settings/index.md>

### 6.8 組織與成員設定
- 角色僅兩種：**Admin**（管成員/邀請/設定/可刪組織）、**Member**（僅能存取專案與 issue，不能管組織）；「兩種角色對專案與任務的存取權相同，差異只在組織管理能力」。
- 邀請：輸入 email、選角色，**邀請連結 7 天過期**；待處理邀請清單僅 Admin 可見，可撤銷。
- 移除成員：立即撤銷其對組織內所有專案的存取權，但過去活動紀錄仍可見。
- 硬規則：**組織內必須至少留一個 Admin**（唯一 Admin 不能被降級/離開，除非先轉移角色）。
- **Personal organisation 無法新增成員、無法刪除**；只有 Team organisation 才能無限量邀請成員（不需額外付費）。
來源：<https://vibekanban.com/docs/settings/organization-settings.md>、<https://vibekanban.com/docs/cloud/organizations.md>、<https://vibekanban.com/docs/cloud/team-members.md>

### 6.9 疑難排解（本機/Cloud 兩份文件皆有列舉）
本機常見問題：
- Agent 回報「空 codebase」→ 通常是 repo 開了 git sparse-checkout，需 `git sparse-checkout disable` 後重建 task
- 需要詳細 log → `RUST_LOG=debug npx vibe-kanban`
- 嚴重錯誤需重置 → 刪除應用資料夾（macOS `~/Library/Application Support/ai.bloop.vibe-kanban/`、Linux `~/.local/share/vibe-kanban/`、Windows `%APPDATA%\bloop\vibe-kanban/`），此舉**永久刪除所有 task 與設定**

Cloud 常見問題（八大類）：登入/OAuth（彈窗封鎖、cookie 問題、session 過期）、組織管理（邀請/可見性/離開組織前需先轉移 admin）、團隊協作（邀請信被當垃圾信、快取問題）、專案同步（載入失敗多為網路問題）、看板操作（排序模式非 Manual 則無法拖拉）、Issue 管理（隱藏篩選/欄位導致「找不到」任務）、GitHub 整合（分支名沒帶 issue ID 如 `TASK-123` 則 PR 連結不上）、效能（多為網路/分頁過多，非應用本身問題）。
來源：<https://vibekanban.com/docs/troubleshooting.md>、<https://vibekanban.com/docs/cloud/troubleshooting.md>

---

## 7. 明確「沒有」的東西（依文件通篇檢視所得，非窮舉但為高信心結論）

| 項目 | 現況 | 依據 |
|---|---|---|
| 多人即時協作看板 | **有，但正被官方關閉**（見第 0 節），關閉後只剩單機單人模式 | shutdown 公告 |
| 組織內細粒度權限 | 只有 **Admin / Member** 二元制，兩者對專案/任務存取權完全相同，沒有第三種角色、沒有專案層級/欄位層級的細粒度 ACL | organization-settings、team-members 文件 |
| 跨組織資源分享 | 未見任何「把某專案分享給別組織/外部訪客只讀連結」機制；只有「屬於哪個 organisation」的歸屬概念，沒有分享連結/公開連結功能 | 全文件掃描未見對應描述 |
| Wiki / 知識庫 | 完全沒有提及文件協作、wiki 頁面等功能；VK 的「文件」概念僅止於 issue 描述與 Settings 內的文字模板 tag | 全文件掃描未見對應描述 |
| 自訂欄位 (custom fields) | 明確在 customisation 文件中被排除（「does not cover custom fields」） | cloud/customisation.md |
| 看板 Swimlane | 文件明確指出「no information about swimlanes」 | cloud/kanban-board.md |
| Workflow automation（自動化規則，如「進 Done 自動關聯 PR」等） | 明確被排除在 customisation 涵蓋範圍外 | cloud/customisation.md |
| 公開 Webhook / 事件訂閱給第三方 | 未見任何對外 webhook 文件，只有內部 WebSocket 給自家前端/擴充套件用 | 全文件掃描 |
| Cloud 認證的 API Key / SSO | 只支援 GitHub/Google OAuth 兩種，未提供 API key 登入或企業 SSO | cloud/authentication.md |
| 帳號互聯 (account linking) | 明確聲明「not currently supported」，GitHub 與 Google 帳號不能合併 | cloud/authentication.md |
| 瀏覽器內建自動化測試 | 內建瀏覽器只有手動預覽/inspect/devtools，**沒有**任何自動化測試執行/錄製能力 | browser-testing.md |
| Session/多 agent 並行寫入的合併機制 | 明確是「last write wins」，沒有鎖定或衝突合併 UI | multi-repo-sessions.md |
| 正式對外 REST API 文件頁 | 未見官方公開發布的 REST API reference 文件頁；程式化操作官方導向走 MCP，而非公開 REST API 文件 | 全文件掃描、DeepWiki（來自原始碼推導而非官方文件頁） |

---

## 附錄：完整文件頁面清單（llms.txt 索引，供後續逐頁複查）

**Agents**（10 頁，各為單一 agent 安裝/認證指南）：Amp、CCR、Claude Code、Cursor Agent CLI、Factory Droid、Gemini CLI、GitHub Copilot、OpenAI Codex、OpenCode、Qwen Code

**Cloud**：Browser Testing、Authentication、Customising Your Board、Filtering & Sorting、Getting Started with Cloud、Vibe Kanban Cloud（index）、Issues、Kanban View、List View、Organisations、Projects、Team Members、Troubleshooting

**Core**：Get Started、Vibe Kanban（index）、Issue Management、Remote Access、Responsible Disclosure、Reviewing Code、Supported Coding Agents、Troubleshooting

**Integrations**：Azure Repos Integration、Connecting MCP Servers、Vibe Kanban MCP Server、VSCode Extension Integration、GitHub Integration

**Self-Hosting**：Deploy with Docker Compose、Local Development

**Settings**：Agent Profiles & Configuration、Creating Tags、Overview、Settings Overview（index）、Connecting MCP Servers、Organisation Settings、Projects & Repositories、Remote Projects

**Workspaces**：Changes Panel、Chat Interface、Command Bar、Creating Workspaces、Git Operations、Workspaces Overview（index）、Interface Guide、Managing Workspaces、Multi-Repo & Sessions、Repositories、Sessions、Slash Commands

來源索引頁：<https://vibekanban.com/docs/llms.txt>

DeepWiki 章節（`deepwiki.com/BloopAI/vibe-kanban`）：1 Overview、2 Core Concepts（2.1-2.7）、3 Executor System（3.1-3.5）、4 Backend Services（4.1-4.10）、5 Frontend Application（5.1-5.10）、6 Git and GitHub Integration（6.1-6.4）、7 Development and Deployment（7.1-7.8）、8 Additional Tools（8.1-8.3）、9 Cloud and Team Collaboration（9.1-9.4）、10 API Reference（10.1-10.3）、11 Glossary
