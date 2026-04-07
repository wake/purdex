import { describe, it, expect } from 'vitest'
import { normalizeUrl } from '../url-utils'

describe('normalizeUrl', () => {
  it('returns valid https URL as-is', () => {
    expect(normalizeUrl('https://github.com')).toBe('https://github.com/')
  })

  it('returns valid http URL as-is', () => {
    expect(normalizeUrl('http://localhost:3000')).toBe('http://localhost:3000/')
  })

  it('prepends https:// when no scheme', () => {
    expect(normalizeUrl('github.com')).toBe('https://github.com/')
  })

  it('prepends https:// for domain with path', () => {
    expect(normalizeUrl('github.com/wake/tmux-box')).toBe('https://github.com/wake/tmux-box')
  })

  it('returns null for javascript: scheme', () => {
    expect(normalizeUrl('javascript:alert(1)')).toBeNull()
  })

  it('returns null for file: scheme', () => {
    expect(normalizeUrl('file:///etc/passwd')).toBeNull()
  })

  it('returns null for ftp: scheme', () => {
    expect(normalizeUrl('ftp://example.com')).toBeNull()
  })

  it('returns null for empty string', () => {
    expect(normalizeUrl('')).toBeNull()
  })

  it('returns null for whitespace only', () => {
    expect(normalizeUrl('   ')).toBeNull()
  })

  it('trims whitespace', () => {
    expect(normalizeUrl('  https://github.com  ')).toBe('https://github.com/')
  })

  it('returns null for malformed URL', () => {
    expect(normalizeUrl('not a url at all')).toBeNull()
  })
})
