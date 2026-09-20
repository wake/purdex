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

  // === The native `storage` event (see sync.ts, WHY TWO SIGNALS) ===

  /** What another document's write looks like here. jsdom checks `storageArea`'s type, so it is put on a plain Event. */
  function storageEvent(key: string | null, newValue: string | null, storageArea: Storage = localStorage): Event {
    return Object.assign(new Event('storage'), { key, newValue, storageArea })
  }

  describe('the native storage event', () => {
    beforeEach(() => localStorage.clear())

    it('rehydrates the store registered for that key', async () => {
      const { createSyncManager } = await import('../sync')
      const manager = createSyncManager()
      const store = { persist: { rehydrate: vi.fn() } }
      manager.register('purdex-tabs', store)

      window.dispatchEvent(storageEvent('purdex-tabs', '{"n":1}'))

      expect(store.persist.rehydrate).toHaveBeenCalledOnce()
      manager.destroy()
    })

    it('ignores a key nobody registered, a cleared storage (`key: null`) and another storage area', async () => {
      const { createSyncManager } = await import('../sync')
      const manager = createSyncManager()
      const store = { persist: { rehydrate: vi.fn() } }
      manager.register('purdex-tabs', store)

      window.dispatchEvent(storageEvent('purdex-unknown', 'x'))
      window.dispatchEvent(storageEvent(null, null))
      window.dispatchEvent(storageEvent('purdex-tabs', 'x', sessionStorage))

      expect(store.persist.rehydrate).not.toHaveBeenCalled()
      manager.destroy()
    })

    it('one listener, installed by the first register and taken down by destroy', async () => {
      const add = vi.spyOn(window, 'addEventListener')
      const remove = vi.spyOn(window, 'removeEventListener')
      const { createSyncManager } = await import('../sync')
      const manager = createSyncManager()
      expect(add.mock.calls.filter(([type]) => type === 'storage')).toHaveLength(0) // creating a manager installs nothing
      const store = { persist: { rehydrate: vi.fn() } }
      manager.register('purdex-tabs', store)
      manager.register('purdex-hosts', { persist: { rehydrate: vi.fn() } })
      const added = add.mock.calls.filter(([type]) => type === 'storage')
      expect(added).toHaveLength(1)

      manager.destroy()

      expect(remove.mock.calls.filter(([type]) => type === 'storage').map(([, fn]) => fn)).toEqual([added[0][1]])
      window.dispatchEvent(storageEvent('purdex-tabs', 'x'))
      expect(store.persist.rehydrate).not.toHaveBeenCalled()
      add.mockRestore()
      remove.mockRestore()
    })

    it('works without BroadcastChannel, and without `window` it installs nothing and does not throw', async () => {
      vi.unstubAllGlobals()
      vi.stubGlobal('BroadcastChannel', undefined)
      const { createSyncManager } = await import('../sync')
      const manager = createSyncManager()
      const store = { persist: { rehydrate: vi.fn() } }
      manager.register('purdex-tabs', store)
      window.dispatchEvent(storageEvent('purdex-tabs', 'x'))
      expect(store.persist.rehydrate).toHaveBeenCalledOnce()
      manager.destroy()

      vi.stubGlobal('window', undefined)
      const headless = createSyncManager()
      expect(() => headless.register('purdex-tabs', store)).not.toThrow()
      expect(() => headless.destroy()).not.toThrow()
    })

    it('THE BROADCAST CAME FIRST AND READ THE OLD VALUE (another renderer process): the storage event, which carries the new one, rehydrates again', async () => {
      const { createSyncManager } = await import('../sync')
      const manager = createSyncManager()
      const store = { persist: { rehydrate: vi.fn() } }
      manager.register('purdex-tabs', store)
      localStorage.setItem('purdex-tabs', '{"n":4}') // what this process still sees when the message arrives

      instances[0].onmessage!({ data: { key: 'purdex-tabs' } } as MessageEvent)
      localStorage.setItem('purdex-tabs', '{"n":5}') // …and now the write is visible here
      window.dispatchEvent(storageEvent('purdex-tabs', '{"n":5}'))

      expect(store.persist.rehydrate).toHaveBeenCalledTimes(2)
      manager.destroy()
    })

    it('the broadcast came first and already read the NEW value: the storage event has nothing to add and is skipped', async () => {
      const { createSyncManager } = await import('../sync')
      const manager = createSyncManager()
      const store = { persist: { rehydrate: vi.fn() } }
      manager.register('purdex-tabs', store)
      localStorage.setItem('purdex-tabs', '{"n":5}')

      instances[0].onmessage!({ data: { key: 'purdex-tabs' } } as MessageEvent)
      window.dispatchEvent(storageEvent('purdex-tabs', '{"n":5}'))

      expect(store.persist.rehydrate).toHaveBeenCalledOnce()
      // …but only that once: a later write of another value is heard, and so is the same value after it.
      window.dispatchEvent(storageEvent('purdex-tabs', '{"n":6}'))
      window.dispatchEvent(storageEvent('purdex-tabs', '{"n":5}'))
      expect(store.persist.rehydrate).toHaveBeenCalledTimes(3)
      manager.destroy()
    })

    it('a LOCAL write in between forgets what was read: the same string written again elsewhere is news', async () => {
      const { createSyncManager } = await import('../sync')
      const manager = createSyncManager()
      const store = { persist: { rehydrate: vi.fn() } }
      manager.register('purdex-tabs', store)
      localStorage.setItem('purdex-tabs', '{"n":5}')
      instances[0].onmessage!({ data: { key: 'purdex-tabs' } } as MessageEvent)

      manager.notify('purdex-tabs') // this window wrote {"n":6}: memory is no longer {"n":5}
      window.dispatchEvent(storageEvent('purdex-tabs', '{"n":5}'))

      expect(store.persist.rehydrate).toHaveBeenCalledTimes(2)
      manager.destroy()
    })

    it('a removal (`newValue: null`) rehydrates', async () => {
      const { createSyncManager } = await import('../sync')
      const manager = createSyncManager()
      const store = { persist: { rehydrate: vi.fn() } }
      manager.register('purdex-tabs', store)
      localStorage.setItem('purdex-tabs', '{"n":5}')
      instances[0].onmessage!({ data: { key: 'purdex-tabs' } } as MessageEvent)
      window.dispatchEvent(storageEvent('purdex-tabs', null))
      expect(store.persist.rehydrate).toHaveBeenCalledTimes(2)
      manager.destroy()
    })
  })
})
