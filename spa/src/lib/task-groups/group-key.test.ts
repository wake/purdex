import { describe, expect, it } from 'vitest'
import { UNGROUPED, manualGroupKey, parseGroupKey, teamGroupKey, teamKeyOfGroupKey } from './group-key'

describe('group keys', () => {
  it('round-trips a team key (TeamView.key = <local host id>\\0<team id>)', () => {
    const view = 'mlab\u0000team-1'
    const key = teamGroupKey(view)
    expect(key).toBe('team\u0000mlab\u0000team-1')
    expect(parseGroupKey(key)).toEqual({ kind: 'team', teamKey: view })
    expect(teamKeyOfGroupKey(key)).toBe(view)
  })

  it('round-trips a manual group id', () => {
    const key = manualGroupKey('a1b2c3d4')
    expect(key).toBe('group\u0000a1b2c3d4')
    expect(parseGroupKey(key)).toEqual({ kind: 'manual', id: 'a1b2c3d4' })
    expect(teamKeyOfGroupKey(key)).toBeNull()
  })

  it('knows the ungrouped key', () => {
    expect(UNGROUPED).toBe('ungrouped')
    expect(parseGroupKey(UNGROUPED)).toEqual({ kind: 'ungrouped' })
    expect(teamKeyOfGroupKey(UNGROUPED)).toBeNull()
  })

  it('refuses a NUL inside a manual id', () => {
    expect(() => manualGroupKey('a\u0000b')).toThrow()
    expect(() => manualGroupKey('')).toThrow()
    expect(parseGroupKey('group\u0000a\u0000b')).toBeNull()
  })

  it('refuses a malformed team key and unknown input', () => {
    expect(() => teamGroupKey('')).toThrow()
    expect(parseGroupKey('team\u0000')).toBeNull()
    expect(parseGroupKey('team\u0000onlyhost')).toBeNull()
    expect(parseGroupKey('team')).toBeNull()
    expect(parseGroupKey('nope')).toBeNull()
    expect(parseGroupKey('')).toBeNull()
    expect(teamKeyOfGroupKey('team\u0000')).toBeNull()
  })
})
