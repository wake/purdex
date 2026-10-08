# Claude Code 多 agent 機制研究：Agent Teams 與 dynamic workflow（ultracode）

日期 2026-10-08｜本機 Claude Code 2.1.294｜目的：給 Purdex lead／member 的資源池、上下訊息規格、審查強制當設計依據。

證據標記：**〔官〕** 官方文件　**〔實〕** 本機拋棄式實驗　**〔本〕** 本機 binary／bundled skill 原文（第一手，但不在公開文件頁）　**〔二〕** 二手或通用知識　**〔推〕** 我的推論

---

## 1. 總結

1. **Agent Teams** 是「一個 lead session 加上幾個長時間存在的 teammate session」。它們之間靠三樣東西協作：檔案型共享任務清單、檔案型信箱、三個品質閘 hook（TeammateIdle／TaskCreated／TaskCompleted）。由 lead 逐回合調度，形狀最接近 Purdex 的 lead／member。
2. **dynamic workflow（ultracode）** 把調度寫成 JS 腳本，由 runtime 執行，一次跑幾十到幾百個短命 subagent。中間結果放在腳本變數裡，不進對話；有硬性的併發上限（每次執行 16 個，CPU 少時會更少）、總數上限 1000、可選的 token 硬上限，同一 session 內可以 resume。
3. 兩套都**只在單一主機、單一 lead 行程之內**。Teams 一個 session 只能有一個 team、不能巢狀、lead 不能換人、`/resume` 救不回 in-process teammate。workflow 執行中不能接受人的輸入。
4. 兩套都沒有「整台主機的資源池」。它們能限制的是 agent 數量（併發槽、總數）和 token／金額，而且都是單一 session 或單一次執行的範圍。測試、build 這類**子行程負載**，官方完全沒有做準入控制。
5. Purdex 最值得借的有三樣：官方的**任務欄位與狀態機**（照抄命名即可）、**系統自動送出的 idle 回報**（回報不必靠模型自律），以及「**hook exit 2 擋住完成**」的閘門語意。Purdex 自己必須補上的是：跨主機 ID、接力後身分要延續，以及主機層的負載量測。

---

## 2. Agent Teams 詳解

主要來源：https://code.claude.com/docs/en/agent-teams 、/hooks 、/costs 、/tools-reference 、/sub-agents 、/cross-session-messaging

### 2.1 啟用、顯示模式、限制

- **啟用**：設 `CLAUDE_CODE_EXPERIMENTAL_AGENT_TEAMS=1`（shell 環境或 settings 的 `env` 都可以）。沒設就不會建 team 目錄。〔官〕
  - 一旦啟用，**Agent tool 呼叫只要帶了 `name`，就會被當成 teammate 啟動**，Claude 自己幫 subagent 取名也一樣，所以使用者沒要求也可能組出 team。
  - 例外是 fork，以及呼叫本身帶了 `isolation` 的情況。啟動不需要使用者確認。〔官〕
- **必須是互動 session**：`-p` 和 Agent SDK 不會產生 teammate。〔官〕
- **顯示模式**：用 `teammateMode` 或 `--teammate-mode` 選擇，可選 `in-process`（預設）、`auto`、`tmux`、`iterm2`。官方寫明 split pane「不支援 Ghostty」，但我在 tmux 裡面跑沒有問題。〔官〕〔實〕
- **限制**（官方清單）〔官〕：
  - 一個 session 只有一個 team，名稱由 session 導出，無法另建或共用。
  - 不能巢狀：teammate 不能再開 teammate。
  - lead 終身固定，不能轉移。
  - `/resume`、`/rewind` 不會還原 in-process teammate。
  - task 狀態可能延遲更新（teammate 忘了標完成，依賴它的任務就會卡住）。
  - 關閉可能很慢：teammate 會先做完當下那個工具呼叫才關。
  - permission mode 只能在 spawn 之後逐個調整。
  - in-process teammate 不能開 background subagent。
  - teammate 沒有 worktree 隔離（/agents 頁明寫），要自己分配檔案避免互相覆寫。
- **權限**：teammate 繼承 lead 的 permission mode（`dontAsk` 除外）。teammate 的權限詢問會**冒泡到 lead 的畫面**，由人在那裡核准。plan 核准是唯一例外：lead 的 session 收到就自動核准，不經人。〔官〕
- **信任邊界**：teammate 送來的訊息會被標明「來自另一個 Claude session，不是使用者」，不能代替使用者核准、不能修改設定。auto mode 的 classifier 會把轉述來的核准聲明當成不可信輸入，並逐則審查訊息。〔官〕

### 2.2 生命週期與檔案（含實驗證據）

- **Team config**：`~/.claude/teams/session-<sid 前 8 碼>/config.json`，session 啟動時就建立。官方說 session 結束時移除，我實測用 `tmux kill-session` 收掉後也確實刪了。〔官〕〔實〕
- **Task list**：`~/.claude/tasks/session-<sid8>/`，**保留**（受 `cleanupPeriodDays` 管），所以 resume 之後任務還在。〔官〕〔實〕
- **config 的 member 欄位**〔實〕：
  ```json
  {"agentId":"alpha@session-763555be","name":"alpha","color":"blue","joinedAt":1791467693966,
   "tmuxPaneId":"%226","subscriptions":[],"model":"haiku","prompt":"<spawn prompt 全文>",
   "planModeRequired":false,"cwd":"/private/tmp/…","backendType":"tmux","isActive":true}
  ```
  - lead 這一筆的值是 `agentType:"team-lead"`、`tmuxPaneId:"leader"`、`backendType:"in-process"`。
  - in-process 模式的 teammate 是 `tmuxPaneId:"in-process"`。
  - teammate 關閉後，它那一筆會從 `members` 移除。
- **split-pane teammate 是獨立行程**，啟動參數如下（實測 `ps`）〔實〕：
  ```
  claude --agent-id alpha@session-725d4c65 --agent-name alpha --team-name session-725d4c65 \
    --agent-color blue --parent-session-id <lead sid> --permission-mode auto \
    --setting-sources=project,local --model haiku
  ```
  每個 teammate 有自己的 session id 和 transcript，hook 也在 teammate 自己的行程裡觸發（TeammateIdle 的 `session_id` 是 teammate 的）。〔實〕
- **陷阱：環境變數沒傳下去**〔實〕〔推〕
  - 我把 `CLAUDE_CODE_ENABLE_TODO_TOOLS=1` 寫在 lead 指令列上（inline env），split-pane teammate **沒有拿到**，因此沒有 Task 工具，只能改用訊息協作。
  - 改寫進專案 settings 的 `env` 之後才有。推測原因是新的 tmux pane 繼承的是 tmux server 的環境，不是 lead 行程的環境。
- **關閉協定**〔實〕
  - 純文字的「請關閉」不會讓 teammate 結束，它會回覆「這不是正式 shutdown request」。
  - 正式流程是 lead 送 `shutdown_request`，teammate 回 `shutdown_approved` 後行程才結束。也可以拒絕（官方有 `shutdown rejection`）。〔官〕

### 2.3 共享任務清單

- **工具**：`TaskCreate{subject, description, activeForm?, metadata?}`、`TaskUpdate{taskId, status?, subject?, description?, activeForm?, addBlocks?, addBlockedBy?, owner?, metadata?}`、`TaskGet`、`TaskList`。〔官〕（SDK TypeScript reference）
- **狀態機**：`pending → in_progress → completed`，另外可以把 status 設成 `deleted` 來刪除。「blocked」**不存成狀態**，它是 `blockedBy` 裡還有未完成任務時推導出來的。〔官〕〔實〕
- **檔案格式**：一個任務一個 JSON，另有 `.lock`（claim 用的檔案鎖）和 `.highwatermark`（ID 計數器）。〔實〕
  ```json
  {"id":"2","subject":"B step","description":"…","owner":"gamma","status":"in_progress",
   "blocks":[],"blockedBy":["1"]}
  ```
  任務 1 完成後，任務 2 的 `blockedBy:["1"]` **仍然留在檔案裡**，所以「解鎖」是讀取時判斷的，不是去改依賴欄位。〔實〕
- **claim**：
  - lead 可以指派，teammate 也可以自己領下一個「未指派、未被擋住」的任務。claim 用檔案鎖防 race。〔官〕
  - 實測 teammate 自領，就是用 `TaskUpdate` 寫入 `owner` 加上 `in_progress`。〔實〕
  - TaskUpdate 成功時，工具回傳會提示「Task completed. Call TaskList now to find your next available task…」，也就是由系統推它去領下一個。〔實〕
- **設定 owner 時，系統會往 owner 的信箱寫一則 `task_assignment`**〔實〕：
  - 自己領自己的任務也會收到，造成 teammate 處理「重複指派」的雜訊（兩次實驗都發生）。
  - 格式：`{"type":"task_assignment","taskId":"1","subject":"…","description":"…","assignedBy":"alpha","timestamp":"…"}`
- **全部任務完成並收尾後，任務 JSON 被刪掉**〔實〕：
  - 只剩 `.highwatermark`（值為 3）和 `.lock`，lead 再呼叫 TaskList 時得到 `No tasks found`。
  - 確切的觸發時機我沒有釘死，官方文件也沒寫。
- **已知卡住問題**：teammate 沒把任務標完成，依賴它的任務就一直卡著，官方建議人工或叫 lead 去推它。〔官〕
- **Task 工具預設只在舊模型上提供**〔官〕
  - 預設清單是 Claude 3.x、Opus 4–4.7、Sonnet 4–4.6、Haiku 4.5。
  - **Opus 5.5／Haiku 5.5 預設沒有**，要設 `CLAUDE_CODE_ENABLE_TODO_TOOLS=1`。我實測 `--model haiku` 會解析成 Haiku 5.5。〔實〕
  - 沒有 Task 工具時，team 就退回只用訊息協作。
- **跨 session 共用任務清單**：設 `CLAUDE_CODE_TASK_LIST_ID=<名>`，多個 session 就共用 `~/.claude/tasks/<名>/`。〔官〕不必是 team，但僅限本機檔案；它在非 team 情境下的 claim 語意我**未實測**。

### 2.4 信箱

- **位置**：`~/.claude/teams/{team}/inboxes/{agent}.json`。in-process 模式**也用檔案信箱**。〔官〕〔實〕
- **條目格式**〔實〕：
  ```json
  {"from":"beta","text":"beta done","summary":"Beta completion notice","timestamp":"2026-10-08T13:56:16.838Z",
   "color":"green","msgV":1,"msg_id":"30926e73-…","type":"message","read":false}
  ```
  - 讀取後條目會從檔案移除，檔案回到 `[]`。
  - spawn prompt 本身就是 lead 寫進 teammate 信箱的第一則訊息。
  - 模型看到的形式是 `<teammate-message teammate_id="team-lead">…</teammate-message>`。〔實〕
- **結構化協定訊息**：放在 `text` 裡的 JSON 字串（外層 `type` 仍是 `"message"`）〔實〕：
  - `idle_notification`：`{type, from, timestamp, idleReason:"available", summary?, result}`。其中 `result` 是 teammate 這回合的最終回答，**由系統自動送出**。〔官〕〔實〕
  - `shutdown_request`：`{type, requestId:"shutdown-<ms>@<name>", from, reason, timestamp}`
  - `shutdown_approved`：`{type, requestId, from, timestamp, paneId, backendType}`
  - 官方還列了 plan approval request／response、權限請求、shutdown rejection，但這次實驗沒有觸發，欄位未知。〔官〕
- **送達語意**〔官〕：
  - 只有寫入收件人信箱檔**成功**才算送出，失敗時寄件方會收到錯誤（2.1.224 起）。
  - 讀取時逐則驗證，格式錯誤的條目會被移除並報錯，合法的照送（2.1.207 起）。
  - 一律自動送達，lead 不需要輪詢。廣播要逐一寄送。
  - 對象已經關閉時，回應是 `Not sent — no agent named 'alpha' is reachable`。〔實〕
- **人類插手**：在 agent panel 選 teammate 後直接打字、按 Esc 中斷它這一回合、按 `x` 停掉它。在 teammate 畫面輸入 `/compact`、`/clear` 會作用在 lead，所以要先確認。〔官〕

### 2.5 hooks

| hook | 觸發 | payload（除共通欄位外） | exit 2 語意 |
|---|---|---|---|
| TeammateIdle | teammate 結束回合、將閒置 | `teammate_name`、`team_name`（已棄用） | 不讓它閒置，stderr 當回饋要它繼續做；JSON `{"continue":false,"stopReason"}` 則讓它整個停掉 |
| TaskCreated | 透過 TaskCreate 建任務時 | `task_id`、`task_subject`、`task_description?`、`teammate_name?`、`team_name?` | 刪掉剛建的任務，stderr 當作工具錯誤回給模型；`continue:false` 被忽略 |
| TaskCompleted | TaskUpdate 標完成時，**或** teammate 在還有 in_progress 任務的情況下結束回合 | 同上 | 不准標完成，stderr 回饋給模型；`continue:false` 只在「teammate 結束回合觸發」那種情況有效 |

- 三個 hook 都**不支援 matcher**，每次都觸發。沒有 Task 工具的 session 不會觸發 TaskCreated。〔官〕
- 實測 TaskCompleted 加 exit 2〔實〕：
  - 模型收到的工具結果是 `TaskCompleted hook feedback:\n[<hook 路徑>]: <stderr>`，任務沒被標完成。
  - 模型照著回饋做之後重試，就通過了。
  - teammate 觸發的 payload 帶 `teammate_name`，lead 自己觸發的不帶。
- **實驗副產品**：我的閘門要求「送出 VERIFIED 這個字」，模型輕易就照做了，lead 自己也評論「閘門被關鍵字滿足，而不是被真正的檢查滿足」。也就是說，**閘門要檢查可以驗證的產物，不能只檢查模型的說法**。〔實〕
- 相關 hook〔官〕：
  - SubagentStart：可以注入 `additionalContext`，不能擋。in-process teammate 每處理一則新訊息都會觸發一次。
  - SubagentStop／Stop：`decision:"block"` 可以擋，有連續 8 次的上限，用 `CLAUDE_CODE_STOP_HOOK_BLOCK_CAP` 調整。
  - PreToolUse：allow／deny／ask／defer，可以用 `updatedInput` 改寫輸入。
- **mod 的 `agent.spawn`** 事件會帶 `e.isTeammate`，可以用 `{deny}` 拒絕或改 model。〔官〕

### 2.6 成本與併發（官方說法）

- 「**teammate 數量沒有硬上限**」，但 token 成本隨人數線性成長。建議 3–5 人、每人 5–6 個任務；15 個獨立任務用 3 人就夠。〔官〕
- 省錢建議：teammate 用 Sonnet、team 保持小、spawn prompt 精簡、做完就關（閒置的 teammate 仍持續消耗）。〔官〕
- 不適用 subagent 的併發上限（20 個），官方原話是「follow their own limits」，但沒說是什麼上限。〔官〕
- teammate 撞到使用量上限時，**不會**像 workflow 那樣暫停等待。〔官〕

### 2.7 實驗紀錄（約 6 分鐘，已清理）

```bash
P=/private/tmp/claude-501/teams-probe-17394202
# P/.claude/settings.json：五個 hook（TeammateIdle/TaskCreated/TaskCompleted/SubagentStart/SubagentStop）
#   都指向 log.sh，它把 stdin 記錄下來；TaskCompleted 對每個 task 第一次回 exit 2
tmux new-session -d -s teams-probe-17394202 -c $P \
 "CLAUDE_CODE_EXPERIMENTAL_AGENT_TEAMS=1 CLAUDE_CODE_ENABLE_TODO_TOOLS=1 CLAUDE_CODE_DISABLE_CLAUDE_MDS=1 \
  claude --model haiku --setting-sources project,local --teammate-mode tmux"
```

- **A 輪**（tmux 模式，env 只寫在指令列）：teammate 沒有 Task 工具。
- **B 輪**（env 改寫進專案 settings）：alpha／beta 自己領任務、互傳訊息、被 hook 擋一次後完成、正式關閉。
- **C 輪**（in-process 模式）：gamma 領了 A，完成後再自領被 A 擋住的 B。
- 另外用輪詢腳本每 50 ms 快照信箱和任務檔，2.2–2.4 引用的片段都是從這裡來的。

**清理**：已刪除 tmux session、拋棄式目錄、四個 `~/.claude/tasks/session-*`、空的 `~/.claude/teams/`、該目錄的 transcript 與 scratchpad。`~/.claude.json` 裡多了一筆探測路徑的 project 紀錄，因為是全域檔，我沒有去動它。

---

## 3. workflow／ultracode 詳解

主要來源：https://code.claude.com/docs/en/workflows 、bundled skill `workflow-authoring`〔本〕、binary 裡的 Workflow tool 描述〔本〕、SDK reference 的 Workflow 型別〔官〕

### 3.1 定位與「誰能觸發」

- **定位**：「由腳本決定下一步」。和其他做法的差別：subagent／skill 由 Claude 逐回合決定，Teams 由 lead 逐回合決定，workflow 由腳本決定。中間結果放在腳本變數裡，規模可以到每次幾十到幾百個 agent，中斷後在同一 session 可以 resume。〔官〕
- **opt-in 規則**（Workflow tool 描述原文〔本〕）：只在使用者明確選擇時才呼叫。
  - 允許的情況：prompt 裡有 `ultracode` 關鍵字、session 開著 ultracode、使用者**親口**要求跑 workflow、使用者觸發的 skill／指令要求呼叫、使用者指名某個已存的 workflow。
  - 不允許的情況：「任務適合平行」不算，要先描述大概成本，再問使用者。
- **關鍵字只認人打的字**：`-p`、沒標成 human 的 SDK 輸入、排程、webhook、PR 留言都不算（2.1.210 起）。〔官〕
- **執行前核准**：依 permission mode 決定〔官〕。
  - auto 模式只問第一次，開著 ultracode 時不問。
  - manual 模式每次都問。
  - bypass、`-p`、SDK 不問；這些情況走一般權限規則，可以用 `Workflow` 或 `Workflow(<name>)` 這種 allow rule 放行。
  - 腳本傳給 `agent()` 的 prompt 在 auto 模式下**不算使用者的請求**。
- **ultracode 的效果**：開著時跳過 Large workflow 警告、**取消 subagent 併發上限**、跳過 auto 模式的首次核准。關掉 workflow 功能用 `disableWorkflows` 或 `CLAUDE_CODE_DISABLE_WORKFLOWS`。〔官〕

### 3.2 腳本 API〔本〕〔官〕

- 開頭必須是 `export const meta = {name, description, phases?, whenToUse?}`，而且是純字面值。
- **`agent(prompt, {label, phase, schema, model, effort, isolation:'worktree', agentType})`**
  - 不帶 schema 時回傳最終文字；帶 schema 時強制呼叫 StructuredOutput 工具，回傳驗證過的物件。
  - 驗證失敗最多重試 5 次（`MAX_STRUCTURED_OUTPUT_RETRIES`），之後拋錯。
  - schema 自相矛盾時，啟動前就報錯。
  - 被使用者跳過，或 API 錯誤重試用盡時，回傳 `null`。
- **`pipeline(items, stage1, stage2…)`**：每個 item 獨立往下走，**階段之間沒有屏障**，這是預設寫法。某個 stage 丟錯時，該 item 變成 `null`，其餘 stage 跳過。
- **`parallel(thunks)`**：屏障，等全部完成。永遠不會 reject，失敗的那格是 `null`。
- 其他：`phase(title)`、`log(msg)`、`args`（呼叫時帶入的 JSON）、`workflow(name|{scriptPath}, args)`（只能巢狀一層，共用併發、總數和預算）。
- **禁止**：`Date.now()`、`Math.random()`、不帶參數的 `new Date()`（會破壞 resume）、`import()`、檔案系統、shell。腳本只負責調度，實際工作都交給 agent。
- **大小指引** `workflowSizeGuideline`：只是建議值，不是上限。`small` 少於 5 個、`medium` 少於 10 個（預設；Pro 預設 small）、`large` 少於 50 個、`unrestricted` 不限。〔官〕

### 3.3 併發、上限、預算

| 項目 | 值 | 證據 |
|---|---|---|
| 每次執行的併發 agent 數 | 預設 16，CPU 少時更少（skill 寫 `min(16, CPU-2)`）；`CLAUDE_CODE_WORKFLOW_MAX_CONCURRENT_AGENTS` 可設 1–256；超過的排隊等空槽，**不拒絕** | 〔官〕〔本〕 |
| 每次執行的 agent 總數 | 1000（防失控迴圈） | 〔官〕 |
| 單次 parallel／pipeline | 4096 項，超過就明確報錯，不會靜默截斷 | 〔官〕 |
| Large workflow 警告 | 排程超過 25 個 agent 或預估超過 1.5M token 時顯示，**只是提示，不暫停** | 〔官〕 |
| token 預算 `budget` | 使用者在 prompt 下「+500k」這類指令時才有值；`budget.total`／`spent()`（**輸出 token**，主迴圈加所有 workflow 共用一池）／`remaining()`；**是硬上限，達到後 `agent()` 會拋錯**；腳本可以用 `remaining()` 動態決定規模 | 〔本〕（公開文件頁查不到） |
| 撞到使用量上限 | 撞到的 agent 等重設、**不再啟動新 agent**，重設後自動繼續；條件是互動 session 加訂閱，24 小時內會重設，最多等兩次 | 〔官〕 |
| prompt cache 錯開 | 同前綴的兄弟 agent 先等第一個開始回應，最多 5 秒（`…_PREFIX_STAGGER_MS`） | 〔官〕 |

### 3.4 resume、失敗、重試〔官〕

- **resume 只在同一 session 內有效**：
  - 暫停可以直接 resume。已停止的，請 Claude 用同一份腳本加 `resumeFromRunId` 重新啟動。
  - 重播的順序是 agent 的啟動順序。已完成且 prompt 沒變的，直接回傳快取。**第一個 prompt 變了的 agent 以及它之後的全部重跑。**
  - 中途失敗（含被按 `x` 停掉）的 agent，**連同它之後啟動的全部重跑**。
  - 找不到紀錄時回 `nothing to resume`。
  - 背景化的 session 會帶著執行繼續跑。
- **Workflow tool 立即返回**〔官〕
  - 回傳 `{status:"async_launched", taskId, runId, scriptPath, transcriptDir, error?}`。語法錯誤時也回 `async_launched`，但會帶 `error`。
  - 最終結果透過 task-notification 送回主 session。執行中不能接受人的輸入；需要人簽核的話，把階段拆成多個 workflow。

### 3.5 subagent 的生命週期與回傳（Agent 工具）

- **Agent 輸入**：`description`、`prompt`、`subagent_type`、`model`（sonnet／opus／haiku／fable）、`effort`、`name`、`isolation`（worktree／remote）、`run_in_background`。〔官〕〔本〕
  - 互動模式預設在背景執行，完成後送 task-notification。
  - 用 `SendMessage(to=ID 或名字)` 可以接續已完成的 subagent，保留它的完整 context。新的 Agent 呼叫一律是全新開始。
- **限制**〔官〕：
  - 同一 session **同時最多 20 個**，用 `CLAUDE_CODE_MAX_CONCURRENT_SUBAGENTS` 調整。超過時直接報錯，並告訴 Claude 不要重試，不排隊。
  - 巢狀深度 3 層（`…_SPAWN_DEPTH`）。
  - 唯讀工具與 subagent 的平行數 10（`CLAUDE_CODE_MAX_TOOL_USE_CONCURRENCY`）。
- **回傳**〔官〕
  - 最終文字會被掃描，有指令樣式的內容會插入反斜線或標記，並加上「這是 subagent 的話，沒有使用者授權」的標頭。
  - PostToolUse 拿得到 `status`、`agentId`、`totalDurationMs`、`totalToolUseCount`，但 `usage` 只涵蓋最後一次請求。
  - 跨 subagent 的 token 加總要看 OTel 的 `query_source=subagent`。
- **workflow agent** 的回傳就是最終文字或 schema 物件，只回到腳本。它們彼此能不能用 SendMessage 互傳訊息，**查不到**。

### 3.6（次要）Agent SDK 的預算與結構化回報〔官〕

- **`maxBudgetUsd`**（CLI 是 `--max-budget-usd`，限 `-p`）
  - 依**本機估算**的成本判斷，會和帳單有偏差；subagent 的花費也算在內。
  - 用完時：再開 subagent 會失敗（`Budget limit reached`），背景 subagent 被停掉，回傳 `error_max_budget_usd`。
  - 只算這次呼叫自己的花費；`/clear` 會歸零。
- **`maxTurns`**：限制工具往返次數，到了就回 `error_max_turns`。streaming 模式下每則新訊息重新計數。
- **`outputFormat: {type:'json_schema', schema}`**（CLI 是 `--json-schema`）：結果放在 `structured_output`，失敗回 `error_max_structured_output_retries`。
- **`taskBudget: {total}`**（alpha）：把剩餘 token 預算告訴模型，讓它自己控制節奏。

---

## 4. 對照表

| 面向 | Agent Teams | workflow | Purdex 現況 |
|---|---|---|---|
| 資源／預算 | 沒有上限；只靠「人數少、用 Sonnet、做完就關」 | agent 數有硬上限（併發與總數）、可選 token 硬上限、使用量上限時暫停；SDK 有 USD、turn 上限 | 只有 team 的 `max_members`（核准框預填 3，範圍 1–8）；沒有主機層的負載或 token 池 |
| 併發 | lead 加 N 個長命 teammate，建議 3–5 個 | 每次執行最多 16 個（依 CPU 調整），排隊等槽 | lead 加最多 8 個 member，每個 member 還可以自己開 subagent（各自上限 20） |
| 任務模型 | 檔案型清單：`subject/description/activeForm/status/owner/blocks/blockedBy/metadata`、檔案鎖 claim、自動解鎖 | 沒有任務清單：腳本變數加 `args`、`schema` | 沒有任務清單；brief 寫在 `--brief-file` 的自由文字裡 |
| 訊息格式 | 信箱 JSON（`from/text/summary/timestamp/msg_id/msgV/type/read`）；協定訊息是 text 裡的 JSON（`task_assignment`、`idle_notification`、`shutdown_*`、plan） | 沒有訊息；agent 只回傳結果（可帶 schema） | `pdx msg send` 自由文字，走原生 peer socket；有 `[pdx team]` 前綴的通知 |
| 審查／品質閘 | TeammateIdle／TaskCreated／TaskCompleted 回 exit 2；Stop、PreToolUse | 寫在腳本裡（adversarial verify、評審團），schema 強制結構 | R1→攻擊方→critic 靠 skill 和自律；有 hook 硬鎖（U17），但沒有針對審查的閘門 |
| 人類介入 | 直接對 teammate 打字、中斷、停掉；權限詢問冒泡到 lead | 執行前核准；執行中只能暫停、停止、重啟單一 agent | 一鍵核准（推到所有 client）、無人值守模式（U23）、AskUserQuestion 分流（U19） |
| context 延續 | 不處理 teammate 接力；resume 救不回 in-process teammate | 短命 agent，沒有接力需求；同一 session 內 resume | 核心功能：relay（交接檔、`/clear`、轉址鏈），lead 安排 member 接力 |
| 跨主機 | 不支援（本機檔案）；原生跨機訊息要靠 Remote Control | 不支援（`isolation:'remote'` 是送上雲） | 支援：tailnet、依 repo 選主機（U15）、跨主機 spawn |

---

## 5. 可以借給 Purdex 的設計

### (a) 資源池

**對照使用者的想法**（每台主機的 daemon 一個池、容量 100、session 申請時給估值、觀察實際用量後回補）。官方和業界的現成做法：

1. **排隊而不是拒絕**：workflow 超出併發時排隊，subagent 超出時直接拒絕並叫模型別重試。對長命 session 來說，**排隊（加上限時）比較不會讓模型亂試**。〔官〕
2. **容量依硬體決定**：workflow 用 `min(16, CPU-2)`。容量 100 不要寫死，可以定義成 `100 = 主機可承受的負載`。mlab 有 10 核、16 GB，換算時同時看 CPU 和記憶體兩個維度。〔本〕〔推〕
3. **估值加量測**的現成模型〔二〕
   - Bazel：每個 action 宣告資源估值，排程器對主機的 `--local_cpu_resources`／`--local_ram_resources` 做準入。
   - Kubernetes：requests（排程用的估值）和實際用量分開；用量超出 limits 才處置。
   - GNU make jobserver：跨行程的 token 池，子行程拿到 token 才能開工。
   - 結論：使用者的「先給估值、之後看實際用量」正是 requests／usage 的分法。
4. **主機層實測當後備**：Claude Code 自己在 OS 回報 critical memory pressure、且 session 閒置 30 分鐘時，會殺掉背景 shell（可以用 `…_BG_SHELL_PRESSURE_REAP` 關掉）。這就是官方「拿實測壓力當底線」的先例。〔官〕
5. **用量上限時停止準入、不殺正在跑的**：workflow 撞到使用量上限時，不再啟動新 agent，執行中的 agent 等待。這可以直接當 Purdex 的「主機忙」語意。〔官〕

**具體建議：**

- **準入公式**〔推〕
  - 准入條件：`已預約 + 未預約行程的實測佔用 + 本次估值 ≤ 100`。
  - 實測佔用：daemon 本來就知道每個 session 的 pane PID 和 CC PID（lead-adopt 規格用過 PID 加 `proc_start` 驗證），可以沿行程樹把 CPU 和 RSS **歸屬到 session**。
  - 估值在命令開始後 N 秒改用實測值的移動平均（EWMA）回補，命令結束（行程消失）時歸還。比照既有 lease 設計（relay spec §9.2）。
- **只能攔已知類型怎麼補**
  - 已知的重活（完整 vitest、`go test -race`、`pnpm build`、`electron:build`）走白名單加權重，在 PreToolUse 或 mod 的 `tool.call`（matcher `{tool:'Bash'}`）裡判斷。
  - 未知命令不攔，但它的實測佔用照樣計入主機總量，下一個已知重活就會因此排隊。也就是說，「未知」由量測補上，不由攔截補上。
- **不要硬擋，避免誤判**
  - **能改寫就不擋**：PreToolUse 的 `updatedInput` 可以改寫命令。例如自動把 `vitest run` 改成 `vitest run --maxWorkers=3`（記憶裡既有的 mlab 規則），模型照常做事。〔官〕〔推〕
  - **等待要有時限，逾時就放行**：PreToolUse command hook 預設 timeout 600 秒，逾時**不擋**，照一般權限流程繼續（官方明寫「別把會卡住的 hook 當閘門」）。這正好讓「最多排隊 N 秒、之後放行」天生就失敗時放行，不會死鎖。〔官〕
  - **讓模型知道**：放行或排隊時用 `additionalContext` 告訴它「主機忙，已排隊 37 秒」，不要讓它以為命令卡住而去重試。〔官〕
  - **另一條路：自願包一層**：`pdx res run --weight 40 -- <cmd>`。命令本身阻塞等槽，不受 hook 時限影響；skill 規定重活要包這層，hook 只負責發現沒包的情況並提醒。〔推〕
- **分階段，控制複雜度**〔推〕
  1. **P0 只量測**：daemon 取樣負載和記憶體，歸屬到 session，顯示在 App 和 `pdx team`。不改任何行為。
  2. **P1 提示**：主機超過門檻時，hook 對已知重活回 `additionalContext` 加上自動降並行的改寫。
  3. **P2 軟閘**：已知重活排隊，有時限，逾時放行；加上 `pdx res run`。
  4. **P3 硬限**：只對極少數情況（例如每台主機同時只能跑一個完整 vitest）採用。
  - 每一階段都可以獨立關閉。池以 daemon 為單位，比照 U23 的「每台主機自己持有狀態」，不進 Profile Sync。
- **token 和 agent 數的預算**（另一個維度）：可以借 workflow 的 `budget` 和 SDK 的 `maxBudgetUsd`，在 team 層設「member 數加每個 member 的 subagent 上限」。例如 spawn 時就把 `CLAUDE_CODE_MAX_CONCURRENT_SUBAGENTS` 帶進 member 的 env。官方的 `agent.spawn` mod 事件也可以拒絕超量的 subagent。〔官〕〔推〕

### (b) 上下訊息規格

**照抄（命名與語意）：**

- **任務**：`id`、`subject`、`description`、`activeForm`、`status`（`pending|in_progress|completed|deleted`）、`owner`、`blocks`、`blockedBy`、`metadata`。
- 「blocked」**由 `blockedBy` 推導，不當成狀態儲存**。claim 等於寫入 `owner` 加上 `in_progress`，並用鎖做 CAS。
- 完成某個任務時，回覆裡附上「去領下一個」的提示（官方 TaskUpdate 的回傳就是這樣做）。
- **訊息外框**：`from`、`text`、`summary`（約 5–10 字，超過 200 字元截斷，對應 SendMessage）、`timestamp`、`msg_id`、`msgV`、`type`。
- **協定訊息種類**：`task_assignment`、`idle_notification{idleReason, summary, result}`、`shutdown_request/approved/rejected{requestId, reason}`、`plan_approval_request/response`。
- **送達語意**：寫入成功才算送出；收件端逐則驗證，壞掉的剔除、好的照送。
- **最重要的一點**：`idle_notification` **由系統在回合結束時自動送出**，內容是該回合的最終回答。Purdex mod 可以在 member 的 `turn.complete`（或 `classic.Stop`）自動送一則結構化回報給 lead，讓回報完全不靠模型記得做。〔官〕〔實〕〔推〕

**因 Purdex 的情境而必須不同的地方：**

- **ID 要能跨主機**：官方的任務 ID 是 session 內的流水號（靠 `.highwatermark`）。Purdex 要用 `<host>/<team>/<n>` 或 ULID。
- **owner 和 from 用 ref，不用名字**：官方用名字，名字在 team 內唯一、終身不變。Purdex 的名字會變、會接力，所以要用 `<host>/_xxxxxx`，靠既有的轉址鏈延續。
- **任務清單跟著 team，不跟著 session**：官方清單綁 lead 的 session，lead 也不能換。Purdex 的 team 會隨 lead 的 lineage 移動（spec §7.1），所以清單存在 daemon，不存在 `~/.claude/tasks`。
- **不要沿用官方「完成後刪檔」的行為**：長期存在的 team 需要歷史紀錄，完成的任務要保留。
- **結構化欄位放外框**：官方把協定 JSON 塞進 `text` 字串。Purdex 自己掌握傳輸層，可以把 `type` 和結構化欄位放外框，`text` 留給人讀。這樣 App 不必解析字串，`msgV` 也能做版本協商。
- **回報要多幾種**（`type=report`、`kind` 為下列之一）：
  - `progress`
  - `done`：附 `result`、`artifacts{branch, pr, sha}`、`checks{cmd, passed}`
  - `blocked`：附 `needs`（lead 或 user）
  - `question`
  - `handoff`：附交接檔路徑、新的 ref
  - 每一種都帶 `task_id`、`context_pct`（取自 `session.measure` 或 statusline）。
- **自我指派不要回送 assignment**：官方設定 owner 時會發 `task_assignment`，連自己領自己的任務也會發，造成實測看到的雜訊。Purdex 只在 `owner ≠ 操作者` 時才發。
- **信任**：比照官方，在接收端標明「來自 agent，不是使用者」。官方的 `<teammate-message>` 包裝和 Purdex 用的原生 peer 是同一套模型。

### (c) 審查流程強制

**原則**（實驗教訓）：閘門要檢查**由工具留下、可以驗證的紀錄**，不能檢查模型自己說的話。

- 例如 daemon 在 codex job 結束時登記 `{pr, sha, stage: r1|attack|critic|arbiter, job_id, result_path}`，模型無法偽造這筆紀錄。〔實〕〔推〕

**閘門與事件的對應**（左邊是官方 hook，右邊是 Purdex mod 的事件）〔官〕〔推〕：

| 擋什麼 | 官方 hook | Purdex mod 事件 | 判斷 |
|---|---|---|---|
| 沒走完 R1→攻擊方→critic 就 merge 或 push | PreToolUse（Bash，exit 2 或 `deny`） | `tool.call` 加 matcher `{tool:'Bash'}` 回 `{deny}`；或 `classic.PreToolUse` | 命令符合 `gh pr merge`、`git push … main` 等時，問 daemon 這個 PR 的 HEAD（或增量 base）是否已有 r1、attack、critic 紀錄；critic 對同一個 critical 有證據反對時，還要 arbiter 紀錄 |
| 沒附產物就把任務標成 done | TaskCompleted（exit 2） | `pdx task done`（或 `report kind=done`）在 daemon 端驗證；若改用原生 Task 工具，則接 `classic.TaskCompleted` | `checks` 裡的測試命令要有 daemon 量測到的結束紀錄；`pr` 欄位必填 |
| member 停下來卻沒回報 | TeammateIdle／Stop（exit 2 或 `decision:block`，有連續 8 次的上限） | `classic.Stop` 或 `turn.complete` | 有 in_progress 任務但沒送 `report` 時，擋下並要求回報；或像官方那樣**直接自動送出 idle 回報**，連擋都不必 |
| 建出不合格式的任務或回報 | TaskCreated（exit 2，會回滾） | `session.send` 回 `{isDelivered:false, reason}` | 驗證外框 schema（`type`、`task_id`、`summary` 長度等） |
| 審查階段中修改 diff | PreToolUse（Edit／Write） | `tool.call` | 審查進行中鎖住該 PR 的檔案（選用） |

**注意事項：**

- **閘門逾時就放行**：PreToolUse command hook 逾時不擋。審查閘門必須快速回答，查 daemon 的本機 API 就好，不要在 hook 裡跑 codex。〔官〕
- **原生 Task 事件預設不會觸發**：Purdex member 跑 Opus 5.5 時預設沒有 Task 工具，所以 TaskCreated／TaskCompleted 不會觸發。如果要借原生 Task 工具，spawn 時要帶 `CLAUDE_CODE_ENABLE_TODO_TOOLS=1`，而且**要放在 env 或 settings 裡，不要放 tmux 指令列**（見 2.2 的陷阱）。〔官〕〔實〕
- **Stop 閘門有上限**：連續擋 8 次之後就會被強制放行，所以只能用來督促，不能當硬鎖。〔官〕

---

## 6. 不確定處與證據等級

**查不到或沒驗證的：**

- 任務 JSON 在「全部完成」之後被刪除的**確切觸發時機**（是全部完成時、teammate 關閉時，還是 lead 收尾時）。我觀察到兩次，但官方文件沒寫。〔實〕
- `CLAUDE_CODE_TASK_LIST_ID` 跨 session 共用時，在**沒有 team** 的情況下，claim、檔案鎖、依賴解鎖、`task_assignment` 的行為。未實測。
- plan approval／rejection 與權限請求這兩種協定訊息的 JSON 欄位，這次沒有觸發。
- workflow 的 `budget` 和「+500k」指令只出現在 bundled skill 的文字裡，公開文件查不到；「輸出 token」的計法也來自 skill。〔本〕
- workflow 的併發公式：官方寫「16，CPU 少時更少」，skill 寫 `min(16, CPU-2)`。mlab 是 10 核，照 skill 推算應為 8，未實測。
- workflow agent 彼此能不能用 SendMessage 互傳訊息，查不到。
- split-pane teammate 沒拿到 env，原因是我推測的（tmux pane 繼承 tmux server 的環境），沒有逐項驗證。〔推〕
- Teams 本身有沒有隱藏的 teammate 數上限：官方明寫「沒有硬上限」，我沒有壓力測試。
- 第 5 節提到的 Bazel、Kubernetes、make jobserver 是通用知識，這次沒有重新查證。〔二〕

**主要結論的證據等級：**

- 第 2.1、2.5、2.6 節、第 3 節的限制表、第 3.6 節：〔官〕
- 第 2.2–2.4 節的檔案格式、協定訊息、hook payload、閘門行為：〔實〕
- Workflow 的 opt-in 規則：〔本〕（binary 原文）
- 腳本 API：〔本〕加〔官〕
- 第 5 節的建議：以〔推〕為主，各條所依據的事實已在條目內標出來源。
