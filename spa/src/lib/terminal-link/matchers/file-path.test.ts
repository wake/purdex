import { describe, it, expect } from 'vitest'
import { createFilePathMatcher, ABS_RE, REL_RE, BARE_RE, TILDE_RE } from './file-path'

describe('createFilePathMatcher — absolute', () => {
  const make = (isEnabled = true) =>
    createFilePathMatcher({ id: 'test-abs', regex: ABS_RE, isEnabled: () => isEnabled })

  it('matches /path/to/file.md', () => {
    const r = make().provide('see /a/b/c.md here')
    expect(r).toHaveLength(1)
    expect(r[0].text).toBe('/a/b/c.md')
    expect(r[0].meta).toEqual({ path: '/a/b/c.md' })
  })

  it('captures line/col suffix', () => {
    const r = make().provide('at /x/y.ts:12:3!')
    expect(r[0].meta).toEqual({ path: '/x/y.ts', line: 12, col: 3 })
  })

  it('skips dotdir like /home/u/.config', () => {
    expect(make().provide('go /home/u/.config')).toHaveLength(0)
  })

  it('skips path inside URL', () => {
    expect(make().provide('https://a.com/b.md')).toHaveLength(0)
  })

  it('returns [] when flag off', () => {
    expect(make(false).provide('/a/b.md')).toHaveLength(0)
  })

  it('matches multi-extension path like /x/foo.d.ts', () => {
    const r = make().provide('open /x/foo.d.ts')
    expect(r).toHaveLength(1)
    expect(r[0].text).toBe('/x/foo.d.ts')
    expect(r[0].meta).toEqual({ path: '/x/foo.d.ts' })
  })

  it('multi-ext with line suffix: /x/bar.min.js:42', () => {
    const r = make().provide('at /x/bar.min.js:42 here')
    expect(r[0].meta).toEqual({ path: '/x/bar.min.js', line: 42 })
  })

  it('does NOT match /path/to/1.2.3 (all-digit extensions)', () => {
    expect(make().provide('see /path/1.2.3 dir')).toHaveLength(0)
  })

  it('does NOT pick /foo.ts out of ~/foo.ts', () => {
    expect(make().provide('open ~/foo.ts')).toHaveLength(0)
  })

  it('does NOT pick /CLAUDE.md out of ./CLAUDE.md', () => {
    expect(make().provide('open ./CLAUDE.md')).toHaveLength(0)
  })

  it('matches hyphenated dotted segment /a/b/foo.pre-edit.md', () => {
    const r = make().provide('see /a/b/foo.pre-edit.md here')
    expect(r).toHaveLength(1)
    expect(r[0].text).toBe('/a/b/foo.pre-edit.md')
    expect(r[0].meta).toEqual({ path: '/a/b/foo.pre-edit.md' })
  })

  it('keeps + build-metadata in dotted segment (custom-css tarball)', () => {
    const path =
      '/Users/wake/Workspace/wake/mattermost-custom-css-plugin/dist/com.wake.custom-css-0.0.0+075a408.tar.gz'
    const r = make().provide(`built ${path} ok`)
    expect(r).toHaveLength(1)
    expect(r[0].text).toBe(path)
    expect(r[0].meta).toEqual({ path })
  })
})

describe('createFilePathMatcher — relativeSlash', () => {
  const make = (isEnabled = true) =>
    createFilePathMatcher({ id: 'test-rel', regex: REL_RE, isEnabled: () => isEnabled })

  it('matches src/App.tsx', () => {
    const r = make().provide('edit src/App.tsx now')
    expect(r).toHaveLength(1)
    expect(r[0].text).toBe('src/App.tsx')
    expect(r[0].meta).toEqual({ path: 'src/App.tsx' })
  })

  it('matches ./CLAUDE.md', () => {
    const r = make().provide('edit ./CLAUDE.md now')
    expect(r).toHaveLength(1)
    expect(r[0].text).toBe('./CLAUDE.md')
    expect(r[0].meta).toEqual({ path: './CLAUDE.md' })
  })

  it('matches internal/agent/cc/extract.go:14', () => {
    const r = make().provide('internal/agent/cc/extract.go:14')
    expect(r[0].meta).toEqual({ path: 'internal/agent/cc/extract.go', line: 14 })
  })

  it('does NOT match absolute path', () => {
    expect(make().provide('/abs/x.md')).toHaveLength(0)
  })

  it('does NOT match bare filename', () => {
    expect(make().provide('x.md')).toHaveLength(0)
  })

  it('skips URL-internal segments', () => {
    expect(make().provide('https://a.com/b/c.md')).toHaveLength(0)
  })

  it('returns [] when flag off', () => {
    expect(make(false).provide('src/App.tsx')).toHaveLength(0)
  })

  it('matches multi-extension relative path src/a/foo.d.ts', () => {
    const r = make().provide('edit src/a/foo.d.ts')
    expect(r[0].text).toBe('src/a/foo.d.ts')
    expect(r[0].meta).toEqual({ path: 'src/a/foo.d.ts' })
  })

  it('does NOT match dir/1.2.3 (all-digit extensions)', () => {
    expect(make().provide('cd dir/1.2.3')).toHaveLength(0)
  })

  it('matches hyphenated dotted segment docs/souls/morphy.pre-edit.SOUL.md', () => {
    const r = make().provide('edit docs/souls/morphy.pre-edit.SOUL.md ok')
    expect(r).toHaveLength(1)
    expect(r[0].text).toBe('docs/souls/morphy.pre-edit.SOUL.md')
    expect(r[0].meta).toEqual({ path: 'docs/souls/morphy.pre-edit.SOUL.md' })
  })

  it('matches hyphenated dotted segment with line:col', () => {
    const r = make().provide('internal/x/morphy.pre-edit.SOUL.md:12:3')
    expect(r[0].meta).toEqual({ path: 'internal/x/morphy.pre-edit.SOUL.md', line: 12, col: 3 })
  })
})

describe('createFilePathMatcher — bare', () => {
  const make = (isEnabled = true) =>
    createFilePathMatcher({ id: 'test-bare', regex: BARE_RE, isEnabled: () => isEnabled })

  it('matches bare package.json', () => {
    const r = make().provide('see package.json')
    expect(r).toHaveLength(1)
    expect(r[0].text).toBe('package.json')
    expect(r[0].meta).toEqual({ path: 'package.json' })
  })

  it('does NOT match segments inside a/b.md', () => {
    expect(make().provide('a/b.md')).toHaveLength(0)
  })

  it('does NOT match absolute /a/b.md', () => {
    expect(make().provide('/a/b.md')).toHaveLength(0)
  })

  it('captures line/col for bare name', () => {
    const r = make().provide('see foo.ts:5:2')
    expect(r[0].meta).toEqual({ path: 'foo.ts', line: 5, col: 2 })
  })

  it('returns [] when flag off', () => {
    expect(make(false).provide('foo.md')).toHaveLength(0)
  })

  it('matches multi-extension bare name foo.d.ts', () => {
    const r = make().provide('see foo.d.ts')
    expect(r[0].text).toBe('foo.d.ts')
    expect(r[0].meta).toEqual({ path: 'foo.d.ts' })
  })

  it('matches v1.2.3.tar.gz:10', () => {
    const r = make().provide('got v1.2.3.tar.gz:10')
    expect(r[0].text).toBe('v1.2.3.tar.gz:10')
    expect(r[0].meta).toEqual({ path: 'v1.2.3.tar.gz', line: 10 })
  })

  it('does NOT match IP address 192.168.1.1', () => {
    expect(make().provide('ping 192.168.1.1 response')).toHaveLength(0)
  })

  it('does NOT match version number like 1.2.3', () => {
    expect(make().provide('v1.2.3 released')).toHaveLength(0)
  })

  it('does NOT match decimal number 1.5', () => {
    expect(make().provide('got 1.5 seconds')).toHaveLength(0)
  })

  it('DOES still match foo.d.ts (letters in ext)', () => {
    const r = make().provide('open foo.d.ts')
    expect(r).toHaveLength(1)
    expect(r[0].text).toBe('foo.d.ts')
  })

  it('matches hyphenated dotted segment report.pre-edit.md', () => {
    const r = make().provide('see report.pre-edit.md')
    expect(r).toHaveLength(1)
    expect(r[0].text).toBe('report.pre-edit.md')
    expect(r[0].meta).toEqual({ path: 'report.pre-edit.md' })
  })

  it('does NOT consume a trailing hyphen after ext foo.md-', () => {
    const r = make().provide('see foo.md- ok')
    expect(r[0].text).toBe('foo.md')
    expect(r[0].meta).toEqual({ path: 'foo.md' })
  })

  it('does NOT match IP address 192.168.1.1 (hyphen regression guard)', () => {
    expect(make().provide('ping 192.168.1.1 x')).toHaveLength(0)
  })

  it('does NOT match semver prerelease v1.2.3-beta', () => {
    expect(make().provide('got v1.2.3-beta released')).toHaveLength(0)
  })

  it('does NOT match semver prerelease 1.2.3-rc.1', () => {
    expect(make().provide('at 1.2.3-rc.1 tag')).toHaveLength(0)
  })

  it('DOES match bar.min-2.js (real file with hyphen ext)', () => {
    const r = make().provide('open bar.min-2.js')
    expect(r).toHaveLength(1)
    expect(r[0].text).toBe('bar.min-2.js')
  })

  it('DOES match data.2024-01.json (real file with hyphen ext)', () => {
    const r = make().provide('see data.2024-01.json')
    expect(r).toHaveLength(1)
    expect(r[0].text).toBe('data.2024-01.json')
  })

  it('DOES still match tarball v1.2.3.tar.gz:10', () => {
    const r = make().provide('got v1.2.3.tar.gz:10')
    expect(r[0].text).toBe('v1.2.3.tar.gz:10')
    expect(r[0].meta).toEqual({ path: 'v1.2.3.tar.gz', line: 10 })
  })

  it('does NOT match semver build metadata v1.0.0+build123', () => {
    expect(make().provide('released v1.0.0+build123 today')).toHaveLength(0)
  })

  it('does NOT match semver build metadata 1.0.0+abc', () => {
    expect(make().provide('tag 1.0.0+abc here')).toHaveLength(0)
  })

  it('does NOT match semver with hyphenated build metadata 1.0.0+build-123', () => {
    expect(make().provide('release 1.0.0+build-123 shipped')).toHaveLength(0)
  })

  it('does NOT match semver with dotted build metadata 1.0.0+exp.sha.5114f85', () => {
    expect(make().provide('version 1.0.0+exp.sha.5114f85 here')).toHaveLength(0)
  })

  it('does NOT match semver whose build metadata ends in letters 1.0.0+exp.sha', () => {
    expect(make().provide('build 1.0.0+exp.sha done')).toHaveLength(0)
  })

  it('does NOT match semver 1.0.0+abc.def (dotted alpha build metadata)', () => {
    expect(make().provide('tag 1.0.0+abc.def now')).toHaveLength(0)
  })

  it('does NOT match numeric-hyphen date-like report.2024-01', () => {
    expect(make().provide('see report.2024-01 log')).toHaveLength(0)
  })

  // Chosen bias (see allExtensionsVersionLike): a filename whose stem before `+`
  // is itself a bare version is sacrificed (not linkified), preferred over
  // linkifying the common `1.0.0+exp.sha`-style version noise in terminals.
  it('does NOT match a bare-version stem + real ext v1.0.0+build123.txt (trade-off)', () => {
    expect(make().provide('wrote v1.0.0+build123.txt here')).toHaveLength(0)
  })

  it('does NOT match report.2024+01.log (bare-version stem trade-off)', () => {
    expect(make().provide('tail report.2024+01.log now')).toHaveLength(0)
  })

  it('DOES match tarball with + build metadata name-0.0.0+075a408.tar.gz (package stem)', () => {
    const r = make().provide('built com.wake.custom-css-0.0.0+075a408.tar.gz ok')
    expect(r).toHaveLength(1)
    expect(r[0].text).toBe('com.wake.custom-css-0.0.0+075a408.tar.gz')
    expect(r[0].meta).toEqual({ path: 'com.wake.custom-css-0.0.0+075a408.tar.gz' })
  })
})

describe('createFilePathMatcher — tilde', () => {
  const make = (isEnabled = true) =>
    createFilePathMatcher({ id: 'test-tilde', regex: TILDE_RE, isEnabled: () => isEnabled })

  it('matches ~/foo.ts', () => {
    const r = make().provide('open ~/foo.ts')
    expect(r).toHaveLength(1)
    expect(r[0].text).toBe('~/foo.ts')
    expect(r[0].meta).toEqual({ path: '~/foo.ts' })
  })

  it('matches ~/.config/x.ts', () => {
    const r = make().provide('open ~/.config/x.ts')
    expect(r).toHaveLength(1)
    expect(r[0].text).toBe('~/.config/x.ts')
    expect(r[0].meta).toEqual({ path: '~/.config/x.ts' })
  })

  it('does NOT match word~/foo.ts', () => {
    expect(make().provide('word~/foo.ts')).toHaveLength(0)
  })

  it('captures line/col at line start', () => {
    const r = make().provide('~/foo.ts:10:5')
    expect(r).toHaveLength(1)
    expect(r[0].meta).toEqual({ path: '~/foo.ts', line: 10, col: 5 })
  })

  it('does NOT match ~~double', () => {
    expect(make().provide('~~/foo.ts')).toHaveLength(0)
  })

  it('returns [] when flag off', () => {
    expect(make(false).provide('~/foo.ts')).toHaveLength(0)
  })

  it('matches hyphenated dotted segment ~/d/bar.min-2.js', () => {
    const r = make().provide('open ~/d/bar.min-2.js')
    expect(r).toHaveLength(1)
    expect(r[0].text).toBe('~/d/bar.min-2.js')
    expect(r[0].meta).toEqual({ path: '~/d/bar.min-2.js' })
  })
})
