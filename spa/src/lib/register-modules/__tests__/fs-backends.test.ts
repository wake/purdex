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

// --- Removed host must NOT fall back to another machine ---------------------
//
// `getDaemonBase` answers a request for an unknown host with the ACTIVE host's
// address (a deliberate convenience for other consumers). Routed through the fs
// registry that convenience becomes the exact failure this PR exists to stop:
// after a host is deleted, an editor pane still bound to it would read — and
// WRITE — the same path on a different machine. The resolver therefore refuses
// to hand back any backend for a host the store no longer knows.
describe('registerBuiltinFsBackends — removed host is refused, never re-pointed', () => {
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

  function removeHostB(): void {
    useHostStore.setState({
      hosts: {
        hostA: { id: 'hostA', name: 'A', ip: '10.0.0.1', port: 7860, token: 'tokenA', order: 0 },
      },
      hostOrder: ['hostA'],
      activeHostId: 'hostA',
      runtime: {},
    })
  }

  it('resolves hostB while it still exists (regression guard for the case below)', async () => {
    const fetchMock = stubFetch()
    await getFsBackend({ type: 'daemon', hostId: 'hostB' })!.stat('/x')
    expect(urlOf(fetchMock)).toBe('http://10.0.0.2:7861/api/fs/stat')
  })

  it('returns undefined once hostB is removed — no backend at all', () => {
    getFsBackend({ type: 'daemon', hostId: 'hostB' }) // warm the per-host cache
    removeHostB()
    expect(getFsBackend({ type: 'daemon', hostId: 'hostB' })).toBeUndefined()
  })

  it('never hands back a backend that would write to the active host instead', async () => {
    getFsBackend({ type: 'daemon', hostId: 'hostB' }) // warm the per-host cache
    removeHostB()
    const fetchMock = stubFetch()

    const backend = getFsBackend({ type: 'daemon', hostId: 'hostB' })
    // The dangerous outcome is not "undefined" per se — it is any object whose
    // write lands on 10.0.0.1 (hostA). Prove no request is even possible.
    expect(backend).toBeUndefined()
    expect(fetchMock).not.toHaveBeenCalled()
  })

  it('keeps the active-host proxy reachable for the hostId-less probe', async () => {
    removeHostB()
    const fetchMock = stubFetch()
    await getFsBackend({ type: 'daemon', hostId: '' })!.stat('/x')
    expect(urlOf(fetchMock)).toBe('http://10.0.0.1:7860/api/fs/stat')
  })

  it('re-adding the same id yields a backend pointing at its NEW address (no stale cache hit)', async () => {
    const before = getFsBackend({ type: 'daemon', hostId: 'hostB' })
    expect(before).toBeDefined()
    removeHostB()
    // Same id, different machine — a cached instance must not survive as-is.
    useHostStore.setState({
      hosts: {
        hostA: { id: 'hostA', name: 'A', ip: '10.0.0.1', port: 7860, token: 'tokenA', order: 0 },
        hostB: { id: 'hostB', name: 'B2', ip: '10.0.0.9', port: 7999, token: 'tokenB2', order: 1 },
      },
      hostOrder: ['hostA', 'hostB'],
      activeHostId: 'hostA',
      runtime: {},
    })

    const fetchMock = stubFetch()
    const after = getFsBackend({ type: 'daemon', hostId: 'hostB' })
    expect(after).toBeDefined()
    await after!.read('/x')

    expect(urlOf(fetchMock)).toBe('http://10.0.0.9:7999/api/fs/read')
    expect(headersOf(fetchMock)).toMatchObject({ Authorization: 'Bearer tokenB2' })
    // Still stable per host afterwards (the ImagePreviewPane identity contract).
    expect(getFsBackend({ type: 'daemon', hostId: 'hostB' })).toBe(after)
  })
})
