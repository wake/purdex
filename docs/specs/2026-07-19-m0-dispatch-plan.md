# Implementation Plan: M0 — Ploom↔Purdex 派工整合

> **依據 spec**：`docs/specs/2026-07-19-m0-dispatch-integration.md`（三輪 codex 深審定稿，SOT commit `19e622b` 之 spec 版本）
> **PR 結構**（spec §17）：**PR-0 共享契約+golden fixtures** → **PR-Ploom-M0** ∥ **PR-Purdex-M0**（fixtures 就位後可平行，各以假對手測）
> **範圍**：跨兩 repo。Purdex＝本 worktree；Ploom＝`~/Workspace/wake/ploom`（0.1.29-dev，S6 spec PARKED 待疊五層）

## Overview

把 spec 拆成小而可驗的 TDD task。PR-0 先固定兩 repo 的共享 wire contract 與 golden fixtures（解耦錨點），之後 Ploom 與 Purdex 各以假對手平行開發、各自測綠，最後接真對手做一次端到端。

## Architecture Decisions（承 spec，開發時的錨）

- **契約優先**：PR-0 的 golden fixtures 是兩 repo 的唯一真相；任一側改契約＝改 fixtures＝雙側重跑。
- **execution_id 對外唯一 handle**；**crash-recovery handle=daemon 自選 `session_name`**（HasSession probe），`session_code`=衍生 deeplink handle。
- **liveness 看 `status ∈ {accepted,running}`**，`launch_state` 只當 relaunch fence。
- **durable 順序**：admission txn 落 row（含 accepted immutable facts）→ durable enqueue accepted → spawn；重啟由 row 重建 replay。
- **terminal**：時點認 process-exit、成敗認 `result.is_error`。
- **SOT**：Purdex runtime / Ploom projection；雙側兩表非雙寫。

---

## 依賴圖

```
PR-0 契約+fixtures
   ├──────────────► PR-Ploom-M0 ──┐
   └──────────────► PR-Purdex-M0 ─┤
                                   └──► 端到端接真對手（最後）
Purdex 內部（垂直重排）：
  P.1 module+store ─┐               P.2 relay seam(早·高風險) ─┐
  P.3 consumer ─────┤                                          │
  P.4 outbox+report ┼─► P.6 launch(durable cut) ─► P.7 垂直切片閉合
  P.5 admission ────┘                                          │
  P.8 outcome+artifact ◄─ P.2,P.4,P.6      P.9 reconcile+reclaim ◄─ P.4,P.6,P.8
  P.10 sandbox ◄─ P.5,P.6                  P.11 OS handler(早) ─► P.12 SPA route/resolver ◄─ P.6
Ploom 內部：L.0 S6疊五層 → L.1 tables → L.2 poll/claim/fetch ∥ L.3 report(含 deeplink compose)
             → L.4 intent → L.5 UI
```

---

## PR-0 · 共享契約 + Golden Fixtures

### Task 0.1: 契約 schema 定稿
**Description**：把 spec §3/§4 的 wire contract 寫成單一可引用的契約檔（含 `schema_version=1`、四端點 request/response、report 的 seq/ack_seq、error taxonomy、accepted-before-lifecycle ordering）。
**Acceptance**：
- [ ] 契約檔涵蓋 pending-list / claim(成功·同daemon冪等·他daemon409) / fetch / report(ack_seq·accepted_required) 全形狀
- [ ] **每欄位明列（防漏）**：`schema_version`、`execution_id`、`dispatch_id`(echo)、`attempt_no`、`provider`、`status`、`seq`、`ack_seq`、`repo_location`(echo)、`effective_sandbox_profile`、`head_at_start`、`dirty_at_start`、`session_code`(nullable)、`artifacts[]{kind,pointer,meta}`
- [ ] error taxonomy 列全（`accepted_required`/`dispatch_not_found`/`not_owner`/`already_claimed`/`schema_incompatible`/`stale_seq`/`unknown_sandbox_profile`）
- [ ] sandbox_profile enum + 偏序 + clamp + unknown/default 明列
- [ ] **SOT 對照 spec commit `19e622b`**（非更舊版）
**Verify**：人工對照 spec §3/§4/§8.1 逐條 checklist。
**Dependencies**：None
**Files**：`docs/specs/m0-contract.md`（Purdex repo；Ploom 引用同一份）
**Scope**：S

### Task 0.2: Golden fixtures
**Description**：每端點的 canonical request/response JSON 範例 + 錯誤案例 + seq/ordering 情境（accepted→running→terminal、亂序 seq、重複 claim），兩 repo 都對這組 fixture 寫 mock 測試。
**Acceptance**：
- [ ] 每端點 ≥1 成功 + ≥1 錯誤 fixture
- [ ] seq 冪等 / accepted-before-lifecycle / 重複 claim 各有情境 fixture
- [ ] fixtures 為純 JSON，可被 Go/TS 兩側載入
**Verify**：JSON schema validate 通過；兩側各寫一個 loader smoke test。
**Dependencies**：0.1
**Files**：`docs/fixtures/m0/*.json`
**Scope**：S

### Checkpoint PR-0
- [ ] 契約 + fixtures committed，兩 repo 開工前的唯一真相就位
- [ ] **人工 review**（可派 codex 審契約完整性）後才開 Ploom/Purdex

---

## PR-Purdex-M0 · 執行消費端 + runtime SOT

> **重排原則（codex plan review #1·#4·#8）**：垂直切片、durable outbox 在 launch 前、高風險 relay seam 早 fail-fast。第一個真垂直切片＝**P.7 後**（pending→claim→fetch→admission→accepted-enqueue→launch→running report）。module wiring 明列（#5）。

### Task P.1: execution module 骨架 + wiring + runtime store
**Description**：新增 `execution` module（`module.go` + `Dependencies()/Start()` + 於 `cmd/pdx` `registerServeModules()` 註冊，否則 store/worker/reconcile 都不會被接起）；execution row schema（spec §4.3 全欄位）+ CRUD + `execution_id` 生成 + 狀態機 + dispatch_id upsert 冪等。
**Acceptance**：
- [ ] execution module 註冊進 `registerServeModules`，daemon 起得來
- [ ] row 含 spec §4.3 全欄位（session_name 非NULL/session_code nullable/launch_state/outcome_source…）；同 dispatch_id upsert 回既有 execution_id
- [ ] 狀態轉移守衛（terminal 不回 running）
**Verify**：`go test ./internal/module/execution/...` + daemon 起動 smoke（module wiring）
**Dependencies**：0.2
**Files**：`internal/module/execution/{module.go,store.go,store_test.go,states.go}` + `cmd/pdx/main.go`(wiring)
**Scope**：M

### Task P.2: Relay/bridge terminal seam（早期 fail-fast spike）
**Description**：spec §5.3 + codex #4——**高風險先做**：讓 relay 於 subprocess 退出時經**既有 WS 傳一個結構化 terminal event（含 exit code）**（非只 close frame；**不改既有 streaming 行為**），daemon bridge/stream handler 把它冒到可訂閱的訊號。選定 Q1 判斷：relay 傳結構化 event、daemon 端分類（**不**做 daemon subprocess wait，child 在 relay 側 daemon 拿不到 handle）。
**Acceptance**：
- [ ] relay 退出時發結構化 terminal event（exit code），既有逐行 streaming 不回歸
- [ ] daemon 端可取得該 terminal 訊號（bridge/stream handler seam）
- [ ] 現有 stream 測試全綠（釘既行為）
**Verify**：`go test ./internal/relay/... ./internal/bridge/... ./internal/module/stream/...`
**Dependencies**：None（可與 P.1 平行；不需 execution store）
**Files**：`internal/relay/relay.go`、`internal/bridge/bridge.go`、`internal/module/stream/handler.go`（最小 seam + 測試）
**Scope**：M（**風險最高、故最早**）

### Task P.3: Dispatch module + consumer poll/claim/fetch
**Description**：新增 `dispatch` module（module.go + wiring）；輪詢 Ploom pending → claim → 兩段式 fetch。以 golden fixtures 假 Ploom 測。
**Acceptance**：
- [ ] dispatch module 註冊；poll→claim→fetch 對假 Ploom 跑通
- [ ] claim 他 daemon 409 跳過、同 daemon 冪等；schema_incompatible 拒
**Verify**：`go test ./internal/module/dispatch/...`（對 fixtures）
**Dependencies**：0.2, P.1
**Files**：`internal/module/dispatch/{module.go,worker.go,worker_test.go,client.go}` + wiring
**Scope**：M

### Task P.4: Outbox 表 + report client + ack cursor + accepted durability
**Description**：spec §3.3——**獨立 outbox 表**（codex Q2）；report client（retry/backoff、401/403 永久失敗、stale_seq）；ack_seq cursor；accepted-before-lifecycle ordering；daemon 重啟由 row 重建 accepted + replay 未 ack。**在 launch 前就位**（durable cut point 依賴，#1）。
**Acceptance**：
- [ ] 獨立 outbox 表；accepted 未 ack 不發 lifecycle（本地可續跑）
- [ ] 5xx 退避 / 401·403 永久失敗 / stale_seq 丟棄
- [ ] 重啟能從 row 重建 accepted 並 replay（不卡 accepted_required）
**Verify**：`go test`（durable/replay/ordering/ack cursor）
**Dependencies**：P.1
**Files**：`internal/module/dispatch/{outbox.go,report.go,report_test.go}`
**Scope**：M

### Task P.5: Admission rule（canonical + per-repo lock + single-live）
**Description**：spec §7——canonical repo path（symlink/`..`/trailing、逃出根→拒）+ per-canonical-repo lock 跨 accept→launch + single-live(`status∈{accepted,running}`) + head/dirty 快照。
**Acceptance**：
- [ ] symlink 與真實路徑解同一 canonical key（繞過被擋）
- [ ] 已有 live（status-based）→ 拒、error 明確
- [ ] 檢查+落 row+snapshot 在同一 per-repo lock 內原子
**Verify**：`go test`（乾淨/dirty/已有live/symlink/TOCTOU 序列化）
**Dependencies**：P.1
**Files**：`internal/module/execution/{admission.go,admission_test.go,canonical.go}`
**Scope**：M

### Task P.6: Launch fence + session_name + durable-ordered accepted→spawn
**Description**：spec §4.3/§5.2——admission txn 內：預生 `session_name`（execution_id deterministic）落 row → **durable enqueue accepted（經 P.4 outbox）** → `NewSession(session_name,cwd)` spawn → 寫 `launch_state=launched` + 補算 session_code。串起 admission+outbox+launch 的 durable cut。
**Acceptance**：
- [ ] 順序＝落 row → enqueue accepted → spawn → launched（crash 各點可 reconcile）
- [ ] session_name spawn 前非NULL；recovery 讀 launch_state∈{launching,launched} 不 relaunch
- [ ] session_code 建立後 `EncodeSessionID` 補算
**Verify**：`go test`（durable 順序；fence 不重複 launch；session_name 決定性）
**Dependencies**：P.4, P.5（沿用 `internal/module/session`+`internal/tmux`）
**Files**：`internal/module/execution/{launch.go,launch_test.go}`
**Scope**：M

### Task P.7: Consumer 串接（第一個垂直切片閉合）
**Description**：把 P.3(poll/claim/fetch) → 建 execution(P.1) → admission(P.5) → launch(P.6) → report accepted/running(P.4) 串成完整消費迴圈。對假 Ploom 端到端。
**Acceptance**：
- [ ] pending→claim→fetch→admission→accepted-ack→launch→running report 對假 Ploom 全跑通
- [ ] admission 拒的派工回 failed report
**Verify**：`go test`（端到端 against fixtures）；**首個可跑垂直切片**
**Dependencies**：P.3, P.4, P.5, P.6
**Files**：`internal/module/dispatch/worker.go`（串接）+ 整合測試
**Scope**：M

### Checkpoint A：seam + 垂直切片
- [ ] P.2 relay seam 後既有 stream 測試全綠（回歸 gate）
- [ ] P.7 後：pending→…→running report 端到端可跑

### Task P.8: Terminal outcome 分類 + artifact enqueue
**Description**：spec §5.3/§6/§4.2——消費 P.2 的 terminal 訊號做 outcome 分類（exit code + `result.is_error`/subtype、outcome_source）；terminal 時擷取 diff（相對 head_at_start，pointer+meta，**不 inline**）並**經 report enqueue 回 Ploom**（補 codex #6 缺口：artifact 一定進 projection）。
**Acceptance**：
- [ ] exit0+result.is_error→failed；exit0+ok→completed；exit0+無result→completed(outcome_source=exit_only)；result-before-exit 不過早 terminal
- [ ] diff pointer+meta（files/add/del）經 report artifacts[] enqueue，Ploom 收得到
**Verify**：`go test`（各 outcome 情境 + artifact 進 report payload）
**Dependencies**：P.2, P.4, P.6
**Files**：`internal/module/execution/{terminal.go,artifact.go,*_test.go}`
**Scope**：M

### Task P.9: Startup reconcile sweep + manual reclaim + 收孤兒
**Description**：spec §5.4——啟動掃 `status∈{accepted,running}`，`HasSession(session_name)` 探活→running/terminal；launching+session不在→failed+by-name 收孤兒；terminal 後解除 admission 阻塞；**+ manual reclaim**（daemon 端點/指令觸發把卡住 execution 拉回 reconcile，codex #2）；收斂靠 outbound replay(P.4)。
**Acceptance**：
- [ ] 探活用 session_name；launching crash→failed+收孤兒；reconcile terminal 後同 repo 可再派工
- [ ] **manual reclaim** 可人工觸發拉回卡住 execution
- [ ] reconcile 後未送 report 靠 outbox replay 補送
**Verify**：`go test`（crash × launch_state × session 存在矩陣 + manual reclaim）
**Dependencies**：P.4, P.6, P.8
**Files**：`internal/module/execution/{reconcile.go,reclaim.go,*_test.go}`
**Scope**：M

### Task P.10: Sandbox profile clamp
**Description**：spec §8.1——enum(`read-only⊏ask⊏workspace-write⊏danger-full`)+偏序+clamp(min)+unknown→reject+缺省+映射 `claude --permission-mode`。
**Acceptance**：
- [ ] request 寬於 host policy→clamp 到 host；unknown→`unknown_sandbox_profile` 拒；缺省依 host(預設 ask)
- [ ] 映射表落地 executor flag
**Verify**：`go test`（clamp/unknown/缺省/映射）
**Dependencies**：P.5, P.6（供 admission/launch 用 effective profile）
**Files**：`internal/module/execution/{sandbox.go,sandbox_test.go}`
**Scope**：S–M

### Checkpoint B：durable replay + 執行正確性
- [ ] P.4/P.9 crash 情境：launch 後 kill daemon→重啟 reconcile 收斂、replay 補 report、同 repo 可再派工
- [ ] P.8 outcome 分類 + artifact 進 report 驗證

### Task P.11: OS protocol handler（electron）
**Description**：spec §9——`electron/main.ts` 補 `setAsDefaultProtocolClient('purdex')` + `requestSingleInstanceLock` + `open-url`(mac)/`second-instance`(win/linux)，收到 `purdex://execution/<id>` broadcast 到 renderer（沿用 notification-click 管線模板）。
**Acceptance**：
- [ ] `purdex://execution/<id>` 喚起單一 app 實例並把 deeplink broadcast 到 renderer
**Verify**：手動喚起（需重打包 electron）
**Dependencies**：None（可早做）
**Files**：`electron/main.ts`
**Scope**：M

### Task P.12: SPA execution route + read path + resolver（observe-only）
**Description**：spec §9 + codex #3——SPA 現無 execution route/fallback（`route-utils.ts` 只有 history/hosts/settings/session/workspace）、通知 action 只有 open-session/open-host。新增 execution route + read path（活著 focus observe-only / 活不到落 execution 詳情頁），resolver 消費 P.11 broadcast，**不接 SubscriberToRelay 寫入**。
**Acceptance**：
- [ ] 新增 execution route + 詳情/搜尋落點（不落空）
- [ ] 活→focus tab observe-only（不寫 stdin）；不活→落詳情頁
- [ ] resolver 不掛寫入路徑
**Verify**：`vitest`（route + resolver 兩段）
**Dependencies**：P.6（活著 focus 依賴 session/session_code）, P.11
**Files**：`spa/src/lib/route-utils.ts`、`spa/src/hooks/useNotificationDispatcher.ts`、`spa/.../deeplinkResolver.{ts,test.ts}`、execution route/page component
**Scope**：M

### Checkpoint PR-Purdex-M0（含 UI/packaging gate）
- [ ] `go test ./...` + `vitest` + `pnpm lint` + `pnpm build` 全綠（codex sandbox 無網路→主 session 手動跑）
- [ ] **P.11/P.12 UI+packaging gate**：electron 重打包後 deeplink 喚起 + 兩段落點手驗
- [ ] 對假 Ploom 端到端：poll→claim→launch→report→diff→deeplink 跑通

---

## PR-Ploom-M0 · 派工 intent + projection（`~/Workspace/wake/ploom`）

> 前置：更新 Ploom `docs/specs/2026-06-30-s6-purdex-dispatch-design.md` 疊五層（execution projection 層 + execution_id handle + manual reclaim 拉進 M0）——可為本 PR 首個 doc task。

### Task L.0: S6 spec 疊五層（doc）
**Acceptance**：S6 spec 加 execution projection 層、execution_id 取代 session_code、對齊本 M0 契約與 §7 三題拍板。
**Verify**：人工對照 M0 契約。**Files**：`ploom/docs/specs/2026-06-30-...md`。**Scope**：S。**Deps**：0.1

### Task L.1: dispatch 表 + execution projection 表
**Description**：migration——`dispatch`（spec §3.1 + S6）+ `execution` projection（execution_id/status/deeplink/artifacts pointer/seq_acked）。
**Acceptance**：
- [ ] 兩表 migration（Ploom OCC 慣例）
- [ ] execution projection 綁 dispatch_id + issue_id
**Verify**：`go test`（migration up/down；OCC version）
**Dependencies**：L.0
**Files**：`ploom/internal/store/{dispatch.go,execution_projection.go,migrations/00NN_*.sql}`
**Scope**：M

### Task L.2: `/daemon/*` poll/claim/fetch endpoints
**Description**：spec §3.1——三端點；authz=daemon 綁定帳號 token（is_agent S4）；隔離只回 caller daemon；claim 原子 + 重複防護；兩段式 fetch。
**Acceptance**：
- [ ] 只回 caller daemon 自己的 dispatch（daemon_id + membership 雙層）
- [ ] claim pending→claimed 原子；他 daemon 409；同 daemon 冪等
- [ ] fetch 回完整 issue+repo_location+sandbox_profile
**Verify**：`go test`（authz 隔離、claim 原子、對 golden fixtures）
**Dependencies**：L.1, 0.2
**Files**：`ploom/internal/api/daemon_dispatch_handlers.go(+test)`
**Scope**：M

### Task L.3: `/daemon/dispatches/{id}/report` endpoint（含 deeplink compose）
**Description**：spec §3.3/§4——seq 去重 + ack_seq + accepted-before-lifecycle(未 ack accepted 發 lifecycle→409) + error taxonomy + 投影寫 `issue_event`（append-only）+ **組/存 deeplink**（accepted 後用 `daemon.host`(registration row，**optional hint**)+`execution_id` 組 `purdex://execution/<id>`，預留 `?host=`；無 host 也能靠 execution_id 落地）。〔codex #10：原 L.6 併入此，deeplink 只是 helper〕
**Acceptance**：
- [ ] seq≤ack 丟棄回 200+ack_seq；accepted_required 正確 409
- [ ] report 投影成 issue_event（上 IssueActivity 時間軸）；artifacts[] pointer/meta 存 projection
- [ ] accepted 後組並存 deeplink；`daemon.host` 缺時仍以 execution_id 成立
**Verify**：`go test`（seq 冪等、ordering、投影、deeplink compose、對 fixtures）
**Dependencies**：L.1, 0.2
**Files**：`ploom/internal/api/daemon_report_handler.go(+test)` + deeplink helper
**Scope**：M

### Task L.4: Issue 派工 intent
**Description**：人在 issue 按派工 → 建 dispatch row（pending）；錨點授權 = issue project **editor+**；先 commit dispatch row（spec §4.3 race 假設）。
**Acceptance**：
- [ ] 派工 API 建 pending dispatch，authz=editor+
- [ ] 一 issue 可多次 dispatch（歷史保留）
**Verify**：`go test`（authz、建 row）
**Dependencies**：L.1
**Files**：`ploom/internal/api/issue_dispatch_handler.go(+test)`
**Scope**：S–M

### Task L.5: IssueEditor 派工 UI + execution 狀態列
**Description**：`web/src/routes/IssueEditor.tsx` 加派工按鈕 + execution 狀態列（execution projection → issue_event → IssueActivity 時間軸；TanStack Query）。
**Acceptance**：
- [ ] 派工按鈕觸發 intent；狀態列顯示 queued/running/completed/failed
- [ ] deeplink 可點；diff summary meta 顯示
**Verify**：Ploom 前端測（依其慣例）+ 手動
**Dependencies**：L.3, L.4
**Files**：`ploom/web/src/routes/IssueEditor.tsx` + 相關 hook/component
**Scope**：M

> ~~L.6 Deeplink compose~~ → **併入 L.3**（codex #10：原 L.6 只 helper 性質、與 L.3 acceptance 重疊）。

### Checkpoint PR-Ploom-M0
- [ ] `go test ./...` 全綠；前端 build/test 綠
- [ ] 對假 daemon：pending→claim→fetch→report→投影+deeplink 跑通

---

## 端到端（兩 PR merge 後）
- [ ] seed 一個 daemon row（手動）→ Ploom issue 派工 → 真 Purdex daemon 領→跑 `claude -p`→狀態列走 queued→running→completed + diff 摘要 + deeplink 可點
- [ ] crash 情境手測：launch 後 kill daemon → 重啟 reconcile 收斂、同 repo 可再派工

---

## Risks and Mitigations
| Risk | Impact | Mitigation |
|------|--------|------------|
| P.4 觸及共用 relay/bridge 引入回歸 | High | 先寫測試釘現行為；只加 seam 不改橋接；最小 diff |
| durable outbox/replay(P.6) 複雜度被低估 | Med | 先做最小持久（execution row + outbox table），replay 只認 ack_seq |
| 跨 repo 契約漂移 | High | PR-0 golden fixtures 為單一真相；任一側改契約雙側重跑 |
| Ploom S6 疊五層動到 PARKED 設計 | Med | L.0 先 doc 對齊再寫碼；不解凍 S6d 範圍 |
| session_name 決定性生成與現有 session 命名衝突 | Med | 前綴命名空間 `pdx-exec-<id>`；HasSession 去重既有 |

## Open Questions — codex plan review 已收斂
1. **P.2 relay seam 形式** → **relay 經既有 WS 傳結構化 terminal event，daemon 端分類**（**非** daemon subprocess wait——child 在 relay 側 daemon 拿不到 handle）。✅ 定（並提前為早期 spike）。
2. **outbox 儲存** → **獨立 outbox 表**（replay 掃描/ack cursor/多筆 pending 清楚，不糊 P.1 邊界）。✅ 定（P.4）。
3. **daemon.host 來源** → **daemon registration row，且只當 optional hint**；無值也要能靠 `execution_id` 落地。✅ 定（L.3）。
4. **PR 順序** → 實作可在 PR-0 後平行，但 **merge/reality-check 順序 = PR-0 → Ploom → Purdex**（不再開放）。✅ 定。

> 已無 open question 阻塞開工。剩餘為 impl 級細節（隨 task 展開）。

## Verification（開工前）
- [x] 每 task 有 acceptance + verify + 依賴 + 檔案 + scope
- [x] 依賴序正確（PR-0→Ploom→Purdex；各 PR 內 bottom-up + 垂直切片）
- [x] 無 task >5 檔（P.6 report+outbox 已拆 P.4/P.6；P.10 deeplink 已拆 P.11/P.12）
- [x] 每 PR 有 checkpoint（Purdex 內加 Checkpoint A/B + UI gate）
- [x] module wiring 明列（P.1/P.3）；高風險 relay seam 提前（P.2）；durable outbox 在 launch 前（P.4→P.6）
- [x] codex plan review 一輪（10 findings + 4 題全採，本版已納）
- [ ] **人工審 plan 後才開工**（← 現在的 gate）
