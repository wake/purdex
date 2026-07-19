# Purdex Web P4 — Electron-only / dev-only 降級 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 確保所有 Electron-only / dev-only 功能在 web（純瀏覽器）**不出現、不報錯**；補上唯一漏網的 dev-only 入口 gate。

**Architecture:** 經完整稽核（見下），現有 capability 模型（`getPlatformCapabilities`）已把絕大多數 Electron-only 功能安全 gate。唯一真漏網是 `tmux-agent-monitor` settings section——其條件 `import.meta.env.DEV || caps.devUpdateEnabled` 的裸 `DEV` 分支在 **web dev build** 會註冊該入口（不 throw，走 host-api HTTP，但 UI 洩漏；production web build 已正確隱藏）。本 phase 補上 `isElectron` gate。

**Tech Stack:** React 19 / TypeScript / Vitest。

## 稽核結論（P4 依據）

| 項目 | 狀態 | gate |
|---|---|---|
| **tmux-agent-monitor** | **GAP** | `index.tsx:392` 裸 `import.meta.env.DEV` 缺 isElectron → web dev 洩漏 |
| memory-monitor | SAFE | `getProcessMetrics` 雙重 guard（`MemoryMonitorPage.tsx:174/184`），web 客端指標為空、不 throw；模組本身跨平台（host-api 指標） |
| browser pane | SAFE | `BrowserPane.tsx:23/30` `if(!window.electronAPI) return` + `:56` fallback；new-tab provider `disabled:!caps.canBrowserPane`；restore 於 web 顯示 fallback、不 throw |
| tear-off / merge | SAFE | context menu 由 `caps.canTearOffTab`/`window.electronAPI` 條件顯示；handler `if(!window.electronAPI) break/return` |
| useElectronIpc | SAFE | 各 effect `if(!window.electronAPI...) return` 早退 |
| dev-update / DevEnvironmentSection | SAFE | `if(caps.devUpdateEnabled)`（= isElectron），web dev+prod 皆隱藏 |
| system tray / ElectronSection | SAFE | `if(caps.canSystemTray)` |
| 其餘 electronAPI 呼叫 | SAFE | 皆 optional-chaining 或 guard 後 |

→ **唯一需修：tmux-agent-monitor。**

## Global Constraints

- 修正僅加 gate、不改 tmux-agent-monitor 功能本身。
- 測試：`cd spa && npx vitest run`。單一 task、單一 commit。

---

## File Structure

- **Modify** `spa/src/lib/register-modules/index.tsx` — tmux-agent-monitor 註冊條件加 `caps.isElectron`。
- **Modify** `spa/src/lib/register-modules.test.ts` — 補「web（無 electronAPI）即使 DEV 也不註冊 tmux-agent-monitor」測試。

---

## Task 1: gate tmux-agent-monitor 於 Electron

**Files:**
- Modify: `spa/src/lib/register-modules/index.tsx`
- Test: `spa/src/lib/register-modules.test.ts`

- [ ] **Step 1: 寫失敗測試**

於 `register-modules.test.ts` 沿用既有 pattern（參考該檔既有 `does not register electron section when no electronAPI` 測試——它在無 `window.electronAPI` 下 `registerBuiltinModules()` 後斷言某 settings section 不存在；使用相同的 settings-section 查詢方式），新增：

```ts
it('does not register tmux-agent-monitor on web (no electronAPI) even in DEV', () => {
  // 確保 web 情境：無 electronAPI（vitest 預設環境即 web；import.meta.env.DEV 為 true）
  delete (window as unknown as { electronAPI?: unknown }).electronAPI
  registerBuiltinModules()
  // 以該檔既有查詢 settings sections 的方式，斷言不含 'tmux-agent-monitor'
  const ids = getRegisteredSettingsSectionIds() // ← 對齊該檔既有取得 section id 的方式
  expect(ids).not.toContain('tmux-agent-monitor')
})
```

> 註：`getRegisteredSettingsSectionIds()` 為佔位，實作時改用 `register-modules.test.ts` 既有測試取得 settings sections 的實際 API（例如既有 `does not register electron section` 測試所用的 registry getter / `dispatchSettingsContributions` 快照）。同檔可另加一個 Electron 情境（設 `window.electronAPI = { getAppInfo: ... }` 或最小 stub）斷言 **有** 註冊，以對稱鎖住行為（若既有測試 harness 便於設置）。

- [ ] **Step 2: 跑測試確認失敗**

Run: `cd spa && npx vitest run src/lib/register-modules.test.ts`
Expected: FAIL（現況 web dev 仍註冊 tmux-agent-monitor）。

- [ ] **Step 3: 實作**

`index.tsx:392`：

```tsx
  if ((import.meta.env.DEV || caps.devUpdateEnabled) && caps.isElectron) {
    registerSettingsSection({
      id: 'tmux-agent-monitor',
      label: 'settings.section.tmux_agent_monitor',
      order: SETTINGS_ORDER.TMUX_AGENT_MONITOR,
      component: TmuxAgentMonitorSection,
    })
  }
```

（`caps.devUpdateEnabled` 已隱含 `isElectron`，故加 `&& caps.isElectron` 對 Electron 行為不變，只擋掉 web dev 的裸 `DEV` 洩漏。）

- [ ] **Step 4: 跑測試確認通過 + 全套回歸**

Run: `cd spa && npx vitest run src/lib/register-modules.test.ts`
Expected: PASS。
Run: `cd spa && npx vitest run`
Expected: 全綠（Electron 情境的既有測試不受影響——Electron 下條件仍為真）。

- [ ] **Step 5: Commit**

```bash
cd /Users/wake/Workspace/wake/purdex/.claude/worktrees/web-version
git add spa/src/lib/register-modules/index.tsx spa/src/lib/register-modules.test.ts
git commit -m "fix(web): gate tmux-agent-monitor dev tool behind isElectron (P4)"
```

---

## 收尾驗證（全 task 完成後，主 Claude 執行）

- [ ] `cd spa && npx vitest run` — 全綠。
- [ ] `cd spa && pnpm run lint` — 無新增錯誤。
- [ ] **交付使用者手動驗證**（部署後）：於 `https://purdex.mlab.host/`（web）開 Settings，確認無 tmux-agent-monitor / dev-environment / electron(tray) 入口；開 DevTools console 確認無 `window.electronAPI` undefined 造成的錯誤；browser-pane / memory-monitor 若經 restore 顯示 fallback/空指標而非崩潰。

---

## Self-Review 對照 spec

- **spec §5.4 / §4 Tier C：Electron-only 功能 web 乾淨隱藏（browser pane / local FS / tear-off / tray / memory monitor / dev update）** → 稽核確認**皆已安全 gate**（見稽核結論表）。✅
- **spec §5.4 已知 dev-only 例外 tmux-agent-monitor** → Task 1 補 isElectron gate。✅
- **spec §5.4 web 無 electronAPI undefined 錯誤** → 稽核確認所有 electronAPI 呼叫皆 guard/optional-chaining。✅
- 無 placeholder（`getRegisteredSettingsSectionIds` 佔位已註明對齊既有 test API）。
