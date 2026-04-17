import { describe, it, expect, beforeEach } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import { TerminalSection } from './TerminalSection'
import { useUISettingsStore } from '../../stores/useUISettingsStore'
import { useAgentStore } from '../../stores/useAgentStore'

describe('TerminalSection', () => {
  beforeEach(() => {
    useUISettingsStore.setState({
      terminalRenderer: 'webgl',
      keepAliveCount: 0,
      keepAlivePinned: false,
      terminalRevealDelay: 300,
      terminalSettingsVersion: 0,
    })
    useAgentStore.setState({ tabIndicatorStyle: 'badge', ccIconVariant: 'bot', showOscTitle: false })
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

  it('clamps keep-alive count to 0-6 when renderer is webgl', () => {
    useUISettingsStore.setState({ terminalRenderer: 'webgl', keepAliveCount: 0 })
    render(<TerminalSection />)
    const input = screen.getByLabelText('Keep-alive Count')
    fireEvent.change(input, { target: { value: '15' } })
    expect(useUISettingsStore.getState().keepAliveCount).toBe(6)
  })

  it('clamps keep-alive count to 0-10 when renderer is dom', () => {
    useUISettingsStore.setState({ terminalRenderer: 'dom', keepAliveCount: 0 })
    render(<TerminalSection />)
    const input = screen.getByLabelText('Keep-alive Count')
    fireEvent.change(input, { target: { value: '15' } })
    expect(useUISettingsStore.getState().keepAliveCount).toBe(10)
  })

  it('auto-clamps keepAliveCount when switching from dom to webgl', () => {
    useUISettingsStore.setState({ terminalRenderer: 'dom', keepAliveCount: 8 })
    render(<TerminalSection />)
    fireEvent.click(screen.getByText('WebGL'))
    expect(useUISettingsStore.getState().keepAliveCount).toBe(6)
  })

  it('shows webgl hint when renderer is webgl', () => {
    useUISettingsStore.setState({ terminalRenderer: 'webgl' })
    render(<TerminalSection />)
    expect(screen.getByText(/GPU context/i)).toBeTruthy()
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

  it('clamps negative keep-alive count to 0', () => {
    render(<TerminalSection />)
    const input = screen.getByLabelText('Keep-alive Count')
    fireEvent.change(input, { target: { value: '-5' } })
    expect(useUISettingsStore.getState().keepAliveCount).toBe(0)
  })

  it('clamps reveal delay to 0-2000', () => {
    render(<TerminalSection />)
    const input = screen.getByLabelText('Reveal Delay')
    fireEvent.change(input, { target: { value: '5000' } })
    expect(useUISettingsStore.getState().terminalRevealDelay).toBe(2000)
  })

  it('does not bump version when selecting same renderer', () => {
    render(<TerminalSection />)
    fireEvent.click(screen.getByText('WebGL')) // already selected
    expect(useUISettingsStore.getState().terminalSettingsVersion).toBe(0)
  })

  it('updates tabIndicatorStyle when a segment is clicked', () => {
    render(<TerminalSection />)
    fireEvent.click(screen.getByText('Icon only'))
    expect(useAgentStore.getState().tabIndicatorStyle).toBe('icon')
    fireEvent.click(screen.getByText('Dot only'))
    expect(useAgentStore.getState().tabIndicatorStyle).toBe('dot')
    fireEvent.click(screen.getByText('Dot beside icon'))
    expect(useAgentStore.getState().tabIndicatorStyle).toBe('iconDot')
  })

  it('updates ccIconVariant when a cc icon button is clicked', () => {
    render(<TerminalSection />)
    fireEvent.click(screen.getByRole('button', { name: /Star/ }))
    expect(useAgentStore.getState().ccIconVariant).toBe('star')
    fireEvent.click(screen.getByRole('button', { name: /Bot/ }))
    expect(useAgentStore.getState().ccIconVariant).toBe('bot')
  })

  it('shows cc_icon hidden hint only in dot mode', () => {
    useAgentStore.setState({ tabIndicatorStyle: 'badge' })
    const { rerender } = render(<TerminalSection />)
    expect(screen.queryByText(/no visible effect/i)).toBeNull()
    useAgentStore.setState({ tabIndicatorStyle: 'dot' })
    rerender(<TerminalSection />)
    expect(screen.getByText(/no visible effect/i)).toBeTruthy()
  })

  it('toggles showOscTitle', () => {
    render(<TerminalSection />)
    fireEvent.click(screen.getByLabelText('Show agent dynamic title'))
    expect(useAgentStore.getState().showOscTitle).toBe(true)
    fireEvent.click(screen.getByLabelText('Show agent dynamic title'))
    expect(useAgentStore.getState().showOscTitle).toBe(false)
  })
})
