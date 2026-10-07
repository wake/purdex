import { describe, it, expect, afterEach } from 'vitest'
import { render, cleanup } from '@testing-library/react'
import { BrowserPane } from './BrowserPane'

afterEach(() => {
  cleanup()
  delete (window as unknown as Record<string, unknown>).electronAPI
})

describe('BrowserPane', () => {
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
