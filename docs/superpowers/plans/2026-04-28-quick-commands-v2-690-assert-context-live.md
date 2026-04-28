# Plan: Quick Commands v2 — `assertContextLive` enforcement (#690)

**Source**: spec `2026-04-27-quick-commands-v2-design.md` §3.3.1（spec patch 同 PR）
**Issue**: [#690](https://github.com/wake/purdex/issues/690)
**Phase**: Pre-Phase 1b'（小型 enforcement，不屬 1b' 本體）
**Branch**: `feat/quick-commands-v2-assert-context-live-required`（off `origin/main` @ `3c999acf` alpha.240）
**Estimate**: 1 commit / ~30 行 diff

## Context recap

PR #686 codex round-5（final approve）建議 followup：把 `runWorkspaceSlot` 的 `Deps.assertContextLive` 從 optional 改 required，避免未來 workspace-context caller 漏 wire（Phase 1b' Plus hover popover 是首個 high-risk 場景）。

選 **Option 1（type-level）**，理由：
- Compile-time 擋住，比 grep test（Option 2）穩
- Phase 1c HOST_ACTIONS 自然分流為 `runHostSlot` — 強制好設計
- 改動表面 < 30 行，無 behavior change（caller 已 wire）

不選 Option 2 / 3：
- Option 2 grep test 會被 `// eslint-disable` 或檔名移動繞過
- Option 3 lint plugin 是 overkill，純為 1 個 callsite 寫 plugin

## 變更範圍

### 1. `spa/src/lib/slot-executor.ts`

```diff
 interface Deps {
   switchToSession: (hostId: string, sessionCode: string) => void
   resolveHostId: () => Promise<string | null>
-  /**
-   * Optional — HOST_ACTIONS callers (Phase 1c) don't have a workspace context
-   * to verify and pass `undefined` to opt out of this check.
-   */
-  assertContextLive?: () => boolean
+  /**
+   * Required — workspace liveness probe. Returns false if the surrounding
+   * UI context (e.g. workspace) was destroyed while async work was in flight.
+   * The executor calls this after `createSession` resolves and BEFORE
+   * `executeCommand` runs so destructive commands aren't sent to a session
+   * the user can no longer reach.
+   *
+   * Phase 1c HOST_ACTIONS: host context has no workspace to verify and will
+   * use a separate `runHostSlot` entry point (spec §3.3.1).
+   */
+  assertContextLive: () => boolean
 }
```

```diff
-  if (deps.assertContextLive && !deps.assertContextLive()) {
+  if (!deps.assertContextLive()) {
     toast.show(t('quick_commands.toast.switch_failed'))
     return
   }
```

註解的 "shared with Phase 1c host entry" 改寫為「workspace-only entry; Phase 1c will introduce a sibling `runHostSlot`」。

### 2. `spa/src/lib/slot-executor.test.ts`

7 個既有 test 補 `assertContextLive: () => true`（負控；保留 happy-path 行為）：
- `happy path` (line 40)
- `hostId null → invokes resolveHostId` (line 64)
- `hostId null → user cancels picker` (line 80)
- `createSession failure` (line 98)
- `send-keys failure` (line 127)
- `switchToSession failure` (line 162)
- `combined send-keys + switch failure` (line 249)

加一個 type-level test。寫法（codex round-1 M1 — `@ts-expect-error` 必須直接放在會報錯的那一行；放在物件內結尾會壓不到「缺 key」的 diagnosis）：

```typescript
it('requires assertContextLive at type level (#690 / spec §3.3.1)', () => {
  // Future workspace-context callers (e.g. Phase 1b' popover) cannot silently
  // regress to round-4-vulnerable shape without this @ts-expect-error firing.
  // The directive sits on the `const` line — that's where TS reports the
  // "missing required property 'assertContextLive'" error; if we ever add
  // assertContextLive to the literal below, this test breaks loudly to flag
  // the regression.
  // @ts-expect-error - assertContextLive is required (#690)
  const deps: Parameters<typeof runWorkspaceSlot>[2] = {
    switchToSession: () => {},
    resolveHostId: async () => null,
  }
  expect(deps).toBeDefined()
})
```

包進 `it()` block 是為了：
1. 避免 `noUnusedLocals` 編譯錯誤（型別檢查已執行，runtime 用 `expect(deps).toBeDefined()` 帶過）
2. 讓 vitest 計入 test count（regression 時 vitest output 會顯示 fail）
3. 統一在 `describe('runWorkspaceSlot')` 區塊內

### 3. `WorkspaceQuickCommandsContextMenu.tsx`

無變更 — caller 已 wire `assertContextLive`（PR #686 round-4 fix `0b23593e` 引入）。

### 4. spec `2026-04-27-quick-commands-v2-design.md`

§3.3.1 已加（同 PR）— 內含 Why / Enforcement / Phase 1c 影響 / 驗收。

## 驗證指令

於 worktree root：

```bash
cd spa
pnpm install --frozen-lockfile  # 若需要
pnpm run build                       # 等同 tsc -b + vite build；包含完整 src 型別檢查（codex round-1 L1）
pnpm vitest run lib/slot-executor   # 7+1 test 全綠
pnpm run lint                        # 無新 warning
```

`pnpm exec tsc --noEmit` 不寫進步驟 — `spa/tsconfig.json` 是 project-reference root（`files: []`），單獨跑 `--noEmit` 不會檢查 `src/`，反而給錯誤的安全感。`pnpm run build` 內部的 `tsc -b` 會實際編譯 referenced projects（`tsconfig.app.json` + `tsconfig.node.json`），可一次涵蓋 type check + build 驗證。

無 daemon 改動，免跑 `go test`。

## 風險評估

| 風險 | 等級 | mitigation |
|---|---|---|
| caller 編譯失敗 | 低 | 已盤點：production 1 個 caller 已 wire；test 7 個會補 |
| 既有 test 行為改變 | 極低 | `() => true` 是負控，等價於原 `undefined` 略過 guard 路徑；toast assertion 不變 |
| Phase 1c 設計被綁死 | 低 | spec §3.3.1 明示「分流形狀以 1c 寫到時為準」，本次不預先決定 generic 還是兩個 function |
| ts-expect-error 反向 fail（key 已加導致 expect-error 不再觸發） | 中 | 用 `Parameters<typeof runWorkspaceSlot>[2]` 動態取型，避免 hardcode interface |
| Type escape hatch 繞過 enforcement（codex round-1 L2） | 中 | type-level required 擋正常 caller 但擋不住 `as any` / `as unknown as Deps` / `Object.assign(base, payload as any)` 等。code review checklist 必含「新增 `runWorkspaceSlot` caller 是否用 type cast 繞過 deps 完整性」；spec §3.3.1 之外另在 PR description 提示 reviewer |

## 回滾

單一 commit revert 即可；caller 端不需改。

## Phase 順序檢查

依 `2026-04-27-quick-commands-v2.md` plan §順序檢查清單：

- [x] 1a — alpha.236
- [x] (prep) #679 — alpha.238
- [x] 1b — alpha.240
- [ ] **#690 enforcement（本 PR）** ← 在此插入
- [ ] 1b' — Plus hover popover
- [ ] 1c — HOST_ACTIONS chip

#690 不是新 phase，是 1b ship 後 1b' 之前的 type-safety 強化。1b' 的 popover caller 將直接 benefit 於本次 enforcement（漏 wire 即編譯 fail）。

## Codex review 關注點建議

- **Round 1（standard）**：spec §3.3.1 / plan / diff 連貫性；type-level test 寫法是否 robust
- **Round 2（3 parallel）**：
  - **Attacker**：能否繞過 enforcement？例如 `as any` cast、`satisfies` 後 spread、interface widening
  - **Defender**：是否阻擋了合法用例？optional 是否真的不必要（HOST_ACTIONS 之外還有別的 path 嗎）
  - **File-quality**：slot-executor.ts SRP 是否被影響；test 檔加 `_typeCheck_*` 的 noise 是否可接受
