import { describe, expect, it } from 'vitest'
import { DAEMON_ID_MAX_LENGTH, isValidDaemonId } from './daemon-id'

describe('isValidDaemonId — any `host_id` a daemon can report (internal/config/hostid.go), minus what is unsafe to store or render', () => {
  it('accepts generated ids: <lowercased short hostname>:<base36 code>, the "unknown" fallback included', () => {
    for (const id of ['mini-lab:278cbm', 'unknown:abc123', 'air-2026:0z9y8x', 'c5n:zzzzzz']) {
      expect(isValidDaemonId(id), id).toBe(true)
    }
  })

  it('accepts what EnsureHostID keeps as is or never normalises: upper case, extra colons, spaces, Unicode', () => {
    for (const id of ['Mini-Lab:278cbm', 'a:b:c', 'host name:abc123', 'münchen:abc123', 'no-colon', '主機:abc123']) {
      expect(isValidDaemonId(id), id).toBe(true)
    }
  })

  it('accepts up to 512 UTF-16 code units, not one more', () => {
    expect(DAEMON_ID_MAX_LENGTH).toBe(512)
    const at = `${'a'.repeat(505)}:abc123`
    expect(at).toHaveLength(512)
    expect(isValidDaemonId(at)).toBe(true)
    expect(isValidDaemonId(`a${at}`)).toBe(false)
  })

  it('rejects the empty string and non-strings', () => {
    for (const v of ['', undefined, null, 5, {}, ['mini:abc123']]) expect(isValidDaemonId(v), String(v)).toBe(false)
  })

  it('rejects control (Cc) and format (Cf) characters anywhere: newline, NUL, tab, C1, bidi overrides / isolates, zero-width', () => {
    for (const id of [
      'mini:abc123\n', 'mini:abc\u0000123', 'mini:abc123\t', 'mini:abc\u0085', 'mini\u007f:abc',
      'mini\u202e:abc123', 'mini\u2066:abc', 'mini:abc123\u200f', 'mini:abc\u200b', 'mini:abc\ufeff',
    ]) {
      expect(isValidDaemonId(id), JSON.stringify(id)).toBe(false)
    }
  })

  it('rejects line / paragraph separators (Zl, Zp) — they break a log line or the UI like a newline', () => {
    for (const id of ['mini :abc123', 'mini:abc123 ']) expect(isValidDaemonId(id), JSON.stringify(id)).toBe(false)
  })

  it('rejects lone surrogates (Cs) — they render as U+FFFD, so two different ids would look the same', () => {
    for (const id of ['\ud800', 'x\udc00y', 'mini:abc\ud83d', 'mini\ude00:abc']) expect(isValidDaemonId(id), JSON.stringify(id)).toBe(false)
  })

  it('accepts a proper surrogate pair (astral characters)', () => {
    for (const id of ['host-😀:abc123', '𠀀:abc123']) {
      expect(id.length - [...id].length).toBe(1) // exactly one pair: two code units, one code point
      expect(isValidDaemonId(id), JSON.stringify(id)).toBe(true)
    }
  })
})
