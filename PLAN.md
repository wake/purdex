# Editor 模組自有資產化 + 開檔體驗強化 — Implementation Plan (Index)

> **Per-phase plan 拆檔**（A 決議 (ii)）：本檔僅作 index + 共通契約。各 phase 詳細 task 見 `plans/P*.md`。
>
> **For agentic workers:** REQUIRED SUB-SKILL: Use `superpowers:subagent-driven-development`（推薦）或 `superpowers:executing-plans` 逐 task 執行。Steps 用 checkbox (`- [ ]`) 追蹤。
>
> **Subagent CWD 強制**：本 worktree 在 `/Users/wake/Workspace/wake/purdex/.claude/worktrees/worktree-editor-self-contained/`。subagent 每個 Bash 指令必須 `cd <worktree-path> && ...` 前綴（per `feedback_subagent_cwd_enforcement.md`）。

**Goal:** 把 Editor module 的 file opener / 設定從 `register-modules.tsx` body 收編進 module definition；改 tab 插入為「append after current」；建立 agent-driven path cache；補檔案不存在的 popup + 三層 fallback 搜尋。

**Architecture:** SPA 的 `ModuleDefinition` 加 `fileOpeners` 並用 `useModuleEnabledStore` 過濾；新增 `PathHint` v1 minimal event 走既有 `core.HostEvent` 廣播管道；`tryOpenFile` 走「stat (ENOENT-only) → cache lookup（含 stat verify + prune）→ popup → daemon fs.search（server-side allowlist）」管線。

**Tech Stack:** React 19 / Zustand 5 / Vitest / Go net/http / gorilla/websocket。

**Spec:** [SPEC.md](./SPEC.md)（rev 6）。

---

## Per-Phase Plan 索引

| Phase | Plan 檔案 | 主要範圍 | review 大小 | 依賴 |
|---|---|---|---|---|
| **P1** | [plans/P1-register-modules-fileopeners.md](plans/P1-register-modules-fileopeners.md) | `register-modules.tsx` 拆檔 + `Module.fileOpeners` interface + owner-scoped registry + Editor 收編 + 通用 `DisabledModulePlaceholder` + PaneLayoutRenderer fallback wiring | 中 | 無 |
| **P2** | [plans/P2-tab-insert.md](plans/P2-tab-insert.md) | Tab 插入改 append-current（泛用化 `findInsertTarget` 進 `lib/tab-insert/`） | 小 | 無 |
| **P3** | [plans/P3-link-detection.md](plans/P3-link-detection.md) | Link detection 三個檔案路徑開關搬 Editor settings（i18n 改 `locales/`） | 小 | 無 |
| **P4** | [plans/P4-path-cache.md](plans/P4-path-cache.md) | daemon `PathHint` v1 minimal channel + dedup-by-(session,dir,basename) + CC HookInstaller + SPA path cache store + auto-cleanup（dispose-aware） | 中 | 無 |
| **P5** | [plans/P5-popup-fs-search.md](plans/P5-popup-fs-search.md) | File-not-found popup service（HMR/cancellation safe）+ Layer 1/2/3 整合 + daemon `fs.search` server-side allowlist | 中 | P1 + P4 |

P1-P4 互相獨立可平行 review；P5 用到 P1 的 opener pipeline、P4 的 path cache。

---

## v4 修訂概覽

本 PLAN 已吸收 PLAN 第二輪 4 份 codex review（task-mogr65md / mogrm8zk / mogrkjal / mogrl2l6）共 33 findings + ABCD 鎖定決議。每 phase plan 頂端有「v4 修訂指引」表格列出對該 phase 的所有 deviation；實作前必看。

關鍵架構鐵則（跨 phase 不變）：

- **lib → UI 反向依賴永禁**：`spa/src/lib/` 不准 import `spa/src/components/`（`module-registry.ts` 不可 import `DisabledModulePlaceholder`；fallback 在 render 層注入）
- **WS payload privacy**：PathHint 廣播 payload 不含完整 `path`、不含 `basename`，只 dir 級
- **fs API host-bound**：跨 host 操作必須走 host-id-aware backend factory，不取 active host 捷徑；錯誤分類嚴格只 ENOENT/404 視為 missing
- **fs.search server-side allowlist**：不接受 client-supplied absolute path roots，只接 `session-cwd` / `workspace-projectPath` capability
- **mandatory excludes union**：daemon 端硬編碼 excludes 不可被 client `[]` 覆蓋
- **schema versioning**：所有跨進程 schema 帶 `schemaVersion`；rehydrate 時 unknown 一律 defensive drop
- **localStorage key 含版本後綴**：如 `PATH_CACHE_V1`
- **commit message lowercase**

---

## 驗證指令（每 task 結尾依需要跑）

```bash
# SPA 單元測試（單檔）
cd spa && npx vitest run path/to/file.test.ts

# SPA 全測 + lint + build（**Codex sandbox 無網路；主 Claude 必須手動跑驗證**，per feedback_codex_sandbox_no_install.md）
cd spa && pnpm install && npx vitest run && pnpm run lint && pnpm run build

# Go 測試
go test ./...

# Go 單包測試
go test ./internal/module/agent/...
go test ./internal/module/fs/...
```

## TDD 模式（每 task 適用）

1. 寫失敗的測試
2. 跑測試 → 預期 fail（with specific error）
3. 寫最小實作
4. 跑測試 → 預期 pass
5. Commit（conventional commit format **lowercase**，逐 task 獨立）

> 小 task（純 type/fixture 變更）允許同一 behavioral slice 合併 2-3 個 micro-step；但失敗測試不能省（防守 review #12）。

---

## Phase / PR 對應關係

| Phase | PR 標題草稿 | 兩輪 codex review |
|---|---|---|
| P1 | `feat(spa): editor module owns file openers + disabled placeholder` | 標準 + adversarial |
| P2 | `refactor(spa): tab insertion appends after current` | 標準 + adversarial |
| P3 | `refactor(spa): migrate file path link detection settings to editor` | 標準 + adversarial |
| P4 | `feat(daemon+spa): agent path hint channel + path cache store` | 標準 + adversarial |
| P5 | `feat(spa+daemon): file-not-found popup with three-layer fallback` | 標準 + adversarial |

每 Phase 結束 = 1 個 PR；merged 後再下一 Phase。Phases 內 task 都獨立 commit。每個 phase 必跑 verification gate（vitest + lint + build + go test）才能 PR。

---

## File Structure 變更總覽（v4 子目錄化版本）

| 路徑 | 動作 | 階段 |
|---|---|---|
| `spa/src/lib/module-registry.ts` | modify（加 `fileOpeners` + `disabledComponent` opt-in；`resolvePaneRenderer` 回 metadata；**禁 import component**） | P1 |
| `spa/src/lib/file-opener-registry.ts` | modify（owner-scoped：`ownerModuleId` + `unregisterByOwner`） | P1 |
| `spa/src/lib/register-modules.tsx` | modify（縮成 shim → `export * from './register-modules'`） | P1 |
| `spa/src/lib/register-modules/index.tsx` | create（orchestrator < 80 行） | P1 |
| `spa/src/lib/register-modules/editor-module.tsx` | create（含 P3 settings + P5 popup deps） | P1, P3, P5 |
| `spa/src/lib/register-modules/fs-backends.tsx` | create | P1 |
| `spa/src/lib/register-modules/module-file-openers.ts` | create（apply 流程） | P1 |
| `spa/src/components/modules/DisabledModulePlaceholder.tsx` | create（**子目錄**） | P1 |
| `spa/src/components/modules/DisabledModulePlaceholder.test.tsx` | create | P1 |
| `spa/src/components/PaneLayoutRenderer.tsx` | modify（line 28：`resolvePaneRenderer` + disabled fallback wiring） | P1 |
| `spa/src/components/PaneLayoutRenderer.test.tsx` | create | P1 |
| `spa/src/lib/tab-insert/find-insert-target.ts` | create（**子目錄**，rename target） | P2 |
| `spa/src/lib/tab-insert/find-insert-target.test.ts` | create | P2 |
| `spa/src/lib/find-browser-insert-target.ts` | rename → `lib/tab-insert/find-insert-target.ts` | P2 |
| `spa/src/lib/open-browser-tab.ts` | modify | P2 |
| `spa/src/stores/useTabStore.ts` | modify（**不加 wsState.insertTab**；caller 自行 insert） | P2 |
| `spa/src/lib/terminal-link/openers/file-path.ts` | modify | P2, P5 |
| `spa/src/components/FileTreeView.tsx` | modify（移除既有 `getDefaultOpener + openSingletonTab` 直接呼叫） | P2, P5 |
| `spa/src/components/settings/LinkDetectionSection.tsx` | modify（縮減為 bare） | P3 |
| `spa/src/components/settings/editor/EditorLinkDetectionSection.tsx` | create（**子目錄**） | P3 |
| `spa/src/locales/zh-TW.json` / `en.json` | modify（**不是 `i18n/`**） | P3 |
| `internal/module/agent/path_hint.go` | create（**v1 minimal schema**） | P4 |
| `internal/module/agent/path_hint_test.go` | create（4.3b emit integration） | P4 |
| `internal/module/agent/path_hint_extractor.go` | create（純函式） | P4 |
| `internal/module/agent/path_hint_extractor_test.go` | create（4.3a unit） | P4 |
| `internal/module/agent/handler.go` | modify（從 `req.RawEvent` decode + emit） | P4 |
| `spa/src/lib/storage/keys.ts` | modify（加 **`PATH_CACHE_V1`**） | P4 |
| `spa/src/types/agent-events.ts` | modify（加 `PathHint` v1 type） | P4 |
| `spa/src/lib/agent-ws/index.ts` | create（router 入口） | P4 |
| `spa/src/lib/agent-ws/status-dispatch.ts` | create（既有 status 邏輯搬入） | P4 |
| `spa/src/lib/agent-ws/path-hint-dispatch.ts` | create | P4 |
| `spa/src/lib/agent-ws/resolve-workspace-id-for-agent-session.ts` | create（**重命名版** helper） | P4 |
| `spa/src/lib/agent-ws-dispatch.ts` | modify（縮成 shim → `export * from './agent-ws'`） | P4 |
| `spa/src/hooks/useMultiHostEventWs.ts` | modify（**whitelist 三條**） | P4 |
| `spa/src/stores/path-cache/usePathCacheStore.ts` | create（**子目錄**；含 `add()` normalization + `storage: purdexStorage`） | P4 |
| `spa/src/stores/path-cache/usePathCacheStore.test.ts` | create | P4 |
| `spa/src/stores/path-cache/auto-cleanup.ts` | create（dispose function + hydration race） | P4 |
| `spa/src/stores/path-cache/auto-cleanup.test.ts` | create | P4 |
| `internal/module/fs/search_engine.go` | create（**5.1a 純函式**） | P5 |
| `internal/module/fs/search_engine_test.go` | create | P5 |
| `internal/module/fs/search_handler.go` | create（**5.1b method `(m *FsModule).handleSearch`**） | P5 |
| `internal/module/fs/search_handler_test.go` | create | P5 |
| `internal/module/fs/module.go` | modify（route `m.handleSearch`） | P5 |
| `spa/src/lib/file-open/fs-search.ts` | create（**子目錄**） | P5 |
| `spa/src/lib/file-open/open-file.ts` | create（**service factory + host-bound**） | P5 |
| `spa/src/lib/file-open/open-file.test.ts` | create | P5 |
| `spa/src/lib/file-open/file-not-found-popup-service.tsx` | create（**改名**；HMR + AbortController） | P5 |
| `spa/src/lib/file-open/file-not-found-popup-service.test.tsx` | create | P5 |
| `spa/src/components/editor/popups/FileNotFoundPopup.tsx` | create（**子目錄**） | P5 |
| `spa/src/components/editor/popups/FileNotFoundPopup.test.tsx` | create | P5 |
| `spa/src/components/settings/editor/EditorOpenBehaviorSection.tsx` | create（**子目錄**） | P5 |
| `spa/src/stores/useUISettingsStore.ts` | modify（加 `popupOnMissingFile` / `autoSearchLayer1`） | P5 |
| `spa/src/__tests__/editor-open-flow.integration.test.tsx` | create（跨 phase regression） | P1 起，逐 phase 補滿 |
