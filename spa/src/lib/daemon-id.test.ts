import { describe, expect, it } from 'vitest'
import { DAEMON_ID_MAX_LENGTH, isValidDaemonId } from './daemon-id'

describe('isValidDaemonId — what a daemon `host_id` can be (internal/config/hostid.go)', () => {
  it('accepts real ids: <lowercased short hostname>:<base36 code>, the "unknown" fallback included', () => {
    for (const id of ['mini-lab:278cbm', 'unknown:abc123', 'air-2026:0z9y8x', 'box_1.lan:a', 'c5n:zzzzzz']) {
      expect(isValidDaemonId(id), id).toBe(true)
    }
  })

  it('accepts exactly up to the maximum length', () => {
    const at = `${'a'.repeat(DAEMON_ID_MAX_LENGTH - 7)}:abc123`
    expect(at).toHaveLength(DAEMON_ID_MAX_LENGTH)
    expect(isValidDaemonId(at)).toBe(true)
    expect(isValidDaemonId(`a${at}`)).toBe(false)
  })

  it('rejects the empty string and non-strings', () => {
    for (const v of ['', undefined, null, 5, {}, ['mini:abc123']]) expect(isValidDaemonId(v), String(v)).toBe(false)
  })

  it('rejects control characters, whitespace and Unicode controls anywhere', () => {
    for (const id of [
      'mini:abc123\n', 'mini\n:abc123', 'mini:abc\u0000123', 'mini:abc 123', ' mini:abc123', 'mini:abc123\t',
      'mini‮:abc123', 'mini:abc123‏', 'mini⁦:abc', 'mini:abc ', 'mini:abc​',
    ]) {
      expect(isValidDaemonId(id), JSON.stringify(id)).toBe(false)
    }
  })

  it('rejects anything but <label>:<code> — no colon, several colons, an empty half, upper case, other punctuation, non-ASCII', () => {
    for (const id of ['mini-lab', ':abc123', 'mini:', 'mini:ab:c', 'Mini:abc123', 'mini:ABC123', 'mini:abc-123', 'mi/ni:abc', 'mini:<b>', 'mïni:abc123']) {
      expect(isValidDaemonId(id), id).toBe(false)
    }
  })
})
