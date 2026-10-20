import { describe, it, expect } from 'vitest'
import { parseExceptionList } from './exceptions'
import knownJson from './__fixtures__/pool_host_exceptions.json'
import unknownJson from './__fixtures__/pool_host_exceptions_unknown.json'

describe('parseExceptionList', () => {
  it('reads wwd\'s Exception list: the end time, the pid, the holds', () => {
    const got = parseExceptionList([
      { name: 'app-pause-a', until: '2026-10-19T18:30:00+08:00', pid: null, holds: ['heavy', 'light'] },
      { name: 'electron-builder', until: null, pid: 4242, holds: ['heavy'] },
    ])
    expect(got).toEqual([
      { name: 'app-pause-a', until: Date.parse('2026-10-19T18:30:00+08:00'), pid: null, holds: ['heavy', 'light'] },
      { name: 'electron-builder', until: null, pid: 4242, holds: ['heavy'] },
    ])
  })

  it('leaves out an entry that does not read, keeps the known holds only, and takes a missing holds as none', () => {
    const got = parseExceptionList([{ name: '' }, 5, { name: 'ok', until: 'not a time', pid: -1, holds: ['heavy', 'gpu', 3] }, { name: 'bare' }])
    expect(got).toEqual([
      { name: 'ok', until: null, pid: null, holds: ['heavy'] },
      { name: 'bare', until: null, pid: null, holds: [] },
    ])
  })

  it('an answer that is neither the wire object nor a list is not known', () => {
    for (const bad of [null, undefined, {}, 'x', { exceptions: [] }, { known: 'yes', exceptions: [] }]) expect(parseExceptionList(bad)).toBeNull()
    expect(parseExceptionList([])).toEqual([])
  })
})

describe('parseExceptionList: the wire object { host, known, exceptions }', () => {
  it('known:true reads the entries, with the classes they let through and the flags they carry', () => {
    expect(parseExceptionList(knownJson)).toEqual([
      { name: 'app-dev-package', until: Date.parse('2026-10-19T09:00:00Z'), pid: null, holds: ['heavy', 'light'] },
      { name: 'stress-run', until: null, pid: 4242, holds: ['heavy'], allow: { classes: ['release'], repos: ['purdex'] }, flags: ['release', 'expect_pressure'] },
    ])
  })

  it('drops the classes and flags it does not know; an allow with nothing left is still an allow (wwd: listed means only those)', () => {
    const got = parseExceptionList([{ name: 'a', until: null, pid: 1, holds: ['heavy'], allow: { classes: ['merge', 'gpu', 3], repos: ['r', 4] }, flags: ['expect_pressure', 'x'] },
      { name: 'b', allow: { classes: ['gpu'] }, flags: ['x'] }, { name: 'c', allow: 'all', flags: 'release' }])
    expect(got).toEqual([
      { name: 'a', until: null, pid: 1, holds: ['heavy'], allow: { classes: ['merge'], repos: ['r'] }, flags: ['expect_pressure'] },
      { name: 'b', until: null, pid: null, holds: [], allow: { classes: [], repos: [] } },
      { name: 'c', until: null, pid: null, holds: [] },
    ])
  })

  it('known:false is "not known" (null), not an empty list', () => {
    expect(parseExceptionList(unknownJson)).toBeNull()
  })

  it('known:true with no exceptions is an empty list; a broken entry is left out', () => {
    expect(parseExceptionList({ host: 'h', known: true, exceptions: [] })).toEqual([])
    expect(parseExceptionList({ host: 'h', known: true, exceptions: [5, { name: 'ok' }] })).toEqual([{ name: 'ok', until: null, pid: null, holds: [] }])
  })

  it('known:true with exceptions missing, null, an object or a string is a bad answer, not "none"', () => {
    for (const bad of [undefined, null, {}, 'x']) expect(parseExceptionList({ host: 'h', known: true, exceptions: bad })).toBeNull()
    expect(parseExceptionList({ host: 'h', known: true })).toBeNull()
  })
})
