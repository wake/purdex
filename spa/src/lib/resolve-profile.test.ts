import { describe, it, expect } from 'vitest'
import { resolveProfile, type ProfileKey, type Profile } from './resolve-profile'

function p(enabled: boolean, cols: number): Profile {
  return { enabled, columns: Array.from({ length: cols }, () => []) }
}

function profiles(enabled: Record<ProfileKey, boolean>) {
  return {
    '3col': p(enabled['3col'], 3),
    '2col': p(enabled['2col'], 2),
    '1col': p(enabled['1col'], 1),
  }
}

describe('resolveProfile', () => {
  it('wide viewport picks 3col when enabled', () => {
    const r = resolveProfile(true, true, profiles({ '3col': true, '2col': true, '1col': true }))
    expect(r).toBe('3col')
  })

  it('wide viewport falls back to 2col when 3col disabled', () => {
    const r = resolveProfile(true, true, profiles({ '3col': false, '2col': true, '1col': true }))
    expect(r).toBe('2col')
  })

  it('wide viewport falls back to 1col when 3col and 2col disabled', () => {
    const r = resolveProfile(true, true, profiles({ '3col': false, '2col': false, '1col': true }))
    expect(r).toBe('1col')
  })

  it('mid viewport picks 2col when enabled', () => {
    const r = resolveProfile(false, true, profiles({ '3col': true, '2col': true, '1col': true }))
    expect(r).toBe('2col')
  })

  it('mid viewport falls back to 1col when 2col disabled', () => {
    const r = resolveProfile(false, true, profiles({ '3col': true, '2col': false, '1col': true }))
    expect(r).toBe('1col')
  })

  it('narrow viewport always picks 1col regardless of 3col/2col state', () => {
    const r = resolveProfile(false, false, profiles({ '3col': true, '2col': true, '1col': true }))
    expect(r).toBe('1col')
  })

  it('defends against all-disabled (should be prevented by setter but safe)', () => {
    const r = resolveProfile(true, true, profiles({ '3col': false, '2col': false, '1col': false }))
    expect(r).toBe('1col')
  })
})
