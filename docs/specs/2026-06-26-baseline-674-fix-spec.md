# Spec — Fix #674: baseline test failures (sync hosts token + TabBar pinned tooltip)

- **Issue**: #674（`bug` / `test`，origin/main 既有，predates #863）
- **Base**: alpha.298（`661cc892`）
- **Scope**: `spa`（sync hosts contributor + 型別 + TabBar test）
- **Status**: draft → codex review

修兩個**獨立**的 baseline 失敗（共 4 個 test）。決策已定：token 清除 sentinel 採 **`null`**。

## A. Sync `hostsContributor.deserialize` — token 清除契約（3 個 test）

### 現況（三方不一致）
- **Test** 期望 `token` 為 `null`（`.toBeNull()`）：(1) host 只存在於 incoming snapshot（本地無）；(2) id 撞名但 `ip` 不同；(3) id 撞名但 `port` 不同。安全語意：endpoint 身分不符必須強制 re-auth，避免 bearer token 送到 attacker-controlled daemon。
- **Impl**（`spa/src/lib/sync/contributors/hosts.ts:63`）：`token: sameEndpoint ? currentHost.token : undefined` —— 寫 `undefined`。
- **Type**（`spa/src/stores/useHostStore.ts` `HostConfig`）：`token?: string` —— 連 `null` 都不允許。

### 修法（採 `null` sentinel）
1. `HostConfig.token` 型別 `token?: string` → `token?: string | null`。`null` = 明確「已清除，需 re-auth」，且 JSON 序列化會保留（`undefined` 會被 drop）。
2. `hosts.ts:63` `: undefined` → `: null`。
3. 既有 3 個 test 不動（已 assert `null`），轉綠。
4. **Ripple 收斂於 store/sync 邊界**：下游只接受 `string | undefined` 的 sink，於 call site coalesce `?? undefined`，不改其簽章、不讓 `null` 語意外溢到 UI/health 層（codex spec review P1：`DevEnvironmentSection` 為已知型別破口，須列入；`tsc` 已權威確認以下為完整集合）：
   - `spa/src/hooks/useMultiHostEventWs.ts:79` 餵給 `checkHealth(getToken?: () => string | undefined)`：`...hosts[hostId]?.token` → `... ?? undefined`。
   - `spa/src/components/hosts/OverviewSection.tsx:139` 餵給 `TokenField(token?: string)`：`token={host.token}` → `token={host.token ?? undefined}`。
   - `spa/src/components/settings/DevEnvironmentSection.tsx:35` 的 `token` selector 流入 `streamCheck`(`:184`) / `applyUpdate`(`:245`)，其 Electron API 簽章（`electron.d.ts:114`）僅收 `string | undefined`：於 selector coalesce `?? undefined`，一次收斂兩個 call site。
5. **`addHost` opts**（`useHostStore.ts:43`）`token?: string` → `token?: string | null`：`host-lifecycle.ts:145` 的 host restore 直接傳整包 `HostConfig`（其 cleared token 應維持 `null`），否則 `tsc` 卡 TS2345。
6. 其餘 `host.token` 消費者對 `null` 已安全（falsy 檢查 / `?? ''`）：`useHostStore.ts:160` `if (!host?.token)`、`MemoryMonitorPage.tsx:498` `host.token ?? ''`。`pnpm run build`（tsc）權威確認無其他破口。

> 不外溢理由：`null` 的「明確清除」語意只在 persistence/sync 契約層有意義（test 也只在此 assert）；UI/health 既有以 `undefined` 模型「無 token」，於邊界 coalesce 最小化型別 churn。

## B. TabBar pinned tooltip — stale test（1 個 test）

### 現況
- `HoverTooltip`（`spa/src/components/HoverTooltip.tsx:91`）已把 `role="tooltip"` 元素 **`createPortal` 到 `document.body`**，且常駐（opacity 切顯隱，非 mount/unmount）。
- Test（`spa/src/components/TabBar.test.tsx:102`）用 `pinnedRoot.querySelector('[role="tooltip"]')` —— 只在 pinned tab **子樹**內找，portal 出去的元素永遠找不到 → `null` → fail。
- Impl 正確（portal 為刻意設計）。**只改 test**。

### 修法（**純 test-only**，不補 impl testId — codex spec review P3）
pinned tab 本體不渲染 `span.overflow-hidden`（`SortableTab.tsx:74` 分支），故 label 文字唯一存在於 portal 出去的 tooltip。改 test 以**可及性名稱**精準 scope（repo 既有 `SortableTab.test.tsx:180` / `HoverTooltip.test.tsx:79` 已用全域查詢 tooltip 的先例）：
- `const tooltip = screen.getByRole('tooltip', { name: 'aaa001' })` —— 一次斷言 role + 文字，且按 accessible name 唯一定位該 pinned tab 的 tooltip（其餘 normal tab 的 tooltip name 不同）。
- 保留既有正向意圖：tooltip 文字 === label（`aaa001`）、pinned tab 內**不**渲染可見 label（`span.overflow-hidden` 為 null）。
- **不**在 impl 端補 `testId`：`getByRole` name 已能唯一定位，避免把純測試問題擴成產品 API 變更。

## 驗收條件（AC）

- **AC1**：`hosts.test.ts` 三個 token preservation test 全綠（new host / ip 不符 / port 不符 → `token === null`）。
- **AC2**：`HostConfig.token` 型別允許 `null`；`pnpm run build`（tsc）通過，無因型別放寬而生的新 error。
- **AC3**：既有「preserves token when host id exists」test 續綠（sameEndpoint → 保留原 token 字串）。
- **AC3b**（codex spec review P2）：新增測試證明 cleared token 跨 **JSON persist 邊界**存活為 `null` —— deserialize 一個新 host 後 `JSON.parse(JSON.stringify(host))` 仍含 `token` key 且值為 `null`（`undefined` 會被 drop，這正是選 `null` 而非 `undefined` 的理由；zustand persist 以 `JSON.stringify` 序列化）。
- **AC4**：TabBar pinned tooltip test 改為查 portal 後轉綠；tooltip 文字 === label、pinned tab 無可見 label span；其餘 TabBar test（normal/locked）不回歸。
- **AC5**：`npx vitest run`（全套件）**4 個 baseline 失敗歸零**、無新增失敗；`pnpm run lint` 通過。

## 非目標
- 不改 token 的傳輸/序列化（serialize 已 strip token）。
- 不改 HoverTooltip 的 portal 設計、不改 TabBar 渲染結構（B 僅測試 + 必要時最小 testId）。
- 不處理 token 持久化到 IndexedDB 與否（既有行為）。
