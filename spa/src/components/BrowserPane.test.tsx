import { describe, it, expect, afterEach } from 'vitest'
import { render, screen, cleanup } from '@testing-library/react'
import { BrowserPane } from './BrowserPane'

afterEach(() => {
  cleanup()
  delete (window as unknown as Record<string, unknown>).electronAPI
})

describe('BrowserPane', () => {
  it('renders SPA fallback message when no electronAPI', () => {
    // Ensure no electronAPI is present
    delete (window as unknown as Record<string, unknown>).electronAPI
    render(<BrowserPane paneId="p1" url="https://example.com" />)
    expect(screen.getByText('Requires desktop app')).toBeInTheDocument()
  })

  it('renders placeholder div when electronAPI exists', () => {
    ;(window as unknown as Record<string, unknown>).electronAPI = {
      openBrowserView: () => {},
      closeBrowserView: () => {},
      navigateBrowserView: () => {},
      resizeBrowserView: () => {},
    }
    const { container } = render(<BrowserPane paneId="p2" url="https://example.com" />)
    const div = container.querySelector('[data-browser-pane="p2"]')
    expect(div).toBeInTheDocument()
  })
})
