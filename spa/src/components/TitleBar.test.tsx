import { describe, it, expect, beforeEach } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import { Router } from 'wouter'
import { memoryLocation } from 'wouter/memory-location'
import { TitleBar } from './TitleBar'
import { useTabStore } from '../stores/useTabStore'
import { useSyncStore } from '../lib/sync/use-sync-store'
import { createTab } from '../types/tab'

describe('TitleBar', () => {
  it('renders the title text', () => {
    render(<TitleBar title="Purdex — purdex2" />)
    expect(screen.getByText('Purdex — purdex2')).toBeDefined()
  })

  it('renders layout pattern buttons', () => {
    render(<TitleBar title="test" />)
    expect(screen.getByTestId('layout-buttons')).toBeDefined()
  })

  it('layout pattern buttons are disabled when no active tab', () => {
    useTabStore.setState({ tabs: {}, tabOrder: [], activeTabId: null, visitHistory: [] })
    render(<TitleBar title="test" />)
    const buttons = screen.getByTestId('layout-buttons').querySelectorAll('button')
    // CollapseButton + 4 region toggles + 4 layout patterns = 9 buttons
    expect(buttons).toHaveLength(9)
    // Only layout pattern buttons (last 4, indices 5-8) should be disabled
    for (let i = 5; i < 9; i++) {
      expect(buttons[i]).toHaveProperty('disabled', true)
    }
  })

  it('renders with correct height', () => {
    const { container } = render(<TitleBar title="test" />)
    const bar = container.firstElementChild as HTMLElement
    expect(bar.getAttribute('style')).toContain('height: 30px')
  })

  it('calls applyLayout when layout button is clicked', () => {
    const tab = createTab({ kind: 'dashboard' })
    useTabStore.setState({ tabs: { [tab.id]: tab }, tabOrder: [tab.id], activeTabId: tab.id, visitHistory: [] })

    render(<TitleBar title="test" />)
    const buttons = screen.getByTestId('layout-buttons').querySelectorAll('button')
    // Layout pattern buttons start after CollapseButton + 4 region toggles (index 5)
    expect(buttons[5]).toHaveProperty('disabled', false)

    // Click "Split horizontal" (second layout pattern button = index 6)
    fireEvent.click(buttons[6])
    const updated = useTabStore.getState().tabs[tab.id]
    expect(updated.layout.type).toBe('split')
  })

  it('keeps layout pattern buttons disabled when no active tab', () => {
    useTabStore.setState({ tabs: {}, tabOrder: [], activeTabId: null, visitHistory: [] })
    render(<TitleBar title="test" />)
    const buttons = screen.getByTestId('layout-buttons').querySelectorAll('button')
    for (let i = 5; i < 9; i++) {
      expect(buttons[i]).toHaveProperty('disabled', true)
    }
  })

  it('title span uses max-width instead of fixed padding to prevent button overlap', () => {
    render(<TitleBar title="A very long title that could overlap with buttons" />)
    const span = screen.getByText('A very long title that could overlap with buttons')
    expect(span.className).toContain('max-w-')
    expect(span.className).not.toContain('px-20')
  })

  it('all enabled buttons have cursor-pointer class', () => {
    const tab = createTab({ kind: 'dashboard' })
    useTabStore.setState({ tabs: { [tab.id]: tab }, tabOrder: [tab.id], activeTabId: tab.id, visitHistory: [] })
    render(<TitleBar title="test" />)
    const buttons = screen.getByTestId('layout-buttons').querySelectorAll('button:not(:disabled)')
    expect(buttons.length).toBeGreaterThan(0)
    for (const btn of buttons) {
      expect(btn.className).toContain('cursor-pointer')
    }
  })
})

describe('TitleBar — sync conflict warning', () => {
  beforeEach(() => {
    useSyncStore.getState().reset()
  })

  it('does not render warning icon when no pending conflicts', () => {
    render(<TitleBar title="test" />)
    expect(screen.queryByLabelText(/sync conflict|同步衝突/i)).toBeNull()
  })

  it('renders warning icon + tooltip when pending conflicts > 0', () => {
    const bundle = { version: 1, timestamp: 5000, device: 'A', collections: {} }
    useSyncStore.getState().setPendingConflicts(
      [{ contributor: 'prefs', field: 'theme', lastSynced: 'x', local: 'y', remote: { value: 'z', device: 'A' } }],
      bundle,
    )
    render(<TitleBar title="test" />)
    const btn = screen.getByLabelText(/sync conflict|同步衝突/i)
    expect(btn).toBeTruthy()
    expect(btn.getAttribute('title')).toMatch(/1/)
  })

  it('clicking icon navigates to /settings/sync', () => {
    const bundle = { version: 1, timestamp: 5000, device: 'A', collections: {} }
    useSyncStore.getState().setPendingConflicts(
      [{ contributor: 'prefs', field: 'theme', lastSynced: 'x', local: 'y', remote: { value: 'z', device: 'A' } }],
      bundle,
    )

    const { hook, history } = memoryLocation({ path: '/', record: true })
    render(
      <Router hook={hook}>
        <TitleBar title="test" />
      </Router>,
    )
    const btn = screen.getByLabelText(/sync conflict|同步衝突/i)
    fireEvent.click(btn)
    expect(history[history.length - 1]).toBe('/settings/sync')
  })
})
