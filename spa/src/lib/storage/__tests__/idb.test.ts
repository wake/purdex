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
})
