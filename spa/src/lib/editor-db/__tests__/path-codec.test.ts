import { afterEach, describe, expect, it, vi } from 'vitest'
import { EDITOR_CONTENTS_STORE, EDITOR_NODES_STORE, openEditorDb } from '../db'
import { canonicalizePath, isDescendantPath, splitParentPath } from '../path-codec'

type FakeOpenRequest = IDBOpenDBRequest & {
  triggerBlocked: () => void
  triggerError: (error: Error) => void
  triggerSuccess: () => void
  triggerUpgradeNeeded: () => void
}

function createFakeObjectStore(): IDBObjectStore {
  return {
    createIndex: vi.fn(),
  } as unknown as IDBObjectStore
}

function createFakeDatabase() {
  const nodesStore = createFakeObjectStore()
  const contentsStore = createFakeObjectStore()
  const database = {
    close: vi.fn(),
    createObjectStore: vi.fn((storeName: string) => {
      if (storeName === EDITOR_NODES_STORE) {
        return nodesStore
      }

      if (storeName === EDITOR_CONTENTS_STORE) {
        return contentsStore
      }

      throw new Error(`Unexpected object store: ${storeName}`)
    }),
    onversionchange: null,
  } as unknown as IDBDatabase

  return { contentsStore, database, nodesStore }
}

function createFakeOpenRequest(database: IDBDatabase): FakeOpenRequest {
  const request = {
    error: null,
    onblocked: null,
    onerror: null,
    onsuccess: null,
    onupgradeneeded: null,
    result: database,
    triggerBlocked() {
      request.onblocked?.(new Event('blocked'))
    },
    triggerError(error: Error) {
      request.error = error
      request.onerror?.(new Event('error'))
    },
    triggerSuccess() {
      request.onsuccess?.(new Event('success'))
    },
    triggerUpgradeNeeded() {
      request.onupgradeneeded?.(new Event('upgradeneeded'))
    },
  }

  return request as FakeOpenRequest
}

afterEach(async () => {
  vi.unstubAllGlobals()
})

describe('canonicalizePath', () => {
  it('normalizes duplicate slashes and strips trailing slash', () => {
    expect(canonicalizePath('//notes//daily///')).toBe('/notes/daily')
  })

  it('preserves root slash', () => {
    expect(canonicalizePath('/')).toBe('/')
  })

  it('rejects relative paths', () => {
    expect(() => canonicalizePath('notes/a.md')).toThrow(/absolute/i)
  })

  it('rejects dot segments', () => {
    expect(() => canonicalizePath('/notes/../a.md')).toThrow(/dot/i)
  })
})

describe('splitParentPath', () => {
  it('splits canonical path into parent and name', () => {
    expect(splitParentPath('/notes/a.md')).toEqual({ parentPath: '/notes', name: 'a.md' })
  })

  it('uses root as parent for top-level files', () => {
    expect(splitParentPath('/untitled.md')).toEqual({ parentPath: '/', name: 'untitled.md' })
  })

  it('rejects root because it has no parent', () => {
    expect(() => splitParentPath('/')).toThrow(/root/i)
  })
})

describe('isDescendantPath', () => {
  it('matches path segments, not raw string prefix', () => {
    expect(isDescendantPath('/notes/a.md', '/notes')).toBe(true)
    expect(isDescendantPath('/notes-old/a.md', '/notes')).toBe(false)
  })

  it('does not treat root as a descendant of itself', () => {
    expect(isDescendantPath('/', '/')).toBe(false)
  })
})

describe('openEditorDb', () => {
  it('closes the connection and notifies on versionchange', async () => {
    const { database, nodesStore } = createFakeDatabase()
    const request = createFakeOpenRequest(database)
    const open = vi.fn(() => request)
    const onVersionChange = vi.fn()

    vi.stubGlobal('indexedDB', { open })

    const databasePromise = openEditorDb({ onVersionChange })
    request.triggerUpgradeNeeded()
    request.triggerSuccess()

    const resolvedDatabase = await databasePromise
    const event = new Event('versionchange')
    resolvedDatabase.onversionchange?.(event as IDBVersionChangeEvent)

    expect(onVersionChange).toHaveBeenCalledWith(event, resolvedDatabase)
    expect(database.close).toHaveBeenCalledTimes(1)
    expect(nodesStore.createIndex).toHaveBeenCalledWith('path', 'path', { unique: true })
    expect(nodesStore.createIndex).toHaveBeenCalledWith('docId', 'docId', { unique: true })
    expect(nodesStore.createIndex).toHaveBeenCalledWith('parentPath', 'parentPath', { unique: false })
  })

  it('still closes the connection when onVersionChange throws', async () => {
    const { database } = createFakeDatabase()
    const request = createFakeOpenRequest(database)
    const open = vi.fn(() => request)
    const onVersionChange = vi.fn(() => {
      throw new Error('versionchange callback failed')
    })

    vi.stubGlobal('indexedDB', { open })

    const databasePromise = openEditorDb({ onVersionChange })
    request.triggerSuccess()

    const resolvedDatabase = await databasePromise
    const event = new Event('versionchange')

    expect(() => {
      resolvedDatabase.onversionchange?.(event as IDBVersionChangeEvent)
    }).toThrow('versionchange callback failed')
    expect(database.close).toHaveBeenCalledTimes(1)
  })

  it('surfaces blocked upgrades before resolving the new connection', async () => {
    const { database } = createFakeDatabase()
    const request = createFakeOpenRequest(database)
    const open = vi.fn(() => request)
    const onBlocked = vi.fn()

    vi.stubGlobal('indexedDB', { open })

    const databasePromise = openEditorDb({ onBlocked })
    request.triggerBlocked()

    expect(onBlocked).toHaveBeenCalledTimes(1)

    request.triggerSuccess()
    await expect(databasePromise).resolves.toBe(database)
  })
})
