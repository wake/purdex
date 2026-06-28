import { describe, it, expect, beforeEach } from 'vitest'
import { InAppBackend } from '../fs-backend-inapp'
import { closeAllIDB } from '../storage/idb'
import { sha256Hex } from '../crypto-hash'
import { buildManifest } from './manifest'

const enc = (s: string) => new TextEncoder().encode(s)

function deleteInappDB(): Promise<void> {
  return new Promise((resolve, reject) => {
    const req = indexedDB.deleteDatabase('pdx-inapp-fs')
    req.onsuccess = () => resolve()
    req.onerror = () => reject(req.error)
    req.onblocked = () => reject(new Error('deleteDatabase blocked'))
  })
}

// A high BMP private-use char (U+E000) and a supplementary emoji (U+1F600).
// In UTF-16 code-unit order (JS default `.sort()` / `<`) the emoji's leading
// surrogate 0xD83D sorts BEFORE 0xE000, so the emoji would come first. In UTF-8
// byte order (and Go string byte order, the daemon's order) U+E000 (0xEE..)
// sorts BEFORE the emoji (0xF0..). The manifest comparator must follow UTF-8.
const PUA = '.txt'
const EMOJI = '\u{1F600}.txt'

describe('buildManifest — canonical In-App tree manifest', () => {
  let backend: InAppBackend

  beforeEach(async () => {
    await closeAllIDB()
    await deleteInappDB()
    backend = new InAppBackend()
    // Empty dir (must round-trip as a kind:'dir' entry).
    await backend.mkdir('/buffer/B')
    // ASCII files + a nested dir.
    await backend.write('/buffer/a.txt', enc('one two three'))
    await backend.write('/buffer/c.bin', new Uint8Array([0xff, 0x00, 0xfe]))
    // Two files with IDENTICAL bytes → one shared hash, one blob entry.
    await backend.write('/buffer/dupA.txt', enc('same content'))
    await backend.write('/buffer/dupB.txt', enc('same content'))
    // Nested dir + file (relative path + parent-before-child ordering).
    await backend.write('/buffer/sub/inner.md', enc('a b'))
    // Non-ASCII filenames (UTF-8 byte-order divergence).
    await backend.write(`/buffer/${PUA}`, enc('x'))
    await backend.write(`/buffer/${EMOJI}`, enc('y'))
  })

  it('emits root-relative entries sorted by UTF-8 bytes of path (daemon order)', async () => {
    const { entries } = await buildManifest(backend)
    const paths = entries.map((e) => e.path)
    expect(paths).toEqual([
      'B',
      'a.txt',
      'c.bin',
      'dupA.txt',
      'dupB.txt',
      'sub',
      'sub/inner.md',
      PUA,
      EMOJI,
    ])
  })

  it('UTF-8 byte order differs from the naive JS UTF-16 sort (PUA before emoji)', async () => {
    const { entries } = await buildManifest(backend)
    const paths = entries.map((e) => e.path)
    const naive = [...paths].sort() // UTF-16 code-unit order
    // Naive order would put the emoji before the PUA char.
    expect(naive.indexOf(EMOJI)).toBeLessThan(naive.indexOf(PUA))
    // Canonical (UTF-8) order is the opposite.
    expect(paths.indexOf(PUA)).toBeLessThan(paths.indexOf(EMOJI))
  })

  it('directory entries carry kind:dir / hash:"" / size:0 / words:0 incl empty + nested', async () => {
    const { entries } = await buildManifest(backend)
    const byPath = new Map(entries.map((e) => [e.path, e]))
    for (const dir of ['B', 'sub']) {
      expect(byPath.get(dir)).toEqual({ path: dir, kind: 'dir', hash: '', size: 0, words: 0 })
    }
  })

  it('file entries carry sha256 + byte size + word count', async () => {
    const { entries } = await buildManifest(backend)
    const byPath = new Map(entries.map((e) => [e.path, e]))
    const a = byPath.get('a.txt')!
    expect(a.kind).toBe('file')
    expect(a.size).toBe(enc('one two three').byteLength)
    expect(a.words).toBe(3)
    expect(a.hash).toBe(await sha256Hex(enc('one two three')))
    // Binary file: counted as 0 words.
    expect(byPath.get('c.bin')!.words).toBe(0)
    // Nested text file word count.
    expect(byPath.get('sub/inner.md')!.words).toBe(2)
  })

  it('dedups identical-byte files to one shared hash and one blob entry', async () => {
    const { entries, blobs } = await buildManifest(backend)
    const byPath = new Map(entries.map((e) => [e.path, e]))
    const dupHash = await sha256Hex(enc('same content'))
    expect(byPath.get('dupA.txt')!.hash).toBe(dupHash)
    expect(byPath.get('dupB.txt')!.hash).toBe(dupHash)
    // 6 distinct file contents: a.txt, c.bin, 'same content', inner.md, 'x', 'y'.
    expect(blobs.size).toBe(6)
    expect(blobs.has(dupHash)).toBe(true)
  })
})
