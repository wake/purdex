import { describe, it, expect } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import { TitleBar } from './TitleBar'
import { useTabStore } from '../stores/useTabStore'
import { createTab } from '../types/tab'

describe('TitleBar', () => {
  it('renders the title text', () => {
    render(<TitleBar title="tmux-box — tbox2" />)
    expect(screen.getByText('tmux-box — tbox2')).toBeDefined()
  })

  it('renders layout pattern buttons', () => {
    render(<TitleBar title="test" />)
    expect(screen.getByTestId('layout-buttons')).toBeDefined()
  })

  it('layout buttons are disabled when no active tab', () => {
    useTabStore.setState({ tabs: {}, tabOrder: [], activeTabId: null, visitHistory: [] })
    render(<TitleBar title="test" />)
    const buttons = screen.getByTestId('layout-buttons').querySelectorAll('button')
    expect(buttons).toHaveLength(4)
    for (const btn of buttons) {
      expect(btn).toHaveProperty('disabled', true)
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
    expect(buttons[0]).toHaveProperty('disabled', false)

    // Click "Split horizontal" (second button)
    fireEvent.click(buttons[1])
    const updated = useTabStore.getState().tabs[tab.id]
    expect(updated.layout.type).toBe('split')
  })

  it('keeps buttons disabled when no active tab', () => {
    useTabStore.setState({ tabs: {}, tabOrder: [], activeTabId: null, visitHistory: [] })
    render(<TitleBar title="test" />)
    const buttons = screen.getByTestId('layout-buttons').querySelectorAll('button')
    for (const btn of buttons) {
      expect(btn).toHaveProperty('disabled', true)
    }
  })
})
