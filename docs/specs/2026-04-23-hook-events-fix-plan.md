# Hook Events PR #616 Fix Plan

- **Date**: 2026-04-23
- **Version**: v2（依 plan codex review `review-mobsgj0y-rgwbdv` 四點 finding 全採納重寫）
- **Spec**: `docs/specs/2026-04-23-lights-rebuild-spec.md` §2.4（架構護欄）
- **Parent plan**: `docs/specs/2026-04-23-hook-events-declaration-plan.md`（既有，11 commits merged 進本分支）
- **Worktree**: `lights-spec-guardrails`（branch `worktree-lights-spec-guardrails`，PR #616）
- **依賴**: Phase 1 merged at alpha.215（§5 L2 對齊完成）；Events() SSoT 已在本分支建立
- **範圍**: 修四路 codex review 攻擊/防守/體質視角提出的 4 個 findings（2 HIGH + 2 MED），讓 Events() SSoT 主張在 codex 與 opencode 端真正成立，且不因升級誤判既有安裝

## 0. v1 → v2 重寫重點

Plan v1 被 codex review `review-mobsgj0y-rgwbdv` 判 needs-attention（2 HIGH + 2 MED finding）。v2 全數採納：

| v1 設計 | v2 更正 | 依據 |
|---|---|---|
| `FutureOnly` 過濾 `SupportedStatuses()`（codex 5→2 status） | `SupportedStatuses()` 完全不碰 FutureOnly，保留為 parser capability 宣告（codex 仍 5 status） | `StatusSupporter` 介面註解明定「獨立於 installer 當前覆蓋」；過濾會打破 proxy/未來 CLI 路徑的假陰性 |
| CheckHooks 對 FutureOnly missing 一律靜默 | 區分三態：absent（tolerated）/ present-but-broken（warning + blocks allInstalled）/ valid（pass） | 防止「已配置但損壞」被混成「未安裝但可接受」 |
| drift fixtures 移除 codex 6 個 FutureOnly event 的 9 筆資料 | **保留全部 fixtures**；parser contract 仍持續測；另拆 capability 面向 | DeriveStatus 解析能力不應因 installer 面向而失去回歸保護 |
| Commit 順序: SupportedStatuses 收斂（2）→ drift 升級（4），中間 commit 紅 | drift 測試升級放在 SupportedStatuses 語意切換之前；**每 commit 真正全綠** | 自相矛盾：原 plan 同時要求每 commit 全綠 + 中間依賴後續 commit 補洞 |

核心哲學修正：**`FutureOnly` 只影響 installer/checker 面向，不影響 DeriveStatus 能力宣告面向**。兩個面向獨立。

## 1. 契約鎖定

本節鎖定全 Fix 行為，避免實作期再決策。

### 1.1 HookEventSpec 新增 FutureOnly 欄位

`internal/agent/provider.go` `HookEventSpec` 結構加 `FutureOnly bool`：

```
type HookEventSpec struct {
    Name        string
    EmitsStatus []Status
    Description string
    FutureOnly  bool // Installer/checker 面向標記；不影響 DeriveStatus 能力宣告
}
```

**語意（v2 收斂版）**：

- **FutureOnly 只影響 installer / checker 面向**：CheckHooks 對缺省 key 的寬鬆判定、installer 後續可能的「若 CLI 版本不支援則略過」策略（本 PR 不實作動態 gate）
- **不影響 DeriveStatus 能力宣告**：`SupportedStatuses()` / `DeriveSupportedStatuses` 不讀此欄位，codex 仍宣告 5 個 status（Idle/Running/Waiting/Error/Clear）
- **不影響 drift 測試的 parser 契約**：fixture 保留完整，`DeriveStatus` 所有解析路徑持續測
- **預設 false**：`FutureOnly=false` 表示 installer 把此 event 當 required（missing = issue）。在 cc 與 opencode 所有 events 均為 false；僅 codex 6 個 event 顯式設 true

**不把語意等同於「CLI 永遠不會支援」**：只表達「installer 已寫入但 runtime 可能不 emit；checker 對此 missing 不當 bug」。

**Inspector UI 未來如何呈現**：未來若想區分「declared」與「currently reliable」，可以讀 `Events()` 中每個 spec 的 `FutureOnly` bit 自行決定顯示；不耦合進 `SupportedStatuses()`。

### 1.2 DeriveSupportedStatuses 不動

`internal/agent/supported_statuses.go` **不改**。仍為：

```
for _, spec := range specs {
    for _, s := range spec.EmitsStatus { ... }
}
```

不過濾 FutureOnly。codex `SupportedStatuses()` 結果不變。

v1 原本的 S1/S2/S3「skip FutureOnly」測試**移除**。改用 trivial 測試確認「新欄位預設 false + 通過 defensive copy」（§2.1）。

### 1.3 三家 provider FutureOnly 標記矩陣

| Provider | Event | FutureOnly | 理由 |
|---|---|---|---|
| cc | 全部 9 個 | false | cc CLI 實際會 emit 全部 9 個 |
| codex | SessionStart / UserPromptSubmit / Stop | false | 主線 codex CLI hooks 長期支援 |
| codex | SubagentStart / SubagentStop / StopFailure / Notification / PermissionRequest / SessionEnd | **true** | codex CLI 目前不保證 emit；DeriveStatus 已能解析；installer 仍安裝為鋪路 |
| opencode | 全部 8 個 | false | plugin template 實際會 emit 全部 8 個 |

codex `SupportedStatuses()` **維持 5 status**（Idle/Running/Waiting/Error/Clear），因為 §1.2 決定不過濾。

**不做 per-CLI-version gate**：版本探測與動態 FutureOnly 翻轉留給未來 phase。

### 1.4 codex CheckHooks 三態判定

`internal/agent/codex/hooks.go` `CheckHooks` 核心改動：對每個 event 依 FutureOnly 與現況分三態處理。

對每個 event，讀 `hooks.json` 對應 key 得到狀態：
- **State A: absent** — hooks.json 無此 event key（或 entries 為空）
- **State B: present-but-broken** — key 存在但找不到正確 pdx command（例：legacy direct-entry、command 不正確、或已被人為改壞）
- **State C: valid** — key 存在且 pdx command 正確

決策表：

| FutureOnly | State | `events[name].Installed` | `Issues` | 影響 `allInstalled` |
|---|---|---|---|---|
| false | A (absent) | false | append `<name> hook not installed` | **是**（blocks） |
| false | B (broken) | false | append `<name> hook: pdx command not found` | **是**（blocks） |
| false | C (valid) | true | — | 不影響 |
| **true** | A (absent) | false | **不 append**（legacy tolerance） | **否**（不 blocks） |
| **true** | B (broken) | false | **append warning** `<name> hook: pdx command malformed (FutureOnly event has existing hook entry but pdx path incorrect — run install to repair)` | **是**（blocks） |
| **true** | C (valid) | true | — | 不影響 |

核心原則：
- **FutureOnly absent** → 是「legacy 尚未升級」或「runtime 未必需要」的寬鬆態，完全靜默
- **FutureOnly present-but-broken** → 已配置卻壞掉，是確實的 bug，必須顯化並 block（避免「綠燈但實際壞」）
- **FutureOnly valid** → 已升級完成，與 non-FutureOnly 同等認定

實作：遍歷 `p.Events()`（取得含 FutureOnly bit 的 spec slice），而非 `p.eventNames()`。對每個 spec：
1. 查 hooks[spec.Name] 現況
2. `hasLegacyPdxDirectCodexEntry` → broken 分支
3. `findPdxCommandInCodex` → valid 分支（有 command）或 broken 分支（key 存在但無 pdx command）
4. key 不存在 → absent 分支
5. 依 spec.FutureOnly 與 state 查表套用決策

**3-event legacy 使用者**（升級後）：SessionStart/UserPromptSubmit/Stop = valid（state C），6 個 FutureOnly = absent（state A）→ Issues 空、allInstalled=true。

**主線使用者**（完整 9 event 新安裝）：全部 valid → Installed=true、Issues=[]。

**部分損壞**：若有 FutureOnly key 被手動改壞 → `Issues` 顯化 warning + allInstalled=false（避免假綠燈）。

### 1.5 opencode renderManagedPlugin 從 Events 生成

Finding #2（PR #616 第 2 輪防守 HIGH + 體質 MED）核心：不讓 template 與 `opencodeEventSpecs` 成為兩份平行 SSoT。

策略 A（選用）：template 保留固定 switch/case 結構，**但渲染時驗證** template 實際 emit 的 pdx event set ⊆ `opencodeEventSpecs.Name` 集合。

具體：

- `renderManagedPlugin(pdxPath string) string` 維持原簽名；函式尾部呼叫 `validateSpecsCoverEmitted(body, opencodeEventSpecs)` — 若 template 字串 emit 的 event name 不在 specs 內 → `panic("contract violation: template emits undeclared event: ...")` 
- `extractEmittedEvents(body string) []string` — 用 regex `emit\(['"](\w+)['"]` 從 body 抽出 emit 的 event names
- `validateSpecsCoverEmitted(body string, specs []HookEventSpec) error` — 強等：`set(extractEmittedEvents(body)) == set(specs.Name)`；任何一側 superset 都回 error

**為什麼 panic 而非 error**：render 函式原簽名回 string，加 error 會改呼叫點。panic 只會在 build 測試階段觸發（因為 template 是 const-like 字串），release path 不會 runtime 爆。

**配合測試**（§2.3 PT1-PT3）驗證：
- PT1：`extractEmittedEvents` 正確從 sample body 抽出 event names
- PT2：`renderManagedPlugin("/p")` 產出的 body emit set == `opencodeEventSpecs.Name` set
- PT3：用 synthetic specs（例移除 `Stop`）餵 `validateSpecsCoverEmitted` → return error（不經 renderManagedPlugin，避免要 mock package var）

### 1.6 opencode CheckHooks per-event emit 驗證

`internal/agent/opencode/hooks.go` `CheckHooks` 對齊 §1.5 extraction 成為 per-event 驗證：

1. 讀檔、驗 marker（不變）
2. 呼叫 `extractEmittedEvents(string(data))` 取 emit set
3. 對每個 `opencodeEventSpecs` event：
   - event name ∈ emit set → `Installed=true, Command=pluginPath`
   - event name ∉ emit set → `Installed=false`，push `<name> emit missing in plugin` 
4. opencode 當前無 FutureOnly event（§1.3），`allInstalled = (全部 events Installed=true)`
5. 若 emit set 含有 specs 沒有的 event name（template 動到但 specs 沒補） → 加 issue `unexpected emit '<name>' not in catalog` + 不影響 allInstalled（此路徑 render 階段已 panic 擋掉，但 CheckHooks 對人為改動的 plugin 檔案仍做防禦）

### 1.7 drift test per-event 強相等 + parser contract 保留

`internal/agent/drift_test.go` 改動原則（v2 收斂）：

- **保留所有既有 fixture**（含 codex 6 個 FutureOnly event 的 9 筆）— parser contract 不退化
- **新增 per-event 強相等斷言**：抓單一 event 過度宣告（Finding #4）
- **所有斷言涵蓋 FutureOnly event**：SupportedStatuses 未過濾，declaredSet 也不過濾，所以三向相等天然包含 FutureOnly
- **不新增 D6 防呆**（v1 曾提議「fixture 不得含 FutureOnly」）— v2 移除該設計；FutureOnly fixture 是刻意的 parser contract 保護

具體改動：

- `TestDriftThreeWayEquality` — **不動**。仍驗三向相等；codex declared/emitted/supported 皆為 5 status set。
- `TestDriftFixtureCoversAllEvents` — **不動**。要求所有 `Events()` event 都有 fixture（含 FutureOnly）。
- `TestDriftFixtureCoverageNonEmpty` — **不動**。
- **新增 `TestDriftPerEventEmitsStatusMatch`**：對每個 provider 的每個 spec（含 FutureOnly），取該 event name 的所有 fixtures，跑 DeriveStatus 收集 `emitSet`（Valid=true && Status != ""），斷言 `emitSet == set(spec.EmitsStatus)`（雙向強相等）。抓單一 event 的 EmitsStatus 過度或不足宣告。

`providerFixtures` **不變**（v1 曾計畫移除 codex 9 筆 FutureOnly fixture；v2 撤回）。

**驗收 D5 有效性**：人為測試把 codex `Stop` spec 的 `EmitsStatus` 從 `[Idle]` 改為 `[Idle, Clear]` → `TestDriftPerEventEmitsStatusMatch` 應紅（Stop fixture 只 emit Idle，spec 多宣告 Clear）。

### 1.8 opencode events.go 對齊 sanity check

從 `plugin_template.go` renderManagedPlugin 靜態分析：
- template emit: `SessionStart`, `PermissionRequest`, `StopFailure`, `Stop`, `SessionEnd`, `UserPromptSubmit`, `SubagentStart`, `SubagentStop`（8 個）
- `opencodeEventSpecs.Name`：同上 8 個

**相等**。§1.5 validate 不會 panic。

### 1.9 零改動邊界

以下檔案 Fix **不得觸碰**：

- `internal/agent/registry.go`, `status.go`, `coverage.go`, `hook_version.go`, `process_info*.go`
- `internal/agent/supported_statuses.go`（v2 移除修改此檔的計畫）
- `internal/agent/cc/**` 全部（cc 所有 event FutureOnly=false 由預設值保證 — 可不顯式標）
- `internal/agent/opencode/status.go`, `provider.go`, `readiness.go`, `events.go`（opencode events 無 FutureOnly 標記 — 預設 false）
- `internal/agent/codex/status.go`, `provider.go`, `readiness.go`, `provider_test.go`（v1 要求 codex supported 測試 5→2 — v2 撤回，provider_test 不動）
- `internal/agent/probe/**`
- `internal/store/**`, `internal/module/**`, `spa/**`

## 2. 測試案例清單

### 2.1 FutureOnly 欄位單元測試

`internal/agent/supported_statuses_test.go` 既有 case 不變。**不新增**「skip FutureOnly」測試（v2 移除）。

`internal/agent/provider_test.go`（若不存在則在 `supported_statuses_test.go` 加）新增：

| # | 名稱 | 斷言 |
|---|---|---|
| F1 | `TestHookEventSpecFutureOnlyDefaultsToFalse` | 宣告一個不指定 FutureOnly 的 HookEventSpec → `FutureOnly=false` |
| F2 | `TestDeriveSupportedStatusesIgnoresFutureOnlyBit` | specs 含 FutureOnly=true + EmitsStatus=[Waiting] → Waiting 仍出現在結果（防止未來被誤加過濾 logic） |

F2 是防呆測試，鎖定「DeriveSupportedStatuses 不因 FutureOnly 過濾」的契約。

### 2.2 codex Events 欄位與 CheckHooks FutureOnly 行為

`internal/agent/codex/events_test.go` 新增：

| # | 名稱 | 斷言 |
|---|---|---|
| CE1 | `TestCodexEventsFutureOnlyFlags` | 3 個非 FutureOnly（SessionStart/UserPromptSubmit/Stop）+ 6 個 FutureOnly，一一 assert `spec.FutureOnly` bit |
| CE2 | `TestCodexEventsDefensiveCopyPreservesFutureOnly` | 取兩次 `Events()`、mutate 第一次的 FutureOnly → 第二次不受影響 |

`internal/agent/codex/hooks_test.go` 新增（用既有 writing temp hooks.json + pdx path 的 helper 模式；若 helper 不存在則仿 cc 的 pattern）：

| # | 名稱 | 情境 | 斷言 |
|---|---|---|---|
| CH1 | `TestCheckHooks_LegacyThreeEvent_ReportsInstalled` | hooks.json 僅 SessionStart/UserPromptSubmit/Stop valid（FutureOnly 6 absent） | `allInstalled=true`、`Issues=[]`、6 個 FutureOnly `Events` map entry `Installed=false` |
| CH2 | `TestCheckHooks_NineEvent_FullyInstalled_NoIssues` | hooks.json 9 events 全 valid | `allInstalled=true`、`Issues=[]`、9 個 entry `Installed=true` |
| CH3 | `TestCheckHooks_RequiredEventMissing_Blocks` | 只有 2 個 required event valid（缺 SessionStart） | `allInstalled=false`、Issues 含 `SessionStart hook not installed` |
| CH4 | `TestCheckHooks_FutureOnlyBroken_WarnsAndBlocks` | hooks.json 3 required valid + 1 FutureOnly（Notification）key 存在但 command 錯誤 | `allInstalled=false`、Issues 含 `Notification hook: pdx command malformed...`、Notification Installed=false |
| CH5 | `TestCheckHooks_FutureOnlyAbsent_DoesNotBlock` | hooks.json 3 required valid + 3 FutureOnly absent + 3 FutureOnly valid | `allInstalled=true`、Issues=[]、3 absent entries Installed=false、3 valid entries Installed=true |

### 2.3 opencode template 與 CheckHooks 對齊

`internal/agent/opencode/plugin_template_test.go` 新增：

| # | 名稱 | 斷言 |
|---|---|---|
| PT1 | `TestExtractEmittedEvents` | helper 從 sample body 正確抽出 event names（含 regex corner：字串插值前後空白、單引號 vs 雙引號、兩個 emit 同行） |
| PT2 | `TestValidateSpecsCoverEmitted_Equal` | `validateSpecsCoverEmitted(body, opencodeEventSpecs)` 對真 template body 回 nil |
| PT3 | `TestValidateSpecsCoverEmitted_EmitNotInSpec` | synthetic body 含 `emit('Fake')` + specs 無 Fake → error |
| PT4 | `TestValidateSpecsCoverEmitted_SpecNotInEmit` | synthetic body 缺某 event + specs 含它 → error |
| PT5 | `TestRenderManagedPlugin_ProducesValidBody` | `renderManagedPlugin("/p")` 不 panic；結果 body 含 marker + 8 個 event emit |

### 2.4 opencode hooks CheckHooks per-event emit

`internal/agent/opencode/hooks_test.go` 新增：

| # | 名稱 | 斷言 |
|---|---|---|
| OH1 | `TestCheckHooks_ValidPlugin_PerEventInstalled` | 用 `writeManagedPlugin` 寫合法 template → CheckHooks 每個 event `Installed=true`、Issues=[] |
| OH2 | `TestCheckHooks_HandEditedPlugin_EventMissing` | 寫一份含 marker 但移除某 `emit('Stop',` 的 body → CheckHooks 該 event `Installed=false`、Issues 含 `Stop emit missing in plugin` |
| OH3 | `TestCheckHooks_UnmanagedPlugin_ReturnsUnmanaged` | 既有行為保留：不帶 marker → Installed=false + Issues |
| OH4 | `TestCheckHooks_UnexpectedEmitInPlugin_Flagged` | 寫一份含 marker + 額外 `emit('Fake', ...)` 的 body → Issues 含 `unexpected emit 'Fake' not in catalog`、allInstalled 不因此 false（現有 8 個 event 仍 valid） |

OH4 測 §1.6 對「template 動到、specs 沒補」的防禦路徑。

### 2.5 drift test per-event 強相等

`internal/agent/drift_test.go` 新增：

| # | 名稱 | 斷言 |
|---|---|---|
| D5 | `TestDriftPerEventEmitsStatusMatch` | 對每個 provider 的每個 spec：取該 event 的 fixtures → emitSet == set(spec.EmitsStatus) |

D5 天然覆蓋 FutureOnly event（因為 fixtures 與 declaredSet 都包含）。**無 D6**（v1 的 FutureOnly fixture 禁令移除）。

既有 D1/D2/D3/D4 不動。`providerFixtures` 不變。

## 3. TDD 執行順序

嚴格紅綠，每個 step 一個 commit；commit message 用 Conventional Commits + `Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>`。

**關鍵**：每 commit 完跑 `go build ./... && go test ./... && go vet ./...` **必須全綠**。v2 下這可滿足，因為：
- DeriveSupportedStatuses 不變 → codex SupportedStatuses 仍 5 status → drift ThreeWayEquality 不會因 FutureOnly 標記而破
- fixtures 不移除 → parser drift 不退化
- CheckHooks 內部行為變化只影響 codex/hooks_test.go 本身

### Commit 1 — `feat(agent): add FutureOnly bit to HookEventSpec`
- 紅：寫 F1 + F2 → 失敗（F1 編譯錯 / F2 尚未驗證）
- 綠：`provider.go` 加 `FutureOnly bool` 欄位 + 註解（§1.1）；`DeriveSupportedStatuses` **不動**；F1/F2 綠
- 檢驗：cc / codex / opencode `Events()` 的 defensive copy 需保留 FutureOnly bit（目前 three-field literal copy 自動涵蓋 struct 所有欄位 — 確認一下）
- 跑 `go test ./internal/agent/...` 全綠

### Commit 2 — `feat(agent/codex): mark 6 future-only events`
- 紅：寫 CE1 → 失敗（欄位未標 true）
- 綠：`codex/events.go` 把 SubagentStart / SubagentStop / StopFailure / Notification / PermissionRequest / SessionEnd 六個 event 加 `FutureOnly: true`
- 補：CE2（defensive copy 保留 FutureOnly bit）
- 跑 `go test ./internal/agent/codex/...` 綠（現有 `TestCodexSupportedStatuses` 不動，仍 5 status — v2 下不需改）
- 跑 `go test ./internal/agent/...` 全綠（drift 三向相等仍成立）

### Commit 3 — `feat(agent/codex): three-state CheckHooks with FutureOnly awareness`
- 紅：寫 CH1-CH5 → 失敗（CheckHooks 尚未 aware）
- 綠：`codex/hooks.go` 改 CheckHooks 遍歷用 `p.Events()`（取得 FutureOnly bit），決策表依 §1.4 實作三態
- 留意：`hasLegacyPdxDirectCodexEntry` / `findPdxCommandInCodex` 已區分 broken state，主要改動是「broken + FutureOnly → warning issue + block」（以前並無 FutureOnly 概念，所有 broken 都 block）
- 跑 `go test ./internal/agent/codex/...` 綠

### Commit 4 — `test(agent): per-event drift assertion`
- 紅：寫 D5 → 對既有 spec/fixture 跑起來可能紅（若有 over-declaration）或直接綠
- 綠：若紅，修 spec 或 fixture 對齊（最可能 case：cc 的 `Notification` EmitsStatus `[Waiting, Idle]` 對 fixtures 的 4 筆全綠 — 應直接綠）
- 跑 `go test ./internal/agent/...` 綠

### Commit 5 — `refactor(agent/opencode): extract emitted events helper`
- 紅：寫 PT1 → 失敗（helper 不存在）
- 綠：`plugin_template.go` 加 `extractEmittedEvents(body string) []string`
- 跑 `go test ./internal/agent/opencode/...` 綠

### Commit 6 — `feat(agent/opencode): validate template emits declared events`
- 紅：寫 PT2 + PT3 + PT4 + PT5 → 失敗
- 綠：加 `validateSpecsCoverEmitted(body string, specs []HookEventSpec) error`；`renderManagedPlugin` 結尾呼叫 validate，mismatch panic
- PT5 確認正常 template 不 panic
- 跑 `go test ./internal/agent/opencode/...` 綠

### Commit 7 — `feat(agent/opencode): CheckHooks verifies per-event emit`
- 紅：寫 OH1 + OH2 + OH3 + OH4 → 失敗（CheckHooks 仍假綠回填）
- 綠：`opencode/hooks.go` CheckHooks 依 §1.6 改寫：讀檔 → extract → per-event 檢查 + 超集防禦
- 跑 `go test ./internal/agent/opencode/...` 綠

### Commit 8 — `docs: hook events fix plan retrospective（可選）`
若 §3 順序與實際有偏差，補歷史註記。

**Commit 綠燈分析**：
- Commit 1 → DeriveSupportedStatuses 不變；codex SupportedStatuses 5 status 不變；drift 測試仍綠
- Commit 2 → 只是在 event spec 上加 FutureOnly bit；無消費者讀取 → 所有行為不變
- Commit 3 → CheckHooks 行為變，但只影響 `codex/hooks_test.go` 與新增 CH1-CH5
- Commit 4 → D5 新增；既有 D1/D2/D3/D4 + fixtures 不變
- Commit 5/6/7 → opencode package 內部
- **每一 commit 後全套 go test 綠**

## 4. 實作檔案預估

| 檔案 | 動作 | 預估行數 |
|---|---|---|
| `internal/agent/provider.go` | +`FutureOnly bool` 欄位 + 註解 | +8 |
| `internal/agent/supported_statuses_test.go` 或 `provider_test.go` | +F1/F2 | +25 |
| `internal/agent/codex/events.go` | 6 event 加 `FutureOnly: true` | +6 |
| `internal/agent/codex/events_test.go` | +CE1/CE2 | +40 |
| `internal/agent/codex/hooks.go` | CheckHooks 三態決策 | +25 |
| `internal/agent/codex/hooks_test.go` | +CH1-CH5 | +120 |
| `internal/agent/opencode/plugin_template.go` | +`extractEmittedEvents` + `validateSpecsCoverEmitted` + render panic | +40 |
| `internal/agent/opencode/plugin_template_test.go` | +PT1-PT5 | +80 |
| `internal/agent/opencode/hooks.go` | CheckHooks per-event emit + unexpected defense | +25 |
| `internal/agent/opencode/hooks_test.go` | +OH1-OH4 | +90 |
| `internal/agent/drift_test.go` | +D5（無 fixture 刪除、無 FutureOnly skip） | +45 |
| `docs/specs/2026-04-23-hook-events-fix-plan.md` | 本檔 v2 | +400 |
| **合計** | | **~900 行**（含 plan 文件） |

## 5. 不做項目清單（防自動擴展）

以下衝動一律拒絕：

- **不過濾 FutureOnly 於 `SupportedStatuses()` 或 `DeriveSupportedStatuses`** — v2 核心決定；F2 測試鎖定此契約
- **不移除任何既有 fixture** — parser contract 必須持續測
- **不新增「fixture 禁含 FutureOnly」測試** — v1 D6 移除
- **不改 codex `provider_test.go` SupportedStatuses 斷言**（仍 5 status）
- **不探測 codex CLI 版本** — FutureOnly 是 build-time 靜態標記
- **不引入 `/api/hooks/codex/capability` endpoint**
- **不重寫 opencode template 為完全 codegen**
- **不把 cc 任何 event 標為 FutureOnly**
- **不調整 `Coverage()` 簽名或回傳欄位**
- **不改 SPA 任何檔案**
- **不 force push / rebase** — 保留全歷史
- **不合併 fix commits** — 分 7 commits 便於 reviewer 逐步追蹤
- **不順手修其他 finding** — 四路 review 只有這 4 個 finding

## 6. Subagent 開發指引

- **cwd 強制前綴**：每個 Bash 指令以 `cd /Users/wake/Workspace/wake/purdex/.claude/worktrees/lights-spec-guardrails && ` 開頭
- **分支**：已在 `worktree-lights-spec-guardrails`，不另切；**不 push**（主 session 負責）
- **Commit message 格式**：Conventional Commits + 結尾 `Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>`
- **TDD 嚴格紅綠**：每 commit 內先寫測試跑紅再實作跑綠；不可批次寫完所有 test + 一次實作
- **每 commit 完成後**：跑 `go build ./... && go test ./... && go vet ./...` 全綠才進下一個；**不可中間紅燈**
- **關鍵抽 helper 點**：
  - codex `CheckHooks`：建議新增私有 `checkCodexEvent(spec HookEventSpec, rawEntries any) (HookEventInfo, issues []string, block bool)` helper 把三態判定抽出，避免 CheckHooks 變 60+ 行巨型函式
  - opencode `CheckHooks`：`extractEmittedEvents` + `validateSpecsCoverEmitted` 都已是 helper
- **回報**：完成時給 commit hash 列表 + `git log --oneline -15` + 最終 `go test ./...` 完整輸出 + `go vet ./...` 無 warning 確認

## 7. 驗收清單

- [ ] 7 個 commits 符合上述 message 規範
- [ ] `go build ./...` 綠
- [ ] `go test ./...` 全綠（含 F1-F2 / CE1-CE2 / CH1-CH5 / D5 / PT1-PT5 / OH1-OH4 共 19 個新測試）
- [ ] `go vet ./...` 無 warning
- [ ] PR diff 僅涉及 §4 表格列出的 12 個檔 + plan 文件，**其餘不得出現**
- [ ] codex `SupportedStatuses()` 仍回傳 5 個 Status（不動）
- [ ] codex `Events()` 中 6 個 FutureOnly event 的 bit 為 true，其餘 false
- [ ] codex `CheckHooks()` 對 legacy 3-event hooks.json 回 `allInstalled=true` + Issues=[]
- [ ] codex `CheckHooks()` 對「FutureOnly key 存在但 command 錯誤」回 `allInstalled=false` + warning issue
- [ ] opencode `CheckHooks()` 對手改過、少 emit 的 plugin 正確標 `Installed=false` + issue
- [ ] opencode `renderManagedPlugin` 對人為構造的不合法 specs/body 會 panic（僅在 mock 下 — 真 runtime 不 panic）
- [ ] drift per-event 斷言（D5）對每個 provider/spec 覆蓋正確

## 8. 風險與緩解

| 風險 | 機率 | 影響 | 緩解 |
|---|---|---|---|
| FutureOnly 語意仍可能被後續開發者誤用為 SupportedStatuses 過濾 | 中 | 未來 regression | F2 測試鎖定契約；`provider.go` FutureOnly 欄位註解明寫「不影響 DeriveStatus 能力宣告」 |
| CheckHooks 三態決策複雜度累積 | 低 | 新增 FutureOnly 時 regression | 抽 `checkCodexEvent` helper；CH1-CH5 覆蓋所有交叉組合 |
| `extractEmittedEvents` regex 遇到 JS 註解誤判 | 低 | CheckHooks 假陽性 | PT1 測 edge case（註解、插值、單雙引號） |
| opencode render panic 在 production 路徑誤觸 | 低 | Install 失敗 | template 是 const-like 字串；panic 只在 specs/template 不同步時發生；PT5 常態 smoke test |
| drift fixtures 保留後，若 FutureOnly event 在 DeriveStatus 邏輯日後被刪除但 fixture 沒刪 | 低 | 測試失敗警告到位 | D5 per-event 強相等會紅，指向確切 spec/event；是有益的警告 |
| commit 4 D5 對既有 spec/fixture 跑起來紅（有過度宣告） | 中 | 需額外修 spec 或 fixture | 先跑一次 D5 preview；若紅先修 spec 再加 D5 斷言 |

## 9. 第 3 輪 Codex Review 預期 focus

**Focus text 骨架**（派發時套 4 findings + v2 設計決策）：

- 本輪修復 PR #616 第 2 輪 3 路 review 的 4 findings（+ plan review 4 points）：
  1. codex CheckHooks 對 3-event legacy 使用者誤判（透過 FutureOnly + 三態判定）
  2. opencode renderManagedPlugin 與 CheckHooks 的假綠燈（透過 extractEmittedEvents + validate + CheckHooks per-event）
  3. codex future-only events 混入 current capability（透過 FutureOnly bit，**但不影響 SupportedStatuses**；留給 Inspector UI 自行判讀）
  4. drift test per-event 盲點（透過 D5 per-event 強相等）
- 請驗收：
  - 攻擊：FutureOnly 三態決策的邊界（absent/broken/valid × required/optional）；extractEmittedEvents regex 假陽性；opencode render panic 是否能在 release path 誤觸
  - 防守：SSoT 主張是否仍成立（FutureOnly 不削減 SupportedStatuses，那 Inspector 要怎麼知道 codex 目前只能 emit 哪些？— plan 有無明確交代 future 路徑）
  - 體質：fix plan 拆的 7 commits 是否職責清晰；drift_test 新 D5 是否過大；hooks.go `checkCodexEvent` helper 是否正確抽出三態判定

第 3 輪仍需 4 路（標準 + 攻擊 + 防守 + 體質）。
