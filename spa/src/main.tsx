import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import './index.css'
import App from './App.tsx'
import { registerBuiltinLocales } from './lib/register-locales'
import { registerBuiltinThemes } from './lib/register-themes'
import { registerBuiltinModules } from './lib/register-modules'
import { startBackupAutoTrigger } from './lib/storage-backup/backup-auto-trigger'
import { ensureDefaultDeviceName } from './stores/useDeviceNameStore'
import { startHostConfigLoader } from './lib/host-config-loader'
import { startHostDaemonIdVerification } from './lib/host-daemon-id'
import { startHostReresolve } from './lib/host-reresolve'
import { startPeerCacheInvalidation } from './lib/host-lifecycle'
import { startNexHostInvalidation } from './stores/useNexHostStore'
import { startExecutionListInvalidation } from './stores/useExecutionListStore'
import { startProfileSync } from './lib/profile/start'
import { bootHostLooks } from './lib/host-look-migration'
import { startStandaloneAdoption } from './features/workspace/lib/adopt-standalone'
import { startHostReshowRecovery } from './lib/rebuild/host-reshow'
import { scheduleLegacyResidueCleanup } from './lib/legacy-residue-cleanup'
import { getActiveSessionInfo } from './lib/active-session'
import { useTabStore } from './stores/useTabStore'
import { useAgentStore } from './stores/useAgentStore'
import { useLayoutStore } from './stores/useLayoutStore'

// Locales / themes are also registered by useI18nStore / useThemeStore before their persist
// hydrates (#1385) — by the time this line runs, those stores already exist. These calls are
// idempotent Map sets, kept so the registries don't depend on which module imports a store first.
registerBuiltinLocales()
registerBuiltinThemes()
registerBuiltinModules()

// Persistent In-App backup auto-trigger (Phase 2b): subscribes to /buffer
// mutations and debounces a backup to the active host's daemon. Lives here
// (app bootstrap), not in StoragePane, so editing a buffer with the Storage
// pane closed still backs up (R1-C1). Module-scope so it persists for the
// app's lifetime; never disposed.
startBackupAutoTrigger()
// Default device name (Electron hostname / UA) for Settings › Profile and the profile wizard.
// Never rejects; a failure keeps the store's fallback name.
void ensureDefaultDeviceName()
// Host config (projects / commands / resume templates): fetch each host's copy when it connects.
startHostConfigLoader()
// Daemon identity: one /api/info per (re)connect / endpoint / token / stored-daemonId change → observeDaemonId.
startHostDaemonIdVerification()
// Host re-resolve (host ownership §3.3): a reference kept verbatim because this device lacked its host points at the
// local host once that host is here — after hydration, on every host-identity change and every store rehydrate.
startHostReresolve()
// Peer cache: drop a host's cached peer rows when its daemon identity changes
// (removed, re-pointed, token rotated) — a cached address belongs to a daemon.
startPeerCacheInvalidation()
// Nex readiness cache: refetch a host's /api/info + capabilities when its daemon reconnects.
startNexHostInvalidation()
// Execution lists: open/close a host's site-wide stream on nex readiness, drop its rows on identity change.
startExecutionListInvalidation()
// Profile Sync: with no master set this is one subscription to useProfileStore and nothing else (app lifetime).
// It starts behind the host-look gate (plan §0.16): after the host and look stores hydrated and the first-run look
// migration returned — the collector exists only inside it, so no `settings` build precedes the migration. With
// synchronous localStorage this is a microtask. `bootHostLooks` never rejects.
void bootHostLooks().then(() => startProfileSync())
// Every tab belongs to exactly one workspace: a tab found in none is moved into `Unsorted`, one found in two keeps
// the first — a moment after the tab world last changed, start included (app lifetime). Not Profile Sync's: it
// runs with no master, too.
startStandaloneAdoption()
// Shown hosts (host ownership H2d-4): showing a host again — by its switch or a synced apply — recovers its sessions
// once per daemon, so a revivable pane comes back without waiting for a `sessions` frame (app lifetime).
startHostReshowRecovery()

useLayoutStore.getState().reconcileViews()

// Cross-store subscription: auto-markRead when active tab changes to a session.
// Inlined here to avoid circular dependency between active-session.ts and useAgentStore.
// Compare composite keys (hostId:sessionCode) for cross-host correctness.
let prevKey: string | null = (() => {
  const info = getActiveSessionInfo()
  return info ? `${info.hostId}:${info.sessionCode}` : null
})()
useTabStore.subscribe(() => {
  const currentInfo = getActiveSessionInfo()
  const currentKey = currentInfo ? `${currentInfo.hostId}:${currentInfo.sessionCode}` : null
  if (currentKey !== prevKey) {
    prevKey = currentKey
    if (currentInfo) {
      useAgentStore.getState().markRead(currentInfo.hostId, currentInfo.sessionCode)
    }
  }
})

// Leftovers of the removed Sync / device-state / workspace-snapshot features (#1303): on a later task, after the first
// render is scheduled; never awaited, never throws.
scheduleLegacyResidueCleanup()

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <App />
  </StrictMode>,
)
