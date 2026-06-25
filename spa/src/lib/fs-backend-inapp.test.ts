import { describe, it, expect, beforeEach } from 'vitest'
import { InAppBackend } from './fs-backend-inapp'
import { closeAllIDB } from './storage/idb'
import {
  registerFsBackend,
  getFsBackend,
  clearFsBackendRegistry,
} from './fs-backend'

// Promise-wrapped deleteDatabase. indexedDB.deleteDatabase returns an
// IDBOpenDBRequest (NOT a Promise), so `await indexedDB.deleteDatabase(...)`
// does NOT wait for the delete to finish. We must wrap it and wait for
// onsuccess; onblocked means a connection was not closed and the next case
// would race against stale data, so we treat it as a failure.
function deleteInappDB(): Promise<void> {
  return new Promise((resolve, reject) => {
    const req = indexedDB.deleteDatabase('pdx-inapp-fs')
    req.onsuccess = () => resolve()
    req.onerror = () => reject(req.error)
    req.onblocked = () =>
      reject(new Error('deleteDatabase blocked — connection not closed'))
  })
}

// Simulate a process restart: close cached IDB connections so a fresh
// InAppBackend must reopen the landed DB (rather than reuse the openIDB
// singleton connection, which would only prove cache reuse, not persistence).
async function reopenBackend(): Promise<InAppBackend> {
  await closeAllIDB()
  return new InAppBackend()
}

const enc = (s: string) => new TextEncoder().encode(s)
const dec = (b: Uint8Array) => new TextDecoder().decode(b)

describe('InAppBackend (IndexedDB-backed)', () => {
  let backend: InAppBackend

  beforeEach(async () => {
    // fake-indexeddb is an in-memory implementation that persists across
    // cases, so we must explicitly close all cached connections and drop the
    // DB before each test for isolation.
    await closeAllIDB()
    await deleteInappDB()
    backend = new InAppBackend()
  })

  it('reports as available', () => {
    expect(backend.available()).toBe(true)
  })

  // AC1
  it('AC1: write then read returns the content', async () => {
    await backend.write('/buffer/a.txt', enc('hello world'))
    expect(dec(await backend.read('/buffer/a.txt'))).toBe('hello world')
  })

  // AC2 — persist core: write → reopen (simulate restart) → read
  it('AC2: content persists across backend re-creation (closeAllIDB)', async () => {
    await backend.write('/buffer/a.txt', enc('persist me'))
    const fresh = await reopenBackend()
    expect(dec(await fresh.read('/buffer/a.txt'))).toBe('persist me')
  })

  // AC3 — auto-create parent directories
  it('AC3: write auto-creates parent directories', async () => {
    await backend.write('/buffer/sub/a.txt', enc('x'))
    const stat = await backend.stat('/buffer/sub')
    expect(stat.isDirectory).toBe(true)
  })

  // AC4 — stat shape
  it('AC4: stat returns correct size/mtime/isDirectory/isFile', async () => {
    await backend.write('/buffer/stat.txt', enc('abc'))
    const stat = await backend.stat('/buffer/stat.txt')
    expect(stat.isFile).toBe(true)
    expect(stat.isDirectory).toBe(false)
    expect(stat.size).toBe(3)
    expect(stat.mtime).toBeGreaterThan(0)
  })

  // AC5 — list direct children, dir-first + name sorted, no grandchildren
  it('AC5: list returns direct children, dir-first + name sorted, no grandchildren', async () => {
    await backend.write('/buffer/b.txt', enc('b'))
    await backend.write('/buffer/a.txt', enc('a'))
    await backend.write('/buffer/sub/deep.txt', enc('d')) // creates dir /buffer/sub
    const entries = await backend.list('/buffer')
    // dir-first then name-sorted: sub (dir), a.txt, b.txt
    expect(entries.map((e) => e.name)).toEqual(['sub', 'a.txt', 'b.txt'])
    expect(entries[0].isDir).toBe(true)
    // no grandchildren leaking
    expect(entries.map((e) => e.name)).not.toContain('deep.txt')
  })

  // AC6 — mkdir
  it('AC6: mkdir creates a directory entry', async () => {
    await backend.mkdir('/buffer/d')
    const stat = await backend.stat('/buffer/d')
    expect(stat.isDirectory).toBe(true)
  })

  // AC7 — delete file
  it('AC7: delete a file makes read throw', async () => {
    await backend.write('/buffer/del.txt', enc('x'))
    await backend.delete('/buffer/del.txt')
    await expect(backend.read('/buffer/del.txt')).rejects.toThrow()
  })

  // AC8 — delete dir recursively removes prefix children
  it('AC8: delete a directory recursively removes children', async () => {
    await backend.write('/buffer/d/a.txt', enc('a'))
    await backend.write('/buffer/d/sub/b.txt', enc('b'))
    await backend.delete('/buffer/d')
    await expect(backend.read('/buffer/d/a.txt')).rejects.toThrow()
    await expect(backend.read('/buffer/d/sub/b.txt')).rejects.toThrow()
  })

  // AC9 — rename moves a file
  it('AC9: rename moves content, source read throws', async () => {
    await backend.write('/buffer/old.txt', enc('move me'))
    await backend.rename('/buffer/old.txt', '/buffer/new.txt')
    expect(dec(await backend.read('/buffer/new.txt'))).toBe('move me')
    await expect(backend.read('/buffer/old.txt')).rejects.toThrow()
  })

  // AC10 — read/stat/rename throw for nonexistent
  it('AC10: read/stat/rename throw for nonexistent paths', async () => {
    await expect(backend.read('/buffer/nope')).rejects.toThrow()
    await expect(backend.stat('/buffer/nope')).rejects.toThrow()
    await expect(
      backend.rename('/buffer/nope', '/buffer/whatever'),
    ).rejects.toThrow()
  })

  // AC11 — persisted list + stat after rebuild (split from delete/rename below
  // so a failure points at one invariant; codex health-review #1).
  it('AC11: persisted list + stat work after rebuild', async () => {
    await backend.write('/buffer/x.txt', enc('x'))
    await backend.write('/buffer/y.txt', enc('y'))
    await backend.mkdir('/buffer/dir')
    const fresh = await reopenBackend()

    expect((await fresh.list('/buffer')).map((e) => e.name)).toEqual([
      'dir',
      'x.txt',
      'y.txt',
    ])
    const xstat = await fresh.stat('/buffer/x.txt')
    expect(xstat.size).toBe(1)
    expect(xstat.isFile).toBe(true)
  })

  // AC11b — persisted recursive delete after rebuild
  it('AC11b: persisted recursive delete works after rebuild', async () => {
    await backend.write('/buffer/d/a.txt', enc('a'))
    await backend.write('/buffer/d/sub/b.txt', enc('b'))
    const fresh = await reopenBackend()
    await fresh.delete('/buffer/d')
    await expect(fresh.read('/buffer/d/a.txt')).rejects.toThrow()
    await expect(fresh.read('/buffer/d/sub/b.txt')).rejects.toThrow()
  })

  // AC11c — persisted rename after rebuild
  it('AC11c: persisted rename works after rebuild', async () => {
    await backend.write('/buffer/x.txt', enc('x'))
    const fresh = await reopenBackend()
    await fresh.rename('/buffer/x.txt', '/buffer/z.txt')
    expect(dec(await fresh.read('/buffer/z.txt'))).toBe('x')
    await expect(fresh.read('/buffer/x.txt')).rejects.toThrow()
  })

  // AC12 — preserve lenient overwrite semantics (I6): rename to existing target
  // overwrites and does NOT throw; mkdir on existing path does NOT throw; write
  // to a path whose parent is an existing file does NOT throw.
  it('AC12: rename to existing target blind-overwrites without throwing', async () => {
    await backend.write('/buffer/a.txt', enc('AAA'))
    await backend.write('/buffer/b.txt', enc('BBB'))
    await backend.rename('/buffer/a.txt', '/buffer/b.txt')
    expect(dec(await backend.read('/buffer/b.txt'))).toBe('AAA')
    await expect(backend.read('/buffer/a.txt')).rejects.toThrow()
  })

  it('AC12: mkdir on existing path does not throw', async () => {
    await backend.mkdir('/buffer/d')
    await expect(backend.mkdir('/buffer/d')).resolves.toBeUndefined()
    const stat = await backend.stat('/buffer/d')
    expect(stat.isDirectory).toBe(true)
  })

  it('AC12: write where parent is an existing file does not throw (no FS validation)', async () => {
    await backend.write('/buffer/parent', enc('iamafile'))
    // parent already exists as a file; lenient semantics must not throw
    await expect(
      backend.write('/buffer/parent/child.txt', enc('child')),
    ).resolves.toBeUndefined()
    expect(dec(await backend.read('/buffer/parent/child.txt'))).toBe('child')
  })

  // AC13 — rename is non-recursive (I7): only the single entry moves
  it('AC13: rename is non-recursive — children stay at original paths', async () => {
    await backend.write('/buffer/d/a.txt', enc('a'))
    await backend.mkdir('/buffer/d')
    await backend.rename('/buffer/d', '/buffer/e')
    // /buffer/d entry itself moved
    const eStat = await backend.stat('/buffer/e')
    expect(eStat.isDirectory).toBe(true)
    // child stays at original path (non-recursive)
    expect(dec(await backend.read('/buffer/d/a.txt'))).toBe('a')
  })

  // AC14 — binary / empty payload persists byte-for-byte across rebuild
  it('AC14: empty and non-text binary payloads persist byte-for-byte', async () => {
    const empty = new Uint8Array(0)
    const binary = new Uint8Array([0, 255, 128, 1])
    await backend.write('/buffer/empty.bin', empty)
    await backend.write('/buffer/bytes.bin', binary)
    const fresh = await reopenBackend()

    const emptyOut = await fresh.read('/buffer/empty.bin')
    const binaryOut = await fresh.read('/buffer/bytes.bin')
    expect(Array.from(emptyOut)).toEqual([])
    expect(emptyOut.byteLength).toBe(0)
    expect(Array.from(binaryOut)).toEqual([0, 255, 128, 1])
  })

  // Thin integration case: registry path (getFsBackend) persists end-to-end.
  it('integration: registry-resolved inapp backend persists across closeAllIDB', async () => {
    clearFsBackendRegistry()
    registerFsBackend('inapp', new InAppBackend())
    const reg = getFsBackend({ type: 'inapp' })
    expect(reg).toBeDefined()
    await reg!.write('/buffer/reg.txt', enc('via registry'))

    await closeAllIDB()

    // simulate process restart: fresh registry + fresh backend instance,
    // reading the landed DB.
    clearFsBackendRegistry()
    registerFsBackend('inapp', new InAppBackend())
    const reg2 = getFsBackend({ type: 'inapp' })
    expect(dec(await reg2!.read('/buffer/reg.txt'))).toBe('via registry')

    clearFsBackendRegistry()
  })
})
