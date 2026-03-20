import { describe, it, expect, vi } from 'vitest'
import { render, screen, fireEvent, cleanup } from '@testing-library/react'
import { StatusBar } from './StatusBar'

describe('StatusBar', () => {
  it('renders host and session info', () => {
    cleanup()
    render(<StatusBar hostName="mlab" sessionName="dev-server" status="connected" viewMode="terminal" viewModes={['terminal', 'stream']} onViewModeChange={vi.fn()} />)
    expect(screen.getByText('mlab')).toBeTruthy()
    expect(screen.getByText('dev-server')).toBeTruthy()
    expect(screen.getByText('connected')).toBeTruthy()
  })

  it('renders empty state when no session', () => {
    cleanup()
    render(<StatusBar hostName={null} sessionName={null} status={null} viewMode={null} viewModes={null} onViewModeChange={vi.fn()} />)
    expect(screen.getByText('No active session')).toBeTruthy()
  })

  it('shows viewMode badge when viewModes available', () => {
    cleanup()
    render(<StatusBar hostName="mlab" sessionName="dev" status="connected" viewMode="terminal" viewModes={['terminal', 'stream']} onViewModeChange={vi.fn()} />)
    expect(screen.getByText('terminal')).toBeTruthy()
  })

  it('does not show viewMode badge when no viewModes', () => {
    cleanup()
    render(<StatusBar hostName="mlab" sessionName="file.ts" status="connected" viewMode={null} viewModes={null} onViewModeChange={vi.fn()} />)
    expect(screen.queryByTitle('切換檢視模式')).toBeNull()
  })

  it('opens popup on badge click and calls onViewModeChange', () => {
    cleanup()
    const onChange = vi.fn()
    render(<StatusBar hostName="mlab" sessionName="dev" status="connected" viewMode="terminal" viewModes={['terminal', 'stream']} onViewModeChange={onChange} />)
    fireEvent.click(screen.getByTitle('切換檢視模式'))
    // popup should show both options
    const streamOption = screen.getAllByText('stream')
    fireEvent.click(streamOption[streamOption.length - 1])
    expect(onChange).toHaveBeenCalledWith('stream')
  })
})
