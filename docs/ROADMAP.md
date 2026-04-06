# tmux-box Roadmap

**最後更新**: 2026-04-06
**當前版本**: 1.0.0-alpha.48

---

## 已完成

### 基礎建設（Daemon + 架構）

| Phase | 主題 | 版本 | Spec | Plan |
|-------|------|------|------|------|
| 1 | Daemon Terminal | — | — | [plan](superpowers/plans/2026-03-17-phase1-daemon-terminal.md) |
| 2 | Stream Mode | — | — | [plan](superpowers/plans/2026-03-17-phase2-stream-mode.md) |
| — | Stream Handoff | — | [spec](superpowers/specs/2026-03-17-stream-handoff-design.md) | [plan](superpowers/plans/2026-03-17-stream-handoff.md) |
| — | Stream Redesign | — | [spec](superpowers/specs/2026-03-17-stream-redesign.md) | [plan](superpowers/plans/2026-03-17-stream-redesign.md) |
| — | Stream WS Lifecycle | — | [spec](superpowers/specs/2026-03-18-stream-ws-lifecycle-design.md) | [plan](superpowers/plans/2026-03-18-stream-ws-lifecycle.md) |
| 1a | Storage Abstraction | alpha.40 | [spec](superpowers/specs/2026-04-03-host-connection-storage-architecture.md) | — |
| 2a-2b | Host Identification | alpha.41-42 | 同上 | — |
| 3 | Connection Detection | alpha.43 | [spec](superpowers/specs/2026-04-03-phase3-connection-detection-design.md) | [plan](superpowers/plans/2026-04-03-phase3-connection-detection.md) |
| 4 | Error UI & Resilience | alpha.44 | [spec](superpowers/specs/2026-04-04-phase4-error-ui-design.md) | [plan](superpowers/plans/2026-04-04-phase4-error-ui.md) |
| 5a | Pairing + Token Auth | alpha.46 | [spec](superpowers/specs/2026-04-05-phase5-pairing-token-design.md) | [plan](superpowers/plans/2026-04-05-phase5a-pairing-token.md) |
| 5b | WS Ticket + Auth Error UI | alpha.47 | [spec](superpowers/specs/2026-04-06-phase5b-ws-ticket-auth-error-design.md) | [plan](superpowers/plans/2026-04-06-phase5b-ws-ticket-auth-error.md) |
| 5c | API Auth Migration | alpha.48 | — | — |
| 6 | Hooks Unification | merged | — | — |

### UI + 功能

| Phase | 主題 | Spec | Plan |
|-------|------|------|------|
| 1 | Tab System | — | [plan](superpowers/plans/2026-03-20-phase1-tab-system.md) |
| 1.1 | Tab Model + View Toggle | [spec](superpowers/specs/2026-03-20-phase1.1-tab-model-view-toggle.md) | [plan](superpowers/plans/2026-03-20-phase1.1-tab-model-view-toggle.md) |
| 1.5 | Tab Interactions | [spec](superpowers/specs/2026-03-21-phase1.5-tab-interactions-design.md) | [plan](superpowers/plans/2026-03-21-phase1.5-tab-interactions.md) |
| 1.6a | Module Session | [spec](superpowers/specs/2026-03-22-phase1.6a-module-session-design.md) | [plan](superpowers/plans/2026-03-22-phase1.6a-module-session.md) |
| 1.6b | Stream CC Migration | [spec](superpowers/specs/2026-03-23-phase1.6b-stream-cc-migration-design.md) | [plan](superpowers/plans/2026-03-23-phase1.6b-stream-cc-migration.md) |
| 1.6c | Multi-Host UI | [spec](superpowers/specs/2026-03-31-phase1.6c-multi-host-design.md) | [plan](superpowers/plans/2026-03-31-phase1.6c-multi-host.md) |
| — | Tab Session Decoupling | [spec](superpowers/specs/2026-03-24-tab-session-decoupling-design.md) | [plan](superpowers/plans/2026-03-24-tab-session-decoupling.md) |
| — | Pin/Lock Independence | [spec](superpowers/specs/2026-03-22-pin-lock-independence.md) | [plan](superpowers/plans/2026-03-22-pin-lock-independence.md) |
| — | i18n System | [spec](superpowers/specs/2026-03-25-i18n-system-design.md) | [plan](superpowers/plans/2026-03-25-i18n-system.md) |
| — | Settings UI | [spec](superpowers/specs/2026-03-25-settings-ui-design.md) | [plan](superpowers/plans/2026-03-25-settings-ui.md) |
| — | Theme System | [spec](superpowers/specs/2026-03-25-theme-system-design.md) | [plan](superpowers/plans/2026-03-25-theme-system.md) |
| — | Electron Shell | [spec](superpowers/specs/2026-03-26-electron-shell-design.md) | [plan](superpowers/plans/2026-03-26-electron-shell.md) |
| — | PWA / Browser Pane | [spec](superpowers/specs/2026-03-26-pwa-electron-design.md) | [plan](superpowers/plans/2026-03-26-pwa-platform-browser-pane.md) |
| — | Dev Auto Update | [spec](superpowers/specs/2026-03-27-dev-auto-update-design.md) | [plan](superpowers/plans/2026-03-27-dev-auto-update.md) |
| — | Keyboard Shortcuts | [spec](superpowers/specs/2026-03-28-app-keyboard-shortcuts-design.md) | [plan](superpowers/plans/2026-03-28-app-keyboard-shortcuts.md) |
| — | Agent Hook Status | [spec](superpowers/specs/2026-03-29-agent-hook-status-design.md) | [plan](superpowers/plans/2026-03-29-agent-hook-status.md) |
| — | Agent Hook Enhancement | [spec](superpowers/specs/2026-03-30-agent-hook-enhancement-design.md) | — |
| — | Notification System | [spec](superpowers/specs/2026-03-29-notification-system-design.md) | [plan](superpowers/plans/2026-03-29-notification-system.md) |
| — | Agent File Upload | [spec](superpowers/specs/2026-03-31-agent-file-upload-design.md) | [plan](superpowers/plans/2026-03-31-agent-file-upload.md) |

### 參考設計文件

| 文件 | 說明 |
|------|------|
| [tmux-box 初始設計](superpowers/specs/2026-03-16-tmux-box-design.md) | 專案原始設計 |
| [Tabbed Workspace UI 設計](superpowers/specs/2026-03-20-tabbed-workspace-ui-design.md) | 整體 UI 架構（Activity Bar、側欄、Tab 群組等） |
| [Host Connection Storage 架構](superpowers/specs/2026-04-03-host-connection-storage-architecture.md) | Host 連線管理 7-phase 總體規劃 |
| [Workspace + Editor 綜合設計](superpowers/specs/2026-04-06-workspace-and-editor-module-design.md) | Phase 7-9 的補充修訂文件 |

---

## 待開發

| Phase | 主題 | Spec | 說明 |
|-------|------|------|------|
| **7** | **Workspace 強化** | [spec](superpowers/specs/2026-04-06-phase7-workspace-enhancement-design.md) | defaultHost/Path、快捷鍵切換、quick actions、dashboard |
| **8** | **Side Panel 系統** | [spec](superpowers/specs/2026-04-06-phase8-side-panel-system-design.md) | 4 zone 框架、panel registry、三模式（固定/預設/縮減） |
| **9** | **Editor Module** | [spec](superpowers/specs/2026-04-06-phase9-editor-module-design.md) | daemon FS API、Monaco editor、file opener registry、file tree、grep、diff |

---

## 未排期

### 功能

| 主題 | 來源 | 備註 |
|------|------|------|
| Stream 即時體驗 | feature ideas #2 | 逐字渲染、tool progress 心跳、cli-bridge-sub 重連 |
| Stream 輔助功能 | feature ideas #3 | prompt suggestion、tool summary、rate limit、task UI、JSONL |
| Tab Split + Pane Attach | feature ideas #9 | PaneLayout split type 已定義但未啟用 |
| Quick Switcher（⌘K） | [UI spec](superpowers/specs/2026-03-20-tabbed-workspace-ui-design.md) §8 | 可在 Phase 7 後任意插入 |
| 手機版響應式 | [UI spec](superpowers/specs/2026-03-20-tabbed-workspace-ui-design.md) §14 | 所有桌面功能穩定後再做 |
| 側欄拖曳配置 | [UI spec](superpowers/specs/2026-03-20-tabbed-workspace-ui-design.md) §7c | 低優先，Phase 8 預設配置已夠用 |
| Session 共享 / 唯讀掛載 | feature ideas #8 | |
| 使用者自定義 Keybindings | feature ideas #10 | |

### 品質與技術債

| 類別 | Issue 數 | 代表項目 |
|------|----------|----------|
| Daemon 品質 | ~8 | #26 cfgMu、#28 rollback、#133 TOCTOU |
| SPA 品質 | ~15 | #62 水平線、#92 setState、#93 memoization |
| Agent Hook 殘留 | 4 | #114 subagent idle、#124 PermissionRequest |
| Dev Update 殘餘 | 5 | #97 timeout、#99 goroutine cancel |
| Electron 體驗 | 5 | title bar、URL 開啟、theme 同步 |
| Phase 殘留 issues | ~10 | #149 session ID、#157 migration、#159 ghost |
