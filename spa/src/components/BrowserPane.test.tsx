import { describe, it, expect, afterEach, beforeEach, vi } from 'vitest'
import { render, cleanup, screen, fireEvent } from '@testing-library/react'
import { BrowserPane } from './BrowserPane'
import { useTabStore } from '../stores/useTabStore'
import { createTab } from '../types/tab'
import { getPrimaryPane } from '../lib/pane-tree'

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

// #1816 — the Mac App loads the SPA from the dev server, so its Electron preload can be older than this code:
// `window.electronAPI` is there but a method the pane calls is not. Each one is checked on its own; a missing
// one is a no-op, never a TypeError, and a missing `openBrowserView` (no view can ever show) renders a
// degraded state the user can leave instead of an empty pane.
describe('BrowserPane with an older preload (#1816)', () => {
  const UNSUPPORTED = "This version of the Purdex App doesn't support the built-in browser yet. Update the App."
  const BACK = 'Back to New Tab'

  function fullApi() {
    return {
      openBrowserView: vi.fn(),
      closeBrowserView: vi.fn(),
      navigateBrowserView: vi.fn(),
      resizeBrowserView: vi.fn(),
      browserViewGoBack: vi.fn(),
      browserViewGoForward: vi.fn(),
      browserViewReload: vi.fn(),
      browserViewStop: vi.fn(),
      browserViewOpenMiniWindow: vi.fn(),
    }
  }
  type Api = ReturnType<typeof fullApi>

  function setApi(api: Partial<Api>) {
    ;(window as unknown as Record<string, unknown>).electronAPI = api
  }

  function without(method: keyof Api): Partial<Api> {
    const api: Partial<Api> = fullApi()
    delete api[method]
    return api
  }

  beforeEach(() => {
    useTabStore.setState({ tabs: {}, tabOrder: [], activeTabId: null })
  })

  it('a full electronAPI: opens the view on mount, navigates on a new url, closes it on unmount (unchanged)', () => {
    const api = fullApi()
    setApi(api)
    const { rerender, unmount } = render(<BrowserPane paneId="p2" url="https://example.com" />)

    expect(api.openBrowserView).toHaveBeenCalledWith('https://example.com', 'p2')
    expect(screen.getByRole('textbox')).toBeInTheDocument()
    expect(screen.queryByText(UNSUPPORTED)).not.toBeInTheDocument()

    rerender(<BrowserPane paneId="p2" url="https://example.org" />)
    expect(api.navigateBrowserView).toHaveBeenCalledWith('p2', 'https://example.org')

    unmount()
    expect(api.closeBrowserView).toHaveBeenCalledWith('p2')
  })

  it('without openBrowserView: no throw, and a degraded state instead of an empty pane', () => {
    setApi(without('openBrowserView'))

    expect(() => render(<BrowserPane paneId="p2" url="https://example.com" />)).not.toThrow()

    expect(screen.getByText(UNSUPPORTED)).toBeInTheDocument()
    expect(screen.getByRole('button', { name: BACK })).toBeInTheDocument()
    // No toolbar: its buttons would drive a view that can never exist.
    expect(screen.queryByRole('textbox')).not.toBeInTheDocument()
  })

  it('no electronAPI at all: the same degraded state', () => {
    expect(() => render(<BrowserPane paneId="p2" url="https://example.com" />)).not.toThrow()
    expect(screen.getByText(UNSUPPORTED)).toBeInTheDocument()
  })

  it('the degraded state is left through its button: the pane goes back to a New Tab page', () => {
    setApi(without('openBrowserView'))
    const tab = createTab({ kind: 'browser', url: 'https://example.com' })
    useTabStore.getState().addTab(tab)
    const paneId = getPrimaryPane(tab.layout).id
    render(<BrowserPane paneId={paneId} url="https://example.com" />)

    fireEvent.click(screen.getByRole('button', { name: BACK }))

    expect(getPrimaryPane(useTabStore.getState().tabs[tab.id].layout).content).toEqual({ kind: 'new-tab' })
  })

  it('without navigateBrowserView: a new url does not throw', () => {
    setApi(without('navigateBrowserView'))
    const { rerender } = render(<BrowserPane paneId="p2" url="https://example.com" />)

    expect(() => rerender(<BrowserPane paneId="p2" url="https://example.org" />)).not.toThrow()
  })

  /**
   * React reports an error thrown by an event handler through `window.reportError` rather than out of
   * `fireEvent`, so a handler's TypeError is caught here as the error event it becomes.
   */
  function handlerErrors(fire: () => void): unknown[] {
    const errors: unknown[] = []
    const onError = (e: ErrorEvent) => { errors.push(e.error); e.preventDefault() }
    window.addEventListener('error', onError)
    try { fire() } catch (err) { errors.push(err) } finally { window.removeEventListener('error', onError) }
    return errors
  }

  it('without navigateBrowserView: entering a url in the toolbar does not throw', () => {
    setApi(without('navigateBrowserView'))
    render(<BrowserPane paneId="p2" url="https://example.com" />)
    const input = screen.getByRole('textbox')

    fireEvent.change(input, { target: { value: 'https://example.org' } })
    expect(handlerErrors(() => fireEvent.keyDown(input, { key: 'Enter' }))).toEqual([])
  })

  it('without closeBrowserView: unmount does not throw', () => {
    setApi(without('closeBrowserView'))
    const { unmount } = render(<BrowserPane paneId="p2" url="https://example.com" />)

    expect(() => unmount()).not.toThrow()
  })

  it('without resizeBrowserView: mounting does not throw, and the view still opens', () => {
    const api = without('resizeBrowserView')
    setApi(api)

    expect(() => render(<BrowserPane paneId="p2" url="https://example.com" />)).not.toThrow()
    expect(api.openBrowserView).toHaveBeenCalledWith('https://example.com', 'p2')
    expect(screen.queryByText(UNSUPPORTED)).not.toBeInTheDocument()
  })

  it('without browserViewReload: the toolbar reload does not throw', () => {
    setApi(without('browserViewReload'))
    render(<BrowserPane paneId="p2" url="https://example.com" />)

    expect(handlerErrors(() => fireEvent.click(screen.getByRole('button', { name: 'Reload' })))).toEqual([])
  })
})
