# Catalog Naming Separation (W2) — Dev Spec

- **Date**: 2026-04-28
- **Worktree**: `lights-w2-naming`（branch `worktree-lights-w2-naming`）
- **Work item**: W2（per `docs/specs/2026-04-28-lights-rebuild-fix-spec.md` §2）
- **Type**: 程式 + 測試 + post-ship reinstall（純內部抽象 + 一次性 rename）
- **Replaces**: 不取代任何 doc；本 spec 是 fix-spec PR-3 的 dev spec 落地

---

## 0. 來龍去脈

W2 是 fix-spec §4 PR-3 工作 — 在 catalog 邊界引入雙欄位區分，讓 daemon 內部命名與 upstream agent 自家事件命名解耦。**不依賴 W1 audit ship**（catalog naming 是 input/output 邊界整理，跟 audit 結果正交）。

當前狀態（alpha.243 main）：

- 三家 catalog `events.go` 用單一 `Name string` 欄位，**同時承擔三個角色**：
  1. daemon 內部 catalog 主鍵 / DeriveStatus switch case label
  2. installer 寫進 hooks file 的 matcher key（cc/codex 寫 settings.json/hooks.json，opencode 由 plugin emit）
  3. CLI `pdx hook --agent <X> <Name>` 的 positional argument
- 三角色綁同一字串：upstream agent 任一改名 → daemon 內部全部跟著動 / 反之亦然，膨脹風險

**W2 改造後**：

```
HOOK LAYER (per-agent installer + handler entry)
  cc:       ~/.claude/settings.json   key = UpstreamKey "SessionStart"
  codex:    ~/.codex/hooks.json       key = UpstreamKey "SessionStart"
  opencode: plugin Bus listener       key = UpstreamKey "session.created"
                          ↓
           command argument (or plugin emit) = PurdexName "PdxSessionStart"
                          ↓
        Daemon handler entry 收到永遠是 (agent_type, PurdexName)
                          ↓
                  DeriveStatus switch case = PurdexName
```

**核心原則**（per fix-spec §1）：

- **抽象在 input/output 邊界**：UpstreamKey 只在 installer 寫 hooks file / opencode plugin 端 demux 時出現
- **daemon 內部一律 PurdexName**：handler / DeriveStatus / NormalizedEvent / WS broadcast / TraceStore 全部用 PurdexName
- **`Pdx` 前綴用以區分**：與 cc/codex 上游同名（如 `SessionStart`）視覺一望而辨

### 0.1 Scope decision

**W2 同時做 schema 抽象 + rename**（per user 決議）：

- Schema：`HookEventSpec` 加 `PurdexName string` + `UpstreamKeys []string` 兩欄位
- Rename：所有 catalog entry 的 `PurdexName` 加 `Pdx` 前綴（mechanical 加前綴，**不重新分類**）

不分兩 PR（不做 W2a schema-only / W2b rename）的理由：

- schema 加雙欄位卻不 rename → PurdexName 與 UpstreamKey 字串值今日相等 → 無法在測試與 review 中驗證「daemon 與 installer 分離」是否真的生效（測不出兩個欄位被誤用）
- 一次到位 + 一次 reinstall，user 體驗成本最低

**No user-facing migration / backward-compat**（per `feedback_no_alpha_migration`）：

- daemon **不接受** post-W2 與 pre-W2 的「兩種 hook payload 形態並存」(no fallback / no two-step lookup)
- user 升級 alpha 後**必須**跑 `pdx install --reinstall` 才能繼續用，否則該 agent 的 hook payload 會被當成 unknown event
- 中間態風險（main 已進 Phase 1 但 user 還沒升）靠**延後 bump** 規避：三 phase **全 merge 完**才出單一 bump PR，bump 前 main 只是 dev-time work-in-progress，user 端永遠跑 pre-W2 daemon + 舊 hooks
- 本 spec 中 `Name` 欄位的雙寫 transition（§3.4）是 **dev-time aid**，純粹給 main 上 Phase 1/2 期間 codex/opencode 的 catalog literal 過渡編譯用，**不是 runtime fallback**，daemon 在任何 phase 都只 dispatch 自家 PurdexName

### 0.2 與 W1 audit 的關係

W1 audit doc (`docs/specs/2026-04-28-hook-status-audit-spec.md`) §4 與 §6/§7 工作池中的事件名稱（`SessionStart` / `Stop` / `PermissionRequest` 等）是 **pre-W2 PurdexName == UpstreamKey 時期的字面值**。本 spec 完成後 **audit doc 不需修改** — 因為：

- audit doc 描述的是「邏輯事件 / catalog 概念」層次，不是「字串字面值」層次
- audit doc §9 已內含 W2 命名 disclaimer（"W2 之後 daemon 內部一律使用 PurdexName"）
- audit doc 的 W5/W6 工作池若日後引用具體 catalog event，按當時的 PurdexName 重新對齊（W5/W6 PR 時自然修正）

---

## 1. 範圍與目標

### 1.1 範圍

| 區塊 | 修改內容 |
|------|---------|
| 共用層 schema | `internal/agent/provider.go` 加 `PurdexName` + `UpstreamKeys []string` 欄位；`Name` 欄位**保留為 deprecated alias**，僅 transition 期間相容（spec §6 收尾刪除）|
| cc | `internal/agent/cc/{events,hooks,status}.go` 三檔；catalog entry rename + installer 改寫 + DeriveStatus case rename |
| codex | `internal/agent/codex/{events,hooks,status}.go` 三檔；同 cc + DRY 修補 `codexOwnedCleanupEventNames` |
| opencode | `internal/agent/opencode/{events,hooks,plugin_template,status}.go` 四檔；plugin emit RHS 改 Go 端常數注入 |
| CLI | `cmd/pdx/hook.go` positional arg 語義升級（值轉變：UpstreamKey → PurdexName） |
| Handler | `internal/module/agent/handler.go` `EventRequest` 欄位語義升級；JSON tag 更新；NormalizedEvent.RawEventName 語意保持 |
| Tests | 三家 hooks_test / status_test / 共用 events_test；新增「installer key vs command arg 不同字串」斷言 + handler per-agent_type per-phase 行為矩陣（§6.3）|
| Documentation | 本 spec / kickoff memory / fix-spec §10 dev spec 路徑勾稽 |

### 1.2 不在範圍

- ❌ W1 audit doc 內容修正（per §0.2）
- ❌ DeriveStatus 邏輯改動（只 rename case label，不改 status mapping）
- ❌ ProbeIntent / W6 框架（屬 W3+W4 / W5+W6 PR）
- ❌ SPA / Electron 端 status projection 與 UI 改動（NormalizedEvent.RawEventName 對 SPA 端是 opaque 字串，rename 後不影響 SPA 邏輯）
- ❌ 跨 agent 的 catalog 統一（三家仍維持各自 `events.go`）
- ❌ Telemetry / counters 命名變更
- ❌ 新增任何 always-on 行為或 framework

### 1.3 結束條件

當下列全達成時 W2 視為完成：

1. ✅ `HookEventSpec` 加 `PurdexName` + `UpstreamKeys` 欄位；`Name` 標 deprecated comment 並在 transition PR 後刪除
2. ✅ 三家 catalog 全 rename 為 `Pdx` prefix；`UpstreamKeys` 正確填入
3. ✅ 三家 installer / plugin 寫入 hooks file 後 key=UpstreamKey、command(or plugin emit)=PurdexName
4. ✅ Daemon handler / DeriveStatus 全用 PurdexName
5. ✅ Go test (`go test ./...`) 全綠
6. ✅ SPA 邊跑 vitest + lint + build 全綠（NormalizedEvent.RawEventName 字面值改變，需更新 fixture）
7. ✅ 三 phase PR 依序 ship（PR-W2-1 → PR-W2-2 → PR-W2-3），全 merged 後**才**出單一 PR-W2-bump
8. ✅ Post-ship 在 mlab 主機跑 `pdx install --reinstall` 並 ad-hoc 檢查三個 hook 檔案命名對齊
9. ✅ Spec 過 codex review 兩輪（per CLAUDE.md PR Review 兩輪制）

---

## 2. 設計核心

### 2.1 雙欄位 schema

```go
// internal/agent/provider.go
type HookEventSpec struct {
    // PurdexName is the daemon-internal stable identifier for this catalog
    // entry. Always prefixed with "Pdx". Used as:
    //   - DeriveStatus switch case label
    //   - CLI `pdx hook --agent <agent> <PurdexName>` positional argument
    //   - HTTP EventRequest.PurdexName payload value
    //   - NormalizedEvent.PurdexName / TraceStore record key
    // Daemon code MUST use PurdexName for all internal lookups and matching.
    PurdexName string

    // UpstreamKeys lists the raw event names that the agent's upstream hook
    // system fires when this catalog entry should match. Used only at the
    // installer/plugin boundary:
    //   - cc: written as ~/.claude/settings.json "hooks" map key
    //   - codex: written as ~/.codex/hooks.json matcher-group key
    //   - opencode: matched against Bus event name in plugin demux switch
    // For cc/codex this is normally a single-element slice. For opencode,
    // multiple upstream Bus events may map to the same PurdexName
    // (e.g., permission.asked + question.asked → PdxPermissionRequest).
    UpstreamKeys []string

    EmitsStatus []Status
    Description string
    FutureOnly  bool
    Handling    HookHandling
}
```

**lookup helpers**（共用層，新增）：

```go
// LookupByPurdexName: O(N), 預期 N≤11，無需 index。
// 給 daemon 內部所有 catalog 反查使用（包含 DeriveStatus / handler routing）。
func LookupByPurdexName(specs []HookEventSpec, purdexName string) (HookEventSpec, bool)

// LookupByUpstreamKey: O(N*M)，M = avg UpstreamKeys size，極小。
//
// 限制（per spec §2.5）:
//   - 僅適用於 cc / codex 等「UpstreamKey 與 PurdexName 1:1 對應」的 agent，
//     可用於測試斷言或 dev tooling。
//   - **不適合用於 opencode filter-based events 的 runtime routing**：
//     opencode `session.status` / `tool.execute.before` / `tool.execute.after`
//     需要 type / tool filter 才能正確映射到 catalog；catalog UpstreamKeys 不表達
//     filter 條件，runtime routing 仍以 plugin 端 demux 為權威。Lookup 命中
//     `session.status` 直接得到 `PdxStop` 在 busy/retry 子型情境下會誤判。
func LookupByUpstreamKey(specs []HookEventSpec, upstreamKey string) (HookEventSpec, bool)
```

不導入 map / index — N 太小，maintain index 反而引入 cache invalidation。

### 2.2 命名規則：`Pdx` 前綴 + mechanical rename

**規則**：每個 catalog entry 的 `PurdexName` = `"Pdx" + 既有 Name`，**字面 mechanical 加前綴**，不改 capitalization、不重組分類。

| 原 Name (cc/codex 三家通用) | PurdexName |
|---|---|
| `SessionStart` | `PdxSessionStart` |
| `UserPromptSubmit` | `PdxUserPromptSubmit` |
| `Stop` | `PdxStop` |
| `StopFailure` | `PdxStopFailure` |
| `Notification` | `PdxNotification`（cc / codex；opencode 不存在）|
| `PermissionRequest` | `PdxPermissionRequest` |
| `SessionEnd` | `PdxSessionEnd` |
| `SubagentStart` | `PdxSubagentStart` |
| `SubagentStop` | `PdxSubagentStop` |

cc 額外的 catalog miss 路徑（`compact_ignored` / `notification_unknown_type`）為 reason string，**不是 catalog entry**，不在 rename 範圍。

### 2.3 UpstreamKeys 填值規則（per agent）

#### cc（9 installable）

UpstreamKeys 都是單元素 slice，值 = pre-W2 Name（即 cc CLI 上游 hook event name）：

| PurdexName | UpstreamKeys |
|---|---|
| `PdxSessionStart` | `["SessionStart"]` |
| `PdxUserPromptSubmit` | `["UserPromptSubmit"]` |
| `PdxStop` | `["Stop"]` |
| `PdxStopFailure` | `["StopFailure"]` |
| `PdxNotification` | `["Notification"]` |
| `PdxPermissionRequest` | `["PermissionRequest"]` |
| `PdxSessionEnd` | `["SessionEnd"]` |
| `PdxSubagentStart` | `["SubagentStart"]` |
| `PdxSubagentStop` | `["SubagentStop"]` |

#### codex（9 installable + 2 unsupported）

同 cc 規則。`PreToolUse` / `PostToolUse` 兩個 unsupported entry：UpstreamKeys = `[原 Name]`，PurdexName = `Pdx` + 原 Name（即使非 installable 也加前綴，避免後續若 promote installable 時遺漏）。

#### opencode（8 installable + 20 unsupported + 37 ignored = 65 total）

只 8 installable 需要填 UpstreamKeys（其餘 Unsupported / Ignored 不安裝、不參與 plugin demux，UpstreamKeys 留 `nil`）：

| PurdexName | UpstreamKeys（plugin Bus 事件來源）|
|---|---|
| `PdxSessionStart` | `["session.created"]` |
| `PdxUserPromptSubmit` | `["chat.message"]` |
| `PdxStop` | `["session.status"]`（plugin 內 type==='idle' filter）|
| `PdxStopFailure` | `["session.error"]` |
| `PdxPermissionRequest` | `["permission.asked", "question.asked"]` ← **多元素**|
| `PdxSessionEnd` | `["session.deleted"]` |
| `PdxSubagentStart` | `["tool.execute.before"]`（plugin 內 input.tool==='task' filter）|
| `PdxSubagentStop` | `["tool.execute.after"]`（同上 filter）|

⚠️ **設計界限**：filter 條件（`type==='idle'` / `input.tool==='task'`）**不入 catalog**，仍歸 plugin 端 dispatch 邏輯。catalog UpstreamKeys 只表達「哪些 raw event 名會 fire 進這個 catalog entry」，不表達 demux 細節。

### 2.4 邊界擺位（單張表收斂）

| 邊界位置 | 用 PurdexName 還是 UpstreamKey | 程式位置 |
|---|---|---|
| cc settings.json `hooks` map key | UpstreamKey | `cc/hooks.go:mergeClaudeHooks` |
| cc command 字串 `pdx hook --agent cc <X>` 的 `<X>` | PurdexName | `cc/hooks.go:makePdxEntry` |
| codex hooks.json matcher-group key | UpstreamKey | `codex/hooks.go:mergeCodexHooksFile` |
| codex command 字串 | PurdexName | `codex/hooks.go:mergeCodexHooksFile` |
| opencode plugin Bus listener case label | UpstreamKey | `opencode/plugin_template.go` switch 的 case 字串 |
| opencode plugin emit() 第一引數 | PurdexName | `opencode/plugin_template.go` emit() 呼叫點 |
| opencode plugin Bun.spawn args | PurdexName | plugin 內 spawn 邏輯（已是 emit 後） |
| CLI `pdx hook --agent <agent>` positional[0] | PurdexName | `cmd/pdx/hook.go:64` |
| HTTP EventRequest body | PurdexName | `internal/module/agent/handler.go:78-87` |
| DeriveStatus eventName 參數 | PurdexName | 三家 `status.go:9-...` |
| NormalizedEvent.RawEventName | PurdexName | `handler.go:308 / 337`（SPA 已視 opaque 字串）|
| TraceStore record event_name 欄位 | PurdexName | trace 寫入點 |

### 2.5 反查需求

僅 opencode plugin 反查使用：plugin 收到 Bus event name → 找對應 PurdexName 來 emit。

實作上 **不依賴 daemon Go 端 lookup**：

- plugin 是 **JS code template**，writeManagedPlugin 階段把映射表硬編進 plugin（dispatch table 從 Go events.go 編譯期讀取後 stringify 嵌入）
- 因此 reverse lookup 在 plugin 內是 O(1) switch case，不需 runtime 反查
- Go 端的 `LookupByUpstreamKey` helper 只供測試斷言與將來其他用途

---

## 3. 三 phase 切分

### Phase 1（M）— 共用 schema + cc 端到端

**範圍**：

- `internal/agent/provider.go`：新增 `PurdexName` + `UpstreamKeys` 欄位 + lookup helpers + `Name` 欄位標 `// Deprecated:` comment（保留以利 codex/opencode phase 過渡編譯）
- `internal/agent/cc/events.go`：catalog entry 用共用 helper `agent.NewSpec(...)` 一次填 PurdexName + UpstreamKeys（同時 backfill `Name` deprecated 欄位以利 codex/opencode 過渡編譯）；phase 3 ship 同 PR 移除 `Name` backfill
- `internal/agent/cc/hooks.go`：`mergeClaudeHooks` / `makePdxEntry` 改寫；`ccKnownEventNames` / `ccOwnedCleanupEventNames` 從 catalog 自動衍生（DRY 修補）
- `internal/agent/cc/status.go`：`deriveCCStatus` switch case 改 `PdxXxx`
- `cmd/pdx/hook.go`：positional[0] 註解改為 `purdexName`，buildHookPayload 第二參數命名同步
- `internal/module/agent/handler.go`：`EventRequest.EventName` 改名 `PurdexName`，**primary JSON tag = `purdex_name`**；保留 `event_name` 為 unmarshal-only alias（Go custom UnmarshalJSON：兩鍵都接、`purdex_name` 優先；marshal 只送 `purdex_name`），Phase 3 ship 同 PR 移除 alias — see §3.4 / §4.1 transition 策略
- `internal/module/agent/handler.go`：呼 `provider.DeriveStatus(req.PurdexName, req.RawEvent)`
- 共用 `event_spec_test.go`（新檔）：測 lookup helpers + cc spec 的「PurdexName 與 UpstreamKeys 互斥（PurdexName 不能 == 任一 UpstreamKey）」斷言（防止退化成單一字串）
- `cc/hooks_test.go`：新增測試「installer 寫入 settings.json 時 hooks key == UpstreamKey 且 command 字串末段 token == PurdexName」

**測試**：

- `go test ./internal/agent/...` 全綠
- `go test ./internal/module/agent/...` 全綠
- `go test ./cmd/pdx/...` 全綠
- 手動驗證：`go run ./cmd/pdx install --check`（cc 部分）顯示無漂移

**退場驗證**：

- 在 worktree 內跑 `go run ./cmd/pdx install --reinstall`（針對 mlab 主機 cc）後檢視 `~/.claude/settings.json`：`"hooks"` 下 key 應仍是 `"SessionStart"` 等 UpstreamKey；每個 entry 的 `command` 字串末段應改為 `PdxSessionStart`

**PR 範圍**：~600-900 行 diff（含測試）

---

### Phase 2（M）— codex 端到端

**範圍**：

- `internal/agent/codex/events.go`：catalog entry 加 PurdexName + UpstreamKeys
- `internal/agent/codex/hooks.go`：`mergeCodexHooksFile` matcher-group key=UpstreamKey、command=PurdexName；`codexOwnedCleanupEventNames()` 從 catalog 自動衍生（DRY 修補）；`checkCodexEvent` 用 UpstreamKey 反查 hooks.json key
- `internal/agent/codex/status.go`：`deriveCodexStatus` switch case 改 `PdxXxx`
- `codex/hooks_test.go`：新增測試「matcher-group key == UpstreamKey 且 command == PurdexName」+「`codexOwnedCleanupEventNames()` 與 `codexEventSpecs` 同步」

**驗證**：

- `~/.codex/hooks.json` reinstall 後 key 為 `"SessionStart"` 等，command 字串末段為 `PdxSessionStart`
- `~/.codex/config.toml` 的 `features.codex_hooks=true` 不變動（與 event name 無耦合）

**前置依賴**：Phase 1 ship（schema 已加雙欄位 + handler 已用 PurdexName）

**並行可能**：與 Phase 3 可在 Phase 1 ship 後並行 catalog 改寫部分；但 Phase 3 內含「移除 `Name` 欄位 + 移除 `event_name` JSON alias」cleanup step **必須在 Phase 2 merged 後**才能進行（否則 codex package 仍引用 `spec.Name` 編譯失敗）

---

### Phase 3（S-M）— opencode plugin template + transition cleanup

**範圍**：

- `internal/agent/opencode/events.go`：catalog 加 PurdexName + UpstreamKeys（含多元素 PdxPermissionRequest）
- `internal/agent/opencode/plugin_template.go`：
  - 模板頭部加 `const PURDEX_EVENT = { PdxSessionStart: "PdxSessionStart", ... }` 物件，由 Go 端編譯期生成（`renderManagedPlugin` 內 string concat）
  - emit() 呼叫點 8 處 RHS 從硬寫 `'SessionStart'` 改為 `PURDEX_EVENT.PdxSessionStart`
  - case label（LHS）保持原樣（UpstreamKey）
  - 其他 plugin 邏輯（`suppressIdleForSession` / type filter）不動
- `internal/agent/opencode/hooks.go`：`renderManagedPlugin(pdxPath)` 簽章不變；內部加常數注入；magic marker 不升（per Q3）
- `internal/agent/opencode/status.go`：`deriveOpenCodeStatus` switch case 改 `PdxXxx`
- `opencode/hooks_test.go`：plugin template 渲染後驗證
  - 8 處 `emit('PdxXxx', ...)` 出現
  - 沒有殘留硬寫的 `emit('SessionStart', ...)` 形態
  - magic marker 仍為 `pdx-managed:opencode-hooks:v1`
- **Transition cleanup（同 PR）**：
  - 移除 `internal/agent/provider.go` 的 `Name` 欄位 + `agent.NewSpec` helper 的 `Name` backfill
  - 移除 `EventRequest` 的 `event_name` JSON alias（custom UnmarshalJSON 簡化為單鍵 `purdex_name`）
  - cc / codex 的 catalog literal 不再有 `Name`（前兩 phase 的 backfill 路徑全清）

**驗證**：

- `~/.config/opencode/plugins/pdx-agent-hooks.js` reinstall 後查看：
  - `case 'session.created':` 等 LHS 保留 UpstreamKey
  - `emit('PdxSessionStart', ...)` 等 RHS 是 PurdexName（或常數引用）
  - magic marker `pdx-managed:opencode-hooks:v1` 不變
- 啟動實機 opencode session，觀察 daemon log 收到的 EventRequest.PurdexName 為 `PdxXxx`

**前置依賴**：**Phase 2 ship**（Phase 3 cleanup 步驟移除的 `Name` 欄位若 Phase 2 還沒 merge，codex package 仍會引用，編譯失敗）；catalog 改寫部分可與 Phase 2 並行起草，但 PR ship 順序強制 Phase 2 → Phase 3

---

### 3.4 Dev-time transition 機制（**not** user-facing migration）

Phase 1 加 `PurdexName` 後 `Name` 欄位**短暫並存於 main**（cc 已用新欄位、codex/opencode 仍用舊）。**這是 main-branch dev-time 雙寫，不是 user-facing migration** — bump 之前 user 永遠跑 pre-W2 daemon（per §0.1 「No user-facing migration」），看不到 main 的中間態。

| Phase（main-only） | provider.go HookEventSpec | cc | codex | opencode |
|---|---|---|---|---|
| Pre-W2 | 只有 `Name` | 用 Name | 用 Name | 用 Name |
| Phase 1 ship | 加 PurdexName + UpstreamKeys；Name 標 Deprecated；helper 雙寫 | 用 PurdexName/UpstreamKeys；DeriveStatus case=PdxXxx | 仍用 Name（catalog literal 經 helper 雙寫）；DeriveStatus case=Name | 仍用 Name（同上）；DeriveStatus case=Name |
| Phase 2 ship | 同上 | 已遷 | 用 PurdexName/UpstreamKeys；DeriveStatus case=PdxXxx | 仍用 Name；DeriveStatus case=Name |
| Phase 3 ship | 移除 Name 欄位 + helper 雙寫 + JSON alias | 已遷 | 已遷 | 已遷 |
| Bump alpha | 三 phase 全 merge 後 single bump PR | — | — | — |

Phase 1/2 期間 `Name` 與 `PurdexName` 並存：

- 為避免雙寫漂移，**catalog literal 用共用 exported helper builder 自動生成兩欄位**：

```go
// internal/agent/provider.go
// NewSpec is a transition helper used by catalog packages (cc / codex / opencode)
// to populate both the new PurdexName/UpstreamKeys fields and the deprecated Name
// field in a single literal. Removed in Phase 3 ship together with the Name field.
//
// Exported because cc/codex/opencode are separate packages and cannot reference
// a package-private helper.
func NewSpec(purdexName string, upstreamKeys ...string) HookEventSpec {
    return HookEventSpec{
        PurdexName:   purdexName,
        UpstreamKeys: upstreamKeys,
        Name:         strings.TrimPrefix(purdexName, "Pdx"), // dev-time backfill only
    }
}
```

Catalog 各 package 引用：

```go
// internal/agent/cc/events.go (Phase 1 ship 起)
var ccEventSpecs = []agent.HookEventSpec{
    agent.NewSpec("PdxSessionStart", "SessionStart"), // PurdexName, then UpstreamKeys...
    agent.NewSpec("PdxUserPromptSubmit", "UserPromptSubmit"),
    // ...
}
```

- Phase 3 ship（依賴 Phase 2 merged）：移除 `Name` 欄位 + `NewSpec` helper 改為 plain literal `agent.HookEventSpec{PurdexName: ..., UpstreamKeys: ...}`

**Phase 1/2 main-only safety net**：Phase 1/2 期間若 codex / opencode package 仍引用 `spec.Name`，因 helper 自動 backfill `strings.TrimPrefix(purdexName, "Pdx")` 仍可編譯且行為正確（拿到 pre-W2 字面值）。**這是 dev-time transition aid 而非 user-facing fallback**：

- main daemon binary 在 Phase 1/2 期間 build 出來時，cc 部分用新 catalog（DeriveStatus case=`PdxXxx`），codex/opencode 部分用舊 catalog（DeriveStatus case=`Xxx`）— 兩條 codepath 都對自家 hook payload 自洽，**無 cross-agent fallback / two-step lookup 行為**
- bump 之前 user 永遠跑 pre-W2 daemon binary，所以 main 的這個 dev-time 中間態完全不暴露給 user
- Phase 3 ship 同 PR 移除 `Name` + helper 雙寫，所有 stale reference 在編譯期被抓出

---

## 4. 端到端資料流（after W2）

以 cc UserPromptSubmit 為例：

```
[cc CLI 端 hook 觸發]
   cc CLI fire event "UserPromptSubmit"（UpstreamKey）
   ↓ hooks 機制查 ~/.claude/settings.json
   match key "UserPromptSubmit"（UpstreamKey）
   ↓ 執行 command
   `pdx hook --agent cc PdxUserPromptSubmit`（PurdexName 進 stdin/argv）
   ↓
[CLI: cmd/pdx/hook.go runHook]
   positional[0] = "PdxUserPromptSubmit"（PurdexName）
   eventName := "PdxUserPromptSubmit"
   ↓ POST /api/agent/event
   {"purdex_name": "PdxUserPromptSubmit", "raw_event": {...}, "agent_type": "cc", ...}
   ↓
[Daemon: internal/module/agent/handler.go handleEvent]
   req.PurdexName = "PdxUserPromptSubmit"
   ↓ provider.DeriveStatus("PdxUserPromptSubmit", req.RawEvent)
[internal/agent/cc/status.go deriveCCStatus]
   switch eventName { case "PdxUserPromptSubmit": ... }
   → DeriveResult{Valid: true, Status: StatusRunning}
   ↓
[Handler 回 200 + projection / broadcast]
   NormalizedEvent.RawEventName = "PdxUserPromptSubmit"
   trace.Apply event_name = "PdxUserPromptSubmit"
   WS broadcast → SPA（SPA 視為 opaque 字串）
```

### 4.1 EventRequest JSON 欄位 transition

EventRequest 是 daemon 內部 endpoint，**只有同版本 daemon 自帶的 `pdx hook` CLI 會送**（user 不會跨版本混用）。但因為 main 上 Phase 1/2 期間 codex/opencode 走「`spec.Name` backfill 路徑」生成 catalog literal，CLI 從 hooks file 讀到的字串仍是舊 `Name`（如 `"UserPromptSubmit"`，不含 Pdx prefix），所以 daemon 端在 Phase 1/2 期間需要對舊版 catalog 字串也合法接受 — 但**前提是該字串對應 agent 自家 catalog 的 PurdexName**（per §3.4 雙寫），不是跨 agent 混用。

JSON tag 設計：

- **Primary tag**：`purdex_name`（Phase 1 起）
- **Unmarshal-only alias**：`event_name`（custom UnmarshalJSON 接受兩鍵，`purdex_name` 優先；marshal 永遠輸出 `purdex_name`）
- **Phase 3 ship 同 PR 移除 alias**：unmarshal 只認 `purdex_name`

```go
// internal/module/agent/handler.go (Phase 1 ship 起，Phase 3 simplify)
type EventRequest struct {
    PurdexName string          `json:"purdex_name"`
    RawEvent   json.RawMessage `json:"raw_event"`
    AgentType  string          `json:"agent_type"`
    // ... 其他欄位
}

// Phase 1/2 transition only; Phase 3 ship 移除整個 Method
func (r *EventRequest) UnmarshalJSON(b []byte) error {
    type alias EventRequest
    var a struct {
        *alias
        EventNameAlias string `json:"event_name"`
    }
    a.alias = (*alias)(r)
    if err := json.Unmarshal(b, &a); err != nil { return err }
    if r.PurdexName == "" && a.EventNameAlias != "" {
        r.PurdexName = a.EventNameAlias
    }
    return nil
}
```

**為什麼是 alias 不是兩個獨立欄位**：避免 `pdx hook` CLI 送哪個鍵的 ambiguity；marshal 永遠單鍵輸出，alias 只在 unmarshal 期間 absorb 舊鍵。

**alias 期間的 PurdexName 字串值**：handler 收到的字串值由 catalog 決定（cc Phase 1 後是 `PdxXxx`、codex Phase 2 前還是 `Xxx`、opencode Phase 3 前還是 `Xxx`）。daemon 把這個字串原封不動 forward 到對應 agent 的 DeriveStatus；後者根據自家 phase 進度匹配 case label。**daemon 端不做跨 agent 字串轉換 / 沒有 fallback 比對**。

---

## 5. 三家差異處理（精要表）

| 議題 | cc | codex | opencode |
|---|---|---|---|
| UpstreamKeys 元素數 | 1 | 1 | 1-2（PdxPermissionRequest 為 2）|
| Catalog 邊界 | settings.json hooks key | hooks.json matcher-group key | plugin Bus event listener case |
| 命令字串生成位置 | `cc/hooks.go:makePdxEntry` | `codex/hooks.go:mergeCodexHooksFile` | `opencode/plugin_template.go` emit() |
| DRY 修補 | `ccOwnedCleanupEventNames` 改自動 | `codexOwnedCleanupEventNames` 改自動 | （opencode 無此函式）|
| Filter 條件處理 | 無 filter | 無 filter | plugin 端 type / tool filter（**不入 catalog**）|
| Magic marker | N/A | N/A | `pdx-managed:opencode-hooks:v1`（**不升版**）|
| Reinstall 影響 | 必要 | 必要 | 必要 |

---

## 6. 測試策略

### 6.1 單元測試（per phase）

每 phase 至少新增以下 assertion：

1. **catalog 一致性**：`PurdexName != ""` 且以 `Pdx` 開頭；`UpstreamKeys` 對 installable entry 至少一個元素
2. **Name vs PurdexName 互斥**：所有 entry 的 PurdexName 不在自己的 UpstreamKeys 列表內（否則 schema 退化）
3. **DRY 衍生**：`ccKnownEventNames()` / `codexOwnedCleanupEventNames()` 結果 == `Filter(catalog, IsInstallable)` 的 UpstreamKeys union
4. **lookup helpers**：`LookupByPurdexName("PdxSessionStart")` 能找到；`LookupByUpstreamKey("session.created")`（opencode）能找到 `PdxSessionStart`

### 6.2 整合測試（installer）

每 phase 新增「reinstall 後檔案內容」斷言：

- cc：parse `~/.claude/settings.json` 後 `hooks["SessionStart"][0].hooks[0].command` 字串末段 == `"PdxSessionStart"`
- codex：parse `~/.codex/hooks.json` 後 matcher group `"SessionStart"` 的 command 同上
- opencode：renderManagedPlugin 輸出檢查 `emit('PdxSessionStart'`（或常數引用形態）出現 1 次、`emit('SessionStart'`（無 Pdx）不再出現

### 6.3 Handler 行為測試（per agent_type，**反映 phase 進度**）

`internal/module/agent/handler_test.go`：每筆 case 必帶 `agent_type` 才能對 catalog literal 的 phase 狀態自洽斷言。

**Phase 1 ship 後（cc 已遷、codex / opencode 未遷）**：

| Test case | agent_type | purdex_name | 預期 |
|---|---|---|---|
| cc 新格式 primary tag | `cc` | `PdxUserPromptSubmit` | 200 + status=running |
| cc 舊格式（pre-W2 字面值） | `cc` | `UserPromptSubmit` | invalid + `event_not_in_catalog` |
| cc unmarshal alias `event_name` | `cc` | (`event_name="PdxUserPromptSubmit"`) | 200 + status=running |
| codex 中間態仍合法（main only） | `codex` | `UserPromptSubmit` | 200 + status=running（codex catalog literal Phase 1 還是 `Name`）|
| codex 提早送 PdxXxx（誤送） | `codex` | `PdxUserPromptSubmit` | invalid + `event_not_in_catalog`（catalog 還沒認）|
| opencode 中間態仍合法 | `opencode` | `UserPromptSubmit` | 200 + status=running |

**Phase 2 ship 後（cc + codex 已遷、opencode 未遷）**：

| Test case | agent_type | purdex_name | 預期 |
|---|---|---|---|
| codex 新格式 | `codex` | `PdxUserPromptSubmit` | 200 + status=running |
| codex 舊格式 | `codex` | `UserPromptSubmit` | invalid |
| opencode 中間態仍合法 | `opencode` | `UserPromptSubmit` | 200 + status=running |

**Phase 3 ship 後（三家全遷）**：

| Test case | agent_type | purdex_name | 預期 |
|---|---|---|---|
| opencode 新格式 | `opencode` | `PdxUserPromptSubmit` | 200 + status=running |
| opencode 舊格式 | `opencode` | `UserPromptSubmit` | invalid |
| event_name alias 已移除 | any | (`event_name="PdxUserPromptSubmit"`) | invalid（alias 不再被 unmarshal） |

每個 phase 對應 PR 在 `handler_test.go` 增測該 phase 後的合法 / 非法 case；同步 phase ship 時舊 phase 的「中間態仍合法」case 升為 invalid。

### 6.4 SPA 端

NormalizedEvent.RawEventName 已是 opaque 字串，SPA 端應**無邏輯改動**。但既有 fixture / snapshot test 內若硬寫 `"SessionStart"` 等字串，需更新為 `"PdxSessionStart"`。掃描清單：

```bash
rg -l "RawEventName|raw_event_name|event_name" spa/src spa/test
```

Phase 1 起即更新（影響 cc 相關 snapshot）。Phase 2/3 順帶更新各自家 fixture。

---

## 7. 風險與收斂

### 7.1 五大 Bloat 自我檢查（per `feedback_skeleton_convergence`）

| 徵兆 | W2 是否觸發 | 自評 |
|---|---|---|
| 把 working code 變 data | ❌ | 雙欄位是 input/output 邊界區分，不是 working code → data |
| Parallel registry | ❌ | 仍單一 catalog `events.go`，無平行對映表；plugin 內 dispatch 表是現有結構，本次只改 RHS string source |
| 統一抽象（generic framework） | ❌ | 三家各自 events.go；helper 只 2 個 lookup function |
| Refactor working code without functional reason | ⚠️ 中 | rename 是有 functional reason（解耦 + 命名標識），但屬「non-functional cleanup that touches a lot of files」— 用 phase 切分 + helper transition 收斂 |
| Config flag | ❌ | 無新增 flag |

### 7.2 主要風險

| Risk | Mitigation |
|---|---|
| Phase 1/2 main 中間態誤 bump → user 升到 dev-only 中間態 daemon | §0.1 + §8 + §9 多處明示「三 phase 全 merge 後才出 bump PR」；PR-W2-bump 排在 PR-W2-3 後 |
| Phase 1/2 transition 期間 `Name` 與 `PurdexName` 雙欄位漂移 | exported `agent.NewSpec(...)` helper 強制兩欄位同源；catalog literal 不直接寫 `Name` 欄位；Phase 3 ship 同 PR 移除 helper 確保 stale ref 編譯期被抓 |
| 既有 SPA fixture / snapshot 大量需更新 | spec §6.4 一次性 grep 清單；每 phase 改自家 agent 對應 fixture |
| EventRequest JSON `event_name` alias 期內潛伏 bug | 整合 test 同時測新舊 JSON；alias 在 Phase 3 同 PR 移除 |
| `LookupByUpstreamKey` 被誤用於 opencode filter events 的 routing | helper godoc 明文限制（§2.1）+ §2.5 不得作為 plugin filter routing 的 SOT；如果未來真的需要表達 filter，另開 issue 設計 `UpstreamFilter` metadata（不在 W2 範圍）|
| codex `codexOwnedCleanupEventNames` DRY 修補意外破壞 lifecycle | 修補同 PR 加 round-trip test：install → catalog 全 entry 都被 cleanup recognise；catalog 增刪 entry 時 cleanup 自動跟上 |
| opencode plugin emit 改常數注入後 JS template 解析錯誤 | renderManagedPlugin 新增 unit test：模板渲染後 `Bun.spawn` 呼叫處字串為 PurdexName；磁碟寫入後 grep 反查 |
| reinstall 對 user 環境破壞性 | alpha 階段允許（per `feedback_no_alpha_migration`）；ship + bump 同 commit chain，user 升 alpha 時 reinstall |
| 並發 session（per `feedback_concurrent_session_safety`）| 三 phase 各自 PR；每次 enter worktree 前 base 來自 origin/main（per `feedback_bump_base_origin_not_local`）|

### 7.3 跑偏防線（per `feedback_phase_skip_threshold`）

W2 範圍嚴格止於：

1. catalog 雙欄位 + helper
2. installer / plugin 寫入邊界改寫
3. handler / DeriveStatus 內部 rename
4. 測試與 fixture 對齊

**禁止越界做**：

- ❌ ProbeIntent / ProbeIntentProvider interface（W6 範圍）
- ❌ 移除 `manageActivityWatch` always-on policy（W3 範圍）
- ❌ TraceStore 新增 step / dev log 補完（W4 範圍）
- ❌ 任何 status mapping 邏輯改動（DeriveStatus body 不動，僅 case label rename）
- ❌ 跨 agent 抽 generic event interface（仍維持三家獨立 events.go）

任一冒出 → 停手 surface（per fix-spec §7）。

---

## 8. Post-ship 動作

**Bump 時機（per §0.1 no-migration 原則）**：Phase 1 / 2 / 3 三個 PR **全部 merge 完才出單一 bump PR**。Phase 1 / 2 merged 期間 main daemon binary 處於 dev-time 中間態，**不 bump alpha**，user 不會升級到中間態 daemon。Phase 3 merge 後 main daemon binary 是完整 W2 形態，bump alpha → user 升級 + 一次性 reinstall。

三 phase 全 ship + bump alpha 後（單一 bump PR）：

1. **mlab 主機跑 reinstall**
   ```
   pdx install --reinstall
   ```
2. **逐檔檢查命名對齊**
   - `~/.claude/settings.json`：`hooks.SessionStart[0].hooks[0].command` 末段 token == `PdxSessionStart`
   - `~/.codex/hooks.json`：`hooks.SessionStart[0].command` 同上
   - `~/.config/opencode/plugins/pdx-agent-hooks.js`：grep `emit('PdxSessionStart'` 命中 8 處（或 PURDEX_EVENT 引用）；`emit('SessionStart'`（無 Pdx）零命中
3. **重啟 daemon + agents 並觀察**：
   - 隨意觸發 `UserPromptSubmit` 事件（任一 agent）
   - 檢查 daemon log（`PDX_DEV_MODE=1` 開啟下）顯示 `event_name=PdxUserPromptSubmit`
   - SPA lights 行為與 pre-W2 一致（無誤判 / 無漏發）

若任何步驟失敗 → 立即開 issue 並（必要時）roll back bump PR。

---

## 9. PR 拆分與 review 節奏

| PR | 範圍 | 前置依賴 | 預估行數 | Codex review |
|---|---|---|---|---|
| **PR-W2-1** | Phase 1（共用 schema + `agent.NewSpec` helper + cc + handler 含 unmarshal alias）| origin/main | ~600-900 | 兩輪：標準 + 防守視角（spec alignment 防線，per `feedback_codex_pr_review_spec_alignment`）|
| **PR-W2-2** | Phase 2（codex catalog/installer/status + DRY 修補 codexOwnedCleanupEventNames）| PR-W2-1 merged | ~400-600 | 兩輪：標準 + 攻擊視角（race / 邊界）|
| **PR-W2-3** | Phase 3（opencode plugin template + 移除 `Name` 欄位 / `agent.NewSpec` 雙寫 / `event_name` JSON alias）| PR-W2-2 merged | ~300-500 | 兩輪：標準 + 體質視角（plugin template 可讀性）|
| **PR-W2-bump** | VERSION + CHANGELOG | PR-W2-3 merged | <50 | 不需 codex |

**順序強制理由**：

- PR-W2-2 依賴 PR-W2-1 提供的 schema 與 `agent.NewSpec` helper
- PR-W2-3 cleanup step（移除 `Name` 欄位）依賴 PR-W2-2 已遷（否則 codex package 編譯失敗）
- PR-W2-bump 只在三 phase 全 merge 後才出，避免 user 升級到中間態 daemon（per §0.1 no-migration 原則）

每 PR 兩輪 review 沿用 CLAUDE.md PR Review 兩輪制。

---

## 10. 文獻

- 上層 spec：`docs/specs/2026-04-23-lights-rebuild-spec.md`
- Fix-spec：`docs/specs/2026-04-28-lights-rebuild-fix-spec.md`（§1 架構基準圖、§2 W2 工作項、§4 PR-3 規劃、§10 dev spec 路徑）
- W1 audit doc：`docs/specs/2026-04-28-hook-status-audit-spec.md`（§9 W2 命名 disclaimer）
- 既有 catalog：
  - `internal/agent/cc/events.go`
  - `internal/agent/codex/events.go`
  - `internal/agent/opencode/events.go`
  - 共用 `internal/agent/provider.go` `HookEventSpec`
- DeriveStatus：
  - `internal/agent/cc/status.go`
  - `internal/agent/codex/status.go`
  - `internal/agent/opencode/status.go`
- Installer / plugin：
  - `internal/agent/cc/hooks.go`
  - `internal/agent/codex/hooks.go`
  - `internal/agent/opencode/{hooks,plugin_template}.go`
- Handler / CLI：
  - `cmd/pdx/hook.go`
  - `internal/module/agent/handler.go`
- Memory：
  - `feedback_no_alpha_migration.md`
  - `feedback_skeleton_convergence.md`
  - `feedback_phase_skip_threshold.md`
  - `feedback_codex_pr_review_spec_alignment.md`
  - `feedback_concurrent_session_safety.md`
  - `feedback_bump_base_origin_not_local.md`

---

## 11. Open questions（spec review 期間填補）

留空待 codex review 填入。
