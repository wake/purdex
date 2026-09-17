// spa/src/components/BrowserNewTabSection.test.tsx
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, cleanup } from '@testing-library/react'
import { BrowserNewTabSection } from './BrowserNewTabSection'
import { useI18nStore } from '../stores/useI18nStore'
import { useBrowserHistoryStore } from '../stores/useBrowserHistoryStore'

const onSelect = vi.fn()

beforeEach(() => {
  cleanup()
  onSelect.mockClear()
  useI18nStore.setState({ t: (k: string) => k })
  useBrowserHistoryStore.setState({ urls: [] })
})

describe('BrowserNewTabSection', () => {
  // Mount-time `focus()` scrolls every scrollable ancestor to reveal the input,
  // which dragged the New Tab column past the session list. No section of the
  // New Tab page owns the caret.
  it('does not steal focus on mount', () => {
    render(<BrowserNewTabSection onSelect={onSelect} />)
    const input = screen.getByPlaceholderText('browser.url_placeholder')
    expect(document.activeElement).toBe(document.body)
    expect(document.activeElement).not.toBe(input)
  })

  it('leaves the input focusable by an explicit focus() call', () => {
    render(<BrowserNewTabSection onSelect={onSelect} />)
    const input = screen.getByPlaceholderText('browser.url_placeholder')
    expect(input).toBeInTheDocument()
    input.focus()
    expect(document.activeElement).toBe(input)
  })
})
