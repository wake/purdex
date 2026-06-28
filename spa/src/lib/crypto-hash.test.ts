import { describe, it, expect } from 'vitest'
import { sha256Hex } from './crypto-hash'

const enc = (s: string) => new TextEncoder().encode(s)

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
