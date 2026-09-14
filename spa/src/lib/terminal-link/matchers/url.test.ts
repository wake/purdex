import { describe, it, expect } from 'vitest'
import { urlMatcher } from './url'

const texts = (line: string) => urlMatcher.provide(line).map((t) => t.text)

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

describe('full-width boundaries', () => {
  it('stops at a full-width comma glued to the URL', () => {
    expect(texts('https://e.com/a，然後回報')).toEqual(['https://e.com/a'])
  })

  it('does not include a trailing full-width stop', () => {
    expect(texts('https://e.com/a。')).toEqual(['https://e.com/a'])
  })

  it('does not include a trailing full-width paren', () => {
    expect(texts('（見 https://e.com/a）')).toEqual(['https://e.com/a'])
  })

  it('splits two URLs separated only by a full-width comma', () => {
    expect(texts('https://a.com，https://b.com')).toEqual(['https://a.com', 'https://b.com'])
  })
})

describe('non-ASCII cut (option C)', () => {
  it('keeps CJK right after a path separator', () => {
    expect(texts('https://zh.wikipedia.org/wiki/臺灣 島')).toEqual(['https://zh.wikipedia.org/wiki/臺灣'])
  })

  it('keeps CJK right after a query "="', () => {
    expect(texts('https://e.com/?q=測試')).toEqual(['https://e.com/?q=測試'])
  })

  it('keeps an IDN host with a dot between non-ASCII labels', () => {
    expect(texts('https://例子.測試/a')).toEqual(['https://例子.測試/a'])
  })

  it('cuts CJK after a "." that follows ASCII, then strips the dot', () => {
    expect(texts('https://e.com/a.然後')).toEqual(['https://e.com/a'])
  })

  it('cuts CJK glued directly after an ASCII path char', () => {
    expect(texts('參考https://e.com/x這頁')).toEqual(['https://e.com/x'])
  })

  it('reports the range up to the cut index', () => {
    const out = urlMatcher.provide('參考https://e.com/x這頁')
    expect(out).toHaveLength(1)
    expect(out[0].range).toEqual({ startCol: 2, endCol: 2 + 'https://e.com/x'.length })
  })

  it('cuts an emoji glued after ASCII', () => {
    expect(texts('https://e.com/x🎉')).toEqual(['https://e.com/x'])
  })

  it('keeps a whole CJK run after "/" even when prose is glued (accepted sacrifice)', () => {
    expect(texts('https://zh.wikipedia.org/wiki/臺灣是個島')).toEqual([
      'https://zh.wikipedia.org/wiki/臺灣是個島',
    ])
  })
})

describe('bracket balance', () => {
  it('keeps a balanced trailing ")"', () => {
    expect(texts('https://en.wikipedia.org/wiki/Foo_(bar) ok')).toEqual([
      'https://en.wikipedia.org/wiki/Foo_(bar)',
    ])
  })

  it('strips an unbalanced trailing ")"', () => {
    expect(texts('(see https://e.com/a)')).toEqual(['https://e.com/a'])
  })

  it('strips markdown link closer and trailing dot', () => {
    expect(texts('[x](https://e.com/a).')).toEqual(['https://e.com/a'])
  })

  it('strips ")." after the URL', () => {
    expect(texts('https://e.com/a).')).toEqual(['https://e.com/a'])
  })

  it('keeps a balanced trailing "]"', () => {
    expect(texts('https://e.com/a[1]')).toEqual(['https://e.com/a[1]'])
  })
})
