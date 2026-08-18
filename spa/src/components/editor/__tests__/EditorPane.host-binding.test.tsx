// EditorPane — host-bound backend resolution, driven through the REAL fs-backend
// registry so the assertions can prove which host each request lands on.
import { screen, waitFor } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import {
  renderEditorPane,
} from './editor-pane-harness'
import { fsBackendActual, getFsBackendMock } from './editor-pane-mocks'
import { bufferKey } from '../../../lib/editor-buffer-key'
import { registerBuiltinFsBackends } from '../../../lib/register-modules/fs-backends'
import { useEditorStore } from '../../../stores/useEditorStore'
import { useTabStore } from '../../../stores/useTabStore'
import { useRecentFilesStore } from '../../../stores/useRecentFilesStore'
import { useHostStore } from '../../../stores/useHostStore'
import type { Pane } from '../../../types/tab'
import type { PlatformCapabilities } from '../../../lib/platform'

vi.mock('../../../lib/fs-backend', async (importOriginal) => {
  const actual = await importOriginal<typeof import('../../../lib/fs-backend')>()
  const mocks = await import('./editor-pane-mocks')
  mocks.fsBackendActual.current = actual
  return { ...actual, getFsBackend: mocks.getFsBackendMock }
})
vi.mock('../MonacoWrapper', async () => ({
  MonacoWrapper: (await import('./editor-pane-mocks')).MonacoWrapperStub,
}))
vi.mock('../DiffView', async () => ({
  DiffView: (await import('./editor-pane-mocks')).DiffViewStub,
}))
vi.mock('../EditorStatusBar', async () => ({
  EditorStatusBar: (await import('./editor-pane-mocks')).EditorStatusBarStub,
}))
vi.mock('../TiptapEditor', async () => ({
  TiptapEditor: (await import('./editor-pane-mocks')).TiptapEditorStub,
}))

const HOST_BOUND_CAPS: PlatformCapabilities = {
  isElectron: false,
  canTearOffTab: false,
  canMergeWindow: false,
  canBrowserPane: false,
  canSystemTray: false,
  canNotification: false,
  devUpdateEnabled: false,
  hasLocalFilesystem: false,
}

describe('EditorPane — host-bound backend resolution', () => {
  const REMOTE_PATH = '/remote/notes.md'
  let fetchMock: ReturnType<typeof vi.fn>

  function remotePane(hostId: string): Pane {
    return {
      id: 'pane-remote',
      content: { kind: 'editor', source: { type: 'daemon', hostId }, filePath: REMOTE_PATH },
    }
  }

  beforeEach(() => {
    useEditorStore.getState().clearAllBuffers()
    useTabStore.setState({ tabs: {}, tabOrder: [], activeTabId: null, visitHistory: [] })
    useRecentFilesStore.setState({ files: [] })

    // Drive the component through the REAL registry — a mocked getFsBackend
    // could never prove which host the read actually lands on.
    const actual = fsBackendActual.current!
    actual.clearFsBackendRegistry()
    getFsBackendMock.mockImplementation(actual.getFsBackend)

    useHostStore.setState({
      hosts: {
        hostA: { id: 'hostA', name: 'A', ip: '10.0.0.1', port: 7860, token: 'tokenA', order: 0 },
        hostB: { id: 'hostB', name: 'B', ip: '10.0.0.2', port: 7861, token: 'tokenB', order: 1 },
      },
      hostOrder: ['hostA', 'hostB'],
      activeHostId: 'hostA',
      runtime: {},
    })
    registerBuiltinFsBackends(HOST_BOUND_CAPS)

    fetchMock = vi.fn(async (url: string) => {
      if (url.endsWith('/api/fs/read')) {
        return { ok: true, arrayBuffer: async () => new TextEncoder().encode('remote body').buffer }
      }
      return { ok: true, json: async () => ({ size: 11, mtime: 42, isDirectory: false, isFile: true }) }
    })
    vi.stubGlobal('fetch', fetchMock)
  })

  afterEach(() => {
    vi.unstubAllGlobals()
    getFsBackendMock.mockReset()
    fsBackendActual.current!.clearFsBackendRegistry()
    useHostStore.getState().reset()
    useEditorStore.getState().clearAllBuffers()
  })

  it('reads and stats a remote file on its own host while another host is active', async () => {
    renderEditorPane(remotePane('hostB'))

    await waitFor(() => {
      expect(
        useEditorStore.getState().buffers[bufferKey({ type: 'daemon', hostId: 'hostB' }, REMOTE_PATH)],
      ).toBeDefined()
    })

    const urls = fetchMock.mock.calls.map((c) => c[0] as string)
    expect(urls).toContain('http://10.0.0.2:7861/api/fs/read')
    expect(urls).toContain('http://10.0.0.2:7861/api/fs/stat')
    expect(urls.every((u) => u.startsWith('http://10.0.0.2:7861'))).toBe(true)
  })

  it('still resolves the active host for a pane bound to it', async () => {
    renderEditorPane(remotePane('hostA'))

    await waitFor(() => {
      expect(
        useEditorStore.getState().buffers[bufferKey({ type: 'daemon', hostId: 'hostA' }, REMOTE_PATH)],
      ).toBeDefined()
    })

    const urls = fetchMock.mock.calls.map((c) => c[0] as string)
    expect(urls.every((u) => u.startsWith('http://10.0.0.1:7860'))).toBe(true)
  })

  // The whole point of host-bound resolution is that a daemon file NEVER touches
  // another machine. Deleting the host used to re-point the pane at the active
  // one (via `getDaemonBase`'s fallback), so the pane would happily show — and
  // save over — hostA's copy of the same path. It must land in the T1.2b
  // no-backend error state instead.
  it('falls into the no-backend error state when the pane\'s host is removed, never reads the active host', async () => {
    const pane = remotePane('hostB')
    const { unmount } = renderEditorPane(pane)
    await waitFor(() => {
      expect(
        useEditorStore.getState().buffers[bufferKey({ type: 'daemon', hostId: 'hostB' }, REMOTE_PATH)],
      ).toBeDefined()
    })

    // Host removed while the pane is open; the buffer is dropped with it and the
    // pane re-loads from scratch (the real-world sequence is a reopen/remount).
    unmount()
    useEditorStore.getState().clearAllBuffers()
    useHostStore.setState({
      hosts: {
        hostA: { id: 'hostA', name: 'A', ip: '10.0.0.1', port: 7860, token: 'tokenA', order: 0 },
      },
      hostOrder: ['hostA'],
      activeHostId: 'hostA',
      runtime: {},
    })
    fetchMock.mockClear()

    renderEditorPane(pane)

    expect(await screen.findByTestId('editor-load-error')).toBeInTheDocument()
    expect(
      useEditorStore.getState().buffers[bufferKey({ type: 'daemon', hostId: 'hostB' }, REMOTE_PATH)],
    ).toBeUndefined()
    // The decisive assertion: not a single request went to hostA.
    expect(fetchMock).not.toHaveBeenCalled()
  })
})
