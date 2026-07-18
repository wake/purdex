# Ploom + Purdex 派工整合：里程碑拆分（最小可用聚焦）

> 依 codex 拍板順序：先釘 execution/attempt 模型 → Purdex 執行 runtime → diff review 迴圈 → MM bridge 最後。
> 五層資料模型：`issue → dispatch → execution → attempt → session`。穩定對外 handle = `execution_id`。

## 關鍵定位
- **M0 = 第一個有用的閉環**（demo-able、能跑通，但脆）
- **M1 = 第一個可日常用**（daily-driver reliable）
- 「最小可用」落在 **M0→M1 之間**：M0 有用、M1 可靠。因兩邊引擎已存在，M0 主要是接線。

---

## 里程碑表

### M0 · Walking skeleton ★ 最小可用（有用但脆）
- **能做什麼**：Ploom issue 按「派工」→ 選 host+repo → Purdex 跑 `claude -p`（已有）→ issue 上看到狀態(queued/running/done) + deeplink 跳 Purdex 看即時 + 完成後 diff/結果回填 issue。**解決核心痛點：work item 與 execution 對上。**
- **新工作**
  - 縫：最小事件契約 `DispatchRequested / ExecutionAccepted / ExecutionLifecycle / ExecutionArtifact(diff,transcript)`；**execution_id 從第一天就當穩定 handle**（不用 session_code 對外）。
  - Ploom：dispatch 資料表 + 1 個 dispatch API + 「派工」按鈕 + issue 上 execution 狀態列 + deeplink。
  - Purdex：接 dispatch endpoint + execution 記錄(execution_id+狀態)包住現有 session 啟動 + 回報狀態 + 完成擷取 diff。
- **量**：M（引擎已在，主要接線 + 資料模型）
- **刻意不做**：worktree、lease/reclaim、attempt、provider 中立(先只 claude)、inline review、內嵌執行 UI(先 deeplink)
- **風險/取捨**：無 lease → laptop sleep / daemon crash 會讓派工卡住、需手動重派（可接受，M1 補）；單 host、單 execution、單 repo

### M1 · 韌性（第一個可日常用）
- **能做什麼**：派工可靠——斷線/crash/sleep 後能 reclaim；重複派工 idempotent；控制權清楚(一 execution 一 controller、其他唯讀)。
- **新工作**：lease/reclaim、idempotency key、狀態機補全、observer/controller 分離(修 `handler.go:92` 多 subscriber 寫 stdin 的 race)、dispatch worker poll/claim。
- **量**：M
- **意義**：M0 是 demo，M1 才是 daily-driver。**這是「最小可用」的真正下限。**

### M2 · Provider 中立
- **能做什麼**：派工時選 claude 或 codex，都走訂閱、零 API token。
- **新工作**：`codex exec --json` launch adapter（已有 observe 骨架 `internal/agent/codex/provider.go`）+ 中性事件正規化統一。
- **量**：S–M

### M3 · Attempt + worktree
- **能做什麼**：同一 issue 可多次 attempt(重試/換做法/並行不同 agent)；execution 跑在隔離 worktree，diff 穩定可重現。
- **新工作**：attempt 資料層 + worktree-per-execution 生命週期(建/命名/清理孤兒)。
- **量**：M(簡版) → L(追平 VK)
- **取捨**：M0–M2 可省 worktree(單 execution/repo、dirty tree 自負)；要並行 + 可重現 review 就必須上。

### M4 · Diff review 迴圈（Ploom 最貴一塊）
- **能做什麼**：在 Ploom 內審 diff、inline comment、打包送回 agent follow-up、內嵌執行檢視(不再只 deeplink)。**完整「issue→執行→審查→再派」閉環。**
- **新工作**：`diff artifact → hunk anchor → review comment → follow-up command` 閉環 + Ploom 執行 review console UI(issue 詳情頁擴成工作單+執行觀測)。
- **量**：L（codex 認定 Ploom 側最貴、風險最高）

### M5+ · 追平 VK 花邊（逐項獨立、按需求排）
- PR 建立、dev server preview、多 attempt compare UI、完整 MCP surface、multi-host scheduler。

### Later · MM bridge（codex：最後）
- hermes 拉 agent/subagent 進 MM thread 即時對話(interactive lease 重機制)。等前面全穩再做。

---

## 規模總結
- **有用的閉環 = M0**（M，引擎已在）
- **可日常用 = M0+M1**（2 個 M）
- **不綁死一家 = +M2**（S–M）
- **追平 VK = 到 M4**（+L+L，約前面總和的 2–3 倍，非線性）
- MM bridge 在最後，非現在。

## 待 codex review 的疑問
1. M0 是不是正確的「最小可用」切法？deferring lease 到 M1 安全，還是 lease 必須進 M0？
2. execution_id 進 M0、attempt 延到 M3——這個「先穩定 handle、晚做 attempt」的順序會不會晚了？（因為 attempt_no 已出現在 ExecutionAccepted 契約）
3. worktree 延到 M3、M0–M2 直接在 repo dir 跑——單人自用這個降階的隱藏代價？
4. M4(diff review) 擺最後對嗎？還是它其實是「最小可用」的一部分(沒有 review 迴圈，派工結果只能看不能改)？
5. 有沒有里程碑之間的隱藏依賴或順序 hazard 我沒看到？
