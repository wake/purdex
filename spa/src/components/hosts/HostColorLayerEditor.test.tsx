import { describe, it, expect, vi } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import { HostColorLayerEditor } from './HostColorLayerEditor'
import { HOST_COLOR_PRESETS } from '../../lib/host-color'
import { hsvToHex } from '../../lib/color-space'

const base = { layer: 'main' as const, color: '#3b82f6', alpha: 100, inherited: false, onClose: () => {} }

function areaRect(el: HTMLElement) {
  el.getBoundingClientRect = () => ({ left: 0, top: 0, width: 200, height: 200, right: 200, bottom: 200, x: 0, y: 0, toJSON() {} }) as DOMRect
}
function pointAt(el: HTMLElement, x: number, y: number, pointerId = 1) {
  el.setPointerCapture = () => {}
  el.releasePointerCapture = () => {}
  fireEvent.pointerDown(el, { clientX: x, clientY: y, pointerId, button: 0 })
}

describe('HostColorLayerEditor — main', () => {
  it('shows presets, the area, hue + alpha strips, hex and a preview', () => {
    render(<HostColorLayerEditor {...base} onChange={() => {}} />)
    for (const hex of HOST_COLOR_PRESETS) expect(screen.getByRole('button', { name: hex })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: '#3b82f6' })).toHaveAttribute('aria-pressed', 'true')
    expect(screen.getByTestId('host-color-area')).toBeInTheDocument()
    expect(screen.getByTestId('host-color-area-marker')).toBeInTheDocument()
    expect(screen.getByTestId('host-color-range-h')).toBeInTheDocument()
    expect(screen.getByTestId('host-color-range-a')).toBeInTheDocument()
    expect(screen.queryByTestId('host-color-range-s')).toBeNull()
    expect(screen.queryByTestId('host-color-range-l')).toBeNull()
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

  it('the area is painted with the current hue and the marker sits at (s, 100−v)', () => {
    render(<HostColorLayerEditor {...base} color="#ff0000" onChange={() => {}} />)
    const area = screen.getByTestId('host-color-area')
    // jsdom's cssstyle serializer normalizes hex colors inside a gradient to
    // `rgb(...)` on assignment (same collapse as the rgba-alpha probe above),
    // so compare against the same normalization instead of the literal hex.
    const probe = document.createElement('div')
    probe.style.background = '#ff0000'
    expect(area.style.background).toContain(probe.style.background)
    const marker = screen.getByTestId('host-color-area-marker')
    expect(marker.style.left).toBe('100%')
    expect(marker.style.top).toBe('0%')
  })

  it('pointer down on the area writes s/v from the position, keeping hue and alpha', () => {
    const onChange = vi.fn()
    render(<HostColorLayerEditor {...base} color="#ff0000" alpha={80} onChange={onChange} />)
    const area = screen.getByTestId('host-color-area')
    areaRect(area)
    pointAt(area, 0, 0) // s=0, v=100 → white
    expect(onChange).toHaveBeenLastCalledWith({ color: '#ffffff', alpha: 80 })
    fireEvent.pointerUp(area, { pointerId: 1 })
    pointAt(area, 200, 200) // s=100, v=0 → black
    expect(onChange).toHaveBeenLastCalledWith({ color: '#000000', alpha: 80 })
    fireEvent.pointerUp(area, { pointerId: 1 })
    pointAt(area, 200, 0) // s=100, v=100 → pure hue
    expect(onChange).toHaveBeenLastCalledWith({ color: '#ff0000', alpha: 80 })
  })

  it('pointer move while captured keeps writing; positions are clamped to the area', () => {
    const onChange = vi.fn()
    render(<HostColorLayerEditor {...base} color="#ff0000" onChange={onChange} />)
    const area = screen.getByTestId('host-color-area')
    areaRect(area)
    pointAt(area, 100, 100)
    fireEvent.pointerMove(area, { clientX: 500, clientY: -50, pointerId: 1 })
    expect(onChange).toHaveBeenLastCalledWith({ color: '#ff0000', alpha: 100 })
    fireEvent.pointerUp(area, { pointerId: 1 })
    const calls = onChange.mock.calls.length
    fireEvent.pointerMove(area, { clientX: 10, clientY: 10, pointerId: 1 })
    expect(onChange.mock.calls.length).toBe(calls)
  })

  it('tracks only the pointer that started the drag; other pointer ids are ignored', () => {
    const onChange = vi.fn()
    render(<HostColorLayerEditor {...base} color="#ff0000" onChange={onChange} />)
    const area = screen.getByTestId('host-color-area')
    areaRect(area)
    pointAt(area, 100, 100, 1)
    const afterDown = onChange.mock.calls.length
    // A different pointer id moving must not write.
    fireEvent.pointerMove(area, { clientX: 0, clientY: 0, pointerId: 2 })
    expect(onChange.mock.calls.length).toBe(afterDown)
    // A different pointer id "up" must not end the drag — pointer 1 still writes after.
    fireEvent.pointerUp(area, { pointerId: 2 })
    fireEvent.pointerMove(area, { clientX: 200, clientY: 200, pointerId: 1 })
    expect(onChange).toHaveBeenLastCalledWith({ color: '#000000', alpha: 100 })
  })

  it('hue strip changes hue only; the area repaints with the new hue', () => {
    const onChange = vi.fn()
    const { rerender } = render(<HostColorLayerEditor {...base} color="#ff0000" onChange={onChange} />)
    fireEvent.input(screen.getByTestId('host-color-range-h'), { target: { value: '120' } })
    expect(onChange).toHaveBeenLastCalledWith({ color: '#00ff00', alpha: 100 })
    rerender(<HostColorLayerEditor {...base} color="#00ff00" onChange={onChange} />)
    const probe = document.createElement('div')
    probe.style.background = '#00ff00'
    expect(screen.getByTestId('host-color-area').style.background).toContain(probe.style.background)
  })

  it('keeps hue while the color is grey/black/white across drags', () => {
    const onChange = vi.fn()
    const { rerender } = render(<HostColorLayerEditor {...base} color="#000000" onChange={onChange} />)
    fireEvent.input(screen.getByTestId('host-color-range-h'), { target: { value: '240' } }) // still black
    rerender(<HostColorLayerEditor {...base} color="#000000" onChange={onChange} />)
    const area = screen.getByTestId('host-color-area')
    areaRect(area)
    pointAt(area, 200, 0) // s=100 v=100 with the remembered hue 240
    expect(onChange).toHaveBeenLastCalledWith({ color: '#0000ff', alpha: 100 })
  })

  it('arrow keys nudge s/v (Shift ×10)', () => {
    const onChange = vi.fn()
    render(<HostColorLayerEditor {...base} color="#ff0000" onChange={onChange} />)
    const area = screen.getByTestId('host-color-area')
    fireEvent.keyDown(area, { key: 'ArrowLeft' })
    expect(onChange).toHaveBeenLastCalledWith({ color: hsvToHex({ h: 0, s: 99, v: 100 }), alpha: 100 })
    fireEvent.keyDown(area, { key: 'ArrowDown', shiftKey: true })
    expect(onChange).toHaveBeenLastCalledWith({ color: hsvToHex({ h: 0, s: 99, v: 90 }), alpha: 100 })
  })

  it('exposes slider semantics with a live value text', () => {
    const onChange = vi.fn()
    render(<HostColorLayerEditor {...base} color="#ff0000" onChange={onChange} />)
    const area = screen.getByRole('slider', { name: /Saturation and brightness/ })
    expect(area).toHaveAttribute('aria-valuemin', '0')
    expect(area).toHaveAttribute('aria-valuemax', '100')
    expect(area).toHaveAttribute('aria-valuenow', '100')
    expect(area.getAttribute('aria-valuetext')).toContain('100%')
    fireEvent.keyDown(area, { key: 'ArrowLeft' })
    expect(area.getAttribute('aria-valuetext')).toContain('99%')
  })

  it('re-derives HSV when the parent hands in a different color', () => {
    const { rerender } = render(<HostColorLayerEditor {...base} color="#ff0000" onChange={() => {}} />)
    rerender(<HostColorLayerEditor {...base} color="#0000ff" onChange={() => {}} />)
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
    expect(screen.queryByTestId('host-color-area')).toBeNull()
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
