import { describe, it, expect, beforeEach } from 'vitest'
import { render, screen, fireEvent, act } from '@testing-library/react'
import { DeviceStateSection } from './DeviceStateSection'
import { useDeviceStateStore } from '../../../stores/useDeviceStateStore'
import type { DeviceStateStatus } from '../../../stores/useDeviceStateStore'
import { useHostStore } from '../../../stores/useHostStore'

function nameInput() {
  return screen.getByTestId('device-state-name') as HTMLInputElement
}

beforeEach(() => {
  useDeviceStateStore.setState({
    deviceName: null,
    defaultDeviceName: 'Chrome · macOS',
    status: { kind: 'idle' },
  })
  useHostStore.setState({
    hosts: { h1: { id: 'h1', name: 'Mini', ip: '100.64.0.2', port: 7860, order: 0 } },
    hostOrder: ['h1'],
    runtime: {},
    devHostId: null,
  })
})

describe('DeviceStateSection', () => {
  it('renders a heading', () => {
    render(<DeviceStateSection />)
    expect(screen.getByRole('heading', { level: 3 })).toBeInTheDocument()
  })

  it('shows the default name as value and placeholder when no override', () => {
    render(<DeviceStateSection />)
    const input = nameInput()
    expect(input.value).toBe('Chrome · macOS')
    expect(input.placeholder).toBe('Chrome · macOS')
    expect(input).toHaveAccessibleName()
  })

  it('shows the custom name when set', () => {
    useDeviceStateStore.setState({ deviceName: 'Work Air' })
    render(<DeviceStateSection />)
    expect(nameInput().value).toBe('Work Air')
  })

  it('commits a trimmed rename on Enter', () => {
    render(<DeviceStateSection />)
    fireEvent.change(nameInput(), { target: { value: '  Studio  ' } })
    fireEvent.keyDown(nameInput(), { key: 'Enter' })
    expect(useDeviceStateStore.getState().deviceName).toBe('Studio')
  })

  it('commits a trimmed rename on blur', () => {
    render(<DeviceStateSection />)
    fireEvent.change(nameInput(), { target: { value: ' Laptop ' } })
    fireEvent.blur(nameInput())
    expect(useDeviceStateStore.getState().deviceName).toBe('Laptop')
    expect(nameInput().value).toBe('Laptop')
  })

  it('does not commit on Enter while composing', () => {
    render(<DeviceStateSection />)
    fireEvent.change(nameInput(), { target: { value: '筆電' } })
    fireEvent.keyDown(nameInput(), { key: 'Enter', isComposing: true })
    expect(useDeviceStateStore.getState().deviceName).toBeNull()
  })

  it('hides the reset button when there is no override', () => {
    render(<DeviceStateSection />)
    expect(screen.queryByTestId('device-state-name-reset')).not.toBeInTheDocument()
  })

  it('reset button restores the default name', () => {
    useDeviceStateStore.setState({ deviceName: 'Work Air' })
    render(<DeviceStateSection />)
    fireEvent.click(screen.getByTestId('device-state-name-reset'))
    expect(useDeviceStateStore.getState().deviceName).toBeNull()
    expect(nameInput().value).toBe('Chrome · macOS')
    expect(screen.queryByTestId('device-state-name-reset')).not.toBeInTheDocument()
  })

  it('resyncs the draft when the stored name changes externally', () => {
    render(<DeviceStateSection />)
    fireEvent.change(nameInput(), { target: { value: 'draft' } })
    fireEvent.blur(nameInput())
    expect(nameInput().value).toBe('draft')
    // Simulate a sync / other window update.
    act(() => {
      useDeviceStateStore.setState({ deviceName: 'From sync' })
    })
    expect(nameInput().value).toBe('From sync')
  })

  it('shows "not set" when no dev host is selected', () => {
    render(<DeviceStateSection />)
    const target = screen.getByTestId('device-state-target')
    expect(target).toHaveAttribute('data-target', 'none')
    expect(target.textContent).not.toContain('Mini')
  })

  it('shows the dev host name as the target', () => {
    useHostStore.setState({ devHostId: 'h1' })
    render(<DeviceStateSection />)
    const target = screen.getByTestId('device-state-target')
    expect(target).toHaveAttribute('data-target', 'h1')
    expect(target.textContent).toContain('Mini')
  })

  it('treats a dangling dev host id as not set', () => {
    useHostStore.setState({ devHostId: 'gone' })
    render(<DeviceStateSection />)
    expect(screen.getByTestId('device-state-target')).toHaveAttribute('data-target', 'none')
  })

  const kinds: DeviceStateStatus[] = [
    { kind: 'idle' },
    { kind: 'no-target' },
    { kind: 'offline', hostId: 'h1' },
    { kind: 'uploading', hostId: 'h1' },
    { kind: 'ok', at: Date.now(), hostId: 'h1' },
    { kind: 'error', message: 'boom-503', hostId: 'h1' },
  ]
  for (const status of kinds) {
    it(`renders status kind ${status.kind}`, () => {
      useDeviceStateStore.setState({ status })
      render(<DeviceStateSection />)
      const line = screen.getByTestId('device-state-status')
      expect(line).toHaveAttribute('data-kind', status.kind)
      expect(line.textContent?.trim()).not.toBe('')
      // Never leak raw interpolation placeholders.
      expect(line.textContent).not.toContain('{{')
      if (status.kind === 'error') expect(line.textContent).toContain('boom-503')
    })
  }
})
