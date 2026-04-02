import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'

let instances: Array<{
  name: string
  onmessage: ((event: MessageEvent) => void) | null
  postMessage: ReturnType<typeof vi.fn>
  close: ReturnType<typeof vi.fn>
}>

class MockBroadcastChannel {
  name: string
  onmessage: ((event: MessageEvent) => void) | null = null
  postMessage = vi.fn()
  close = vi.fn()
  constructor(name: string) {
    this.name = name
    instances.push(this)
  }
}

beforeEach(() => {
  instances = []
  vi.stubGlobal('BroadcastChannel', MockBroadcastChannel)
})

afterEach(() => {
  vi.unstubAllGlobals()
  vi.resetModules()
})

describe('createSyncManager', () => {
  it('register creates BroadcastChannel with correct name', async () => {
    const { createSyncManager } = await import('../sync')
    const manager = createSyncManager()
    const store = { persist: { rehydrate: vi.fn() } }

    manager.register('purdex-tabs', store)

    expect(instances).toHaveLength(1)
    expect(instances[0].name).toBe('purdex-sync')
    manager.destroy()
  })

  it('notify posts key to channel', async () => {
    const { createSyncManager } = await import('../sync')
    const manager = createSyncManager()
    const store = { persist: { rehydrate: vi.fn() } }
    manager.register('purdex-tabs', store)

    manager.notify('purdex-tabs')

    expect(instances[0].postMessage).toHaveBeenCalledWith({ key: 'purdex-tabs' })
    manager.destroy()
  })

  it('incoming message triggers rehydrate on matching store', async () => {
    const { createSyncManager } = await import('../sync')
    const manager = createSyncManager()
    const store = { persist: { rehydrate: vi.fn() } }
    manager.register('purdex-tabs', store)

    instances[0].onmessage!({ data: { key: 'purdex-tabs' } } as MessageEvent)

    expect(store.persist.rehydrate).toHaveBeenCalledOnce()
    manager.destroy()
  })

  it('incoming message for unregistered key does nothing', async () => {
    const { createSyncManager } = await import('../sync')
    const manager = createSyncManager()
    const store = { persist: { rehydrate: vi.fn() } }
    manager.register('purdex-tabs', store)

    instances[0].onmessage!({ data: { key: 'purdex-unknown' } } as MessageEvent)

    expect(store.persist.rehydrate).not.toHaveBeenCalled()
    manager.destroy()
  })

  it('gracefully handles missing BroadcastChannel', async () => {
    vi.unstubAllGlobals()
    vi.stubGlobal('BroadcastChannel', undefined)

    const { createSyncManager } = await import('../sync')
    const manager = createSyncManager()
    const store = { persist: { rehydrate: vi.fn() } }

    manager.register('purdex-tabs', store)
    manager.notify('purdex-tabs')
    manager.destroy()
  })

  it('destroy closes channel and clears registry', async () => {
    const { createSyncManager } = await import('../sync')
    const manager = createSyncManager()
    const store = { persist: { rehydrate: vi.fn() } }
    manager.register('purdex-tabs', store)

    manager.destroy()

    expect(instances[0].close).toHaveBeenCalledOnce()
  })

  it('incoming message with invalid data does not throw', async () => {
    const { createSyncManager } = await import('../sync')
    const manager = createSyncManager()
    const store = { persist: { rehydrate: vi.fn() } }
    manager.register('purdex-tabs', store)

    // Should not throw
    instances[0].onmessage!({ data: null } as MessageEvent)
    instances[0].onmessage!({ data: {} } as MessageEvent)
    instances[0].onmessage!({ data: 'garbage' } as MessageEvent)

    expect(store.persist.rehydrate).not.toHaveBeenCalled()
    manager.destroy()
  })
})
