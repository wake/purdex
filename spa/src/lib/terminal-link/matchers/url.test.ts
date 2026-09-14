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

describe('full-width boundaries (step 1)', () => {
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

  it('stops at full-width square brackets', () => {
    expect(texts('https://e.com/［說明］')).toEqual(['https://e.com/'])
  })

  it('stops at half-width CJK punctuation ｡｢｣､', () => {
    expect(texts('https://e.com/a｡')).toEqual(['https://e.com/a'])
    expect(texts('https://e.com/a｢x｣')).toEqual(['https://e.com/a'])
    expect(texts('https://e.com/a､b')).toEqual(['https://e.com/a'])
  })

  it('treats "。" as a stop even inside an IDN-like host (documented trade-off)', () => {
    expect(texts('https://例子。測試/a')).toEqual(['https://例子'])
  })

  it('keeps the katakana middle dot "・" as URL content', () => {
    expect(texts('https://e.com/wiki/アラン・チューリング')).toEqual([
      'https://e.com/wiki/アラン・チューリング',
    ])
  })
})

describe('non-ASCII cut (step 2, option C: cut only after ASCII alnum)', () => {
  it('keeps CJK right after a path separator', () => {
    expect(texts('https://zh.wikipedia.org/wiki/臺灣 島')).toEqual(['https://zh.wikipedia.org/wiki/臺灣'])
  })

  it('keeps CJK right after a query "="', () => {
    expect(texts('https://e.com/?q=測試')).toEqual(['https://e.com/?q=測試'])
  })

  it('keeps an IDN host with a dot between non-ASCII labels', () => {
    expect(texts('https://例子.測試/a')).toEqual(['https://例子.測試/a'])
  })

  it('keeps an IDN label after an ASCII label (www.例子.com)', () => {
    expect(texts('https://www.例子.com/a')).toEqual(['https://www.例子.com/a'])
  })

  it('keeps ASCII digit after non-ASCII and CJK after "." (例子1.測試)', () => {
    expect(texts('https://例子1.測試/a')).toEqual(['https://例子1.測試/a'])
  })

  it('keeps CJK after "_"', () => {
    expect(texts('https://e.com/wiki/ISO_標準')).toEqual(['https://e.com/wiki/ISO_標準'])
  })

  it('keeps an emoji after "-"', () => {
    expect(texts('https://e.com/release-🎉')).toEqual(['https://e.com/release-🎉'])
  })

  it('cuts CJK glued directly after an ASCII letter in the path', () => {
    expect(texts('參考https://e.com/x這頁')).toEqual(['https://e.com/x'])
  })

  it('cuts CJK glued directly after the host', () => {
    expect(texts('詳見https://e.com這頁')).toEqual(['https://e.com'])
  })

  it('cuts an emoji glued after an ASCII letter', () => {
    expect(texts('https://e.com/x🎉')).toEqual(['https://e.com/x'])
  })

  it('cuts CJK after alnum inside a query value (accepted sacrifice)', () => {
    expect(texts('https://e.com/?q=abc中文')).toEqual(['https://e.com/?q=abc'])
  })

  it('keeps CJK prose right after "?" (accepted false keep)', () => {
    expect(texts('https://e.com/?然後回報')).toEqual(['https://e.com/?然後回報'])
  })

  it('keeps CJK prose after an ASCII "." (accepted false keep)', () => {
    expect(texts('https://e.com/a.然後')).toEqual(['https://e.com/a.然後'])
  })

  it('keeps a whole CJK run after "/" even when prose is glued (accepted false keep)', () => {
    expect(texts('https://zh.wikipedia.org/wiki/臺灣是個島')).toEqual([
      'https://zh.wikipedia.org/wiki/臺灣是個島',
    ])
  })

  it('reports the range up to the cut index', () => {
    const out = urlMatcher.provide('參考https://e.com/x這頁')
    expect(out).toHaveLength(1)
    expect(out[0].range).toEqual({ startCol: 2, endCol: 2 + 'https://e.com/x'.length })
  })

  it('reports UTF-16 offsets when a non-BMP prefix precedes the URL', () => {
    const out = urlMatcher.provide('😀https://e.com/臺灣')
    expect(out).toHaveLength(1)
    expect(out[0].text).toBe('https://e.com/臺灣')
    expect(out[0].range).toEqual({ startCol: 2, endCol: 18 })
  })
})

describe('bracket balance (step 3)', () => {
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

  it('strips to balance then stops (a_(b)).)', () => {
    expect(texts('https://e.com/a_(b)).')).toEqual(['https://e.com/a_(b)'])
  })

  it('keeps a balanced trailing "]"', () => {
    expect(texts('https://e.com/a[1]')).toEqual(['https://e.com/a[1]'])
  })

  it('strips an unbalanced trailing "]" only', () => {
    expect(texts('https://e.com/a[1]]')).toEqual(['https://e.com/a[1]'])
  })

  it('keeps an IPv6 literal host', () => {
    expect(texts('https://[::1]')).toEqual(['https://[::1]'])
  })

  it('strips a 300-char ")" tail', () => {
    expect(texts('https://e.com/a' + ')'.repeat(300))).toEqual(['https://e.com/a'])
  })
})

describe('scheme-only guard', () => {
  it('emits nothing when strip leaves only the scheme', () => {
    expect(texts('https://).')).toEqual([])
  })
})

describe('stop set, one character at a time (step 1)', () => {
  // Literal copy of the full-width / CJK stop set in url.ts (minus \s"'<>`),
  // so a dropped character from the constant fails one named row here.
  const FULL_WIDTH_STOPS = [...'，。、；：！？（）［］「」『』【】《》〈〉〔〕｛｝“”‘’…｡｢｣､']

  it.each(
    FULL_WIDTH_STOPS.map((c) => [c, `U+${c.codePointAt(0)!.toString(16).toUpperCase().padStart(4, '0')}`]),
  )('stops at %s (%s)', (c) => {
    expect(texts(`https://e.com/a${c}b`)).toEqual(['https://e.com/a'])
  })
})

describe('Latin / combining-mark exemption from the cut (step 2)', () => {
  it('keeps an accented Latin IDN host (bücher.example)', () => {
    expect(texts('請見 https://bücher.example/catalog')).toEqual(['https://bücher.example/catalog'])
  })

  it('keeps an NFC accented path segment with the right range', () => {
    const line = 'README.md:12: https://example.com/café/menu'
    const out = urlMatcher.provide(line)
    expect(out).toHaveLength(1)
    expect(out[0].text).toBe('https://example.com/café/menu')
    expect(out[0].range).toEqual({ startCol: 14, endCol: 14 + 'https://example.com/café/menu'.length })
  })

  it('keeps an NFD accented path segment (combining mark after ASCII letter)', () => {
    const url = 'https://example.com/café/menu'
    const out = urlMatcher.provide(`${url} x`)
    expect(out).toHaveLength(1)
    expect(out[0].text).toBe(url)
  })

  it('still cuts Cyrillic glued after ASCII (accepted)', () => {
    expect(texts('https://e.com/abcдом')).toEqual(['https://e.com/abc'])
  })
})

describe('rescan after a cut (step 2)', () => {
  it('finds a second URL swallowed by the greedy scan', () => {
    const out = urlMatcher.provide('參考https://a.com/x或https://b.com/y')
    expect(out.map((t) => t.text)).toEqual(['https://a.com/x', 'https://b.com/y'])
    expect(out[0].range).toEqual({ startCol: 2, endCol: 17 })
    expect(out[1].range).toEqual({ startCol: 18, endCol: 33 })
  })

  it('finds every URL in a three-URL chain', () => {
    expect(texts('參考https://a.com/x或https://b.com/y再看https://c.com/z')).toEqual([
      'https://a.com/x',
      'https://b.com/y',
      'https://c.com/z',
    ])
  })

  it('returns identical results when provide is called twice on the same line', () => {
    const line = '參考https://a.com/x或https://b.com/y'
    const first = urlMatcher.provide(line)
    const second = urlMatcher.provide(line)
    expect(second).toEqual(first)
    expect(second).toHaveLength(2)
  })
})
