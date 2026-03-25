import { describe, it, expect } from 'vitest'
import { parseAndValidateLocale } from './locale-import'

describe('parseAndValidateLocale', () => {
  it('accepts valid locale JSON', () => {
    const result = parseAndValidateLocale({
      name: 'Test',
      translations: { 'common.save': 'Save' },
    })
    expect(typeof result).not.toBe('string')
    if (typeof result !== 'string') {
      expect(result.name).toBe('Test')
      expect(result.translations['common.save']).toBe('Save')
    }
  })
  it('rejects non-object', () => {
    expect(parseAndValidateLocale(null)).toBe('Invalid JSON')
    expect(parseAndValidateLocale('str')).toBe('Invalid JSON')
  })
  it('rejects missing name', () => {
    expect(parseAndValidateLocale({ translations: {} })).toContain('name')
  })
  it('rejects empty name', () => {
    expect(parseAndValidateLocale({ name: '  ', translations: {} })).toContain('name')
  })
  it('rejects missing translations', () => {
    expect(parseAndValidateLocale({ name: 'X' })).toContain('translations')
  })
  it('returns error when all values are non-string', () => {
    expect(parseAndValidateLocale({
      name: 'X',
      translations: { key: 123 },
    })).toBe('No valid translation keys found')
  })
  it('filters out non-string values, keeps valid ones', () => {
    const result = parseAndValidateLocale({
      name: 'X',
      translations: { good: 'yes', bad: 42 },
    })
    if (typeof result !== 'string') {
      expect(result.translations).toEqual({ good: 'yes' })
    }
  })
  it('rejects when no valid keys remain', () => {
    expect(parseAndValidateLocale({
      name: 'X',
      translations: { a: 1, b: false },
    })).toContain('No valid')
  })
  it('preserves optional baseLocale field', () => {
    const result = parseAndValidateLocale({
      name: 'X',
      baseLocale: 'zh-TW',
      translations: { a: 'b' },
    })
    if (typeof result !== 'string') {
      expect(result.baseLocale).toBe('zh-TW')
    }
  })
})
