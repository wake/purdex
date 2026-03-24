import { describe, it, expect, beforeEach } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import { TerminalSection } from './TerminalSection'
import { useUISettingsStore } from '../../stores/useUISettingsStore'

describe('TerminalSection', () => {
  beforeEach(() => {
    useUISettingsStore.setState({
      terminalRenderer: 'webgl',
      keepAliveCount: 0,
      keepAlivePinned: false,
      terminalRevealDelay: 300,
      terminalSettingsVersion: 0,
    })
  })

  it('renders section title', () => {
    render(<TerminalSection />)
    expect(screen.getByText('Terminal')).toBeTruthy()
  })

  it('toggles renderer and bumps version', () => {
    render(<TerminalSection />)
    fireEvent.click(screen.getByText('DOM'))
    const state = useUISettingsStore.getState()
    expect(state.terminalRenderer).toBe('dom')
    expect(state.terminalSettingsVersion).toBe(1)
  })

  it('updates keep-alive count', () => {
    render(<TerminalSection />)
    const input = screen.getByLabelText('Keep-alive Count')
    fireEvent.change(input, { target: { value: '3' } })
    expect(useUISettingsStore.getState().keepAliveCount).toBe(3)
  })

  it('clamps keep-alive count to 0-10', () => {
    render(<TerminalSection />)
    const input = screen.getByLabelText('Keep-alive Count')
    fireEvent.change(input, { target: { value: '15' } })
    expect(useUISettingsStore.getState().keepAliveCount).toBe(10)
  })

  it('toggles keep-alive pinned', () => {
    render(<TerminalSection />)
    fireEvent.click(screen.getByLabelText('Keep-alive Pinned'))
    expect(useUISettingsStore.getState().keepAlivePinned).toBe(true)
  })

  it('updates reveal delay', () => {
    render(<TerminalSection />)
    const input = screen.getByLabelText('Reveal Delay')
    fireEvent.change(input, { target: { value: '500' } })
    expect(useUISettingsStore.getState().terminalRevealDelay).toBe(500)
  })
})
