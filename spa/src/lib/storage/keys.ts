/** 所有 localStorage key 名稱 — single source of truth */
export const STORAGE_KEYS = {
  TABS: 'purdex-tabs',
  HOSTS: 'purdex-hosts',
  SESSIONS: 'purdex-sessions',
  AGENT: 'purdex-agent',
  WORKSPACES: 'purdex-workspaces',
  HISTORY: 'purdex-history',
  I18N: 'purdex-i18n',
  THEMES: 'purdex-themes',
  UI_SETTINGS: 'purdex-ui-settings',
  GLOBAL_SETTINGS: 'purdex-global-settings',
  HOST_SETTINGS: 'purdex-host-settings',
  WORKSPACE_SETTINGS: 'purdex-workspace-settings',
  NOTIFICATION_SETTINGS: 'purdex-notification-settings',
  BROWSER_HISTORY: 'purdex-browser-history',
  LAYOUT: 'purdex-layout',
  NEW_TAB_LAYOUT: 'purdex-newtab-layout',
  /** 手動管理（非 Zustand store），直接操作 localStorage，不走 browserStorage/syncManager */
  NOTIFICATION_SEEN: 'purdex-notification-seen',
  MODULE_CONFIG: 'purdex-module-config',
  MODULE_ENABLED: 'purdex-module-enabled',
  EDITOR_SETTINGS: 'purdex-editor-settings',
  SYNC_STATE: 'purdex-sync-state',
  PATH_CACHE_V1: 'purdex-path-cache-v1',
  RECENT_FILES: 'purdex-recent-files',
  PLACEHOLDER_FILES: 'purdex-placeholder-files',
  DEVICE_STATE: 'purdex-device-state',
  HEADLESS_LAUNCHER: 'purdex-headless-launcher',
  /** 手動管理（非 Zustand store）：lib/client-identity.ts 經 browserStorage 直接讀寫，值是裸字串 id */
  CLIENT_IDENTITY: 'purdex-client-identity',
  /** Profile Sync control plane（useProfileStore）：master 與 autoSync，走 syncManager 讓每個視窗一致 */
  PROFILE: 'purdex-profile',
  /** 手動管理（非 Zustand store）：這是 key 的**前綴**，不是完整的 key —— lib/profile/section-store.ts 組出
   *  `<前綴>:<profileId>:s:<section>` 與 `<前綴>:<profileId>:p:<hash>`；只有 leader 會寫，刻意不註冊 syncManager */
  PROFILE_SECTIONS: 'purdex-profile-sections',
  /** 手動管理（非 Zustand store）：lib/profile/leader.ts 的租約 `{windowId, expiresAt}`，直接操作 localStorage，
   *  不走 browserStorage/syncManager（每 2 秒續約一次，不需要廣播；喚醒 follower 靠原生 `storage` 事件） */
  PROFILE_LEADER: 'purdex-profile-leader',
} as const
