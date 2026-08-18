import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, cleanup } from '@testing-library/react'
import { PdfPreviewPane } from './PdfPreviewPane'
import type { Pane } from '../../types/tab'
import type { FileSource } from '../../types/fs'
import { getFsBackend } from '../../lib/fs-backend'
import { registerBuiltinFsBackends } from '../../lib/register-modules/fs-backends'
import { useHostStore } from '../../stores/useHostStore'
import type { PlatformCapabilities } from '../../lib/platform'

const read = vi.fn().mockResolvedValue(new Uint8Array([1, 2, 3]))
const backendInstance = { read }

// Keep the real module reachable so the host-binding test can drive the pane
// through the REAL registry.
const fsBackendActual = vi.hoisted(() => ({
  current: null as typeof import('../../lib/fs-backend') | null,
}))

vi.mock('../../lib/fs-backend', async (importOriginal) => {
  const actual = await importOriginal<typeof import('../../lib/fs-backend')>()
  fsBackendActual.current = actual
  return { ...actual, getFsBackend: vi.fn(() => backendInstance) }
})

const PDF_FS_CAPS: PlatformCapabilities = {
  isElectron: false,
  canTearOffTab: false,
  canMergeWindow: false,
  canBrowserPane: false,
  canSystemTray: false,
  canNotification: false,
  devUpdateEnabled: false,
  hasLocalFilesystem: false,
}

function makePane(filePath: string, source: FileSource = { type: 'inapp' }): Pane {
  return { id: 'p1', content: { kind: 'pdf-preview', source, filePath } }
}

beforeEach(() => {
  read.mockClear()
  vi.mocked(getFsBackend).mockReturnValue(backendInstance as unknown as ReturnType<typeof getFsBackend>)
  globalThis.URL.createObjectURL = vi.fn(() => 'blob:mock')
  globalThis.URL.revokeObjectURL = vi.fn()
})

afterEach(() => {
  cleanup()
})

describe('PdfPreviewPane', () => {
  it('fills its mount with an explicit height (h-full) so the iframe height chain works under a non-flex parent', async () => {
    // Same root cause as ImagePreviewPane: the per-tab mount wrapper in
    // TabContent is position:absolute (a plain block, not a flex container), so
    // a `flex-1` root collapses to content height and the iframe's `h-full` has
    // no definite height to resolve against. The pane must claim height via
    // `h-full w-full` like EditorPane. Guards against regressing to `flex-1`.
    render(<PdfPreviewPane pane={makePane('/doc.pdf')} isActive={true} />)
    const iframe = await screen.findByTitle('doc.pdf')
    const root = iframe.parentElement?.parentElement as HTMLElement
    expect(root.className).toContain('h-full')
    expect(root.className).not.toContain('flex-1')
  })

  // Real-consumer coverage for host-bound resolution (T1.1). The read effect is
  // keyed on the resolved backend object, so this also pins the resolver's
  // per-host instance stability: a fresh instance per call would re-fire the
  // effect on every render and re-download the PDF forever.
  it('reads a remote PDF from its own host, not the active one', async () => {
    const actual = fsBackendActual.current!
    actual.clearFsBackendRegistry()
    vi.mocked(getFsBackend).mockImplementation(actual.getFsBackend)
    useHostStore.setState({
      hosts: {
        hostA: { id: 'hostA', name: 'A', ip: '10.0.0.1', port: 7860, order: 0 },
        hostB: { id: 'hostB', name: 'B', ip: '10.0.0.2', port: 7861, order: 1 },
      },
      hostOrder: ['hostA', 'hostB'],
      activeHostId: 'hostA',
      runtime: {},
    })
    registerBuiltinFsBackends(PDF_FS_CAPS)

    const fetchMock = vi.fn(async (_url: string) => ({
      ok: true,
      arrayBuffer: async () => new Uint8Array([1, 2, 3]).buffer,
    }))
    vi.stubGlobal('fetch', fetchMock)

    try {
      render(
        <PdfPreviewPane
          pane={makePane('/remote.pdf', { type: 'daemon', hostId: 'hostB' })}
          isActive={true}
        />,
      )
      await screen.findByTitle('remote.pdf')

      expect(fetchMock).toHaveBeenCalledTimes(1)
      expect(fetchMock.mock.calls[0][0]).toBe('http://10.0.0.2:7861/api/fs/read')
    } finally {
      vi.unstubAllGlobals()
      actual.clearFsBackendRegistry()
      useHostStore.getState().reset()
    }
  })
})
