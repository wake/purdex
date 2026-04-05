import { describe, it, expect } from 'vitest'
import { decodePairingCode, generatePurdexToken, cleanPairingInput } from './pairing-codec'

describe('cleanPairingInput', () => {
  it('strips dashes, slashes, and spaces', () => {
    expect(cleanPairingInput('ABCD-EFG-HIJKL')).toBe('ABCDEFGHIJKL')
    expect(cleanPairingInput('AB CD / EF')).toBe('ABCDEF')
  })
})

describe('decodePairingCode', () => {
  it('returns null for invalid input', () => {
    expect(decodePairingCode('invalid!')).toBeNull()
    expect(decodePairingCode('')).toBeNull()
    expect(decodePairingCode('AB')).toBeNull()
  })

  it('handles clean 13-char input', () => {
    // all zeros = "1111111111111"
    const result = decodePairingCode('1111111111111')
    expect(result).not.toBeNull()
    expect(result!.ip).toBe('0.0.0.0')
    expect(result!.port).toBe(0)
    expect(result!.secret).toBe('000000')
  })

  it('decodes all-zeros correctly', () => {
    // 13 '1's in Base58 = all zeros (9 bytes of zeros)
    const result = decodePairingCode('1111111111111')
    expect(result).not.toBeNull()
    expect(result!.ip).toBe('0.0.0.0')
    expect(result!.port).toBe(0)
    expect(result!.secret).toBe('000000')
  })

  it('strips formatting before decoding', () => {
    const r1 = decodePairingCode('1111-1111-11111')
    const r2 = decodePairingCode('1111111111111')
    expect(r1).toEqual(r2)
  })
})

describe('generatePurdexToken', () => {
  it('returns purdex_ prefix + 40 hex chars', () => {
    const token = generatePurdexToken()
    expect(token).toMatch(/^purdex_[0-9a-f]{40}$/)
    expect(token.length).toBe(47)
  })

  it('generates unique tokens', () => {
    const t1 = generatePurdexToken()
    const t2 = generatePurdexToken()
    expect(t1).not.toBe(t2)
  })
})
