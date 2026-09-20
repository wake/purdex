import { describe, it, expect, vi, afterEach } from 'vitest'
import { sha256Hex, sha256HexSync } from './crypto-hash'

const enc = (s: string) => new TextEncoder().encode(s)

// Reference digest straight from WebCrypto (the test environment has it), NOT
// through `sha256Hex` — so a bug in the wrapper cannot mask a fallback bug.
const realSubtle = globalThis.crypto.subtle
async function subtleHex(bytes: Uint8Array): Promise<string> {
  const digest = await realSubtle.digest('SHA-256', bytes as unknown as ArrayBuffer)
  return Array.from(new Uint8Array(digest), (b) => b.toString(16).padStart(2, '0')).join('')
}

// mulberry32 — fixed-seed PRNG so the random comparison is reproducible.
function mulberry32(seed: number): () => number {
  let a = seed >>> 0
  return () => {
    a = (a + 0x6d2b79f5) >>> 0
    let t = a
    t = Math.imul(t ^ (t >>> 15), t | 1)
    t ^= t + Math.imul(t ^ (t >>> 7), t | 61)
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296
  }
}

// Expected values verified with `shasum -a 256` / node `crypto.createHash`.
const VECTORS: Array<[string, string, string]> = [
  ['empty', '', 'e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855'],
  ['"abc"', 'abc', 'ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad'],
  [
    '448-bit message',
    'abcdbcdecdefdefgefghfghighijhijkijkljklmklmnlmnomnopnopq',
    '248d6a61d20638b8e5c026930c3e6039a33ce45964ff2167f6ecedd419db06c1',
  ],
]
const MILLION_A = 'cdc76e5c9914fb9281a1c7e284d73e67f1809a48a497200e046d39ccc7112cd0'

describe('sha256Hex (browser WebCrypto)', () => {
  it('hashes the empty input to the known SHA-256 vector', async () => {
    const hex = await sha256Hex(new Uint8Array(0))
    expect(hex).toBe('e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855')
  })

  it('hashes "abc" to the known SHA-256 vector', async () => {
    const hex = await sha256Hex(enc('abc'))
    expect(hex).toBe('ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad')
  })

  it('returns 64-char lowercase hex', async () => {
    const hex = await sha256Hex(enc('the quick brown fox'))
    expect(hex).toMatch(/^[0-9a-f]{64}$/)
  })

  it('is deterministic for identical bytes', async () => {
    const a = await sha256Hex(enc('same'))
    const b = await sha256Hex(enc('same'))
    expect(a).toBe(b)
  })
})

describe('sha256HexSync (pure-JS FIPS 180-4)', () => {
  it.each(VECTORS)('matches the NIST vector: %s', (_name, input, expected) => {
    expect(sha256HexSync(enc(input))).toBe(expected)
  })

  it('matches the NIST vector: one million "a"', () => {
    expect(sha256HexSync(enc('a'.repeat(1_000_000)))).toBe(MILLION_A)
  })

  it.each([0, 1, 55, 56, 57, 63, 64, 65, 119, 120, 127, 128])(
    'matches WebCrypto at the padding boundary: %i bytes',
    async (len) => {
      const bytes = new Uint8Array(len)
      for (let i = 0; i < len; i++) bytes[i] = (i * 37 + 0x80) & 0xff
      expect(sha256HexSync(bytes)).toBe(await subtleHex(bytes))
    },
  )

  it('matches WebCrypto on 200 seeded random inputs (0–4096 bytes, full byte range)', async () => {
    const rand = mulberry32(0x5eed1234)
    let sawHighByte = false
    for (let n = 0; n < 200; n++) {
      const len = Math.floor(rand() * 4097)
      const bytes = new Uint8Array(len)
      for (let i = 0; i < len; i++) {
        bytes[i] = Math.floor(rand() * 256)
        if (bytes[i] >= 0x80) sawHighByte = true
      }
      expect(sha256HexSync(bytes), `case ${n} len ${len}`).toBe(await subtleHex(bytes))
    }
    expect(sawHighByte).toBe(true)
  })

  it('matches WebCrypto on multi-byte UTF-8 (CJK, emoji)', async () => {
    for (const s of ['繁體中文的工作區設定', '🦊🚀 emoji ✳ mixed 混合', '𠮷野家 — surrogate pairs 👩‍👩‍👧‍👦']) {
      const bytes = enc(s)
      expect(sha256HexSync(bytes)).toBe(await subtleHex(bytes))
    }
  })

  it('honours the view offset/length of a subarray', async () => {
    const backing = new Uint8Array(300).map((_, i) => (i * 7) & 0xff)
    const view = backing.subarray(13, 213)
    expect(sha256HexSync(view)).toBe(await subtleHex(view.slice()))
  })

  it('reports the 1 MB cost (informational)', () => {
    const bytes = new Uint8Array(1024 * 1024)
    const rand = mulberry32(42)
    for (let i = 0; i < bytes.length; i++) bytes[i] = Math.floor(rand() * 256)
    sha256HexSync(bytes.subarray(0, 4096)) // warm up
    const t0 = performance.now()
    const hex = sha256HexSync(bytes)
    const ms = performance.now() - t0
    console.log(`[crypto-hash] sha256HexSync 1 MiB: ${ms.toFixed(1)} ms`)
    expect(hex).toMatch(/^[0-9a-f]{64}$/)
  })
})

describe('sha256Hex fallback (non-secure context)', () => {
  afterEach(() => {
    vi.unstubAllGlobals()
  })

  it('falls back to pure JS when crypto.subtle is undefined', async () => {
    vi.stubGlobal('crypto', { subtle: undefined })
    expect(globalThis.crypto.subtle).toBeUndefined()
    expect(await sha256Hex(enc('abc'))).toBe(VECTORS[1][2])
  })

  it('falls back to pure JS when crypto itself is undefined', async () => {
    vi.stubGlobal('crypto', undefined)
    expect(await sha256Hex(enc('abc'))).toBe(VECTORS[1][2])
  })

  it('falls back when subtle.digest rejects', async () => {
    const digest = vi.fn().mockRejectedValue(new Error('OperationError'))
    vi.stubGlobal('crypto', { subtle: { digest } })
    expect(await sha256Hex(enc('abc'))).toBe(VECTORS[1][2])
    expect(digest).toHaveBeenCalledOnce()
  })

  it('falls back when subtle.digest throws synchronously', async () => {
    const digest = vi.fn(() => {
      throw new Error('NotSupportedError')
    })
    vi.stubGlobal('crypto', { subtle: { digest } })
    expect(await sha256Hex(enc('abc'))).toBe(VECTORS[1][2])
    expect(digest).toHaveBeenCalledOnce()
  })

  it('restores the real WebCrypto after the stubs', () => {
    expect(globalThis.crypto.subtle).toBe(realSubtle)
  })
})
