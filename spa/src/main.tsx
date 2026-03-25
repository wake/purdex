import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import './index.css'
import App from './App.tsx'
import { registerBuiltinThemes } from './lib/register-themes'
import { registerBuiltinPanes } from './lib/register-panes'

registerBuiltinThemes()
registerBuiltinPanes()

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <App />
  </StrictMode>,
)
