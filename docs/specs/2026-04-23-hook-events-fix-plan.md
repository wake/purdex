# Hook Events PR #616 Fix Plan

- **Date**: 2026-04-23
- **Spec**: `docs/specs/2026-04-23-lights-rebuild-spec.md` §2.4（架構護欄）
- **Parent plan**: `docs/specs/2026-04-23-hook-events-declaration-plan.md`（既有，11 commits merged 進本分支）
- **Worktree**: `lights-spec-guardrails`（branch `worktree-lights-spec-guardrails`，PR #616）
- **依賴**: Phase 1 merged at alpha.215（§5 L2 對齊完成）；Events() SSoT 已在本分支建立
- **範圍**: 修四路 codex review 攻擊/防守/體質視角提出的 4 個 findings（2 HIGH + 2 MED），讓 Events() SSoT 主張在 codex 與 opencode 端真正成立，且不因升級誤判既有安裝

## 1. 契約鎖定

本節鎖定全 Fix 行為，避免實作期再決策。

### 1.1 HookEventSpec 新增 FutureOnly 欄位

`internal/agent/provider.go` `HookEventSpec` 結構加一個 `FutureOnly bool`：

```
type HookEventSpec struct {
    Name        string
    EmitsStatus []Status
    Description string
    FutureOnly  bool // 此 event 已在 spec 中宣告，但當前 CLI 版本未必會 emit
}
```

**語意**：

- `FutureOnly=false`（預設）— 當前 CLI 會 emit 此 event；installer 安裝、CheckHooks 嚴格檢查 missing、drift test 要求 fixture 覆蓋、SupportedStatuses derive 會納入 EmitsStatus
- `FutureOnly=true` — installer 仍安裝（為 CLI 升級鋪路），但 runtime 未必收得到；CheckHooks 對此 event missing **不標 issue、不計入 allInstalled**；drift test **不要求** fixture；SupportedStatuses derive 時**跳過**其 EmitsStatus

**不把語意等同於「CLI 永遠不會支援」**：只表達「目前 build-time 信心不足，把它從對外 capability 宣告裡隔離」。後續 CLI 驗證通過後翻成 false 即可。

### 1.2 DeriveSupportedStatuses 過濾 FutureOnly

`internal/agent/supported_statuses.go` `DeriveSupportedStatuses` 對 FutureOnly spec **跳過**其 EmitsStatus：

```
for _, spec := range specs {
    if spec.FutureOnly {
        continue
    }
    for _, s := range spec.EmitsStatus { ... }
}
```

排序與去重邏輯不變；空輸入仍回非 nil empty slice；deterministic lexicographic 排序不變。

**影響**：codex 6 個 FutureOnly event 的 5 個 status（Waiting/Error/Clear/加 Notification idle 分支的 Idle 重複/PermissionRequest 的 Waiting 重複）— 其實經過各自 emits 表 intersection 後，codex SupportedStatuses 會收斂到「Running+Idle」（由 UserPromptSubmit+SessionStart/Stop 兩個非 FutureOnly event 貢獻）。

### 1.3 三家 provider FutureOnly 標記矩陣

| Provider | Event | FutureOnly | 理由 |
|---|---|---|---|
| cc | 全部 9 個 | false | cc CLI 實際會 emit 全部 9 個（此版本 plugin 驗證過） |
| codex | SessionStart / UserPromptSubmit / Stop | false | 主線 codex CLI hooks 長期支援這 3 個（既有 3-event 安裝） |
| codex | SubagentStart / SubagentStop / StopFailure / Notification / PermissionRequest / SessionEnd | **true** | codex CLI 目前不保證 emit；但 DeriveStatus 已能解析；installer 安裝為未來鋪路 |
| opencode | 全部 8 個 | false | plugin template 實際會 emit 全部 8 個（§1.5 後 template 從 Events 生成） |

codex 3-FutureOnly 後，**SupportedStatuses=[Idle, Running]**；drift test 對這 2 個 status 做 per-event 強相等；6 個 FutureOnly event 跳過 fixture 檢查。

**不做 per-CLI-version gate**：版本探測與動態 FutureOnly 翻轉留給未來 phase；此 PR 的 FutureOnly 是 build-time 靜態標記。

### 1.4 codex CheckHooks 對 FutureOnly missing 不報問題

`internal/agent/codex/hooks.go` CheckHooks 改動：

- 遍歷仍用 `p.eventNames()`（維持 9 個 event 全部檢查與寫入 `Events` map）
- **但**對 `spec.FutureOnly=true` 的 event，若 `hooks.json` 缺該 key 或 pdx command 不在：
  - `events[eventName] = HookEventInfo{Installed: false}`（仍如實記錄實況）
  - **不 append issue**
  - **不設 `allInstalled = false`**
- 非 FutureOnly event 維持現有嚴格檢查（missing → issue + allInstalled=false）

遍歷時需要每個 event name 配對其 spec（取得 FutureOnly bit）：用 `p.Events()` 取 `[]HookEventSpec`（或建一個 `eventSpecs()` 私有 helper 回傳已定義的 `codexEventSpecs` slice 以避免 defensive copy 開銷；兩者擇一，不影響對外契約）。

**3-event legacy 使用者**：升級後 hooks.json 仍只有 `SessionStart/UserPromptSubmit/Stop`。其他 6 個 FutureOnly 為 missing（依 §1.4 不標 issue、不計 allInstalled）。只要 3 個非 FutureOnly event 都 installed → `allInstalled=true`。不需要額外 legacy fallback 分支 — FutureOnly 語意已自然覆蓋此 case。

**主線使用者（新安裝 9 event）**：9 個都 installed → `allInstalled=true`、issues 空；FutureOnly event 依然 `Installed=true`（因為 installer 有寫）。

**部分受損**：若 3 個非 FutureOnly 缺其中一個 → `allInstalled=false` + 該 event issue（與現行一致）；FutureOnly 缺失不影響 overall 判定。

### 1.5 opencode renderManagedPlugin 從 Events 生成

這是 Finding #2（防守 HIGH + 體質 MED）的核心：**不讓 template 與 opencodeEventSpecs 成為兩份平行 SSoT**。

`internal/agent/opencode/plugin_template.go` 改動：

- `renderManagedPlugin(pdxPath string) string` **改簽名** 加入 `specs []agent.HookEventSpec` 第二參數（或在函式內讀 `opencodeEventSpecs` — 同套件內 package 級變數，直接讀即可避免 caller 端傳遞）
- 函式內部從 `opencodeEventSpecs` 動態生成：
  - plugin JS 的 `emit('<Name>', payload)` 字串依 specs 順序產出
  - event type mapping（`session.created → SessionStart`、`permission.asked → PermissionRequest`、…）仍保留在 Go 側「OpenCode 原生 event → pdx event name」的轉譯表，因為這是從 opencode runtime 事件語言到 pdx hook 語言的獨立概念
  - **但**：`emit('<pdx event name>', ...)` 呼叫的 pdx event name 必須出現在 `opencodeEventSpecs` 的 `Name` 集合內 — 生成時以 specs Name 為白名單；若 mapping 表宣告了 specs 沒有的 event，生成時跳過並在測試層抓出（§2.5 D6）

**實作策略**（擇 A）：

- **策略 A**（優先）：template 保留固定 switch/case 結構（因為每個 opencode 原生 event 的 payload 轉譯 JS 邏輯不同），但 template 渲染前 **先檢查每個 emit 的 pdx event 是否在 `opencodeEventSpecs` 集合內**；否則 panic（「contract violation: template emits undeclared event」）。渲染出的 JS 字串保持與目前一致（避免 churn），但運行時 emit 的 event set 受 specs 檢查守護。
- **策略 B**（若 A 太鬆）：把 template 裡的 switch/case 完全從 specs driven — 但 payload 轉譯邏輯會被 codegen 化，churn 大。**本 PR 不選**。

**選擇策略 A**：改動小、風險低、仍解決 SSoT 核心論點（specs 宣告什麼，template 就能 emit 什麼；不能 emit specs 裡沒有的 event）。

**配合測試**：`plugin_template_test.go` 新增 D5（見 §2.5）驗證「template 字串實際 emit 的 event name」⊆ `opencodeEventSpecs.Name`，且強相等（不能少也不能多）。測試用 regex `/emit\(['"]([A-Za-z]+)['"]/g` 從渲染出的 JS 字串抓 event name。

### 1.6 opencode CheckHooks 對齊新驗證

`internal/agent/opencode/hooks.go` CheckHooks 目前邏輯：檔案存在 + managed marker → 全部 event 標 `Installed=true`。這是 Finding #2 的假綠燈根源。

**新邏輯**：

1. 讀檔、驗 marker（不變）
2. 用 §1.5 同一套 regex 從檔案內容抓實際 emit 的 event name set
3. 對每個 `opencodeEventSpecs` event：
   - 若該 event name 在 emit set 中 → `Installed=true, Command=pluginPath`
   - 否則 → `Installed=false`，push issue `<event> emit missing in plugin`
4. `allInstalled = (每個 non-FutureOnly event 都 installed)` — opencode 當前無 FutureOnly event（§1.3），等同「全部 event 都 installed」

**regex 位置**：在 plugin_template.go 暴露一個 `extractEmittedEvents(body string) []string` helper，CheckHooks 與測試共用。單一正規來源避免 drift。

### 1.7 drift test per-event 強相等 + FutureOnly skip

`internal/agent/drift_test.go` 改動核心是把 provider 級 union 比對升級為 per-event 強相等：

**既有 TestDriftThreeWayEquality**：保留（provider 級三向 — Events union vs DeriveStatus union vs SupportedStatuses），但遍歷 fixtures 前**濾掉 FutureOnly spec 對應的 fixture**（若 fixture table 誤放 FutureOnly event fixture → D6 再報，不在此測試）。

具體 FutureOnly 處理：
- declaredSet 遍歷 `installer.Events()` 時跳過 `spec.FutureOnly=true`
- 對 fixtures 的 emit 判斷不變（emitted 只管 Valid=true && Status != ""）
- supportedSet 因 SupportedStatuses 已由 DeriveSupportedStatuses 自動濾 FutureOnly → 自然三向相等

**新增 TestDriftPerEventEmitsStatusMatch**：對每個 non-FutureOnly spec，取該 event name 對應所有 fixture，跑 DeriveStatus，收集 emit status set（Valid=true && Status != ""），斷言 **emit set == spec.EmitsStatus set**（雙向相等）。抓「單一 event 過度宣告」漏網。

**新增 TestDriftFixtureOnlyCoversNonFutureOnly**：駐點防呆 — `providerFixtures` 不應為 FutureOnly event 寫 fixture（避免測試假綠 / fixture 過期）。遍歷 fixtures，對每個 fixture `eventName`，若對應 spec `FutureOnly=true` → `t.Errorf`。這保證 FutureOnly event 不會在日後變回 non-FutureOnly 時，fixture 還在但其實驗的是 mock 資料。

**既有 TestDriftFixtureCoversAllEvents**：改為只要求 **non-FutureOnly** event 都有 fixture；FutureOnly event 跳過此斷言。

**既有 TestDriftFixtureCoverageNonEmpty**：不動。

`providerFixtures` 內容：
- cc 12 個 fixture 不動
- codex **移除** 6 個 FutureOnly event 的 fixture（Notification 4 支 + PermissionRequest 1 + SubagentStart 1 + SubagentStop 1 + StopFailure 1 + SessionEnd 1 共 9 個？需逐項對照現有列表）
- opencode 不動（當前無 FutureOnly event）

**codex fixture 精準列表**：

非 FutureOnly（保留）：
- `SessionStart`, `UserPromptSubmit`, `Stop` — 3 fixture

FutureOnly（移除）：
- `Notification` 4 支 + `PermissionRequest` + `SubagentStart` + `SubagentStop` + `StopFailure` + `SessionEnd` — 共 9 fixture

移除後 codex fixture 只剩 3 支；drift per-event 對 3 個 non-FutureOnly 強相等。

### 1.8 opencode events.go 對 status 補齊

Finding 沒直接要求，但既然 §1.5-1.6 做 plugin emit ↔ specs 對齊，必須檢查 opencode template 實際 emit 的 pdx event 是否全部在 `opencodeEventSpecs` 內。

檢查現況（從 `plugin_template.go` renderManagedPlugin 靜態分析）：
- template emit: `SessionStart`, `PermissionRequest`, `StopFailure`, `Stop`, `SessionEnd`, `UserPromptSubmit`, `SubagentStart`, `SubagentStop` — 共 8 個
- `opencodeEventSpecs` Name 集合：`SessionStart`, `UserPromptSubmit`, `SubagentStart`, `SubagentStop`, `PermissionRequest`, `Stop`, `StopFailure`, `SessionEnd` — 共 8 個

**相等**。§1.5 runtime 檢查生效時不會 panic。

### 1.9 零改動邊界

以下檔案 Fix **不得觸碰**：

- `internal/agent/registry.go`, `status.go`, `coverage.go`, `hook_version.go`, `process_info*.go`
- `internal/agent/cc/**` 除非為配合 `supported_statuses` 介面升級而被動調整（不預期）
- `internal/agent/opencode/status.go`, `provider.go`, `readiness.go`（僅動 `hooks.go`, `plugin_template.go`, `events.go` 可能需改 FutureOnly=false 顯式標記 — 若預設值即 false 也可不改）
- `internal/agent/codex/status.go`, `provider.go`, `readiness.go`（僅動 `hooks.go`, `events.go`）
- `internal/agent/probe/**`
- `internal/store/**`
- `internal/module/**`
- `spa/**`

不新增 API endpoint、不改 registry、不引入 agent 版本探測。

## 2. 測試案例清單

按檔案組織：

### 2.1 FutureOnly 欄位單元測試（provider-level）

`internal/agent/supported_statuses_test.go` 既有 case 不變，新增：

| # | 名稱 | 情境 | 斷言 |
|---|---|---|---|
| S1 | `TestDeriveSupportedStatuses_SkipsFutureOnly` | specs 含 1 個 FutureOnly + 2 個 non-FutureOnly | 回傳只含 non-FutureOnly 的 EmitsStatus union |
| S2 | `TestDeriveSupportedStatuses_AllFutureOnly` | 全部 spec FutureOnly=true | 回傳空 slice（非 nil） |
| S3 | `TestDeriveSupportedStatuses_FutureOnlyEmptyEmits` | FutureOnly=true + EmitsStatus=nil | 回傳空 slice（與 §1.2 語意一致，不 panic） |

### 2.2 codex Events 與 CheckHooks FutureOnly 行為

`internal/agent/codex/events_test.go` 新增：

| # | 名稱 | 斷言 |
|---|---|---|
| CE1 | `TestCodexEventsFutureOnlyFlags` | 3 個非 FutureOnly（SessionStart/UserPromptSubmit/Stop）+ 6 個 FutureOnly，一一 assert `spec.FutureOnly` |
| CE2 | `TestCodexEventsDefensiveCopyPreservesFutureOnly` | 取兩次 `Events()`、mutate 第一次的 FutureOnly → 第二次不受影響 |

`internal/agent/codex/hooks_test.go` 新增（用既有 writing temp hooks.json + pdx path 的 helper 模式）：

| # | 名稱 | 情境 | 斷言 |
|---|---|---|---|
| CH1 | `TestCheckHooks_LegacyThreeEvent_ReportsInstalled` | hooks.json 僅 SessionStart/UserPromptSubmit/Stop 且 pdx command 正確 | `allInstalled=true`、`Issues` 不含 FutureOnly event missing、6 個 FutureOnly 的 `Events` map entry 為 `Installed=false` |
| CH2 | `TestCheckHooks_NineEvent_FullyInstalled_NoIssues` | hooks.json 含完整 9 個 event 且 pdx command 正確 | `allInstalled=true`、`Issues=[]`、9 個 event map entry `Installed=true` |
| CH3 | `TestCheckHooks_LegacyThreeEvent_MissingRequired_StillFailsForThat` | hooks.json 缺 SessionStart（只剩 2 個）+ 6 FutureOnly 缺 | `allInstalled=false`、`Issues` 含 SessionStart missing、FutureOnly missing 不入 Issues |
| CH4 | `TestCheckHooks_PartialFutureOnly_DoesNotTaintOverall` | hooks.json 3 非 FutureOnly + 3 FutureOnly（其他 3 FutureOnly 缺） | `allInstalled=true`、`Issues=[]`、3 缺的 FutureOnly `Installed=false` |

### 2.3 opencode template 與 CheckHooks 對齊

`internal/agent/opencode/plugin_template_test.go` 新增：

| # | 名稱 | 斷言 |
|---|---|---|
| PT1 | `TestExtractEmittedEvents` | helper `extractEmittedEvents` 從 template body 正確抽出 8 個 event name（順序可變） |
| PT2 | `TestRenderManagedPlugin_OnlyEmitsDeclaredEvents` | `renderManagedPlugin("/p")` 回傳 body 的 emit set ⊆ `opencodeEventSpecs.Name` 且強相等 |
| PT3 | `TestRenderManagedPlugin_PanicsOnMismatch` | 臨時注入 mock `opencodeEventSpecs` 缺一 event（例移除 `Stop`）→ render 時 panic（契約違反） |

PT3 若用 init-time 注入困難（const-like slice），改用 `validateSpecsCoverEmitted(body, specs) error` 直接跑 negative case 測試即可，不需改 render 流程。

`internal/agent/opencode/hooks_test.go` 新增：

| # | 名稱 | 斷言 |
|---|---|---|
| OH1 | `TestCheckHooks_ValidPlugin_PerEventInstalled` | 用 `writeManagedPlugin` 寫合法 template → CheckHooks 每個 event `Installed=true`、Issues=[] |
| OH2 | `TestCheckHooks_HandEditedPlugin_EventMissing` | 寫一份含 marker 但移除某 `emit('Stop', ...)` 的 body → CheckHooks 該 event `Installed=false` 且 Issues 含 `Stop emit missing` |
| OH3 | `TestCheckHooks_UnmanagedPlugin_ReturnsUnmanaged` | 既有行為保留：不帶 marker → Installed=false + Issues |

### 2.4 drift test 升級

`internal/agent/drift_test.go` 改動：

| # | 名稱 | 斷言 |
|---|---|---|
| D3（修改） | `TestDriftThreeWayEquality` | declaredSet/supportedSet 遍歷跳過 FutureOnly；emit 行為不變；三向相等對 non-FutureOnly |
| D4（修改） | `TestDriftFixtureCoversAllEvents` | 只要求 non-FutureOnly event 有 fixture；FutureOnly skip |
| D5（新增） | `TestDriftPerEventEmitsStatusMatch` | 對每個 non-FutureOnly spec，fixture emit set == spec.EmitsStatus（雙向強相等） |
| D6（新增） | `TestDriftFixturesDoNotCoverFutureOnly` | 反向防呆：fixture 中若出現 FutureOnly event 名 → t.Errorf |

### 2.5 cc/opencode SupportedStatuses 不變（回歸保險）

既有 `cc/provider_test.go` `TestCCSupportedStatuses` 應仍回傳 5 個 Status — 補斷言確認新增 FutureOnly 不影響 cc（全為 false）。若既有測試已明列 5 個 status → 無須改。

`opencode/provider_test.go` `TestOpenCodeSupportedStatuses` 同理，5 status 不變。

`codex/provider_test.go` `TestCodexSupportedStatuses` 需改：**從 5 status 改為 2 status**（Idle + Running），因 6 個 FutureOnly event 的 Waiting/Error/Clear 被濾掉。這是 FutureOnly 的直接後果。commit 順序需要先 landing FutureOnly 欄位與 codex 標記才跑這個斷言更新。

## 3. TDD 執行順序

嚴格紅綠，每個 step 一個 commit；commit message 用 Conventional Commits + `Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>`。

### Commit 1 — `feat(agent): add FutureOnly bit to HookEventSpec`
- 紅：寫 S1/S2/S3 → 失敗（DeriveSupportedStatuses 未濾）
- 綠：加欄位 + 改 `DeriveSupportedStatuses` skip FutureOnly
- 跑 `go test ./internal/agent/...` 綠

### Commit 2 — `feat(agent/codex): mark 6 future-only events`
- 紅：寫 CE1 → 失敗（6 個 event 尚未標 FutureOnly）
- 綠：在 `codexEventSpecs` 6 個 event 上加 `FutureOnly: true`
- 同步：`codex/provider_test.go` `TestCodexSupportedStatuses` 從 5 status 改為 2 status（斷言 supported == {Idle, Running}）
- 跑 `go test ./internal/agent/codex/...` 綠

**備註**：寫 CE2 放在同 commit 測 defensive copy 保留 FutureOnly bit。

### Commit 3 — `feat(agent/codex): skip FutureOnly missing in CheckHooks`
- 紅：寫 CH1/CH2/CH3/CH4 → 失敗（CheckHooks 仍嚴格判所有 event missing）
- 綠：CheckHooks 內部先取 specs（含 FutureOnly bit），對 FutureOnly missing 不 issue + 不計 allInstalled
- 跑 `go test ./internal/agent/codex/...` 綠

### Commit 4 — `test(agent): upgrade drift test to per-event + FutureOnly skip`
- 紅：寫 D5（per-event 強相等）+ D6（FutureOnly skip）→ 失敗（D6 若舊 fixture 還含 codex FutureOnly event fixture）
- 修：`providerFixtures` 移除 codex 6 個 FutureOnly event fixture；`TestDriftThreeWayEquality` 的 declaredSet/supportedSet 迴圈跳過 FutureOnly；`TestDriftFixtureCoversAllEvents` 只要求 non-FutureOnly 有 fixture
- 綠：三個測試 + D5/D6 都過
- 跑 `go test ./internal/agent/...` 綠

### Commit 5 — `refactor(agent/opencode): extract emitted events from template`
- 紅：寫 PT1 → 失敗（extractEmittedEvents 不存在）
- 綠：在 `plugin_template.go` 加 `extractEmittedEvents(body string) []string` helper（正規 `emit\(['"](\w+)['"]`）
- 跑 `go test ./internal/agent/opencode/...` 綠

### Commit 6 — `feat(agent/opencode): validate template emits declared events`
- 紅：寫 PT2 + PT3（驗證 helper + negative case）→ 失敗
- 綠：加 `validateSpecsCoverEmitted(body string, specs []HookEventSpec) error`；`renderManagedPlugin` 最後呼叫 validate，若 error → panic with 契約違反訊息
- 跑 `go test ./internal/agent/opencode/...` 綠

### Commit 7 — `feat(agent/opencode): CheckHooks verifies per-event emit`
- 紅：寫 OH1/OH2/OH3 → 失敗（CheckHooks 仍假綠回填）
- 綠：CheckHooks 讀檔後呼叫 `extractEmittedEvents`，對每個 spec 檢查其 Name 是否在 emit set；missing 標 Installed=false + push issue
- 跑 `go test ./internal/agent/opencode/...` 綠

### Commit 8 — `docs: hook events fix plan retrospective（可選）`
若 §3 順序與實際有偏差，補歷史註記。

**Commit 順序原理**：
- Commit 1 建欄位 + 介面；所有後續 commit 依賴這個
- Commit 2-3 完成 codex 側（事件標記 + CheckHooks 行為）
- Commit 4 升級 drift test 保護這些新行為
- Commit 5-7 完成 opencode 側（extraction helper → validate → CheckHooks per-event）
- 每個 commit 完成後 `go build ./... && go test ./... && go vet ./...` 全綠

## 4. 實作檔案預估

| 檔案 | 動作 | 預估行數 |
|---|---|---|
| `internal/agent/provider.go` | +`FutureOnly bool` 欄位 + 註解 | +8 |
| `internal/agent/supported_statuses.go` | skip FutureOnly | +3 |
| `internal/agent/supported_statuses_test.go` | +S1/S2/S3 | +40 |
| `internal/agent/codex/events.go` | 6 event 加 `FutureOnly: true` | +6 |
| `internal/agent/codex/events_test.go` | +CE1/CE2 | +40 |
| `internal/agent/codex/hooks.go` | CheckHooks FutureOnly 分支 | +15 |
| `internal/agent/codex/hooks_test.go` | +CH1/CH2/CH3/CH4 | +90 |
| `internal/agent/codex/provider_test.go` | 改 supported 斷言（5→2） | ~5 |
| `internal/agent/opencode/plugin_template.go` | +`extractEmittedEvents` + `validateSpecsCoverEmitted` + render panic | +35 |
| `internal/agent/opencode/plugin_template_test.go` | +PT1/PT2/PT3 | +60 |
| `internal/agent/opencode/hooks.go` | CheckHooks per-event emit check | +20 |
| `internal/agent/opencode/hooks_test.go` | +OH1/OH2/OH3 | +70 |
| `internal/agent/drift_test.go` | +D5/D6 + D3/D4 FutureOnly skip + fixture 精修 | +60 |
| `docs/specs/2026-04-23-hook-events-fix-plan.md` | 本檔 | +300 |
| **合計** | | **~760 行**（含 plan 文件） |

## 5. 不做項目清單（防自動擴展）

以下衝動一律拒絕 — subagent 不得擴張：

- **不探測 codex CLI 實際版本** — FutureOnly 是 build-time 靜態標記，非 runtime gate
- **不引入 `/api/hooks/codex/capability` endpoint** — 宣告與檢測分離，Inspector 另行處理
- **不重寫 opencode template 為完全 codegen** — 策略 A（保留固定 switch/case + 運行時檢查）即可
- **不把 cc 任何 event 標為 FutureOnly** — cc 驗證過全部 9 個會 emit
- **不改 `SpecStringSlice` 或其他宣告 helper**（若存在）— 不在本 PR scope
- **不調整 `Coverage()` 簽名或回傳欄位** — Phase 0 契約不動
- **不重命名任何既有測試** — 只加新 case 或微調（例 codex supported 斷言 status 列表）
- **不改 SPA 任何檔案** — 本 PR 全 Go 側
- **不 force push / rebase** — 分支已 push 11 commits + PR，保留全歷史
- **不合併 fix commits** — 分 7 commits 便於 reviewer 逐步追蹤
- **不順手修其他 finding** — 四路 review 只有這 4 個 finding，不額外添加

## 6. Subagent 開發指引

- **cwd 強制前綴**：每個 Bash 指令以 `cd /Users/wake/Workspace/wake/purdex/.claude/worktrees/lights-spec-guardrails && ` 開頭（依 `feedback_subagent_cwd_enforcement.md`）
- **分支**：已在 `worktree-lights-spec-guardrails`，不另切；**不 push**（主 session 負責）
- **Commit message 格式**：Conventional Commits + 結尾 `Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>`
- **TDD 嚴格紅綠**：每 commit 內先寫測試跑紅再實作跑綠，不可批次寫完所有 test + 一次實作
- **每 commit 完成後**：跑 `go build ./... && go test ./... && go vet ./...` 全綠才進下一個
- **回報**：完成時給 commit hash 列表 + `git log --oneline -15` + 最終 `go test ./...` 完整輸出
- **Codex sandbox 限制**：不需跑 SPA（本 PR 全 Go 側）— 但仍保留 `feedback_codex_sandbox_no_install.md` 準則以防 subagent 誤觸 spa

## 7. 驗收清單

- [ ] 7 個 commits 符合上述 message 規範（Commit 8 可選）
- [ ] `go build ./...` 綠
- [ ] `go test ./...` 全綠（包含新增 S1-S3、CE1-CE2、CH1-CH4、PT1-PT3、OH1-OH3、D5-D6 共 16 個測試 case）
- [ ] `go vet ./...` 無 warning
- [ ] PR diff 只涉及 §4 表格列出的 13 個檔 + plan 文件，**其餘不得出現**
- [ ] codex `SupportedStatuses()` 回傳 `[Idle, Running]`（2 個，非 5 個）
- [ ] opencode `CheckHooks()` 對「手動改過、少一個 emit」的 plugin 正確標 `Installed=false` + issue
- [ ] codex `CheckHooks()` 對「只安裝舊 3 event 的 legacy hooks.json」回 `allInstalled=true` 且 Issues 不含 FutureOnly event missing
- [ ] drift test 對「若把 codex `Stop` 的 `EmitsStatus` 改為 `[Idle, Clear]`」應紅（per-event 強相等觸發）

## 8. 風險與緩解

| 風險 | 機率 | 影響 | 緩解 |
|---|---|---|---|
| FutureOnly 語意誤解為「CLI 永不支援」 | 中 | 未來翻 false 時遺漏檢查 | 在 `HookEventSpec` struct 註解與 plan §1.1 顯式說明「build-time 信心不足」；留 TODO 於 codex events.go 指向未來 enable 路徑 |
| `extractEmittedEvents` regex 遇到 JS 註解內 `emit('XXX')` 誤判 | 低 | CheckHooks 假陽性 | regex 加 word boundary；template 內不寫註解帶 emit 字串；PT1 測試覆蓋一個 edge case（如 body 含 `// emit('Foo')` 註解，確認不被抽出） |
| opencode CheckHooks 因檔案 IO error 在 race 下假綠 | 低 | 罕見 race 掩蓋 | 維持既有 `os.ReadFile` 錯誤處理，不動；此 PR 不引入新 race |
| codex provider_test supported 斷言從 5→2 可能讓 reviewer 誤以為退步 | 中 | review 釋疑成本 | PR description 顯式說明：「2 是當前 CLI 真實可 emit 集合；FutureOnly 6 event 翻 false 後自然恢復 5」 |
| drift fixture 移除 codex 6 個 FutureOnly fixture 後，日後翻 false 時 fixture 需補回 | 低 | 後續 phase 會遇 | commit 4 在 drift_test.go 頂端留註解：「FutureOnly event fixture 未列出；翻 false 時補回 §1.1 曾列出的對應 DeriveStatus case」 |
| 某 finding 在實作中發現新子問題 | 中 | scope 膨脹 | 遵 §5 不做項目；發現新問題開 gh issue 延後，不納入本 PR |

## 9. 第 3 輪 Codex Review 預期 focus

**Focus text 骨架**（實際派發時可微調，確保 codex 能讀到「本輪修哪些」）：

- 本輪修復 PR #616 第 2 輪 3 路 review 的 4 findings：
  1. codex CheckHooks 對 3-event legacy 使用者誤判（透過 FutureOnly 自然處理）
  2. opencode renderManagedPlugin 與 CheckHooks 的假綠燈（透過 extractEmittedEvents + validate）
  3. codex future-only events 混入 current capability（透過 FutureOnly 欄位 + SupportedStatuses skip）
  4. drift test per-event 盲點（透過 TestDriftPerEventEmitsStatusMatch + FutureOnly skip）
- 請驗收：
  - 攻擊：FutureOnly 翻譯正確（邊界 / race / 誤用）；extractEmittedEvents regex 假陽性
  - 防守：SSoT 收斂是否真成立（opencode template + specs + CheckHooks 三向一致）；codex FutureOnly 語意是否對外清楚
  - 體質：fix plan 拆的 7 commits 是否職責清晰；drift_test 新增 D5/D6 是否過大（>250 行考慮拆）；hooks.go 是否應該把 FutureOnly check 抽 helper

**第 3 輪仍需 4 路**（標準 + 攻擊 + 防守 + 體質），不縮減 — 修復後再驗 SSoT 主張確實落地。
