import { describe, it, expect, beforeEach } from 'vitest'
import { render, screen, fireEvent, act } from '@testing-library/react'
import { HostColorField } from './HostColorField'
import { useHostStore } from '../../stores/useHostStore'
import { HOST_COLOR_PRESETS } from '../../lib/host-color'

const HOST_ID = 'h1'

function host() {
  return useHostStore.getState().hosts[HOST_ID]
}

function hexInput() {
  return screen.getByTestId('host-color-hex') as HTMLInputElement
}

beforeEach(() => {
  useHostStore.setState({
    hosts: { [HOST_ID]: { id: HOST_ID, name: 'H', ip: '1.2.3.4', port: 7860, order: 0 } },
    hostOrder: [HOST_ID],
    runtime: {},
  })
})

describe('HostColorField', () => {
  it('renders 8 preset swatches, none pressed without a color', () => {
    render(<HostColorField hostId={HOST_ID} />)
    for (const hex of HOST_COLOR_PRESETS) {
      expect(screen.getByRole('button', { name: hex })).toHaveAttribute('aria-pressed', 'false')
    }
  })

  it('preset click saves that hex and marks it pressed', () => {
    render(<HostColorField hostId={HOST_ID} />)
    const hex = HOST_COLOR_PRESETS[5]
    fireEvent.click(screen.getByRole('button', { name: hex }))
    expect(host().colors?.console?.main.color).toBe(hex)
    expect(screen.getByRole('button', { name: hex })).toHaveAttribute('aria-pressed', 'true')
    expect(hexInput().value).toBe(hex)
  })

  it('valid hex + Enter saves normalized value', () => {
    render(<HostColorField hostId={HOST_ID} />)
    fireEvent.change(hexInput(), { target: { value: 'ABCDEF' } })
    fireEvent.keyDown(hexInput(), { key: 'Enter' })
    expect(host().colors?.console?.main.color).toBe('#abcdef')
    expect(screen.queryByRole('alert')).toBeNull()
  })

  it('blur commits', () => {
    render(<HostColorField hostId={HOST_ID} />)
    fireEvent.change(hexInput(), { target: { value: '#123456' } })
    fireEvent.blur(hexInput())
    expect(host().colors?.console?.main.color).toBe('#123456')
  })

  it('invalid hex shows alert and does not change store', () => {
    useHostStore.getState().setHostColor(HOST_ID, '#3b82f6')
    render(<HostColorField hostId={HOST_ID} />)
    fireEvent.change(hexInput(), { target: { value: 'red' } })
    fireEvent.keyDown(hexInput(), { key: 'Enter' })
    expect(screen.getByRole('alert')).toBeInTheDocument()
    expect(host().colors?.console?.main.color).toBe('#3b82f6')
  })

  it('valid commit after invalid clears the alert', () => {
    render(<HostColorField hostId={HOST_ID} />)
    fireEvent.change(hexInput(), { target: { value: 'red' } })
    fireEvent.blur(hexInput())
    expect(screen.getByRole('alert')).toBeInTheDocument()
    fireEvent.change(hexInput(), { target: { value: '#00ff00' } })
    fireEvent.blur(hexInput())
    expect(screen.queryByRole('alert')).toBeNull()
    expect(host().colors?.console?.main.color).toBe('#00ff00')
  })

  it('empty input commit clears the color', () => {
    useHostStore.getState().setHostColor(HOST_ID, '#3b82f6')
    render(<HostColorField hostId={HOST_ID} />)
    fireEvent.change(hexInput(), { target: { value: '  ' } })
    fireEvent.keyDown(hexInput(), { key: 'Enter' })
    expect('color' in host()).toBe(false)
  })

  it('clear button removes the color key', () => {
    useHostStore.getState().setHostColor(HOST_ID, '#3b82f6')
    render(<HostColorField hostId={HOST_ID} />)
    fireEvent.click(screen.getByTestId('host-color-clear'))
    expect('color' in host()).toBe(false)
    expect(hexInput().value).toBe('')
  })

  it('clear on uncolored host resets invalid draft and alert', () => {
    render(<HostColorField hostId={HOST_ID} />)
    fireEvent.change(hexInput(), { target: { value: 'red' } })
    fireEvent.keyDown(hexInput(), { key: 'Enter' })
    expect(screen.getByRole('alert')).toBeInTheDocument()
    fireEvent.click(screen.getByTestId('host-color-clear'))
    expect(hexInput().value).toBe('')
    expect(screen.queryByRole('alert')).toBeNull()
    expect(host().color).toBeUndefined()
  })

  it('clear on colored host with uncommitted invalid draft resets draft and removes color', () => {
    useHostStore.getState().setHostColor(HOST_ID, '#3b82f6')
    render(<HostColorField hostId={HOST_ID} />)
    fireEvent.change(hexInput(), { target: { value: 'nope' } })
    fireEvent.click(screen.getByTestId('host-color-clear'))
    expect(hexInput().value).toBe('')
    expect('color' in host()).toBe(false)
    expect(screen.queryByRole('alert')).toBeNull()
  })

  it('Enter during IME composition does not commit', () => {
    render(<HostColorField hostId={HOST_ID} />)
    fireEvent.change(hexInput(), { target: { value: 'abcdef' } })
    fireEvent.keyDown(hexInput(), { key: 'Enter', isComposing: true })
    expect(host().color).toBeUndefined()
  })

  it('re-syncs draft when stored color changes externally', () => {
    render(<HostColorField hostId={HOST_ID} />)
    act(() => useHostStore.getState().setHostColor(HOST_ID, '#ec4899'))
    expect(hexInput().value).toBe('#ec4899')
  })

  it.each([
    ['non-string object', {}],
    ['css injection string', 'url(x)'],
  ])('tolerates malformed stored color (%s): no throw, empty input, nothing pressed', (_label, bad) => {
    useHostStore.setState({
      hosts: { [HOST_ID]: { id: HOST_ID, name: 'H', ip: '1.2.3.4', port: 7860, order: 0, color: bad as never } },
    })
    render(<HostColorField hostId={HOST_ID} />)
    expect(hexInput().value).toBe('')
    for (const hex of HOST_COLOR_PRESETS) {
      expect(screen.getByRole('button', { name: hex })).toHaveAttribute('aria-pressed', 'false')
    }
    expect(() => {
      fireEvent.focus(hexInput())
      fireEvent.blur(hexInput())
    }).not.toThrow()
    expect(() => fireEvent.keyDown(hexInput(), { key: 'Enter' })).not.toThrow()
    expect(hexInput().value).toBe('')
    expect(screen.queryByRole('alert')).toBeNull()
  })

  it('resyncs to empty when stored color turns malformed externally', () => {
    useHostStore.getState().setHostColor(HOST_ID, '#3b82f6')
    render(<HostColorField hostId={HOST_ID} />)
    act(() => {
      useHostStore.setState({
        hosts: { [HOST_ID]: { id: HOST_ID, name: 'H', ip: '1.2.3.4', port: 7860, order: 0, color: {} as never } },
      })
    })
    expect(hexInput().value).toBe('')
    expect(() => fireEvent.blur(hexInput())).not.toThrow()
  })

  it('marks the preset pressed from colors.console.main', () => {
    useHostStore.getState().setHostColorLayer(HOST_ID, 'console', 'main', { color: '#3b82f6', alpha: 70 })
    render(<HostColorField hostId={HOST_ID} />)
    expect(screen.getByRole('button', { name: '#3b82f6' })).toHaveAttribute('aria-pressed', 'true')
  })
})
