import { describe, it, expect } from 'vitest'
import { isQuotaError } from './quota'

// isQuotaError is the single quota detector shared by the Sync snapshot store
// and the Storage upload path (T1c-4). These cases pin every signal it accepts:
// the standard DOMException name plus the historic numeric codes, and the
// negatives that must NOT be mistaken for a quota exhaustion.
describe('isQuotaError', () => {
  it('true for a DOMException named QuotaExceededError', () => {
    expect(isQuotaError(new DOMException('full', 'QuotaExceededError'))).toBe(true)
  })

  it('true for the standard numeric code 22', () => {
    const err = new DOMException('full', 'SomeOtherName')
    Object.defineProperty(err, 'code', { value: 22 })
    expect(isQuotaError(err)).toBe(true)
  })

  it('true for the Firefox legacy code 1014', () => {
    const err = new DOMException('full', 'SomeOtherName')
    Object.defineProperty(err, 'code', { value: 1014 })
    expect(isQuotaError(err)).toBe(true)
  })

  it('false for a plain Error', () => {
    expect(isQuotaError(new Error('QuotaExceededError'))).toBe(false)
  })

  it('false for a non-DOMException value', () => {
    expect(isQuotaError({ name: 'QuotaExceededError', code: 22 })).toBe(false)
    expect(isQuotaError(null)).toBe(false)
    expect(isQuotaError('QuotaExceededError')).toBe(false)
  })

  it('false for an unrelated DOMException (wrong name + code)', () => {
    expect(isQuotaError(new DOMException('nope', 'AbortError'))).toBe(false)
  })
})
