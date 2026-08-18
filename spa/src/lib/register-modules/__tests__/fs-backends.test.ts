import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { registerBuiltinFsBackends } from '../fs-backends'
import { getFsBackend, clearFsBackendRegistry } from '../../fs-backend'
import { useHostStore } from '../../../stores/useHostStore'
import type { PlatformCapabilities } from '../../platform'

const CAPS: PlatformCapabilities = {
  isElectron: false,
  canTearOffTab: false,
  canMergeWindow: false,
  canBrowserPane: false,
  canSystemTray: false,
  canNotification: false,
  devUpdateEnabled: false,
  hasLocalFilesystem: false,
}

type FetchMock = ReturnType<typeof vi.fn>

function stubFetch(): FetchMock {
  const fetchMock = vi.fn(async () => ({
    ok: true,
    arrayBuffer: async () => new Uint8Array([1, 2, 3]).buffer,
    json: async () => ({ path: '/', entries: [] }),
    text: async () => '',
  }))
  vi.stubGlobal('fetch', fetchMock)
  return fetchMock as unknown as FetchMock
}

function setHosts(activeHostId: string | null): void {
  useHostStore.setState({
    hosts: {
      hostA: { id: 'hostA', name: 'A', ip: '10.0.0.1', port: 7860, token: 'tokenA', order: 0 },
      hostB: { id: 'hostB', name: 'B', ip: '10.0.0.2', port: 7861, token: 'tokenB', order: 1 },
    },
    hostOrder: ['hostA', 'hostB'],
    activeHostId,
    runtime: {},
  })
}

function urlOf(fetchMock: FetchMock): string {
  return fetchMock.mock.calls[0][0] as string
}

function headersOf(fetchMock: FetchMock): Record<string, string> {
  return (fetchMock.mock.calls[0][1] as { headers: Record<string, string> }).headers
}

describe('registerBuiltinFsBackends — daemon host binding', () => {
  beforeEach(() => {
    clearFsBackendRegistry()
    setHosts('hostA')
    registerBuiltinFsBackends(CAPS)
  })

  afterEach(() => {
    vi.unstubAllGlobals()
    clearFsBackendRegistry()
    useHostStore.getState().reset()
  })

  it('resolves a daemon source against its own hostId, not the active host', async () => {
    const fetchMock = stubFetch()
    const backend = getFsBackend({ type: 'daemon', hostId: 'hostB' })
    expect(backend).toBeDefined()

    await backend!.read('/remote/file.md')

    expect(urlOf(fetchMock)).toBe('http://10.0.0.2:7861/api/fs/read')
    expect(headersOf(fetchMock)).toMatchObject({ Authorization: 'Bearer tokenB' })
  })

  it('writes to the source host even while another host is active', async () => {
    const fetchMock = stubFetch()
    const backend = getFsBackend({ type: 'daemon', hostId: 'hostB' })

    await backend!.write('/remote/file.md', new Uint8Array([104, 105]))

    expect(urlOf(fetchMock)).toBe('http://10.0.0.2:7861/api/fs/write')
  })

  it('keeps the empty-hostId probe working via the active-host proxy', async () => {
    const fetchMock = stubFetch()
    // fs-backends.tsx guards registration with getFsBackend({ hostId: '' }); it
    // must keep resolving, and it must stay the active-host proxy.
    const probe = getFsBackend({ type: 'daemon', hostId: '' })
    expect(probe).toBeDefined()

    await probe!.stat('/x')

    expect(urlOf(fetchMock)).toBe('http://10.0.0.1:7860/api/fs/stat')
  })

  it('active-host proxy follows the active host after a switch', async () => {
    useHostStore.setState({ activeHostId: 'hostB' })
    const fetchMock = stubFetch()

    await getFsBackend({ type: 'daemon', hostId: '' })!.stat('/x')

    expect(urlOf(fetchMock)).toBe('http://10.0.0.2:7861/api/fs/stat')
  })

  it('returns a stable backend instance per host', () => {
    // ImagePreviewPane compares the resolved backend object during render (and
    // PdfPreviewPane keys an effect on it). A fresh object per call turns those
    // into infinite loops, so the resolver must hand back one instance per host.
    const first = getFsBackend({ type: 'daemon', hostId: 'hostB' })
    const second = getFsBackend({ type: 'daemon', hostId: 'hostB' })
    expect(first).toBe(second)
    expect(getFsBackend({ type: 'daemon', hostId: 'hostA' })).not.toBe(first)
  })

  it('leaves the inapp backend on the flat registry', () => {
    const inapp = getFsBackend({ type: 'inapp' })
    expect(inapp?.id).toBe('inapp')
    expect(getFsBackend({ type: 'inapp' })).toBe(inapp)
  })
})
