import { describe, it, expect, beforeEach, vi } from 'vitest'
import { InAppBackend } from './fs-backend-inapp'
import { closeAllIDB } from './storage/idb'
import { supportsMutationEvents } from './fs-backend'

const enc = (s: string) => new TextEncoder().encode(s)

function deleteInappDB(): Promise<void> {
  return new Promise((resolve, reject) => {
    const req = indexedDB.deleteDatabase('pdx-inapp-fs')
    req.onsuccess = () => resolve()
    req.onerror = () => reject(req.error)
    req.onblocked = () => reject(new Error('deleteDatabase blocked'))
  })
}

let backend: InAppBackend

beforeEach(async () => {
  await closeAllIDB()
  await deleteInappDB()
  backend = new InAppBackend()
})

describe('InAppBackend.onMutation emitter (T2b-6)', () => {
  it('exposes the SupportsMutationEvents capability', () => {
    expect(supportsMutationEvents(backend)).toBe(true)
  })

  it('fires after write', async () => {
    const cb = vi.fn()
    backend.onMutation(cb)
    await backend.write('/buffer/a.txt', enc('hi'))
    expect(cb).toHaveBeenCalledTimes(1)
  })

  it('fires after mkdir', async () => {
    const cb = vi.fn()
    backend.onMutation(cb)
    await backend.mkdir('/buffer/d')
    expect(cb).toHaveBeenCalledTimes(1)
  })

  it('fires after createUnique (new empty file)', async () => {
    const cb = vi.fn()
    backend.onMutation(cb)
    await backend.createUnique('/buffer', 'Untitled', 'md')
    expect(cb).toHaveBeenCalledTimes(1)
  })

  it('fires after mkdirUnique (new empty folder)', async () => {
    const cb = vi.fn()
    backend.onMutation(cb)
    await backend.mkdirUnique('/buffer')
    expect(cb).toHaveBeenCalledTimes(1)
  })

  it('fires after rename', async () => {
    await backend.write('/buffer/a.txt', enc('hi'))
    const cb = vi.fn()
    backend.onMutation(cb)
    await backend.rename('/buffer/a.txt', '/buffer/b.txt')
    expect(cb).toHaveBeenCalledTimes(1)
  })

  it('fires after delete', async () => {
    await backend.write('/buffer/a.txt', enc('hi'))
    const cb = vi.fn()
    backend.onMutation(cb)
    await backend.delete('/buffer/a.txt')
    expect(cb).toHaveBeenCalledTimes(1)
  })

  it('unsubscribe stops further notifications', async () => {
    const cb = vi.fn()
    const off = backend.onMutation(cb)
    await backend.write('/buffer/a.txt', enc('1'))
    off()
    await backend.write('/buffer/a.txt', enc('2'))
    expect(cb).toHaveBeenCalledTimes(1)
  })
})
