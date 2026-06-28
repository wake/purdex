import { describe, it, expect } from 'vitest'
import { wordCountFor, isWordCountable } from './text-metrics'

const enc = (s: string) => new TextEncoder().encode(s)

describe('text-metrics — shared word-count util (extracted from StorageRow)', () => {
  it('counts words for an allowlisted text extension (.txt)', () => {
    expect(wordCountFor('/buffer/note.txt', enc('one two three'))).toBe(3)
  })

  it('counts words for .md / .env / .gitignore dotfiles', () => {
    expect(wordCountFor('/buffer/a.md', enc('alpha beta'))).toBe(2)
    expect(wordCountFor('/buffer/.env', enc('FOO=1 BAR=2'))).toBe(2)
    expect(wordCountFor('/buffer/.gitignore', enc('node_modules\ndist'))).toBe(2)
  })

  it('returns 0 for a binary / non-allowlisted extension', () => {
    expect(wordCountFor('/buffer/img.png', enc('not really counted'))).toBe(0)
    expect(wordCountFor('/buffer/blob.bin', new Uint8Array([0xff, 0x00, 0xfe]))).toBe(0)
  })

  it('returns 0 for a text file over the 256 KB cap', () => {
    const big = new Uint8Array(256 * 1024 + 1)
    big.fill(0x61) // 'a' repeated — one giant "word" if counted
    expect(wordCountFor('/buffer/huge.txt', big)).toBe(0)
  })

  it('ignores leading/trailing/multiple whitespace (split filter)', () => {
    expect(wordCountFor('/buffer/x.txt', enc('  a   b\n\tc  '))).toBe(3)
    expect(wordCountFor('/buffer/empty.txt', enc('   '))).toBe(0)
  })

  it('isWordCountable gates on allowlist + size cap (the StorageRow read gate)', () => {
    expect(isWordCountable('/buffer/a.txt', 10)).toBe(true)
    expect(isWordCountable('/buffer/a.png', 10)).toBe(false)
    expect(isWordCountable('/buffer/a.txt', 256 * 1024 + 1)).toBe(false)
  })
})
