import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { InAppBackend } from './fs-backend-inapp'
import { closeAllIDB } from './storage/idb'
import { supportsReplaceTree, type ReplaceEntry } from './fs-backend'

function deleteInappDB(): Promise<void> {
  return new Promise((resolve, reject) => {
    const req = indexedDB.deleteDatabase('pdx-inapp-fs')
    req.onsuccess = () => resolve()
    req.onerror = () => reject(req.error)
    req.onblocked = () => reject(new Error('deleteDatabase blocked'))
  })
}

const enc = (s: string) => new TextEncoder().encode(s)
const dec = (b: Uint8Array) => new TextDecoder().decode(b)

describe('InAppBackend.replaceTree', () => {
  let backend: InAppBackend

  beforeEach(async () => {
    await closeAllIDB()
    await deleteInappDB()
    backend = new InAppBackend()
  })

  afterEach(() => {
    vi.restoreAllMocks()
  })

  it('is narrowable via the supportsReplaceTree guard', () => {
    expect(supportsReplaceTree(backend)).toBe(true)
    expect(supportsReplaceTree(undefined)).toBe(false)
  })

  it('replaces a populated tree to match the target exactly (added/removed/changed/empty-dir)', async () => {
    await backend.write('/buffer/old.txt', enc('old'))
    await backend.write('/buffer/sub/keep.txt', enc('keep-v1'))
    await backend.write('/buffer/removeme/x.txt', enc('gone'))

    const entries: ReplaceEntry[] = [
      { relPath: 'docs', isDir: true },
      { relPath: 'docs/empty', isDir: true },
      { relPath: 'docs/a.md', isDir: false, bytes: enc('AAA') },
      { relPath: 'sub/keep.txt', isDir: false, bytes: enc('keep-v2') },
      { relPath: 'sub', isDir: true },
      { relPath: 'b.txt', isDir: false, bytes: enc('BBB') },
    ]
    await backend.replaceTree('/buffer', entries)

    // Added files present with the restored bytes.
    expect(dec(await backend.read('/buffer/docs/a.md'))).toBe('AAA')
    expect(dec(await backend.read('/buffer/b.txt'))).toBe('BBB')
    // Changed file carries the new content.
    expect(dec(await backend.read('/buffer/sub/keep.txt'))).toBe('keep-v2')
    // Empty dir survives.
    expect((await backend.stat('/buffer/docs/empty')).isDirectory).toBe(true)
    // Removed entries are gone.
    await expect(backend.read('/buffer/old.txt')).rejects.toThrow()
    await expect(backend.read('/buffer/removeme/x.txt')).rejects.toThrow()
  })

  it('does NOT fire onMutation (restore must not self-trigger an auto-backup)', async () => {
    await backend.write('/buffer/a.txt', enc('a'))
    const spy = vi.fn()
    backend.onMutation(spy)
    await backend.replaceTree('/buffer', [
      { relPath: 'a.txt', isDir: false, bytes: enc('a2') },
    ])
    expect(spy).not.toHaveBeenCalled()
  })

  it('rolls back the whole txn on a mid-write failure — prior tree intact (single txn)', async () => {
    await backend.write('/buffer/keep.txt', enc('keep'))
    await backend.write('/buffer/also.txt', enc('also'))

    // Fail the 2nd put() of the write phase (deletes use delete(), not put()).
    let puts = 0
    const orig = IDBObjectStore.prototype.put
    vi.spyOn(IDBObjectStore.prototype, 'put').mockImplementation(function (
      this: IDBObjectStore,
      ...args: unknown[]
    ) {
      puts += 1
      if (puts === 2) throw new Error('boom-put')
      return (orig as (...a: unknown[]) => IDBRequest).apply(this, args)
    })

    await expect(
      backend.replaceTree('/buffer', [
        { relPath: 'x.txt', isDir: false, bytes: enc('x') },
        { relPath: 'y.txt', isDir: false, bytes: enc('y') },
        { relPath: 'z.txt', isDir: false, bytes: enc('z') },
      ]),
    ).rejects.toThrow(/boom-put/)

    vi.restoreAllMocks()

    // The prior tree is fully intact — no half-cleared, no partial new entries.
    expect(dec(await backend.read('/buffer/keep.txt'))).toBe('keep')
    expect(dec(await backend.read('/buffer/also.txt'))).toBe('also')
    await expect(backend.read('/buffer/x.txt')).rejects.toThrow()
    await expect(backend.read('/buffer/y.txt')).rejects.toThrow()
  })

  it('rejects an invalid relPath before opening the txn (tree untouched)', async () => {
    await backend.write('/buffer/keep.txt', enc('keep'))

    const bad: Array<ReplaceEntry[]> = [
      [{ relPath: '../evil', isDir: false, bytes: enc('e') }],
      [{ relPath: '/abs.txt', isDir: false, bytes: enc('e') }],
      [{ relPath: 'a\\b.txt', isDir: false, bytes: enc('e') }],
      [{ relPath: '', isDir: true }],
      [
        { relPath: 'dup.txt', isDir: false, bytes: enc('1') },
        { relPath: 'dup.txt', isDir: false, bytes: enc('2') },
      ],
      // prefix-conflict: a file used as an ancestor directory of another entry.
      [
        { relPath: 'a', isDir: false, bytes: enc('file') },
        { relPath: 'a/b.txt', isDir: false, bytes: enc('child') },
      ],
    ]
    for (const entries of bad) {
      await expect(backend.replaceTree('/buffer', entries)).rejects.toThrow()
    }
    // Nothing mutated by any rejected call.
    expect(dec(await backend.read('/buffer/keep.txt'))).toBe('keep')
  })
})
