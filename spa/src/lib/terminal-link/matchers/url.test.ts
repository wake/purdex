import { describe, it, expect } from 'vitest'
import { urlMatcher } from './url'

describe('url matcher', () => {
  it('matches https URLs', () => {
    const out = urlMatcher.provide('visit https://example.com for info')
    expect(out).toHaveLength(1)
    expect(out[0].text).toBe('https://example.com')
    expect(out[0].range).toEqual({ startCol: 6, endCol: 25 })
  })

  it('matches http URLs', () => {
    const out = urlMatcher.provide('http://a.b/c?x=1')
    expect(out).toHaveLength(1)
    expect(out[0].text).toBe('http://a.b/c?x=1')
  })

  it('matches multiple URLs on one line', () => {
    const out = urlMatcher.provide('a https://x.com and https://y.com z')
    expect(out.map((t) => t.text)).toEqual(['https://x.com', 'https://y.com'])
  })

  it('strips trailing punctuation', () => {
    const out = urlMatcher.provide('see https://example.com.')
    expect(out[0].text).toBe('https://example.com')
  })

  it('does not match non-URL text', () => {
    expect(urlMatcher.provide('just text, no url here')).toEqual([])
  })

  it('produces type "url"', () => {
    expect(urlMatcher.type).toBe('url')
  })
})
