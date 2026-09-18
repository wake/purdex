import { describe, it, expect } from 'vitest'
import { joinCwd, utf8ByteLength, validateSubPath } from './cwd-input'

describe('validateSubPath', () => {
  it('accepts an empty sub-path as the root itself', () => {
    expect(validateSubPath('')).toEqual({ ok: true, value: '' })
  })

  it('accepts a plain relative path unchanged', () => {
    expect(validateSubPath('spa/src')).toEqual({ ok: true, value: 'spa/src' })
  })

  it('trims a trailing slash: a/b/ → a/b', () => {
    expect(validateSubPath('a/b/')).toEqual({ ok: true, value: 'a/b' })
  })

  it('rejects a leading / as absolute', () => {
    expect(validateSubPath('/etc')).toEqual({ ok: false, reason: 'absolute' })
  })

  it('rejects a leading ~ as tilde', () => {
    expect(validateSubPath('~/w')).toEqual({ ok: false, reason: 'tilde' })
    expect(validateSubPath('~')).toEqual({ ok: false, reason: 'tilde' })
  })

  it('rejects any .. segment as dotdot', () => {
    expect(validateSubPath('a/../b')).toEqual({ ok: false, reason: 'dotdot' })
    expect(validateSubPath('..')).toEqual({ ok: false, reason: 'dotdot' })
  })

  it('rejects any . segment as dot_segment', () => {
    expect(validateSubPath('./a')).toEqual({ ok: false, reason: 'dot_segment' })
    expect(validateSubPath('a/./b')).toEqual({ ok: false, reason: 'dot_segment' })
  })

  it('rejects a//b as empty_segment', () => {
    expect(validateSubPath('a//b')).toEqual({ ok: false, reason: 'empty_segment' })
  })

  it('rejects any backslash as backslash', () => {
    expect(validateSubPath('a\\b')).toEqual({ ok: false, reason: 'backslash' })
  })

  it('rejects leading or trailing whitespace as whitespace, without trimming it', () => {
    expect(validateSubPath(' a')).toEqual({ ok: false, reason: 'whitespace' })
    expect(validateSubPath('a ')).toEqual({ ok: false, reason: 'whitespace' })
    expect(validateSubPath('a b')).toEqual({ ok: true, value: 'a b' })
  })

  it('keeps $HOME and $VAR literal — never expanded, accepted as characters', () => {
    expect(validateSubPath('$HOME/x')).toEqual({ ok: true, value: '$HOME/x' })
    expect(validateSubPath('$VAR')).toEqual({ ok: true, value: '$VAR' })
  })

  it('accepts dotfiles and names that merely contain dots', () => {
    expect(validateSubPath('.claude/worktrees')).toEqual({ ok: true, value: '.claude/worktrees' })
    expect(validateSubPath('a...b/c.d')).toEqual({ ok: true, value: 'a...b/c.d' })
  })

  it('neither throws nor blocks a 10 000-char sub-path (no client length cap)', () => {
    const long = 'x'.repeat(10_000)
    expect(validateSubPath(long)).toEqual({ ok: true, value: long })
  })
})

describe('joinCwd', () => {
  it('returns the root alone for an empty sub-path', () => {
    expect(joinCwd('/srv/dev', '')).toBe('/srv/dev')
  })

  it('joins root and sub-path with exactly one slash', () => {
    expect(joinCwd('/srv/dev', 'a/b')).toBe('/srv/dev/a/b')
    expect(joinCwd('/srv/dev/', 'a/b')).toBe('/srv/dev/a/b')
  })
})

describe('utf8ByteLength', () => {
  it('counts ASCII one byte per character', () => {
    expect(utf8ByteLength('abc')).toBe(3)
  })

  it('counts unicode by UTF-8 bytes, not code units', () => {
    expect(utf8ByteLength('中文')).toBe(6)
    expect(utf8ByteLength('é')).toBe(2)
    expect(utf8ByteLength('😀')).toBe(4)
  })

  it('counts the empty string as zero', () => {
    expect(utf8ByteLength('')).toBe(0)
  })
})
