import { describe, it, expect, beforeEach } from 'vitest'
import {
  absoluteFilePathMatcher,
  relativeSlashFilePathMatcher,
  bareFilenameFilePathMatcher,
} from './file-path'
import { useUISettingsStore } from '../../../stores/useUISettingsStore'

function setFlags(opts: { abs?: boolean; rel?: boolean; bare?: boolean }) {
  useUISettingsStore.setState({
    linkDetectAbsolute: opts.abs ?? false,
    linkDetectRelativeSlash: opts.rel ?? false,
    linkDetectBareFilename: opts.bare ?? false,
  })
}

describe('absoluteFilePathMatcher', () => {
  beforeEach(() => setFlags({ abs: true }))

  it('matches /path/to/file.md', () => {
    const r = absoluteFilePathMatcher.provide('see /a/b/c.md here')
    expect(r).toHaveLength(1)
    expect(r[0].text).toBe('/a/b/c.md')
    expect(r[0].meta).toEqual({ path: '/a/b/c.md' })
  })

  it('captures line/col suffix', () => {
    const r = absoluteFilePathMatcher.provide('at /x/y.ts:12:3!')
    expect(r[0].meta).toEqual({ path: '/x/y.ts', line: 12, col: 3 })
  })

  it('skips dotdir like /home/u/.config', () => {
    expect(absoluteFilePathMatcher.provide('go /home/u/.config')).toHaveLength(0)
  })

  it('skips path inside URL', () => {
    expect(absoluteFilePathMatcher.provide('https://a.com/b.md')).toHaveLength(0)
  })

  it('returns [] when absolute flag off', () => {
    setFlags({ abs: false })
    expect(absoluteFilePathMatcher.provide('/a/b.md')).toHaveLength(0)
  })
})

describe('relativeSlashFilePathMatcher', () => {
  beforeEach(() => setFlags({ rel: true }))

  it('matches src/App.tsx', () => {
    const r = relativeSlashFilePathMatcher.provide('edit src/App.tsx now')
    expect(r).toHaveLength(1)
    expect(r[0].text).toBe('src/App.tsx')
    expect(r[0].meta).toEqual({ path: 'src/App.tsx' })
  })

  it('matches internal/agent/cc/extract.go:14', () => {
    const r = relativeSlashFilePathMatcher.provide('internal/agent/cc/extract.go:14')
    expect(r[0].meta).toEqual({ path: 'internal/agent/cc/extract.go', line: 14 })
  })

  it('does NOT match absolute path', () => {
    expect(relativeSlashFilePathMatcher.provide('/abs/x.md')).toHaveLength(0)
  })

  it('does NOT match bare filename', () => {
    expect(relativeSlashFilePathMatcher.provide('x.md')).toHaveLength(0)
  })

  it('skips URL-internal segments', () => {
    expect(relativeSlashFilePathMatcher.provide('https://a.com/b/c.md')).toHaveLength(0)
  })

  it('returns [] when flag off', () => {
    setFlags({ rel: false })
    expect(relativeSlashFilePathMatcher.provide('src/App.tsx')).toHaveLength(0)
  })
})

describe('bareFilenameFilePathMatcher', () => {
  beforeEach(() => setFlags({ bare: true }))

  it('matches bare package.json', () => {
    const r = bareFilenameFilePathMatcher.provide('see package.json')
    expect(r).toHaveLength(1)
    expect(r[0].text).toBe('package.json')
    expect(r[0].meta).toEqual({ path: 'package.json' })
  })

  it('does NOT match segments inside a/b.md', () => {
    const r = bareFilenameFilePathMatcher.provide('a/b.md')
    expect(r).toHaveLength(0)
  })

  it('does NOT match absolute /a/b.md', () => {
    expect(bareFilenameFilePathMatcher.provide('/a/b.md')).toHaveLength(0)
  })

  it('captures line/col for bare name', () => {
    const r = bareFilenameFilePathMatcher.provide('see foo.ts:5:2')
    expect(r[0].meta).toEqual({ path: 'foo.ts', line: 5, col: 2 })
  })

  it('returns [] when flag off', () => {
    setFlags({ bare: false })
    expect(bareFilenameFilePathMatcher.provide('foo.md')).toHaveLength(0)
  })
})
