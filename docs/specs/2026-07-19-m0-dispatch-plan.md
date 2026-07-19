# Implementation Plan: M0 — Ploom↔Purdex 派工整合

> **依據 spec**：`docs/specs/2026-07-19-m0-dispatch-integration.md`（三輪 codex 深審定稿 `60a8dc9`）
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
Purdex 內部：TP1 store → TP2 admission / TP3 launch-fence → TP4 terminal
             → TP5 consumer / TP6 report+replay → TP7 diff / TP8 reconcile
             （TP9 sandbox、TP10 deeplink 相對獨立，可早做）
Ploom 內部：TL1 tables → TL2 poll/claim/fetch / TL3 report → TL4 intent
             → TL5 UI → TL6 deeplink-compose
```

---

## PR-0 · 共享契約 + Golden Fixtures

### Task 0.1: 契約 schema 定稿
**Description**：把 spec §3/§4 的 wire contract 寫成單一可引用的契約檔（含 `schema_version=1`、四端點 request/response、report 的 seq/ack_seq、error taxonomy、accepted-before-lifecycle ordering）。
**Acceptance**：
- [ ] 契約檔涵蓋 pending-list / claim(成功·同daemon冪等·他daemon409) / fetch / report(ack_seq·accepted_required) 全形狀
- [ ] error taxonomy 列全（`accepted_required`/`dispatch_not_found`/`not_owner`/`already_claimed`/`schema_incompatible`/`stale_seq`/`unknown_sandbox_profile`）
- [ ] sandbox_profile enum + 偏序 + clamp + unknown/default 明列
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

### Task P.1: execution runtime store
**Description**：execution row schema（spec §4.3 全欄位）+ CRUD + `execution_id` 生成 + 狀態機（accepted/running/completed/failed）+ dispatch_id upsert 冪等。
**Acceptance**：
- [ ] row 含 spec §4.3 全欄位（session_name 非NULL / session_code nullable / launch_state / outcome_source…）
- [ ] 同 dispatch_id upsert 回既有 execution_id（不重建）
- [ ] 狀態轉移合法性守衛（不能從 terminal 回 running）
**Verify**：`go test ./internal/module/execution/...`（冪等、狀態機、欄位）
**Dependencies**：0.2
**Files**：`internal/module/execution/{store.go,store_test.go,states.go}`
**Scope**：M

### Task P.2: Admission rule（canonical + per-repo lock + single-live）
**Description**：spec §7——canonical repo path（resolve symlink/`..`/trailing、逃出允許根→拒）+ per-canonical-repo lock 跨 accept→launch + single-live（`status∈{accepted,running}`）+ head/dirty 快照。
**Acceptance**：
- [ ] symlink 與真實路徑解到同一 canonical key（繞過被擋）
- [ ] 已有 live execution（status-based）時新派工被拒、error 明確
- [ ] admission 檢查+落 row+snapshot 在同一 per-repo lock 內原子
**Verify**：`go test`（乾淨/dirty/已有live/symlink繞過/TOCTOU 併發兩派工序列化）
**Dependencies**：P.1
**Files**：`internal/module/execution/{admission.go,admission_test.go,canonical.go}`
**Scope**：M

### Task P.3: Launch fence + session_name handle
**Description**：spec §4.3/§5.2——daemon 自選 `session_name`（由 execution_id deterministic 生）於 admission txn 落 row → `NewSession(session_name,cwd)` 建 tmux session → 成功寫 `launch_state=launched`；launch_state 三態 fence。
**Acceptance**：
- [ ] session_name spawn 前落 row（非NULL）
- [ ] recovery 讀到 launch_state∈{launching,launched} 不 relaunch
- [ ] session_code 於建立後由 `EncodeSessionID` 補算入 row
**Verify**：`go test`（fence 不重複 launch；session_name 決定性；launched 後不再 relaunch）
**Dependencies**：P.1（沿用 `internal/module/session`+`internal/tmux`）
**Files**：`internal/module/execution/{launch.go,launch_test.go}`
**Scope**：M

### Task P.4: Terminal 偵測接線 + outcome 分類
**Description**：spec §5.3——把「subprocess 退出 + exit code」從 relay/bridge 冒到 execution 層當 terminal 時點；parse `result` event 做成敗分類（is_error/subtype）；outcome_source 記錄。**不改 relay 橋接本體**，只加訊號路由 seam。
**Acceptance**：
- [ ] process-exit（含 exit code）到達 execution 層觸發 terminal
- [ ] exit0+result.is_error→failed；exit0+result ok→completed；exit0+無result→completed(outcome_source=exit_only)
- [ ] result-before-exit 不過早標 terminal
**Verify**：`go test`（用 test seam 模擬 process exit + 各 result 情境；SIGTERM 無 result→failed）
**Dependencies**：P.1；謹慎觸及 `internal/relay`+`internal/bridge`
**Files**：`internal/module/execution/{terminal.go,terminal_test.go}` + relay/bridge seam（最小）
**Scope**：M（風險：碰共用 relay，先寫測試釘行為）

### Task P.5: Dispatch consumer worker（poll/claim/fetch）
**Description**：spec §3——輪詢 Ploom pending → claim → 兩段式 fetch → 建 execution（P.1）→ 過 admission（P.2）→ launch（P.3）。以 fixtures 的假 Ploom 測。
**Acceptance**：
- [ ] poll→claim→fetch→launch 全鏈路對假 Ploom 跑通
- [ ] claim 409（他 daemon）正確跳過；同 daemon 冪等
- [ ] schema_incompatible 明確拒
**Verify**：`go test`（對 golden fixtures 假 Ploom）
**Dependencies**：0.2, P.1, P.2, P.3
**Files**：`internal/module/dispatch/{worker.go,worker_test.go,client.go}`
**Scope**：M

### Task P.6: Report + durable outbound queue + ack cursor + replay
**Description**：spec §3.3/§4.3——durable outbound report queue；accepted(seq=1) 先 ack 才發 lifecycle；ack_seq cursor；daemon 重啟由 row 重建 accepted + replay 未 ack。
**Acceptance**：
- [ ] accepted 未 ack 前不發 running（本地可續跑）
- [ ] 5xx 指數退避；401/403 永久失敗；stale_seq 丟棄
- [ ] daemon 重啟能從 row 重建 accepted 並 replay（不卡 accepted_required）
**Verify**：`go test`（durable queue 持久化/replay；ordering；ack cursor 推進）
**Dependencies**：P.1, P.5
**Files**：`internal/module/dispatch/{report.go,report_test.go,outbox.go}`
**Scope**：M

### Task P.7: Diff 擷取（pointer-first）
**Description**：spec §6——execution 完成後 `git diff` 相對 `head_at_start` → 產 daemon-scoped pointer + summary meta（files/add/del）；transcript pointer 指既有紀錄。
**Acceptance**：
- [ ] diff 相對 head_at_start（非 HEAD）
- [ ] 回 pointer + meta，**不 inline blob**
- [ ] pointer 可經 daemon 取回全貌
**Verify**：`go test`（固定 repo fixture 算 diff summary；pointer 解析）
**Dependencies**：P.1, P.4
**Files**：`internal/module/execution/{artifact.go,artifact_test.go}`
**Scope**：S–M

### Task P.8: Startup reconcile sweep + 收孤兒
**Description**：spec §5.4——啟動掃 `status∈{accepted,running}`；`HasSession(session_name)` 探活→running/terminal；launching+session不在→failed+by-name 收孤兒；terminal 後解除 admission 阻塞。
**Acceptance**：
- [ ] 探活用 session_name（非 session_code）
- [ ] launching crash→failed 且收孤兒（by-name kill）
- [ ] reconcile 成 terminal 後同 repo 可再派工
**Verify**：`go test`（模擬 crash 後各 launch_state × session 存在與否矩陣）
**Dependencies**：P.1, P.3, P.4
**Files**：`internal/module/execution/{reconcile.go,reconcile_test.go}`
**Scope**：M

### Task P.9: Sandbox profile clamp
**Description**：spec §8.1——enum(`read-only⊏ask⊏workspace-write⊏danger-full`)+偏序+clamp(min)+unknown→reject+缺省+映射 `claude --permission-mode`。
**Acceptance**：
- [ ] request 寬於 host policy → effective clamp 到 host policy
- [ ] unknown profile→`unknown_sandbox_profile` 拒；缺省依 host（預設 ask）
- [ ] 映射表落地到實際 executor flag
**Verify**：`go test`（clamp 偏序；unknown/缺省；映射）
**Dependencies**：P.1
**Files**：`internal/module/execution/{sandbox.go,sandbox_test.go}`
**Scope**：S–M

### Task P.10: Deeplink handler + SPA resolver（observe-only）
**Description**：spec §9——electron main 補 `setAsDefaultProtocolClient('purdex')` + single-instance + open-url/second-instance；SPA resolver 沿用 notification-click 管線，兩段落點（活著 focus observe-only / 活不到落 execution 詳情頁），**不接 stdin 寫入**。
**Acceptance**：
- [ ] `purdex://execution/<id>` 喚起 app 並路由
- [ ] execution 活→focus tab（observe-only，不寫 stdin）；不活→落詳情頁不落空
- [ ] 不把 deeplink view 接上 SubscriberToRelay 寫入路徑
**Verify**：`vitest`（resolver 兩段邏輯）；手動喚起驗證（需重打包 electron）
**Dependencies**：P.1
**Files**：`electron/main.ts` + `spa/.../deeplinkResolver.{ts,test.ts}`
**Scope**：M

### Checkpoint PR-Purdex-M0
- [ ] `go test ./...` + `vitest` + `pnpm lint` + `pnpm build` 全綠（codex sandbox 無網路→主 session 手動跑）
- [ ] 對假 Ploom 端到端：poll→claim→launch→report→diff 跑通

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

### Task L.3: `/daemon/dispatches/{id}/report` endpoint
**Description**：spec §3.3/§4——seq 去重 + ack_seq + accepted-before-lifecycle(未 ack accepted 發 lifecycle→409) + error taxonomy + 投影寫 `issue_event`（append-only）。
**Acceptance**：
- [ ] seq≤ack 丟棄回 200+ack_seq；accepted_required 正確 409
- [ ] report 投影成 issue_event（上 IssueActivity 時間軸）
- [ ] 組/存 deeplink（見 L.6）
**Verify**：`go test`（seq 冪等、ordering、投影、對 fixtures）
**Dependencies**：L.1, 0.2
**Files**：`ploom/internal/api/daemon_report_handler.go(+test)`
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

### Task L.6: Deeplink compose
**Description**：report accepted 後，Ploom 用 `daemon.host` + `execution_id` 組 `purdex://execution/<id>`（預留 `?host=`）存 projection。
**Acceptance**：deeplink 正確組出並存、供 UI 顯示。
**Verify**：`go test`。**Files**：`ploom/internal/.../deeplink.go(+test)`。**Scope**：S。**Deps**：L.3

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

## Open Questions（plan 階段展開，多數 impl 級）
1. **P.4 relay seam 形式**：exit code 經 WS close frame 帶回，還是新增 daemon 端 subprocess wait？（plan→impl 首查）
2. **P.6 outbox 儲存**：獨立 outbox 表 vs execution row 內嵌 pending report 欄位？（傾向獨立表，便於 replay 掃描）
3. **Ploom daemon.host 來源**：deeplink 的 host 從 daemon 註冊 row 的 network 位址取？（seed daemon 時一併寫）
4. **PR 順序**：Ploom/Purdex 真平行，還是先 merge PR-Ploom 定端點再 Purdex？（codex：先 Ploom 契約端；但 fixtures 就位後技術上可平行）

## Verification（開工前）
- [x] 每 task 有 acceptance + verify + 依賴 + 檔案 + scope
- [x] 依賴序正確（PR-0→兩側；各 PR 內 bottom-up）
- [x] 無 task >5 檔（P.5/P.6 接近上限，必要時再拆）
- [x] 每 PR 有 checkpoint
- [ ] **人工/codex 審 plan 後才開工**
