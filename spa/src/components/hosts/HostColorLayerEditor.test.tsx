import { describe, it, expect, vi } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import { HostColorLayerEditor } from './HostColorLayerEditor'
import { HOST_COLOR_PRESETS } from '../../lib/host-color'

const base = { layer: 'main' as const, color: '#3b82f6', alpha: 100, inherited: false, onClose: () => {} }

describe('HostColorLayerEditor — main', () => {
  it('shows presets, H/S/L/A ranges, hex and a preview', () => {
    render(<HostColorLayerEditor {...base} onChange={() => {}} />)
    for (const hex of HOST_COLOR_PRESETS) expect(screen.getByRole('button', { name: hex })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: '#3b82f6' })).toHaveAttribute('aria-pressed', 'true')
    for (const id of ['h', 's', 'l', 'a']) expect(screen.getByTestId(`host-color-range-${id}`)).toBeInTheDocument()
    expect((screen.getByTestId('host-color-hex') as HTMLInputElement).value).toBe('#3b82f6')
    // jsdom's cssstyle serializer collapses an opaque `rgba(r, g, b, 1)` down to
    // `rgb(r, g, b)` on any style assignment (verified independent of React with
    // a plain jsdom Document), so the literal alpha=1 string never survives a
    // round trip through `.style`. Probe the same collapse instead of hardcoding
    // its output, so this still pins "preview uses rgbaString(color, alpha)".
    const probe = document.createElement('div')
    probe.style.background = 'rgba(59, 130, 246, 1)'
    expect(screen.getByTestId('host-color-preview').style.background).toBe(probe.style.background)
    expect(screen.queryByTestId('host-color-inherit')).toBeNull()
  })

  it('preset click writes that color with the current alpha', () => {
    const onChange = vi.fn()
    render(<HostColorLayerEditor {...base} alpha={80} onChange={onChange} />)
    fireEvent.click(screen.getByRole('button', { name: HOST_COLOR_PRESETS[2] }))
    expect(onChange).toHaveBeenCalledWith({ color: HOST_COLOR_PRESETS[2], alpha: 80 })
  })

  it('dragging hue writes a new hex on every input event, keeping s/l/alpha', () => {
    const onChange = vi.fn()
    render(<HostColorLayerEditor {...base} color="#ff0000" onChange={onChange} />)
    fireEvent.input(screen.getByTestId('host-color-range-h'), { target: { value: '120' } })
    expect(onChange).toHaveBeenLastCalledWith({ color: '#00ff00', alpha: 100 })
  })

  it('keeps hue while the color is grey: hue drag then saturation drag yields that hue', () => {
    const onChange = vi.fn()
    const { rerender } = render(<HostColorLayerEditor {...base} color="#808080" onChange={onChange} />)
    fireEvent.input(screen.getByTestId('host-color-range-h'), { target: { value: '120' } })
    // Grey has no hue in hex form: the write is a no-op color-wise, the parent re-renders with the same hex…
    rerender(<HostColorLayerEditor {...base} color="#808080" onChange={onChange} />)
    // …but the editor must remember h=120 so the next saturation drag is green, not red.
    fireEvent.input(screen.getByTestId('host-color-range-s'), { target: { value: '100' } })
    const last = onChange.mock.calls.at(-1)![0] as { color: string }
    expect(last.color).toBe('#00ff00')
  })

  it('keeps saturation and hue while the color is black or white', () => {
    const onChange = vi.fn()
    const { rerender } = render(<HostColorLayerEditor {...base} color="#000000" onChange={onChange} />)
    fireEvent.input(screen.getByTestId('host-color-range-h'), { target: { value: '240' } })
    rerender(<HostColorLayerEditor {...base} color="#000000" onChange={onChange} />)
    fireEvent.input(screen.getByTestId('host-color-range-s'), { target: { value: '100' } })
    rerender(<HostColorLayerEditor {...base} color="#000000" onChange={onChange} />)
    fireEvent.input(screen.getByTestId('host-color-range-l'), { target: { value: '50' } })
    const last = onChange.mock.calls.at(-1)![0] as { color: string }
    expect(last.color).toBe('#0000ff')
  })

  it('re-derives HSL when the parent hands in a different color (preset click elsewhere)', () => {
    const onChange = vi.fn()
    const { rerender } = render(<HostColorLayerEditor {...base} color="#ff0000" onChange={onChange} />)
    rerender(<HostColorLayerEditor {...base} color="#0000ff" onChange={onChange} />)
    expect((screen.getByTestId('host-color-range-h') as HTMLInputElement).value).toBe('240')
  })

  it('dragging alpha writes only alpha', () => {
    const onChange = vi.fn()
    render(<HostColorLayerEditor {...base} onChange={onChange} />)
    fireEvent.input(screen.getByTestId('host-color-range-a'), { target: { value: '35' } })
    expect(onChange).toHaveBeenLastCalledWith({ color: '#3b82f6', alpha: 35 })
  })

  it('hex input commits a normalized value on Enter and blur; invalid shows an alert and does not write', () => {
    const onChange = vi.fn()
    render(<HostColorLayerEditor {...base} onChange={onChange} />)
    const hex = screen.getByTestId('host-color-hex') as HTMLInputElement
    fireEvent.change(hex, { target: { value: 'ABCDEF' } })
    fireEvent.keyDown(hex, { key: 'Enter' })
    expect(onChange).toHaveBeenLastCalledWith({ color: '#abcdef', alpha: 100 })
    fireEvent.change(hex, { target: { value: 'red' } })
    fireEvent.blur(hex)
    expect(screen.getByRole('alert')).toBeInTheDocument()
    expect(onChange).toHaveBeenCalledTimes(1)
  })

  it('close button calls onClose', () => {
    const onClose = vi.fn()
    render(<HostColorLayerEditor {...base} onChange={() => {}} onClose={onClose} />)
    fireEvent.click(screen.getByTestId('host-color-editor-close'))
    expect(onClose).toHaveBeenCalled()
  })
})

describe('HostColorLayerEditor — middle/light', () => {
  it('inherited: only the inherit switch (on) and the alpha range are shown', () => {
    render(<HostColorLayerEditor {...base} layer="middle" alpha={60} inherited onChange={() => {}} />)
    expect(screen.getByTestId('host-color-inherit')).toHaveAttribute('aria-checked', 'true')
    expect(screen.getByTestId('host-color-range-a')).toBeInTheDocument()
    expect(screen.queryByTestId('host-color-range-h')).toBeNull()
    expect(screen.queryByTestId('host-color-hex')).toBeNull()
    expect(screen.queryByRole('button', { name: HOST_COLOR_PRESETS[0] })).toBeNull()
  })

  it('inherited alpha drag writes alpha without a color', () => {
    const onChange = vi.fn()
    render(<HostColorLayerEditor {...base} layer="light" alpha={22} inherited onChange={onChange} />)
    fireEvent.input(screen.getByTestId('host-color-range-a'), { target: { value: '40' } })
    expect(onChange).toHaveBeenLastCalledWith({ alpha: 40 })
  })

  it('turning inherit off writes the inherited color explicitly; turning it on drops the color', () => {
    const onChange = vi.fn()
    const { rerender } = render(<HostColorLayerEditor {...base} layer="middle" alpha={60} inherited onChange={onChange} />)
    fireEvent.click(screen.getByTestId('host-color-inherit'))
    expect(onChange).toHaveBeenLastCalledWith({ color: '#3b82f6', alpha: 60 })
    rerender(<HostColorLayerEditor {...base} layer="middle" alpha={60} inherited={false} onChange={onChange} />)
    expect(screen.getByTestId('host-color-range-h')).toBeInTheDocument()
    fireEvent.click(screen.getByTestId('host-color-inherit'))
    expect(onChange).toHaveBeenLastCalledWith({ alpha: 60 })
  })
})
