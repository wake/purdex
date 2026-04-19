import { describe, expect, it, beforeEach } from 'vitest'
import { openIDB, closeAllIDB } from '../idb'

describe('openIDB', () => {
  beforeEach(async () => {
    await closeAllIDB()
    indexedDB.deleteDatabase('test-db')
  })

  it('creates an objectStore via upgrade callback', async () => {
    const db = await openIDB('test-db', 1, (raw) => {
      raw.createObjectStore('items', { keyPath: 'id' })
    })
    expect(db.objectStoreNames.contains('items')).toBe(true)
  })

  it('reuses the same connection for same name', async () => {
    const a = await openIDB('test-db', 1, (raw) => {
      raw.createObjectStore('items', { keyPath: 'id' })
    })
    const b = await openIDB('test-db', 1, (raw) => {
      raw.createObjectStore('items', { keyPath: 'id' })
    })
    expect(a).toBe(b)
  })

  it('runs upgrade when opened with a higher version (F1: cache keyed by version)', async () => {
    const v1 = await openIDB('test-db', 1, (raw) => {
      raw.createObjectStore('v1', { keyPath: 'id' })
    })
    expect(v1.objectStoreNames.contains('v1')).toBe(true)
    v1.close()

    let upgradeCalledForV2 = false
    const v2 = await openIDB('test-db', 2, (raw) => {
      upgradeCalledForV2 = true
      if (!raw.objectStoreNames.contains('v2')) raw.createObjectStore('v2', { keyPath: 'id' })
    })
    expect(upgradeCalledForV2).toBe(true)
    expect(v2.objectStoreNames.contains('v2')).toBe(true)
    expect(v2).not.toBe(v1)
  })
})
