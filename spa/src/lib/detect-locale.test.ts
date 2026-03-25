import { describe, it, expect } from 'vitest'
import { detectLocale } from './detect-locale'

describe('detectLocale', () => {
  it('returns exact match', () => {
    expect(detectLocale(['zh-TW', 'en'], ['en', 'zh-TW'])).toBe('zh-TW')
  })
  it('returns prefix match when no exact', () => {
    expect(detectLocale(['en-US', 'ja'], ['en', 'zh-TW'])).toBe('en')
  })
  it('falls back to en when no match', () => {
    expect(detectLocale(['ja', 'ko'], ['en', 'zh-TW'])).toBe('en')
  })
  it('falls back to en for empty browser languages', () => {
    expect(detectLocale([], ['en', 'zh-TW'])).toBe('en')
  })
  it('prefers earlier browser language', () => {
    expect(detectLocale(['en', 'zh-TW'], ['en', 'zh-TW'])).toBe('en')
  })
})
