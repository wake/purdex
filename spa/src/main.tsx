import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import './index.css'
import App from './App.tsx'
import { registerBuiltinLocales } from './lib/register-locales'
import { registerBuiltinThemes } from './lib/register-themes'
import { registerBuiltinPanes } from './lib/register-panes'

registerBuiltinLocales()
registerBuiltinThemes()
registerBuiltinPanes()

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <App />
  </StrictMode>,
)
