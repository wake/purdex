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
  /** 工作台的主機外觀（useHostLookStore，host ownership H2c）：`{ looks: { [wireId]: { name?, colors?, color?, icon?, iconWeight? } } }`，
   *  key 是 wire id；投影進 `settings`（PROJECTIONS.settings），走 syncManager */
  HOST_LOOKS: 'purdex-host-looks',
  /** 手動管理（非 Zustand store）：主機外觀首次遷移（HostConfig → HOST_LOOKS）已跑過的標記，值是 `'1'`；
   *  device-local、**永遠不進 SOT**（不得列入 lib/profile/projections.ts）、刻意不註冊 syncManager、直接操作 localStorage（讀寫皆 try/catch） */
  HOST_LOOKS_MIGRATED: 'purdex-host-looks-migrated',
  /** 工作台顯示的主機（useShownHostsStore，host ownership H2d）：`{ ids: string[] }`，純清單、沒有 `all`；
   *  `[]` ＝ 所有主機都隱藏（預設）；`ids` 是 wire id（未知的 id 與順序照留）；投影進 `settings`（PROJECTIONS.settings），走 syncManager */
  SHOWN_HOSTS: 'purdex-shown-hosts',
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
  /** 本機的 slave profiles、active 指標、停放中的 master 世界（useLocalProfilesStore）：device-local，
   *  **永遠不進 SOT**（不得列入 lib/profile/projections.ts）；走 syncManager 讓每個視窗一致 */
  LOCAL_PROFILES: 'purdex-local-profiles',
  /** 手動管理（非 Zustand store）：lib/storage/world-fence.ts —— 三個世界 store（TABS／WORKSPACES／LOCAL_PROFILES）的
   *  epoch 柵欄，值是十進位整數字串；直接操作 localStorage，不走 browserStorage/syncManager。**從沒切換過 profile 時這個 key 不存在** */
  WORLD_EPOCH: 'purdex-world-epoch',
  /** 手動管理（非 Zustand store）：這是 key 的**前綴**，不是完整的 key —— lib/profile/section-store.ts 組出
   *  `<前綴>:<profileId>:s:<section>` 與 `<前綴>:<profileId>:p:<hash>`；只有 leader 會寫，刻意不註冊 syncManager */
  PROFILE_SECTIONS: 'purdex-profile-sections',
  /** 手動管理（非 Zustand store）：lib/profile/leader.ts 的租約 `{windowId, expiresAt}`，直接操作 localStorage，
   *  不走 browserStorage/syncManager（每 2 秒續約一次，不需要廣播；喚醒 follower 靠原生 `storage` 事件） */
  PROFILE_LEADER: 'purdex-profile-leader',
  /** 手動管理（非 Zustand store）：lib/profile/sync-status.ts —— leader 發布的 `{at, leader, master, status, blocked, problems}`（`master`＝`hostId|profileId|attachGeneration`，讀取端只認自己的），
   *  follower 靠原生 `storage` 事件讀；直接操作 localStorage（理由同 PROFILE_LEADER）。沒有 master 時這個 key 不存在 */
  PROFILE_STATUS: 'purdex-profile-status',
  /** 手動管理（非 Zustand store）：這是 key 的**前綴** —— lib/profile/sync-status.ts 組出 `<前綴><encodeURIComponent(masterTag)>:<id>`，一個指令一個 key、依 master 分命名空間
   *  （follower 寫、leader 執行後刪；陣列是 read-modify-write，必然掉指令）。沒有 master 時不存在任何這類 key */
  PROFILE_COMMAND_PREFIX: 'purdex-profile-cmd:',
  /** 手動管理（非 Zustand store）：lib/profile/pull-unconfirmed.ts —— 「pull 因 SOT 的 hosts 在確認後變了而被停下」的通知
   *  `{hostId, profileId, at}`（#1366）；device-local、不進 SOT、刻意不註冊 syncManager、直接操作 localStorage（讀寫皆 try/catch）。
   *  **不放進 useProfileStore**：寫通知的視窗記憶可能是舊的，經 persist 寫會把整份舊 control plane（master／generation／
   *  direction／guard）蓋回 storage。跨視窗靠原生 `storage` 事件；Dismiss 或下一次 attach 成功時刪除。沒有通知時這個 key 不存在 */
  PROFILE_PULL_UNCONFIRMED: 'purdex-profile-pull-unconfirmed',
} as const
