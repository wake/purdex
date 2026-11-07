# 壓縮控制 Phase 2–5 — plan

> **For agentic workers:** 用 superpowers:subagent-driven-development 逐 task 實作（subagent 帶 `model: "sonnet"`）；步驟用 `- [ ]` 追蹤。

**目標**：壓縮改由 Purdex mod 在回合結束時觸發，門檻、自動、閒置三個值可以在主機預設與每個 session 上調，lead 也能管 member 的設定。

**架構**：daemon 存設定並算生效值（host config 新列 `compaction`＋team.db 新表 `session_compaction`），透過 mod 每 15 秒讀一次的 `/mod/v1/team` 交給 mod。register.js 在 `turn.complete` 判斷要不要 `$.session.compact()`，在 `session.compact` 擋閒置壓縮。App 讀寫 `/api/team/compaction/{session_id}`；lead 用 `pdx compact` 走另一條帶 `origin_inbox` 的路由。

**技術**：Go（`internal/module/hostconfig`、`internal/module/team`、`internal/modevents`、`internal/module/dialogue`、`internal/promptq`、`cmd/pdx`）、mod JS（`cmd/pdx/plugin/purdex/hooks`）、SPA（React、Zustand、vitest）。

**Spec**：`docs/specs/2026-11-07-compact-control-spec.md`（以下稱 spec）§5–§12、§13 Phase 2–5。Phase 1 另見 `docs/plans/2026-11-07-compact-control-phase1-plan.md`，本 plan 假設它已合併。

**流程**：完整開發流程。本 plan 帶 spec 給 codex 審一輪 → subagent TDD → 每張 PR 跑 codex R1 ＋ R2 → merge。七張 PR 依序：2a → 2b → 3 → 4a → 4b → 5a → 5b（4a、4b 只依賴 2a，可以和 3 並行開發，但一次只開一位 implementer）。全部合併後一起 bump、一起部署。每個 task 一個 commit。

## 我定的事（spec 沒寫明）

1. **主機預設的讀取**：`compaction` 列跟其他列一樣出現在 `GET /api/hostconfig` 的回應裡，不另開單列 GET；寫入是 `PUT /api/hostconfig/compaction`。spec §7 寫「照其他 host config 列」，現有的列都是這樣。
2. **lead 走另一條路由**：`POST /api/team/member-compaction`，帶 `origin_inbox`，用既有的 `callerTeam`／`matchMember` 確認是自己的 member。App 的 `/api/team/compaction/{session_id}` 沒有身分檢查，「agent 不能改自己的設定」照無人值守的做法守：路由字串不出現在 `cmd/pdx` 與 skill 裡，用 guard 測試釘住。
3. **lead 改的是部分欄位**：`pdx compact --threshold 50` 只改門檻，其他覆寫不動；App 的 PUT 照 spec 整份取代。
4. **接力沿用**：自我接力與 member 接力都複製覆寫（spec §5.3 沒分種類）。
5. **daemon 讀不到時**：host config 列讀壞了用內建預設（＝今天的行為）。`selfRelayState` 出錯時，`/mod/v1/team` 不帶 `compaction`（mod 保留上一次），App 的 GET 回 500。
6. **mod 判斷「有沒有設定」**：成功讀到但沒有 `compaction`（舊 daemon）或格式不對＝不介入；讀失敗＝保留上一次；session 換 id＝在下一次讀到之前不介入。
7. **程式放哪**：回合結束的自動壓縮與閒置攔截放 register.js（接力狀態在那裡），透過 events.js export 的 getter 拿設定；`--now` 放 events.js（佇列在那裡）。
8. **`--now` 在接力進行中拒絕**：member 有未結束的接力時回 `relay_open`（exit 13）。
9. **門檻的 App 控制項是 −／＋ 按鈕**，一次 5、限 1–100，按下就存。沒有輸入框就沒有草稿，切分頁不會丟東西。CLI 可以設 1–100 任何整數。
10. **手機 App 不開這些路由**：不加進 `cmd/pdx/device_scope.go` 的 `deviceAllowed`，iOS App 沒有這個介面。
11. **面板的那一行只給活著的 Claude Code session**；team 面板只對跟 lead 同一台主機的 session 顯示（遠端 member 的設定改不到，spec §9 也回 `remote_unsupported`）。
12. **`pdx team` 的 COMPACT 欄**：`自動 70%`、`手動`、`接力`（不介入）、`-`（讀不到或遠端）。

## Global Constraints

- 三個值：`auto` bool、`threshold` 整數 1–100、`idle` bool。「討論」＝自動關、閒置關；「開發」＝自動開、70、閒置開。模式不存。
- 沒寫過的主機：`{"solo":{"auto":true,"threshold":70,"idle":true},"team":{"auto":true,"threshold":70,"idle":true}}`。`solo` 給角色 none，`team` 給 lead 與 member。
- 生效值＝覆寫 ?? 主機預設[角色]。`managed:false` 的兩種 `reason`：`self_relay`（自我接力 state 是 `on`）、`member_auto`（member 且 `member_auto` 開）。
- 防重壓：每次壓縮後第一個讀到的 tokens 為「底」，tokens ≥ 底＋**20,000** 才再觸發；mod 重新載入後沒有底。
- 觸發條件：主對話、`reason === 'answer'`、managed 且 auto、接力狀態閒置、`percent` 有值且 ≥ 門檻、沒有壓縮呼叫在進行。用 `$.clock.after(0, …)` 呼叫，不在 hook 裡等。被拒且訊息含 `a turn is running`：不記；其他失敗或 `{skip}`：底＝當下 tokens。
- 閒置攔截：managed 且 `idle` 為假時，`trigger === 'auto'` 且沒有主回合在跑、以及 `precompute`，回 `{skip}`。member 的既有攔截排在前面。回合中的自動壓縮、`manual`、`plugin` 永遠不擋。
- 回報：`plugin` 一律報成 `manual`（`pdx relay compacted --trigger manual`）。
- 路由與名稱：`GET`／`PUT /api/team/compaction/{session_id}`、`POST /api/team/member-compaction`、`PUT /api/hostconfig/compaction`、capability `team.compaction.v1`、佇列工作種類 `compact`。
- 文字（App，繁中為準，英文另譯）：區名「壓縮」；列名「獨立 session」「team」；說明「關掉自動壓縮時，context 快滿（約 97%）仍會由 Claude Code 自動壓縮。」；按鈕「討論」「開發」；「自訂」；「門檻」；「閒置壓縮」；「跟主機預設」；不介入時「自我接力開著，由接力處理」或「member 由 lead 的接力設定處理」。
- 測試分層：開發中只跑改到的（`pdx test go-pkg -- <套件>`、`pdx test spa-file -- <檔>`、`pdx test mod-hooks`）；PR 前 `pdx test go-changed -- <套件>`、`pdx test lint`、mod 有改就 `pdx test mod-hooks`＋`pdx test mod-changed`、SPA 有改就 `pdx test spa-related`＋`pdx test tsc`（會送 mserv，不在 mlab 跑 tsc）。完整套件只在 Task 15。測試用的 tmux 一律 `-L <label>`，先 `unset TMUX`；只能 kill 自己的 pid。
- `pdx test mod-hooks` 已知有 3 條紅（#3987），以「除了這 3 條」判定。
- 新 capability 要有「真模組註冊後 `/api/info` 看得到」的測試（範本 `internal/module/hostconfig/personas_test.go:472`），並同步改 `internal/core/info_handler_test.go` 釘死的整份清單。
- en 與 zh-TW 兩份 locale 同時加 key（`locales/locale-completeness.test.ts` 會擋）。

## Review Focus

spec 沒寫明、但最可能出事的情況：

1. **daemon 不在、舊 daemon、讀失敗**：mod 必須跟今天一模一樣，不自己壓縮、不擋閒置壓縮（Task 6、7、8 的測試）。
2. **反覆壓縮**：門檻低於壓縮後的用量時，靠「底」擋住；「回合正在跑」被拒的重試也不能在同一個回合裡一直呼叫（Task 7）。
3. **「討論」被自動壓縮**：auto 關時 mod 永遠不呼叫；回合中的安全網永遠不擋（Task 7、8）。
4. **`/clear` 不沿用、接力沿用**：新 session 拿錯覆寫會讓設定莫名其妙變掉（Task 2）。
5. **App 切分頁時舊的回應蓋掉新的**：面板的 session 在請求途中換掉（Task 10）。

---

## PR 2a：daemon 的設定與 App 路由

### Task 1：host config 的 `compaction` 列

**Files:**
- Create: `internal/module/hostconfig/compaction.go`、`internal/module/hostconfig/compaction_test.go`
- Modify: `internal/module/hostconfig/handler.go`（`emptyFor`、`handleGet` 的列表）、`read.go`（`readers`）、`module.go`（`Init` 的 `Registry.Register`、`RegisterRoutes`）

**Interfaces:**
- Produces:
  - `const KeyCompaction = "compaction"`、`const CompactionKey = "hostconfig.compaction"`
  - `type CompactionSet struct { Auto bool \`json:"auto"\`; Threshold int \`json:"threshold"\`; Idle bool \`json:"idle"\` }`
  - `type CompactionDefaults struct { Solo CompactionSet \`json:"solo"\`; Team CompactionSet \`json:"team"\` }`
  - `var DefaultCompaction CompactionDefaults`（兩組都是開發）
  - `type CompactionReader interface { Compaction() (CompactionDefaults, error) }`，`*Module` 實作；列讀壞回 error。

照 `relay_quota.go` 的寫法：嚴格的 `normalizeCompaction`（兩組與三個欄位都必填、門檻 1–100 的整數、未知欄位與重複 key 都 400）、寬鬆的 `readCompaction`。

- [ ] **Step 1：寫會紅的測試**（照 `relay_quota_test.go`）
  - `TestCompaction_DefaultWhenNeverWritten`：`GET /api/hostconfig` 的 `compaction.items` 等於預設、revision 0；`Compaction()` 回 `DefaultCompaction`。
  - `TestCompaction_PutRoundTrip`：PUT 後 GET 與 `Compaction()` 都是寫入的值。
  - `TestCompaction_PutRejects`：表格測試，缺 `solo`、缺 `threshold`、門檻 0／101／70.5／`"70"`、外層或內層多一個欄位、重複 key → 400，原值不變。
  - `TestCompaction_StaleRevisionConflicts`：舊 revision → 409。
  - `TestCompaction_StoredGarbageIsAnError`：直接寫壞的 JSON 進表，`Compaction()` 回 error，GET 標 invalid。
  - `TestCompaction_RelayPutLeavesItAlone`：PUT `relay` 不動 `compaction`。
  - `TestInit_RegistersCompactionReader`。
- [ ] **Step 2：`pdx test go-pkg -- ./internal/module/hostconfig/`，確認紅。**
- [ ] **Step 3：實作。**
- [ ] **Step 4：同一指令，確認綠。**
- [ ] **Step 5：commit** `feat(hostconfig): the compaction defaults row`

### Task 2：wire 型別、`session_compaction` 表、接力沿用

**Files:**
- Create: `internal/team/wire_compaction.go`、`internal/module/team/compaction_store.go`、`internal/module/team/compaction_store_test.go`
- Modify: `internal/module/team/store.go`（`OpenStore` 加 Exec，放在 refused index 之前）、`internal/module/team/relay_store_report.go`（約 353 行 lineage INSERT 旁）

**Interfaces:**
- Produces（`internal/team`）：
  - `type CompactionOverride struct { Auto *bool \`json:"auto"\`; Threshold *int \`json:"threshold"\`; Idle *bool \`json:"idle"\` }`（JSON 的 null＝跟主機預設；GET 回應不省略欄位）
  - `type CompactionEffective struct { Managed bool \`json:"managed"\`; Reason string \`json:"reason,omitempty"\`; Auto bool \`json:"auto"\`; Threshold int \`json:"threshold"\`; Idle bool \`json:"idle"\` }`
  - `type CompactionView struct { Role string \`json:"role"\`; Override CompactionOverride \`json:"override"\`; Effective CompactionEffective \`json:"effective"\` }`
  - 常數：`CompactionRoute = "/api/team/compaction/{session_id}"`、`MemberCompactionRoute = "/api/team/member-compaction"`、`CapabilityCompaction = "team.compaction.v1"`、`CompactionReasonSelfRelay = "self_relay"`、`CompactionReasonMemberAuto = "member_auto"`
- Produces（`internal/module/team`）：
  - `(s *Store) CompactionOverride(sessionID string) (team.CompactionOverride, error)`：沒有列回零值、nil error。
  - `(s *Store) SetCompactionOverride(sessionID string, o team.CompactionOverride, now int64) error`：三個都 nil 時刪列。
  - `copyCompactionTx(tx *sql.Tx, from, to string, now int64) error`：來源沒有列就什麼都不做。

表：`session_compaction(session_id TEXT PRIMARY KEY, auto INTEGER, threshold INTEGER, idle INTEGER, updated_at INTEGER NOT NULL)`。讀寫照 `relay_store_prefs.go`。`ReportRelay` 在寫 `session_lineage` 的同一個交易裡呼叫 `copyCompactionTx`，自我接力與 member 接力都做；失敗就整個交易失敗。

- [ ] **Step 1：寫會紅的測試**
  - `TestCompactionOverride_RoundTripWithNulls`：只設門檻，讀回 `Auto`、`Idle` 為 nil。
  - `TestCompactionOverride_AllNilDeletesTheRow`：寫入再全部清成 nil，表裡沒有那一列。
  - `TestCompactionOverride_UnknownSessionIsEmpty`。
  - `TestReportRelay_ClearedCopiesTheOverride`：自我接力與 member 接力各一次，新 session 讀到同樣的覆寫。
  - `TestReportRelay_NotClearedCopiesNothing`：`cleared` 之前的狀態不複製。
  - `TestReportRelay_NoOverrideLeavesTheNewSessionEmpty`。
  - 既有的舊 schema 開檔測試（`testdata/team-alpha615.schema.sql`）加一條斷言：開完有 `session_compaction` 表。
- [ ] **Step 2：`pdx test go-pkg -- ./internal/module/team/`，確認紅。**
- [ ] **Step 3：實作。**
- [ ] **Step 4：同一指令，確認綠。**
- [ ] **Step 5：commit** `feat(team): per-session compaction overrides, carried across a relay`

### Task 3：生效值、App 路由、capability

**Files:**
- Create: `internal/module/team/compaction.go`、`internal/module/team/compaction_handler_test.go`
- Modify: `internal/module/team/module.go`（`Module` 多一個 `compaction hostconfig.CompactionReader`，`Init` 用 `lookup` 取得；`RegisterRoutes` 加兩條）、`internal/core/info_handler.go`、`internal/core/info_handler_test.go`

**Interfaces:**
- Consumes：Task 1 的 `CompactionReader`、Task 2 的型別與 store。
- Produces：
  - `(m *Module) compactionView(sessionID string) (team.CompactionView, error)`：用 `selfRelayState` 取角色與狀態（出錯就回錯）；member 且 `memberAutoOn()` → `managed:false, reason:member_auto`；state `on` → `managed:false, reason:self_relay`；否則 managed，生效值＝覆寫 ?? 主機預設[角色]。不介入時 auto／threshold／idle 照樣填生效值。
  - `(m *Module) compactionDefaults() hostconfig.CompactionDefaults`：讀錯回 `DefaultCompaction`，同一個錯誤 10 分鐘內只 log 一次（照 `memberAutoOn`）。
  - `GET /api/team/compaction/{session_id}` → 200 `CompactionView`；`compactionView` 出錯 → 500。
  - `PUT` 同路徑，body 三個 key 都可以是 null 或缺少（缺少＝null），未知 key、門檻超出 1–100 或不是整數 → 400；整份取代覆寫，回應同 GET。
  - `info_handler.go` 的靜態清單加 `team.compaction.v1`。

- [ ] **Step 1：寫會紅的測試**
  - `TestCompactionView_Table`：角色 none／lead／member × 覆寫（無、只有門檻、全部）× 自訂的主機預設，斷言生效值與 `Role`。
  - `TestCompactionView_SelfRelayOnIsNotManaged`、`…_SelfRelayPausedIsManaged`、`…_MemberAutoOnIsNotManaged`、`…_MemberAutoOffUsesTheTeamSet`。
  - `TestCompactionView_HostConfigUnreadableUsesTheDefaults`。
  - `TestCompactionGet_RelayStateErrorIs500`。
  - `TestCompactionPut_ReplacesTheWholeOverride`：先 PUT `{auto:false}`，再 PUT `{threshold:50}`，覆寫只剩門檻。
  - `TestCompactionPut_AllNullClears`、`TestCompactionPut_Rejects`（表格）。
  - `TestInfo_AnnouncesCompactionForTheRealModule`（照 personas 的真模組測試）。
- [ ] **Step 2：`pdx test go-pkg -- ./internal/module/team/ ./internal/core/`，確認紅。**
- [ ] **Step 3：實作。**
- [ ] **Step 4：同一指令，確認綠。**
- [ ] **Step 5：commit** `feat(team): the effective compaction settings and the App's routes`

**PR 2a 層測試**：`pdx test go-changed -- ./internal/module/hostconfig/ ./internal/module/team/ ./internal/core/`、`pdx test lint`。

---

## PR 2b：交給 mod

### Task 4：`/mod/v1/team` 帶 `compaction`

**Files:**
- Modify: `internal/team/wire_modread.go`（`ModRead` 加欄位）、`internal/modevents/handler.go`（`TeamRead` 加欄位）、`internal/module/modevents/module.go`（約 130 行搬欄位）、`internal/module/team/module.go`（約 586 行的註冊）、新檔 `internal/module/team/modread_compaction.go`
- Test: `internal/modevents/` 的 team read 測試、`internal/module/modevents/` 的模組測試、`internal/module/team/modread_compaction_test.go`

**Interfaces:**
- Produces：
  - `team.ModRead.Compaction *CompactionEffective`
  - `modevents.TeamRead.Compaction *TeamCompaction \`json:"compaction,omitempty"\``，`type TeamCompaction struct { Managed bool \`json:"managed"\`; Auto bool \`json:"auto"\`; Threshold int \`json:"threshold"\`; Idle bool \`json:"idle"\` }`（spec §6.1 的形狀）
  - `type modReader struct{ m *Module }`，`ModTeamRead` 先呼叫 `m.store.ModTeamRead`，再用 `compactionView` 填 `Compaction`；出錯時不填（log 限流）。註冊改成 `team.ModReader(modReader{m})`。

- [ ] **Step 1：寫會紅的測試**：route 在有值時帶 `compaction`、nil 時沒有這個 key；模組層原樣搬過去；`modReader` 的值等於 `compactionView(...).Effective`；`selfRelayState` 出錯時 `Compaction` 為 nil、`role`／`members` 照常。
- [ ] **Step 2：`pdx test go-pkg -- ./internal/modevents/ ./internal/module/modevents/ ./internal/module/team/`，確認紅。**
- [ ] **Step 3：實作。** **Step 4：同一指令，確認綠。**
- [ ] **Step 5：commit** `feat(modevents): hand the mod its compaction settings`

### Task 5：compact-context 帶 `trigger`

**Files:**
- Modify: `internal/modevents/compact_route.go`（body 的匿名 struct、`CompactContextService` 介面）、`internal/module/dialogue/jobs.go`、`internal/module/dialogue/compact_context.go`（約 67 行的 log）、所有實作該介面的 fake
- Test: `internal/modevents/compact_route_test.go`、`internal/module/dialogue/compact_context_test.go`

**Interfaces:**
- Produces：`CompactContext(ctx context.Context, sessionID, trigger string) (string, bool)`；log 行多 `trigger=<值>`，空字串印 `-`。沒帶 `trigger` 的舊 mod 照常可用。

- [ ] **Step 1：寫會紅的測試**：route 把 `trigger` 原樣交給 service；沒帶時是 `""`；log 行含 `trigger=plugin`。
- [ ] **Step 2：`pdx test go-pkg -- ./internal/modevents/ ./internal/module/dialogue/`，確認紅。**
- [ ] **Step 3：實作。** **Step 4：同一指令，確認綠。**
- [ ] **Step 5：commit** `feat(dialogue): log what triggered a compaction`

**PR 2b 層測試**：`pdx test go-changed -- ./internal/modevents/ ./internal/module/modevents/ ./internal/module/team/ ./internal/module/dialogue/`、`pdx test lint`。

---

## PR 3：mod

測試都加在 `cmd/pdx/plugin/purdex/hooks/events.test.ts` 新的一節「compaction control」（`evWorld` 同時有 team read 與整個 mod）。`evWorld` 要加兩個選項：`usage`（`session.usage` 的回答，可以依呼叫次數變）與 `compactCalls`（記錄底下的 `session.compact` 收到的 trigger）。`teamAnswer` 加第四個參數 `compaction`。用到的 hello 要是 `self_relay: 'off'`，除非測試要的就是接力狀態。

### Task 6：讀設定、compact-context 帶 trigger

**Files:**
- Modify: `cmd/pdx/plugin/purdex/hooks/workbook.js`、`events.js`
- Test: `events.test.ts`

**Interfaces:**
- Produces：
  - `workbook.js`：`export function parseCompaction(v)`：`managed`、`auto`、`idle` 是 boolean 且 `threshold` 是 1–100 的整數時回 `{ managed, auto, threshold, idle }`，否則 `null`。`compactContextBody(stream, sessionId, trigger)`。
  - `events.js`：`team.compaction`（成功讀到時設成 `parseCompaction(body.compaction)`；讀失敗不動；`forgetTeam` 清成 `null`）；`export function compactionPolicy()` 回 `team.compaction`；`compactFetch` 帶 `e.trigger`。

- [ ] **Step 1：寫會紅的測試**：`parseCompaction` 的表格（合法、缺欄位、門檻 0／101／70.5、型別錯）；compact-context 的請求 body 有 `trigger: 'manual'`。policy 的行為在 Task 7、8 驗。
- [ ] **Step 2：`pdx test mod-hooks`，確認紅。** **Step 3：實作。** **Step 4：同一指令，確認綠。**
- [ ] **Step 5：commit** `feat(mod): read the compaction settings`

### Task 7：回合結束時自動壓縮

**Files:**
- Modify: `cmd/pdx/plugin/purdex/hooks/register.js`（import `compactionPolicy`、`s` 的新欄位、`turn.complete` hook 約 1986 行、新的頂層函式）
- Test: `events.test.ts`

**Interfaces:**
- Consumes：Task 6 的 `compactionPolicy()`。
- Produces（register.js 頂層）：`s.compactFloor`（number 或 undefined）、`s.compactFloorPending`、`s.compactBusy`；`maybeAutoCompact($, e)`、`autoCompactTick($)`、`autoCompact($, tokens)`。

`turn.complete`（主對話、interactive）在既有的接力步驟之後：讀一次 `$.session.usage()`；`compactFloorPending` 且 tokens 有值 → 設底、清 pending；再照 Global Constraints 的觸發條件判斷，「接力狀態閒置」＝`s.state === 'idle'`、`s.control.length === 0`、沒有被 daemon 暫停（照 register.js 現有表示暫停的欄位）。成立就設 `compactBusy`、`$.clock.after(0, () => autoCompactTick($))`。`autoCompact` 再確認一次 `s.state === 'idle'` 才呼叫，結束時清 `compactBusy`。任何壓縮真的跑完（`compactedAfter` 沒被 skip 的路徑）都設 `compactFloorPending = true`。

- [ ] **Step 1：寫會紅的測試**
  1. `at a turn end over the threshold the mod compacts once, after the hook returned`：policy 開發 70、percent 72；`turnDone` 回傳時還沒呼叫，`settle()` 後 `compactCalls` 長度 1（harness 有帶 trigger 就斷言是 `plugin`）。
  2. `below the threshold nothing happens`、`a percent that is missing does nothing`（usage 只有 `window`）。
  3. `an aborted, refused or failed turn does not compact`（三種 reason）。
  4. `unmanaged, auto off, no compaction field, or no team read at all: nothing`（四種）。
  5. `a subagent's turn end does nothing`。
  6. `a relay under way wins`：hello `self_relay: 'on'`、門檻 70、percent 72：接力開始問，mod 不呼叫壓縮。
  7. `the floor stops a second compaction until 20,000 more tokens`：壓縮後第一個回合 tokens X 不壓；X＋19,999 不壓；X＋20,000 壓。
  8. `a refusal because a turn is running leaves no floor`：底下的 compact 丟 `a turn is running (t2); …`，下一個回合結束再試。
  9. `another refusal or a skip sets the floor`：之後 ＋19,999 不壓。
  10. `a second turn end while a compaction call is out does not call again`。
- [ ] **Step 2：`pdx test mod-hooks`，確認紅。** **Step 3：實作。** **Step 4：同一指令＋`pdx test mod-changed`，確認綠。**
- [ ] **Step 5：commit** `feat(mod): compact at a turn end over the threshold`

### Task 8：閒置攔截、`plugin` 報成 `manual`

**Files:**
- Modify: `register.js` 的 `session.compact` hook（約 2155 行）
- Test: `events.test.ts`

在 hook 開頭的 `precompute` 早退之前：interactive、主對話、policy managed 且 `idle` 為假、`trigger === 'precompute'` → `{ skip: SKIP_IDLE_OFF }`（新常數，文字說明是設定關掉閒置壓縮）。member 的既有攔截之後：同樣條件且 `trigger === 'auto' && !s.turnRunning` → `{ skip: SKIP_IDLE_OFF }`，先 `disarmIdle()`。回報用的 trigger：`e.trigger === 'plugin'` → `'manual'`。

- [ ] **Step 1：寫會紅的測試**
  1. `idle off skips an idle compaction and a precompute`：底下沒被呼叫，回傳有 `skip`。
  2. `idle on lets an idle compaction run`。
  3. `a compaction inside a turn is never skipped, idle off or not`。
  4. `a manual compaction is never skipped`。
  5. `a member's own intercept comes first`：hello `role: member`、`idle_intercept: 1`，policy idle 關，skip 原因是 `SKIP_MEMBER_IDLE`。
  6. `unmanaged or unknown settings skip nothing`。
  7. `a plugin compaction is reported as manual`：`relay compacted --session <sid> --trigger manual`。
- [ ] **Step 2：`pdx test mod-hooks`，確認紅。** **Step 3：實作。** **Step 4：同一指令＋`pdx test mod-changed`，確認綠。**
- [ ] **Step 5：commit** `feat(mod): stop idle compactions the settings turn off`

**PR 3 層測試**：`pdx test mod-hooks`、`pdx test mod-changed`、`pdx test lint`。

---

## PR 4a：App 主機頁的「壓縮」區

### Task 9：host config store 與 `CompactionSection`

**Files:**
- Modify: `spa/src/lib/host-config-api.ts`（型別）、`spa/src/stores/useHostConfigStore.ts`（`compaction`、`compactionSupported`、`saveCompaction`）、`spa/src/lib/register-modules/index.tsx`（`{ localId: 'compaction', labelKey: 'hosts.compaction', order: 12, component: CompactionSection }`）、`spa/src/locales/en.json`、`zh-TW.json`
- Create: `spa/src/components/settings/ThresholdStepper.tsx`、`spa/src/components/hosts/CompactionSection.tsx`、`spa/src/components/hosts/CompactionSection.test.tsx`

**Interfaces:**
- Produces：
  - `ThresholdStepper({ value, onChange, disabled, label }: { value: number; onChange: (v: number) => void; disabled?: boolean; label: string })`：−／＋ 一次 5，限 1–100，樣式照 `components/team/Steppers.tsx`。Task 10 也用它。
  - store：`entry.compaction: { solo: CompactionSet; team: CompactionSet }`、`entry.compactionSupported`（`compaction` 列存在時為真，照 `relaySupported`）、`saveCompaction(hostId, value)`。

`CompactionSection` 照 `RelaySection` 的版面與寫法（`useHostConfigGate`、`queueHostConfigSave(hostConfigQueueKey(hostId, 'compaction'), …)`、409 顯示 `host_config.conflict`、不支援時顯示 unsupported 文字、`!editable` 時鎖住）。兩列各有：自動壓縮開關、`ThresholdStepper`（自動關時 disabled）、閒置壓縮開關；下方一行說明。每個操作都整份 PUT。

- [ ] **Step 1：寫會紅的測試**（照 `RelaySection.test.tsx`，zh-TW）：顯示預設值；切 team 的閒置開關 → `saveCompaction` 收到整份、只有那一格變；按 ＋ 從 70 → 75；自動關時 stepper disabled；409 顯示衝突文字；沒有 `compaction` 列時顯示不支援；不能編輯時鎖住。
- [ ] **Step 2：`pdx test spa-file -- src/components/hosts/CompactionSection.test.tsx`，確認紅。** **Step 3：實作。** **Step 4：同一指令，確認綠。**
- [ ] **Step 5：commit** `feat(spa): the host's compaction defaults`

**PR 4a 層測試**：`pdx test spa-related`、`pdx test tsc`、`pdx test lint`。

---

## PR 4b：App 面板上的那一行

### Task 10：API、capability、`CompactionLine`

**Files:**
- Create: `spa/src/lib/team/compaction-api.ts`、`compaction-api.test.ts`、`spa/src/components/team/CompactionLine.tsx`、`CompactionLine.test.tsx`
- Modify: `spa/src/lib/team/types.ts`（`COMPACTION_CAPABILITY`）、`spa/src/stores/useUnattendedStore.ts`（`compactionSupport`）、`spa/src/lib/team/unattended-support.ts`（約 46 行）、兩份 locale

**Interfaces:**
- Produces：
  - `getCompaction(hostId: string, sessionId: string, signal?: AbortSignal): Promise<CompactionView>`、`putCompaction(hostId: string, sessionId: string, override: CompactionOverride): Promise<CompactionView>`，用 `lib/team/approval-api.ts` 的 `send`。
  - `CompactionLine({ hostId, sessionId }: { hostId: string; sessionId: string })`。

行為：掛上與 `sessionId` 改變時讀一次；回應的 key（hostId＋sessionId）跟當下不同就丟掉。`effective.managed` 為假時整行換成一句（依 `reason`）。否則：「討論」「開發」兩個按鈕（照 `PersonalSettings` 顏色按鈕的 `aria-pressed` 寫法與 `keepFocus`），生效值是討論（auto 關、idle 關）或開發（auto 開、70、idle 開）時亮對應那個，都不是時前面標「自訂」；`ThresholdStepper`（auto 關時 disabled）；閒置開關；覆寫不是全空時顯示「跟主機預設」。寫入：
- 討論 → `{ auto: false, threshold: <原覆寫的門檻或 null>, idle: false }`
- 開發 → `{ auto: true, threshold: 70, idle: true }`
- 門檻或閒置 → 原覆寫只改那一格
- 跟主機預設 → 三個都 null

PUT 的回應就是新的值（spec 的「改完再讀一次」）；失敗時顯示錯誤、值不變。

- [ ] **Step 1：寫會紅的測試**：API 的路徑與 body；兩種模式的亮法與「自訂」；四種寫入的 body；不介入的兩句；有覆寫才有「跟主機預設」；讀取途中 `sessionId` 換掉，舊回應不顯示；PUT 失敗顯示錯誤。
- [ ] **Step 2：`pdx test spa-file -- src/components/team/CompactionLine.test.tsx`（以及 api 測試），確認紅。** **Step 3：實作。** **Step 4：同一指令，確認綠。**
- [ ] **Step 5：commit** `feat(spa): a session's compaction line`

### Task 11：掛進個人面板與 team 面板

**Files:**
- Modify: `spa/src/components/team/PersonalPanel.tsx`、`PersonalSettings.tsx`（多收 `target`）、`spa/src/components/team/TeamPanel.tsx`（約 127 行）
- Test: `PersonalPanel.full.test.tsx`、`TeamPanel.settingsBlock.test.tsx`（或同目錄新檔）

個人面板：`target` 是 Claude Code（`agentType` 的值照現有判斷 Claude Code 的寫法）、沒有結束、`compactionSupport === 'yes'` 時，設定區加一列「壓縮」放 `CompactionLine`。team 面板：在 lead 與 members 的 seat 裡找 `tabId === activeTabId` 的那一個，它在 lead 那台主機上且 `compactionSupport === 'yes'` 時，顯示 `CompactionLine`；這一行**不受** `showSettings`（只給 lead 分頁）限制。

- [ ] **Step 1：寫會紅的測試**：個人面板 capability yes 有這一列、no 沒有、Codex 沒有、已結束沒有；team 面板 member 分頁在前景時有這一行（目前的 gate 會讓它消失，這條是回歸測試）、lead 分頁也有、遠端 member 沒有、沒有 seat 在前景時沒有。
- [ ] **Step 2：跑這兩個測試檔，確認紅。** **Step 3：實作。** **Step 4：同一指令，確認綠。**
- [ ] **Step 5：commit** `feat(spa): show the compaction line in the personal and team panels`

**PR 4b 層測試**：`pdx test spa-related`、`pdx test tsc`、`pdx test lint`。

---

## PR 5a：lead 看與改

### Task 12：daemon 的 lead 路由與 `pdx team` 的資料

**Files:**
- Create: `internal/module/team/member_compaction.go`、`member_compaction_test.go`
- Modify: `internal/team/wire_compaction.go`（請求與回應型別）、`internal/team/wire_team.go`（`Member` 加欄位）、`internal/module/team/module.go`（路由）、組 `team.Member` 的地方（在 `internal/module/team/` grep `RelayQuota:`）

**Interfaces:**
- Produces：
  - `type MemberCompactionRequest struct { OriginInbox string \`json:"origin_inbox"\`; Target string \`json:"target"\`; Set *CompactionOverride \`json:"set,omitempty"\`; Reset bool \`json:"reset,omitempty"\` }`（Task 14 再加 `Now`）
  - `type MemberCompactionView struct { Address string \`json:"address"\`; Ref string \`json:"ref"\`; CompactionView }`
  - `team.Member.Compaction *CompactionEffective \`json:"compaction,omitempty"\``：本機 member 用 `compactionView` 填，出錯或遠端為 nil。
  - `POST /api/team/member-compaction`：`callerTeam`（不是 lead → `not_lead`）→ `matchMember`（找不到 → 409 `not_your_member`）→ `isRemoteRow` → 409 `remote_unsupported`。`Reset` 刪覆寫；`Set` 把非 nil 的欄位合併進現有覆寫（驗證同 Task 3）；兩者都沒有就只讀。`Reset` 與 `Set` 同時給 → 400。

- [ ] **Step 1：寫會紅的測試**：不是 lead、不是自己的 member、遠端 member 三種拒絕；只讀；合併（先有 `idle:false`，再設門檻 50，兩個都在）；reset；門檻 0 → 400；`Reset`＋`Set` → 400；`GET /api/team` 的 member 帶 `compaction`。
- [ ] **Step 2：`pdx test go-pkg -- ./internal/module/team/`，確認紅。** **Step 3：實作。** **Step 4：同一指令，確認綠。**
- [ ] **Step 5：commit** `feat(team): a lead reads and changes a member's compaction settings`

### Task 13：`pdx compact`、COMPACT 欄、skill、guard

**Files:**
- Create: `cmd/pdx/compact_cmd.go`、`compact_cmd_test.go`、`cmd/pdx/compaction_route_guard_test.go`
- Modify: `cmd/pdx/main.go`（`case "compact"`、`Commands:` 說明）、`cmd/pdx/team_show_cmd.go`（約 113 行的表頭與每列）、`cmd/pdx/plugin/purdex/skills/pdx-team/SKILL.md`

**Interfaces:**
- Consumes：Task 12 的路由與型別。
- Produces：`runCompact(args []string, …) int`，照 `kill_cmd.go` 的 `teamSetup`／`teamReportErr`（`not_your_member`、`remote_unsupported` 已經在 `teamRefusalCodes`，自動是 exit 13）。

用法：`pdx compact <ref> [--auto on|off] [--threshold N] [--idle on|off] [--reset]`。`--reset` 與改值的旗標同時給、`on|off` 以外的值、`N` 不是 1–100 的整數 → 印 usage、exit 2。輸出：

```
member <address> [<ref>]
生效  自動 70%  閒置 開
覆寫  門檻 50
```

`生效` 一行在不介入時寫 `不介入（自我接力）` 或 `不介入（member 由 lead 的接力設定處理）`；auto 關時寫 `手動  閒置 關`；沒有覆寫時 `覆寫  無`。

`pdx team` 在 EFFORT 後面加 COMPACT 欄，值見「我定的事」第 12 條。

skill 的 lead 段落加一條：`pdx compact` 看與改 member 的壓縮設定、COMPACT 欄的意思；不寫任何 `/api/...` 路由。

guard：`cmd/pdx/` 下非測試的 `.go` 與 `cmd/pdx/plugin/purdex/skills/**` 都不可以出現字串 `/api/team/compaction/`（照 `relay_quota_guard_test.go`）。

- [ ] **Step 1：寫會紅的測試**：旗標解析與 usage 錯誤；只讀時送的 body 沒有 `set`；`--threshold 50 --idle off` 的 `set`；`--reset`；兩種拒絕 → exit 13；輸出三種樣子；`pdx team` 的 COMPACT 欄四種值；guard。
- [ ] **Step 2：`pdx test go-pkg -- ./cmd/pdx/`，確認紅。** **Step 3：實作。** **Step 4：同一指令，確認綠（`unattended_guard_test.go` 也在這個套件）。**
- [ ] **Step 5：commit** `feat(pdx): pdx compact, and a COMPACT column in pdx team`

**PR 5a 層測試**：`pdx test go-changed -- ./internal/module/team/ ./cmd/pdx/`、`pdx test mod-changed`（skill 在 plugin 裡）、`pdx test lint`。

---

## PR 5b：`pdx compact --now`

### Task 14：佇列的 `compact` 工作

**Files:**
- Modify: `internal/promptq/queue.go`（`KindCompact`、`SubmitCompact`、檔頭的 wire 說明）、`internal/team/wire_compaction.go`（`Now bool`、回應的 `Now string`、`ErrCompactUnsupported = "compact_unsupported"`）、`internal/module/team/member_compaction.go`、`cmd/pdx/team_cmd.go`（`teamRefusalCodes` 加 `compact_unsupported`）、`cmd/pdx/compact_cmd.go`（`--now`）、`cmd/pdx/plugin/purdex/hooks/workbook.js`（`PROMPT_KINDS`、`parsePromptJob`）、`events.js`（`pqRun`、`pqCompact`、`onTurnComplete`、三處 session 換 id 清旗標）
- Test: `internal/promptq/queue_test.go`、`member_compaction_test.go`、`compact_cmd_test.go`、`events.test.ts`

**Interfaces:**
- Produces：
  - `(q *Queue) SubmitCompact(ctx context.Context, sessionID string) (Outcome, error)`：走 prompt owner。
  - mod：`pqCompact($)`：`ev.turnId` 有值 → `compactNowWanted = true`，回 `accepted`、reason `turn_end`；否則 `$.clock.after(0, () => compactNowTick($))`，回 `accepted`、reason `now`。`onTurnComplete`（主對話，任何 reason）在 `compactNowWanted` 時排一次 tick。`compactNow($)` 呼叫 `$.session.compact()`；被拒且是回合正在跑 → `compactNowWanted = true`，等下一個回合結束；其他錯誤只 log。
  - daemon：`Now` 時先做 Task 12 的檢查；member 有未結束的接力 → 409 `relay_open`；`SubmitCompact` 的結果 `accepted` → 回應 `now: "now" | "turn_end"`；`dropped unsupported_kind` → 409 `compact_unsupported`；其他 dropped／unknown → 409 `compact_failed`（加進 `teamRefusalCodes`）。
  - CLI：`--now` 不能跟改值的旗標一起用；印 `已叫 member 壓縮` 或 `member 正在跑回合，這個回合結束時壓縮`。

- [ ] **Step 1：寫會紅的測試**：`KindCompact` 交給 prompt owner；daemon 的 `relay_open`、`compact_unsupported`、成功兩種；mod 閒置時從 timer 壓縮一次、回 `now`；回合中回 `turn_end`，回合結束（含 aborted）才壓縮；回合正在跑被拒時下一個回合結束再試；`/clear` 之後不再壓；`parsePromptJob` 認得 `compact`；CLI 的兩種輸出、拒絕 exit 13、與改值旗標一起用 exit 2。
- [ ] **Step 2：`pdx test go-pkg -- ./internal/promptq/ ./internal/module/team/ ./cmd/pdx/`＋`pdx test mod-hooks`，確認紅。** **Step 3：實作。** **Step 4：同樣的指令＋`pdx test mod-changed`，確認綠。**
- [ ] **Step 5：commit** `feat: pdx compact --now`

**PR 5b 層測試**：`pdx test go-changed -- ./internal/promptq/ ./internal/module/team/ ./cmd/pdx/`、`pdx test mod-hooks`、`pdx test mod-changed`、`pdx test lint`；改到鎖或 goroutine 時加 `pdx test go-race -- ./internal/promptq/`。

---

## Task 15：發版、部署、驗收

- [ ] 發版閘門（lead 跑）：`pdx test go-all`、`pdx test spa-full`、`pdx test tsc-force`、`pdx test mod-all`。紅了就找出是哪張 PR，往前修，不出版。
- [ ] bump PR：`VERSION`、`package.json`、`spa/package.json`、`CHANGELOG.md`；不派 codex。
- [ ] 打包 App：`pnpm run electron:build`（App 有改，`out/` 要更新）。
- [ ] 部署 daemon：照 `reference_pdx_daemon_runtime.md` 與 `feedback_deploy_gate_before_pdx_setup.md`。查閘門與停 daemon 分開；`relays_active=0` 在重啟前與 `pdx setup` 前各查一次。mlab 與 air26 一起升，各留一份舊版備份，兩台都跑 `pdx setup`。
- [ ] **拿掉 env 前先問使用者**（spec §10）。同意後兩台各自：先備份 `~/.claude/settings.json`，用 jq 刪 `env.CLAUDE_AUTOCOMPACT_PCT_OVERRIDE` 這一個 key，不印檔案內容，只用 `jq 'has("env") and (.env | has("CLAUDE_AUTOCOMPACT_PCT_OVERRIDE"))'` 確認已經沒有。告訴使用者：已在跑的 session 要重開才生效。
- [ ] 請使用者在 Settings → Development 更新 Mac App。
- [ ] 驗收 spec §11 第 1、3、4、5 項（第 2 項 Phase 1 已驗，這裡挑一種情況重跑一次當回歸）。真機、Haiku、預設 tmux server 上名字固定的拋棄式 session（需要重開，env 已拿掉）。第 5 項要一個 lead 與兩個 member（一個閒置、一個在跑回合），驗完 `pdx kill` 收掉。
- [ ] 部署後比對（spec §12）：把「依 `compact context` log 的 trigger 分開算」與「閒置關的 session 不應出現 `Compacted while idle`」寫進記憶裡 11-10 比對那一條。
