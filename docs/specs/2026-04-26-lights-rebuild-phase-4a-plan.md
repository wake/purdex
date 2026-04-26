# Lights Rebuild — Phase 4a Plan v1.2 (PR-4a-0 OpenCode Hooks Completion)

**Status**: Draft v1.2（Round 2 convergence fixes — 2 high + 4 medium + 2 low + 1 nit applied）
**Date**: 2026-04-27
**Worktree**: `.claude/worktrees/lights-phase-4-audit`（branch `worktree-lights-phase-4-audit`）
**Baseline**: `origin/main @ 63168dd9` (`1.0.0-alpha.230`)
**Audit issue**: [#656 v5.2](https://github.com/wake/purdex/issues/656)
**Codex review trail (non-normative — see §11)**:
- Round 1: `task-mog1gsb0-0idiaa` (audit) + `task-mog1gtot-c79vs7` (plan) — 27 findings → v1.1 + v5.1 fixes
- Round 2: `task-mog24leu-l5lbd0` (audit) + `task-mog24m6r-ql5fdt` (plan) — 11 findings → v1.2 + v5.2 fixes

**Related specs**:
- `docs/specs/2026-04-23-lights-rebuild-spec.md` §2.4 / §8.1 / §71（原方向，audit 已記錄偏離）
- `docs/specs/2026-04-25-agent-hooks-hotfix-plan.md`（hooks-hotfix-plan，PR 1+2 已 ship；PR 3-6 收編進 PR-4a-0）
- `docs/specs/2026-04-25-lights-rebuild-phase-3-5-plan.md` v12（PR-3.5a）
- `docs/specs/2026-04-26-lights-rebuild-phase-3-5b-plan.md` v4（PR-3.5b）

---

## 0. 來龍去脈

Phase 3.5b 完工後（alpha.227）轉入 Phase 4a — **probe primitive rebuild + module-layer policy + graceWindow**。Audit issue #656 v5.2 記錄了：

1. **設計方向偏離原 spec §8.1**（字元偵測砍掉、probe layer 純 plumbing、policy 全分散到 agent module）
2. **三家 agent catalog 對齊度** — cc / codex 在 hooks-hotfix-plan PR #645 + #655 完成 `HookHandling` 四態完整 catalog；opencode 線**未完成**
3. **Phase 4a Slice 6（opencode module 接新 primitive）** 在 alpha.230 上**無法 e2e 驗證** — 因為 plugin template 用 `chat.message` / `session.idle` 等 key 是否仍為 1.14.x current API 未驗證

**方案 A 決議**（2026-04-27 主工作流）：將 hooks-hotfix-plan opencode 收尾工作收編為 Phase 4a 的 **PR-4a-0**。本 plan v1.2 聚焦 PR-4a-0 完整細節；PR-4a-1 / PR-4a-2 僅給大綱 + Slice 6 design-impact checkpoint。

**Directive 加碼項**（user 2026-04-27）：

> opencode 的 agent hook 因為尚未完全實作，建議**重新完整對齊驗證一次**，也同時確認**是否還需要使用 `chat.message`**。

→ PR-4a-0 必須對 `sst/opencode` v1.14.23 source 做完整 enumeration audit，覆蓋 5 個關鍵決策（`chat.message` / `permission.ask` strong hook / `session.idle` / `session.status` non-idle / `question.asked`），**且 audit 必須驗證每個 strong hook 的 trigger callsite**（interface 存在不等於 runtime emit — Round 2 AM1）。

---

## 1. Scope

### 1.1 PR-4a-0 In scope

1. **OpenCode 1.14.23 上游 hook 全清單對齊驗證**（§2.1）— 含 trigger callsite grep 硬要求
2. **opencode catalog 補完 ignored/unsupported 條目**（§2.2）— 對齊 cc/codex；含 bipartite naming + collision policy
3. **opencode SupportedVersion 報告**（§2.3）— 5 個 nil-error CheckHooks return path 全補
4. **opencode provenance fixtures**（§2.4）— `testdata/opencode-1.14.23-*`（manifest + events.json + payloads tree）
5. **opencode plugin template refresh — conditional**（§2.5）— 廣化 skip 條件 + partial-stale policy gate
6. **SPA agent icon coverage**（§2.6）

### 1.2 PR-4a-0 Out of scope

- 不動 `internal/module/agent/*`（屬 PR-4a-1/2）
- 不動 `internal/agent/probe/*`（屬 PR-4a-1）
- 不動 `internal/tmux/executor.go`（屬 PR-4a-1 Slice 0）
- 不動 cc / codex catalog（已在 PR #655 完成；任何 cc/codex 改動另開 issue）
- 不做 hooks-hotfix-plan PR 3「Remaining Claude Strictness」— 推論可 close 不做（cc 嚴格 checker + isPdxCommand_RequiresHookSubcommand 已在 PR #645 完成，CS1-CS9 測試齊全）
- 不改 `internal/agent/opencode/status.go`（status mapping 為 frame layer concern，不在 hook surface）
- 不動 `internal/store/*` / tab rendering / `spa/src/features/**` 非 icon test 的檔案

### 1.3 PR-4a-1 / PR-4a-2 大綱（不在本 plan v1.2 細部，待 PR-4a-0 對齊結果後起草下一版 plan）

詳見 §7。**注意**：§7.3 Slice 6 design-impact checkpoint 是 PR-4a-0 ship 前的 stop/go gate，含 evidence-based fail rule（Round 2 M4）。

---

## 2. 設計（PR-4a-0）

### 2.1 OpenCode 1.14.23 上游 hook 全清單對齊驗證

**產出**：`docs/specs/2026-04-26-opencode-1.14.23-hook-audit.md`（commit 1，純文件）+ `internal/agent/opencode/testdata/opencode-1.14.23-events.json`（commit 2 同步建立 — 見 §2.2 / §2.4 / Commit 2）

**驗證來源**（按優先序）：

1. `github.com/sst/opencode` repo @ tag `v1.14.23`（**權威**）— **全 repo grep 範圍**：
   - `BusEvent.define(` / `bus.publish(` / `Bus.publish(` 掃 `packages/opencode/src/**` 全樹（不只 `bus/*`）
   - `subscribe(` / `subscribeAll(` 掃 plugin layer
   - **`Plugin.trigger("<hookName>")` callsite grep**（new — Round 2 AM1）— strong hooks interface 存在 ≠ 真的 emit；audit 必須驗每個 strong hook 都有 trigger callsite
   - 必含模組：`packages/opencode/src/{session,permission,question,bus,tool,file,lsp,server,todo,shell,tui,chat,plugin}/**`
   - `packages/plugin/src/index.ts` — `Hooks` interface 全清單（strong hooks）
2. `https://opencode.ai/docs/plugins/`（公開 docs，作為對照；發現與 source 不一致時以 source 為準）
3. `opencode --version` runtime 確認

**對齊報告必含的章節**：

1. **Strong hooks full list (1.14.23)**
   - 每個 hook：name / signature / 是否仍在 1.14.x current API / **trigger callsite location（必須有，否則歸類 dead strong hook）**（Round 2 AM1）/ payload type / Purdex 用途（or 不用的 reason）
   - **`chat.message` 顯式判定**：
     - 是否仍在 `Hooks` interface？是否有 trigger callsite？
     - 若 interface + callsite 都 OK：保留現用 mapping `chat.message → UserPromptSubmit`
     - 若 interface 在但 callsite 無：標 `unsupported`，尋找替代候選
     - 替代候選：`chat.params` / Bus event `message.updated`/`message.part.updated` / 新版 `experimental.chat.messages.transform`
   - **`permission.ask` strong hook 顯式判定**（Round 1 AH2 + Round 2 AM1 加深）：
     - 必須驗 `Plugin.trigger("permission.ask")` callsite 是否存在
     - 若無 callsite：分類為 `unsupported`/dead strong hook，**不採用**
     - 若有 callsite：決策（切換 / 雙訂閱 / 保留現狀 Bus event）；採用時須同步：
       - plan §7.3 Waiting checkpoint 加 strong hook path
       - OC1 fixture 加 strong hook payload
       - template consumed-keys exact match 列入 strong hook
       - 上述未同步前**禁止採用** `permission.ask`
2. **Bus events full list (1.14.23)**
   - 從 §2.1 全 repo grep 結果 enumerate 所有 publish callsite
   - 每個 event：name / definition location / publish location / payload shape / Purdex 是否訂閱 / 不訂閱的 reason
   - **`session.idle` 顯式判定**：
     - 1.14.23 是否仍 emit？source 標記 deprecated 但行為仍存在？
     - vs `session.status({type:"idle"})` 取捨；若上游確認 deprecated 但 emit，是 partial-stale 案例（見 §8 Risk + §6 ship gate H6.4）
   - **`session.status` non-idle 顯式判定**：
     - `SessionStatus.Info` 含 `idle` / `retry` / `busy` 三種
     - `busy` 是否能 mapping 到 `Running`？`retry` 是否該 mapping 到 `Error` 或 ignored？
     - 決策：分類為 status / ignored / unsupported 並給原因
   - **`question.asked` 顯式判定**：
     - v1.14.23 source 直接 `bus.publish(Event.Asked, info)`
     - public docs 未列為 plugin event；屬「source-level 存在但 docs 未公開」案例
     - 決策：是否接受 undocumented Bus event 作為 supported mapping
3. **Catalog 對齊建議**
   - 從上游全清單中，列出 Purdex `events.go` 應補的 ignored / unsupported 條目（具體 Name 字串清單 + 對應 upstream key）
4. **Plugin template 影響**
   - 若 §2.1.1 / §2.1.2 任一判定要換鍵 / 加 filter / 改 payload path / 加 feature detection / 採用 `permission.ask` strong hook → 列出具體變更建議
   - 若全部判定為 stable + 無需 template 變更 → 顯式 declare「無需 §2.5 plugin template refresh」
5. **Slice 6 design-impact summary**（落實 §7.3 stop/go checkpoint 的依據）
   - 三個 trigger pattern 各自填寫：runtime sample 是否取得 / source callsite 是否定位 / payload fixture 是否完整 / OC1 是否 cover
   - 任三項缺一即標 unreliable

### 2.2 catalog 補完（events.go ignored/unsupported）

**改動**：`internal/agent/opencode/events.go` + `internal/agent/opencode/testdata/opencode-1.14.23-events.json`（新建）

**前置**：§2.1 對齊報告已產出。

**catalog 命名約定（bipartite — Round 1 C3）**：

OpenCode catalog 是**bipartite naming**：

- **既有 8 個 installable entries 保留 Purdex-normalized PascalCase names**（`SessionStart` / `UserPromptSubmit` / `SubagentStart` / `SubagentStop` / `PermissionRequest` / `Stop` / `StopFailure` / `SessionEnd`）— 因為 installer / template 互動以這些 normalized name 為 key
- **新加 ignored/unsupported entries 用 upstream key**（dotted lowercase，例如 `session.updated` / `message.removed` / `chat.params`）— 因為這些不被 installer 接，`Name` 欄不需匹配 normalized 形式

**`HookEventSpec` 不加新欄位**。upstream key ↔ purdex normalized name 的 mapping 由 `internal/agent/opencode/testdata/opencode-1.14.23-events.json`（§2.2 schema；§2.4 testdata tree）保存（Round 2 N1 wording fix）。

**Collision policy（new — Round 2 M3）**：

1. `events.json` 內 `upstreamKey` **全域唯一**（across busEvents + strongHooks）
2. `HookEventSpec.Name` 不得與既有 installable normalized name 撞名（即 ignored/unsupported entry 的 upstream key 不能等於 `SessionStart` / `UserPromptSubmit` / `SubagentStart` / `SubagentStop` / `PermissionRequest` / `Stop` / `StopFailure` / `SessionEnd`）
3. 若 upstream 未來新增 PascalCase event 撞到 normalized name：
   - **預設行為**：catalog entry 必須分類為 `unsupported` 並使用 explicit disambiguated Name（例如加 prefix `upstream:SessionStart`）
   - 或在納入前先擴 schema（例如加 `nameQualifier` 欄）
4. **Test 驗證**：HC5b 加 collision check — `opencodeEventSpecs` 內所有 ignored/unsupported `Name` 必須 ∉ installable normalized name set

**規則**：

1. 依 §2.1 報告的「Catalog 對齊建議」清單，補入 `opencodeEventSpecs` slice
2. 新加條目**必須**設 `Handling: agent.HookHandlingIgnored` 或 `agent.HookHandlingUnsupported`，**不可**用 default
3. 新加條目 `EmitsStatus` 必須為 `[]agent.Status{}`（non-installable 不可 emit status）
4. 既有 8 個條目**不變 Name 與 Handling**
5. 條目 `Description` 必須 ≤70 chars，無尾句點，無 emoji

**`events.json` schema**（Round 1 C2 / M9 + Round 2 H2 加 `stalenessPolicy`）：

```jsonc
{
  "version": "1.14.23",
  "tag": "v1.14.23",
  "commitSha": "<from sst/opencode tag>",
  "auditedAt": "2026-04-27",
  "auditReport": "docs/specs/2026-04-26-opencode-1.14.23-hook-audit.md",
  "busEvents": [
    {
      "upstreamKey": "session.created",
      "definedAt": "packages/opencode/src/session/index.ts:LINE",
      "publishedAt": ["packages/opencode/src/session/index.ts:LINE"],
      "payloadFields": ["sessionID", "title", ...],
      "purdex": {
        "kind": "installable",
        "purdexEventName": "SessionStart",
        "templateConsumesAt": "internal/agent/opencode/plugin_template.go:132",
        "stalenessPolicy": null  // 只在 deprecated-but-supported 時填
      }
    },
    {
      "upstreamKey": "session.idle",
      "definedAt": "...",
      "publishedAt": ["..."],
      "payloadFields": [...],
      "purdex": {
        "kind": "installable",
        "purdexEventName": "Stop",
        "templateConsumesAt": "internal/agent/opencode/plugin_template.go:155",
        "stalenessPolicy": {
          "state": "deprecated-but-emit",
          "decision": "retain | switch | dualSubscribe",
          "rationale": "<one-line 解釋為何選此>",
          "switchTarget": "session.status({type:\"idle\"})",  // 若 decision==switch 必填
          "dedupRequired": false  // 若 decision==dualSubscribe 必填 true
        }
      }
    },
    {
      "upstreamKey": "session.updated",
      "definedAt": "...",
      "publishedAt": ["..."],
      "payloadFields": [...],
      "purdex": {
        "kind": "ignored",
        "reason": "metadata observation, not status-bearing"
      }
    }
    // ... 30+ entries
  ],
  "strongHooks": [
    {
      "upstreamKey": "chat.message",
      "interfaceLocation": "packages/plugin/src/index.ts:LINE",
      "triggerCallsiteAt": "packages/opencode/src/chat/...:LINE",  // Round 2 AM1 必填
      "payloadFields": [...],
      "purdex": {
        "kind": "installable",
        "purdexEventName": "UserPromptSubmit",
        "templateConsumesAt": "internal/agent/opencode/plugin_template.go:167",
        "stalenessPolicy": null
      }
    }
    // ... 16+ entries
  ]
}
```

**`stalenessPolicy` 規則**（Round 2 H2）：

- 只在 `kind == "installable"` 且 upstream 有任何 deprecated/undocumented/source-level-only 註記時填
- `decision` 三選一：`retain`（接受 deprecation 風險）/ `switch`（換鍵）/ `dualSubscribe`（雙訂閱 + dedup）
- 若 `decision == "switch"`：必須觸發 §2.5 plugin template refresh
- 若 `decision == "dualSubscribe"`：必須觸發 §2.5 plugin template refresh + 加 dedup test case
- 若 `decision == "retain"`：§7.3 checkpoint 必須在 Slice 6 summary 把該 entry 列為 known residual risk 並給 Go/Stop 結論

**預期條目數量**：依 §2.1 報告。粗估 OpenCode bus events 28~32 + strong hooks 16~19 - 已宣告 8 ≈ **30~40 個新 ignored/unsupported 條目**（actual 數量以 events.json 為準）。

### 2.3 SupportedVersion 報告（hooks.go）

**改動**：`internal/agent/opencode/hooks.go`

**新增**：

```go
const opencodeHooksSupportedVersion = "1.14.23"
```

**7 個 CheckHooks return site，5 個 nil-error path 必須帶版本欄**：

| Site (line 約) | Type | 加 SupportedVersion? |
|---|---|---|
| 63 — `cannot find home dir` | error return | ❌ 不加 |
| 69 — missing plugin | nil-error HookStatus | ✅ 加 |
| 76 — `read plugin: %w` | error return | ❌ 不加 |
| 79 — unmanaged plugin | nil-error HookStatus | ✅ 加 |
| 112 — path resolution failure | nil-error HookStatus | ✅ 加 |
| 125 — managed body drift | nil-error HookStatus | ✅ 加 |
| 136 — fully installed | nil-error HookStatus | ✅ 加 |

5 個 nil-error path 加：

```go
SupportedVersion: opencodeHooksSupportedVersion,
ExceedsSupport:   agent.CompareHookAgentVersions(agentVersion, opencodeHooksSupportedVersion) > 0,
```

**對應 cc/codex pattern**：見 `cc/hooks.go:44-45,95-96` / `codex/hooks.go:50-51,103-104`。

### 2.4 Provenance fixtures（testdata）

**新建檔案結構**：

```
internal/agent/opencode/testdata/
├── opencode-1.14.23-manifest.json    ← single-file manifest（two-stage build — Round 2 M1）
│   ├ tag / commitSha / version / normalizedVersion
│   ├ auditReport (path)
│   ├ catalogSummary { busEvents: count, strongHooks: count, installable: count, ignored: count, unsupported: count }
│   └ payloadFixtureDir (path) — Commit 4 補上（Commit 2 不寫此欄）
├── opencode-1.14.23-version.txt       ← 真實 `opencode --version` 輸出（OC5 raw 版本字串）
├── opencode-1.14.23-source.md         ← source URLs + commit hash + audit 過程記錄 + fixture provenance（runtime vs source-derived）
├── opencode-1.14.23-events.json       ← §2.2 schema，**Commit 2 即建立**（不 defer 到 Commit 4）
└── opencode-1.14.23-payloads/         ← Commit 4 才建（Commit 2 manifest 不引用）
    ├── session.created.json           ← 每個 plugin_template.go 訂閱的 event 一個 payload fixture
    ├── permission.asked.json
    ├── question.asked.json
    ├── session.error.json
    ├── session.idle.json              ← 即使是 deprecated key，只要 plugin 仍訂閱，fixture 必存
    ├── session.deleted.json
    ├── chat.message.json
    ├── tool.execute.before.json
    └── tool.execute.after.json
```

**Two-stage manifest build（Round 2 M1）**：

- **Commit 2**：建立 `manifest.json` 但**不**含 `payloadFixtureDir` 欄（因為 fixtures 在 Commit 4 才建）
- **Commit 4**：補上 `manifest.json` 的 `payloadFixtureDir` 欄 → 此時 manifest 完整
- 兩階段都通過自動 schema 驗證（用 `omitempty` JSON tag 或 separate Commit-2 / Commit-4 schema）

**fixture 內容規則**：

- 來源：§2.1 對齊報告 + 1.14.23 source 直接抄出
- payload fixture 必含**所有**`renderManagedPlugin` 從該 event 讀的 field（不可省略）
- 不放敏感資料（API keys / user paths）
- `manifest.json` 與 `events.json` 在 Commit 2 即建立（不等 Commit 4）— 確保 HC5 紅燈測試有獨立可信依據
- `source.md` 必須明確標示每個 payload fixture 是 **runtime trace** 來源還是 **source-derived schema**（partial fixtures 必標 `source-derived`）

**對應測試**：

- OC1a `TestOpenCodeTemplateEventContractsDocumented` — 對每個 plugin 訂閱的 event，從 `payloads/<event>.json` load payload，跑 mock plugin handler，斷言 template 讀的 field 都存在
- HC5d `TestOpenCodeManifestCatalogSummaryMatchesEvents`（new — Round 2 M2）— assert `manifest.json.catalogSummary.busEvents` == `events.json.busEvents.length`；同樣覆蓋 strongHooks count + 各 kind count + installable/ignored/unsupported 與 `opencodeEventSpecs` 統計一致

### 2.5 Plugin template refresh（conditional）

**前置**：§2.1 對齊報告產出後，**逐項評估每個 template-consumed event key**。

**改動**：`internal/agent/opencode/plugin_template.go`

**Skip 條件（廣化 — Round 1 H5 + Round 2 H2 加 partial-stale gate）**：當且僅當以下**全部**為真，本 commit 可 skip：

1. 所有 template-consumed event keys（`session.created` / `permission.asked` / `question.asked` / `session.error` / `session.idle` / `session.deleted` / `chat.message` / `tool.execute.before` / `tool.execute.after`）在 1.14.23 source 確認**仍存在且 emit/可訂閱**
2. 所有 payload paths 在 1.14.23 schema 仍存在
3. 不需要新增 SDK / 版本 feature detection
4. 不需要新增 `subscribe(filter)` filter
5. **所有 `stalenessPolicy != null` 的 entry 的 `decision` 都是 `retain`**（即沒有 `switch` 或 `dualSubscribe` 需要 template 變更）

**只要任一項不成立，本 commit 必須執行**。

**改動規則**：

1. 依 §2.1 報告指定的替代鍵 / filter / payload path 換掉現有 key
2. 同步更新 `:175` 的 `source: 'chat.message'` 等 traceability 欄位
3. 若 §2.1 判定 `question.asked` 為 undocumented Bus event 但仍接受 → plugin 加註解說明 source 來源 + audit decision link
4. 若任一 `stalenessPolicy.decision == "dualSubscribe"`（雙訂閱）→ 加 dedup logic 防止 double-fire + 加 dedup test case (OC1c)
5. 若任一 `stalenessPolicy.decision == "switch"` → 換鍵後同步更新 `events.json.publishedAt` 與 `templateConsumesAt`

**驗證**：對應 OC1 `TestOpenCodePluginTemplate_UsesVerifiedEvents`（**unconditional** — 見 §3 與 Round 1 C1）。

### 2.6 SPA agent icon coverage

**改動**：`spa/src/lib/agent-icons.test.tsx`

**新增 test**（snippet 對齊現有 `getAgentIcon` API — Round 1 N16）：

```ts
describe('opencode', () => {
  it('returns opencode icon for opencode agent type', () => {
    const icon = getAgentIcon('opencode', /* current API options */);
    expect(icon).toBeDefined();
  });

  it('opencode icon is independent of cc/codex variants', () => {
    const baseline = getAgentIcon('opencode', /* current API */);
    const variantA = getAgentIcon('opencode', /* variant variation */);
    expect(baseline).toBe(variantA);
  });
});
```

**注意**：實作前先讀 `spa/src/lib/agent-icons.tsx` 確認當前 `getAgentIcon` signature 與 options shape；snippet 內 placeholder 由實作者依 alpha.230 source 填入。

---

## 3. 測試矩陣

| ID | Test | File | Red Assertion | TDD type |
|---|---|---|---|---|
| HC5 | `TestOpenCodeEvents_ClassifyAgainstFrozenManifest` | `internal/agent/opencode/events_test.go` | 從 `testdata/opencode-1.14.23-events.json` load 上游 entries，斷言每 entry 在 `opencodeEventSpecs` 內或被顯式標記 ignored/unsupported 在 manifest 內。**獨立可信 SSoT** | red |
| HC5b | `TestOpenCodeEvents_NonInstallableHaveExplicitHandlingAndNoNameCollision` | `internal/agent/opencode/events_test.go` | (a) 新加 ignored/unsupported 條目顯式設 `Handling`；(b) **collision check** — `Name` ∉ installable normalized name set（Round 2 M3）| red |
| HC5c | `TestOpenCodeEvents_NonInstallableHaveEmptyEmitsStatus` | `internal/agent/opencode/events_test.go` | ignored/unsupported 條目 `EmitsStatus` 為 empty slice | red |
| **HC5d** | `TestOpenCodeManifestCatalogSummaryMatchesEvents`（**new — Round 2 M2**）| `internal/agent/opencode/events_test.go` | (a) `manifest.catalogSummary.busEvents` == `events.json.busEvents.length`；(b) `strongHooks` 同；(c) `installable`/`ignored`/`unsupported` count 與 `opencodeEventSpecs` 統計一致 | red |
| OC1a | `TestOpenCodeTemplateEventContractsDocumented` | `internal/agent/opencode/plugin_template_test.go` | 每個 plugin 訂閱的 event 都有 fixture，且 fixture payload 含 template 讀的所有 field | red |
| OC1 | `TestOpenCodePluginTemplate_UsesVerifiedEvents` | `internal/agent/opencode/plugin_template_test.go` | **Unconditional**（Round 1 C1）— 用 `payloads/<event>.json` 跑 mock plugin handler，驗證 spawn `pdx hook` 帶正確 normalised payload | red |
| OC1c | `TestOpenCodePluginTemplate_DualSubscribeDedup`（**conditional — only if §2.5 dualSubscribe path 觸發**）| `internal/agent/opencode/plugin_template_test.go` | 雙訂閱情境下，同一 logical event 不會 double-fire `pdx hook` | red (conditional) |
| OC4 | `TestOpenCodeCheckHooks_ReportsSupportedVersion` | `internal/agent/opencode/hooks_test.go` | table tests 跑 5 個 nil-error CheckHooks return path，全部 return 含 `SupportedVersion=1.14.23` | red |
| OC5 | `TestOpenCodeCheckHooks_ExceedsSupport` | `internal/agent/opencode/hooks_test.go` | mock `agent --version` return `1.15.0` → `ExceedsSupport=true`；其他 case | red |
| OI1 | `returns opencode icon for opencode agent type` | `spa/src/lib/agent-icons.test.tsx` | `getAgentIcon('opencode', ...)` returns defined component | **characterization** |
| OI2 | `opencode icon is independent of cc/codex variants` | `spa/src/lib/agent-icons.test.tsx` | component identity stable across variant combinations | **characterization** |

**Test 數量**：10 (固定) + 1 (conditional OC1c) = **10~11 tests**

---

## 4. Commit 順序（TDD）

### Commit 1 — `docs(opencode): 1.14.23 hook surface re-alignment audit`

**範圍**：純文件，新增 `docs/specs/2026-04-26-opencode-1.14.23-hook-audit.md`

**內容**：§2.1 列的 5 個章節 + 5 個顯式判定 + Catalog 對齊建議 + Plugin template 影響 + Slice 6 design-impact summary（為 §7.3 checkpoint 提供依據）

**TDD red**：不適用（純文件）

**Run**：無（doc-only）

### Commit 2 — `feat(agent/opencode): classify upstream catalog with frozen manifest`

**Red**：HC5 / HC5b / HC5c / HC5d 失敗

**Green**：

- 建 `testdata/opencode-1.14.23-events.json`（§2.2 schema）
- 建 `testdata/opencode-1.14.23-manifest.json` — **不含 `payloadFixtureDir` 欄**（Round 2 M1 — 因 payload fixtures 在 Commit 4 才建）
- 依 Commit 1 audit 報告補 `opencodeEventSpecs` 的 ignored/unsupported 條目
- 加 HC5 / HC5b（含 collision check）/ HC5c / HC5d（catalogSummary 一致性）
- 對任一 `stalenessPolicy != null` 的 entry，audit 報告必須有對應決策段落（Commit 2 ship gate 強制）

**Run**：

```
go test ./internal/agent/opencode/... ./internal/agent/... -count=1
```

**注意**：

- `SupportedStatuses()` 不該變動（新加條目都 empty `EmitsStatus`），跑 `internal/agent/supported_statuses_test.go` 確認
- **events.json + 部分 manifest 提前到本 commit**（Round 1 C2 / M8 + Round 2 M1）— manifest 的 `payloadFixtureDir` 欄 deferred 到 Commit 4

### Commit 3 — `feat(agent/opencode): report hook supported version`

**Red**：OC4 / OC5 失敗

**Green**：

- 加 `opencodeHooksSupportedVersion = "1.14.23"`
- 5 個 nil-error CheckHooks return path 補 `SupportedVersion` + `ExceedsSupport`
- 加 OC4 table test + OC5 三個 case

**Run**：

```
go test ./internal/agent/opencode -count=1
```

### Commit 4 — `test(agent/opencode): add payload fixtures and template contract tests`

**Red**：OC1a / OC1 失敗（payload fixtures 不存在）

**Green**：

- 建 `testdata/opencode-1.14.23-payloads/<event>.json`（每 plugin 訂閱 event 一份）
- 建 `opencode-1.14.23-version.txt`（real `opencode --version` 輸出）
- 建 `opencode-1.14.23-source.md`（source URLs + commit hash + per-fixture `runtime` vs `source-derived` 標註）
- **補上 manifest.json 的 `payloadFixtureDir` 欄**（Round 2 M1）— 此時 manifest 完整
- 加 OC1a：對每個 plugin 訂閱 event，load fixture，斷言 template 讀的 field 都存在
- 加 OC1：**unconditional** — 用 fixtures 跑 mock plugin handler，驗證現有 9 個 callback mapping 正確

**Run**：

```
go test ./internal/agent/opencode -count=1
```

**注意**：本 commit 不換鍵；OC1 此時驗的是**現有** mapping。Commit 5（若執行）會更新 OC1 期望值。

### Commit 5 — `fix(agent/opencode): refresh plugin event mapping` (**conditional**)

**前置**：§2.5 廣化 skip 條件**任一不成立**

**Red**：OC1 失敗（現有 mapping 不再匹配 1.14.23 期望 payload）

**Green**：

- 依 Commit 1 報告換鍵 / 加 filter / 改 payload path / 加 feature detection
- 更新 `plugin_template.go` 對應 callback name + `source:` 欄位
- 更新 OC1 期望值到新 mapping
- 若 §2.5 條件 (5) `dualSubscribe` 情境 → 加 OC1c dedup test case
- 同步更新 manifest `templateConsumesAt` line numbers + `events.json.stalenessPolicy.switchTarget`

**Run**：

```
go test ./internal/agent/opencode -count=1
```

**Skip 條件**：§2.5 五項全部 verified stable。Skip 時：

- 不 commit 此 step
- OC1 仍存在（在 Commit 4 加），驗的是現有 mapping
- §6 ship gate 對 OC1 + H6.4 partial-stale policy gate 要求**不變**

### Commit 6 — `test(spa): add opencode agent icon characterization coverage`

**Type**: Characterization / coverage commit（**not TDD red**）

**Justification**: production icon support 已在 alpha.230 baseline 存在。本 commit 補測試覆蓋率 + 防回歸 guard。

**Implementation**：

- `spa/src/lib/agent-icons.test.tsx` 加 opencode describe block（§2.6 snippet 但對齊現有 API shape）
- OI1 / OI2 兩個 test（baseline 應即綠）

**Run**：

```
pnpm --prefix spa exec vitest run src/lib/agent-icons.test.tsx
```

### Final Verification（Round 2 H1 加 boundary script）

```
go test ./internal/agent/... -count=1
pnpm --prefix spa exec vitest run src/lib/agent-icons.test.tsx
scripts/check-pr-4a0-boundary.sh origin/main    # H6.3 enforcement — fails if any changed file outside §5 Allowed paths
```

PR 提交前：

```
go test ./... -count=1
pnpm --prefix spa run lint
pnpm --prefix spa run build
scripts/check-pr-4a0-boundary.sh origin/main
```

`scripts/check-pr-4a0-boundary.sh` 由本 PR 一併建立（內容見 §5）。

---

## 5. 不做（明列 boundary — Round 1 L14 + Round 2 H1 加 enforcement）

**Allowed paths**（PR-4a-0 改動限制在以下範圍）：

- `docs/specs/2026-04-26-opencode-1.14.23-hook-audit.md`（new）
- `docs/specs/2026-04-26-lights-rebuild-phase-4a-plan.md`（this file）
- `internal/agent/opencode/events.go`
- `internal/agent/opencode/events_test.go`
- `internal/agent/opencode/hooks.go`
- `internal/agent/opencode/hooks_test.go`
- `internal/agent/opencode/plugin_template.go`（conditional Commit 5）
- `internal/agent/opencode/plugin_template_test.go`
- `internal/agent/opencode/testdata/opencode-1.14.23-*`（new tree）
- `spa/src/lib/agent-icons.test.tsx`
- `scripts/check-pr-4a0-boundary.sh`（new — H6.3 enforcement script）

**Forbidden paths**（CI / boundary script 必擋）：

- `internal/module/agent/*` — 屬 PR-4a-1/2
- `internal/agent/probe/*` — 屬 PR-4a-1
- `internal/agent/cc/*` — 已在 PR #645/#655 完成
- `internal/agent/codex/*` — 已在 PR #645/#655 完成
- `internal/tmux/*` — 屬 PR-4a-1 Slice 0
- `internal/store/*` — 不在本 phase 範圍
- `internal/agent/opencode/status.go` — frame-layer concern
- tab rendering 相關：`spa/src/features/tabs/**` / 任何 SPA tabbar / SubagentDots component
- 其餘 `spa/src/features/**`（除 icon test 外）/ SPA UI 組件

**`scripts/check-pr-4a0-boundary.sh`**（new — Round 2 H1）：

```bash
#!/usr/bin/env bash
# Usage: scripts/check-pr-4a0-boundary.sh <base-ref>
# Exit 0 if all changed files ⊆ Allowed paths; non-zero otherwise.

set -euo pipefail
BASE="${1:-origin/main}"

ALLOWED=(
  'docs/specs/2026-04-26-opencode-1\.14\.23-hook-audit\.md'
  'docs/specs/2026-04-26-lights-rebuild-phase-4a-plan\.md'
  'internal/agent/opencode/events\.go'
  'internal/agent/opencode/events_test\.go'
  'internal/agent/opencode/hooks\.go'
  'internal/agent/opencode/hooks_test\.go'
  'internal/agent/opencode/plugin_template\.go'
  'internal/agent/opencode/plugin_template_test\.go'
  'internal/agent/opencode/testdata/opencode-1\.14\.23-.*'
  'spa/src/lib/agent-icons\.test\.tsx'
  'scripts/check-pr-4a0-boundary\.sh'
)

PATTERN="^($(IFS='|'; echo "${ALLOWED[*]}"))$"

VIOLATIONS=$(git diff --name-only "$BASE" | grep -vE "$PATTERN" || true)

if [[ -n "$VIOLATIONS" ]]; then
  echo "PR-4a-0 boundary violation — files outside allowed paths:" >&2
  echo "$VIOLATIONS" >&2
  exit 1
fi

echo "PR-4a-0 boundary check passed."
```

**Out of plan items**（建獨立 issue 追蹤）：

- cc / codex trace observability 升級（PreToolUse 從 ignored 升 detail）
- codex 5 個 FutureOnly 假想 events reconsider
- hooks-hotfix-plan PR 3「Remaining Claude Strictness」可 close 不做的最終確認

---

## 6. Ship gate（PR-4a-0 — Round 1 H6 加 3 gate + Round 2 H2 加 H6.4）

PR-4a-0 ship 必須全綠：

| 項 | 條件 |
|---|---|
| Audit 報告完整 | Commit 1 文件覆蓋 §2.1 列的 5 章節 + 5 個顯式判定 + Slice 6 design-impact summary |
| HC5 / HC5b / HC5c / HC5d | catalog 完整對齊 manifest（exact set assertion + collision check + catalogSummary count match）|
| OC4 / OC5 | SupportedVersion 5 nil-error path 全綠；2 error path 不檢查版本欄 |
| OC1a | 所有 plugin 訂閱 event 都有 fixture |
| OC1 | **Unconditional** — 現有 mapping（Commit 5 skip）或新 mapping（Commit 5 執行）皆綠 |
| OC1c | 若 `dualSubscribe` decision 觸發則必須綠 |
| OI1 / OI2 | SPA characterization test 綠 |
| **H6.1 events.json ↔ events.go exact match** | HC5 + HC5d 自動化測試斷言 |
| **H6.2 template consumed keys ↔ payload fixtures exact match** | OC1a 自動化測試斷言 |
| **H6.3 changed-file boundary check** | `scripts/check-pr-4a0-boundary.sh origin/main` 退出碼 0（CI / 本機 final verification 必跑 — Round 2 H1）|
| **H6.4 partial-stale policy documented and accepted**（new — Round 2 H2）| 對每個 `stalenessPolicy != null` 的 entry：(a) `decision` 必填三選一；(b) audit 報告對應段落 list rationale；(c) 若 `decision == "retain"`，§7.3 Slice 6 summary 必須 explicit 寫入 known residual risk + Go/Stop 結論 |
| `go test ./...` 全綠 | 含 `internal/agent/supported_statuses_test.go` |
| `pnpm --prefix spa run lint` 綠 | |
| `pnpm --prefix spa run build` 綠 | |
| Codex review 兩輪 | 標準 + 3-parallel adversarial（per CLAUDE.md PR Review 兩輪制）|

---

## 7. PR-4a-1 / PR-4a-2 大綱（Round 1 M11 + Round 2 M4 加 evidence-based fail rule）

### 7.1 PR-4a-1（probe primitive + cc + helper + dev log）大綱

| Slice | 範圍 | 估 LoC | 估 tests |
|---|---|---|---|
| 0 | tmux API 擴展（`CapturePaneTopLines(target, n)` helper + 內部用 `CapturePaneRange(target, start, endInclusive)` — Round 1 AM3）+ fake executor 對應 | ~40 | 4 |
| 1 | Probe primitive 重構（`WatchFullScreen` / `WatchTopLines` + `watchLoop` + `ScreenChangeEvent`）；移除 `ActivitySignal` enum 的 signal 解讀職責；watcher ownership 改 watch-loop-owned（Round 1 AM4）| ~80 | 6 |
| 2 | ShellPrompt utility 保留 probe 層（不動） | 0 | 0 |
| 3 | Module 共用 orchestrator helper | ~60 | 5 |
| 4 | cc module 接新 primitive | ~40 | 5 |
| 7 | graceWindow + `PDX_DEV_MODE=1` observation log + **新增 expvar `purdex_probe_*`（不 rename 既有 `purdex_phase35_*`）**（Round 1 AN1）| ~30 | 4 |

**淨 +250 LoC + 24 tests**。

### 7.2 PR-4a-2（codex + opencode + 清舊）大綱

| Slice | 範圍 | 估 LoC | 估 tests |
|---|---|---|---|
| 5 | codex module 接新 primitive | ~30 | 4 |
| 6 | opencode module 接新 primitive（**依賴 PR-4a-0 對齊結果 + §7.3 checkpoint 通過**）| ~30 | 3 |
| 8 | 清舊 onActivityDetected / shouldWatchActivity / ActivitySignal enum signal 解讀 | -60 | (移除既有) |

**淨 0 LoC + 7 tests**。

### 7.3 Slice 6 design-impact stop/go checkpoint（Round 1 M11 + Round 2 M4 evidence-based fail rule）

PR-4a-0 ship 前**必須**產出 `Slice 6 design impact summary`（一段 plan v2 草稿章節，落在 audit 報告末段），檢驗以下 3 個 trigger pattern 是否仍可靠：

1. **Running transition trigger**：opencode `UserPromptSubmit` 是否在 1.14.x 上**穩定 emit**？（依賴 §2.1 `chat.message` 判定 + Commit 5 是否執行）
2. **Idle transition trigger**：opencode `Stop` 是否在 1.14.x 上**穩定 emit**？（依賴 §2.1 `session.idle` vs `session.status` 判定 + 雙訂閱情境）
3. **Waiting transition trigger**：opencode `PermissionRequest`（從 `permission.asked` + `question.asked`）是否兩條 path 在 1.14.x 上**穩定 emit**？（依賴 §2.1 `question.asked` 判定）；若 audit 採用 `permission.ask` strong hook → 加第 4 條

**Evidence-based reliability 判定（Round 2 M4）**：

對每個 trigger，**audit 必須提供以下三項佐證**才可標 `reliable`：

1. **Source callsite**：對應 upstream 事件 / hook 在 1.14.23 source 內可定位的 publish/trigger 位置
2. **Payload fixture**：對應 `payloads/<event>.json` 存在且含 plugin handler 真正讀的所有 field
3. **OC1 coverage**：OC1 test 對該 trigger 的 callback path 至少有一個 case

**任一缺項 → 自動標 unreliable → Stop**（不接受純 audit prose 描述作為佐證）。

對於採用 `stalenessPolicy.decision == "retain"` 的 deprecated-but-supported event（典型 `session.idle`）：
- 三項佐證仍適用
- 若三項齊全 + plan §6 H6.4 已記載 + Slice 6 summary 寫入 known residual risk → 可標 `reliable-with-residual`
- `reliable-with-residual` 等同 Go，但必須在 Slice 6 summary 顯式記錄該 risk

**Stop/Go 規則**：

- **All 3 reliable / reliable-with-residual** → Go：PR-4a-2 Slice 6 設計按 plan 大綱繼續
- **任一 unreliable** → Stop：PR-4a-1 起草前**必須**先開 design-blocking issue，討論：
  - opencode 是否該降級為 hook-only mode（無 probe trigger）
  - PR 切分是否要重排（opencode 拉到 PR-4a-3 獨立處理）
  - 整體 trigger pattern 是否要改用 frame-diff fallback

**Document location**：summary 寫在 `docs/specs/2026-04-26-opencode-1.14.23-hook-audit.md` 末段（Commit 1 同步產出），不另開檔。

---

## 8. Risk

| Risk | Mitigation |
|---|---|
| §2.1 audit 範圍過大（OpenCode source 變動快，30+ events 全梳理）| 限定 1.14.23 tag，不追 dev branch；audit 用 subagent 平行查（一支 strong hooks + trigger callsite / 一支 bus events / 一支 message + question + permission flow），各回 enumerated list 後主 Claude 整合 |
| §2.4 fixture payload 與 1.14.23 runtime 不符 | 若 audit 階段無法取得真實 runtime 樣本，先用 source-derived schema；附 `source.md` 註明每 fixture 的 provenance（runtime vs source-derived）；後續 PR 真實對接時補 runtime fixture |
| **Partial-stale event keys**（Round 1 M10 + Round 2 H2）| `session.idle` 是 deprecated-but-still-emit 的典型；§2.1 對每個 partial-stale key 必須給出三選一決策（`stalenessPolicy.decision` = `retain`/`switch`/`dualSubscribe`），寫入 events.json，並通過 §6 H6.4 ship gate；若 `retain`，§7.3 checkpoint 必須 explicit 列為 known residual risk |
| §2.5 conditional 換鍵牽動 Slice 6 設計 | 本 plan 顯式 declare PR-4a-2 待 PR-4a-0 ship 後再起草 + §7.3 evidence-based checkpoint 強制檢驗 |
| Codex sandbox 無網路（feedback_codex_sandbox_no_install）| Commit 2-6 主 Claude 必須手動跑 `pnpm install` + vitest + lint + build；Codex review 不可作為 SPA 驗證來源 |
| 主 repo 並發 session（feedback_concurrent_session_safety）— Round 1 H7 + Round 2 L1 加留痕 | 進 worktree 前**先 `git status -s` 確認 clean**（**輸出貼進 PR description 留痕**）；clean 後 `git fetch origin main && git reset --hard origin/main`；**若非 clean 則停止並由 user 處理 in-progress work，不可自動覆蓋**。commit 後 push 前再 `git pull --rebase origin main` 一次 |
| §7.3 checkpoint 失敗時 PR-4a-1 進度延誤 | checkpoint 是強制 stop/go gate；若 stop，主工作流必須暫停 PR-4a-1 起草，先處理 design-blocking issue |
| Strong hook (e.g., `permission.ask`) interface exists but no trigger callsite — Round 2 AM1 | §2.1 audit 對每個 strong hook 必須驗 `Plugin.trigger("<hookName>")` callsite；無 callsite → 分類 `unsupported`/dead strong hook，**不採用** |

---

## 9. LOC 預估（Round 1 M12 重算 + Round 2 H2/M2 加新 test）

| Commit | 估 LoC | 估 tests | 備註 |
|---|---|---|---|
| 1: audit 報告（純文件）| ~450 (新文件) | 0 | 含 5 個顯式判定章節 + Slice 6 impact summary + per-strong-hook trigger callsite list |
| 2: catalog 補完 + manifest (lite) + events.json | **~180~260** (events.go) + **~150~250** (manifest.json + events.json) | **5** (HC5 + HC5b 含 collision + HC5c + HC5d catalogSummary)| 30-40 entries × 4-6 lines/entry |
| 3: SupportedVersion | ~30 (hooks.go) | 4 (OC4 5-path table + OC5 三 case) | 不變 |
| 4: payload fixtures + version.txt + source.md + manifest.payloadFixtureDir + OC1/OC1a | ~250~400 (fixtures + helpers) | 6 (OC1a per-event + OC1 per-event) | 9 events × payload fixture |
| 5: plugin refresh (conditional) | 0~60 | 0~5 (OC1 update + OC1c dedup) | dedup test 增加 |
| 6: SPA icon test | ~30 | 2 (OI1 + OI2) | characterization |
| (extra) | ~30 (`scripts/check-pr-4a0-boundary.sh`) | 0 | H6.3 enforcement |

**總計**：**~1090~1480 LoC + 17~22 tests**（含新文件 ~450 LoC + 新 testdata 樹 + boundary script）

純 Go/TS/JSON code 改動：~640~1030 LoC + 17~22 tests。**屬中-大型 PR**。

**拆 PR 門檻**：

- 若 §2.1 對齊報告 enumerate **>50 upstream keys**（bus events + strong hooks 合計）→ 拆 PR
- 若 template consumed events **>12 cases** → 拆 PR
- 預期值（28 bus + 19 strong = 47 keys；9 template events）— 在門檻內，**不拆**
- **拆 PR trigger 在 Commit 1 audit 完成後決定**（依 audit enumerate 實際數量）
- 若實際超門檻：拆為 PR-4a-0a（catalog + manifest + version）+ PR-4a-0b（payload fixtures + OC1）+ PR-4a-0c（plugin refresh + SPA test）

---

## 10. 結束條件（PR-4a-0）

**Ship**：

- §6 ship gate 全綠（含 H6.1/H6.2/H6.3/H6.4 4 個新 gate）
- §7.3 Slice 6 design-impact checkpoint 通過 evidence-based 三項佐證 → Go 結論
- PR merged
- 對應 main bump PR ship（VERSION 進到 alpha.231）

**Memory 更新**：

- `kickoff_lights_rebuild.md` 更新「目前進度」段落，記 PR-4a-0 ship 與 §2.1 對齊報告路徑 + §7.3 checkpoint 結論（含 reliable / reliable-with-residual / stop 分類）
- `project_progress.md` 更新

**下一階段**：起草 Phase 4a plan v2（涵蓋 PR-4a-1 + PR-4a-2 完整細節，引用 PR-4a-0 對齊報告與 §7.3 checkpoint 結論作為 Slice 6 設計依據）

---

## 11. 文獻

- Audit issue: [#656 v5.2](https://github.com/wake/purdex/issues/656)
- Hooks hotfix plan: `docs/specs/2026-04-25-agent-hooks-hotfix-plan.md`（PR 1+2 已 ship；PR 4/5/6 收編進本 plan PR-4a-0）
- Lights rebuild spec: `docs/specs/2026-04-23-lights-rebuild-spec.md` §2.4 / §8.1 / §71
- 前置 phase: PR #644（3.5a）/ PR #650（3.5b）/ PR #645（hotfix PR 1）/ PR #655（hotfix PR 2）

**Codex review trail（non-normative — Round 2 L2）**：以下 job IDs 是 transient artifact，僅供開發歷程追溯，非規範性引用。Round 1/2 findings 已 inline 入本 plan 各節 + audit issue body，後續 review 不需重新讀 job log：

- Round 1: `task-mog1gsb0-0idiaa`（audit）+ `task-mog1gtot-c79vs7`（plan）
- Round 2: `task-mog24leu-l5lbd0`（audit）+ `task-mog24m6r-ql5fdt`（plan）

**待產出**：

- `docs/specs/2026-04-26-opencode-1.14.23-hook-audit.md`（Commit 1）
- `internal/agent/opencode/testdata/opencode-1.14.23-*`（Commit 2 + Commit 4）
- `scripts/check-pr-4a0-boundary.sh`（與 PR-4a-0 一併建立）
- `docs/specs/2026-04-26-lights-rebuild-phase-4a-plan.md` v2（PR-4a-0 ship 後）
