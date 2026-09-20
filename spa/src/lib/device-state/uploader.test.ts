import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { useTabStore } from '../../stores/useTabStore'
import { useWorkspaceStore } from '../../features/workspace/store'
import { useHostStore } from '../../stores/useHostStore'
import { __resetDefaultDeviceNameForTest, useDeviceNameStore } from '../../stores/useDeviceNameStore'
import { useDeviceStateStore } from '../../stores/useDeviceStateStore'
import { getClientId } from '../client-identity'
import type { Tab } from '../../types/tab'
import { putDeviceState } from './api'
import { resolveDefaultDeviceName } from '../device-name'
import { resolveAppVersion, startDeviceStateUploader } from './uploader'

vi.mock('./api', async (importOriginal) => ({
  ...(await importOriginal<typeof import('./api')>()),
  putDeviceState: vi.fn(),
}))

vi.mock('../device-name', async (importOriginal) => ({
  ...(await importOriginal<typeof import('../device-name')>()),
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
  __resetDefaultDeviceNameForTest()
  useDeviceNameStore.setState({ deviceName: null, defaultDeviceName: 'Browser' })
  useDeviceStateStore.setState({ status: { kind: 'idle' } })
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
    expect(useDeviceNameStore.getState().defaultDeviceName).toBe('Mac')
    expect(put).not.toHaveBeenCalled()
    await vi.advanceTimersByTimeAsync(5000)
    expect(put).toHaveBeenCalledTimes(1)
    const [hostId, clientId, body] = put.mock.calls[0]
    expect(hostId).toBe('h1')
    expect(clientId).toBe(getClientId())
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
    expect(useDeviceNameStore.getState().defaultDeviceName).toBe('字'.repeat(64))
    await vi.advanceTimersByTimeAsync(5000)
    expect(put.mock.calls[0][2].deviceName).toBe('字'.repeat(64))
  })

  it("stores 'Browser' when the resolved default name is blank", async () => {
    vi.mocked(resolveDefaultDeviceName).mockResolvedValue('   ')
    start()
    await vi.advanceTimersByTimeAsync(0)
    expect(useDeviceNameStore.getState().defaultDeviceName).toBe('Browser')
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
    useDeviceNameStore.getState().setDeviceName('Studio')
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

  describe('target endpoint changes (same devHostId)', () => {
    it('re-uploads when the target token changes', async () => {
      await startSettled()
      useHostStore.getState().updateHost('h1', { token: 'new-token' })
      await vi.advanceTimersByTimeAsync(5000)
      expect(put).toHaveBeenCalledTimes(1)
      expect(put.mock.calls[0][0]).toBe('h1')
    })

    it('re-uploads when the target ip changes', async () => {
      await startSettled()
      useHostStore.getState().updateHost('h1', { ip: '10.0.0.9' })
      await vi.advanceTimersByTimeAsync(5000)
      expect(put).toHaveBeenCalledTimes(1)
      expect(put.mock.calls[0][0]).toBe('h1')
    })

    it('re-uploads when the target port changes', async () => {
      await startSettled()
      useHostStore.getState().updateHost('h1', { port: 7861 })
      await vi.advanceTimersByTimeAsync(5000)
      expect(put).toHaveBeenCalledTimes(1)
    })

    it('ignores a token change on a non-target host', async () => {
      await startSettled()
      useHostStore.getState().updateHost('h2', { token: 'other' })
      await vi.advanceTimersByTimeAsync(10000)
      expect(put).not.toHaveBeenCalled()
    })

    it('retries after an auth error once the token is fixed', async () => {
      put.mockRejectedValueOnce(new Error('401'))
      start()
      await vi.advanceTimersByTimeAsync(5000)
      expect(useDeviceStateStore.getState().status.kind).toBe('error')
      useHostStore.getState().updateHost('h1', { token: 'fixed' })
      await vi.advanceTimersByTimeAsync(5000)
      expect(put).toHaveBeenCalledTimes(2)
      expect(useDeviceStateStore.getState().status).toEqual({ kind: 'ok', at: 1234, hostId: 'h1' })
    })
  })

  describe('target revalidation before and after the PUT', () => {
    function startWithPendingVersion() {
      let resolveVersion!: (v: string) => void
      const getAppVersion = vi
        .fn<() => Promise<string>>()
        .mockImplementationOnce(() => new Promise((r) => { resolveVersion = r }))
        .mockResolvedValue('1.0.0')
      stop = startDeviceStateUploader({ debounceMs: 5000, now: () => 1234, getAppVersion })
      return { resolve: (v: string) => resolveVersion(v) }
    }

    it('does not PUT to the old host when devHostId switches during the await', async () => {
      const version = startWithPendingVersion()
      await vi.advanceTimersByTimeAsync(5000)
      expect(put).not.toHaveBeenCalled()
      useHostStore.getState().setDevHost('h2')
      version.resolve('1.0.0')
      await vi.advanceTimersByTimeAsync(0)
      expect(put).not.toHaveBeenCalled()
      await vi.advanceTimersByTimeAsync(5000)
      expect(put).toHaveBeenCalledTimes(1)
      expect(put.mock.calls[0][0]).toBe('h2')
    })

    it('does not PUT when the target disconnects during the await', async () => {
      const version = startWithPendingVersion()
      await vi.advanceTimersByTimeAsync(5000)
      useHostStore.getState().setRuntime('h1', { status: 'disconnected' })
      version.resolve('1.0.0')
      await vi.advanceTimersByTimeAsync(10000)
      expect(put).not.toHaveBeenCalled()
      expect(useDeviceStateStore.getState().status).toEqual({ kind: 'offline', hostId: 'h1' })
    })

    it('does not PUT with a stale token captured before the await', async () => {
      const version = startWithPendingVersion()
      await vi.advanceTimersByTimeAsync(5000)
      useHostStore.getState().updateHost('h1', { token: 'rotated' })
      version.resolve('1.0.0')
      await vi.advanceTimersByTimeAsync(0)
      expect(put).not.toHaveBeenCalled()
      await vi.advanceTimersByTimeAsync(5000)
      expect(put).toHaveBeenCalledTimes(1)
      expect(put.mock.calls[0][0]).toBe('h1')
    })

    it('ignores an in-flight h1 result after devHostId switches to h2', async () => {
      let resolvePut!: (v: { stored: boolean }) => void
      put.mockImplementationOnce(() => new Promise((r) => { resolvePut = r }))
      start()
      await vi.advanceTimersByTimeAsync(5000)
      expect(put).toHaveBeenCalledTimes(1)
      useHostStore.getState().setDevHost('h2')
      resolvePut({ stored: true })
      await vi.advanceTimersByTimeAsync(0)
      expect(useDeviceStateStore.getState().status).not.toEqual({ kind: 'ok', at: 1234, hostId: 'h1' })
      await vi.advanceTimersByTimeAsync(5000)
      expect(put).toHaveBeenCalledTimes(2)
      expect(put.mock.calls[1][0]).toBe('h2')
      expect(useDeviceStateStore.getState().status).toEqual({ kind: 'ok', at: 1234, hostId: 'h2' })
      // h1 was never recorded → switching back must PUT to h1 again
      useHostStore.getState().setDevHost('h1')
      await vi.advanceTimersByTimeAsync(5000)
      expect(put).toHaveBeenCalledTimes(3)
      expect(put.mock.calls[2][0]).toBe('h1')
    })

    it('ignores an in-flight h1 error after devHostId switches to h2', async () => {
      let rejectPut!: (e: Error) => void
      put.mockImplementationOnce(() => new Promise((_, r) => { rejectPut = r }))
      start()
      await vi.advanceTimersByTimeAsync(5000)
      useHostStore.getState().setDevHost('h2')
      rejectPut(new Error('boom'))
      await vi.advanceTimersByTimeAsync(0)
      expect(useDeviceStateStore.getState().status.kind).not.toBe('error')
    })
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
