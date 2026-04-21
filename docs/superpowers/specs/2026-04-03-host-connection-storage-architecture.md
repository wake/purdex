# Host 連線管理 + Storage 架構重構

> 2026-04-03 — 基於 alpha.39 的規劃，涵蓋連線偵測、錯誤 UI、storage 抽象化、token 安全、多視窗同步

## 一、連線偵測架構

### 連線層級

```
SPA ←[HTTP/WS]→ Daemon ←[CLI]→ tmux server → sessions
```

| Layer | 對象 | 偵測方式 | 重連策略 |
|-------|------|---------|---------|
| L1+L2 | Daemon 可達性 | `fetch /api/health` + timeout 判定 | 3 次快速 retry → disconnected → 背景長間隔重連（可選） |
| L3 | tmux server | `/api/health` 回傳 `{"ok":true, "tmux":true/false}` | retry 1 次後停止 |
| L4 | Session 存在 | session list 比對 / WS 404 | 不重連（session 層級，各 tab 自行處理） |

### L1+L2 細節

一次 `fetch /api/health`（timeout 3s）同時判定：

| 結果 | L1 主機 | L2 Daemon |
|------|---------|-----------|
| HTTP 200 | ✅ | ✅ |
| 快速失敗（< 500ms） | ✅ | ❌（connection refused） |
| timeout（3s） | ❌ | ❌ |

### L1 背景持續重連

電腦休眠喚醒時網路恢復需要時間。3 次快速 retry 後：
- UI 鎖定 disconnected
- 背景以長間隔（15s）持續嘗試
- 預設開啟，Settings 提供開關
- 成功後自動恢復 connected

### Daemon 擴充

`GET /api/health` 回應加入 tmux 可達性：

```json
{"ok": true, "tmux": true}
```

Daemon 執行 `tmux list-sessions` 或類似指令判定 tmux server 是否在線。

### useHostConnection 模組

```typescript
useHostConnection(hostId)
├── daemon: 'connected' | 'refused' | 'unreachable'
└── tmux: 'ok' | 'unavailable'
```

集中偵測，per host。所有 tab content 共享結果。

## 二、錯誤 UI 規範

### Host 層級（L1-L3）

| 元件 | 行為 |
|------|------|
| StatusBar | `refused`/`unreachable` → 紅色 disconnected |
| SessionPanel | host offline 時 disable session list + 錯誤訊息 |
| HostSidebar | StatusIcon 區分 refused / unreachable / tmux unavailable |
| OverviewSection | 「主機無法連線」/「Daemon 未啟動」/「tmux 環境無法連線」 |
| 所有 session tab | reconnecting → disconnected 錯誤頁 + 重連按鈕 |

### L2/L3 彈窗通知

- L1 不彈（網路問題太常見）
- L2/L3 彈 Electron 系統通知，每 host 每次斷線只彈一次
- 通知含「前往 Host」按鈕

### Tab icon 狀態

| 狀態 | Icon |
|------|------|
| reconnecting | Spinner 轉圈 |
| disconnected / error | Warning 警告 |
| connected | 原本的 Terminal/Stream icon |

### 手動 Reconnect 按鈕

所有 layer 失敗的錯誤畫面都提供「重新連線」按鈕，跳過 backoff 立即重試。

### Tab Content 各自的 L4 + 錯誤處理

| 情境 | Tab name | Tab 內容 | 可恢復 |
|------|----------|---------|--------|
| Host 斷線（L1/L2） | `{name}` | reconnecting → disconnected + 重連按鈕 | ✅ 自動/手動重連 |
| tmux 不可達（L3） | `{name}` | tmux 環境無法連線 | ✅ tmux 恢復後重連 |
| tmux server 重啟 | `{cachedName}（session 中斷）` | session 已失效 + session list（跨 host） | 手動選新 session |
| Session 被關閉（L4） | `{cachedName}（session 已關閉）` | session 已關閉 + session list（跨 host） | 手動選新 session |
| Host 被刪除 | `{cachedName}（host 已移除）` | host 已移除 + session list（跨 host） | 手動選新 session |

### Host 刪除流程

1. 使用者點刪除 → 彈窗「是否關閉所有此 Host 連線頁簽」
2. 是 → 關閉該 host 所有 tab + 清理所有 store 資料
3. 否 → tab 保留，顯示 `{cachedName}（host 已移除）` + session list

### 刪除時各 Store 清理

| Store | 動作 |
|-------|------|
| HostStore | 移除 hosts[id]、hostOrder、runtime[id] |
| SessionStore | removeHost(id) — 清除 sessions[id] |
| TabStore | 依使用者選擇：關閉或標記為 orphaned |
| AgentStore | 清除該 host 的 composite key 資料 |
| StreamStore | 清除該 host 的 sessions、relayStatus、handoffProgress |
| WS 連線 | useMultiHostEventWs 透過 hostOrderKey 變化自動 cleanup |

## 三、識別系統

### Host ID — 由 Daemon 產生

格式：`hostname:6-char-random-code`（如 `mlab:a1b2c3`）

- Daemon 啟動時產生，持久化到 config
- SPA 從 `/api/info` 取得
- 跨 origin 穩定（不再依賴 SPA 的 generateId）
- 多 IP 連同一台 daemon → 同一個 host ID

### tmux Instance ID — 偵測 server 重啟

格式：`pid:startTime`（如 `41523:1712345678`）

- Daemon 在 `/api/info` 或 session broadcast 中附帶
- SPA 比對 tab 存的 tmuxInstance vs 當前值
- 不一致 → tmux server 重啟過 → 該 host 所有舊 session 綁定失效

### Session ID — 不動

tmux 原生 `$N`，編碼為 6 字元 base36（現有 codec.go）。

理由：
- 主機上可能有使用者自行管理的 tmux session
- 無法強制 session 命名行為

### PaneContent 擴充

```typescript
{
  kind: 'session'
  hostId: string           // daemon 自報的 host ID
  sessionCode: string      // 6-char base36 encoded $N
  tmuxInstance: string     // pid:startTime — 用於偵測 server 重啟
  cachedName: string       // session name cache — 斷線/刪除後仍可顯示
  mode: 'terminal' | 'stream'
}
```

`cachedName` 在建立 tab 時寫入，WS 推送 sessions 更新時同步更新。

## 四、Storage 架構重構

### 抽象層設計

```
┌─────────────────────────────────────────────┐
│              Zustand Stores                  │
│  (useHostStore, useTabStore, ...)            │
│              ↓ persist middleware            │
├─────────────────────────────────────────────┤
│           StorageBackend 抽象層              │
├──────────────────┬──────────────────────────┤
│ ElectronBackend  │   BrowserBackend          │
│ Main process hub │   localStorage            │
│ IPC sync         │   BroadcastChannel sync   │
│ safeStorage 加密 │   明文（非敏感資料）        │
└──────────────────┴──────────────────────────┘
```

### Electron 路徑

```
Window A ←─ IPC ─→ Main Process (state hub) ←─ IPC ─→ Window B
                         │
                    JSON 檔案 + safeStorage（token）
```

- Main process 是 single source of truth
- Renderer 透過 contextBridge IPC 讀寫
- State 變更時 main broadcast 給所有 window
- Token 用 safeStorage 加密（macOS Keychain）
- 其他資料存 JSON 檔案（無容量限制）

### 瀏覽器路徑

```
Tab A ←─ BroadcastChannel ─→ Tab B
              │
         localStorage
```

- localStorage 存所有資料（token 除外）
- BroadcastChannel 同步跨 tab 變更
- Token 不存 localStorage → daemon 發 HttpOnly cookie

### Token 安全

| 環境 | Token 存儲 | SPA 接觸明文？ |
|------|-----------|-------------|
| Electron | safeStorage 加密，IPC 取得 | ❌ main process 持有 |
| 瀏覽器 | HttpOnly cookie（daemon Set-Cookie） | ❌ cookie JS 不可讀 |

### localStorage Key 統一

| Store | 新 Key |
|-------|--------|
| TabStore | `purdex-tabs` |
| HostStore | `purdex-hosts` |
| SessionStore | `purdex-sessions` |
| AgentStore | `purdex-agent` |
| WorkspaceStore | `purdex-workspaces` |
| HistoryStore | `purdex-history` |
| I18nStore | `purdex-i18n` |
| ThemeStore | `purdex-themes` |
| UISettingsStore | `purdex-ui-settings` |
| NotificationSettings | `purdex-notification-settings` |
| 通知去重 | `purdex-notification-seen` |

- 不帶版號，靠 Zustand persist 內部 version 欄位
- 不向下相容，舊 `tbox-*` 直接遺棄
- 全部從 version 1 開始

## 五、Token 強制

- Daemon 首次啟動自動產生 token，寫入 config.toml，印到 stdout
- `/api/health` 免 auth（用於連線測試）
- 其他所有 endpoint 需 token
- Add Host dialog：Token 改為必填
- Terminal/Stream WS 改用 ticket auth（與 session-events WS 一致）
- Auth 失敗時 UI 明確顯示「Token 無效」

## 六、Hooks 頁面整合

- HooksSection 接入 `/api/agent/hook-status` + `/api/agent/hook-setup`
- 移除 `hooks.go` 的 agent_hooks stub
- 合併 `useHookStatus` 到 HooksSection（解決 #109 + #108）
- 加入 #142 最後觸發時間（需 daemon 端記錄）

## 七、實施順序

```
Phase 1: Storage 抽象層 + Key 遷移
  ├── StorageBackend 介面
  ├── ElectronBackend（main process hub + IPC + safeStorage）
  ├── BrowserBackend（localStorage + BroadcastChannel）
  ├── 所有 store 切換到新 backend
  └── Key 從 tbox-* → purdex-*

Phase 2: 識別系統
  ├── Daemon 產生 host ID（hostname:code），持久化到 config
  ├── /api/info 回傳 host ID + tmux instance ID
  ├── HostStore 改用 daemon 自報 ID
  └── PaneContent 加 tmuxInstance + cachedName

Phase 3: 連線偵測
  ├── /api/health 擴充（加 tmux 欄位）
  ├── useHostConnection 模組
  ├── Electron TCP probe（IPC）— L1/L2 精準區分
  └── 重連策略（3 次 + 背景持續）

Phase 4: 錯誤 UI
  ├── Host 層級錯誤 UI（StatusBar、SessionPanel、OverviewSection）
  ├── Tab content L4 錯誤 UI（SessionDestroyed 元件）
  ├── Tab icon spinner/warning
  ├── L2/L3 彈窗通知
  ├── 手動 Reconnect 按鈕
  └── Host 刪除流程（確認 + 清理）

Phase 5: Token 強制
  ├── Daemon 自動產生 token
  ├── HttpOnly cookie（瀏覽器路徑）
  ├── safeStorage（Electron 路徑，Phase 1 已建）
  ├── Terminal/Stream WS ticket auth
  └── Add Host dialog Token 必填

Phase 6: Hooks 整合
  ├── Agent hooks API 接入 HooksSection
  ├── 移除 stub
  └── Hook 觸發時間

Phase 7: UI 快修
  ├── TabBar ICON_MAP 補齊
  ├── HostSidebar sub-pages icon
  ├── #139 expanded 同步
  └── #138 OverviewSection 拆檔
```

### 依賴關係

```
Phase 1 (storage 抽象)
  ↓
Phase 2 (識別系統) ← 依賴 Phase 1（hostId 存儲方式）
  ↓
Phase 3 (連線偵測) ← 依賴 Phase 2（hostId 作為 key）
  ↓
Phase 4 (錯誤 UI) ← 依賴 Phase 3（useHostConnection 狀態）

Phase 5 (token) ← 依賴 Phase 1（safeStorage）

Phase 6 (hooks) ← 獨立
Phase 7 (UI 快修) ← 獨立
```

## 八、關聯 Issues

| Issue | 對應 Phase |
|-------|-----------|
| #146 Host status 健康檢查 | Phase 3 + 4 |
| #147 Session 消失後 tab 無限重連 | Phase 4 |
| #148 Token 認證 | Phase 5 |
| #149 tmux server 重啟 session ID 重用 | Phase 2 |
| #150 Agent Hooks stub | Phase 6 |
| #137 Electron 離線 crash | Phase 4 |
| #138 OverviewSection 拆檔 | Phase 7 |
| #139 HostSidebar expanded 同步 | Phase 7 |
| #109 useHookStatus 孤兒 hook | Phase 6 |
| #108 App.tsx hook-status 抽離 | Phase 6 |
| #112 daemon API 安全金鑰 | Phase 5（合併 #148） |
