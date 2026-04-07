import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import './index.css'
import { MiniBrowserApp } from './components/MiniBrowserApp'

const params = new URLSearchParams(window.location.search)
const paneId = params.get('paneId') || ''

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <MiniBrowserApp paneId={paneId} />
  </StrictMode>,
)
