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
      tabIndicatorStyle: 'badge',
      ccIconVariant: 'bot',
      codexIconVariant: 'openai',
      dynamicTabName: false,
      tabNameTooltipMode: 'both',
      showAgentTitleInStatusBar: false,
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
    expect(useUISettingsStore.getState().tabIndicatorStyle).toBe('icon')
    fireEvent.click(screen.getByText('Dot only'))
    expect(useUISettingsStore.getState().tabIndicatorStyle).toBe('dot')
    fireEvent.click(screen.getByText('Dot beside icon'))
    expect(useUISettingsStore.getState().tabIndicatorStyle).toBe('iconDot')
  })

  it('updates ccIconVariant when a cc icon button is clicked', () => {
    render(<TerminalSection />)
    fireEvent.click(screen.getByRole('button', { name: /Star/ }))
    expect(useUISettingsStore.getState().ccIconVariant).toBe('star')
    fireEvent.click(screen.getByRole('button', { name: /^Bot$/ }))
    expect(useUISettingsStore.getState().ccIconVariant).toBe('bot')
  })

  it('updates codexIconVariant when a codex icon button is clicked', () => {
    render(<TerminalSection />)
    fireEvent.click(screen.getByRole('button', { name: /^Codex$/ }))
    expect(useUISettingsStore.getState().codexIconVariant).toBe('codex')
    fireEvent.click(screen.getByRole('button', { name: /^OpenAI$/ }))
    expect(useUISettingsStore.getState().codexIconVariant).toBe('openai')
  })

  it('shows hidden hints for both cc and codex rows only in dot mode', () => {
    useUISettingsStore.setState({ tabIndicatorStyle: 'badge' })
    const { rerender } = render(<TerminalSection />)
    expect(screen.queryByText(/no visible effect/i)).toBeNull()
    useUISettingsStore.setState({ tabIndicatorStyle: 'dot' })
    rerender(<TerminalSection />)
    expect(screen.getAllByText(/no visible effect/i)).toHaveLength(2)
  })

  it('renders Dynamic tab name and Show in status bar controls', () => {
    render(<TerminalSection />)
    expect(screen.getByLabelText('Dynamic tab name')).toBeTruthy()
    expect(screen.getByLabelText('Show in status bar')).toBeTruthy()
  })

  it('toggles dynamic tab name', () => {
    render(<TerminalSection />)
    fireEvent.click(screen.getByLabelText('Dynamic tab name'))
    expect(useUISettingsStore.getState().dynamicTabName).toBe(true)
  })

  it('renders tab name tooltip setting below Dynamic tab name', () => {
    render(<TerminalSection />)
    const dynamicControl = screen.getByLabelText('Dynamic tab name')
    const tooltipControl = screen.getByText('Tab name tooltip')
    expect(dynamicControl.compareDocumentPosition(tooltipControl) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy()
    expect(screen.getByText('None')).toBeTruthy()
    expect(screen.getByText('Top')).toBeTruthy()
    expect(screen.getByText('Left')).toBeTruthy()
    expect(screen.getByText('Both')).toBeTruthy()
  })

  it('updates tab name tooltip mode', () => {
    render(<TerminalSection />)
    fireEvent.click(screen.getByText('Left'))
    expect(useUISettingsStore.getState().tabNameTooltipMode).toBe('left')
  })

  it('toggles show in status bar', () => {
    render(<TerminalSection />)
    fireEvent.click(screen.getByLabelText('Show in status bar'))
    expect(useUISettingsStore.getState().showAgentTitleInStatusBar).toBe(true)
  })

})
