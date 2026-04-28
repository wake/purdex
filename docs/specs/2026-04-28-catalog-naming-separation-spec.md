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

- daemon **不接受 user-facing 的兩種 hook payload 形態並存**（no user-state fallback / no two-step lookup based on user version mismatch）
- user 升級 alpha 後**必須**跑 `pdx install --reinstall` 才能繼續用，否則該 agent 的 hook payload 會被當成 unknown event
- 中間態風險（main 已進 Phase 1 但 user 還沒升）靠**延後 bump** 規避：三 phase **全 merge 完**才出單一 bump PR，bump 前 main 只是 dev-time work-in-progress，user 端永遠跑 pre-W2 daemon + 舊 hooks
- 本 spec 中 `Name` 欄位的雙寫 transition（§3.4）+ Phase 1/2 期間 daemon lifecycle 的 legacy-name fallback path（§3.4.2）是 **main-branch dev-time aid**，純粹給 phase 切分期間 codex/opencode 的 catalog literal 與 daemon binary 過渡用，**不對 user 暴露**：bump 之前 user 看不到 main 的中間態，所以這些 dev-time fallback 不構成 user-facing migration。Phase 3 ship 同 PR 全部移除

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
| 共用層 schema | `internal/agent/provider.go` 加 `PurdexName` + `UpstreamKeys []string` + `Lifecycle LifecycleEventKind` 三欄位；`Name` 欄位**保留為 deprecated dev-time backfill**，Phase 3 ship 移除（per §3.4）|
| 共用層 lifecycle | `internal/agent/lifecycle.go`（新檔）定義 `LifecycleEventKind` 列舉（per §2.6）|
| cc | `internal/agent/cc/{events,hooks,status}.go` 三檔；catalog entry plain struct literal 列全欄位 + installer 改寫 + DeriveStatus case rename |
| codex | `internal/agent/codex/{events,hooks,status}.go` 三檔；同 cc + DRY 修補 `codexOwnedCleanupEventNames` |
| opencode | `internal/agent/opencode/{events,hooks,plugin_template,status}.go` 四檔；plugin emit RHS 改 Go 端常數注入 |
| CLI | `cmd/pdx/hook.go` positional arg 語義升級（值轉變：UpstreamKey → PurdexName） |
| Handler | `internal/module/agent/handler.go` `EventRequest` 欄位語義升級；JSON tag 更新；NormalizedEvent.RawEventName 語意保持 |
| Daemon lifecycle 改造 | `internal/module/agent/handler.go` + `frame_ops.go` 對 raw event-name 的字面值比對改為 `LookupByPurdexName(...).Lifecycle == LifecycleXxx`（per §2.3.1）；Phase 1 起加 fallback path，Phase 3 ship 同 PR 移除 fallback |
| Tests | 三家 hooks_test / status_test / 共用 events_test；新增「installer key vs command arg 不同字串」+ catalog metadata invariant 斷言 + handler per-agent_type per-phase 行為矩陣（§6.3）+ frame lifecycle per-phase 行為斷言（§6.3.1）|
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

1. ✅ `HookEventSpec` 加 `PurdexName` + `UpstreamKeys` + `Lifecycle` 三欄位；`Name` 在 Phase 3 ship 同 PR 移除
2. ✅ 三家 catalog 全 rename 為 `Pdx` prefix；`UpstreamKeys` 正確填入；`Lifecycle` 對 frame-mutating entry 正確填值
3. ✅ 三家 installer / plugin 寫入 hooks file 後 key=UpstreamKey、command(or plugin emit)=PurdexName
4. ✅ Daemon handler / DeriveStatus / frame_ops lifecycle 比對全用 catalog metadata（無硬編 event-name 字面值）
5. ✅ Go test (`go test ./...`) 全綠
6. ✅ SPA 邊跑 vitest + lint + build 全綠（NormalizedEvent.RawEventName 字面值改變，需更新 fixture）
7. ✅ 三 phase PR 依序 ship（PR-W2-1 → PR-W2-2 → PR-W2-3），全 merged 後**才**出單一 PR-W2-bump
8. ✅ Post-ship 在 mlab 主機跑 `pdx install --reinstall` 並 ad-hoc 檢查三個 hook 檔案命名對齊
9. ✅ Spec 過 codex review 兩輪以上收斂（per CLAUDE.md PR Review 兩輪制）

---

## 2. 設計核心

### 2.1 三欄位 schema 擴充

```go
// internal/agent/provider.go
type HookEventSpec struct {
    // Name is the legacy raw upstream event identifier. Kept during W2
    // transition to allow Phase 1/2 main-branch builds where codex/opencode
    // catalogs have not yet migrated. Removed in Phase 3 ship together with
    // any remaining backfill literals.
    //
    // Deprecated: use PurdexName for daemon-internal matching, UpstreamKeys for
    // installer/plugin boundary writes.
    Name string

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

    // Lifecycle classifies the daemon-internal side effect kind for this
    // catalog entry. Used by frame_ops / handler so that lifecycle handling
    // (frame reset, subagent membership, frame delete, error guard whitelist)
    // can be done via catalog metadata lookup instead of hardcoded event-name
    // string comparison. See §2.6 for the value table.
    Lifecycle LifecycleEventKind

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

### 2.2.1 Catalog literal 風格 — plain struct literal（不用 builder helper）

**Phase 1 起每個 catalog literal 完整列出全欄位**（含 transition 期間的 `Name` backfill）：

```go
// internal/agent/cc/events.go (Phase 1 起)
var ccEventSpecs = []agent.HookEventSpec{
    {
        Name:         "SessionStart",                    // dev-time backfill (§3.4)
        PurdexName:   "PdxSessionStart",
        UpstreamKeys: []string{"SessionStart"},
        Lifecycle:    agent.LifecycleSessionStart,
        EmitsStatus:  []agent.Status{agent.StatusIdle},
        Description:  "Session has started",
        Handling:     agent.HookHandlingStatus,
    },
    {
        Name:         "UserPromptSubmit",
        PurdexName:   "PdxUserPromptSubmit",
        UpstreamKeys: []string{"UserPromptSubmit"},
        Lifecycle:    agent.LifecycleUserPromptSubmit,
        EmitsStatus:  []agent.Status{agent.StatusRunning},
        Description:  "User submitted a prompt",
        Handling:     agent.HookHandlingStatus,
    },
    // ...
}
```

**為什麼不用 builder helper（如 `agent.NewSpec(...)`）**：

- builder 強制限定 「填哪些參數」 → 容易誤把既有欄位（`EmitsStatus` / `Description` / `FutureOnly` / `Handling`）漏掉，phase 1 ship 時 catalog 變 zero-valued 副欄位，連帶破壞 SupportedStatuses derivation / installer Handling / Inspector descriptions
- plain struct literal 強制 reviewer 看見每欄位的具體值，phase 3 移除 `Name:` 行時 Go 編譯器抓所有 stale references，零漏
- 防漂移責任改交給 unit test：`event_spec_test.go` 校驗每筆 entry 的「PurdexName 以 `Pdx` 開頭」「`Name == strings.TrimPrefix(PurdexName, "Pdx")`」「`UpstreamKeys` 對 installable entry 至少一元素」「`PurdexName ∉ UpstreamKeys`」（§6.1）

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

### 2.3.1 Lifecycle 欄位填值（三家對齊）

每個 catalog entry 必填 `Lifecycle` 欄位，daemon 端 frame_ops / handler 用此 metadata 判斷 lifecycle 處理路徑（取代 hardcoded event-name 字面值比對）。

| PurdexName（三家共通）| LifecycleEventKind | daemon 副作用 |
|---|---|---|
| `PdxSessionStart` | `LifecycleSessionStart` | frame reset + 清 subagents（`handler.go:301-305`）|
| `PdxUserPromptSubmit` | `LifecycleUserPromptSubmit` | error guard 白名單（`handler.go:181`）— 任何狀態下都可清回 running |
| `PdxStop` | `LifecycleStop` | error guard 白名單（cc/codex；opencode 排除：`handler.go:187-189`）|
| `PdxStopFailure` | `LifecycleStopFailure` | error transition |
| `PdxNotification` | `LifecycleNone` | 純 status emit（waiting / idle）由 DeriveStatus 決定，無 frame 副作用 |
| `PdxPermissionRequest` | `LifecycleNone` | 同上（純 waiting）|
| `PdxSessionEnd` | `LifecycleSessionEnd` | frame delete + 清 currentStatus / subagents（`handler.go:278-285`）+ error guard 白名單 |
| `PdxSubagentStart` | `LifecycleSubagentStart` | 加入 frame.Subagents membership（`frame_ops.go:133-174`）|
| `PdxSubagentStop` | `LifecycleSubagentStop` | 從 frame.Subagents membership 移除 |

**Lifecycle vs Handling 維度區分**：

- `Handling`：catalog entry 是不是要 install / 處理（`status` / `detail` / `ignored` / `unsupported`）
- `Lifecycle`：daemon 收到後對 frame / subagent / error guard 的副作用 kind

兩個正交。`Notification` / `PermissionRequest` 雖 `Handling=status`，但 `Lifecycle=None`（不動 frame，僅推 status）。

**daemon 端比對改寫**（W2 範圍內必須完成）：

| 原硬編字串比對 | 改為 catalog metadata lookup |
|---|---|
| `req.EventName == "SessionStart"`（handler.go:301）| `LookupByPurdexName(req.PurdexName).Lifecycle == LifecycleSessionStart` |
| `req.EventName == "SessionEnd"`（handler.go:278）| `... == LifecycleSessionEnd` |
| `req.EventName == "Stop"`（handler.go:188）| `... == LifecycleStop` |
| `req.EventName == "UserPromptSubmit"`（error guard）| `... == LifecycleUserPromptSubmit` |
| `req.EventName == "SubagentStart"`（frame_ops）| `... == LifecycleSubagentStart` |
| `req.EventName == "SubagentStop"`（frame_ops）| `... == LifecycleSubagentStop` |

`req.AgentType != "opencode"` 這條 opencode-specific Stop guard（handler.go:187-189）**保留** — 那是 per-agent 行為差異，不是字面值比對問題。

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
| **frame_ops / handler lifecycle 比對** | **catalog `Lifecycle` 欄位（不是字面值）** | `handler.go:181/186/188/278-285/301-305`、`frame_ops.go:133-174`（per §2.3.1）|

### 2.5 反查需求

僅 opencode plugin 反查使用：plugin 收到 Bus event name → 找對應 PurdexName 來 emit。

實作上 **不依賴 daemon Go 端 lookup**：

- plugin 是 **JS code template**，writeManagedPlugin 階段把映射表硬編進 plugin（dispatch table 從 Go events.go 編譯期讀取後 stringify 嵌入）
- 因此 reverse lookup 在 plugin 內是 O(1) switch case，不需 runtime 反查
- Go 端的 `LookupByUpstreamKey` helper 只供測試斷言與將來其他用途

### 2.6 LifecycleEventKind 列舉

```go
// internal/agent/lifecycle.go (新增檔)
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

// String() implementation for trace / log 可讀性
func (k LifecycleEventKind) String() string { ... }
```

每個 catalog entry 透過 `Lifecycle` 欄位標識其 lifecycle kind（per §2.3.1 表）。daemon 端 lifecycle 處理改為 metadata-driven：

```go
// internal/module/agent/handler.go (Phase 1 ship 起)
spec, ok := provider.LookupByPurdexName(req.PurdexName)
if !ok {
    // catalog miss 路徑（不變）
}

switch spec.Lifecycle {
case agent.LifecycleSessionStart:
    // 清 subagents / frame reset
case agent.LifecycleSessionEnd:
    // 刪 frame / 清 currentStatus
case agent.LifecycleSubagentStart, agent.LifecycleSubagentStop:
    // frame.Subagents membership 變動
case agent.LifecycleStop:
    if req.AgentType != "opencode" { /* error guard 白名單 */ }
case agent.LifecycleUserPromptSubmit:
    // error guard 白名單
case agent.LifecycleStopFailure:
    // error transition
case agent.LifecycleNone:
    // status emit only
}
```

**替代方案考量**（記錄一下為何不選）：

- **B. `LifecycleClassifier` interface（每家 provider 自實作 IsSessionStart 等）**：可行但引入新 interface，落入「generic framework」嫌疑（per `feedback_skeleton_convergence`）。本案三家 PurdexName 完全相同（都是 PdxXxx），catalog metadata 自報已足，不需 dispatch interface
- **A. 撤回 phase 切分一次大 PR**：違反先前共識；diff 太大不易 review

選定**方案 D（catalog `Lifecycle` 欄位）**：catalog 結構強化，與既有 `Handling HookHandling` 欄位同性質，無新 interface，daemon 完全 PurdexName-aware（per fix-spec §1）。

---

## 3. 三 phase 切分

### Phase 1（M-L）— 共用 schema + cc 端到端 + lifecycle 改造

**範圍**：

Schema / 共用層：

- `internal/agent/provider.go`：新增 `PurdexName` / `UpstreamKeys []string` / `Lifecycle LifecycleEventKind` 三個欄位；`Name` 欄位標 `// Deprecated:`（dev-time backfill）；新增 `LookupByPurdexName` / `LookupByUpstreamKey` helpers
- `internal/agent/lifecycle.go`（新檔）：`LifecycleEventKind` 列舉與 String()
- `internal/agent/event_spec_test.go`（新檔）：catalog 校驗測試 per-agent / per-phase（per §6.1）— Phase 1 對 cc 套用 invariants 1-7；對 codex / opencode 反向斷言仍 legacy shape（`PurdexName == "" / UpstreamKeys == nil / Lifecycle == LifecycleNone`），防意外提早 partial migrate

cc 端：

- `internal/agent/cc/events.go`：catalog entry 改 plain struct literal 列全欄位（per §2.2.1）— `Name` / `PurdexName` / `UpstreamKeys` / `Lifecycle` / 既有 `EmitsStatus` / `Description` / `FutureOnly` / `Handling` 全填
- `internal/agent/cc/hooks.go`：
  - `mergeClaudeHooks` 寫 settings.json：hooks map key 改用 `spec.UpstreamKeys[0]`（cc 單元素）
  - `makePdxEntry` command 字串末段 token 改用 `spec.PurdexName`
  - `ccKnownEventNames` / `ccOwnedCleanupEventNames` 從 catalog 自動衍生（DRY 修補）— `IsInstallableHookSpec` 篩選後取 `UpstreamKeys` union
- `internal/agent/cc/status.go`：`deriveCCStatus` switch case 改 `PdxXxx`
- `cc/hooks_test.go` / `cc/status_test.go`：installer 端「hooks key == UpstreamKey、command 末段 == PurdexName」+ DeriveStatus case 斷言

CLI / Handler：

- `cmd/pdx/hook.go`：positional[0] 變數名改 `purdexName`，buildHookPayload 第二參數命名同步
- `internal/module/agent/handler.go`：
  - `EventRequest.EventName` → `PurdexName`，primary JSON tag `purdex_name`
  - 加 custom UnmarshalJSON：unmarshal 接受 `purdex_name`（優先）+ `event_name`（alias），marshal 只送 `purdex_name`
  - 呼 `provider.DeriveStatus(req.PurdexName, req.RawEvent)`

Lifecycle 改造（W2 範圍內必做，per §2.3.1）：

- `internal/module/agent/handler.go:181/186/187-189/278-285/301-305`：所有硬編 event-name 字面值改為 `LookupByPurdexName(req.PurdexName).Lifecycle == LifecycleXxx`
- `internal/module/agent/frame_ops.go`：frame mutation 路徑同步改 lifecycle metadata-driven
- `req.AgentType != "opencode"` 這條 opencode-specific Stop guard **保留**（per-agent 行為差異不是字面值問題）
- 改寫後 daemon 對 cc Phase 1 後送 `PdxSessionEnd` / codex/opencode 中間態送 `SessionEnd`（catalog literal `Name=SessionEnd`、`Lifecycle=LifecycleSessionEnd`）兩者都正確 match → frame 處理一致

**測試**：

- `handler_test.go`：per agent_type per phase 行為矩陣（§6.3）— Phase 1 後 cc 新格式合法、舊格式 invalid；codex/opencode 中間態仍合法
- `frame_ops_test.go`：lifecycle 行為斷言 — `PdxSessionEnd` (cc) 與 `SessionEnd` (codex/opencode 中間態) 都應 frame deleted；`PdxSubagentStart` / `SubagentStart` 都應 subagents +1
- `go test ./internal/agent/...` / `go test ./internal/module/agent/...` / `go test ./cmd/pdx/...` 全綠
- 手動驗證：`go run ./cmd/pdx install --check`（cc 部分）顯示無漂移

**退場驗證**：

- 在 worktree 內跑 `go run ./cmd/pdx install --reinstall`（針對 mlab 主機 cc）後檢視 `~/.claude/settings.json`：`"hooks"` 下 key 應仍是 `"SessionStart"` 等 UpstreamKey；每個 entry 的 `command` 字串末段應改為 `PdxSessionStart`
- 啟動實機 cc session，跑 lifecycle 觸發（new prompt / SessionEnd / Subagent）— 透過 `PDX_DEV_MODE=1` log 確認 lifecycle metadata 命中

**PR 範圍**：~800-1100 行 diff（含 lifecycle 改造 + 測試）

---

### Phase 2（M）— codex 端到端

**範圍**：

- `internal/agent/codex/events.go`：catalog entry 改 plain struct literal 列全欄位（per §2.2.1）— `Name` / `PurdexName` / `UpstreamKeys` / `Lifecycle` + 既有 metadata 全填；含 11 entries（9 installable + 2 unsupported `PdxPreToolUse` / `PdxPostToolUse`）
- `internal/agent/codex/hooks.go`：
  - `mergeCodexHooksFile` matcher-group key=`spec.UpstreamKeys[0]`、command 末段 token=`spec.PurdexName`
  - `codexOwnedCleanupEventNames()` 從 catalog 自動衍生（DRY 修補）
  - `checkCodexEvent` 用 UpstreamKey 反查 hooks.json key
- `internal/agent/codex/status.go`：`deriveCodexStatus` switch case 改 `PdxXxx`
- `codex/hooks_test.go`：新增測試「matcher-group key == UpstreamKey 且 command == PurdexName」+「`codexOwnedCleanupEventNames()` 與 `codexEventSpecs` 同步」

**Lifecycle 改造**：daemon 端 lifecycle 比對於 Phase 1 已遷至 catalog metadata-driven，Phase 2 codex catalog literal 填上正確 `Lifecycle` 欄位後自動命中（per §2.3.1 對照表）。codex 不需在 daemon 層額外改動。

**驗證**：

- `~/.codex/hooks.json` reinstall 後 key 為 `"SessionStart"` 等，command 字串末段為 `PdxSessionStart`
- `~/.codex/config.toml` 的 `features.codex_hooks=true` 不變動（與 event name 無耦合）

**前置依賴**：Phase 1 ship（schema 已加三欄位 + handler 已用 PurdexName + lifecycle 已改 metadata-driven）

**並行可能**：與 Phase 3 可在 Phase 1 ship 後並行 catalog 改寫部分；但 Phase 3 內含「移除 `Name` 欄位 + 移除 `event_name` JSON alias」cleanup step **必須在 Phase 2 merged 後**才能進行（否則 codex package 仍引用 `spec.Name` 編譯失敗）

---

### Phase 3（S-M）— opencode plugin template + transition cleanup

**範圍**：

opencode 端：

- `internal/agent/opencode/events.go`：catalog 改 plain struct literal 列全欄位（per §2.2.1）— `Name` 不再需要（同 PR cleanup）/ `PurdexName` / `UpstreamKeys`（含多元素 `PdxPermissionRequest`）/ `Lifecycle` + 既有 metadata
- `internal/agent/opencode/plugin_template.go`：
  - 模板頭部加 `const PURDEX_EVENT = { PdxSessionStart: "PdxSessionStart", ... }` 物件，由 Go 端編譯期生成（`renderManagedPlugin` 內 string concat 從 catalog 衍生）
  - emit() 呼叫點 8 處 RHS 從硬寫 `'SessionStart'` 改為 `PURDEX_EVENT.PdxSessionStart`
  - case label（LHS）保持原樣（UpstreamKey）
  - 其他 plugin 邏輯（`suppressIdleForSession` / type filter）不動
- `internal/agent/opencode/hooks.go`：`renderManagedPlugin(pdxPath)` 簽章不變；內部加常數注入；magic marker 不升（per Q3）
- `internal/agent/opencode/status.go`：`deriveOpenCodeStatus` switch case 改 `PdxXxx`
- `opencode/hooks_test.go`：plugin template 渲染後驗證
  - 8 處 `emit('PdxXxx', ...)` 出現
  - 沒有殘留硬寫的 `emit('SessionStart', ...)` 形態
  - magic marker 仍為 `pdx-managed:opencode-hooks:v1`

**Transition cleanup（同 PR，per §3.4）**：

- 移除 `internal/agent/provider.go` 的 `Name` 欄位
- 移除 `EventRequest` custom UnmarshalJSON 的 `event_name` alias（簡化為單鍵 `purdex_name`）
- cc / codex / opencode 三家 `events.go` catalog literal 移除 `Name:` 行
- Go 編譯器掃出所有殘留 `spec.Name` 引用（理應為 0），逐一修正（如有遺漏）

**驗證**：

- `~/.config/opencode/plugins/pdx-agent-hooks.js` reinstall 後查看：
  - `case 'session.created':` 等 LHS 保留 UpstreamKey
  - `emit('PdxSessionStart', ...)` 等 RHS 是 PurdexName（或常數引用）
  - magic marker `pdx-managed:opencode-hooks:v1` 不變
- 啟動實機 opencode session，觀察 daemon log 收到的 EventRequest.PurdexName 為 `PdxXxx`

**前置依賴**：**Phase 2 ship**（Phase 3 cleanup 步驟移除的 `Name` 欄位若 Phase 2 還沒 merge，codex package 仍會引用，編譯失敗）；catalog 改寫部分可與 Phase 2 並行起草，但 PR ship 順序強制 Phase 2 → Phase 3

---

### 3.4 Dev-time transition 機制（**not** user-facing migration）

Phase 1 加 `PurdexName` / `UpstreamKeys` / `Lifecycle` 三個欄位後 `Name` 欄位**短暫並存於 main**（cc catalog 全填新欄位、codex/opencode catalog 仍引用舊 `Name`）。**這是 main-branch dev-time 雙寫，不是 user-facing migration** — bump 之前 user 永遠跑 pre-W2 daemon（per §0.1 「No user-facing migration」），看不到 main 的中間態。

| Phase（main-only） | provider.go HookEventSpec | cc | codex | opencode | daemon lifecycle 比對 |
|---|---|---|---|---|---|
| Pre-W2 | 只有 `Name` 等 | 用 Name | 用 Name | 用 Name | 字面值（"SessionStart" 等）|
| Phase 1 ship | 加 PurdexName / UpstreamKeys / Lifecycle；Name 標 Deprecated | catalog 全填四命名欄位；DeriveStatus case=PdxXxx | catalog literal 仍用 Name（其他三新欄位 zero-value，daemon 不直接 dispatch codex 經 PurdexName 路徑因為 codex DeriveStatus 仍 case=Name）| 同 codex | catalog `Lifecycle` metadata 取代字面值（per §2.3.1）— 但 codex/opencode catalog 還沒填 Lifecycle 所以 lifecycle 處理照常用 fallback 字面值（Phase 1 短暫 hybrid）|
| Phase 2 ship | 同上 | 已遷 | 全填四命名欄位 | 仍用 Name | 全 metadata-driven for cc + codex；opencode 仍 hybrid |
| Phase 3 ship | 移除 `Name` 欄位 + JSON alias | 已遷 | 已遷 | 全填四命名欄位 | 完全 metadata-driven |
| Bump alpha | 三 phase 全 merge 後 single bump PR | — | — | — | — |

#### 3.4.1 Catalog literal 雙寫策略

**用 plain struct literal，不用 builder helper**（per §2.2.1 + Round-2 G1 防漂移評估）：

```go
// internal/agent/cc/events.go (Phase 1 ship 起)
var ccEventSpecs = []agent.HookEventSpec{
    {
        Name:         "SessionStart",                      // dev-time backfill, removed Phase 3
        PurdexName:   "PdxSessionStart",
        UpstreamKeys: []string{"SessionStart"},
        Lifecycle:    agent.LifecycleSessionStart,
        EmitsStatus:  []agent.Status{agent.StatusIdle},
        Description:  "Session has started",
        Handling:     agent.HookHandlingStatus,
    },
    // ... 其他 8 entries
}

// internal/agent/codex/events.go (Phase 1 期間 — 仍 pre-W2 形態)
var codexEventSpecs = []agent.HookEventSpec{
    {
        Name:        "SessionStart",
        EmitsStatus: []agent.Status{agent.StatusIdle},
        Description: "Session has started",
        Handling:    agent.HookHandlingStatus,
        // PurdexName / UpstreamKeys / Lifecycle 留 zero-value，等 Phase 2 補
    },
    // ...
}
```

#### 3.4.2 Phase 1 lifecycle hybrid 策略 — 三分支 decision tree

Phase 1 ship 後 cc catalog 已填 `Lifecycle` 但 codex/opencode 還沒。daemon 端 lifecycle 比對改寫成清楚的三分支：

```go
// internal/module/agent/handler.go (Phase 1 ship 起，Phase 3 ship 同 PR 簡化)
spec, ok := provider.LookupByPurdexName(req.PurdexName)
switch {
case ok:
    // 已遷移 agent (Phase 1 後的 cc / Phase 2 後的 codex / Phase 3 後的 opencode)
    // catalog 命中 → 用 metadata 決定 lifecycle 行為
    switch spec.Lifecycle {
    case agent.LifecycleNone:
        // 合法的 no-frame-side-effect entry（PdxNotification / PdxPermissionRequest）
        // 純 status emit，由 DeriveStatus 處理；handler 端 lifecycle 處理 no-op
    default:
        // frame-mutating entry — 走 metadata-driven 處理
        handleLifecycleByKind(spec.Lifecycle, ...)
    }
case !ok && isLegacyHookForUnmigrated(req.AgentType, req.PurdexName):
    // dev-time fallback：catalog 尚未遷移的 agent (Phase 1 期間的 codex/opencode；Phase 2 期間的 opencode)
    // 透過字面值比對處理 lifecycle 副作用
    // Phase 3 ship 同 PR 整段移除（屆時三家全遷，分支永不命中）
    handleLifecycleByLegacyName(req.AgentType, req.PurdexName, ...)
default:
    // !ok 且不在 legacy 期間（Phase 3 後 / 已遷移 agent 送了不在 catalog 的字串）
    // → invalid + reason="event_not_in_catalog"
    invalidEvent("event_not_in_catalog")
}
```

`isLegacyHookForUnmigrated` 是 phase-aware predicate：

- Phase 1 期間：返回 true 當 `AgentType ∈ {codex, opencode}` 且 `PurdexName ∈ {SessionStart, UserPromptSubmit, Stop, StopFailure, Notification, PermissionRequest, SessionEnd, SubagentStart, SubagentStop}`（pre-W2 字面值）
- Phase 2 期間：codex 已遷，predicate 限縮 `AgentType == opencode`
- Phase 3 ship 同 PR：predicate + 整段 fallback case 移除

注意：fallback path 是**main-only dev-time** — bump 前 user 看不到。同 §0.1「No user-facing migration」原則：daemon 不對未升級 user 的 raw payload 做 fallback，僅對 main 上 catalog literal 的 phase 進度做 fallback。Phase 3 ship 同 PR 整段 fallback 移除。

**`LifecycleNone` ≠ fallback**：`LifecycleNone` 是合法 catalog 命中（如 `PdxNotification`），表示「該 entry 純 status emit、無 frame 副作用」，handler 不會走 fallback 而是 lifecycle no-op。對比之下「未遷移 agent 的 legacy event」走 `!ok` 分支命中 fallback。兩條路徑清楚分離。

#### 3.4.3 防漂移責任分配

無 builder helper，改靠 unit test：`event_spec_test.go` 校驗每筆 catalog entry 的：

- `PurdexName != ""` 且 `strings.HasPrefix(PurdexName, "Pdx")`
- `Name == strings.TrimPrefix(PurdexName, "Pdx")`（dev-time invariant，Phase 3 後測試移除）
- `UpstreamKeys` 對 `IsInstallableHookSpec` 的 entry 至少一元素
- `PurdexName ∉ UpstreamKeys`（防止退化單一字串）
- `Lifecycle` 對與「frame-mutating event」對應 entry 必填非 `LifecycleNone`（per §2.3.1 對照表）

Phase 3 ship 同 PR：

- 移除 `Name` 欄位 → Go 編譯器抓所有殘留 `spec.Name` 引用
- 移除 lifecycle fallback 路徑 → 行為集中於單一 metadata-driven codepath

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

### 6.1 單元測試（per agent / per phase）

invariants 拆 per-agent 套用，反映 phase 進度。`internal/agent/event_spec_test.go` 內三家分別測試（不對未遷移 agent 強制套新欄位斷言，避免 §3.4.1 中間態 fail）。

**對「已遷移」agent 套用以下 invariants**（Phase 1 對 cc / Phase 2 起對 cc+codex / Phase 3 起對三家）：

1. **catalog 一致性**：`PurdexName != ""` 且以 `Pdx` 開頭；`UpstreamKeys` 對 installable entry 至少一個元素
2. **Name dev-time 對應**：catalog literal 的 `Name == strings.TrimPrefix(PurdexName, "Pdx")`（Phase 1/2 期間有效，Phase 3 ship 同 PR 移除此測試）
3. **PurdexName 與 UpstreamKeys 互斥**：所有 entry 的 PurdexName 不在自己的 UpstreamKeys 列表內（否則 schema 退化成單一字串）
4. **既有 metadata 保留**：每 entry 的 `EmitsStatus` / `Description` / `FutureOnly` / `Handling` 與 pre-W2 時相同（plain struct literal 改寫不能誤丟欄位 — 對應 Round-2 G1 防漂移）
5. **Lifecycle 對齊**：`Lifecycle` 欄位對「frame-mutating」entry（SessionStart / SessionEnd / SubagentStart / SubagentStop / Stop / StopFailure / UserPromptSubmit）為對應的 `LifecycleXxx`；`Notification` / `PermissionRequest` 為 `LifecycleNone`（per §2.3.1）
6. **DRY 衍生**：`ccKnownEventNames()` / `codexOwnedCleanupEventNames()` 結果 == `Filter(catalog, IsInstallable)` 的 UpstreamKeys union
7. **lookup helpers**：`LookupByPurdexName("PdxSessionStart")` 能找到；`LookupByUpstreamKey("session.created")`（opencode）能找到 `PdxSessionStart`

**對「未遷移」agent 反向斷言**（防止意外提早 partial migrate）：

| Phase | 未遷移 agent | 反向斷言 |
|---|---|---|
| Phase 1 | codex / opencode | 每 entry 的 `PurdexName == ""` 且 `UpstreamKeys == nil` 且 `Lifecycle == LifecycleNone`（仍 legacy shape）|
| Phase 2 | opencode | 同上（codex 已升 invariants 1-7）|
| Phase 3 | — | 無未遷移 agent；反向斷言全移除；`Name` 欄位整體刪除 |

每 phase ship 同 PR 升級該 agent 從「反向斷言」移到「正向 invariants 1-7」；Phase 3 ship 同 PR 移除 invariant #2（`Name` 欄位本身已刪）。

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

### 6.3.1 Frame lifecycle 行為測試（per phase，**Round-2 G2 補入**）

`internal/module/agent/handler_test.go` / `frame_ops_test.go`：lifecycle 處理改 metadata-driven 後，需 per agent_type per phase 斷言 frame 副作用真有發生。

**Phase 1 ship 後（cc catalog 填 Lifecycle）**：

| Test case | agent_type | purdex_name | LookupByPurdexName | 預期 decision tree 分支 | frame 副作用 |
|---|---|---|---|---|---|
| cc SessionStart metadata-driven | `cc` | `PdxSessionStart` | ok, Lifecycle=SessionStart | metadata path | frame 重置 + subagents 清空 |
| cc SessionEnd metadata-driven | `cc` | `PdxSessionEnd` | ok, Lifecycle=SessionEnd | metadata path | frame deleted + currentStatus 清 |
| cc SubagentStart metadata | `cc` | `PdxSubagentStart` | ok, Lifecycle=SubagentStart | metadata path | frame.Subagents +1 |
| cc Notification 合法 no-op | `cc` | `PdxNotification` | ok, Lifecycle=None | metadata path / no-op lifecycle | 無 frame 副作用（純 status emit）|
| cc PermissionRequest 合法 no-op | `cc` | `PdxPermissionRequest` | ok, Lifecycle=None | metadata path / no-op lifecycle | 無 frame 副作用 |
| codex 中間態 SessionEnd fallback | `codex` | `SessionEnd` | miss | legacy fallback (predicate 通過) | frame deleted |
| opencode 中間態 SessionStart fallback | `opencode` | `SessionStart` | miss | legacy fallback (predicate 通過) | frame reset |
| codex 提早送 PdxXxx | `codex` | `PdxSessionEnd` | miss（codex catalog Phase 1 還沒填 PurdexName）| legacy predicate fail（不在 pre-W2 字面值集）→ invalid | 無副作用 + `event_not_in_catalog` |

**Phase 2 ship 後（cc + codex catalog 填 Lifecycle，opencode 仍 fallback）**：

| Test case | agent_type | purdex_name | 預期 |
|---|---|---|---|
| codex SessionEnd metadata-driven | `codex` | `PdxSessionEnd` | frame deleted (metadata 路徑) |
| opencode 中間態 | `opencode` | `SessionEnd` | frame deleted (fallback 路徑) |

**Phase 3 ship 後（三家全 metadata-driven，fallback 路徑移除）**：

| Test case | agent_type | purdex_name | 預期 |
|---|---|---|---|
| opencode metadata-driven | `opencode` | `PdxSessionEnd` | frame deleted (metadata 路徑) |
| 任一家送舊字面值 | any | `SessionEnd` | invalid + `event_not_in_catalog`（fallback 已移除）|

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
| plain struct literal 改寫誤丟既有 metadata 欄位（`EmitsStatus` / `Description` / `FutureOnly` / `Handling`）| §6.1 加固定 invariant 測試斷言每 entry 既有欄位值與 pre-W2 時相同；reviewer 與 codex review 重點檢查 catalog literal diff |
| Phase 1/2 期間 `Name` 與 `PurdexName` 漂移 | §6.1 測試斷言 `Name == TrimPrefix(PurdexName, "Pdx")` 強制同源；Phase 3 ship 同 PR 移除 `Name` 欄位讓編譯器抓所有 stale references |
| daemon lifecycle fallback 路徑（§3.4.2）在 Phase 1/2 期間誤跨 agent 命中 | fallback 只在 `!ok`（catalog literal 還沒填 PurdexName，因此 LookupByPurdexName miss）且 `isLegacyHookForUnmigrated` predicate 通過（`AgentType` 仍未遷 + `PurdexName` 在 pre-W2 字面值集合）時走；`Lifecycle == LifecycleNone` 是合法 catalog 命中（`PdxNotification` / `PdxPermissionRequest`）→ lifecycle no-op，**不**走 fallback。測試覆蓋三條獨立路徑：cc 已遷 metadata 命中 / codex/opencode 中間態 fallback / 任一 PurdexName=Pdx* 但 Lifecycle==None 走 no-op |
| 既有 SPA fixture / snapshot 大量需更新 | spec §6.4 一次性 grep 清單；每 phase 改自家 agent 對應 fixture |
| EventRequest JSON `event_name` alias 期內潛伏 bug | 整合 test 同時測新舊 JSON；alias 在 Phase 3 同 PR 移除 |
| `LookupByUpstreamKey` 被誤用於 opencode filter events 的 routing | helper godoc 明文限制（§2.1）+ §2.5 不得作為 plugin filter routing 的 SOT；如未來真要表達 filter，另開 issue 設計 `UpstreamFilter` metadata（不在 W2 範圍）|
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
   - `~/.config/opencode/plugins/pdx-agent-hooks.js`：8 個 Pdx events 各 1 處 emit（共 8 行 `emit('PdxXxx'` 或 `emit(PURDEX_EVENT.PdxXxx,` 形態）；無任何殘留 `emit('SessionStart'` / `emit('Stop'` / `emit('UserPromptSubmit'` 等不含 `Pdx` 前綴形態（零命中）
3. **重啟 daemon + agents 並觀察**：
   - 隨意觸發 `UserPromptSubmit` 事件（任一 agent）
   - 檢查 daemon log（`PDX_DEV_MODE=1` 開啟下）顯示 `event_name=PdxUserPromptSubmit`
   - SPA lights 行為與 pre-W2 一致（無誤判 / 無漏發）

若任何步驟失敗 → 立即開 issue 並（必要時）roll back bump PR。

---

## 9. PR 拆分與 review 節奏

| PR | 範圍 | 前置依賴 | 預估行數 | Codex review |
|---|---|---|---|---|
| **PR-W2-1** | Phase 1（共用 schema 三欄位 + lifecycle.go + plain struct literal cc catalog + cc installer/status + handler PurdexName + unmarshal alias + daemon lifecycle 三分支 decision tree）| origin/main | ~800-1100 | 兩輪：標準 + 防守視角（spec alignment 防線，per `feedback_codex_pr_review_spec_alignment`）|
| **PR-W2-2** | Phase 2（codex plain struct literal catalog + installer/status + DRY 修補 codexOwnedCleanupEventNames + isLegacyHookForUnmigrated predicate 移除 codex case）| PR-W2-1 merged | ~400-600 | 兩輪：標準 + 攻擊視角（race / 邊界）|
| **PR-W2-3** | Phase 3（opencode plain struct literal catalog + plugin template emit 改常數注入 + 移除 `Name` 欄位 + 移除 `event_name` JSON alias + 移除 lifecycle fallback 分支）| PR-W2-2 merged | ~400-600 | 兩輪：標準 + 體質視角（plugin template 可讀性 + cleanup 完整性）|
| **PR-W2-bump** | VERSION + CHANGELOG | PR-W2-3 merged | <50 | 不需 codex |

**順序強制理由**：

- PR-W2-2 依賴 PR-W2-1 提供的 schema（三欄位 + LifecycleEventKind enum + lookup helpers + daemon decision tree 三分支）
- PR-W2-3 cleanup step（移除 `Name` 欄位 + 移除 lifecycle fallback 分支 + 移除 JSON alias）依賴 PR-W2-2 已遷（否則 codex package 仍引用 `spec.Name` 編譯失敗 / `isLegacyHookForUnmigrated` predicate 仍命中 codex）
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
