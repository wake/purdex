import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import './index.css'
import App from './App.tsx'
import { registerBuiltinLocales } from './lib/register-locales'
import { registerBuiltinThemes } from './lib/register-themes'
import { registerBuiltinModules } from './lib/register-modules'
import { startBackupAutoTrigger } from './lib/storage-backup/backup-auto-trigger'
import { startDeviceStateUploader } from './lib/device-state/uploader'
import { startHostConfigLoader } from './lib/host-config-loader'
import { startPeerCacheInvalidation } from './lib/host-lifecycle'
import { startNexHostInvalidation } from './stores/useNexHostStore'
import { startExecutionListInvalidation } from './stores/useExecutionListStore'
import { startProfileSync } from './lib/profile/start'
import { getActiveSessionInfo } from './lib/active-session'
import { useTabStore } from './stores/useTabStore'
import { useAgentStore } from './stores/useAgentStore'
import { useLayoutStore } from './stores/useLayoutStore'

registerBuiltinLocales()
registerBuiltinThemes()
registerBuiltinModules()

// Persistent In-App backup auto-trigger (Phase 2b): subscribes to /buffer
// mutations and debounces a backup to the active host's daemon. Lives here
// (app bootstrap), not in StoragePane, so editing a buffer with the Storage
// pane closed still backs up (R1-C1). Module-scope so it persists for the
// app's lifetime; never disposed.
startBackupAutoTrigger()
// Device state backup: debounced upload of workspaces/tabs to the Development host (app lifetime).
startDeviceStateUploader()
// Host config (projects / commands / resume templates): fetch each host's copy when it connects.
startHostConfigLoader()
// Peer cache: drop a host's cached peer rows when its daemon identity changes
// (removed, re-pointed, token rotated) — a cached address belongs to a daemon.
startPeerCacheInvalidation()
// Nex readiness cache: refetch a host's /api/info + capabilities when its daemon reconnects.
startNexHostInvalidation()
// Execution lists: open/close a host's site-wide stream on nex readiness, drop its rows on identity change.
startExecutionListInvalidation()
// Profile Sync: with no master set this is one subscription to useProfileStore and nothing else (app lifetime).
startProfileSync()

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

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <App />
  </StrictMode>,
)
