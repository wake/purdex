# Hook Events Declaration TDD Plan — `HookInstaller.Events()`

- **Date**: 2026-04-23
- **Spec**: `docs/specs/2026-04-23-lights-rebuild-spec.md` §2.4.3（三家 Agent 事件對齊策略）+ §12 檔案速查
- **Related issue**: #613（codex installer alignment）
- **Worktree**: `lights-hook-events-declaration`（branch `worktree-lights-hook-events-declaration`，後續開 worktree 時建立）
- **依賴**: Phase 0 (`StatusSupporter` + `Coverage`) + Phase 1 (三家 `SupportedStatuses()` + codex 5 事件 + drift test) — 皆已 merged
- **是否開新 Phase**：**否**。§2.4.3 明確以「併入 issue #613 擴展（`HookInstaller.Events()`）」作自然解，不佔 Phase 編號

## 0. 動機與一句話目標

把 `HookInstaller` 介面擴充一個宣告方法 `Events()`，讓「installer 裝什麼事件」「`DeriveStatus` 認識什麼事件」「每個事件能 emit 什麼 Status」「Inspector 要顯示什麼」**共用同一筆宣告**。同時把 `SupportedStatuses()` 從硬編碼改為從 `Events()` 的 `EmitsStatus` union 自動 derive，徹底消除宣告 drift 的源頭。

這次 PR **只收斂宣告層**（build-time 可查），不動 runtime 的 handler / probe / frame / broadcast 流程。符合 §2.4.1「build-time 可做、runtime 不做」判準。

## 1. 契約鎖定

本節鎖定全部行為細節，subagent 實作期不需再決策。

### 1.1 `HookEventSpec` 宣告結構

新型別，放在 `internal/agent/provider.go`（與 `HookInstaller` 同一「Optional capabilities」區塊），欄位：

```
HookEventSpec:
  Name         : string       -- 事件名稱，如 "Notification"；與 hook JSON 的 event_name / pdx hook CLI subcommand 一致
  EmitsStatus  : []Status     -- 該事件透過 DeriveStatus 可能產生的非空 Status slice；空 slice 合法（detail-only，如 SubagentStart/Stop）
  Description  : string       -- 一句話人讀說明，給 Inspector UI 顯示；英文；末尾不加句號
```

**語意規則**：

- `EmitsStatus` 是**非空 Status 集合**（`Status != ""`）；若事件 `DeriveStatus` 永遠回 `Valid=true, Status=""`（detail-only），`EmitsStatus = []agent.Status{}`（空 slice，非 nil）
- `EmitsStatus` 允許重複避免 subagent 煩惱去重（drift 測試以 set 比對；實際建議三家都寫 unique，但不強制）
- `EmitsStatus` 對 polymorphic branch（如 cc `Notification`）列出**所有 sub-branch 可能 Status 的聯集**（例：`{Waiting, Idle}`）
- `Description` 語言：**英文**（與既有 hook event name 同語系，便於 Inspector UI 不做 i18n 先行）；長度建議 < 70 字；不加句號；不使用 emoji

### 1.2 `HookInstaller.Events()` 介面擴充

`HookInstaller` 增加方法（虛擬碼）：

```
HookInstaller (interface):
  InstallHooks(pdxPath string) error          -- existing
  RemoveHooks(pdxPath string) error           -- existing
  CheckHooks() (HookStatus, error)            -- existing
  Events() []HookEventSpec                    -- NEW, required for all HookInstaller implementors
```

**`Events()` 是 required**（非 optional）。一旦實作 `HookInstaller`，必須同時實作 `Events()` — 這讓宣告與安裝綁在同一個 interface，避免第四個 provider 日後只裝不宣告。

**語意**：回傳此 provider 的 hook installer 安裝的**所有事件 + 各自能 emit 的 Status + 描述**。回傳 slice 為 fresh copy（defensive — 比照 Phase 1 `SupportedStatuses()` 慣例），元素順序與 installer 安裝順序一致（與現有 `ccHookEvents` / `codexHookEvents` / `opencodeHookEvents` var 順序對齊）。

### 1.3 `*HookEvents` package var 的去留

現況三家各自有 `ccHookEvents` / `codexHookEvents` / `opencodeHookEvents` 字串 slice，被 `InstallHooks` / `CheckHooks` / `writeManagedPlugin` 迭代。

**決議**：**刪除**三個 package var；改由 `Events()` 作為單一真相來源（SSoT）。內部使用端（`mergeClaudeHooks` / `mergeCodexHooks` / opencode plugin 模板）改為：

- 定義 package-private helper：`eventNames() []string { return map(p.Events(), spec -> spec.Name) }`（虛擬碼）
- installer / check 迴圈從 `for _, event := range ccHookEvents` 改為 `for _, event := range p.eventNames()`
- opencode 的 `writeManagedPlugin` / `renderManagedPlugin` 若需要事件清單（看檔案內實作），同樣走 helper；若 template 檔是固定字串就保持原狀，只要 `CheckHooks` 用 helper 即可

**為什麼 SSoT 在 `Events()`**：多個來源註定 drift（Phase 1 已證明 `SupportedStatuses` 硬編碼的維護成本）；package var 與 `Events()` 並存 = 第二個真相源。

**例外**：const 型 version 字串（`ccHooksSupportedVersion` / `codexHooksSupportedVersion`）**不動**，仍為 package const — 它們不是事件宣告。

### 1.4 三家 `Events()` 宣告內容

#### cc（9 事件）

| Name                | EmitsStatus                                        | Description                                                  |
|---------------------|----------------------------------------------------|--------------------------------------------------------------|
| `SessionStart`      | `[Idle]`                                           | Claude Code session started (non-compact source)             |
| `UserPromptSubmit`  | `[Running]`                                        | User submitted a prompt to the agent                         |
| `SubagentStart`     | `[]` (detail-only)                                 | Nested sub-agent task dispatched                             |
| `SubagentStop`      | `[]` (detail-only)                                 | Nested sub-agent task completed                              |
| `Stop`              | `[Idle]`                                           | Agent finished responding and is idle                        |
| `StopFailure`       | `[Error]`                                          | Agent stopped due to an error                                |
| `Notification`      | `[Waiting, Idle]`                                  | Permission/elicitation prompt, idle prompt, or auth success  |
| `PermissionRequest` | `[Waiting]`                                        | Tool permission request awaiting user approval               |
| `SessionEnd`        | `[Clear]`                                          | Claude Code session ended                                    |

`Notification` 的 sub-branch 邏輯（`permission_prompt` / `elicitation_dialog` → Waiting；`idle_prompt` / `auth_success` → Idle；其他 → Valid=false）由 `deriveCCStatus` 實作內部處理；`EmitsStatus` 只列聯集。

#### codex（擴展：3 事件 → 9 事件；對齊 cc）

**重點**：本 PR 擴展 codex installer 清單從目前的 3 個（`SessionStart` / `UserPromptSubmit` / `Stop`）到 9 個，對齊 cc 與 codex `deriveCodexStatus` 已支援的完整集合（Phase 1 已讓 codex `DeriveStatus` 認識 9 個事件，installer 端尚未跟進 — 即 issue #613 alignment gap）。

擴展後：

| Name                | EmitsStatus          | Description                                                  |
|---------------------|----------------------|--------------------------------------------------------------|
| `SessionStart`      | `[Idle]`             | Codex session started                                        |
| `UserPromptSubmit`  | `[Running]`          | User submitted a prompt                                      |
| `SubagentStart`     | `[]` (detail-only)   | Nested sub-agent task dispatched                             |
| `SubagentStop`      | `[]` (detail-only)   | Nested sub-agent task completed                              |
| `Stop`              | `[Idle]`             | Agent finished responding and is idle                        |
| `StopFailure`       | `[Error]`            | Agent stopped due to an error                                |
| `Notification`      | `[Waiting, Idle]`    | Permission/elicitation/idle prompt notifications             |
| `PermissionRequest` | `[Waiting]`          | Tool permission request awaiting user approval               |
| `SessionEnd`        | `[Clear]`            | Codex session ended                                          |

**擴展 vs 現況對照**（issue #613 alignment 的核心）：

```
Before (installer writes to ~/.codex/hooks.json):   [SessionStart, UserPromptSubmit, Stop]            -- 3
After  (installer writes 9 events):                 [SessionStart, UserPromptSubmit, SubagentStart,
                                                     SubagentStop, Stop, StopFailure, Notification,
                                                     PermissionRequest, SessionEnd]                   -- 9

Before (DeriveStatus knows):    9 events (Phase 1 completed)
After:                          9 events                 -- unchanged, already aligned
```

實際 codex CLI 當前版本可能不會 emit 新增的 6 個事件（proxy 路徑或未來 CLI 才會）— 這**不是問題**，§8 風險表已接受此設計原則（drift test 保證宣告與 `DeriveStatus` 實測一致；未來 CLI / proxy 發生即用）。

#### opencode（8 事件，沿用現況）

| Name                | EmitsStatus          | Description                                                  |
|---------------------|----------------------|--------------------------------------------------------------|
| `SessionStart`      | `[Idle]`             | OpenCode session started                                     |
| `UserPromptSubmit`  | `[Running]`          | User submitted a prompt                                      |
| `SubagentStart`     | `[]` (detail-only)   | Nested sub-agent task dispatched                             |
| `SubagentStop`      | `[]` (detail-only)   | Nested sub-agent task completed                              |
| `PermissionRequest` | `[Waiting]`          | Tool permission request awaiting user approval               |
| `Stop`              | `[Idle]`             | Agent finished responding and is idle                        |
| `StopFailure`       | `[Error]`            | Agent stopped due to an error                                |
| `SessionEnd`        | `[Clear]`            | OpenCode session ended                                       |

### 1.5 `SupportedStatuses()` 改為 derive

三家 `provider.go` 現況是硬編碼 `return []Status{Running, Waiting, Idle, Error, Clear}`（Phase 1 commit 寫死）。改為：

```
SupportedStatuses():
  union = {}
  for each spec in Events():
    for each status in spec.EmitsStatus:
      union[status] = true
  return sorted(union)    -- sorted by Status string for deterministic output
```

實作時抽一個 package-level helper（在 `internal/agent/` 主 package，非 provider 子 package），例如：

```
DeriveSupportedStatuses(events []HookEventSpec) []Status
```

三家各自的 `SupportedStatuses()` 變為：

```
p.SupportedStatuses() -> return DeriveSupportedStatuses(p.Events())
```

**排序策略**：helper 對 Status 字串做 lexicographic sort，確保 `SupportedStatuses()` 回傳確定性順序。Phase 1 的硬編碼順序（Running, Waiting, Idle, Error, Clear）會改變 — 調查現況所有 callers 對順序的依賴（預期：無 — 用 set 語意）並在 §8 標示風險。

### 1.6 Drift 測試三向升級

現況 `internal/agent/drift_test.go` 做兩件事：per-fixture 斷言 + `declared vs emitted` set 比較。升級後做**三向斷言**：

1. **宣告集**：`union(Events().EmitsStatus)` — 由 `Events()` derive
2. **實測集**：對 `Events()` 每個 `Name`，以 fixture 驅動 `DeriveStatus`，收集 `Valid=true && Status!=""` 的 Status
3. **derive 集**：`SupportedStatuses()` 的回傳（現在也是 derived，應與宣告集相等）

三者需**兩兩相等**（等價於三集合相等）。任一不一致即 fail，錯誤訊息列出差集（哪個 Status 在哪個集合存在、另兩個缺）。

per-fixture 斷言（Phase 1 的 codex review 堅持的反退化保險）**保留**：`providerFixtures` map 仍列每個事件的 raw payload + `wantStatus`，確保「刪除 Notification 某個 sub-branch」能被抓到（set 比對單獨做不到，Phase 1 已證明）。

**新增檢查**：每個 `Events()` 的 `Name` 必須在 `providerFixtures` 出現至少一次（確保宣告的事件都有實測；現況 Phase 1 fixture 已覆蓋 cc/codex 9 事件與 opencode 8 事件，本 PR 驗證這點為強制）。

### 1.7 Hooks installer 內部使用

**cc (`mergeClaudeHooks`)**：迭代從 `ccHookEvents` 改為 `p.eventNames()`（或直接 `for _, spec := range p.Events() { event := spec.Name; ... }`）。`filterOutPdx` + `makePdxEntry` 不動（只動 iteration source）。

**codex (`mergeCodexHooks`)**：同上；迭代從 `codexHookEvents` 改為 `p.eventNames()`。由於 codex 事件數從 3 擴到 9，安裝效果變化：用戶首次跑 `InstallHooks` 會把 6 個新事件寫入 `~/.codex/hooks.json`；既有用戶重跑（`pdx install cc/codex` 或等價 CLI）時 6 新事件也會被補入（`filterOutPdxCodex` + append 的既有邏輯自然處理）。

**opencode (`writeManagedPlugin` + `CheckHooks`)**：`CheckHooks` 的事件迴圈從 `opencodeHookEvents` 改為 `p.eventNames()`。`writeManagedPlugin` 若模板為字串常數包含硬編碼事件列表則保持（模板內容不動，除非 `renderManagedPlugin` 內有迴圈 — 進 subagent 實作時視 `plugin_template.go` 內容決定是否改）。本 PR 不動 opencode plugin_template 的字串模板（§1.8 零改動邊界）。

**`CheckHooks` 三家**：事件迴圈同樣改走 `p.eventNames()`。

### 1.8 零改動邊界

本 PR **不得觸碰**：

- `internal/agent/registry.go`、`internal/agent/coverage.go`（Coverage helper 已是通用，`Events()` 擴充透過 `HookInstaller` 入口，不動 Coverage）
- `internal/agent/status.go`（Status enum 不變）
- `internal/agent/probe/**`（probe 層完全獨立）
- `internal/agent/hook_version*.go` / `process_info*.go`
- `internal/module/agent/handler.go` 的流程邏輯（catalog-miss 早退是 Phase 1 既有能力，本 PR 不重構）
- `internal/module/**` 除了必要的測試（預期為零 — 本 PR 不改 module 層）
- `internal/store/**`
- `internal/agent/opencode/plugin_template.go` 的字串模板（事件名稱若在模板內是硬編碼字串則沿用；只動 CheckHooks iteration）
- `spa/**`（Inspector UI 是後續工作，本 PR 不動）
- `/api/agent/monitor/events` endpoint — **不新增**（留 Phase 5）

## 2. 測試案例清單

按檔案組織；全部描述以文字表達，不含 Go。

### 2.1 三家 `Events()` 單元測試

| # | 檔案 | 名稱 | 斷言 |
|---|---|---|---|
| E1 | `cc/hooks_test.go`（或新檔 `cc/events_test.go`） | `TestCCEvents_Count` | `len(Events()) == 9` |
| E2 | 同上 | `TestCCEvents_NamesMatchExpected` | Events 的 Name set 為 §1.4 cc 9 項 |
| E3 | 同上 | `TestCCEvents_EmitsStatusForNotification` | `Notification` 項 `EmitsStatus` set 為 `{Waiting, Idle}` |
| E4 | 同上 | `TestCCEvents_DetailOnlyHaveEmptyEmitsStatus` | `SubagentStart` 與 `SubagentStop` 的 `EmitsStatus` 長度為 0（非 nil — 空 slice） |
| E5 | 同上 | `TestCCEvents_DescriptionsNonEmpty` | 每項 Description 長度 > 0 且無 emoji |
| E6 | 同上 | `TestCCEvents_FreshSliceDefensiveCopy` | 連續呼叫兩次 Events 回傳不同 backing array；mutate 第一次結果不影響第二次 |
| E7 | `codex/events_test.go` | `TestCodexEvents_ExpandedTo9` | `len(Events()) == 9`；Name set 為 §1.4 codex 9 項（**對應 issue #613 擴展驗證**） |
| E8 | 同上 | `TestCodexEvents_EmitsStatusForNotification` | 同 E3 |
| E9 | 同上 | `TestCodexEvents_DetailOnlyHaveEmptyEmitsStatus` | 同 E4 |
| E10 | `opencode/events_test.go` | `TestOpenCodeEvents_Count` | `len(Events()) == 8` |
| E11 | 同上 | `TestOpenCodeEvents_NamesMatchExpected` | Name set 為 §1.4 opencode 8 項 |

### 2.2 `SupportedStatuses` derive 測試

| # | 檔案 | 名稱 | 斷言 |
|---|---|---|---|
| S1 | `internal/agent/supported_statuses_test.go`（新檔，package `agent_test`） | `TestDeriveSupportedStatuses_UnionAndSort` | 輸入 spec list 包含 `{EmitsStatus:[Running,Waiting]}` + `{EmitsStatus:[Idle]}` + `{EmitsStatus:[]}` → 回傳 `[Idle, Running, Waiting]`（lex sort） |
| S2 | 同上 | `TestDeriveSupportedStatuses_DedupesDuplicates` | 同一 Status 在多 spec 出現 → union 後只出現一次 |
| S3 | 同上 | `TestDeriveSupportedStatuses_EmptyInput` | 空 spec list → 回傳 empty slice（長度 0 但非 nil，統一行為） |
| S4 | 三家 `provider_test.go` | `TestCC/Codex/OpenCodeSupportedStatuses_DerivesFromEvents` | 斷言 `SupportedStatuses()` 回傳 set 與 `union(Events().EmitsStatus)` set 相等（兩 line assertion 證 derive 正確） |

### 2.3 Installer iteration 測試

| # | 檔案 | 名稱 | 斷言 |
|---|---|---|---|
| I1 | `cc/hooks_test.go` | `TestCCInstallHooks_WritesAllEventsFromEventsList` | 對 temp settings.json 跑 `InstallHooks` → 讀回 JSON → `hooks` 物件 key set 等於 `Events()` 的 Name set（動態驗證，不硬編碼 9） |
| I2 | `cc/hooks_test.go` | `TestCCCheckHooks_ReportsAllEventsFromEventsList` | 寫一個缺 `Notification` 的 settings.json → `CheckHooks()` 的 `Events` map key set 包含 Events Name set；`Notification` 的 `Installed=false` |
| I3 | `codex/hooks_test.go` | `TestCodexInstallHooks_Writes9EventsAfterExpansion` | 跑 `InstallHooks` → 讀 hooks.json → 寫入 key set 等於擴展後的 9 個；**特別驗證 `SessionEnd` / `Notification` / `PermissionRequest` 已被寫入**（明確的 regression guard，對應 issue #613 修復） |
| I4 | `codex/hooks_test.go` | `TestCodexCheckHooks_ReportsAll9Events` | 同 I2，key set 為擴展後 9 項 |
| I5 | `opencode/hooks_test.go` | `TestOpenCodeCheckHooks_ReportsAll8EventsFromEventsList` | `CheckHooks()` 的 `Events` map key set 等於 `Events()` 的 Name set |

Note: I3 / I4 是 issue #613 的核心 regression guard — 任何未來讓 codex installer 倒退回 3 事件的改動會被抓到。

### 2.4 Drift 測試升級

改 `internal/agent/drift_test.go`：

| # | 名稱 | 斷言 |
|---|---|---|
| D1' | `TestDriftThreeWayEquality`（改名自 `TestDriftDeclaredEqualsEmitted`） | 對三家 provider：`declaredSet`（從 Events union）== `emittedSet`（fixture 驅動 DeriveStatus）== `supportedSet`（`SupportedStatuses()` 回傳）；錯誤訊息列出三集合差異 |
| D2 | `TestDriftFixtureCoverageNonEmpty` | 不變（防呆：fixture 非空） |
| D3 | `TestDriftFixtureCoversAllEvents` | 每家 provider 的 `Events()` 所有 Name 都在 `providerFixtures` 出現至少一次（新增防呆；對應 §1.6 新增檢查） |

### 2.5 Registry / Coverage 整合測試

`internal/agent/coverage_test.go` 現有 C1（Phase 1 `TestCoverageRealProviders`）**不動**。新增（同檔）：

| # | 名稱 | 斷言 |
|---|---|---|
| C2 | `TestCoverageDeclaredMatchesEventsUnion` | 對三家 provider，`CoverageRow.Declared` set 與 `union(Events().EmitsStatus)` set 相等（跨檔契約驗證） |

### 2.6 Handler 相關 regression

無需新增測試。本 PR 不動 handler；既有 `internal/module/agent/handler_test.go` 全套應保持綠，作為 regression guard（subagent 必須跑 `go test ./internal/module/agent/...` 並確認綠）。

## 3. TDD 執行順序

嚴格紅綠，每個 commit 先紅後綠；不可批次寫完全部 test 再一次實作。預期 10 commits（包含 docs）。

### Commit 1 — `feat(agent): add HookEventSpec type + Events() to HookInstaller`

- 紅：在 `internal/agent/provider.go` 寫 `HookEventSpec` + 擴 `HookInstaller`（加 `Events() []HookEventSpec`）；三家 provider 尚未補 `Events()` → `go build ./...` **紅**
- 綠：同 commit 內給三家加最小 stub（`Events() []HookEventSpec { return nil }`）；build 恢復綠
- 注意：此 commit 故意不跑單元測試（stub 不測）；目的是讓 interface 擴充可以被其他 commit 依賴。Commit message 明確說明「stub，後續 commits 填實際內容」
- 同 commit 增加 doc comment：`Events()` 是 SSoT，比 `SupportedStatuses()` / installer var 更權威

### Commit 2 — `feat(agent): add DeriveSupportedStatuses helper + tests`

- 紅：新檔 `internal/agent/supported_statuses.go` 空 + 新檔 `internal/agent/supported_statuses_test.go` 寫 S1 / S2 / S3 → 紅
- 綠：實作 helper（union → sort）
- 驗證：`go test ./internal/agent/... -run DeriveSupportedStatuses` 綠

### Commit 3 — `feat(agent/cc): implement Events() + derive SupportedStatuses`

- 紅：新檔 `cc/events_test.go` 寫 E1-E6 → 紅（stub 回 nil）；`cc/provider_test.go` 改寫 S4 cc 部分 → 紅
- 綠：
  - 在 `cc/hooks.go`（或新檔 `cc/events.go`，視可讀性）實作 `Events()` 回傳 9 個 `HookEventSpec`
  - 修 `cc/provider.go` 的 `SupportedStatuses()` 改為 `DeriveSupportedStatuses(p.Events())`
  - **刪除 `ccHookEvents` package var**；`mergeClaudeHooks` / `CheckHooks` iteration 改走 `p.eventNames()` helper（private method）
- 驗證：`go test ./internal/agent/cc/...` 綠（含既有 installer / check 測試 regression）
- 規模：~60-90 行變更（events.go 新增 + 刪 var + 改 iteration）

### Commit 4 — `feat(agent/opencode): implement Events() + derive SupportedStatuses`

- 同 Commit 3 模式，改 opencode
- 紅：`opencode/events_test.go` E10 / E11 + provider_test S4 opencode 部分
- 綠：實作 `Events()` 8 項 + 刪 `opencodeHookEvents` var + `CheckHooks` 用 helper + `SupportedStatuses` 改 derive
- 注意：`plugin_template.go` 的字串模板若含硬編碼事件列表，**不改**；只動 `CheckHooks` iteration
- 驗證：`go test ./internal/agent/opencode/...` 綠

### Commit 5 — `feat(agent/codex): expand installer to 9 events via Events()`

- 這是 issue #613 的**正體修復** commit。分兩步：
- 紅 step A：`codex/events_test.go` 寫 E7 / E8 / E9 → 紅（stub 回 nil）
- 紅 step B：`codex/hooks_test.go` 寫 I3 / I4 → 紅（installer 仍只寫 3 事件）
- 綠：
  - 實作 `codex.Provider.Events()` 回傳 9 項
  - **刪除 `codexHookEvents` package var**；`mergeCodexHooks` / `CheckHooks` iteration 改走 helper
  - 修 `SupportedStatuses` 改 derive
- 驗證：`go test ./internal/agent/codex/...` 綠；手動檢查 diff 確認 6 個新事件（`SubagentStart/Stop` / `StopFailure` / `Notification` / `PermissionRequest` / `SessionEnd`）確實進入 installer 寫出的 JSON
- Commit message 主體明確提到 close issue #613

### Commit 6 — `test(agent): upgrade drift test to three-way equality`

- 紅：改 `drift_test.go` D1 → D1'（加 supportedSet 斷言）；新增 D3 → 若 Commit 3/4/5 的 `Events()` 與現有 providerFixtures 都對齊，應直接綠；若否，修 fixture 或 Events()
- 綠：三向等價 + fixture-covers-all-events 皆綠
- 驗證：故意臨時把 cc `Events()` 的 `Notification` EmitsStatus 砍一個（本機試，不 commit）→ D1' 應紅；還原後綠

### Commit 7 — `test(agent): add Coverage × Events cross-contract test`

- 紅：`coverage_test.go` 加 C2 → 應綠（前面 commits 已對齊）
- 此 commit 為 verification 性質（無邏輯變更），確保 Coverage 與 Events 契約跨檔穩定
- 若 C2 紅，回頭修（不該發生）

### Commit 8 — `refactor(agent): tighten Events doc comments + eventNames helper consistency`

- 純 doc / refactor commit（可選）：統一三家的 `eventNames()` helper 命名、補 doc comment
- 若 Commit 3/4/5 寫得夠乾淨，此 commit 可略
- 驗證：`go vet ./...` 無 warning；`go test ./...` 綠

### Commit 9 — `docs: add hook-events-declaration plan`

- 本 plan 檔進 repo
- 不改 code

### Commit 10 — `docs(spec): mark §2.4.3 event alignment resolved via Events() PR`

- 改 `docs/specs/2026-04-23-lights-rebuild-spec.md` §2.4.3 加一行註記：「已於 PR #<num> 實作完成」
- 或是在 PR merge 後另開 bump PR 帶這行 — 子任務範圍內視情況而定；本 plan 建議**放入本 PR**，保留設計脈絡
- 若主 session 要求 bump PR 帶，Commit 10 可略

## 4. 實作檔案預估

| 檔案 | 動作 | 預估行數 |
|---|---|---|
| `internal/agent/provider.go` | +`HookEventSpec` struct + 擴 `HookInstaller`；doc comments | +25 |
| `internal/agent/supported_statuses.go` | 新檔：`DeriveSupportedStatuses` helper | +20 |
| `internal/agent/supported_statuses_test.go` | 新檔：S1-S3 | +60 |
| `internal/agent/coverage_test.go` | +C2 | +20 |
| `internal/agent/drift_test.go` | D1→D1' 升級 + D3 新增 | +40 / -10 |
| `internal/agent/cc/events.go`（或嵌入 hooks.go） | 新增 `Events()` + `eventNames()` helper | +60 |
| `internal/agent/cc/hooks.go` | 刪 `ccHookEvents` var；iteration 改 helper | +5 / -5 |
| `internal/agent/cc/provider.go` | `SupportedStatuses` 改 derive | +2 / -7 |
| `internal/agent/cc/events_test.go` | 新檔：E1-E6 | +120 |
| `internal/agent/cc/provider_test.go` | +S4 cc | +15 |
| `internal/agent/cc/hooks_test.go` | +I1 / I2 | +60 |
| `internal/agent/codex/events.go`（或嵌入 hooks.go） | 新增 `Events()` + helper | +60 |
| `internal/agent/codex/hooks.go` | 刪 `codexHookEvents` var；iteration 改 helper | +5 / -6 |
| `internal/agent/codex/provider.go` | `SupportedStatuses` 改 derive | +2 / -7 |
| `internal/agent/codex/events_test.go` | 新檔：E7-E9 | +90 |
| `internal/agent/codex/provider_test.go` | +S4 codex | +15 |
| `internal/agent/codex/hooks_test.go` | +I3 / I4（issue #613 regression guard） | +80 |
| `internal/agent/opencode/events.go`（或嵌入 hooks.go） | 新增 `Events()` + helper | +60 |
| `internal/agent/opencode/hooks.go` | 刪 `opencodeHookEvents` var；iteration 改 helper | +5 / -5 |
| `internal/agent/opencode/provider.go` | `SupportedStatuses` 改 derive | +2 / -7 |
| `internal/agent/opencode/events_test.go` | 新檔：E10-E11 | +70 |
| `internal/agent/opencode/provider_test.go` | +S4 opencode | +15 |
| `internal/agent/opencode/hooks_test.go` | +I5 | +30 |
| `docs/specs/2026-04-23-hook-events-declaration-plan.md` | 本檔 | +320 |
| `docs/specs/2026-04-23-lights-rebuild-spec.md` | §2.4.3 註記（Commit 10） | +3 |
| **合計** | | **~1200 行**（含 plan / spec 更新 + 大量測試） |

## 5. 不做項目清單（防自動擴展）

以下衝動一律拒絕；subagent 如遇到念頭請在 PR description 明確聲明拒絕：

1. **不新增 `/api/agent/monitor/events` endpoint**（留 Phase 5）
2. **不動 SPA**（Inspector UI 是後續工作）
3. **不動 probe 層**（`internal/agent/probe/**`）
4. **不重構 `handler.go` 流程邏輯**（catalog-miss 早退是 Phase 1 既有能力）
5. **不移除 `StatusSupporter` interface**（保留作為通用宣告入口；只是實作改為 derive — 移除會影響 Phase 5 Inspector 與 Coverage row 的契約）
6. **不動 `coverage.go` 的 `Coverage` helper**（Events 擴充透過 `HookInstaller` 入口，Coverage 不新增欄位）
7. **不在 `HookEventSpec` 嵌入 probe intents**（那是 Phase 4b `ProbeIntentProvider`；混進 Hook 宣告會讓兩個分散概念被錯誤耦合）
8. **不 refactor `mergeClaudeHooks` / `mergeCodexHooks`** 成跨家共用 helper（Phase 1 spec §2.4.4 明示 Policy 分散）
9. **不動 `opencode/plugin_template.go` 的字串模板**（即使模板內硬編碼事件列表；模板是 runtime artifact，不是宣告 SSoT）
10. **不幫 codex 補 proxy 路徑的事件接收實作**（這是 runtime 工作，留未來 phase）
11. **不重命名既有 `HookInstaller` 為 `HookProvider`**（即使加了 `Events()` 讓它更像 provider）— rename 是純 churn
12. **不新增 `HookEventSpec.Optional`** 欄位或類似（YAGNI）
13. **不引入 `internal/agent/events_catalog.go` 或類似 central lookup**（§2.4.1 黃線：runtime 收斂 = 膨脹）
14. **不加 hook event 的 i18n 支援**（Description 固定英文）

## 6. Subagent 開發指引

- **cwd 強制前綴**：每個 Bash 指令以 `cd /Users/wake/Workspace/wake/purdex/.claude/worktrees/lights-hook-events-declaration && ` 為前綴（依 `feedback_subagent_cwd_enforcement.md`）。worktree 名稱由主 session 開 worktree 時確認；本 plan 假設為 `lights-hook-events-declaration`
- **分支**：進入後不另切；不 push（主 session 負責）
- **Commit message 格式**：Conventional Commits + 結尾加 `Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>`
- **TDD 嚴格紅綠**：每個 commit 內先寫測試跑紅再實作跑綠；不可批次寫完所有 test 再一次實作
- **刪除 `*HookEvents` var 時**：用 `grep -rn "ccHookEvents\|codexHookEvents\|opencodeHookEvents" internal/` 確認無其他引用（預期三家各 1 檔 3 處 usage：install / remove / check）
- **回報格式**：
  - Commit hash 列（`git log --oneline -12`）
  - `go build ./...` / `go vet ./...` 輸出摘要
  - `go test ./internal/agent/... ./internal/module/agent/...` 結果摘要（測試數量 + PASS/FAIL）
  - SPA 不需跑（本 PR 不動 SPA）
- **不要執行 `pnpm install` 或 `pnpm run build/lint`**（本 PR 不動 SPA）
- **Codex sandbox 限制**：本 PR 全 Go 改動，無 SPA 依賴，sandbox 網路限制不影響

## 7. 驗收清單

- [ ] 9-10 個 commits 符合 Conventional Commits + Co-Authored-By
- [ ] `go build ./...` 綠
- [ ] `go vet ./...` 無 warning
- [ ] `go test ./...` 綠（新增 E1-E11、S1-S4（三家各一）、I1-I5、D1'、D3、C2 共約 24 個測試點）
- [ ] 既有測試 0 regression（特別：`handler_test.go` / `coverage_test.go` C1 / `drift_test.go` D2）
- [ ] 三家 `HookInstaller` 都實作 `Events()` 且欄位符合 §1.4 表格
- [ ] 三家 `*HookEvents` package var 皆已刪除
- [ ] 三家 `SupportedStatuses()` 實作已改為 `DeriveSupportedStatuses(p.Events())`
- [ ] codex installer 實際寫出 9 個事件到 `~/.codex/hooks.json`（手動整合測：跑 `bin/pdx` install → 讀檔確認 key set 為 9 項）
- [ ] drift test 對三向等價（declared / emitted / supported）皆綠
- [ ] drift test 能抓到「砍掉 cc `Notification` 其中一個 EmitsStatus」的人為 regression（手動實驗記錄在 PR description）
- [ ] PR diff 只觸及 §4 表格列出的檔案；無計畫外檔案變動（特別是 `handler.go` / SPA / probe）
- [ ] PR description 列出「不做項目」§5 的明確聲明
- [ ] Close issue #613（PR description 用 `Closes #613` 關鍵字）
- [ ] Spec §2.4.3 已更新註記（Commit 10，或獨立 PR）

## 8. 風險與緩解

| 風險 | 機率 | 影響 | 緩解 |
|---|---|---|---|
| codex CLI 當前版本不會發出新增的 6 事件 | 高 | Installer 裝了沒人觸發 | 接受 — 為 proxy 路徑與未來 CLI 鋪路；drift test + per-fixture 測保證宣告與 DeriveStatus 一致；issue #613 的語意就是「為不一致鋪路」而非「等 CLI 才裝」 |
| `SupportedStatuses()` 從硬編碼改 derive 後回傳順序改變 | 低 | 若有 caller 依賴順序則破壞 | Commit 2 的 helper sort lex；調查現況 callers — 掃 `grep -rn "SupportedStatuses" internal/ spa/` 確認消費端只用 set 語意（無 order 依賴）；記錄調查結果於 PR description |
| `*HookEvents` package var 刪除後有外部依賴 | 低 | Build 失敗 | 範圍僅 purdex repo 內 package internal，`grep -rn` 可在 commit 前掃清；同 repo 內改動可原子化 |
| drift test 升級後過渡期：per-fixture + 三向等價同時跑 | 低 | 錯誤訊息膨脹、實作期紅得看不懂 | 錯誤訊息必須分層顯示（per-fixture 先；set diff 後）；fail 訊息附修復建議（「加 fixture / 改 Events / 改 SupportedStatuses」三路徑） |
| codex installer 擴展後，既有用戶 `~/.codex/hooks.json` 殘留舊 3-event 狀態 | 中 | 用戶需重跑 `pdx install codex` | 設計上 `mergeCodexHooks` 的 `filterOutPdxCodex` + append 對任何事件都能增量更新；用戶重跑 install 自然補齊 6 新事件；記錄於 PR description 的 Manual test plan |
| `Events()` 成為第四個 provider 新手陷阱（忘記實作） | 低 | 新 provider PR 爆 build error | `Events()` 是 required 方法，type checker 強制；build error 本身就是提醒；Commit 1 的 doc comment 也說明 |
| `Notification` 的 `auth_success` / `idle_prompt` sub-branch 被程式碼 reviewer 遺忘 | 中 | `EmitsStatus` 漏列某 Status | drift test 已有 per-fixture 對這 4 個 Notification sub-type 各一筆（Phase 1 codex review 堅持的保險）；Commit 6 升級時確保 fixture 不動 |
| PR 過大（~1200 行）影響 review | 中 | review 疲勞 / 漏 review | 10 commits 按領域切分（core helper → cc → opencode → codex → drift → docs）；每 commit 可獨立 review；PR description 提供「建議 review 順序」 |

## 9. 兩輪 Codex Review 預期 focus

### 第一輪（標準 code review 跨模型差異化檢查）

- 三家 `Events()` 表格的 `EmitsStatus` 是否漏 cc `Notification` 四個 sub-type 對應 Status（Waiting / Idle）；`EmitsStatus` 是否正確處理為 set-semantics
- `DeriveSupportedStatuses` 的 sort 策略是否 deterministic（lex sort Status 字串 vs 其他排序鍵）
- `Events()` 回傳 slice 的 defensive copy 是否一致（三家實作為同型）
- codex installer 擴展的向後相容：既有用戶升級後 `~/.codex/hooks.json` 變動行為
- `*HookEvents` var 刪除後，build 是否真的全綠（跨 package 引用掃乾淨）
- Description 語氣一致性（英文、無句號、無 emoji、長度合理）

### 第二輪 3 parallel

- **攻擊方**（找 bug / 安全 / race）：
  - `Events()` 空回傳情境（provider 忘了實作、stub 漏補）
  - `Events()` 返回順序 non-deterministic 情境（map iteration 洩漏）
  - `SupportedStatuses` 併發呼叫（derive helper thread-safety — 應是 pure function，但驗證）
  - codex 擴展事件後的舊用戶 hooks.json 解析 edge case（legacy format 共存）
  - `eventNames()` helper 洩漏內部 slice（defensive copy 漏）

- **防守方**（驗證設計合理性 / 架構一致性 / API 邊界）：
  - 與 §2.4 架構護欄的一致性（`Events()` 是否真的只在宣告層、沒變 runtime catalog lookup）
  - `HookEventSpec` 欄位集 vs 未來 probe intents 是否有耦合風險（應完全獨立）
  - `SupportedStatuses` 改 derive 是否破壞 Phase 1 Inspector-ready 假設
  - `HookInstaller.Events()` required vs `StatusSupporter` 仍 optional 的不對稱是否合理
  - drift test 三向等價的錯誤訊息設計（診斷性 vs 詳盡度）

- **檔案體質**（過大檔案 / SRP 違反 / 職責不清）：
  - `provider.go` 加 `HookEventSpec` 後是否過大（考慮拆到 `internal/agent/hook_events.go` 獨立檔）
  - 三家 `events.go` 是否該獨立檔案（vs 嵌入 `hooks.go`）— 本 plan 傾向獨立檔，理由：`Events()` 是宣告 SSoT，獨立檔利於 reviewer 一目了然
  - 三家 `events_test.go` 是否過大（E1-E6 / E7-E9 / E10-E11）
  - `eventNames()` helper 命名與位置（private method on Provider vs package-level function）
  - drift_test.go 擴充後是否超過 300 行、fixture 是否該拆 `drift_fixtures.go`
