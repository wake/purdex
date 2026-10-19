import { describe, it, expect } from 'vitest'
import { parseExceptionList } from './exceptions'

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

  it('an answer that is no list is not known', () => {
    for (const bad of [null, undefined, {}, 'x', { exceptions: [] }]) expect(parseExceptionList(bad)).toBeNull()
    expect(parseExceptionList([])).toEqual([])
  })
})
