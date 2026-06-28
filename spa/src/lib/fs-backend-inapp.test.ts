import { describe, it, expect, beforeEach } from 'vitest'
import { InAppBackend } from './fs-backend-inapp'
import { listTreeUnder } from './storage-tree'
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

  // AC12 — mkdir + write retain lenient overwrite semantics (I6). NOTE: rename
  // collision is NO LONGER lenient as of Phase 1b T1 — see "1b T1: rename
  // collision" cases below, which require rename to refuse before any mutation
  // (spec AC-1b: "File rename to an existing path is refused before any backend
  // mutation").

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

  // AC13 (Phase 1b T1) — rename is now dir-aware + recursive: moving a folder
  // re-keys EVERY descendant and removes all old paths (spec §2 gap P2-7 / AC-1b).
  it('AC13: rename moves a directory recursively — all descendants re-keyed, old paths gone', async () => {
    // seed a 2-level nested tree with distinct contents
    await backend.mkdir('/buffer/a')
    await backend.write('/buffer/a/x.md', enc('X-CONTENT'))
    await backend.mkdir('/buffer/a/b')
    await backend.write('/buffer/a/b/y.md', enc('Y-CONTENT'))

    await backend.rename('/buffer/a', '/buffer/z')

    // new tree exists with IDENTICAL content
    expect((await backend.stat('/buffer/z')).isDirectory).toBe(true)
    expect(dec(await backend.read('/buffer/z/x.md'))).toBe('X-CONTENT')
    expect((await backend.stat('/buffer/z/b')).isDirectory).toBe(true)
    expect(dec(await backend.read('/buffer/z/b/y.md'))).toBe('Y-CONTENT')

    // every old /buffer/a* key is gone
    await expect(backend.stat('/buffer/a')).rejects.toThrow()
    await expect(backend.read('/buffer/a/x.md')).rejects.toThrow()
    await expect(backend.stat('/buffer/a/b')).rejects.toThrow()
    await expect(backend.read('/buffer/a/b/y.md')).rejects.toThrow()
    // /buffer parent only contains the moved dir now
    expect((await backend.list('/buffer')).map((e) => e.name)).toEqual(['z'])
  })

  // 1b T1 — a move is not a content edit: every moved descendant keeps its mtime
  it('1b T1: recursive move preserves each descendant mtime (not a content edit)', async () => {
    await backend.mkdir('/buffer/a')
    await backend.write('/buffer/a/x.md', enc('X'))
    await backend.write('/buffer/a/b/y.md', enc('Y')) // auto-creates /buffer/a/b

    // capture original mtimes
    const m = {
      a: (await backend.stat('/buffer/a')).mtime,
      x: (await backend.stat('/buffer/a/x.md')).mtime,
      b: (await backend.stat('/buffer/a/b')).mtime,
      y: (await backend.stat('/buffer/a/b/y.md')).mtime,
    }

    await backend.rename('/buffer/a', '/buffer/z')

    expect((await backend.stat('/buffer/z')).mtime).toBe(m.a)
    expect((await backend.stat('/buffer/z/x.md')).mtime).toBe(m.x)
    expect((await backend.stat('/buffer/z/b')).mtime).toBe(m.b)
    expect((await backend.stat('/buffer/z/b/y.md')).mtime).toBe(m.y)
  })

  // 1b T1 — collision over the target subtree must throw with NO partial mutation:
  // reject if `to` exists OR any `${to}/`-prefixed key exists, and leave the
  // original tree fully intact (spec AC-1b: refused before any backend mutation).
  it('1b T1: rename throws when `to` exactly exists, original tree intact', async () => {
    await backend.mkdir('/buffer/a')
    await backend.write('/buffer/a/x.md', enc('X'))
    await backend.write('/buffer/z', enc('OCCUPIED')) // exact `to` collision

    await expect(backend.rename('/buffer/a', '/buffer/z')).rejects.toThrow()

    // nothing moved: original tree intact, target untouched
    expect((await backend.stat('/buffer/a')).isDirectory).toBe(true)
    expect(dec(await backend.read('/buffer/a/x.md'))).toBe('X')
    expect(dec(await backend.read('/buffer/z'))).toBe('OCCUPIED')
  })

  it('1b T1: rename throws when a `${to}/child` key exists, original tree intact', async () => {
    await backend.mkdir('/buffer/a')
    await backend.write('/buffer/a/x.md', enc('X'))
    // no exact /buffer/z, but a descendant of the target already exists
    await backend.write('/buffer/z/occupied.md', enc('OCCUPIED'))

    await expect(backend.rename('/buffer/a', '/buffer/z')).rejects.toThrow()

    // original tree intact
    expect((await backend.stat('/buffer/a')).isDirectory).toBe(true)
    expect(dec(await backend.read('/buffer/a/x.md'))).toBe('X')
    // target subtree untouched
    expect(dec(await backend.read('/buffer/z/occupied.md'))).toBe('OCCUPIED')
  })

  // 1b T1 — file move regression: single-entry re-key still works
  it('1b T1: rename moves a single file correctly (regression)', async () => {
    await backend.write('/buffer/a/x.md', enc('FILE'))
    await backend.rename('/buffer/a/x.md', '/buffer/a/y.md')
    expect(dec(await backend.read('/buffer/a/y.md'))).toBe('FILE')
    await expect(backend.read('/buffer/a/x.md')).rejects.toThrow()
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

// T1b-2 — createUnique: atomic eager reservation of a unique empty file via
// IDB store.add (the single serialization point that fixes #854's double-click
// shared-key race).
describe('InAppBackend.createUnique (atomic eager namer)', () => {
  let backend: InAppBackend

  beforeEach(async () => {
    await closeAllIDB()
    await deleteInappDB()
    backend = new InAppBackend()
  })

  // T2-1: empty dir → first candidate, ext honored (bare, no leading dot).
  it('T2-1: reserves /buffer/Untitled.md on an empty dir', async () => {
    const path = await backend.createUnique('/buffer', 'Untitled', 'md')
    expect(path).toBe('/buffer/Untitled.md')
    expect((await backend.read(path)).byteLength).toBe(0)
  })

  it('T2-1: honors a txt extension', async () => {
    const path = await backend.createUnique('/buffer', 'Untitled', 'txt')
    expect(path).toBe('/buffer/Untitled.txt')
  })

  // T2-2: collision → increment the suffix.
  it('T2-2: returns /buffer/Untitled-1.md when /buffer/Untitled.md exists', async () => {
    await backend.write('/buffer/Untitled.md', enc(''))
    const path = await backend.createUnique('/buffer', 'Untitled', 'md')
    expect(path).toBe('/buffer/Untitled-1.md')
  })

  it('T2-2: skips multiple existing candidates', async () => {
    await backend.write('/buffer/Untitled.md', enc(''))
    await backend.write('/buffer/Untitled-1.md', enc(''))
    await backend.write('/buffer/Untitled-2.md', enc(''))
    const path = await backend.createUnique('/buffer', 'Untitled', 'md')
    expect(path).toBe('/buffer/Untitled-3.md')
  })

  // T2-3: #854 race — concurrent callers must get distinct keys, no shared key.
  it('T2-3: concurrent createUnique calls reserve distinct paths (#854)', async () => {
    const [a, b] = await Promise.all([
      backend.createUnique('/buffer', 'Untitled', 'md'),
      backend.createUnique('/buffer', 'Untitled', 'md'),
    ])
    expect(a).not.toBe(b)
    const names = (await backend.list('/buffer')).map((e) => e.name).sort()
    expect(names).toEqual(['Untitled-1.md', 'Untitled.md'])
  })

  // T2-4: reserved file is empty and shows up in a recursive tree enumeration.
  it('T2-4: reserved file is empty and appears under listTreeUnder', async () => {
    const path = await backend.createUnique('/buffer', 'Untitled', 'md')
    const stat = await backend.stat(path)
    expect(stat.size).toBe(0)
    expect(stat.isFile).toBe(true)
    const tree = await listTreeUnder(backend, '/buffer')
    expect(tree.map((n) => n.path)).toContain('/buffer/Untitled.md')
  })
})

// T1c-1 — createUnique generalized: optional initial `content` written on the
// same atomic add(), arbitrary `ext` (widened from 'md'|'txt'), and ext-less
// names with NO trailing dot. The 1b 3-arg callers stay unaffected (empty file).
describe('InAppBackend.createUnique (content + arbitrary extension, Phase 1c T1c-1)', () => {
  let backend: InAppBackend

  beforeEach(async () => {
    await closeAllIDB()
    await deleteInappDB()
    backend = new InAppBackend()
  })

  it('writes the supplied bytes into the reserved file (byte-identical)', async () => {
    const bytes = new Uint8Array([0x00, 0x10, 0xff, 0x42, 0x7a])
    const path = await backend.createUnique('/buffer', 'photo', 'png', bytes)
    expect(path).toBe('/buffer/photo.png')
    expect(Array.from(await backend.read(path))).toEqual(Array.from(bytes))
  })

  it('accepts arbitrary extensions (pdf / docx)', async () => {
    const p1 = await backend.createUnique('/buffer', 'doc', 'pdf', new Uint8Array([1]))
    const p2 = await backend.createUnique('/buffer', 'sheet', 'docx', new Uint8Array([2]))
    expect(p1).toBe('/buffer/doc.pdf')
    expect(p2).toBe('/buffer/sheet.docx')
  })

  it('forms an ext-less name (README) with NO trailing dot', async () => {
    const path = await backend.createUnique('/buffer', 'README', '', new Uint8Array([5]))
    expect(path).toBe('/buffer/README')
    expect((await backend.stat(path)).isFile).toBe(true)
    expect(Array.from(await backend.read(path))).toEqual([5])
  })

  it('collision still increments the -N suffix and leaves the original content intact', async () => {
    await backend.createUnique('/buffer', 'a', 'bin', new Uint8Array([1]))
    const path = await backend.createUnique('/buffer', 'a', 'bin', new Uint8Array([2, 2]))
    expect(path).toBe('/buffer/a-1.bin')
    expect(Array.from(await backend.read('/buffer/a.bin'))).toEqual([1])
    expect(Array.from(await backend.read('/buffer/a-1.bin'))).toEqual([2, 2])
  })

  it('a 1b 3-arg call (no content) still reserves an EMPTY file', async () => {
    const path = await backend.createUnique('/buffer', 'Untitled', 'md')
    expect(path).toBe('/buffer/Untitled.md')
    expect((await backend.read(path)).byteLength).toBe(0)
  })
})

// T1b-3 — mkdirUnique: atomic eager reservation of a unique DIRECTORY via the
// same IDB store.add serialization point as createUnique. Folder candidates use
// a SPACE-separated suffix ("New Folder", "New Folder 1", …), unlike files'
// hyphen, matching the plan's T3-3 expectation.
describe('InAppBackend.mkdirUnique (atomic eager folder namer)', () => {
  let backend: InAppBackend

  beforeEach(async () => {
    await closeAllIDB()
    await deleteInappDB()
    backend = new InAppBackend()
  })

  // M1: empty dir → first candidate is a real directory entry.
  it('M1: reserves /buffer/New Folder as a directory on an empty dir', async () => {
    const path = await backend.mkdirUnique('/buffer')
    expect(path).toBe('/buffer/New Folder')
    const stat = await backend.stat(path)
    expect(stat.isDirectory).toBe(true)
    expect(stat.isFile).toBe(false)
  })

  it('M1: honors a custom baseName', async () => {
    const path = await backend.mkdirUnique('/buffer', 'Docs')
    expect(path).toBe('/buffer/Docs')
    expect((await backend.stat(path)).isDirectory).toBe(true)
  })

  // M2: collision → space-separated increment.
  it('M2: returns "/buffer/New Folder 1" when "/buffer/New Folder" exists', async () => {
    await backend.mkdir('/buffer/New Folder')
    const path = await backend.mkdirUnique('/buffer')
    expect(path).toBe('/buffer/New Folder 1')
  })

  it('M2: skips multiple existing candidates', async () => {
    await backend.mkdir('/buffer/New Folder')
    await backend.mkdir('/buffer/New Folder 1')
    await backend.mkdir('/buffer/New Folder 2')
    const path = await backend.mkdirUnique('/buffer')
    expect(path).toBe('/buffer/New Folder 3')
  })

  // M3: concurrent double-create must yield TWO distinct folders (no overwrite),
  // exercising the add-reserve race fix (decision 7).
  it('M3: concurrent mkdirUnique calls reserve distinct folders (no overwrite)', async () => {
    const [a, b] = await Promise.all([
      backend.mkdirUnique('/buffer'),
      backend.mkdirUnique('/buffer'),
    ])
    expect(a).not.toBe(b)
    const names = (await backend.list('/buffer')).map((e) => e.name).sort()
    expect(names).toEqual(['New Folder', 'New Folder 1'])
    // both are real directories
    for (const p of [a, b]) {
      expect((await backend.stat(p)).isDirectory).toBe(true)
    }
  })

  // M4: the reserved folder is a real directory that accepts children.
  it('M4: the reserved folder accepts a child file', async () => {
    const dir = await backend.mkdirUnique('/buffer')
    await backend.write(`${dir}/note.md`, enc('hi'))
    const tree = await listTreeUnder(backend, '/buffer')
    const folder = tree.find((n) => n.path === dir)
    expect(folder?.isDir).toBe(true)
    expect(folder?.children?.map((c) => c.path)).toContain(`${dir}/note.md`)
  })
})
