# Catalog Naming Separation (W2) — Implementation Plan

- **Date**: 2026-04-28
- **Worktree**: `lights-w2-naming`（branch `worktree-lights-w2-naming`）
- **Spec**: `docs/specs/2026-04-28-catalog-naming-separation-spec.md`（985 行 — SOT for design）
- **Fix-spec**: `docs/specs/2026-04-28-lights-rebuild-fix-spec.md`
- **Audit**: `docs/specs/2026-04-28-hook-status-audit-spec.md`

---

## 0. 來龍去脈與 plan 使用方式

本 plan 把 W2 spec 拆成 TDD-first task list，每 task 是一個**獨立 commit**（test → impl → green → commit），失敗可隨時中斷由下一 task ID 接續。

每 task 條目：

- **id**: `P{phase}-T{n}` 連續編號
- **goal**: 一句目標
- **test first**：先寫的測試（檔案路徑 + test name + 預期 assertion）
- **impl**：實作改動點（file:line / 函式 / 改寫摘要）
- **verify**：跑哪個指令確認綠
- **commit msg pattern**：commit message 樣式

預設 commit 樣式：

```
<type>(<scope>): <T-id> <terse summary>

<body 1-3 lines if needed>

Refs spec §<section>.
```

不同 PR scope 在開始前先確認 base 來自 `origin/main`（per `feedback_bump_base_origin_not_local`）。

---

## 1. 共用前置（在 worktree 內，PR-W2-1 開工前）

| id | goal | 動作 |
|----|------|------|
| **PRE-1** | 確認 worktree base 對齊 origin/main | `git fetch origin && git status -s` clean check |
| **PRE-2** | go modules 已 download | `go mod download` |
| **PRE-3** | SPA deps 已裝 | `cd spa && pnpm install` |
| **PRE-4** | 確認既有測試 baseline 全綠 | `go test ./internal/agent/... ./internal/module/agent/... ./cmd/pdx/...` + `cd spa && npx vitest run && pnpm run lint && pnpm run build` |
| **PRE-5** | 截圖既有 catalog literal 形態（reference） | `git show origin/main:internal/agent/cc/events.go` 等留在 plan 內，方便對照 |

---

## 2. Phase 1（PR-W2-1）— 共用 schema + cc 端到端 + lifecycle 改造

**範圍 reference**：spec §3 Phase 1。實作順序由「不破壞既有測試 → 加新欄位 → 改 daemon 路徑 → 改 cc catalog → 改 cc installer → DRY 修補」依序推進。

### 2.1 Schema / 共用層（新欄位先落地，不影響 cc/codex/opencode 既有 catalog）

#### P1-T1：加 `Lifecycle` enum + `lifecycle.go`

- **goal**：新增 `LifecycleEventKind` 列舉與 `String()`
- **test first**：`internal/agent/lifecycle_test.go`（新檔）
  - `TestLifecycleEventKind_String_AllCases`：8 個 kind（None, SessionStart, UserPromptSubmit, Stop, StopFailure, SessionEnd, SubagentStart, SubagentStop）的 `String()` 不能空 / 不能重複
- **impl**：`internal/agent/lifecycle.go`（新檔）
  ```go
  type LifecycleEventKind int
  const (
      LifecycleNone LifecycleEventKind = iota
      LifecycleSessionStart
      LifecycleUserPromptSubmit
      LifecycleStop
      LifecycleStopFailure
      LifecycleSessionEnd
      LifecycleSubagentStart
      LifecycleSubagentStop
  )
  func (k LifecycleEventKind) String() string { ... }
  ```
- **verify**：`go test ./internal/agent/`
- **commit msg**：`feat(agent): P1-T1 add LifecycleEventKind enum`

#### P1-T2：HookEventSpec 加三欄位

- **goal**：`HookEventSpec` struct 加 `PurdexName` / `UpstreamKeys` / `Lifecycle`，`Name` 標 deprecated
- **test first**：`internal/agent/event_spec_test.go`（新檔）
  - `TestHookEventSpec_NewFieldsZeroValueBackwardCompat`：未填新欄位的 literal（pre-W2 形態）`PurdexName == ""` / `UpstreamKeys == nil` / `Lifecycle == LifecycleNone`，原 `Name` 不變動
- **impl**：`internal/agent/provider.go`：HookEventSpec 加三欄位（per spec §2.1），`Name` 加 `// Deprecated:` comment
- **verify**：`go test ./internal/agent/...`（既有 cc/codex/opencode catalog literal 走 zero-value 全綠）
- **commit msg**：`feat(agent): P1-T2 extend HookEventSpec with PurdexName/UpstreamKeys/Lifecycle`

#### P1-T3：Lookup helpers（PurdexName + UpstreamKey）

- **goal**：新增 `LookupByPurdexName` / `LookupByUpstreamKey` helper
- **test first**：`internal/agent/event_spec_test.go`
  - `TestLookupByPurdexName_Found / NotFound / EmptyName`
  - `TestLookupByUpstreamKey_FoundSingle / FoundMulti / NotFound`
  - 用 fixture catalog（不依賴真實三家）— 防止 cc/codex/opencode 改動影響本 helper test
- **impl**：`internal/agent/provider.go` 加兩函式（per spec §2.1）；godoc 必須含 §2.5 限制警告（opencode filter events）
- **verify**：`go test ./internal/agent/`
- **commit msg**：`feat(agent): P1-T3 add LookupByPurdexName/UpstreamKey helpers`

### 2.2 Handler / CLI 改造

#### P1-T4：EventRequest PurdexName + JSON unmarshal alias

- **goal**：`EventRequest.EventName` → `PurdexName`，primary tag `purdex_name`，加 unmarshal alias `event_name`
- **test first**：`internal/module/agent/handler_test.go`
  - `TestEventRequest_Unmarshal_PurdexNameTag`：JSON `{"purdex_name":"X"}` → `req.PurdexName=="X"`
  - `TestEventRequest_Unmarshal_EventNameAlias`：JSON `{"event_name":"X"}` → `req.PurdexName=="X"`
  - `TestEventRequest_Unmarshal_PurdexNamePriority`：兩鍵都送，`purdex_name` 優先
  - `TestEventRequest_Marshal_OnlyPurdexName`：marshal 後 JSON 只含 `purdex_name`，無 `event_name`
- **impl**：`internal/module/agent/handler.go`（per spec §4.1）
  - struct 改名 + custom UnmarshalJSON
  - 同檔 `provider.DeriveStatus(req.EventName, ...)` 改 `req.PurdexName`
  - 任何同檔 `req.EventName` references 全改名（含 trace 寫入點）
- **verify**：`go test ./internal/module/agent/`
- **commit msg**：`refactor(agent): P1-T4 EventRequest PurdexName + JSON alias`

#### P1-T5：CLI hookPayload 改 PurdexName

- **goal**：`cmd/pdx/hook.go:hookPayload` struct 對齊 daemon — `EventName` → `PurdexName`，JSON tag `purdex_name`
- **test first**：`cmd/pdx/hook_test.go`
  - `TestBuildHookPayload_MarshalsPurdexName`：`buildHookPayload(...,name="PdxX",...)` JSON marshal 後字段名為 `purdex_name`
  - `TestRunHook_PositionalArgPassedAsPurdexName`：`runHook([]string{"--agent","cc","PdxSessionStart"})` 帶到 payload 的 PurdexName 字串值
- **impl**：`cmd/pdx/hook.go:17-26 hookPayload` 改名 + JSON tag；`runHook` 變數名 `eventName` → `purdexName`；`buildHookPayload` 第二參數命名同步
- **verify**：`go test ./cmd/pdx/`
- **commit msg**：`refactor(cli): P1-T5 hookPayload PurdexName JSON tag`

### 2.3 Daemon lifecycle 三分支 decision tree

#### P1-T6：定義 `isLegacyHookForUnmigrated` predicate

- **goal**：新增 phase-aware per-agent predicate（per spec §3.4.2）
- **test first**：`internal/module/agent/legacy_hook_test.go`（新檔）
  - `TestIsLegacyHookForUnmigrated_CodexAllNames`：codex + 9 entries 全 true
  - `TestIsLegacyHookForUnmigrated_OpencodeWithoutNotification`：opencode + Notification → false（per spec §3.4.2 negative case）
  - `TestIsLegacyHookForUnmigrated_OpencodeAllOtherNames`：opencode + 8 entries（含 PermissionRequest）全 true
  - `TestIsLegacyHookForUnmigrated_CCAlwaysFalse`：cc 任何 name → false（已遷不在 fallback）
- **impl**：`internal/module/agent/legacy_hook.go`（新檔）
  ```go
  var codexLegacyEventNames = map[string]bool{...} // 9 entries 含 Notification
  var opencodeLegacyEventNames = map[string]bool{...} // 8 entries 不含 Notification

  func isLegacyHookForUnmigrated(agentType, name string) bool { ... }
  ```
- **verify**：`go test ./internal/module/agent/`
- **commit msg**：`feat(agent): P1-T6 add isLegacyHookForUnmigrated predicate`

#### P1-T7：handler lifecycle 三分支改造

- **goal**：`handler.go` 內所有硬編 event-name 字面值的 lifecycle 分支改 metadata-driven + fallback
- **test first**：`handler_test.go`（per spec §6.3.1 Phase 1 矩陣）
  - 6 個 cc metadata-path test（SessionStart/SessionEnd/SubagentStart/Stop/Notification no-op/PermissionRequest no-op）
  - 2 個 codex/opencode 中間態 fallback test（SessionEnd/SessionStart fallback）
  - 1 個 codex 提早送 PdxXxx → invalid（catalog miss + predicate fail）
  - 1 個 opencode + Notification → invalid（per §6.3.1 negative）
  - 1 個 opencode + PermissionRequest → fallback ok（per §6.3.1 positive）
- **impl**：`internal/module/agent/handler.go:181/186/187-189/278-285/301-305`（per spec §2.3.1 + §3.4.2）
  - 在 `provider.DeriveStatus` 後拿 `spec, ok := provider.LookupByPurdexName(req.PurdexName)`
  - 三分支 switch（per spec §3.4.2 pseudo-code）
  - opencode-specific Stop guard `req.AgentType != "opencode"` 保留
- **verify**：`go test ./internal/module/agent/`
- **commit msg**：`refactor(agent): P1-T7 handler lifecycle metadata-driven dispatch`

#### P1-T8：frame_ops lifecycle 改造

- **goal**：frame mutation 路徑同步改 metadata-driven + fallback
- **test first**：`internal/module/agent/frame_ops_test.go`
  - `TestFrameOps_SubagentStart_MetadataPath`：cc PdxSubagentStart → frame.Subagents +1
  - `TestFrameOps_SubagentStart_LegacyFallback`：codex/opencode SubagentStart → frame.Subagents +1（fallback path）
  - `TestFrameOps_SessionEnd_MetadataPath`：cc PdxSessionEnd → frame deleted
  - `TestFrameOps_SessionEnd_LegacyFallback`：codex/opencode SessionEnd → frame deleted（fallback path）
- **impl**：`internal/module/agent/frame_ops.go:133-174` 等 frame mutation 路徑（per spec §2.3.1）
- **verify**：`go test ./internal/module/agent/`
- **commit msg**：`refactor(agent): P1-T8 frame_ops lifecycle metadata-driven`

### 2.4 cc catalog 端到端

#### P1-T9：cc catalog 改 plain struct literal + 三新欄位

- **goal**：`ccEventSpecs` 9 entries 全填 PurdexName / UpstreamKeys / Lifecycle，保留 Name backfill
- **test first**：`internal/agent/cc/events_test.go`（per spec §6.1 invariants 1-7 對 cc）
  - `TestCcEventSpecs_PurdexNamePdxPrefix`
  - `TestCcEventSpecs_NameMatchesTrimPrefix`：`Name == TrimPrefix(PurdexName, "Pdx")`
  - `TestCcEventSpecs_UpstreamKeysNotEmpty`（installable entry）
  - `TestCcEventSpecs_PurdexNameNotInUpstreamKeys`
  - `TestCcEventSpecs_LifecycleAlignment`：per §2.3.1 對照表
  - `TestCcEventSpecs_PreservedLegacyMetadata`：每 entry 的 EmitsStatus / Description / FutureOnly / Handling 與 pre-W2 git show 對照相同
- **impl**：`internal/agent/cc/events.go` ccEventSpecs 改寫（per spec §2.2.1 plain struct literal style）
- **verify**：`go test ./internal/agent/cc/...`
- **commit msg**：`feat(agent/cc): P1-T9 catalog plain struct literal with PurdexName/UpstreamKeys/Lifecycle`

#### P1-T10：cc DeriveStatus switch case rename

- **goal**：`deriveCCStatus` switch case label 改 `PdxXxx`
- **test first**：`internal/agent/cc/status_test.go`
  - `TestDeriveCCStatus_PdxUserPromptSubmit_Running`
  - `TestDeriveCCStatus_PdxNotification_PermissionPrompt_Waiting`
  - `TestDeriveCCStatus_PdxSessionStart_Compact_InvalidWithReason`
  - `TestDeriveCCStatus_LegacySessionStart_Invalid`：舊字面值不再認（catalog miss truly-unknown）
- **impl**：`internal/agent/cc/status.go:13-90` switch case label 全改 `PdxXxx`
- **verify**：`go test ./internal/agent/cc/`
- **commit msg**：`refactor(agent/cc): P1-T10 DeriveStatus switch case PdxName`

#### P1-T11：cc installer 改 UpstreamKey/PurdexName 邊界

- **goal**：settings.json hooks key=UpstreamKey、command 末段=PurdexName
- **test first**：`internal/agent/cc/hooks_test.go`
  - `TestMergeClaudeHooks_KeyIsUpstreamKey`：parse merged settings.json，hooks map keys ∈ pre-W2 字面值集合
  - `TestMakePdxEntry_CommandTokenIsPurdexName`：command 字串 split 後末段 token == PurdexName
  - `TestMergeClaudeHooks_CommandHasPdxPrefix`：command 字串包含 `PdxXxx`
- **impl**：`internal/agent/cc/hooks.go:116-220` 多函式 — `mergeClaudeHooks` / `makePdxEntry`（per spec §2.4 + §3 Phase 1）
- **verify**：`go test ./internal/agent/cc/`
- **commit msg**：`refactor(agent/cc): P1-T11 installer UpstreamKey/PurdexName boundary`

#### P1-T12：cc DRY 修補（known + cleanup sets）

- **goal**：`ccKnownEventNames` / `ccOwnedCleanupEventNames` 從 catalog 自動衍生
- **test first**：`internal/agent/cc/hooks_test.go`
  - `TestCcKnownEventNames_DerivedFromUpstreamKeys`：== `Filter(ccEventSpecs, IsInstallable).UpstreamKeys` union
  - `TestCcOwnedCleanupEventNames_ThreeSetUnion`：== UpstreamKeys ∪ PurdexName ∪ legacy Name（驗證三聯集 — per spec §6.1 invariant 6）
  - `TestCcOwnedCleanupEventNames_CleansLegacyAndNew`：手構 settings.json fixture（混合 pre-W2 `Stop` + W2 `PdxStop` 命令），跑兩次 reinstall round-trip 後兩者都被清掉
- **impl**：`internal/agent/cc/hooks.go:381-401`（per spec §3 Phase 1）
- **verify**：`go test ./internal/agent/cc/`
- **commit msg**：`refactor(agent/cc): P1-T12 DRY known/cleanup sets from catalog`

### 2.5 共用 catalog invariants（per-agent / per-phase）

#### P1-T13：per-agent invariants 測試（forward + reverse）

- **goal**：`internal/agent/event_spec_test.go` 加 cross-agent invariant 測試
- **test first**（test 本身就是目標）
  - `TestCatalogInvariants_CC_AllForwardInvariants`（cc 已遷 → invariants 1-7）
  - `TestCatalogInvariants_Codex_LegacyShape`（codex 未遷 → 反向斷言：每 entry 三新欄位 zero-value）
  - `TestCatalogInvariants_Opencode_LegacyShape`（opencode 同上）
- **impl**：純 test 檔 — invariant assertion runner，遍歷三家 catalog
- **verify**：`go test ./internal/agent/`
- **commit msg**：`test(agent): P1-T13 per-agent catalog invariants Phase 1 baseline`

### 2.6 SPA fixture 對齊（cc 部分）

#### P1-T14：SPA test fixture 更新（cc）

- **goal**：SPA snapshot / fixture 內硬寫 `"SessionStart"` 等 cc 字面值改 `"PdxSessionStart"`
- **test first / verify**：直接跑 `cd spa && pnpm run lint && npx vitest run`，看哪些 fixture 報錯，逐筆修正
- **impl**：grep `RawEventName|raw_event_name|event_name` 在 spa/src spa/test 的硬編 cc event name；逐筆改
- **verify**：vitest + lint + build 全綠
- **commit msg**：`test(spa): P1-T14 update cc event name fixtures to Pdx prefix`

### 2.7 端到端手動驗證

#### P1-T15：mlab 機 cc reinstall 驗證

- **goal**：實機驗證 cc Phase 1 daemon binary 正常運作
- **動作**：
  1. `go build -o /tmp/pdx-w2 ./cmd/pdx`
  2. `/tmp/pdx-w2 install --reinstall`（暫只對 cc）
  3. inspect `~/.claude/settings.json`：`hooks.SessionStart[0].hooks[0].command` 末段 == `PdxSessionStart`
  4. （在 cc 中）跑 `claude` session、送一個 prompt、結束 session
  5. 觀察 daemon log（`PDX_DEV_MODE=1`）顯示 `event_name=PdxUserPromptSubmit` 等
  6. SPA lights 行為與 pre-W2 一致
- **無 commit**（純驗證）；如失敗則中斷並查 root cause

### 2.8 Phase 1 PR

#### P1-T16：起 PR-W2-1

- **動作**：
  - `git push origin worktree-lights-w2-naming`（本 plan / spec / fix-spec 已 push）
  - `gh pr create --title "feat(agent): W2 Phase 1 — schema + cc catalog naming separation + lifecycle metadata"` body 含：
    - Summary: 三欄位 schema + cc 端到端 + lifecycle decision tree
    - Spec ref: `docs/specs/2026-04-28-catalog-naming-separation-spec.md`
    - Test plan checklist（PRE-4 baseline + 各 P1-T* 測試）
- **codex review**：兩輪（標準 + 防守 spec alignment 防線）

---

## 3. Phase 2（PR-W2-2）— codex 端到端

PR-W2-1 merged 後起新 worktree（或 reset 同 worktree）；前置確認 base 對齊 origin/main。

### 3.1 codex catalog

#### P2-T1：codex catalog 改 plain struct literal + 三新欄位

- **test first**：`internal/agent/codex/events_test.go`（per spec §6.1，11 entries 含 9 installable + 2 unsupported `PdxPreToolUse` / `PdxPostToolUse`）
  - 同 P1-T9 一系列 invariant 測試
- **impl**：`internal/agent/codex/events.go` codexEventSpecs 改寫（per spec §3 Phase 2）
- **verify**：`go test ./internal/agent/codex/`
- **commit msg**：`feat(agent/codex): P2-T1 catalog plain struct literal`

#### P2-T2：codex DeriveStatus switch case rename

- **test first**：`codex/status_test.go`（per cc/P1-T10 同型 — 7 個 case test）
- **impl**：`internal/agent/codex/status.go:13-73` switch case rename
- **verify**：`go test ./internal/agent/codex/`
- **commit msg**：`refactor(agent/codex): P2-T2 DeriveStatus switch case PdxName`

### 3.2 codex installer

#### P2-T3：codex installer 改 UpstreamKey/PurdexName 邊界

- **test first**：`codex/hooks_test.go`
  - `TestMergeCodexHooksFile_MatcherKeyIsUpstreamKey`
  - `TestMergeCodexHooksFile_CommandTokenIsPurdexName`
  - `TestCheckCodexEvent_LooksUpByUpstreamKey`：`spec.UpstreamKeys[0]` 反查 hooks.json
  - `TestCodexConfigToml_FeatureFlagUnchanged`：config.toml 的 `features.codex_hooks=true` 不動
- **impl**：`internal/agent/codex/hooks.go:108-128, 209-298, 172-204`（per spec §3 Phase 2）
- **verify**：`go test ./internal/agent/codex/`
- **commit msg**：`refactor(agent/codex): P2-T3 installer UpstreamKey/PurdexName boundary`

#### P2-T4：codex DRY 修補

- **test first**：類同 P1-T12，但對 `codexOwnedCleanupEventNames` + `codexKnownEventNames`
  - `TestCodexOwnedCleanupEventNames_ThreeSetUnion`
  - `TestCodexOwnedCleanupEventNames_CleansLegacyAndNew`：mixed fixture round-trip
- **impl**：`internal/agent/codex/hooks.go:689-701` 改自動衍生
- **verify**：`go test ./internal/agent/codex/`
- **commit msg**：`refactor(agent/codex): P2-T4 DRY known/cleanup sets`

### 3.3 isLegacyHookForUnmigrated 升級

#### P2-T5：移除 codex case from predicate

- **goal**：codex 已遷 → 從 `isLegacyHookForUnmigrated` 移除 codex case
- **test first**：`legacy_hook_test.go` 加
  - `TestIsLegacyHookForUnmigrated_CodexAllFalse`：codex + 任何 name → false（已遷）
  - opencode case 維持原樣
- **impl**：`internal/module/agent/legacy_hook.go` 移除 codex `case "codex":` 整段
- **verify**：`go test ./internal/module/agent/`
- **commit msg**：`refactor(agent): P2-T5 remove codex from legacy fallback predicate`

### 3.4 共用 invariants 升級

#### P2-T6：codex catalog invariants 升級為正向斷言

- **goal**：`event_spec_test.go` 把 codex 從反向斷言（zero-value）改為正向 invariants 1-7
- **test first / impl**：修改 `TestCatalogInvariants_Codex_*` 測試
- **verify**：`go test ./internal/agent/`
- **commit msg**：`test(agent): P2-T6 upgrade codex catalog invariants to forward`

### 3.5 SPA fixture（codex）

#### P2-T7：SPA test fixture 更新（codex）

- 同 P1-T14 對 codex event name 字面值

### 3.6 Phase 2 手動驗證 + PR

#### P2-T8：mlab 機 codex reinstall 驗證

- 同 P1-T15，但 codex 部分

#### P2-T9：起 PR-W2-2

- **codex review**：兩輪（標準 + 攻擊視角）

---

## 4. Phase 3（PR-W2-3）— opencode plugin template + transition cleanup

PR-W2-2 merged 後起。

### 4.1 opencode catalog

#### P3-T1：opencode catalog 改 plain struct literal + 三新欄位

- **test first**：`opencode/events_test.go`（8 installable + 20 Unsupported + 37 Ignored = 65 total，但 invariants 只對 8 installable 套）
  - 同 P1-T9 + 重點 `PdxPermissionRequest` UpstreamKeys 多元素 `["permission.asked","question.asked"]`
- **impl**：`internal/agent/opencode/events.go`
- **verify**：`go test ./internal/agent/opencode/`
- **commit msg**：`feat(agent/opencode): P3-T1 catalog plain struct literal`

#### P3-T2：opencode DeriveStatus switch case rename

- 同 P1-T10 / P2-T2

### 4.2 opencode plugin template

#### P3-T3：plugin_template emit 改 Go 端常數注入

- **test first**：`opencode/plugin_template_test.go`（新檔或既有）
  - `TestRenderManagedPlugin_HasPdxEventConst`：模板開頭 `const PURDEX_EVENT = { PdxSessionStart: "PdxSessionStart", ... }` 8 條全在
  - `TestRenderManagedPlugin_EmitArgsArePdxName`：8 處 emit 呼叫的第一引數 == `PURDEX_EVENT.PdxXxx`
  - `TestRenderManagedPlugin_NoLegacyEmitLiteral`：grep 後 `emit('SessionStart'` 等 legacy 字面值零命中
  - `TestRenderManagedPlugin_MagicMarkerUnchanged`：仍 `pdx-managed:opencode-hooks:v1`
- **impl**：`internal/agent/opencode/plugin_template.go` template 開頭加 `const PURDEX_EVENT`（從 catalog 編譯期生成）；8 處 emit 呼叫改 `PURDEX_EVENT.PdxXxx`
- **verify**：`go test ./internal/agent/opencode/`
- **commit msg**：`refactor(agent/opencode): P3-T3 plugin emit via PURDEX_EVENT const`

### 4.3 Transition cleanup（同 PR）

#### P3-T4：移除 `Name` 欄位 + helper 雙寫

- **goal**：`HookEventSpec.Name` 欄位刪除；三家 catalog literal 移除 `Name:` 行
- **test first**：先確認三家 events_test.go 已不依賴 `spec.Name`（前面 phase 應該已遷，但確認）
- **impl**：
  - `internal/agent/provider.go` 移除 `Name` 欄位
  - 三家 events.go 每 entry 移除 `Name: "..."` 行
  - `internal/agent/event_spec_test.go` 移除「Name dev-time 對應」invariant（per spec §6.1 invariant 2）
  - 任何 `spec.Name` 殘留 reference Go 編譯器抓出修
- **verify**：`go build ./...` + `go test ./...`
- **commit msg**：`refactor(agent): P3-T4 remove deprecated Name field from HookEventSpec`

#### P3-T5：移除 EventRequest `event_name` JSON alias

- **goal**：custom UnmarshalJSON 移除（簡化為單鍵 `purdex_name`）
- **test first**：handler_test.go
  - 移除 `TestEventRequest_Unmarshal_EventNameAlias`（alias 已不存在）
  - 加 `TestEventRequest_Unmarshal_LegacyEventNameRejected`：JSON `{"event_name":"X"}` → `req.PurdexName == ""`
- **impl**：`internal/module/agent/handler.go` 移除 `UnmarshalJSON` method（標準 unmarshal 即可）
- **verify**：`go test ./internal/module/agent/`
- **commit msg**：`refactor(agent): P3-T5 remove event_name JSON alias`

#### P3-T6：移除 lifecycle fallback path + isLegacyHookForUnmigrated

- **goal**：daemon decision tree 化簡為兩分支（ok metadata path / !ok invalid）
- **test first**：handler_test.go / frame_ops_test.go 改
  - 移除「opencode 中間態 SessionStart fallback」測試（已不存在）
  - 加 `TestHandler_OpencodeLegacy_NowInvalid`：opencode + `SessionStart` → invalid（fallback 已移除）
- **impl**：
  - 移除 `internal/module/agent/legacy_hook.go`
  - `handler.go` decision tree 簡化為兩分支
  - `frame_ops.go` 同步簡化
- **verify**：`go test ./internal/module/agent/`
- **commit msg**：`refactor(agent): P3-T6 remove lifecycle fallback path`

### 4.4 共用 invariants 升級

#### P3-T7：opencode catalog invariants 升級

- **goal**：`event_spec_test.go` 三家全套用正向 invariants（無反向斷言）
- **impl**：移除 `TestCatalogInvariants_Opencode_LegacyShape`，加 `TestCatalogInvariants_Opencode_AllForward`
- **verify**：`go test ./internal/agent/`
- **commit msg**：`test(agent): P3-T7 finalize all-agent forward catalog invariants`

### 4.5 cc/codex cleanup set 簡化

#### P3-T8：移除 pre-W2 legacy `Name` set from cleanup helpers

- **goal**：Phase 3 ship 後 user 已 reinstall，舊命令不應再存在 → cleanup set 簡化為兩聯集
- **test first**：cc/codex hooks_test.go
  - `TestCcOwnedCleanupEventNames_TwoSetUnion`：== UpstreamKeys ∪ PurdexName（無 legacy `Name`）
- **impl**：`cc/hooks.go` + `codex/hooks.go` cleanup helper 簡化
- **verify**：`go test ./internal/agent/...`
- **commit msg**：`refactor(agent): P3-T8 simplify cleanup sets to two-set union`

### 4.6 SPA fixture（opencode）

#### P3-T9：SPA test fixture 更新（opencode）

- 同 P1-T14 / P2-T7 對 opencode

### 4.7 手動驗證 + PR

#### P3-T10：mlab 機 opencode reinstall 驗證

- 同 P1-T15，opencode 部分；inspect `~/.config/opencode/plugins/pdx-agent-hooks.js` 含新的 `PURDEX_EVENT` 常數區塊與 8 個 `emit(PURDEX_EVENT.PdxXxx, ...)`

#### P3-T11：起 PR-W2-3

- **codex review**：兩輪（標準 + 體質視角 — plugin template 可讀性 + cleanup 完整性）

---

## 5. Bump + post-ship verification

### 5.1 PR-W2-bump

#### BUMP-T1：起 bump worktree

- **動作**：
  - `EnterWorktree name=chore-bump-alpha-244` 進新 worktree（per `feedback_bump_base_origin_not_local` — base 來自 origin/main）
  - 進 worktree 後 `git reset --hard origin/main` 確保乾淨
- **無 commit**

#### BUMP-T2：bump version files

- **impl**：
  - `VERSION`：`1.0.0-alpha.243` → `1.0.0-alpha.244`
  - `package.json`：version 同步
  - `spa/package.json`：version 同步
  - `CHANGELOG.md`：加 W2 條目（catalog naming separation 三 phase ship + 破壞性升級需 reinstall）
- **commit msg**：`chore: bump version to 1.0.0-alpha.244`

#### BUMP-T3：起 PR-W2-bump

- 標準 bump PR 流程，無 codex review

### 5.2 主機 reinstall + 對齊驗證

#### POST-T1：mlab 主機 reinstall

- **動作**：bump merged + main 拉新 binary 後，跑 `pdx install --reinstall`

#### POST-T2：逐檔驗證命名

- `~/.claude/settings.json`：`hooks.SessionStart[0].hooks[0].command` 末段 token == `PdxSessionStart`
- `~/.codex/hooks.json`：`hooks.SessionStart[0].command` 同上
- `~/.config/opencode/plugins/pdx-agent-hooks.js`：8 個 `emit(PURDEX_EVENT.PdxXxx, ...)`（或字串字面值形態）；無殘留 legacy `emit('SessionStart'` 等
- `~/.codex/config.toml`：`features.codex_hooks=true` 不動

#### POST-T3：實機 lifecycle smoke test

- 三家 agent 各跑一次 session（new prompt → idle → SessionEnd）
- daemon log（`PDX_DEV_MODE=1`）顯示 `event_name=PdxXxx` 全大寫前綴
- SPA lights 行為與 pre-W2 一致（無誤判 / 無漏發）

#### POST-T4：失敗時 rollback

- 任何失敗 → 立即開 issue + 評估 roll back bump PR
- Worktree 清理：`ExitWorktree action=remove` 或留待 user 手動清

---

## 6. 風險與中斷恢復

### 6.1 主要風險（per spec §7.2）+ plan 對應緩解

| Risk | Plan 緩解 |
|------|-----------|
| Phase 1/2 main 中間態誤 bump | BUMP-T1 強制延後到 PR-W2-3 merged 之後 |
| Catalog literal 改寫誤丟 metadata | P1-T9 / P2-T1 / P3-T1 含 `TestXxxEventSpecs_PreservedLegacyMetadata` 對照 git show baseline |
| Phase 1/2 期間 Name / PurdexName 漂移 | P1-T13 invariants 包含 `Name == TrimPrefix(PurdexName, "Pdx")`；Phase 3 P3-T4 強制移除 Name 欄位讓編譯器抓 stale |
| daemon lifecycle fallback 誤跨 agent 命中 | P1-T6 isLegacyHookForUnmigrated per-agent literal set + negative test（opencode + Notification → invalid）|
| LookupByUpstreamKey 誤用於 opencode filter routing | spec §2.1 helper godoc 警告（P1-T3 必含）+ §2.5；無新 routing helper |
| cleanup set 漏抓 | P1-T12 / P2-T4 三聯集 + round-trip test（先 pre-W2 fixture / 再 W2 reinstall fixture）|
| opencode plugin emit 改常數注入後 JS template 解析錯誤 | P3-T3 unit test 含 magic marker / emit RHS / 無 legacy 字面值三斷言 |
| reinstall 對 user 環境破壞性 | POST-T2 / POST-T3 ad-hoc 驗證 + POST-T4 rollback 路徑 |
| 並發 session | 每 PR 起 worktree 前 `git fetch && git status -s` clean check |

### 6.2 中斷恢復點

每個 P*-T* 是獨立 commit。中斷時：

- `git log --oneline` 找最後 task ID
- 從下一 task ID 接續
- 三 phase 之間需等 PR merged → 跨 session 接續用 kickoff memory 觸發詞回到 plan §3 / §4

### 6.3 跑偏防線（per `feedback_phase_skip_threshold`）

W2 範圍嚴格止於 spec §1.1 表列工作 + 本 plan task。**禁止越界做**：

- ❌ ProbeIntent / ProbeIntentProvider（W6 範圍）
- ❌ manageActivityWatch always-on policy 撤回（W3 範圍）
- ❌ TraceStore step 補完 / dev log 補完（W4 範圍）
- ❌ DeriveStatus 邏輯改動（只 rename case label）
- ❌ 跨 agent 抽 generic event interface

任一冒出 → 停手 surface（per fix-spec §7）。

---

## 7. Test 跑指令快查

```bash
# Go 全測
go test ./internal/agent/... ./internal/module/agent/... ./cmd/pdx/...

# 個別
go test ./internal/agent/                          # 共用層 (event_spec / lifecycle / lookup helpers)
go test ./internal/agent/cc/...                    # cc 端
go test ./internal/agent/codex/...                 # codex 端
go test ./internal/agent/opencode/...              # opencode 端
go test ./internal/module/agent/                   # daemon handler / frame_ops / legacy_hook
go test ./cmd/pdx/                                 # CLI hook payload

# Build
go build ./...

# SPA
cd spa && pnpm install
cd spa && npx vitest run
cd spa && pnpm run lint
cd spa && pnpm run build

# Manual reinstall + verify (mlab 主機)
go build -o /tmp/pdx-w2 ./cmd/pdx
/tmp/pdx-w2 install --check
/tmp/pdx-w2 install --reinstall
cat ~/.claude/settings.json | jq '.hooks'
cat ~/.codex/hooks.json | jq '.hooks'
grep "emit" ~/.config/opencode/plugins/pdx-agent-hooks.js
```

---

## 8. 文獻

- Spec：`docs/specs/2026-04-28-catalog-naming-separation-spec.md`（SOT for design）
- Fix-spec：`docs/specs/2026-04-28-lights-rebuild-fix-spec.md`
- W1 audit：`docs/specs/2026-04-28-hook-status-audit-spec.md`
- 上層 spec：`docs/specs/2026-04-23-lights-rebuild-spec.md`
- Memory：
  - `feedback_no_alpha_migration.md`
  - `feedback_phase_skip_threshold.md`
  - `feedback_codex_pr_review_spec_alignment.md`
  - `feedback_concurrent_session_safety.md`
  - `feedback_bump_base_origin_not_local.md`
  - `feedback_codex_review_termination.md`
  - `feedback_codex_dispatch_jobid.md`
  - `feedback_meta_drift_progressive_precision.md`
