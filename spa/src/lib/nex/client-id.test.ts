import { describe, it, expect, beforeEach, vi, afterEach } from 'vitest'
import { getNexClientId, NEX_CLIENT_ID_RE, resetNexClientIdForTests } from './client-id'

describe('getNexClientId', () => {
  beforeEach(() => {
    sessionStorage.clear()
    resetNexClientIdForTests()
  })
  afterEach(() => vi.unstubAllGlobals())

  it('returns a value matching the daemon pattern and stores it in sessionStorage', () => {
    const id = getNexClientId()
    expect(id).toMatch(NEX_CLIENT_ID_RE)
    expect(id).toMatch(/^t-[0-9a-z]{8}$/)
    expect(sessionStorage.getItem('purdex-nex-client-id')).toBe(id)
  })

  it('is stable across calls and reuses a stored value', () => {
    sessionStorage.setItem('purdex-nex-client-id', 't-abcdefgh')
    expect(getNexClientId()).toBe('t-abcdefgh')
    expect(getNexClientId()).toBe('t-abcdefgh')
  })

  it('ignores a stored value that does not match the pattern', () => {
    sessionStorage.setItem('purdex-nex-client-id', 'bad value!')
    const id = getNexClientId()
    expect(id).not.toBe('bad value!')
    expect(id).toMatch(NEX_CLIENT_ID_RE)
  })

  it('falls back to an in-memory id when sessionStorage throws', () => {
    vi.stubGlobal('sessionStorage', {
      getItem: () => { throw new Error('denied') },
      setItem: () => { throw new Error('denied') },
    })
    const a = getNexClientId()
    const b = getNexClientId()
    expect(a).toBe(b)
    expect(a).toMatch(NEX_CLIENT_ID_RE)
  })
})
