// The lifted editors (fleet plan T-2a.3) are controlled: they know no host, so they work on a plain draft. The two
// existing field test files (HostIconField / HostColorField) stay unchanged and prove the daemon-host wrappers behave
// as before; this file proves the bodies on their own.
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'

vi.mock('@tanstack/react-virtual', () => ({
  useVirtualizer: ({ count }: { count: number }) => ({
    getTotalSize: () => count * 38,
    getVirtualItems: () => Array.from({ length: count }, (_, i) => ({ index: i, key: i, start: i * 38, size: 38 })),
  }),
}))
vi.mock('../../features/workspace/lib/icon-path-cache', () => ({
  getIconPath: () => 'M0,0L10,10',
  isWeightLoaded: () => true,
  prefetchWeight: () => Promise.resolve(),
}))

import { HostIconPicker } from './HostIconPicker'
import { HostColorPicker } from './HostColorPicker'
import { useHostStore } from '../../stores/useHostStore'
import { useHostLookStore } from '../../stores/useHostLookStore'

beforeEach(() => {
  useHostLookStore.setState({ looks: {} })
  useHostStore.setState({ hosts: {}, hostOrder: [], runtime: {} })
})

describe('HostIconPicker', () => {
  it('reports the default button as onChange(null) and touches no host store', () => {
    const onChange = vi.fn()
    render(<HostIconPicker icon="Desktop" weight="bold" onChange={onChange} />)
    fireEvent.click(screen.getByTestId('host-icon-default'))
    expect(onChange).toHaveBeenCalledWith(null)
    expect(useHostLookStore.getState().looks).toEqual({})
  })
  it('the catalog\'s clear button is onChange(null), not an empty icon name', () => {
    const onChange = vi.fn()
    render(<HostIconPicker icon="Desktop" weight="bold" onChange={onChange} />)
    fireEvent.click(screen.getByTestId('host-icon-preview'))
    fireEvent.click(screen.getByTestId('clear-icon'))
    expect(onChange).toHaveBeenCalledWith(null)
    expect(onChange).toHaveBeenCalledTimes(1)
    expect(screen.getByTestId('host-icon-preview')).toHaveAttribute('aria-expanded', 'false')
  })
  it('picking an icon reports its name with the weight in force', () => {
    const onChange = vi.fn()
    render(<HostIconPicker icon={undefined} weight="bold" onChange={onChange} />)
    fireEvent.click(screen.getByTestId('host-icon-preview'))
    const first = screen.getAllByRole('button').find((b) => b.getAttribute('data-icon'))!
    fireEvent.click(first)
    expect(onChange).toHaveBeenCalledWith(first.getAttribute('data-icon'), 'bold')
  })
  it('an unknown weight is shown as regular', () => {
    const onChange = vi.fn()
    render(<HostIconPicker icon={undefined} weight="nope" onChange={onChange} />)
    fireEvent.click(screen.getByTestId('host-icon-preview'))
    const first = screen.getAllByRole('button').find((b) => b.getAttribute('data-icon'))!
    fireEvent.click(first)
    expect(onChange).toHaveBeenCalledWith(first.getAttribute('data-icon'), 'regular')
  })
})

describe('HostColorPicker', () => {
  it('with no colour, opening a layer asks for a preset main layer first', () => {
    const onSetLayer = vi.fn()
    render(<HostColorPicker colors={undefined} mode="console" onModeChange={vi.fn()} onSetLayer={onSetLayer} onClearMode={vi.fn()} />)
    fireEvent.click(screen.getByTestId('host-color-layer-main'))
    expect(onSetLayer).toHaveBeenCalledWith('console', 'main', expect.objectContaining({ color: expect.stringMatching(/^#/), alpha: 100 }))
  })
  it('a mode without its own set inherits console and materialises it from console on open', () => {
    const onSetLayer = vi.fn()
    const colors = { console: { main: { color: '#112233', alpha: 80 } } }
    render(<HostColorPicker colors={colors} mode="terminal" onModeChange={vi.fn()} onSetLayer={onSetLayer} onClearMode={vi.fn()} />)
    expect(screen.getByTestId('host-color-layer-main')).toHaveAttribute('data-inherits-console', 'true')
    fireEvent.click(screen.getByTestId('host-color-layer-main'))
    expect(onSetLayer).toHaveBeenCalledWith('terminal', 'main', { color: '#112233', alpha: 80 })
  })
  it('the clear button and the mode switch are reported, not applied', () => {
    const onClearMode = vi.fn()
    const onModeChange = vi.fn()
    render(<HostColorPicker colors={undefined} mode="console" onModeChange={onModeChange} onSetLayer={vi.fn()} onClearMode={onClearMode} />)
    fireEvent.click(screen.getByTestId('host-color-clear'))
    expect(onClearMode).toHaveBeenCalledWith('console')
    fireEvent.click(screen.getByText('Terminal'))
    expect(onModeChange).toHaveBeenCalledWith('terminal')
  })
})
