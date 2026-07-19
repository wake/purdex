# Spec: M0 — Ploom↔Purdex 派工整合（Walking Skeleton）

> **狀態**：草稿（待 codex 審 → plan）
> **里程碑**：M0（第一個有用的閉環；demo-able 但脆）。最小可用線在 M0→M1.5，M0 是起點。
> **範圍**：跨 Ploom + Purdex 兩 repo。傳輸走 **pull 模型**（Purdex daemon 輪詢 Ploom）。
> **關聯**：疊在 Ploom `docs/specs/2026-06-30-s6-purdex-dispatch-design.md`（PARKED，pull 骨架與 §7 拍板均在）之上，以五層資料模型 + `execution_id` 穩定 handle 擴充。

---

## 1. Objective

**把 work item 與 execution 對上、讓 session 不再瞬時。** 使用者在 Ploom issue 按「派工」，Purdex daemon 領工、在指定 repo 用既有 `claude -p` 引擎跑起一次 execution，狀態與產出（diff / transcript pointer）回填到 issue 活動流，並提供 deeplink 讓人跳進 Purdex 看即時。

- **User**：單人自用（dogfooding），單 daemon / 單 host。
- **成功長相**：Ploom issue 上看得到 `queued → running → completed/failed` 狀態列 + 一個可點的 deeplink + 完成後 diff 摘要回填。整條鏈路端到端跑通一次。
- **核心痛點解除**：session 做完就散、無處組織/追蹤 → issue 成為 execution 的家。

### 1.1 五層資料模型（本 spec 的骨幹）

```
issue ──> dispatch ──> execution ──> attempt ──> session
 (Ploom)   (Ploom)      (雙側投影)     (M0 恆=1)   (Purdex runtime)
```

- **穩定對外 handle = `execution_id`**（不是 `session_code`，不是 `dispatch_id`）。deeplink、狀態回報、Ploom 投影全綁 `execution_id`。
- **M0 attempt 恆為 1**（最薄）；attempt 一等概念的資料層留到 M3。但 `attempt_no` 欄位從第一天就在契約裡（=1），避免晚抽 handle。
- **SOT 分工**：**Purdex = runtime SOT**（execution 真實狀態機的權威）；**Ploom = projection SOT**（只投影 runtime 狀態、只產 dispatch intent，**不推進** runtime 狀態機）。

### 1.2 M0 刻意不做（defer 對照）

| 不做 | 去哪 |
|------|------|
| lease / reclaim（斷線保命）| M1 |
| idempotent 重派、controller/observer 分離、修 `handler.go:92` 多 subscriber 寫 stdin race | M1 |
| codex launch adapter（provider 中立**接縫** M0 做，**live** 留 M1）| M1 |
| worktree 隔離 | M3（M0 靠 admission rule 擋，見 §7）|
| attempt 資料層（多次嘗試）| M3 |
| inline diff review console / follow-up 迴圈 | M4 |
| `ExecutionControlRequest`（stop/interrupt）| M1（M0 契約 reserved/forward-ref，見 §4.4）|
| `plm daemon register/list` CLI | M1+（M0 手動 seed daemon row）|
| 內嵌執行 UI | M0 只給 deeplink |

---

## 2. Tech Stack

- **Ploom** (`~/Workspace/wake/ploom`，0.1.29-dev)：Go net/http（httpx router→handler 直呼 store，無 service 層）；modernc SQLite 單寫連線 + OCC version；authz 純函式；React19+Vite8+RR7+TanStack Query v5 前端。
- **Purdex** (`bin/pdx`，1.0.0-alpha.324)：Go net/http + gorilla/websocket + creack/pty + modernc SQLite；既有 `claude -p` relay（`internal/relay/relay.go`）+ handoff（`internal/config/config.go:79`）+ sessions/stream API（`internal/module/stream/`）；Electron shell（`electron/main.ts`）。
- **傳輸**：HTTP，pull 方向（daemon → Ploom）。認證 Bearer（daemon 綁 is_agent 帳號 S4 token）。

---

## 3. 傳輸與 API（Pull 模型）

> Ploom 是純 server，**無 inbound dispatch endpoint 打到 Purdex**。所有動作 daemon 發起：輪詢領工 → claim → report。這修正了先前 push 草案的方向錯誤。

```
[Ploom :server] ── GET /daemon/dispatches?status=pending ─┐   輪詢領工(最小清單)
                ── POST /daemon/dispatches/{id}/claim ─────┤   原子 pending→claimed
                ── GET  /daemon/dispatches/{id} ───────────┤   兩段式②抓完整(issue+repo_location)
                ── POST /daemon/dispatches/{id}/report ────┘   回報 execution 狀態/artifact pointer
       ▲
       │ 起 claude -p、組 execution、擷取 diff —— 全在 Purdex daemon 端做
[Purdex daemon]
```

### 3.1 Ploom daemon 端點（authz = daemon 綁定帳號 token；dispatch 錨點 = issue project **editor+**）

| Endpoint | 作用 | 隔離 |
|----------|------|------|
| `GET /daemon/dispatches?status=pending` | 回**最小清單**（`dispatch_id` + `issue_id` + `schema_version`），**只回屬於 caller daemon 的**〔兩段式①〕 | daemon_id + 帳號 membership 雙層 |
| `POST /daemon/dispatches/{id}/claim` | 原子 `pending→claimed`（寫 `claimed_at`, `daemon_id`）；重複 claim 防護（已 claimed 回 409/冪等回既有）| — |
| `GET /daemon/dispatches/{id}` | 兩段式②：回完整 issue 內容 + `repo_location`（S5 既有）+ `sandbox_profile`(request) | 限 caller daemon |
| `POST /daemon/dispatches/{id}/report` | 回報 `{execution_id, attempt_no, status, seq, artifacts?[], error?}`；Ploom 投影 + 組 deeplink | seq 去重、只吃最新 |

### 3.2 兩段式抓取

- **①輪詢**：只回 `dispatch_id + issue_id`（省頻寬、快）。
- **②領工後**：`GET /daemon/dispatches/{id}` 才回完整 issue + repo_location + sandbox_profile。

### 3.3 Report 的可靠性語意（daemon → Ploom）

> **注意**：pull 模型下沒有 Ploom→Purdex callback。「回報」= daemon 主動 POST report。以下語意套在 report 呼叫上。

- **Auth**：daemon 帳號 S4 Bearer token。
- **Retry/backoff**：Ploom 回 5xx → daemon 指數退避重試；401/403 → **永久失敗**（不重試，記錄）。
- **Seq 去重**：每筆 report 帶**單調遞增 `seq`**（per execution）；Ploom **只吃最新 seq**，舊/重複 seq 丟棄（冪等）。
- **狀態單向**：Ploom 只投影 daemon 給的 runtime 狀態，**不自行推進**（projection SOT）。

---

## 4. 事件契約（邏輯層，映射到 pull 傳輸）

> 事件是**邏輯概念**，傳輸走 pull（不是 push message bus）。schema 帶版本，欄位穩定。

### 4.1 `schema_version`

所有 daemon↔Ploom payload 帶 `schema_version`（M0 = `1`）。Ploom 與 daemon 皆檢查；不相容版本 → 明確拒絕（非 silent）。

### 4.2 邏輯事件 ↔ pull 動作對照

| 邏輯事件 | pull 傳輸落點 | 內容 |
|----------|--------------|------|
| `DispatchRequested` | Ploom `dispatch` row（pending），daemon `GET pending` 讀到 | `dispatch_id, issue_id, schema_version` |
| `ExecutionAccepted` | daemon claim 後首次 `report`（status=`accepted`）| `execution_id, attempt_no(=1), dispatch_id(echo), repo_location(echo), effective_sandbox_profile, session_code(nullable), head_at_start, dirty_at_start` |
| `ExecutionLifecycle` | daemon `report`（status=`running`/`completed`/`failed`）| `execution_id, status, seq, error?` |
| `ExecutionArtifact` | daemon `report` 帶 `artifacts[]`（**pointer-first**，見 §6）| `execution_id, artifacts[]{kind, pointer, meta}` |

### 4.3 冪等與 race（Ploom 先寫 dispatch row → daemon 動作）

- **建立**：Ploom 派工時**先 commit `dispatch` row（pending）**再無他事；daemon 之後才輪詢到。
- **execution 冪等**：daemon 在**單一交易內**以 `dispatch_id` upsert execution 投影——若該 `dispatch_id` 已有 execution，回**既有 `execution_id`**（不重建）。防「claim 後 daemon 重啟又跑一次」造成雙 execution。
- **execution row 欄位**（Purdex 側 runtime SOT）：`execution_id, dispatch_id, repo_location, provider, callback_target(nullable/pull 下多半空), session_code(nullable), attempt_no, status, head_at_start, dirty_at_start, sandbox_profile, created_at, updated_at`。
- **雙側兩表非雙寫**：Purdex 存 execution runtime row；Ploom 存 execution **projection** row（+ 寫進 `issue_event` append-only 活動流）。兩表各自權威（runtime vs projection），靠 report 同步，**不共用一張表、不雙寫**。

### 4.4 `ExecutionControlRequest` — M0 移除（V1）

stop / interrupt 等控制**整個移出 M0 契約**，改 **reserved / forward-ref**（欄位保留位、不實作行為）。M1 才做（需要 interactive lease 前置，見 M1 spec）。

---

## 5. Execution 生命週期 + `claude -p` 接線

### 5.1 狀態機（Purdex runtime SOT）

```
accepted ──> running ──> completed
   │            │
   └──> failed <┘          (failed: 起不來 / 執行錯 / -p 非零退出)
```

- Ploom projection 鏡像此狀態；issue 層「關單/done」是**人工 gate**（Ploom 不自動關 issue）。

### 5.2 沿用既有 `claude -p`（不新寫、不改 runner）

- **起 session**：沿用既有 handoff / stream 啟動路徑（`config.go:79` 的 `claude -p --verbose --input-format stream-json --output-format stream-json`）+ `relay.go` 橋接。M0 **不改 relay/runner 本體**。
- **execution wrapper**：新增薄層——以 `execution_id` 包住「一次 session 啟動」，記 runtime row、綁 `dispatch_id`。

### 5.3 ⚠️ 薄接線點（明列為 task，非假設自動有）

- **terminal 訊號**：從 `-p` stream-json 的 `result` event 接出「這條 execution 結束（含 exit 狀態）」，冒到 execution 層 → 觸發 `completed`/`failed` report。**relay 已 parse stream-json，但把此訊號路由到 execution 層是新工作**，不得假設現成。

---

## 6. Artifacts — Pointer-first（V2）

- **report 只帶 metadata / pointer，不 inline diff / transcript blob**。
- **M0 兩種 artifact**：
  - `diff`：pointer = daemon 端可取回的 diff 位址（deeplink 或 daemon-local ref）+ meta（檔案數、+/- 行數摘要）。issue 上顯示摘要，點 deeplink 看全貌。
  - `transcript`：pointer 指向 session transcript（既有 JSONL / stream 紀錄），非 blob 回填。
- **diff 擷取**：execution 完成後 daemon 端 `git diff`（相對 `head_at_start`）→ 產 pointer + summary meta。**diff 擷取本身 provider-agnostic**（claude/codex 皆同）。

---

## 7. Admission Rule（V3）— 無 worktree 下的資料安全

> M0 不上 worktree（M3 才做），故需 admission rule 防 dirty tree / 併發資料事故。codex：不補後面一定 rework。

**派工被 daemon 受理（accepted）的前置條件**（任一不滿足 → `failed`，error 明列原因）：

1. **repo 乾淨 OR 同 repo 單一 live execution**：目標 repo 若已有一條 live（accepted/running）execution → 拒新派工（或 dirty tree 且無 live → 拒）。單 repo 同時只允許一條 live execution。
2. **記錄 `head_at_start` + `dirty_at_start`**：受理時快照 repo HEAD commit 與 dirty 狀態，寫進 execution row（供 diff base 與事後稽核）。

---

## 8. Sandbox（A 軸）— M0 必須定，least privilege

> A 軸（sandbox / permission，防 agent 亂搞）≠ B 軸（worktree / base_commit，git 隔離）。worktree 不是 security sandbox。A 軸 M0 定，B 軸延 M3。

- **契約帶 `sandbox_profile`**：dispatch 只能 **request** 一個 profile；**Purdex daemon 持 host policy 為唯一權威**。
- **Clamp（只降不升）**：daemon 對 request 的 profile 做 clamp——**只能比 host policy 更嚴，不能更寬**。`ExecutionAccepted` 回 `effective_sandbox_profile`（clamp 後實際值）。
- **M0 執行層 profile**：對應 executor 的 permission 模式（`claude --permission-mode`）。OS 級隔離（container/VM）與更細 host 邊界留後續，但**契約欄位 M0 就在**。
- 定調：sandbox = **blast-radius reduction，非硬隔離**。

---

## 9. Deeplink

- **格式**：`purdex://execution/<execution_id>`，預留 `?host=<hint>` query（單 daemon M0 可省，但格式保留）。
- **Ploom 側**：report `accepted` 後，Ploom 用 `daemon.host` + `execution_id` 組 deeplink 存 projection、顯示在 issue。
- **Purdex 側（OS protocol handler — M0 缺口，需實作）**：
  - `electron/main.ts` 現**無** `setAsDefaultProtocolClient('purdex')` / `requestSingleInstanceLock` / `open-url`（macOS）/ `second-instance`（win/linux）。M0 要補。
  - **resolver 沿用既有 notification-click 管線**（main 收 deeplink → broadcast 到 renderer → SPA 決定誰有 tab → `notification:focus-window` 回呼；模板 `electron/main.ts:78-103` + `useNotificationDispatcher.ts:281`）。
  - **M0 resolver 落點**：execution 活著 → attach/focus 對應 tab；活不到（M0 最小）→ 至少開 Purdex 並帶 execution_id 讓 SPA best-effort 定位。三段式完整落點（snapshot 重建 / JSONL 唯讀）留後續。

---

## 10. Provider 中立接縫（M0 做接縫，claude-only live）

- **execution row + 契約帶 `provider` 欄位**（M0 恆 `claude`）。
- **relay 輸出映射到中性內部事件型別**——不讓 claude stream-json 原始 JSON 往 execution 層 / Ploom 契約漏。
- **M0 只實作 claude launch**；codex launch adapter（第二套 `codex exec --json` parser + 正規化）留 M1。中立接縫的目的正是讓 M1 只需「加 parser + config entry」，非重構。

---

## 11. Commands

```
# Purdex（在 worktree 內）
Daemon build:   go build -o bin/pdx ./cmd/pdx        # 或既有 build 腳本
Daemon test:    go test ./...
SPA test:       cd spa && npx vitest run
SPA lint:       cd spa && pnpm run lint
SPA build:      cd spa && pnpm run build
Electron build: cd spa && pnpm run electron:build     # deeplink handler 改動需重打包

# Ploom（在其 repo）
Build/test:     go test ./...
Web:            cd web && pnpm ...（依 Ploom 慣例）
```

---

## 12. Project Structure（新增/觸及）

**Purdex**
```
internal/module/dispatch/        → 新：dispatch 消費端（poll/claim/兩段式 fetch/report worker）
internal/module/execution/       → 新：execution runtime SOT（execution_id、狀態機、admission rule、diff 擷取）
  ├─ 中性事件型別（provider-neutral）
  └─ -p result 訊號 → execution terminal 接線
internal/relay/                  → 觸及：把 result event 訊號路由出來（不改橋接本體）
electron/main.ts                 → 觸及：purdex:// protocol handler + single-instance + resolver
spa/.../deeplink resolver        → 觸及：沿用 notification-click 管線定位 execution tab
docs/specs/2026-07-19-m0-dispatch-integration.md  → 本 spec
```

**Ploom**
```
internal/store/dispatch*.go      → 新：dispatch 表 + execution projection 表
internal/api/daemon_*.go         → 新：/daemon/dispatches[...] 4 端點（pull）
internal/api/...issue dispatch   → 新：issue「派工」intent 建立 dispatch row
web/src/routes/IssueEditor.tsx   → 觸及：派工按鈕 + execution 狀態列（execution → issue_event → IssueActivity 時間軸）
docs/specs/2026-06-30-s6-...md   → 更新：疊五層 + execution_id + pull（S6 §7 三題已拍板）
```

---

## 13. Code Style

- **Go**：match 既有——Purdex `internal/module/*` 分層、Ploom httpx→handler→store(OCC)→authz(純函式)。錯誤明確 wrap，不吞。
- **契約 payload**（JSON，`schema_version` 必帶）：
  ```json
  {
    "schema_version": 1,
    "execution_id": "exc_...",
    "dispatch_id": "dsp_...",
    "attempt_no": 1,
    "provider": "claude",
    "status": "running",
    "seq": 3,
    "effective_sandbox_profile": "...",
    "artifacts": [{"kind": "diff", "pointer": "...", "meta": {"files": 4, "add": 120, "del": 8}}]
  }
  ```

---

## 14. Testing Strategy

- **Purdex Go**：`go test ./...`。重點——admission rule（乾淨/dirty/已有 live execution 三態）、execution 狀態機、seq 冪等、`-p` result→terminal 接線（用 test seam 模擬 stream-json result event）、diff 擷取相對 head_at_start。
- **Purdex SPA**：`vitest`。deeplink resolver 定位邏輯。
- **Ploom Go**：`go test ./...`。daemon 端點 authz 隔離（只回 caller daemon）、claim 原子性/重複防護、report seq 去重、dispatch_id upsert 冪等（同 dispatch 回既有 execution）。
- **端到端**：一次手動閉環驗證（seed daemon → issue 派工 → 觀察 issue 狀態列走 queued→running→completed + diff 摘要 + deeplink 可點）。
- **codex sandbox 無網路**：SPA 任務主 session 手動 `pnpm install` + vitest/lint/build 驗證。

---

## 15. Boundaries

- **Always**：先寫測試再實作（TDD）；每 task 獨立 commit；契約帶 `schema_version`；`execution_id` 當唯一對外 handle；report 帶單調 seq；admission rule 先擋再跑。
- **Ask first**：跨 repo 的 Ploom schema 變更順序（先 Ploom row 後 daemon 動作的 race 假設）；新增相依；sandbox profile 對應的實際 permission 值；PR 切法（M0 是否再拆 Ploom-M0 / Purdex-M0）。
- **Never**：讓 Ploom 自行推進 runtime 狀態機（違反 projection SOT）；雙寫 execution 表；inline diff/transcript blob 回填（違反 pointer-first）；把 stop/interrupt 塞進 M0；直推 main（走 PR + codex 兩輪 review）；worktree 內 Edit/Write 漏帶 `.claude/worktrees/` 前綴。

---

## 16. Success Criteria（具體可測）

1. **閉環跑通**：seed 一個 daemon row → Ploom issue 按派工 → daemon 輪詢領到 → claim → 起 `claude -p` → issue 狀態列 `queued→running→completed`。
2. **execution_id 穩定 handle**：deeplink `purdex://execution/<id>` 可點，Purdex 開起並定位（execution 活著時 attach 對應 tab）。
3. **diff 回填**：完成後 issue 顯示 diff 摘要（檔案數 + 增刪行數），pointer 可取全貌。
4. **冪等**：同 dispatch 重複領/report 不產生第二條 execution；亂序/重複 seq 被丟棄。
5. **admission rule**：目標 repo 已有 live execution 時，新派工被拒且 error 明確。
6. **sandbox clamp**：request 一個比 host policy 寬的 profile → `effective_sandbox_profile` 被 clamp 到 host policy。
7. **SOT 分工**：Ploom 只投影、不推進；Purdex runtime 為狀態權威。
8. **測試全綠**：Purdex `go test ./...` + `vitest` + `lint` + `build`；Ploom `go test ./...`。

---

## 17. PR / Phase 切法（待 codex 確認）

M0 是一個里程碑，但跨兩 repo。建議切法：

- **PR-Ploom-M0**：dispatch 表 + execution projection 表 + 4 個 `/daemon/*` 端點（pull）+ issue 派工 intent + IssueEditor 狀態列。
- **PR-Purdex-M0**：dispatch 消費端（poll/claim/fetch/report worker）+ execution runtime SOT（狀態機/admission/diff/中性事件）+ `-p` result 接線 + deeplink handler + SPA resolver。
- 兩者靠**契約（§4）**解耦，可用假對手獨立測（Ploom 假 daemon / Purdex 假 Ploom queue）。

> 每個 PR 內部再依 TDD task 切分（見後續 plan）。

---

## 18. Open Questions（待 codex / user）

1. **PR 切法**：M0 再拆 Ploom-M0 / Purdex-M0（§17）妥當，還是合一個 PR 但內部 phase 切？（跨 repo 合一 PR 不可行 → 傾向拆兩 PR，此題主要問 review 順序：先 Ploom 定契約端還是先 Purdex 消費端。）
2. **deeplink resolver M0 落點深度**：M0 只做「execution 活著→attach tab」+「活不到→開 app 帶 id」兩段就好，還是要把 snapshot 重建也拉進 M0？（傾向兩段，snapshot 重建留後續。）
3. **repo_location canonical**：M0 對 `repo_location.local_dir` 要不要就做 canonical path + symlink escape 檢查，還是信任 S5 既有值？（安全題，傾向 M0 就做最小 canonical，因 admission rule 依賴它。）
4. **`-p` result 接線的測試 seam**：relay 現有沒有可注入的 result-event seam，還是要新開？（影響 §5.3 task 大小；plan 前需確認 relay 現況。）
