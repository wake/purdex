import { describe, it, expect, beforeEach, vi } from 'vitest'

vi.mock('../lib/storage/sync', () => ({
  syncManager: { register: vi.fn(), notify: vi.fn(), destroy: vi.fn() },
  createSyncManager: vi.fn(),
}))

import { resolveEditorHomePath, type EditorHomeResolverDeps } from './editor-home-resolver'
import { useHostSettingsStore } from '../stores/useHostSettingsStore'
import { useWorkspaceSettingsStore } from '../stores/useWorkspaceSettingsStore'

function makeDeps(over?: Partial<EditorHomeResolverDeps>): EditorHomeResolverDeps {
  return {
    fetchPaneHome: vi.fn(async () => ''),
    ...over,
  }
}

beforeEach(() => {
  localStorage.clear()
  useHostSettingsStore.setState({ hosts: {} })
  useWorkspaceSettingsStore.setState({ workspaces: {} })
})

describe('resolveEditorHomePath', () => {
  it('prefers workspace value when workspaceId set and workspace has value', async () => {
    useWorkspaceSettingsStore.getState().set('wsA', 'editor', { homePath: '/Users/ws' })
    useHostSettingsStore.getState().set('h1', 'editor', { homePath: '/Users/host' })
    const deps = makeDeps({ fetchPaneHome: vi.fn(async () => '/Users/shell') })
    const out = await resolveEditorHomePath(
      { hostId: 'h1', sessionCode: 's1', workspaceId: 'wsA' },
      deps,
    )
    expect(out).toBe('/Users/ws')
    expect(deps.fetchPaneHome).not.toHaveBeenCalled()
  })

  it('falls through to host when workspace is empty', async () => {
    useHostSettingsStore.getState().set('h1', 'editor', { homePath: '/Users/host' })
    const deps = makeDeps({ fetchPaneHome: vi.fn(async () => '/Users/shell') })
    const out = await resolveEditorHomePath(
      { hostId: 'h1', sessionCode: 's1', workspaceId: 'wsA' },
      deps,
    )
    expect(out).toBe('/Users/host')
    expect(deps.fetchPaneHome).not.toHaveBeenCalled()
  })

  it('falls through to fetchPaneHome when workspace and host empty', async () => {
    const deps = makeDeps({ fetchPaneHome: vi.fn(async () => '/Users/shell') })
    const out = await resolveEditorHomePath(
      { hostId: 'h1', sessionCode: 's1', workspaceId: 'wsA' },
      deps,
    )
    expect(out).toBe('/Users/shell')
    expect(deps.fetchPaneHome).toHaveBeenCalledWith('h1', 's1', undefined)
  })

  it('returns null when all three layers yield nothing (fetchPaneHome returns empty)', async () => {
    const deps = makeDeps({ fetchPaneHome: vi.fn(async () => '') })
    const out = await resolveEditorHomePath(
      { hostId: 'h1', sessionCode: 's1', workspaceId: 'wsA' },
      deps,
    )
    expect(out).toBeNull()
  })

  it('propagates fetchPaneHome rejection so callers can warn with context', async () => {
    const deps = makeDeps({ fetchPaneHome: vi.fn(async () => { throw new Error('boom') }) })
    await expect(
      resolveEditorHomePath(
        { hostId: 'h1', sessionCode: 's1', workspaceId: 'wsA' },
        deps,
      ),
    ).rejects.toThrow('boom')
  })

  it('treats empty workspace string as unset and falls through to host', async () => {
    useWorkspaceSettingsStore.getState().set('wsA', 'editor', { homePath: '' })
    useHostSettingsStore.getState().set('h1', 'editor', { homePath: '/Users/host' })
    const out = await resolveEditorHomePath(
      { hostId: 'h1', sessionCode: 's1', workspaceId: 'wsA' },
      makeDeps(),
    )
    expect(out).toBe('/Users/host')
  })

  it('skips workspace layer when workspaceId is undefined (standalone pane)', async () => {
    useWorkspaceSettingsStore.getState().set('wsA', 'editor', { homePath: '/Users/ws' })
    useHostSettingsStore.getState().set('h1', 'editor', { homePath: '/Users/host' })
    const out = await resolveEditorHomePath(
      { hostId: 'h1', sessionCode: 's1', workspaceId: undefined },
      makeDeps(),
    )
    expect(out).toBe('/Users/host')
  })

  it('Finding 4 regression: wrong workspaceId reads that workspace (not the populated one)', async () => {
    useWorkspaceSettingsStore.getState().set('wsA', 'editor', { homePath: '/Users/ws-a' })
    useHostSettingsStore.getState().set('h1', 'editor', { homePath: '/Users/host' })
    const out = await resolveEditorHomePath(
      { hostId: 'h1', sessionCode: 's1', workspaceId: 'wsB' },
      makeDeps(),
    )
    expect(out).toBe('/Users/host')
  })

  it('honours AbortSignal: does not call fetchPaneHome when aborted', async () => {
    const controller = new AbortController()
    controller.abort()
    const deps = makeDeps({ fetchPaneHome: vi.fn(async () => '/Users/shell') })
    const out = await resolveEditorHomePath(
      { hostId: 'h1', sessionCode: 's1', workspaceId: 'wsA', signal: controller.signal },
      deps,
    )
    expect(out).toBeNull()
    expect(deps.fetchPaneHome).not.toHaveBeenCalled()
  })

  it('treats non-absolute workspace/host values as invalid (fallthrough)', async () => {
    useWorkspaceSettingsStore.getState().set('wsA', 'editor', { homePath: 'relative/ws' })
    useHostSettingsStore.getState().set('h1', 'editor', { homePath: 'relative/host' })
    const deps = makeDeps({ fetchPaneHome: vi.fn(async () => '/Users/shell') })
    const out = await resolveEditorHomePath(
      { hostId: 'h1', sessionCode: 's1', workspaceId: 'wsA' },
      deps,
    )
    expect(out).toBe('/Users/shell')
  })

  it('requires hostId: returns null without it', async () => {
    const out = await resolveEditorHomePath(
      { hostId: undefined, sessionCode: 's1', workspaceId: 'wsA' },
      makeDeps(),
    )
    expect(out).toBeNull()
  })

  it('requires sessionCode for fetchPaneHome: returns null if no host/workspace value and sessionCode missing', async () => {
    const deps = makeDeps({ fetchPaneHome: vi.fn(async () => '/Users/shell') })
    const out = await resolveEditorHomePath(
      { hostId: 'h1', sessionCode: undefined, workspaceId: 'wsA' },
      deps,
    )
    expect(out).toBeNull()
    expect(deps.fetchPaneHome).not.toHaveBeenCalled()
  })

  it('passes abort signal through to fetchPaneHome', async () => {
    const controller = new AbortController()
    const deps = makeDeps({ fetchPaneHome: vi.fn(async () => '/Users/shell') })
    await resolveEditorHomePath(
      { hostId: 'h1', sessionCode: 's1', workspaceId: 'wsA', signal: controller.signal },
      deps,
    )
    expect(deps.fetchPaneHome).toHaveBeenCalledWith('h1', 's1', controller.signal)
  })
})
