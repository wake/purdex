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

**範圍 reference**：spec §3 Phase 1。實作順序：「Schema 共用層 → CLI/Handler struct rename → predicate 定義 → cc catalog/DeriveStatus/installer/DRY → daemon lifecycle metadata-driven 改造（依賴 cc catalog 已填 Lifecycle）→ SPA fixture → 手動驗證 → PR」。

> **G2 / G3 修訂**：lifecycle 改造（handler / frame_ops 走 metadata-driven）排在 cc catalog migration **之後**（cc catalog 已填 Lifecycle 才能跑 metadata path 測試）；catalog migration 與對應 cross-agent invariant runner 改為**綁同一 commit**（避免中間態 `go test ./internal/agent/` fail）。

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

- **goal**：新增 `LookupByPurdexName` / `LookupByUpstreamKey` helper（free function，per spec §2.1 / §2.5）
- **test first**：`internal/agent/event_spec_test.go`
  - `TestLookupByPurdexName_Found / NotFound / EmptyName`
  - `TestLookupByUpstreamKey_FoundSingle / FoundMulti / NotFound`
  - 用 fixture catalog（不依賴真實三家）— 防止 cc/codex/opencode 改動影響本 helper test
- **impl**：`internal/agent/provider.go` 加兩函式（per spec §2.1）；簽章：`func LookupByPurdexName(specs []HookEventSpec, purdexName string) (HookEventSpec, bool)`；godoc 必須含 §2.5 限制警告（opencode filter events）
- **verify**：`go test ./internal/agent/`
- **commit msg**：`feat(agent): P1-T3 add LookupByPurdexName/UpstreamKey helpers`

### 2.2 Handler / CLI struct rename（不含 lifecycle dispatch — 等 P1-T11/T12）

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
  - **lifecycle 比對暫不改**：仍維持原硬編字面值比對（"SessionStart" 等），只把 `req.EventName` 變數名換掉。lifecycle metadata-driven 改造在 P1-T11 / P1-T12 完成（依賴 cc catalog 已填 Lifecycle）
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

### 2.3 Lifecycle 共用層 — predicate 定義（dispatch 改造延後到 P1-T11/T12）

#### P1-T6：定義 `isLegacyHookForUnmigrated` predicate

- **goal**：新增 phase-aware per-agent predicate（per spec §3.4.2），不接到 handler / frame_ops（dispatch 在 P1-T11/T12 才接）
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

### 2.4 cc catalog 端到端

#### P1-T7：cc catalog 改 plain struct literal + 三新欄位 + cross-agent invariant runner

- **goal**：`ccEventSpecs` 9 entries 全填 PurdexName / UpstreamKeys / Lifecycle，保留 Name backfill；同 commit 補 cross-agent invariant runner（cc 正向 + codex/opencode 反向斷言）
- **test first**：
  - `internal/agent/cc/events_test.go`（per spec §6.1 invariants 1-7 對 cc）
    - `TestCcEventSpecs_PurdexNamePdxPrefix`
    - `TestCcEventSpecs_NameMatchesTrimPrefix`：`Name == TrimPrefix(PurdexName, "Pdx")`
    - `TestCcEventSpecs_UpstreamKeysNotEmpty`（installable + non-installable 全 entry）
    - `TestCcEventSpecs_PurdexNameNotInUpstreamKeys`
    - `TestCcEventSpecs_LifecycleAlignment`：per §2.3.1 對照表
    - `TestCcEventSpecs_PreservedLegacyMetadata`：每 entry 的 EmitsStatus / Description / FutureOnly / Handling 與 pre-W2 git show 對照相同
  - `internal/agent/event_spec_test.go`（同 commit）— cross-agent invariant runner（per spec §6.1）：
    - `TestCatalogInvariants_CC_AllForwardInvariants`（cc 已遷 → invariants 1-7）
    - `TestCatalogInvariants_Codex_LegacyShape`（codex 未遷 → 反向斷言：每 entry `PurdexName == ""` / `UpstreamKeys == nil` / `Lifecycle == LifecycleNone`）
    - `TestCatalogInvariants_Opencode_LegacyShape`（opencode 同上）
- **impl**：
  - `internal/agent/cc/events.go` ccEventSpecs 改寫（per spec §2.2.1 plain struct literal style）
  - `internal/agent/event_spec_test.go` 新增 invariant runner（純 test 檔，遍歷三家 catalog）
- **verify**：`go test ./internal/agent/cc/... ./internal/agent/...`
- **commit msg**：`feat(agent/cc): P1-T7 catalog plain struct literal + cross-agent invariant runner`

#### P1-T8：cc DeriveStatus switch case rename

- **goal**：`deriveCCStatus` switch case label 改 `PdxXxx`
- **test first**：`internal/agent/cc/status_test.go`
  - `TestDeriveCCStatus_PdxUserPromptSubmit_Running`
  - `TestDeriveCCStatus_PdxNotification_PermissionPrompt_Waiting`
  - `TestDeriveCCStatus_PdxSessionStart_Compact_InvalidWithReason`
  - `TestDeriveCCStatus_LegacySessionStart_Invalid`：舊字面值不再認（catalog miss truly-unknown）
- **impl**：`internal/agent/cc/status.go:13-90` switch case label 全改 `PdxXxx`
- **verify**：`go test ./internal/agent/cc/`
- **commit msg**：`refactor(agent/cc): P1-T8 DeriveStatus switch case PdxName`

#### P1-T9：cc installer 改 UpstreamKey/PurdexName 邊界

- **goal**：settings.json hooks key=UpstreamKey、command 末段=PurdexName
- **test first**：`internal/agent/cc/hooks_test.go`
  - `TestMergeClaudeHooks_KeyIsUpstreamKey`：parse merged settings.json，hooks map keys ∈ pre-W2 字面值集合
  - `TestMakePdxEntry_CommandTokenIsPurdexName`：command 字串 split 後末段 token == PurdexName
  - `TestMergeClaudeHooks_CommandHasPdxPrefix`：command 字串包含 `PdxXxx`
- **impl**：`internal/agent/cc/hooks.go:116-220` 多函式 — `mergeClaudeHooks` / `makePdxEntry`（per spec §2.4 + §3 Phase 1）
- **verify**：`go test ./internal/agent/cc/`
- **commit msg**：`refactor(agent/cc): P1-T9 installer UpstreamKey/PurdexName boundary`

#### P1-T10：cc DRY 修補（known + cleanup sets）

- **goal**：`ccKnownEventNames` / `ccOwnedCleanupEventNames` 從 catalog 自動衍生
- **test first**：`internal/agent/cc/hooks_test.go`
  - `TestCcKnownEventNames_DerivedFromUpstreamKeys`：== `Filter(ccEventSpecs, IsInstallable).UpstreamKeys` union
  - `TestCcOwnedCleanupEventNames_ThreeSetUnion`：== UpstreamKeys ∪ PurdexName ∪ legacy Name（驗證三聯集 — per spec §6.1 invariant 6）
  - `TestCcOwnedCleanupEventNames_CleansLegacyAndNew`：手構 settings.json fixture（混合 pre-W2 `Stop` + W2 `PdxStop` 命令），跑兩次 reinstall round-trip 後兩者都被清掉
- **impl**：`internal/agent/cc/hooks.go:381-401`（per spec §3 Phase 1）— legacy `Name` set 此 phase **保留**（per G1：legacy set 在 PR-W2-cleanup-followup 才移除，避免 alpha.244 ship 時 user 還沒 reinstall 的中間態漏清舊命令）
- **verify**：`go test ./internal/agent/cc/`
- **commit msg**：`refactor(agent/cc): P1-T10 DRY known/cleanup sets from catalog`

### 2.5 Daemon lifecycle metadata-driven 改造（依賴 cc catalog 已填 Lifecycle）

#### P1-T11：handler lifecycle 三分支改造

- **goal**：`handler.go` 內所有硬編 event-name 字面值的 lifecycle 分支改 metadata-driven + fallback
- **依賴**：P1-T7（cc catalog 已填 Lifecycle）+ P1-T8（cc DeriveStatus 認 PdxXxx），否則 metadata-path 測試無法綠
- **test first**：`handler_test.go`（per spec §6.3.1 Phase 1 矩陣）
  - 6 個 cc metadata-path test（SessionStart/SessionEnd/SubagentStart/Stop/Notification no-op/PermissionRequest no-op）
  - 2 個 codex/opencode 中間態 fallback test（SessionEnd/SessionStart fallback）
  - 1 個 codex 提早送 PdxXxx → invalid（catalog miss + predicate fail）
  - 1 個 opencode + Notification → invalid（per §6.3.1 negative）
  - 1 個 opencode + PermissionRequest → fallback ok（per §6.3.1 positive）
- **impl**：`internal/module/agent/handler.go:181/186/187-189/278-285/301-305`（per spec §2.3.1 + §3.4.2）
  - 在 `provider.DeriveStatus` 後拿 `spec, ok := agent.LookupByPurdexName(installer.Events(), req.PurdexName)`（per spec §2.5 — 先 type-assert provider.(agent.HookInstaller)，再 free function lookup）
  - 三分支 switch（per spec §3.4.2 pseudo-code）
  - opencode-specific Stop guard `req.AgentType != "opencode"` 保留
- **verify**：`go test ./internal/module/agent/`
- **commit msg**：`refactor(agent): P1-T11 handler lifecycle metadata-driven dispatch`

#### P1-T12：frame_ops lifecycle 改造

- **goal**：frame mutation 路徑同步改 metadata-driven + fallback
- **依賴**：P1-T11（共用 lookup pattern）
- **test first**：`internal/module/agent/frame_ops_test.go`
  - `TestFrameOps_SubagentStart_MetadataPath`：cc PdxSubagentStart → frame.Subagents +1
  - `TestFrameOps_SubagentStart_LegacyFallback`：codex/opencode SubagentStart → frame.Subagents +1（fallback path）
  - `TestFrameOps_SessionEnd_MetadataPath`：cc PdxSessionEnd → frame deleted
  - `TestFrameOps_SessionEnd_LegacyFallback`：codex/opencode SessionEnd → frame deleted（fallback path）
- **impl**：`internal/module/agent/frame_ops.go:133-174` 等 frame mutation 路徑（per spec §2.3.1）
- **verify**：`go test ./internal/module/agent/`
- **commit msg**：`refactor(agent): P1-T12 frame_ops lifecycle metadata-driven`

### 2.6 SPA fixture 對齊（cc 部分）

#### P1-T13：SPA test fixture 更新（cc）

- **goal**：SPA snapshot / fixture 內硬寫 `"SessionStart"` 等 cc 字面值改 `"PdxSessionStart"`
- **test first / verify**：直接跑 `cd spa && pnpm run lint && npx vitest run`，看哪些 fixture 報錯，逐筆修正
- **impl**：grep `RawEventName|raw_event_name|event_name` 在 spa/src spa/test 的硬編 cc event name；逐筆改
- **verify**：vitest + lint + build 全綠
- **commit msg**：`test(spa): P1-T13 update cc event name fixtures to Pdx prefix`

### 2.7 端到端手動驗證

#### P1-T14：mlab 機 cc reinstall 完整 lifecycle 驗證（per G5）

- **goal**：實機驗證 cc Phase 1 daemon binary 正常運作 — 完整 lifecycle 路徑覆蓋
- **動作**：
  1. **build**：`go build -o /tmp/pdx-w2 ./cmd/pdx`
  2. **install**：`/tmp/pdx-w2 install --reinstall`（cc 部分）
  3. **檔案斷言（settings.json）**：
     - `~/.claude/settings.json` 內 `hooks.SessionStart[0].hooks[0].command` 末段 == `PdxSessionStart`
     - `hooks.UserPromptSubmit / Stop / SessionEnd / SubagentStart / SubagentStop / Notification / PermissionRequest / StopFailure` 同樣斷言：key 為 UpstreamKey、command 末段為 PdxXxx
  4. **reinstall idempotency**：再跑一次 `--reinstall`，settings.json diff 應為空（`git diff` 或 `cmp` 兩次輸出）
  5. **lifecycle 完整 smoke test**（cc session 內逐項觸發、觀察 daemon log + SPA lights）：
     - **SessionStart**：啟動 `claude` session → daemon log `event_name=PdxSessionStart`；SPA frame reset
     - **UserPromptSubmit**：送 prompt → log `PdxUserPromptSubmit`；SPA running
     - **Stop**：等 cc 結束 stream → log `PdxStop`；SPA idle
     - **StopFailure**：強制中斷（Ctrl-C）→ log `PdxStopFailure`；SPA error
     - **SessionEnd**：`exit` 或關 cc → log `PdxSessionEnd`；SPA frame 消失
     - **SubagentStart / SubagentStop**：在 cc 內觸發 sub-agent task → log 兩個 PdxSubagentXxx；SPA Subagents counter +1 / -1
     - **Notification（waiting no-op）**：cc 發出 permission notification → log `PdxNotification`；SPA waiting；frame 不被重置（lifecycle no-op 確認）
     - **PermissionRequest（waiting no-op）**：cc 發出 permission request → log `PdxPermissionRequest`；SPA waiting；frame 不變
  6. **error guard**：在 SPA 顯示 error 狀態下手動觸發 UserPromptSubmit → 應回到 running（白名單路徑驗證）
  7. **無回歸**：codex / opencode 仍在 mlab 跑著的 sessions 不應有任何狀態變動（fallback path 命中、無 Phase 1 影響）
- **無 commit**（純驗證）；任一條失敗則中斷並查 root cause、不進 PR

### 2.8 Phase 1 PR

#### P1-T15：起 PR-W2-1

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

> **G3 修訂**：codex catalog migration 與 cross-agent invariant 升級（reverse → forward）綁同一 commit（P2-T1），避免中間態 `go test ./internal/agent/` fail。

### 3.1 codex catalog

#### P2-T1：codex catalog 改 plain struct literal + 三新欄位 + invariant 升級為正向

- **goal**：codex 11 entries 全填 PurdexName / UpstreamKeys / Lifecycle；同 commit 把 cross-agent invariant runner 對 codex 從反向（zero-value 斷言）升級為正向（invariants 1-7）
- **test first**：
  - `internal/agent/codex/events_test.go`（per spec §6.1，11 entries 含 9 installable + 2 unsupported `PdxPreToolUse` / `PdxPostToolUse`）
    - 同 P1-T7 cc 一系列 invariant 測試（PurdexNamePdxPrefix / NameMatchesTrimPrefix / UpstreamKeysNotEmpty / PurdexNameNotInUpstreamKeys / LifecycleAlignment / PreservedLegacyMetadata）
  - `internal/agent/event_spec_test.go`（同 commit）— `TestCatalogInvariants_Codex_*` 從反向 `LegacyShape` 改為正向 `AllForwardInvariants`（invariants 1-7）；`TestCatalogInvariants_Opencode_LegacyShape` 維持
- **impl**：
  - `internal/agent/codex/events.go` codexEventSpecs 改寫（per spec §3 Phase 2）
  - `internal/agent/event_spec_test.go` codex invariants 升級
- **verify**：`go test ./internal/agent/codex/... ./internal/agent/...`
- **commit msg**：`feat(agent/codex): P2-T1 catalog plain struct literal + invariants forward upgrade`

#### P2-T2：codex DeriveStatus switch case rename

- **test first**：`codex/status_test.go`（per cc/P1-T8 同型 — 7 個 case test）
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

- **test first**：類同 P1-T10，但對 `codexOwnedCleanupEventNames` + `codexKnownEventNames`
  - `TestCodexOwnedCleanupEventNames_ThreeSetUnion`
  - `TestCodexOwnedCleanupEventNames_CleansLegacyAndNew`：mixed fixture round-trip
- **impl**：`internal/agent/codex/hooks.go:689-701` 改自動衍生（同 cc，legacy `Name` set 此 phase **保留** — per G1，PR-W2-cleanup-followup 才移除）
- **verify**：`go test ./internal/agent/codex/`
- **commit msg**：`refactor(agent/codex): P2-T4 DRY known/cleanup sets`

### 3.3 isLegacyHookForUnmigrated 升級

#### P2-T5：移除 codex case from predicate

- **依賴**：P2-T1（codex catalog 已填 Lifecycle，daemon metadata-path 命中後不再走 fallback）+ P2-T2（DeriveStatus 認 PdxXxx）
- **goal**：codex 已遷 → 從 `isLegacyHookForUnmigrated` 移除 codex case
- **test first**：`legacy_hook_test.go` 加
  - `TestIsLegacyHookForUnmigrated_CodexAllFalse`：codex + 任何 name → false（已遷）
  - opencode case 維持原樣
- **impl**：`internal/module/agent/legacy_hook.go` 移除 codex `case "codex":` 整段
- **verify**：`go test ./internal/module/agent/`
- **commit msg**：`refactor(agent): P2-T5 remove codex from legacy fallback predicate`

### 3.4 SPA fixture（codex）

#### P2-T6：SPA test fixture 更新（codex）

- 同 P1-T13 對 codex event name 字面值
- **commit msg**：`test(spa): P2-T6 update codex event name fixtures to Pdx prefix`

### 3.5 Phase 2 手動驗證 + PR

#### P2-T7：mlab 機 codex reinstall 完整 lifecycle 驗證（per G5）

- **goal**：實機驗證 codex Phase 2 daemon binary — 完整 lifecycle 路徑覆蓋
- **動作**：
  1. **build**：`go build -o /tmp/pdx-w2-p2 ./cmd/pdx`
  2. **install**：`/tmp/pdx-w2-p2 install --reinstall`（codex 部分）
  3. **檔案斷言**：
     - `~/.codex/hooks.json` 內 9 個 matcher group key 為 UpstreamKey、command 末段為 PdxXxx
     - `~/.codex/config.toml` 的 `features.codex_hooks=true` 不變
  4. **reinstall idempotency**：再跑一次 `--reinstall`，hooks.json + config.toml diff 皆為空
  5. **lifecycle 完整 smoke test**（codex session 內逐項觸發）：
     - **SessionStart**：`codex` 啟動 → log `PdxSessionStart`；SPA frame reset
     - **UserPromptSubmit / Stop / StopFailure / SessionEnd**：同 P1-T14 cc 對照表，daemon log 與 SPA lights
     - **SubagentStart / SubagentStop**：codex sub-agent task → counter 加減
     - **Notification / PermissionRequest（waiting no-op）**：no frame side-effect 確認
  6. **error guard**：error 狀態下手動觸發 UserPromptSubmit → 回 running
  7. **無回歸**：cc 已遷的 sessions（Phase 1 ship 的）與 opencode 仍 fallback 的 sessions 各自路徑正確
  8. **opencode 中間態 fallback 仍生效**：opencode session 跑 lifecycle，daemon log 顯示 `event_name=SessionStart` 等 legacy 字面值，frame 副作用透過 `isLegacyHookForUnmigrated` predicate 命中
- **無 commit**（純驗證）

#### P2-T8：起 PR-W2-2

- **codex review**：兩輪（標準 + 攻擊視角）

---

## 4. Phase 3（PR-W2-3）— opencode plugin template + transition cleanup

PR-W2-2 merged 後起。

> **G1 / G3 / G4 修訂**：
> - **G1**：cleanup helpers 的 legacy `Name` set 在本 PR 內**保留**（不移除），lift to PR-W2-cleanup-followup（待 alpha.244 ship + user reinstall 驗證後）。原 P3-T8 已移除，由 §5.3 PR-W2-cleanup-followup 接手。
> - **G3**：opencode catalog migration 與 cross-agent invariant 升級（reverse → forward）綁同一 commit（P3-T1）。
> - **G4**：P3-T4 移除 `Name` 欄位前，先把 cc/codex cleanup helper 重構為 fixture-derived 常數陣列（不依賴 `spec.Name`），新增前置 task P3-T4a。

### 4.1 opencode catalog

#### P3-T1：opencode catalog 改 plain struct literal + 三新欄位 + invariant 升級為正向

- **goal**：opencode 65 entries 全填 PurdexName / UpstreamKeys / Lifecycle（per spec §2.3 修訂後 — 含 unsupported/ignored 也填 UpstreamKeys = [原 Name]）；同 commit 把 cross-agent invariant runner 對 opencode 從反向升級為正向；同 commit 移除「Name dev-time 對應」invariant（per spec §6.1 invariant 2 — 三家全遷後 `Name == TrimPrefix(PurdexName, "Pdx")` 不再保證，因 opencode 部分 entry Name 與 PurdexName 不滿足 mechanical rename，例如 `auth.session` → `PdxAuthSession`）
- **test first**：
  - `opencode/events_test.go`（per spec §6.1，重點 `PdxPermissionRequest` UpstreamKeys 多元素 `["permission.asked","question.asked"]`；65 entries 全套 invariant 1 / 3 / 4 — UpstreamKeys 非空 + PurdexName ∉ UpstreamKeys；installable 8 entries 套 invariant 5 Lifecycle alignment + invariant 4 metadata preservation）
  - `internal/agent/event_spec_test.go`（同 commit）— `TestCatalogInvariants_Opencode_*` 從反向 `LegacyShape` 改為正向 `AllForwardInvariants`（invariants 1, 3-7）；同 commit 移除 invariant 2「Name dev-time 對應」runner（三家全升至正向 → 不再需要 dev-time backfill 校驗）
- **impl**：
  - `internal/agent/opencode/events.go` opencodeEventSpecs 改寫
  - `internal/agent/event_spec_test.go` opencode invariants 升級 + 移除 invariant 2 runner
- **verify**：`go test ./internal/agent/opencode/... ./internal/agent/...`
- **commit msg**：`feat(agent/opencode): P3-T1 catalog plain struct literal + invariants forward upgrade`

#### P3-T2：opencode DeriveStatus switch case rename

- 同 P1-T8 / P2-T2
- **commit msg**：`refactor(agent/opencode): P3-T2 DeriveStatus switch case PdxName`

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

#### P3-T4a：cleanup helper 重構為 fixture-derived 常數陣列（per G4 — 為 P3-T4 鋪路）

- **goal**：cc / codex cleanup helper 內的「pre-W2 legacy `Name` set」依賴改為**檔案層級 fixture 陣列常數**（不再從 `spec.Name` 動態衍生），讓 P3-T4 移除 `Name` 欄位後 build 不破。法律 set 維持原狀，只是來源從「runtime spec.Name 反查」改為「靜態常數陣列」。
- **依賴**：P3-T1 / P3-T2 / P3-T3（catalog 已遷移完）
- **test first**：cc / codex hooks_test.go
  - `TestCcOwnedCleanupEventNames_FixtureDerivedLegacySet`：cleanup set 等於「UpstreamKeys ∪ PurdexName ∪ ccLegacyEventNames（檔案常數陣列）」；驗證 ccLegacyEventNames 內容與 pre-W2 Name 字面值一致（手寫對照表）
  - codex 同
- **impl**：
  - `internal/agent/cc/hooks.go`：新增 `ccLegacyEventNames []string`（檔案層級 var）值 = pre-W2 Name 字面值（手寫，9 entries）；cleanup helper 三聯集改用此常數，不再走 `spec.Name`
  - `internal/agent/codex/hooks.go`：同 — 新增 `codexLegacyEventNames []string`（11 entries）；cleanup helper 改用
  - **無**改動 cleanup 邏輯與三聯集規模 — 純粹是來源重構
- **verify**：`go test ./internal/agent/cc/... ./internal/agent/codex/...`
- **commit msg**：`refactor(agent): P3-T4a fixture-derive cleanup legacy name sets`

#### P3-T4：移除 `Name` 欄位 + 三家 catalog literal 移除 `Name:` 行

- **依賴**：P3-T4a（cleanup helper 已不依賴 `spec.Name`）
- **goal**：`HookEventSpec.Name` 欄位刪除；三家 catalog literal 移除 `Name:` 行
- **test first**：先確認三家 events_test.go / hooks_test.go 已不依賴 `spec.Name`（前面 phase + P3-T4a 應該已遷，再次確認）；移除 invariant 2 對應測試 — 已在 P3-T1 同步移除
- **impl**：
  - `internal/agent/provider.go` 移除 `Name` 欄位
  - 三家 events.go 每 entry 移除 `Name: "..."` 行
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

### 4.4 SPA fixture（opencode）

#### P3-T7：SPA test fixture 更新（opencode）

- 同 P1-T13 / P2-T6 對 opencode
- **commit msg**：`test(spa): P3-T7 update opencode event name fixtures to Pdx prefix`

### 4.5 手動驗證 + PR

#### P3-T8：mlab 機 opencode reinstall 完整 lifecycle 驗證（per G5）

- **goal**：實機驗證 opencode Phase 3 daemon binary — 完整 lifecycle 路徑覆蓋 + 三家全 metadata-driven 一致性
- **動作**：
  1. **build**：`go build -o /tmp/pdx-w2-p3 ./cmd/pdx`
  2. **install**：`/tmp/pdx-w2-p3 install --reinstall`（opencode 部分）
  3. **檔案斷言**：
     - `~/.config/opencode/plugins/pdx-agent-hooks.js` 含新的 `const PURDEX_EVENT = {...}` 常數區塊
     - 8 個 `emit(PURDEX_EVENT.PdxXxx, ...)` 命中；無殘留 legacy `emit('SessionStart'` 等
     - magic marker `pdx-managed:opencode-hooks:v1` 不變
  4. **reinstall idempotency**：再跑一次 `--reinstall`，plugin 檔案 diff 為空
  5. **lifecycle 完整 smoke test**（opencode session 內逐項觸發）：
     - **SessionStart / UserPromptSubmit / Stop / StopFailure / SessionEnd**：daemon log 顯示 `event_name=PdxXxx`；SPA lights 對齊
     - **SubagentStart / SubagentStop**：opencode tool.execute task → counter 加減（注意 input.tool 'task' filter 行為仍由 plugin 端 demux）
     - **PermissionRequest（多 upstream → 單 PurdexName）**：opencode 觸發 permission.asked **與** question.asked 兩種上游，daemon 都收到 `PdxPermissionRequest`；SPA waiting；frame 不變
     - **session.status 非 idle 不發 PdxStop**：opencode session 進入 busy / retry 等子狀態 → plugin 不 emit `PdxStop`（filter type='idle' 確認 — 防止誤判 idle）
  6. **error guard**：error 狀態下 UserPromptSubmit → 回 running
  7. **fallback 已移除**：手動模擬送 legacy `SessionStart` 字面值（`pdx hook --agent opencode SessionStart`）→ daemon log 顯示 `invalid + event_not_in_catalog`（fallback 已移除驗證）
  8. **三家 metadata-driven 一致性**：cc / codex / opencode 各跑一次 SessionEnd，daemon log 都走 `metadata-path / Lifecycle=SessionEnd`，無 fallback 命中
- **無 commit**（純驗證）

#### P3-T9：起 PR-W2-3

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

### 5.3 PR-W2-cleanup-followup（per G1，post-bump 再起）

POST-T1 ~ POST-T3 三家 reinstall + lifecycle smoke 全綠後**才**起此 PR。否則保留 legacy `Name` set 給尚未 reinstall 的 user 容錯。

#### CLEANUP-T1：移除 cc / codex cleanup helper 的 legacy `Name` set

- **goal**：cc / codex `*OwnedCleanupEventNames` 從三聯集（UpstreamKeys ∪ PurdexName ∪ ccLegacyEventNames）簡化為兩聯集（UpstreamKeys ∪ PurdexName）；同 commit 移除 P3-T4a 引入的 `ccLegacyEventNames` / `codexLegacyEventNames` 常數陣列
- **test first**：cc / codex hooks_test.go
  - 修改 `TestCcOwnedCleanupEventNames_FixtureDerivedLegacySet` → `TestCcOwnedCleanupEventNames_TwoSetUnion`：== UpstreamKeys ∪ PurdexName（無 legacy `Name`）
  - 確認 `TestCcOwnedCleanupEventNames_CleansLegacyAndNew` round-trip test 對只含 W2 新命令的 fixture 仍 idempotent；對混合 fixture 確認舊命令現在**會殘留**（已不在 cleanup set），加 `TestCcOwnedCleanupEventNames_OldFixtureNotCleaned`：說明 followup 假設用戶已 reinstall 過、舊命令不應再存在
- **impl**：
  - `internal/agent/cc/hooks.go`：cleanup helper 改兩聯集；移除 `ccLegacyEventNames` 常數
  - `internal/agent/codex/hooks.go`：同 — 改兩聯集；移除 `codexLegacyEventNames` 常數
- **verify**：`go test ./internal/agent/...`
- **commit msg**：`refactor(agent): CLEANUP-T1 simplify cleanup sets to two-set union`

#### CLEANUP-T2：起 PR + 跨機驗證

- **動作**：
  - 起 PR-W2-cleanup-followup（base origin/main，bump-PR merged 後狀態）
  - PR body 提醒 reviewer：「依 alpha.244 ship + reinstall 假設；POST-T1 ~ POST-T3 已驗證 OK」
  - merge 後在 mlab + air 跑一次 reinstall idempotency check（plugin / settings / hooks 三檔 diff 為空）
- **codex review**：一輪標準（小 PR，無架構面風險）

#### CLEANUP-T3：bump（與 CLEANUP-T1 同 PR 或單獨 follow-up）

- **goal**：`alpha.245` bump
- **動作**：依 CLAUDE.md bump 流程（VERSION + package.json + spa/package.json + CHANGELOG.md）
- **可選**：如 CLEANUP-T1 風險低、無功能影響，可與一般 alpha bump 合併不另起 follow-up bump PR

---

## 6. 風險與中斷恢復

### 6.1 主要風險（per spec §7.2）+ plan 對應緩解

| Risk | Plan 緩解 |
|------|-----------|
| Phase 1/2 main 中間態誤 bump | BUMP-T1 強制延後到 PR-W2-3 merged 之後 |
| Catalog literal 改寫誤丟 metadata | P1-T7 / P2-T1 / P3-T1 含 `TestXxxEventSpecs_PreservedLegacyMetadata` 對照 git show baseline |
| Phase 1/2 期間 Name / PurdexName 漂移 | P1-T7 cross-agent invariant runner 包含 `Name == TrimPrefix(PurdexName, "Pdx")`（cc 正向 + codex/opencode 反向）；Phase 3 P3-T4 強制移除 Name 欄位讓編譯器抓 stale |
| Phase 1/2 中間態 `go test ./internal/agent/` fail | catalog migration 與 invariant 升級綁同一 commit（P1-T7 / P2-T1 / P3-T1，per G3）|
| daemon lifecycle fallback 誤跨 agent 命中 | P1-T6 isLegacyHookForUnmigrated per-agent literal set + negative test（opencode + Notification → invalid）|
| LookupByUpstreamKey 誤用於 opencode filter routing | spec §2.1 helper godoc 警告（P1-T3 必含）+ §2.5；無新 routing helper |
| cleanup set 漏抓 | P1-T10 / P2-T4 三聯集 + round-trip test（先 pre-W2 fixture / 再 W2 reinstall fixture）；legacy set 保留至 PR-W2-cleanup-followup（per G1）|
| Phase 3 移除 Name 欄位時 cleanup helper build 破 | P3-T4a 先把 cleanup helper 重構為 fixture-derived 常數（per G4），P3-T4 才動 Name 欄位 |
| opencode plugin emit 改常數注入後 JS template 解析錯誤 | P3-T3 unit test 含 magic marker / emit RHS / 無 legacy 字面值三斷言 |
| reinstall 對 user 環境破壞性 | POST-T2 / POST-T3 ad-hoc 驗證 + POST-T4 rollback 路徑 |
| alpha.244 ship 後 user 尚未 reinstall 而 cleanup set 已縮減 | legacy `Name` set 保留至 PR-W2-cleanup-followup，等 POST 三條全綠後才移除（per G1）|
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
