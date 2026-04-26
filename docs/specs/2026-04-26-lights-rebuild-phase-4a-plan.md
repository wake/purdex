# Lights Rebuild — Phase 4a Plan v1 (PR-4a-0 OpenCode Hooks Completion)

**Status**: Draft v1（PR-4a-0 完整細節 + PR-4a-1/2 大綱）
**Date**: 2026-04-27
**Worktree**: `.claude/worktrees/lights-phase-4-audit`（branch `worktree-lights-phase-4-audit`）
**Baseline**: `origin/main @ 63168dd9` (`1.0.0-alpha.230`)
**Audit issue**: [#656 v5](https://github.com/wake/purdex/issues/656)
**Related specs**:
- `docs/specs/2026-04-23-lights-rebuild-spec.md` §2.4 / §8.1 / §71（原方向，v5 audit 已記錄偏離）
- `docs/specs/2026-04-25-agent-hooks-hotfix-plan.md`（hooks-hotfix-plan，PR 1+2 已 ship；PR 3-6 收編進 PR-4a-0）
- `docs/specs/2026-04-25-lights-rebuild-phase-3-5-plan.md` v12（PR-3.5a）
- `docs/specs/2026-04-26-lights-rebuild-phase-3-5b-plan.md` v4（PR-3.5b）

---

## 0. 來龍去脈

Phase 3.5b 完工後（alpha.227）轉入 Phase 4a — **probe primitive rebuild + module-layer policy + graceWindow**。Audit issue #656 v5 記錄了：

1. **設計方向偏離原 spec §8.1**（字元偵測砍掉、probe layer 純 plumbing、policy 全分散到 agent module）
2. **三家 agent catalog 對齊度** — cc / codex 在 hooks-hotfix-plan PR #645 + #655 完成 `HookHandling` 四態完整 catalog；opencode 線**未完成**（PR 4/5/6 + PR 2 opencode 部分均 pending）
3. **Phase 4a Slice 6（opencode module 接新 primitive）** 在 alpha.230 上**無法 e2e 驗證** — 因為 `chat.message` 是否仍為 1.14.x current API 未驗證；若 stale，opencode 的 `UserPromptSubmit` 不會被 emit，整條 Running transition probe 在 opencode 上是 dead code

**方案 A 決議**（2026-04-27 主工作流）：將 hooks-hotfix-plan opencode 收尾工作收編為 Phase 4a 的 **PR-4a-0**，與 lights probe rebuild 同一條開發線。本 plan v1 聚焦 PR-4a-0 完整細節；PR-4a-1 / PR-4a-2 僅給大綱，待 PR-4a-0 對齊報告產出後再起草下一版 plan。

**Directive 加碼項**（user 2026-04-27）：

> opencode 的 agent hook 因為尚未完全實作，建議**重新完整對齊驗證一次**，也同時確認**是否還需要使用 `chat.message`**。

→ PR-4a-0 不可只做 hooks-hotfix-plan §2.2 OpenCode 子集；必須對 `sst/opencode` v1.14.23 source 做完整 enumeration audit，產出獨立的對齊報告 spec。

---

## 1. Scope

### 1.1 PR-4a-0 In scope

1. **OpenCode 1.14.23 上游 hook 全清單對齊驗證**（§2.1）
2. **opencode catalog 補完 ignored/unsupported 條目**（§2.2）— 對齊 cc/codex
3. **opencode SupportedVersion 報告**（§2.3）— 5 個 CheckHooks return path 全補
4. **opencode provenance fixtures**（§2.4）— `testdata/opencode-1.14.23-*`
5. **opencode plugin template refresh — conditional**（§2.5）— 僅在 §2.1 對齊判定 stale 時執行
6. **SPA agent icon coverage**（§2.6）

### 1.2 PR-4a-0 Out of scope

- 不動 `internal/module/agent/*`（屬 PR-4a-1/2）
- 不動 `internal/agent/probe/*`（屬 PR-4a-1）
- 不動 `internal/tmux/executor.go`（屬 PR-4a-1 Slice 0）
- 不動 cc / codex catalog（已在 PR #655 完成；任何 cc/codex 改動另開 issue）
- 不做 hooks-hotfix-plan PR 3「Remaining Claude Strictness」— 推論可 close 不做（cc 嚴格 checker + isPdxCommand_RequiresHookSubcommand 已在 PR #645 完成，CS1-CS9 測試齊全）
- 不改 `internal/agent/opencode/status.go`（status mapping 為 frame layer concern，不在 hook surface）

### 1.3 PR-4a-1 / PR-4a-2 大綱（不在本 plan v1 細部，待 PR-4a-0 對齊結果後起草下一版 plan）

- **PR-4a-1**：Slice 0/1/2/3/4/7
  - 0: tmux API 擴展（`CapturePaneRange`）+ fake executor
  - 1: probe primitive 重構（`WatchFullScreen` / `WatchTopLines` + `watchLoop` + `ScreenChangeEvent`；移除 `ActivitySignal` enum 的「signal 解讀」職責）
  - 2: ShellPrompt utility 保留 probe 層（不動）
  - 3: module 共用 orchestrator helper（dead-PID + sweep + ErrorGuard）
  - 4: cc module 接新 primitive
  - 7: graceWindow + `PDX_DEV_MODE` observation log + expvar

- **PR-4a-2**：Slice 5/6/8
  - 5: codex module 接新 primitive
  - 6: opencode module 接新 primitive（**依賴 PR-4a-0 對齊結果**）
  - 8: 清舊 onActivityDetected / shouldWatchActivity / ActivitySignal enum

---

## 2. 設計（PR-4a-0）

### 2.1 OpenCode 1.14.23 上游 hook 全清單對齊驗證

**產出**：`docs/specs/2026-04-26-opencode-1.14.23-hook-audit.md`（commit 1，純文件）

**驗證來源**（按優先序）：

1. `github.com/sst/opencode` repo @ tag `v1.14.23`（**權威**）
   - `packages/plugin/src/index.ts` — Hooks interface（strong hooks 完整清單）
   - `packages/opencode/src/bus/*` — Bus event 註冊與 publish callsite
   - `packages/opencode/src/session/status.ts` — session status state machine
   - `packages/opencode/src/permission/index.ts` — permission flow
   - `packages/opencode/src/question/index.ts` — question flow（v2 SDK only？）
   - `packages/opencode/src/session/message.ts` — chat.message vs message.* 的關係
2. `https://opencode.ai/docs/plugins/`（公開 docs，作為對照；發現與 source 不一致時以 source 為準）
3. `opencode --version` runtime 確認

**對齊報告必含的章節**：

1. **Strong hooks full list (1.14.23)**
   - 每個 hook：name / signature / 是否仍在 1.14.x current API / payload type / Purdex 用途（or 不用的 reason）
   - **`chat.message` 顯式判定**：
     - 是否仍在 `Hooks` interface？
     - 若是：保留現用 mapping `chat.message → UserPromptSubmit`
     - 若否：尋找替代候選（candidates: `chat.params` / Bus event `message.updated`/`message.part.updated` / `experimental.chat.messages.transform`）並文件化決策
2. **Bus events full list (1.14.23)**
   - 從 `packages/opencode/src/bus/*` enumerate 所有 `Bus.publish(...)` callsite
   - 每個 event：name / publish location / payload shape / Purdex 是否訂閱 / 不訂閱的 reason
   - **`session.idle` 顯式判定**：
     - 1.14.23 是否仍 emit？
     - vs `session.status({type:"idle"})` 取捨
     - upstream deprecation 註記
   - **`question.asked` 顯式判定**：
     - 1.14.23 是否仍 v2 SDK only？
     - 是否影響 `PermissionRequest` 的 `request_type=question` 分支
3. **Catalog 對齊建議**
   - 從上游全清單中，列出 Purdex `events.go` 應補的 ignored / unsupported 條目（具體 Name 字串清單）
4. **Plugin template 影響**
   - 若 §2.1.1 / §2.1.2 任一判定要換鍵 → 列出具體換鍵建議（例如 `chat.message` → `xxx`、`session.idle` → `session.status`）；若 §2.1.1 / §2.1.2 全 OK → 顯式 declare「無需 §2.5 plugin template refresh」

### 2.2 catalog 補完（events.go ignored/unsupported）

**改動**：`internal/agent/opencode/events.go`

**前置**：§2.1 對齊報告已產出。

**規則**：

1. 依 §2.1 報告的「Catalog 對齊建議」清單，補入 `opencodeEventSpecs` slice
2. 新加條目**必須**設 `Handling: agent.HookHandlingIgnored` 或 `agent.HookHandlingUnsupported`，**不可**用 default（依賴 default 會 fallback 為 detail，導致 `IsInstallableHookSpec` 誤判為 installable）
3. 新加條目 `EmitsStatus` 必須為 `[]agent.Status{}`（non-installable 不可 emit status）
4. 已存在的 8 個條目（SessionStart / UserPromptSubmit / SubagentStart / SubagentStop / PermissionRequest / Stop / StopFailure / SessionEnd）**不變**
   - 註：若 §2.1 判定 `chat.message` 已 stale，`UserPromptSubmit` 條目本身不變（仍是 normalised name）；改的是 plugin_template.go 的 strong hook key（§2.5）
5. 條目 `Description` 必須 ≤70 chars，無尾句點，無 emoji

**預期條目數量**：依 §2.1 報告。粗估 OpenCode bus events 30+ + strong hooks 16+ - 已宣告 8 ≈ **30~40 個新 ignored/unsupported 條目**。實際數量以對齊報告為準。

### 2.3 SupportedVersion 報告（hooks.go）

**改動**：`internal/agent/opencode/hooks.go`

**新增**：

```go
const opencodeHooksSupportedVersion = "1.14.23"
```

**5 個 CheckHooks return path 全補**（依 hooks.go 現況）：

1. `cannot find home dir` path（line 63）— 此 path return error，**不需**加 SupportedVersion（其他 agent 同 pattern）
2. **missing plugin** path（line 69）— 加 `SupportedVersion: opencodeHooksSupportedVersion, ExceedsSupport: agent.CompareHookAgentVersions(agentVersion, opencodeHooksSupportedVersion) > 0`
3. **read plugin error** path（line 76）— return error，不加
4. **unmanaged plugin** path（line 79）— 加
5. **path resolution failure** path（line 112）— 加
6. **managed body drift** path（line 125）— 加
7. **fully installed** path（line 136）— 加

**對應 cc/codex pattern**：見 `cc/hooks.go:44-45,95-96` / `codex/hooks.go:50-51,103-104`。

### 2.4 Provenance fixtures（testdata）

**新建檔案**：

```
internal/agent/opencode/testdata/opencode-1.14.23-version.txt    — opencode --version 輸出（或對等 git tag/commit reference）
internal/agent/opencode/testdata/opencode-1.14.23-source.md       — 對齊報告 commit hash + source URLs
internal/agent/opencode/testdata/opencode-1.14.23-events.json     — 完整 bus events + strong hooks 清單（machine-readable）
internal/agent/opencode/testdata/opencode-1.14.23-payloads/        — 每個 plugin_template.go 訂閱的 event 一個 payload fixture JSON（含 template 真正讀的 field）
```

**fixture 內容規則**：

- 來源：§2.1 對齊報告 + 1.14.23 source 直接抄出
- payload fixture 必含**所有**`renderManagedPlugin` 從該 event 讀的 field（不可省略）
- 不放敏感資料（API keys / user paths）

**對應測試**（OC1a）：`plugin_template_test.go` 加 `TestOpenCodeTemplateEventContractsDocumented` — 對每個 plugin 訂閱的 event，從 fixture load payload，跑 mock plugin handler，斷言 `pdx hook` 收到的 normalised event payload 含必要欄位。

### 2.5 Plugin template refresh（conditional）

**前置**：§2.1 對齊報告判定 `chat.message` / `session.idle` / 其他 plugin_template.go 用的 key 為 stale。

**改動**：`internal/agent/opencode/plugin_template.go`

**規則**：

1. 若 §2.1 對 `chat.message` 判定 OK → **跳過** plugin_template.go 改動
2. 若判定 stale → 換鍵到 §2.1 報告指定的替代鍵；同步更新 `:175` 的 `source: 'chat.message'` 欄位（如有）
3. 若 §2.1 對 `session.idle` 判定 stale → 換鍵到 `session.status`，加 filter `{type: "idle"}`
4. 若 §2.1 對 `question.asked` 判定 v2 SDK only → 加 feature detection / try-catch（plugin 不可在 v1 SDK 上 throw）

**驗證**：對應 OC1 `TestOpenCodePluginTemplate_UsesVerifiedEvents` — 從 fixture load 1.14.23 payload，斷言 plugin handler 能正確 parse + spawn `pdx hook`。

### 2.6 SPA agent icon coverage

**改動**：`spa/src/lib/agent-icons.test.tsx`

**新增 test**：

```ts
describe('opencode', () => {
  it('returns opencode icon for opencode agent type', () => {
    const icon = getAgentIcon('opencode', defaultVariants);
    expect(icon).toBeDefined();
  });

  it('opencode icon is independent of cc/codex variants', () => {
    const baseline = getAgentIcon('opencode', defaultVariants);
    const variantA = getAgentIcon('opencode', { cc: 'alt' });
    const variantB = getAgentIcon('opencode', { codex: 'alt' });
    expect(baseline).toBe(variantA);
    expect(baseline).toBe(variantB);
  });
});
```

**注意**：`defaultVariants` / `getAgentIcon` signature 依現有 `agent-icons.tsx` 實作；test 內可能需要調整 prop shape。

---

## 3. 測試矩陣

| ID | Test | File | Red Assertion |
|---|---|---|---|
| HC5 | `TestOpenCodeEvents_ClassifyCurrentPluginEvents` | `internal/agent/opencode/events_test.go` | §2.1 報告列出的所有上游 events 都在 catalog 內，分類為 status/detail/ignored/unsupported 之一 |
| HC5b | `TestOpenCodeEvents_NonInstallableHaveExplicitHandling` | `internal/agent/opencode/events_test.go` | 新加 ignored/unsupported 條目顯式設 `Handling`，不依賴 default fallback |
| HC5c | `TestOpenCodeEvents_NonInstallableHaveEmptyEmitsStatus` | `internal/agent/opencode/events_test.go` | ignored/unsupported 條目 `EmitsStatus` 為 empty slice |
| OC1a | `TestOpenCodeTemplateEventContractsDocumented` | `internal/agent/opencode/plugin_template_test.go` | 每個 plugin 訂閱的 event 都有 fixture，且 fixture payload 含 template 讀的所有 field |
| OC1 | `TestOpenCodePluginTemplate_UsesVerifiedEvents` | `internal/agent/opencode/plugin_template_test.go` | plugin handler 對 fixture payload 正確 parse 並 spawn `pdx hook`（**conditional** — 若 §2.5 改動才需）|
| OC4 | `TestOpenCodeCheckHooks_ReportsSupportedVersion` | `internal/agent/opencode/hooks_test.go` | table tests 跑 5 個 CheckHooks return path（missing plugin / unmanaged / path resolve fail / managed body drift / fully installed），全部 return 含 `SupportedVersion=1.14.23` |
| OC5 | `TestOpenCodeCheckHooks_ExceedsSupport` | `internal/agent/opencode/hooks_test.go` | mock `agent --version` return `1.15.0` → `ExceedsSupport=true`；return `1.14.23` → false；return `1.14.0` → false |
| OI1 | `returns opencode icon for opencode agent type` | `spa/src/lib/agent-icons.test.tsx` | `getAgentIcon('opencode', ...)` returns defined component |
| OI2 | `opencode icon is independent of cc/codex variants` | `spa/src/lib/agent-icons.test.tsx` | component identity stable across variant combinations |

**Test 數量**：9 (固定) + 1 (conditional OC1) = **9~10 tests**

---

## 4. Commit 順序（TDD）

### Commit 1 — `docs(opencode): 1.14.23 hook surface re-alignment audit`

**範圍**：純文件，新增 `docs/specs/2026-04-26-opencode-1.14.23-hook-audit.md`

**內容**：§2.1 列的 4 個章節 + `chat.message` / `session.idle` / `question.asked` 三個顯式判定 + Catalog 對齊建議 + Plugin template 影響

**TDD red**：不適用（純文件）

**Run**：無（doc-only）

### Commit 2 — `feat(agent/opencode): classify upstream catalog`

**Red**：HC5 / HC5b / HC5c 失敗（catalog 缺新條目）

**Green**：

- 依 Commit 1 audit 報告補 `opencodeEventSpecs` 的 ignored/unsupported 條目
- 加對應測試 HC5 / HC5b / HC5c

**Run**：

```
go test ./internal/agent/opencode/... ./internal/agent/... -count=1
```

**注意**：`SupportedStatuses()` 不該變動（因為新加條目都 empty `EmitsStatus`），跑 `internal/agent/supported_statuses_test.go` 確認 cc/codex/opencode 三家 supported set 不變。

### Commit 3 — `feat(agent/opencode): report hook supported version`

**Red**：OC4 / OC5 失敗

**Green**：

- 加 `opencodeHooksSupportedVersion = "1.14.23"`
- 5 個 CheckHooks return path 補 `SupportedVersion` + `ExceedsSupport`
- 加 OC4 table test（5 path 全綠）
- 加 OC5 ExceedsSupport 三個 case

**Run**：

```
go test ./internal/agent/opencode -count=1
```

### Commit 4 — `test(agent/opencode): document template event contracts`

**Red**：OC1a 失敗（fixture 不存在）

**Green**：

- 建 `internal/agent/opencode/testdata/opencode-1.14.23-*` 結構
- 建 `version.txt` / `source.md` / `events.json` / `payloads/<event>.json`
- 加 OC1a test：對每個 `plugin_template.go` 訂閱的 event，load fixture payload，斷言 template 讀的 field 都存在

**Run**：

```
go test ./internal/agent/opencode -count=1
```

### Commit 5 — `fix(agent/opencode): refresh plugin event mapping` (**conditional**)

**前置**：Commit 1 audit 報告判定至少一個 event key stale

**Red**：OC1 失敗

**Green**：

- 依 Commit 1 報告換鍵
- 更新 `plugin_template.go` 對應 callback name + `source:` 欄位
- 加 OC1 test：mock 1.14.23 plugin runtime payload → handler 正確 spawn `pdx hook`

**Run**：

```
go test ./internal/agent/opencode -count=1
```

**Skip 條件**：若 audit 報告顯式 declare「無需 plugin template refresh」，跳過此 commit。

### Commit 6 — `test(spa): guard opencode agent icon`

**Red**：OI1 / OI2 失敗（test file 沒覆蓋 opencode）

**Green**：

- `spa/src/lib/agent-icons.test.tsx` 加 opencode describe block
- OI1 / OI2 兩個 test

**Run**：

```
pnpm --prefix spa exec vitest run src/lib/agent-icons.test.tsx
```

**注意**：production icon support 已在 alpha.230 baseline 存在；OI1/OI2 屬 coverage guard，可能 baseline 即綠。

### Final Verification

```
go test ./internal/agent/... -count=1
pnpm --prefix spa exec vitest run src/lib/agent-icons.test.tsx
```

PR 提交前：

```
go test ./... -count=1
pnpm --prefix spa run lint
pnpm --prefix spa run build
```

---

## 5. 不做（明列 boundary）

1. **不動 cc / codex catalog**（已在 PR #655 完成）
2. **不動 hooks-hotfix-plan PR 3「Remaining Claude Strictness」**（推論已被 PR #645 涵蓋；本 plan 不重做）
3. **不動 `internal/module/agent/*`** — 屬 PR-4a-1/2
4. **不動 `internal/agent/probe/*`** — 屬 PR-4a-1
5. **不動 `internal/tmux/executor.go`** — 屬 PR-4a-1 Slice 0
6. **不動 `internal/agent/opencode/status.go`** — frame-layer concern，不在 hook surface
7. **不改 cc / codex 的 trace observability**（PreToolUse 等從 ignored 升 detail）— 屬獨立 issue，與 Phase 4a 無關
8. **不處理 codex 5 個 FutureOnly 假想 events**（SubagentStart / SubagentStop / StopFailure / Notification / SessionEnd）— 屬獨立 issue

---

## 6. Ship gate（PR-4a-0）

PR-4a-0 ship 必須全綠：

| 項 | 條件 |
|---|---|
| Audit 報告完整 | Commit 1 文件覆蓋 §2.1 列的 4 章節 + 3 個顯式判定 |
| HC5 / HC5b / HC5c | catalog 完整對齊上游 1.14.23（依 audit 報告）|
| OC4 / OC5 | SupportedVersion 5 path 全綠 |
| OC1a | 所有 plugin 訂閱 event 都有 fixture |
| OC1（conditional） | 若 §2.5 改動則綠 |
| OI1 / OI2 | SPA test 綠 |
| `go test ./...` 全綠 | 含 `internal/agent/supported_statuses_test.go` |
| `pnpm --prefix spa run lint` 綠 | |
| `pnpm --prefix spa run build` 綠 | |
| Codex review 兩輪 | 標準 + 3-parallel adversarial（per CLAUDE.md PR Review 兩輪制）|

---

## 7. PR-4a-1 / PR-4a-2 大綱（待 PR-4a-0 對齊結果定稿）

**為何不在本 plan v1 細寫**：

PR-4a-0 §2.1 對齊報告的兩個關鍵判定（`chat.message` / `session.idle`）會直接影響 PR-4a-2 Slice 6（opencode module 接新 primitive）的設計：
- 若 `chat.message` stale 且 5c 換成 `xxx` event → opencode 的 Running transition trigger 路徑變了
- Slice 6 的 module orchestrator 邏輯需要對照新的 trigger pattern

待 PR-4a-0 ship + 對齊報告定稿後，再起草 plan v2（涵蓋 PR-4a-1 + PR-4a-2 完整細節）。

### 7.1 PR-4a-1（probe primitive + cc + helper + dev log）大綱

| Slice | 範圍 | 估 LoC | 估 tests |
|---|---|---|---|
| 0 | tmux API 擴展（`CapturePaneRange(target, start, end)` 並存方案）+ fake executor 對應 | ~30 | 3 |
| 1 | Probe primitive 重構（`WatchFullScreen` / `WatchTopLines` + `watchLoop` + `ScreenChangeEvent`）；移除 `ActivitySignal` enum 的 signal 解讀職責 | ~80 | 6 |
| 2 | ShellPrompt utility 保留 probe 層（不動） | 0 | 0 |
| 3 | Module 共用 orchestrator helper（dead-PID + sweep + ErrorGuard） | ~60 | 5 |
| 4 | cc module 接新 primitive | ~40 | 5 |
| 7 | graceWindow + `PDX_DEV_MODE=1` observation log + expvar `purdex_probe_*` | ~30 | 4 |

**淨 +240 LoC + 23 tests**。

### 7.2 PR-4a-2（codex + opencode + 清舊）大綱

| Slice | 範圍 | 估 LoC | 估 tests |
|---|---|---|---|
| 5 | codex module 接新 primitive | ~30 | 4 |
| 6 | opencode module 接新 primitive（**依賴 PR-4a-0 對齊結果**）| ~30 | 3 |
| 8 | 清舊 onActivityDetected / shouldWatchActivity / ActivitySignal enum signal 解讀 | -60 | (移除既有) |

**淨 0 LoC + 7 tests**（清舊 offset 加新）。

---

## 8. Risk

| Risk | Mitigation |
|---|---|
| §2.1 audit 範圍過大（OpenCode source 變動快，30+ events 全梳理）| 限定 1.14.23 tag，不追 dev branch；audit 用 subagent 平行查（一支 strong hooks / 一支 bus events / 一支 message flow），各回 enumerated list 後主 Claude 整合 |
| §2.4 fixture payload 與 1.14.23 runtime 不符 | 若 audit 階段無法取得真實 runtime 樣本，先用 source-derived schema；附 `source.md` 註明來源；在後續 PR 真實對接時補 runtime fixture |
| §2.5 conditional 換鍵牽動 Slice 6 設計 | 本 plan 顯式 declare PR-4a-2 待 PR-4a-0 ship 後再起草，避免 plan 寫好但 trigger pattern 變了要重寫 |
| Codex sandbox 無網路（feedback_codex_sandbox_no_install）| Commit 2-6 主 Claude 必須手動跑 `pnpm install` + vitest + lint + build；Codex review 不可作為 SPA 驗證來源 |
| 主 repo 並發 session（feedback_concurrent_session_safety）| 進 worktree 前 `git fetch origin main && git reset --hard origin/main`；commit 後 push 前再 `git pull --rebase origin main` 一次 |

---

## 9. LOC 預估

| Commit | 估 LoC | 估 tests |
|---|---|---|
| 1: audit 報告（純文件）| ~300 (新文件) | 0 |
| 2: catalog 補完 | ~80 (events.go) | 4 (HC5 + HC5b + HC5c)|
| 3: SupportedVersion | ~30 (hooks.go) | 4 (OC4 含 5-path table + OC5 三 case)|
| 4: provenance fixtures | ~150 (testdata + helpers) | 6 (OC1a per-event)|
| 5: plugin refresh (conditional) | 0~40 | 0~3 (OC1) |
| 6: SPA icon test | ~30 | 2 (OI1 + OI2)|

**總計**：**~590~660 LoC + 16~19 tests**（含新文件 ~300 LoC）

純 Go/TS code 改動：~290~360 LoC + 16~19 tests。屬中型 PR。

---

## 10. 結束條件（PR-4a-0）

**Ship**：

- §6 ship gate 全綠
- PR merged
- 對應 main bump PR ship（VERSION 進到 alpha.231）

**Memory 更新**：

- `kickoff_lights_rebuild.md` 更新「目前進度」段落，記 PR-4a-0 ship 與 §2.1 對齊報告路徑
- `project_progress.md` 更新

**下一階段**：起草 Phase 4a plan v2（涵蓋 PR-4a-1 + PR-4a-2 完整細節，引用 PR-4a-0 對齊報告作為 Slice 6 設計依據）

---

## 11. 文獻

- Audit issue: [#656 v5](https://github.com/wake/purdex/issues/656)
- Hooks hotfix plan: `docs/specs/2026-04-25-agent-hooks-hotfix-plan.md`（PR 1+2 已 ship；PR 4/5/6 收編進本 plan PR-4a-0）
- Lights rebuild spec: `docs/specs/2026-04-23-lights-rebuild-spec.md` §2.4 / §8.1 / §71
- 前置 phase: PR #644（3.5a）/ PR #650（3.5b）/ PR #645（hotfix PR 1）/ PR #655（hotfix PR 2）
- 待產出：`docs/specs/2026-04-26-opencode-1.14.23-hook-audit.md`（Commit 1）
- 待產出：`docs/specs/2026-04-26-lights-rebuild-phase-4a-plan.md` v2（PR-4a-0 ship 後）
