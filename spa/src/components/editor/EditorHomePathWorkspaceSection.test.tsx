import { describe, it, expect, beforeEach, vi } from 'vitest'
import { render, screen, cleanup, fireEvent, act } from '@testing-library/react'

vi.mock('../../lib/storage/sync', () => ({
  syncManager: { register: vi.fn(), notify: vi.fn(), destroy: vi.fn() },
  createSyncManager: vi.fn(),
}))

import { EditorHomePathWorkspaceSection } from './EditorHomePathWorkspaceSection'
import { useWorkspaceSettingsStore } from '../../stores/useWorkspaceSettingsStore'
import type { SettingsContextFor } from '../../lib/settings-contribution-types'

const ctx: SettingsContextFor<'workspace'> = { scope: 'workspace', workspaceId: 'wsA' }

beforeEach(() => {
  cleanup()
  localStorage.clear()
  useWorkspaceSettingsStore.setState({ workspaces: {} })
})

describe('EditorHomePathWorkspaceSection', () => {
  it('renders placeholder when no value is stored', () => {
    render(<EditorHomePathWorkspaceSection ctx={ctx} />)
    const input = screen.getByLabelText(/home path/i) as HTMLInputElement
    expect(input.value).toBe('')
    expect(input.placeholder.length).toBeGreaterThan(0)
  })

  it('renders stored value', () => {
    useWorkspaceSettingsStore.getState().set('wsA', 'editor', { homePath: '/Users/x' })
    render(<EditorHomePathWorkspaceSection ctx={ctx} />)
    const input = screen.getByLabelText(/home path/i) as HTMLInputElement
    expect(input.value).toBe('/Users/x')
  })

  it('writes to workspace store on change (commit on blur)', () => {
    render(<EditorHomePathWorkspaceSection ctx={ctx} />)
    const input = screen.getByLabelText(/home path/i) as HTMLInputElement
    fireEvent.change(input, { target: { value: '/Users/foo' } })
    fireEvent.blur(input)
    expect(useWorkspaceSettingsStore.getState().get('wsA', 'editor')).toEqual({ homePath: '/Users/foo' })
  })

  it('Clear button removes stored homePath', () => {
    useWorkspaceSettingsStore.getState().set('wsA', 'editor', { homePath: '/Users/x' })
    render(<EditorHomePathWorkspaceSection ctx={ctx} />)
    fireEvent.click(screen.getByRole('button', { name: /clear|清除/i }))
    const after = useWorkspaceSettingsStore.getState().get('wsA', 'editor')
    expect(after?.homePath).toBeUndefined()
  })

  it('isolates writes between workspaces', () => {
    render(<EditorHomePathWorkspaceSection ctx={{ scope: 'workspace', workspaceId: 'wsA' }} />)
    const input = screen.getByLabelText(/home path/i) as HTMLInputElement
    fireEvent.change(input, { target: { value: '/Users/foo' } })
    fireEvent.blur(input)
    expect(useWorkspaceSettingsStore.getState().get('wsB', 'editor')).toBeUndefined()
  })

  it('renders null when ctx.scope is not workspace', () => {
    const wrongCtx = { scope: 'host', hostId: 'h1', runtime: undefined } as unknown as SettingsContextFor<'workspace'>
    const { container } = render(<EditorHomePathWorkspaceSection ctx={wrongCtx} />)
    expect(container.innerHTML).toBe('')
  })

  it('normalizes trailing whitespace back to the input on blur (R1 codex)', () => {
    useWorkspaceSettingsStore.getState().set('wsA', 'editor', { homePath: '/Users/x' })
    render(<EditorHomePathWorkspaceSection ctx={ctx} />)
    const input = screen.getByLabelText(/home path/i) as HTMLInputElement
    fireEvent.change(input, { target: { value: '/Users/x ' } })
    fireEvent.blur(input)
    expect(useWorkspaceSettingsStore.getState().get('wsA', 'editor')).toEqual({ homePath: '/Users/x' })
    expect(input.value).toBe('/Users/x')
  })

  it('normalizes pure-whitespace input to empty on blur when nothing is stored', () => {
    render(<EditorHomePathWorkspaceSection ctx={ctx} />)
    const input = screen.getByLabelText(/home path/i) as HTMLInputElement
    fireEvent.change(input, { target: { value: '   ' } })
    fireEvent.blur(input)
    expect(input.value).toBe('')
    expect(useWorkspaceSettingsStore.getState().get('wsA', 'editor')).toBeUndefined()
  })

  it('Clear only drops homePath, preserving sibling editor settings (R2 codex)', () => {
    useWorkspaceSettingsStore.getState().set('wsA', 'editor', {
      homePath: '/Users/x',
      wrap: true,
      tabSize: 4,
    })
    render(<EditorHomePathWorkspaceSection ctx={ctx} />)
    fireEvent.click(screen.getByRole('button', { name: /clear|清除/i }))
    expect(useWorkspaceSettingsStore.getState().get('wsA', 'editor')).toEqual({
      wrap: true,
      tabSize: 4,
    })
  })

  it('Blur-to-empty only drops homePath, preserving sibling editor settings (R2 codex)', () => {
    useWorkspaceSettingsStore.getState().set('wsA', 'editor', {
      homePath: '/Users/x',
      wrap: true,
    })
    render(<EditorHomePathWorkspaceSection ctx={ctx} />)
    const input = screen.getByLabelText(/home path/i) as HTMLInputElement
    fireEvent.change(input, { target: { value: '' } })
    fireEvent.blur(input)
    expect(useWorkspaceSettingsStore.getState().get('wsA', 'editor')).toEqual({ wrap: true })
  })

  it('does not clobber the draft while focused when store syncs externally (R2 codex)', () => {
    useWorkspaceSettingsStore.getState().set('wsA', 'editor', { homePath: '/Users/old' })
    render(<EditorHomePathWorkspaceSection ctx={ctx} />)
    const input = screen.getByLabelText(/home path/i) as HTMLInputElement
    fireEvent.focus(input)
    fireEvent.change(input, { target: { value: '/Users/draft' } })
    act(() => {
      useWorkspaceSettingsStore.getState().set('wsA', 'editor', { homePath: '/Users/external' })
    })
    expect(input.value).toBe('/Users/draft')
  })

  it('commit() reads the latest store value, not the render-time snapshot (R2 codex)', () => {
    useWorkspaceSettingsStore.getState().set('wsA', 'editor', { homePath: '/Users/old' })
    render(<EditorHomePathWorkspaceSection ctx={ctx} />)
    const input = screen.getByLabelText(/home path/i) as HTMLInputElement
    fireEvent.focus(input)
    fireEvent.change(input, { target: { value: '/Users/new' } })
    act(() => {
      useWorkspaceSettingsStore.getState().set('wsA', 'editor', { homePath: '/Users/new' })
    })
    fireEvent.blur(input)
    expect(useWorkspaceSettingsStore.getState().get('wsA', 'editor')).toEqual({ homePath: '/Users/new' })
  })
})
