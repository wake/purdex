import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { useTabStore } from '../../stores/useTabStore'
import { useWorkspaceStore } from '../../features/workspace/store'
import { useHostStore } from '../../stores/useHostStore'
import { useDeviceStateStore } from '../../stores/useDeviceStateStore'
import { useSyncStore } from '../sync/use-sync-store'
import type { Tab } from '../../types/tab'
import { putDeviceState } from './api'
import { resolveDefaultDeviceName } from './device-name'
import { resolveAppVersion, startDeviceStateUploader } from './uploader'

vi.mock('./api', async (importOriginal) => ({
  ...(await importOriginal<typeof import('./api')>()),
  putDeviceState: vi.fn(),
}))

vi.mock('./device-name', async (importOriginal) => ({
  ...(await importOriginal<typeof import('./device-name')>()),
  resolveDefaultDeviceName: vi.fn(),
}))

// Real webcrypto digest resolves off the microtask queue, which fake timers
// cannot flush deterministically; the structural key is an equivalent identity.
vi.mock('./payload', async (importOriginal) => {
  const actual = await importOriginal<typeof import('./payload')>()
  return { ...actual, hashPayload: vi.fn(async (snap) => actual.structuralKey(snap)) }
})

const put = vi.mocked(putDeviceState)

function host(id: string) {
  return { id, name: id, ip: '10.0.0.1', port: 7860, order: 0 }
}

function tab(id: string): Tab {
  return {
    id, pinned: false, locked: false, createdAt: 0,
    layout: { type: 'leaf', pane: { id: `p-${id}`, content: { kind: 'new-tab' } } },
  } as Tab
}

function addTab(id: string): void {
  const s = useTabStore.getState()
  useTabStore.setState({ tabs: { ...s.tabs, [id]: tab(id) }, tabOrder: [...s.tabOrder, id] })
}

let stop: (() => void) | null = null

function start() {
  stop = startDeviceStateUploader({ debounceMs: 5000, now: () => 1234, getAppVersion: async () => '1.0.0' })
}

/** Start and let the initial tick upload, then clear the PUT mock. */
async function startSettled() {
  start()
  await vi.advanceTimersByTimeAsync(5000)
  expect(put).toHaveBeenCalledTimes(1)
  put.mockClear()
}

beforeEach(() => {
  vi.useFakeTimers()
  put.mockReset()
  put.mockResolvedValue({ stored: true })
  vi.mocked(resolveDefaultDeviceName).mockReset()
  vi.mocked(resolveDefaultDeviceName).mockResolvedValue('Mac')
  useTabStore.setState({ tabs: {}, tabOrder: [], activeTabId: null, visitHistory: [] })
  useWorkspaceStore.getState().reset()
  useHostStore.setState({
    hosts: { h1: host('h1'), h2: host('h2') },
    hostOrder: ['h1', 'h2'],
    devHostId: 'h1',
    runtime: { h1: { status: 'connected' }, h2: { status: 'connected' } },
  })
  useDeviceStateStore.setState({ deviceName: null, defaultDeviceName: 'Browser', status: { kind: 'idle' } })
})

afterEach(() => {
  stop?.()
  stop = null
  vi.useRealTimers()
})

describe('startDeviceStateUploader', () => {
  it('resolves the default device name once and uploads on the initial tick', async () => {
    start()
    await vi.advanceTimersByTimeAsync(0)
    expect(resolveDefaultDeviceName).toHaveBeenCalledTimes(1)
    expect(useDeviceStateStore.getState().defaultDeviceName).toBe('Mac')
    expect(put).not.toHaveBeenCalled()
    await vi.advanceTimersByTimeAsync(5000)
    expect(put).toHaveBeenCalledTimes(1)
    const [hostId, clientId, body] = put.mock.calls[0]
    expect(hostId).toBe('h1')
    expect(clientId).toBe(useSyncStore.getState().getClientId())
    expect(body.deviceName).toBe('Mac')
    expect(body.appVersion).toBe('1.0.0')
    expect(body.capturedAt).toBe(1234)
    expect(body.payload.version).toBe(1)
    expect(useDeviceStateStore.getState().status).toEqual({ kind: 'ok', at: 1234, hostId: 'h1' })
  })

  it('stores a normalized default device name', async () => {
    vi.mocked(resolveDefaultDeviceName).mockResolvedValue(`  ${'字'.repeat(100)}  `)
    start()
    await vi.advanceTimersByTimeAsync(0)
    expect(useDeviceStateStore.getState().defaultDeviceName).toBe('字'.repeat(64))
    await vi.advanceTimersByTimeAsync(5000)
    expect(put.mock.calls[0][2].deviceName).toBe('字'.repeat(64))
  })

  it("stores 'Browser' when the resolved default name is blank", async () => {
    vi.mocked(resolveDefaultDeviceName).mockResolvedValue('   ')
    start()
    await vi.advanceTimersByTimeAsync(0)
    expect(useDeviceStateStore.getState().defaultDeviceName).toBe('Browser')
  })

  it('collapses a burst of changes into one PUT after the debounce', async () => {
    await startSettled()
    addTab('a')
    await vi.advanceTimersByTimeAsync(2000)
    addTab('b')
    await vi.advanceTimersByTimeAsync(2000)
    useTabStore.setState({ activeTabId: 'a' })
    await vi.advanceTimersByTimeAsync(4999)
    expect(put).not.toHaveBeenCalled()
    await vi.advanceTimersByTimeAsync(1)
    expect(put).toHaveBeenCalledTimes(1)
    expect(put.mock.calls[0][2].payload.tabOrder).toEqual(['a', 'b'])
  })

  it('ignores visitHistory-only changes', async () => {
    await startSettled()
    const spy = vi.spyOn(globalThis, 'setTimeout')
    useTabStore.setState({ visitHistory: ['x'] })
    expect(spy).not.toHaveBeenCalled()
    spy.mockRestore()
    await vi.advanceTimersByTimeAsync(10000)
    expect(put).not.toHaveBeenCalled()
  })

  it('reacts to workspace changes', async () => {
    await startSettled()
    useWorkspaceStore.setState({ activeWorkspaceId: 'w-new' })
    await vi.advanceTimersByTimeAsync(5000)
    expect(put).toHaveBeenCalledTimes(1)
  })

  it('reports no-target when no dev host is selected', async () => {
    useHostStore.setState({ devHostId: null })
    start()
    await vi.advanceTimersByTimeAsync(5000)
    expect(put).not.toHaveBeenCalled()
    expect(useDeviceStateStore.getState().status).toEqual({ kind: 'no-target' })
  })

  it('reports offline, then uploads once the target connects', async () => {
    useHostStore.setState({ runtime: { h1: { status: 'disconnected' } } })
    start()
    await vi.advanceTimersByTimeAsync(5000)
    expect(put).not.toHaveBeenCalled()
    expect(useDeviceStateStore.getState().status).toEqual({ kind: 'offline', hostId: 'h1' })
    useHostStore.getState().setRuntime('h1', { status: 'connected' })
    await vi.advanceTimersByTimeAsync(5000)
    expect(put).toHaveBeenCalledTimes(1)
    expect(put.mock.calls[0][0]).toBe('h1')
  })

  it('skips the request when the payload and name are unchanged', async () => {
    await startSettled()
    // new tabs reference, identical content
    useTabStore.setState({ tabs: { ...useTabStore.getState().tabs } })
    await vi.advanceTimersByTimeAsync(5000)
    expect(put).not.toHaveBeenCalled()
    expect(useDeviceStateStore.getState().status.kind).toBe('ok')
  })

  it('uploads a rename even when the payload hash is identical', async () => {
    start()
    await vi.advanceTimersByTimeAsync(5000)
    const first = put.mock.calls[0][2]
    put.mockClear()
    useDeviceStateStore.getState().setDeviceName('Studio')
    await vi.advanceTimersByTimeAsync(5000)
    expect(put).toHaveBeenCalledTimes(1)
    const second = put.mock.calls[0][2]
    expect(second.deviceName).toBe('Studio')
    expect(second.payload).toEqual(first.payload)
  })

  it('sets error status on failure and retries on the next change', async () => {
    put.mockRejectedValueOnce(new Error('boom'))
    start()
    await vi.advanceTimersByTimeAsync(5000)
    expect(put).toHaveBeenCalledTimes(1)
    expect(useDeviceStateStore.getState().status).toEqual({ kind: 'error', hostId: 'h1', message: 'boom' })
    // same content but not recorded → a new tick must PUT again
    useTabStore.setState({ tabs: { ...useTabStore.getState().tabs } })
    await vi.advanceTimersByTimeAsync(5000)
    expect(put).toHaveBeenCalledTimes(2)
    expect(useDeviceStateStore.getState().status.kind).toBe('ok')
  })

  it('sets uploading while the request is in flight', async () => {
    let resolve!: (v: { stored: boolean }) => void
    put.mockImplementationOnce(() => new Promise((r) => { resolve = r }))
    start()
    await vi.advanceTimersByTimeAsync(5000)
    expect(useDeviceStateStore.getState().status).toEqual({ kind: 'uploading', hostId: 'h1' })
    resolve({ stored: true })
    await vi.advanceTimersByTimeAsync(0)
    expect(useDeviceStateStore.getState().status.kind).toBe('ok')
  })

  it('runs exactly one extra tick when changes land during an in-flight PUT', async () => {
    await startSettled()
    let resolve!: (v: { stored: boolean }) => void
    put.mockImplementationOnce(() => new Promise((r) => { resolve = r }))
    addTab('a')
    await vi.advanceTimersByTimeAsync(5000)
    expect(put).toHaveBeenCalledTimes(1)
    addTab('b')
    await vi.advanceTimersByTimeAsync(5000)
    addTab('c')
    await vi.advanceTimersByTimeAsync(5000)
    expect(put).toHaveBeenCalledTimes(1)
    resolve({ stored: true })
    await vi.advanceTimersByTimeAsync(0)
    expect(put).toHaveBeenCalledTimes(2)
    expect(put.mock.calls[1][2].payload.tabOrder).toEqual(['a', 'b', 'c'])
    await vi.advanceTimersByTimeAsync(20000)
    expect(put).toHaveBeenCalledTimes(2)
  })

  it('uploads to the new host when devHostId switches', async () => {
    await startSettled()
    useHostStore.getState().setDevHost('h2')
    await vi.advanceTimersByTimeAsync(5000)
    expect(put).toHaveBeenCalledTimes(1)
    expect(put.mock.calls[0][0]).toBe('h2')
  })

  it('stop() prevents further PUTs and ignores an in-flight result', async () => {
    let resolve!: (v: { stored: boolean }) => void
    put.mockImplementationOnce(() => new Promise((r) => { resolve = r }))
    start()
    await vi.advanceTimersByTimeAsync(5000)
    expect(put).toHaveBeenCalledTimes(1)
    addTab('a')
    stop!()
    stop = null
    resolve({ stored: true })
    await vi.advanceTimersByTimeAsync(20000)
    expect(put).toHaveBeenCalledTimes(1)
    expect(useDeviceStateStore.getState().status.kind).toBe('uploading')
  })
})

describe('resolveAppVersion', () => {
  afterEach(() => {
    delete (window as unknown as { electronAPI?: unknown }).electronAPI
  })

  it('returns the Electron app version', async () => {
    ;(window as unknown as { electronAPI: unknown }).electronAPI = {
      getAppInfo: vi.fn().mockResolvedValue({ version: '0.9.1' }),
    }
    await expect(resolveAppVersion()).resolves.toBe('0.9.1')
  })

  it('returns empty string without electronAPI', async () => {
    await expect(resolveAppVersion()).resolves.toBe('')
  })

  it('returns empty string when getAppInfo throws', async () => {
    ;(window as unknown as { electronAPI: unknown }).electronAPI = {
      getAppInfo: vi.fn().mockRejectedValue(new Error('x')),
    }
    await expect(resolveAppVersion()).resolves.toBe('')
  })
})
