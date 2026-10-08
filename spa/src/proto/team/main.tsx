// spa/src/proto/team/main.tsx — entry for proto-team.html (team-display prototype; not part of the app).
// No daemon: none of main.tsx's startup wiring runs here. Stores are seeded with a fixed scene.
import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import '../../index.css'
import { registerBuiltinLocales } from '../../lib/register-locales'
import { registerBuiltinThemes } from '../../lib/register-themes'
import { useI18nStore } from '../../stores/useI18nStore'
import { ProtoApp } from './ProtoApp'
import { seed } from './seed'

registerBuiltinLocales()
registerBuiltinThemes()
useI18nStore.getState().setLocale('zh-TW')
seed()

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <ProtoApp />
  </StrictMode>,
)
