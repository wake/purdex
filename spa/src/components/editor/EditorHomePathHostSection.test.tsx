import { describe, it, expect, beforeEach, vi } from 'vitest'
import { render, screen, cleanup, fireEvent, act } from '@testing-library/react'

vi.mock('../../lib/storage/sync', () => ({
  syncManager: { register: vi.fn(), notify: vi.fn(), destroy: vi.fn() },
  createSyncManager: vi.fn(),
}))

import { EditorHomePathHostSection } from './EditorHomePathHostSection'
import { useHostSettingsStore } from '../../stores/useHostSettingsStore'
import type { SettingsContextFor } from '../../lib/settings-contribution-types'

const ctx: SettingsContextFor<'host'> = { scope: 'host', hostId: 'h1', runtime: undefined }

beforeEach(() => {
  cleanup()
  localStorage.clear()
  useHostSettingsStore.setState({ hosts: {} })
})

describe('EditorHomePathHostSection', () => {
  it('renders placeholder when no value is stored', () => {
    render(<EditorHomePathHostSection ctx={ctx} />)
    const input = screen.getByLabelText(/home path/i) as HTMLInputElement
    expect(input.value).toBe('')
  })

  it('renders stored value', () => {
    useHostSettingsStore.getState().set('h1', 'editor', { homePath: '/home/y' })
    render(<EditorHomePathHostSection ctx={ctx} />)
    const input = screen.getByLabelText(/home path/i) as HTMLInputElement
    expect(input.value).toBe('/home/y')
  })

  it('writes to host store on change (commit on blur)', () => {
    render(<EditorHomePathHostSection ctx={ctx} />)
    const input = screen.getByLabelText(/home path/i) as HTMLInputElement
    fireEvent.change(input, { target: { value: '/home/foo' } })
    fireEvent.blur(input)
    expect(useHostSettingsStore.getState().get('h1', 'editor')).toEqual({ homePath: '/home/foo' })
  })

  it('Clear button removes stored homePath', () => {
    useHostSettingsStore.getState().set('h1', 'editor', { homePath: '/home/y' })
    render(<EditorHomePathHostSection ctx={ctx} />)
    fireEvent.click(screen.getByRole('button', { name: /clear|清除/i }))
    const after = useHostSettingsStore.getState().get('h1', 'editor')
    expect(after?.homePath).toBeUndefined()
  })

  it('isolates writes between hosts', () => {
    render(<EditorHomePathHostSection ctx={{ scope: 'host', hostId: 'h1', runtime: undefined }} />)
    const input = screen.getByLabelText(/home path/i) as HTMLInputElement
    fireEvent.change(input, { target: { value: '/home/foo' } })
    fireEvent.blur(input)
    expect(useHostSettingsStore.getState().get('h2', 'editor')).toBeUndefined()
  })

  it('renders null when ctx.scope is not host', () => {
    const wrongCtx = { scope: 'workspace', workspaceId: 'wsA' } as unknown as SettingsContextFor<'host'>
    const { container } = render(<EditorHomePathHostSection ctx={wrongCtx} />)
    expect(container.innerHTML).toBe('')
  })

  it('normalizes trailing whitespace back to the input on blur (R1 codex)', () => {
    useHostSettingsStore.getState().set('h1', 'editor', { homePath: '/home/y' })
    render(<EditorHomePathHostSection ctx={ctx} />)
    const input = screen.getByLabelText(/home path/i) as HTMLInputElement
    // User adds a trailing space — same trimmed value, still a no-op commit
    fireEvent.change(input, { target: { value: '/home/y ' } })
    fireEvent.blur(input)
    // Store is unchanged and the UI reflects the trimmed value
    expect(useHostSettingsStore.getState().get('h1', 'editor')).toEqual({ homePath: '/home/y' })
    expect(input.value).toBe('/home/y')
  })

  it('normalizes pure-whitespace input to empty on blur when nothing is stored', () => {
    render(<EditorHomePathHostSection ctx={ctx} />)
    const input = screen.getByLabelText(/home path/i) as HTMLInputElement
    fireEvent.change(input, { target: { value: '   ' } })
    fireEvent.blur(input)
    expect(input.value).toBe('')
    expect(useHostSettingsStore.getState().get('h1', 'editor')).toBeUndefined()
  })

  it('Clear only drops homePath, preserving sibling editor settings (R2 codex)', () => {
    useHostSettingsStore.getState().set('h1', 'editor', {
      homePath: '/home/y',
      wrap: true,
      tabSize: 4,
    })
    render(<EditorHomePathHostSection ctx={ctx} />)
    fireEvent.click(screen.getByRole('button', { name: /clear|清除/i }))
    expect(useHostSettingsStore.getState().get('h1', 'editor')).toEqual({
      wrap: true,
      tabSize: 4,
    })
  })

  it('Blur-to-empty only drops homePath, preserving sibling editor settings (R2 codex)', () => {
    useHostSettingsStore.getState().set('h1', 'editor', {
      homePath: '/home/y',
      wrap: true,
    })
    render(<EditorHomePathHostSection ctx={ctx} />)
    const input = screen.getByLabelText(/home path/i) as HTMLInputElement
    fireEvent.change(input, { target: { value: '' } })
    fireEvent.blur(input)
    expect(useHostSettingsStore.getState().get('h1', 'editor')).toEqual({ wrap: true })
  })

  it('does not clobber the draft while focused when store syncs externally (R2 codex)', () => {
    useHostSettingsStore.getState().set('h1', 'editor', { homePath: '/home/old' })
    render(<EditorHomePathHostSection ctx={ctx} />)
    const input = screen.getByLabelText(/home path/i) as HTMLInputElement
    fireEvent.focus(input)
    fireEvent.change(input, { target: { value: '/home/draft' } })
    // External write (simulates BroadcastChannel sync)
    act(() => {
      useHostSettingsStore.getState().set('h1', 'editor', { homePath: '/home/external' })
    })
    expect(input.value).toBe('/home/draft')
  })

  it('commit() reads the latest store value, not the render-time snapshot (R2 codex)', () => {
    useHostSettingsStore.getState().set('h1', 'editor', { homePath: '/home/old' })
    render(<EditorHomePathHostSection ctx={ctx} />)
    const input = screen.getByLabelText(/home path/i) as HTMLInputElement
    fireEvent.focus(input)
    fireEvent.change(input, { target: { value: '/home/new' } })
    // Another session writes the same value concurrently
    act(() => {
      useHostSettingsStore.getState().set('h1', 'editor', { homePath: '/home/new' })
    })
    fireEvent.blur(input)
    // commit detects the no-op against live store and leaves state alone
    expect(useHostSettingsStore.getState().get('h1', 'editor')).toEqual({ homePath: '/home/new' })
  })

  it('focus-without-edit + external sync + blur pulls in the new value (R3 codex)', () => {
    useHostSettingsStore.getState().set('h1', 'editor', { homePath: '/home/old' })
    render(<EditorHomePathHostSection ctx={ctx} />)
    const input = screen.getByLabelText(/home path/i) as HTMLInputElement
    fireEvent.focus(input)
    // External update during focus, no user typing
    act(() => {
      useHostSettingsStore.getState().set('h1', 'editor', { homePath: '/home/external' })
    })
    fireEvent.blur(input)
    // Store keeps the external value (no clobber)
    expect(useHostSettingsStore.getState().get('h1', 'editor')).toEqual({ homePath: '/home/external' })
    // Input also reflects the external value
    expect(input.value).toBe('/home/external')
  })
})
