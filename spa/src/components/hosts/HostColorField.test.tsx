import { describe, it, expect, beforeEach } from 'vitest'
import { render, screen, fireEvent, act } from '@testing-library/react'
import { HostColorField } from './HostColorField'
import { useHostStore } from '../../stores/useHostStore'
import { useHostLookStore } from '../../stores/useHostLookStore'
import { hostLookOf } from '../../lib/host-look'
import { HOST_COLOR_PRESETS } from '../../lib/host-color'

const HOST_ID = 'h1'
// H2c-2: colours are written to the look store — read what the selector shows.
const host = () => hostLookOf(HOST_ID)
const layerBtn = (l: 'main' | 'middle' | 'light') => screen.getByTestId(`host-color-layer-${l}`)
const modeBtn = (name: string) => screen.getByRole('button', { name })
const BLUE = { color: '#3b82f6', alpha: 100 }

beforeEach(() => {
  useHostLookStore.setState({ looks: {} })
  useHostStore.setState({
    hosts: { [HOST_ID]: { id: HOST_ID, name: 'H', ip: '1.2.3.4', port: 7860, order: 0 } },
    hostOrder: [HOST_ID],
    runtime: {},
  })
})

describe('HostColorField — layout', () => {
  it('shows the mode switch on Console and three layer swatches, no editor', () => {
    render(<HostColorField hostId={HOST_ID} />)
    expect(screen.getByTestId('host-color-mode')).toBeInTheDocument()
    for (const l of ['main', 'middle', 'light'] as const) expect(layerBtn(l)).toHaveAttribute('aria-pressed', 'false')
    expect(screen.queryByTestId('host-color-editor')).toBeNull()
  })

  it('uncolored host: swatches read "No color"; clicking any layer first materialises main = first preset, then opens that layer', () => {
    render(<HostColorField hostId={HOST_ID} />)
    expect(layerBtn('main').textContent).toContain('No color')
    fireEvent.click(layerBtn('light'))
    expect(host().colors?.console).toEqual({ main: { color: HOST_COLOR_PRESETS[0], alpha: 100 } })
    expect(screen.getByTestId('host-color-editor')).toHaveAttribute('aria-label', 'Light')
    // Editing light now writes for real (the set exists).
    fireEvent.input(screen.getByTestId('host-color-range-a'), { target: { value: '30' } })
    expect(host().colors?.console?.light).toEqual({ alpha: 30 })
  })

  it('uncolored host: opening Main and clicking a preset replaces the materialised default', () => {
    render(<HostColorField hostId={HOST_ID} />)
    fireEvent.click(layerBtn('main'))
    fireEvent.click(screen.getByRole('button', { name: HOST_COLOR_PRESETS[3] }))
    expect(host().colors?.console?.main).toEqual({ color: HOST_COLOR_PRESETS[3], alpha: 100 })
  })

  it('legacy color shows as the console main swatch', () => {
    useHostStore.setState((s) => ({ hosts: { ...s.hosts, [HOST_ID]: { ...s.hosts[HOST_ID], color: '#22c55e' } } }))
    render(<HostColorField hostId={HOST_ID} />)
    expect(layerBtn('main').textContent).toContain('#22c55e')
    expect(layerBtn('middle').textContent).toContain('inherit')
    expect(layerBtn('middle').textContent).toContain('60%')
  })
})

describe('HostColorField — editing', () => {
  beforeEach(() => useHostStore.getState().setHostColorLayer(HOST_ID, 'console', 'main', BLUE))

  it('opening Main and dragging alpha writes console.main live', () => {
    render(<HostColorField hostId={HOST_ID} />)
    fireEvent.click(layerBtn('main'))
    fireEvent.input(screen.getByTestId('host-color-range-a'), { target: { value: '70' } })
    expect(host().colors?.console?.main).toEqual({ color: '#3b82f6', alpha: 70 })
  })

  it('Middle starts inherited; alpha drag writes { alpha } only; inherit off writes the color', () => {
    render(<HostColorField hostId={HOST_ID} />)
    fireEvent.click(layerBtn('middle'))
    expect(screen.getByTestId('host-color-inherit')).toHaveAttribute('aria-checked', 'true')
    fireEvent.input(screen.getByTestId('host-color-range-a'), { target: { value: '45' } })
    expect(host().colors?.console?.middle).toEqual({ alpha: 45 })
    fireEvent.click(screen.getByTestId('host-color-inherit'))
    expect(host().colors?.console?.middle).toEqual({ color: '#3b82f6', alpha: 45 })
    fireEvent.click(screen.getByTestId('host-color-inherit'))
    expect(host().colors?.console?.middle).toEqual({ alpha: 45 })
  })

  it('clicking the open layer again closes the editor; Done closes it too', () => {
    render(<HostColorField hostId={HOST_ID} />)
    fireEvent.click(layerBtn('light'))
    expect(layerBtn('light')).toHaveAttribute('aria-pressed', 'true')
    fireEvent.click(layerBtn('light'))
    expect(screen.queryByTestId('host-color-editor')).toBeNull()
    fireEvent.click(layerBtn('light'))
    fireEvent.click(screen.getByTestId('host-color-editor-close'))
    expect(screen.queryByTestId('host-color-editor')).toBeNull()
  })

  it('clear removes the console set and closes the editor', () => {
    render(<HostColorField hostId={HOST_ID} />)
    fireEvent.click(layerBtn('main'))
    fireEvent.click(screen.getByTestId('host-color-clear'))
    expect(host().colors).toBeUndefined()
    expect(screen.queryByTestId('host-color-editor')).toBeNull()
  })

  it('opens the editor in a floating dialog titled with the layer name; outside mousedown closes it; the swatch still toggles', () => {
    render(<div><HostColorField hostId={HOST_ID} /><button data-testid="outside">x</button></div>)
    fireEvent.click(layerBtn('main'))
    const dialog = screen.getByRole('dialog', { name: 'Main' })
    expect(dialog.parentElement).toBe(document.body)
    expect(dialog.contains(screen.getByTestId('host-color-editor'))).toBe(true)
    fireEvent.mouseDown(screen.getByTestId('outside'))
    expect(screen.queryByTestId('host-color-editor')).toBeNull()
    fireEvent.mouseDown(layerBtn('main'))
    fireEvent.click(layerBtn('main'))
    expect(screen.getByTestId('host-color-editor')).toBeInTheDocument()
    fireEvent.mouseDown(layerBtn('main'))
    fireEvent.click(layerBtn('main'))
    expect(screen.queryByTestId('host-color-editor')).toBeNull()
  })

  it('mousedown on another swatch does not close first and then fail to open: it switches layers', () => {
    render(<HostColorField hostId={HOST_ID} />)
    fireEvent.click(layerBtn('main'))
    fireEvent.mouseDown(layerBtn('light'))
    fireEvent.click(layerBtn('light'))
    expect(screen.getByRole('dialog', { name: 'Light' })).toBeInTheDocument()
  })
})

describe('HostColorField — remount / ghost state (PR #1160 R1 F1/F2)', () => {
  it('switching layers never carries editor state across', () => {
    useHostStore.getState().setHostColorLayer(HOST_ID, 'console', 'main', { color: '#808080', alpha: 100 })
    render(<HostColorField hostId={HOST_ID} />)
    fireEvent.click(layerBtn('main'))
    fireEvent.input(screen.getByTestId('host-color-range-h'), { target: { value: '120' } })
    fireEvent.click(layerBtn('middle'))
    fireEvent.click(screen.getByTestId('host-color-inherit'))
    expect((screen.getByTestId('host-color-range-h') as HTMLInputElement).value).toBe('0')
    const area = screen.getByTestId('host-color-area')
    area.getBoundingClientRect = () =>
      ({ left: 0, top: 0, width: 200, height: 200, right: 200, bottom: 200, x: 0, y: 0, toJSON() {} }) as DOMRect
    area.setPointerCapture = () => {}
    fireEvent.pointerDown(area, { clientX: 200, clientY: 0, pointerId: 1, button: 0 })
    expect(host().colors?.console?.middle?.color).not.toBe('#00ff00')
  })

  it('an unsaved hex draft does not leak into another layer', () => {
    useHostStore.getState().setHostColorLayer(HOST_ID, 'console', 'main', BLUE)
    render(<HostColorField hostId={HOST_ID} />)
    fireEvent.click(layerBtn('main'))
    fireEvent.change(screen.getByTestId('host-color-hex'), { target: { value: 'red' } })
    fireEvent.blur(screen.getByTestId('host-color-hex'))
    expect(screen.getByRole('alert')).toBeInTheDocument()
    fireEvent.click(layerBtn('light'))
    fireEvent.click(screen.getByTestId('host-color-inherit'))
    expect(screen.queryByRole('alert')).toBeNull()
    expect((screen.getByTestId('host-color-hex') as HTMLInputElement).value).toBe('#3b82f6')
  })

  it('a remote clear while the editor is open closes it', () => {
    useHostStore.getState().setHostColorLayer(HOST_ID, 'console', 'main', BLUE)
    render(<HostColorField hostId={HOST_ID} />)
    fireEvent.click(layerBtn('middle'))
    act(() => useHostStore.getState().clearHostColorMode(HOST_ID, 'console'))
    expect(screen.queryByTestId('host-color-editor')).toBeNull()
    expect(layerBtn('middle')).toHaveAttribute('aria-pressed', 'false')
    fireEvent.click(layerBtn('middle'))
    expect(host().colors?.console).toEqual({ main: { color: HOST_COLOR_PRESETS[0], alpha: 100 } })
    expect(screen.getByTestId('host-color-editor')).toHaveAttribute('aria-label', 'Middle')
  })

  it('unknown host: clicking a swatch does not leave a ghost open state', () => {
    render(<HostColorField hostId="nope" />)
    fireEvent.click(layerBtn('main'))
    expect(screen.queryByTestId('host-color-editor')).toBeNull()
    expect(layerBtn('main')).toHaveAttribute('aria-pressed', 'false')
  })

  it('color data returning after a remote clear does not reopen the editor', () => {
    useHostStore.getState().setHostColorLayer(HOST_ID, 'console', 'main', BLUE)
    render(<HostColorField hostId={HOST_ID} />)
    fireEvent.click(layerBtn('middle'))
    act(() => useHostStore.getState().clearHostColorMode(HOST_ID, 'console'))
    act(() => useHostStore.getState().setHostColorLayer(HOST_ID, 'console', 'main', BLUE))
    expect(screen.queryByTestId('host-color-editor')).toBeNull()
    for (const l of ['main', 'middle', 'light'] as const) expect(layerBtn(l)).toHaveAttribute('aria-pressed', 'false')
  })
})

describe('HostColorField — modes', () => {
  beforeEach(() => useHostStore.getState().setHostColorLayer(HOST_ID, 'console', 'main', BLUE))

  it('Terminal without its own set shows dimmed swatches that say it inherits Console', () => {
    render(<HostColorField hostId={HOST_ID} />)
    fireEvent.click(modeBtn('Terminal'))
    for (const l of ['main', 'middle', 'light'] as const) {
      expect(layerBtn(l)).toHaveAttribute('data-inherits-console', 'true')
      expect(layerBtn(l).textContent).toContain('Inherits Console')
    }
  })

  it('clicking a swatch on an inheriting mode copies console main into that mode and opens the editor', () => {
    render(<HostColorField hostId={HOST_ID} />)
    fireEvent.click(modeBtn('Terminal'))
    fireEvent.click(layerBtn('light'))
    expect(host().colors?.terminal).toEqual({ main: BLUE })
    expect(screen.getByTestId('host-color-editor')).toHaveAttribute('aria-label', 'Light')
    expect(layerBtn('main')).toHaveAttribute('data-inherits-console', 'false')
  })

  it('edits under Terminal never touch the console set', () => {
    render(<HostColorField hostId={HOST_ID} />)
    fireEvent.click(modeBtn('Terminal'))
    fireEvent.click(layerBtn('main'))
    fireEvent.click(screen.getByRole('button', { name: HOST_COLOR_PRESETS[0] }))
    expect(host().colors?.terminal?.main.color).toBe(HOST_COLOR_PRESETS[0])
    expect(host().colors?.console?.main).toEqual(BLUE)
  })

  it('switching mode closes any open editor', () => {
    render(<HostColorField hostId={HOST_ID} />)
    fireEvent.click(layerBtn('main'))
    fireEvent.click(modeBtn('Execution'))
    expect(screen.queryByTestId('host-color-editor')).toBeNull()
  })

  it('clear on Terminal removes only the terminal set', () => {
    useHostStore.getState().setHostColorLayer(HOST_ID, 'terminal', 'main', { color: '#ef4444', alpha: 100 })
    render(<HostColorField hostId={HOST_ID} />)
    fireEvent.click(modeBtn('Terminal'))
    fireEvent.click(screen.getByTestId('host-color-clear'))
    expect(host().colors).toEqual({ console: { main: BLUE } })
  })
})
