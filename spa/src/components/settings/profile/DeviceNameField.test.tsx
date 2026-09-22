import { describe, it, expect, beforeEach } from 'vitest'
import { render, screen, fireEvent, act } from '@testing-library/react'
import { DeviceNameField } from './DeviceNameField'
import { useDeviceNameStore } from '../../../stores/useDeviceNameStore'

function nameInput() {
  return screen.getByTestId('device-state-name') as HTMLInputElement
}

beforeEach(() => {
  useDeviceNameStore.setState({
    deviceName: null,
    defaultDeviceName: 'Chrome · macOS',
  })
})

describe('DeviceNameField', () => {
  it('shows the default name as value and placeholder when no override', () => {
    render(<DeviceNameField />)
    const input = nameInput()
    expect(input.value).toBe('Chrome · macOS')
    expect(input.placeholder).toBe('Chrome · macOS')
    expect(input).toHaveAccessibleName()
  })

  it('shows the custom name when set', () => {
    useDeviceNameStore.setState({ deviceName: 'Work Air' })
    render(<DeviceNameField />)
    expect(nameInput().value).toBe('Work Air')
  })

  it('commits a trimmed rename on Enter', () => {
    render(<DeviceNameField />)
    fireEvent.change(nameInput(), { target: { value: '  Studio  ' } })
    fireEvent.keyDown(nameInput(), { key: 'Enter' })
    expect(useDeviceNameStore.getState().deviceName).toBe('Studio')
    expect(nameInput().value).toBe('Studio')
  })

  it('commits a trimmed rename on blur', () => {
    render(<DeviceNameField />)
    fireEvent.change(nameInput(), { target: { value: ' Laptop ' } })
    fireEvent.blur(nameInput())
    expect(useDeviceNameStore.getState().deviceName).toBe('Laptop')
    expect(nameInput().value).toBe('Laptop')
  })

  it('blur without typing does not create an override', () => {
    render(<DeviceNameField />)
    fireEvent.blur(nameInput())
    expect(useDeviceNameStore.getState().deviceName).toBeNull()
  })

  it('does not commit on Enter while composing', () => {
    render(<DeviceNameField />)
    fireEvent.change(nameInput(), { target: { value: '筆電' } })
    fireEvent.keyDown(nameInput(), { key: 'Enter', isComposing: true })
    expect(useDeviceNameStore.getState().deviceName).toBeNull()
    expect(nameInput().value).toBe('筆電')
  })

  it('keeps an uncommitted draft when the default name resolves', () => {
    render(<DeviceNameField />)
    fireEvent.change(nameInput(), { target: { value: 'My dra' } })
    act(() => {
      useDeviceNameStore.setState({ defaultDeviceName: 'Electron · macOS' })
    })
    expect(nameInput().value).toBe('My dra')
    fireEvent.keyDown(nameInput(), { key: 'Enter' })
    expect(useDeviceNameStore.getState().deviceName).toBe('My dra')
    expect(nameInput().value).toBe('My dra')
  })

  it('keeps an uncommitted draft when the stored override changes', () => {
    render(<DeviceNameField />)
    fireEvent.change(nameInput(), { target: { value: 'Local edit' } })
    act(() => {
      useDeviceNameStore.setState({ deviceName: 'From sync' })
    })
    expect(nameInput().value).toBe('Local edit')
    fireEvent.blur(nameInput())
    expect(useDeviceNameStore.getState().deviceName).toBe('Local edit')
  })

  it('an untouched field follows store changes', () => {
    render(<DeviceNameField />)
    act(() => {
      useDeviceNameStore.setState({ defaultDeviceName: 'Electron · macOS' })
    })
    expect(nameInput().value).toBe('Electron · macOS')
    act(() => {
      useDeviceNameStore.setState({ deviceName: 'From sync' })
    })
    expect(nameInput().value).toBe('From sync')
  })

  it('reflects later store changes after a commit', () => {
    render(<DeviceNameField />)
    fireEvent.change(nameInput(), { target: { value: 'draft' } })
    fireEvent.blur(nameInput())
    expect(nameInput().value).toBe('draft')
    act(() => {
      useDeviceNameStore.setState({ deviceName: 'From sync' })
    })
    expect(nameInput().value).toBe('From sync')
  })

  it('committing a blank name falls back to the default', () => {
    useDeviceNameStore.setState({ deviceName: 'Work Air' })
    render(<DeviceNameField />)
    fireEvent.change(nameInput(), { target: { value: '   ' } })
    fireEvent.blur(nameInput())
    expect(useDeviceNameStore.getState().deviceName).toBeNull()
    expect(nameInput().value).toBe('Chrome · macOS')
  })

  it('hides the reset button when there is no override', () => {
    render(<DeviceNameField />)
    expect(screen.queryByTestId('device-state-name-reset')).not.toBeInTheDocument()
  })

  it('reset restores the default name', () => {
    useDeviceNameStore.setState({ deviceName: 'Work Air' })
    render(<DeviceNameField />)
    fireEvent.click(screen.getByTestId('device-state-name-reset'))
    expect(useDeviceNameStore.getState().deviceName).toBeNull()
    expect(nameInput().value).toBe('Chrome · macOS')
    expect(screen.queryByTestId('device-state-name-reset')).not.toBeInTheDocument()
  })

  it('reset clears a draft and follows later default changes', () => {
    useDeviceNameStore.setState({ deviceName: 'Work Air' })
    render(<DeviceNameField />)
    fireEvent.change(nameInput(), { target: { value: 'half-typed' } })
    fireEvent.click(screen.getByTestId('device-state-name-reset'))
    expect(useDeviceNameStore.getState().deviceName).toBeNull()
    expect(nameInput().value).toBe('Chrome · macOS')
    act(() => {
      useDeviceNameStore.setState({ defaultDeviceName: 'Electron · macOS' })
    })
    expect(nameInput().value).toBe('Electron · macOS')
  })

  it('Escape discards the draft and resumes following the store', () => {
    useDeviceNameStore.setState({ deviceName: 'Work Air' })
    render(<DeviceNameField />)
    fireEvent.change(nameInput(), { target: { value: 'oops' } })
    fireEvent.keyDown(nameInput(), { key: 'Escape' })
    expect(nameInput().value).toBe('Work Air')
    expect(useDeviceNameStore.getState().deviceName).toBe('Work Air')
    act(() => {
      useDeviceNameStore.setState({ deviceName: 'From sync' })
    })
    expect(nameInput().value).toBe('From sync')
  })
})
