import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import './index.css'
import App from './App.tsx'
import { registerBuiltinLocales } from './lib/register-locales'
import { registerBuiltinThemes } from './lib/register-themes'
import { registerBuiltinPanes } from './lib/register-panes'
import { getActiveSessionCode } from './lib/active-session'
import { useTabStore } from './stores/useTabStore'
import { useAgentStore } from './stores/useAgentStore'

registerBuiltinLocales()
registerBuiltinThemes()
registerBuiltinPanes()

// Cross-store subscription: auto-markRead when active tab changes to a session.
// Inlined here to avoid circular dependency between active-session.ts and useAgentStore.
let prevSession = getActiveSessionCode()
useTabStore.subscribe(() => {
  const current = getActiveSessionCode()
  if (current !== prevSession) {
    prevSession = current
    if (current !== null) {
      useAgentStore.getState().markRead(current)
    }
  }
})

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <App />
  </StrictMode>,
)
