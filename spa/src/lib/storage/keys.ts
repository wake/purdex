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
  NOTIFICATION_SETTINGS: 'purdex-notification-settings',
  BROWSER_HISTORY: 'purdex-browser-history',
  LAYOUT: 'purdex-layout',
  /** 手動管理（非 Zustand store），直接操作 localStorage，不走 browserStorage/syncManager */
  NOTIFICATION_SEEN: 'purdex-notification-seen',
  MODULE_CONFIG: 'purdex-module-config',
} as const
