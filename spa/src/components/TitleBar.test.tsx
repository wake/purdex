import { describe, it, expect } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import { TitleBar } from './TitleBar'
import { useTabStore } from '../stores/useTabStore'
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
    // 4 region toggles + 4 layout patterns = 8 buttons
    expect(buttons).toHaveLength(8)
    // Only layout pattern buttons (last 4) should be disabled
    for (let i = 4; i < 8; i++) {
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
    // Layout pattern buttons start after 4 region toggles
    expect(buttons[4]).toHaveProperty('disabled', false)

    // Click "Split horizontal" (second layout pattern button = index 5)
    fireEvent.click(buttons[5])
    const updated = useTabStore.getState().tabs[tab.id]
    expect(updated.layout.type).toBe('split')
  })

  it('keeps layout pattern buttons disabled when no active tab', () => {
    useTabStore.setState({ tabs: {}, tabOrder: [], activeTabId: null, visitHistory: [] })
    render(<TitleBar title="test" />)
    const buttons = screen.getByTestId('layout-buttons').querySelectorAll('button')
    for (let i = 4; i < 8; i++) {
      expect(buttons[i]).toHaveProperty('disabled', true)
    }
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
