# PR #69 Review Findings — Tab/Session 解耦

**PR:** feat/tab-session-decoupling → v1
**日期:** 2026-03-25
**Review 輪次:** 第一輪（code-review skill 5 角度）+ 第二輪（攻擊方 + 防守方 + 檔案大小）

---

## 信心指數說明

| 分數 | 含義 |
|------|------|
| 90-100 | 確定是真實問題，高頻觸發 |
| 75-89 | 驗證過的真實問題，重要 |
| 50-74 | 真實但影響有限或低頻 |
| 25-49 | 可能是問題，也可能是 false positive |
| 0-24 | False positive |

## 複雜度說明

| 等級 | 含義 |
|------|------|
| Low | < 30 分鐘，改 1-2 個檔案 |
| Medium | 30-60 分鐘，改 3-5 個檔案或需要設計決策 |
| High | > 60 分鐘，架構變更或跨模組重構 |

## Phase 關聯度說明

| 等級 | 含義 |
|------|------|
| 必修 | 不修會 crash 或功能完全無效 |
| 本 phase | 與 tab/session 解耦直接相關，應在此 PR 處理 |
| 下 phase | 與後續功能（workspace 完善、split view 等）相關 |
| 長期 | 技術債，不影響功能但影響品質 |

---

## 已修復（7 項）

| # | 問題 | 信心 | 複雜度 | Phase | Commit |
|---|------|------|--------|-------|--------|
| F1 | useRouteSync 缺 persist hydration guard — 冷啟動 URL→Tab 在空 store 上執行 | 85 | Low | 必修 | `67e5cb5` |
| F2 | suppressSync.current 在 no-op 路由後卡住 — 下次 URL 更新被吞 | 85 | Low | 必修 | `67e5cb5` |
| F3 | useRouteSync deps 不完整 — viewMode 切換後 URL 不同步 | 75 | Medium | 本 phase | `67e5cb5` |
| F4 | handleCloseTab 對 locked tab 先 recordClose 再被 closeTab no-op | 80 | Low | 本 phase | `67e5cb5` |
| F5 | recordVisit 從未被呼叫 — browseHistory 功能完全無效 | 90 | Low | 本 phase | `67e5cb5` |
| F6 | register-panes.tsx 繞過 store API — 直接 setState | 70 | Low | 本 phase | `67e5cb5` |
| F7 | getPrimaryPane 對空 children crash — 所有 caller 都會爆 | 85 | Low | 必修 | `67e5cb5` |

---

## 已延後（2 項）

| # | 問題 | 信心 | 複雜度 | Phase | 延後原因 |
|---|------|------|--------|-------|----------|
| D1 | reopenLast 不還原 workspace 歸屬 — reopen 後 tab 變 standalone | 85 | Medium | 下 phase | 等 workspace 功能開發後一起處理 |
| D2 | `/w/:workspaceId` 路由是 no-op — 直接訪問無效果 | 85 | Medium | 下 phase | Known limitation，等 workspace 路由完善 |

---

## 未修復 — 開放項目

### Bug 類

| # | 問題 | 信心 | 複雜度 | Phase | 說明 |
|---|------|------|--------|-------|------|
| O1 | StrictMode double-invoke 可能擾亂 suppressSync ref | 65 | Medium | 本 phase | 開發環境 only；suppressSync 的 set/reset 順序在 double-invoke 下不可預測。修法：改用 effect cleanup 或 flushSync |
| O2 | reopen 的 tab 用原 ID，addTab 不檢查已存在 — tabOrder 可能重複 | 75 | Low | 本 phase | close → reopen → close → reopen 同一 tab 時，第二次 addTab 覆蓋 + tabOrder 重複。修法：addTab 加 dedup guard |
| O3 | tabToUrl 把 `new-tab` 映射到 `/t/{id}/terminal` — URL round-trip 壞掉 | 70 | Low | 本 phase | parseRoute 回傳 session-tab 而非 new-tab。重整頁面後語義不一致。修法：new-tab 不映射 URL（保持 `/`）或加專屬路由 |
| O4 | handleReorderTabs 用 displayTabs 的順序覆蓋全域 tabOrder | 65 | Medium | 本 phase | 拖曳排序時 standalone tabs 可能從 tabOrder 消失。修法：merge 而非 replace |
| O5 | session 路由找不到 tab 時，畫面顯示上一個 tab 而非空狀態 | 70 | Low | 本 phase | Spec 說「保留 URL，顯示空狀態」。目前 activeTabId 不變，顯示舊內容。修法：setActiveTab(null) |

### 設計 / 一致性

| # | 問題 | 信心 | 複雜度 | Phase | 說明 |
|---|------|------|--------|-------|------|
| O6 | useRouteSync 完全沒有測試 — TDD 違規 | 75 | Medium | 本 phase | CLAUDE.md 要求 TDD。雙向同步是核心膠水邏輯，零測試覆蓋 |
| O7 | DashboardPage / SettingsPage 不接受 PaneRendererProps — 做法不一致 | 50 | Low | 長期 | register-panes 用 wrapper `() => <DashboardPage />`，HistoryPage 直接接受 props。統一即可 |
| O8 | parseRoute 未驗證 tabId/workspaceId 的 6 碼 base36 格式 | 60 | Low | 本 phase | Spec 明確要求格式驗證。修法：加 `/^[0-9a-z]{6}$/` check |
| O9 | useHistoryStore 缺 partialize — action methods 被嘗試序列化 | 45 | Low | 長期 | Zustand 自動忽略 function，不會出錯。但與其他 store 不一致 |
| O10 | new-tab-registry registerNewTabProvider 無冪等保護 | 50 | Low | 長期 | HMR 時可能產生重複 provider。修法：push 前 filter 同 id |
| O11 | openSingletonTab 的 session 匹配包含 mode — 設計歧義 | 55 | Low | 長期 | 同 session 不同 mode 被視為不同 singleton。可能符合預期也可能不是 |
| O12 | new-tab renderer 的 handleSelect 只操作 activeTabId 的 tab | 50 | Low | 下 phase | 非 active tab 中的 new-tab page 點選無效（keep-alive pool 場景）|

### 部署 / 環境

| # | 問題 | 信心 | 複雜度 | Phase | 說明 |
|---|------|------|--------|-------|------|
| O13 | Vite 未配置 production SPA fallback | 70 | Low | 本 phase | Dev 環境 OK，但 production nginx/Go serve 直接訪問路徑會 404。需 `try_files $uri /index.html` |
| O14 | HistoryPage.tsx `_pane`/`_isActive` lint errors | 90 | Low | 本 phase | ESLint 不認 underscore prefix 的 unused vars |
| O15 | SessionPaneContent 無專屬測試 | 65 | Medium | 本 phase | 取代了 SessionTabContent（有測試），但替代測試未寫 |

### 重構建議（非 bug）

| # | 建議 | 複雜度 | Phase | 說明 |
|---|------|--------|-------|------|
| R1 | StatusBar + TabContextMenu 的 click-outside 邏輯可抽為 `useClickOutside` hook | Low | 長期 | 減少重複 |
| R2 | App.tsx 的 tab/workspace handler 可抽為 `useTabWorkspaceActions()` hook | Medium | 長期 | App.tsx 降至 ~160 行 |
| R3 | contentMatches 可從 useTabStore 抽到 lib/pane-utils | Low | 長期 | 隨 PaneContent 種類增加會變大 |

---

## 統計

| 類別 | 數量 |
|------|------|
| 已修復 | 7 |
| 已延後 | 2 |
| 開放 Bug | 5（O1-O5） |
| 開放 設計/一致性 | 7（O6-O12） |
| 開放 部署/環境 | 3（O13-O15） |
| 重構建議 | 3（R1-R3） |
| **總計** | 27 |

### 建議優先處理（本 phase merge 前）

1. **O2** — addTab dedup guard（Low 複雜度，防 tabOrder 損壞）
2. **O5** — session 路由找不到 tab 時 setActiveTab(null)（Low）
3. **O14** — HistoryPage lint fix（Low）
4. **O8** — parseRoute 格式驗證（Low）
5. **O6** — useRouteSync 測試（Medium，TDD 合規）
