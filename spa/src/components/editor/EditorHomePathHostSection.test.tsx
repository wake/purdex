import { describe, it, expect, beforeEach, vi } from 'vitest'
import { render, screen, cleanup, fireEvent } from '@testing-library/react'

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
})
