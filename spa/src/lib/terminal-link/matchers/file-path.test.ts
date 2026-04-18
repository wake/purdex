import { describe, it, expect } from 'vitest'
import { filePathMatcher } from './file-path'

describe('file-path matcher', () => {
  it('matches absolute Unix paths with extension', () => {
    const out = filePathMatcher.provide('error at /Users/x/a.ts now')
    expect(out).toHaveLength(1)
    expect(out[0].text).toBe('/Users/x/a.ts')
    expect(out[0].meta).toEqual({ path: '/Users/x/a.ts' })
  })

  it('captures line:col suffix into meta', () => {
    const out = filePathMatcher.provide('at /a/b.ts:12:3')
    expect(out).toHaveLength(1)
    expect(out[0].text).toBe('/a/b.ts:12:3')
    expect(out[0].meta).toEqual({ path: '/a/b.ts', line: 12, col: 3 })
  })

  it('captures line-only suffix', () => {
    const out = filePathMatcher.provide('see /a/b.md:42')
    expect(out[0].meta).toEqual({ path: '/a/b.md', line: 42 })
  })

  it('does not match paths without extension', () => {
    expect(filePathMatcher.provide('cd /usr/local/bin')).toEqual([])
  })

  it('does not match inside URLs', () => {
    expect(filePathMatcher.provide('https://x.com/a.md')).toEqual([])
  })

  it('does not match paths inside URL query/fragment', () => {
    expect(filePathMatcher.provide('open https://app.com/r?to=/Users/me/a.ts')).toEqual([])
    expect(filePathMatcher.provide('https://docs.com/g#/home/a.md')).toEqual([])
  })

  it('matches path after URL with whitespace separator', () => {
    const out = filePathMatcher.provide('see https://x.com then /home/a.md')
    expect(out).toHaveLength(1)
    expect(out[0].text).toBe('/home/a.md')
  })

  it('does not match dotdir segments as extensions', () => {
    expect(filePathMatcher.provide('cd /home/user/.config')).toEqual([])
    expect(filePathMatcher.provide('check /var/.cache')).toEqual([])
  })

  it('produces type "file"', () => {
    expect(filePathMatcher.type).toBe('file')
  })

  it('finishes quickly on long extensionless paths (no ReDoS)', () => {
    const line = '/' + Array(50).fill('segment').join('/')
    const start = performance.now()
    expect(filePathMatcher.provide(line)).toEqual([])
    expect(performance.now() - start).toBeLessThan(50)
  })
})
