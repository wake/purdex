import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import './index.css'
import App from './App.tsx'
import { registerBuiltinLocales } from './lib/register-locales'
import { registerBuiltinThemes } from './lib/register-themes'
import { registerBuiltinPanes } from './lib/register-panes'
import { getActiveSessionInfo } from './lib/active-session'
import { useTabStore } from './stores/useTabStore'
import { useAgentStore } from './stores/useAgentStore'

registerBuiltinLocales()
registerBuiltinThemes()
registerBuiltinPanes()

// Cross-store subscription: auto-markRead when active tab changes to a session.
// Inlined here to avoid circular dependency between active-session.ts and useAgentStore.
let prevSessionCode = getActiveSessionInfo()?.sessionCode ?? null
useTabStore.subscribe(() => {
  const info = getActiveSessionInfo()
  const currentCode = info?.sessionCode ?? null
  if (currentCode !== prevSessionCode) {
    prevSessionCode = currentCode
    if (info) {
      useAgentStore.getState().markRead(info.hostId, info.sessionCode)
    }
  }
})

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <App />
  </StrictMode>,
)
