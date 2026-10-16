// Prototype entry: the real chrome (TitleBar, TabBar, TeamPanel) over seeded stores, plus the new JournalPanel.
import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import './proto.css'
import { registerBuiltinLocales } from '../../src/lib/register-locales'
import { registerBuiltinThemes } from '../../src/lib/register-themes'
import { useI18nStore } from '../../src/stores/useI18nStore'
import { Proto } from './Proto'

registerBuiltinLocales()
registerBuiltinThemes()
useI18nStore.getState().setLocale('zh-TW')

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <Proto />
  </StrictMode>,
)
