import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import './index.css'
import { ThemeInjector } from './components/ThemeInjector'
import { MiniBrowserApp } from './components/MiniBrowserApp'

const params = new URLSearchParams(window.location.search)
const paneId = params.get('paneId') || ''

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <ThemeInjector />
    <MiniBrowserApp paneId={paneId} />
  </StrictMode>,
)
