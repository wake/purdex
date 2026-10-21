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
  /** 手動管理（非 Zustand store）：lib/notification-dedup.ts —— `{ [compositeKey]: requestId[] }`，等待核准的 worker
   *  已看過的 request id（通知以 request id 去重，同一毫秒的兩個 request 才不會互吃），每個 key 只留最近 20 個；
   *  跟 NOTIFICATION_SEEN 同時清。device-local，直接操作 localStorage，不走 browserStorage/syncManager */
  NOTIFICATION_SEEN_REQUESTS: 'purdex-notification-seen-requests',
  MODULE_CONFIG: 'purdex-module-config',
  MODULE_ENABLED: 'purdex-module-enabled',
  EDITOR_SETTINGS: 'purdex-editor-settings',
  /** The deck's and the chat's type size and line height (useDeckTypeStore, c9d9c7-60): `{ fontSize, lineHeight }`, each a number or null (the built-in look). Device-local, not synced. */
  DECK_TYPE: 'purdex-deck-type',
  /** The host that holds the personas' source copy (usePersonaSourceStore, name pool v2 spec §4.3): `{ hostId: string | null }`, `null` = the
   *  App's first host. Device-local, not synced, not in Profile; no syncManager. */
  PERSONA_SOURCE: 'purdex-persona-source',
  /** Worker pane theme + icon setting (useWorkerSettingsStore, worker theme spec §4.2): `{ theme, iconStyle, customIcon }`,
   *  one purdex-scope global setting (T1); registered with syncManager (cross-window) and projected into Profile Sync
   *  `settings` like the other appearance stores — all three fields travel; `customIcon` is a plain string (`''` = none)
   *  (PROJECTIONS.settings, settings ordinal 9) */
  WORKER_SETTINGS: 'purdex-worker-settings',
  /** The Mac App's quick-reply switches (useQuickReplyPrefsStore, quick-replies v2 spec §3): `{ rowEnabled, disabled: { [wireHostId]: itemId[] } }` —
   *  off-lists; the keys are WIRE host ids like HOST_LOOKS. Registered with syncManager and projected into Profile Sync `settings` (ordinal 10). */
  QUICK_REPLY_PREFS: 'purdex-quick-reply-prefs',
  /** The manual task groups (useManualGroupsStore, task groups spec §4.1): `{ order, groups }`, members hold LOCAL host ids in the store and WIRE ids on the wire.
   *  Registered with syncManager and projected into Profile Sync `settings` (ordinal 12). */
  TASK_GROUPS: 'purdex-task-groups',
  /** The monitoring tabs' pool hosts (usePoolHostsStore, monitoring tabs spec A3): `{ order, looks }`, both keyed by the wwd `host_id` (same on every device, no local↔wire mapping).
   *  Registered with syncManager and projected into Profile Sync `settings` (ordinal 13). */
  POOL_HOSTS: 'purdex-pool-hosts',
  /** This device's marker that the old global 「主機格指示」 was copied into each pool host's own indicator (`'1'` once done, MON-9a-3). Device-local: never projected, never registered with syncManager. Removed with `useUISettingsStore.fleetCellIndicator` (MON-12). */
  POOL_INDICATOR_MIGRATED: 'purdex-pool-indicator-migrated',
  /** 這台裝置對任務編組視圖的記憶（useGroupViewStore）：選取的群組、每個群組最後的作用中分頁、展開狀態、兩個視圖各自最後的選取；
   *  team key 內是本機 host id（跟著主機重新解析／刪除改寫）。device-local，**永遠不進 SOT**（不得列入 lib/profile/projections.ts）；走 syncManager（只為同裝置多視窗互讀，否則後寫的視窗會蓋掉先寫的） */
  GROUP_VIEW: 'purdex-group-view',
  SYNC_STATE: 'purdex-sync-state',
  PATH_CACHE_V1: 'purdex-path-cache-v1',
  RECENT_FILES: 'purdex-recent-files',
  PLACEHOLDER_FILES: 'purdex-placeholder-files',
  DEVICE_STATE: 'purdex-device-state',
  HEADLESS_LAUNCHER: 'purdex-headless-launcher',
  /** 這台裝置怎麼看一個 session pane（useSessionViewStore，U3 D1）：`{ byPane: { [tabId\0paneId]: { view: 'deck' | 'chat', binding: '<hostId>\0<sessionCode>' } } }`，
   *  終端機＝沒有紀錄；pane 重綁到別的 session 就回終端機；tab／pane 關掉時清。
   *  device-local，**永遠不進 SOT**（不得列入 lib/profile/projections.ts、也不放 pane content）；走 syncManager（同裝置多視窗互讀，否則後寫的視窗會蓋掉先寫的） */
  SESSION_VIEW: 'purdex-session-view',
  /** 任務編組的 session → lineage root 快取（lib/task-groups/root-cache.ts）：`{ roots: { [hostId]: { [agentSessionId]: rootSessionId } } }`，
   *  key 是本機 host id；主機移除／daemon 對不上時清該主機，沒有任何 world（含 parked）的分頁引用的條目也清。device-local，**永遠不進 SOT**、不走 syncManager */
  TASK_GROUP_ROOTS: 'purdex-task-group-roots',
  /** 團隊介面的排列（useTeamUiStore）：每個 team key（`<hostId>\0<teamId>`）的成員順序、收合、面板模式、ghost lead 所在的工作區；
   *  device-local，**永遠不進 SOT**（不得列入 lib/profile/projections.ts）、不走 syncManager；只被 roster frame 與刪除主機清掉 */
  TEAM_UI: 'purdex-team-ui',
  /** 工作簿日誌卡片的排列（useJournalUiStore，J-2b）：最後用的卡片寬度（新 unit 的預設）與每個 team key 的團隊卡開關；
   *  device-local，**永遠不進 SOT**、不走 syncManager。每個 unit 自己的開關／寬度／篩選在 lib/journal/view-memory.ts（只存記憶體） */
  JOURNAL_UI: 'purdex-journal-ui',
  /** 遠端 adopt 核准後的等待（lib/team/adoption-wait.ts）：`{ entries: { [key]: { hostId, approvalId, alias, target, startedAt, state, code, dismissed } } }`，
   *  reload 後依原 deadline（startedAt＋11 分鐘）恢復輪詢；終態被看過或 toast 過就清掉。device-local，**永遠不進 SOT**、不走 syncManager */
  ADOPTION_WAITS: 'purdex-adoption-waits',
  /** 還沒送達主機的撤銷（usePendingRevocationsStore，QR 配對 QP-3）：`{ items: { hostId, pairingId, endpoint?, daemonId?, hostName?, label?, createdAt? }[] }`
   *  （endpoint／daemonId 綁定建立當下的主機身分；舊版只有 hostId＋pairingId 的紀錄不自動重試），
   *  配對手機的裝置 token 在某台連不上的主機上撤銷失敗後記在這裡，主機連上時重試。
   *  device-local，**永遠不進 SOT**（不得列入 lib/profile/projections.ts）、不走 syncManager */
  PENDING_REVOCATIONS: 'purdex-pending-revocations',
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
  /** **Legacy**（host ownership H3b 起沒有任何程式讀寫）：#1366 pull guard 的「pull 被停下」通知 `{hostId, profileId, at}`，
   *  原由已刪除的 lib/profile/pull-unconfirmed.ts 管理。舊版留下的值由 lib/legacy-residue-cleanup.ts 在開機時刪除；
   *  保留這個名字只為記錄它曾被使用、不得挪作他用 */
  PROFILE_PULL_UNCONFIRMED: 'purdex-profile-pull-unconfirmed',
} as const
