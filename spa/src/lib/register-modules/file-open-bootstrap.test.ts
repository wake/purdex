import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import {
  __resetFileOpenBootstrap,
  resolveOpenContextCwdFromSessions,
  tryOpenFileForTerminalLink,
} from './file-open-bootstrap'
import type { FileInfo } from '../../types/fs'
import { useSessionStore } from '../../stores/useSessionStore'
import { useHostStore } from '../../stores/useHostStore'
import { useWorkspaceStore } from '../../features/workspace/store'
import { useEditorSettingsStore, DEFAULT_EDITOR_SETTINGS } from '../../stores/useEditorSettingsStore'
import { __disposeFileNotFoundPopupForTests } from '../file-open'

// We exercise the layer 2 expand wiring by spying on global fetch — same
// approach fs-search.test.ts uses: the session search builds a session-cwd
// root and returns matches newest first.

beforeEach(() => {
  __resetFileOpenBootstrap()
  __disposeFileNotFoundPopupForTests()
  useHostStore.setState({
    activeHostId: 'h1',
    hostOrder: ['h1'],
    getDaemonBase: () => 'http://daemon',
    getAuthHeaders: () => ({}),
    hosts: { h1: { id: 'h1', name: 'h1', ip: '127.0.0.1', port: 7860, order: 0 } },
  })
  useSessionStore.setState({
    sessions: { h1: [{ code: 'sess1', cwd: '/sess/cwd', name: 'a', mode: 'terminal', tabName: '' } as never] },
    activeHostId: 'h1',
    activeCode: 'sess1',
  })
  useWorkspaceStore.setState({
    workspaces: [
      {
        id: 'w1',
        name: 'w',
        tabs: [],
        activeTabId: null,
      },
    ],
    activeWorkspaceId: 'w1',
  })
  useEditorSettingsStore.setState({
    ...DEFAULT_EDITOR_SETTINGS,
    popupOnMissingFile: true,
    autoSearchLayer1: true,
  })
})

afterEach(() => {
  __disposeFileNotFoundPopupForTests()
  __resetFileOpenBootstrap()
})

describe('resolveOpenContextCwdFromSessions', () => {
  it('returns session.cwd for known sessionCode', () => {
    expect(resolveOpenContextCwdFromSessions('h1', 'sess1')).toBe('/sess/cwd')
  })

  it('returns null when sessionCode missing', () => {
    expect(resolveOpenContextCwdFromSessions('h1')).toBe(null)
  })

  it('returns null when host not in store', () => {
    expect(resolveOpenContextCwdFromSessions('hX', 'sess1')).toBe(null)
  })

  it('returns null when session-code not found on host', () => {
    expect(resolveOpenContextCwdFromSessions('h1', 'unknown')).toBe(null)
  })
})

describe('wrong-host guard', () => {
  afterEach(() => {
    vi.unstubAllGlobals()
  })

  it('a file whose host is gone never gets stat-ed on the active host', async () => {
    // The open pipeline builds its backend with `createDaemonBackendForHost`,
    // bypassing `getFsBackend`'s resolver guard. Without the guard inside that
    // helper, `getDaemonBase` answers an unknown host with the ACTIVE host's
    // base — so hostA would happily vouch for a path that belongs to hostX.
    const fetchSpy = vi.fn(
      async () =>
        new Response(JSON.stringify({ size: 1, mtime: 0, isFile: true, isDirectory: false }), {
          status: 200,
          headers: { 'Content-Type': 'application/json' },
        }),
    )
    vi.stubGlobal('fetch', fetchSpy)

    const file: FileInfo = {
      path: '/remote/a.md',
      name: 'a.md',
      extension: 'md',
      size: 0,
      isDirectory: false,
    }

    await expect(
      tryOpenFileForTerminalLink(
        file,
        { type: 'daemon', hostId: 'hX' },
        { hostId: 'hX', cwd: '/', sourceWorkspaceId: 'w1' },
      ),
    ).rejects.toThrow(/hX/)
    expect(fetchSpy).not.toHaveBeenCalled()
  })
})

describe('layer 2 expand search wiring', () => {
  it('layer 2 (session-cwd) succeeds with matches sorted by mtime', async () => {
    globalThis.fetch = vi.fn(async (_url: string, init: RequestInit) => {
      const body = JSON.parse(init.body as string) as { roots: { kind: string; sessionCode?: string }[] }
      expect(body.roots[0].kind).toBe('session-cwd')
      expect(body.roots[0].sessionCode).toBe('sess1')
      return new Response(
        JSON.stringify({
          matches: [
            { path: '/old/foo.go', modTime: '2026-04-25T00:00:00Z', sizeBytes: 1, root: '/' },
            { path: '/new/foo.go', modTime: '2026-04-27T00:00:00Z', sizeBytes: 1, root: '/' },
          ],
          partial: false,
        }),
        { status: 200, headers: { 'Content-Type': 'application/json' } },
      )
    }) as never

    const { fsSearchByCapability } = await import('../file-open')
    const matches = await fsSearchByCapability('h1', 'foo.go', [
      { kind: 'session-cwd', sessionCode: 'sess1' },
    ])
    expect(matches.map((m) => m.path)).toEqual(['/new/foo.go', '/old/foo.go'])
  })
})
