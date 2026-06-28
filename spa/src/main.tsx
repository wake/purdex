import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import './index.css'
import App from './App.tsx'
import { registerBuiltinLocales } from './lib/register-locales'
import { registerBuiltinThemes } from './lib/register-themes'
import { registerBuiltinModules } from './lib/register-modules'
import { startBackupAutoTrigger } from './lib/storage-backup/backup-auto-trigger'
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
