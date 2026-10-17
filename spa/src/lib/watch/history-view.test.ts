import { describe, it, expect } from 'vitest'
import { barParts, scaleMinutes, toRow, toRows } from './history-view'
import type { GateDecision } from './decisions-types'

const rec = (o: Partial<GateDecision> = {}): GateDecision => ({
  time: '2026-10-17T10:00:00Z', argv: ['go', 'test'], cwd: '/Users/w/purdex', session: 'abcdef123456', class: 'heavy', outcome: 'slot',
  waitS: 0, waitReasons: [], applied: {}, exit: 0, durationS: 60, ...o,
})

describe('toRow', () => {
  it('times: the command started at time + wait, ended after the run', () => {
    // duration_s counts from when the gate began waiting, so it holds the wait: 30 s waited + 60 s ran.
    const r = toRow(rec({ waitS: 30, durationS: 90 }), 0)
    expect(r.startMs - r.arriveMs).toBe(30_000)
    expect(r.endMs - r.arriveMs).toBe(90_000)
    expect(r.runS).toBe(60)
    expect(r.project).toBe('purdex')
  })

  it('cores = cpu_s / duration_s; absent usage stays null (not 0)', () => {
    expect(toRow(rec({ cpuS: 150, durationS: 60 }), 0).cores).toBe(2.5)
    // the cores are over the run only: 150 cpu s / (90 - 30) s
    expect(toRow(rec({ cpuS: 150, waitS: 30, durationS: 90 }), 0).cores).toBe(2.5)
    const bare = toRow(rec(), 0)
    expect([bare.cores, bare.maxRssMb, bare.majorFaults]).toEqual([null, null, null])
  })

  it('a zero value is kept, and a zero duration gives no cores', () => {
    expect(toRow(rec({ majorFaults: 0, maxRssMb: 0 }), 0)).toMatchObject({ majorFaults: 0, maxRssMb: 0 })
    expect(toRow(rec({ cpuS: 3, durationS: 0 }), 0).cores).toBeNull()
  })

  it('a started command with no run left (duration <= wait) has no run part and no cores', () => {
    const r = toRow(rec({ cpuS: 3, waitS: 60, durationS: 60 }), 0)
    expect(r).toMatchObject({ runS: 0, cores: null, started: true })
    expect(toRow(rec({ cpuS: 3, waitS: 70, durationS: 60 }), 0).runS).toBe(0)
  })

  it('a refused, interrupted or orphaned command only waited: no run, no failure, no usage', () => {
    for (const outcome of ['refused', 'interrupted', 'orphaned']) {
      const r = toRow(rec({ outcome, waitS: 40, durationS: 0, exit: 1, cpuS: 5, maxRssMb: 9, majorFaults: 2 }), 0)
      expect(r).toMatchObject({ started: false, runS: 0, failed: false, cores: null, maxRssMb: null, majorFaults: null })
    }
  })

  it('a non-zero exit of a started command is a failure', () => {
    expect(toRow(rec({ exit: 2 }), 0).failed).toBe(true)
    expect(toRow(rec({ exit: 0 }), 0).failed).toBe(false)
  })
})

describe('toRows / scale', () => {
  it('newest first by the time queued', () => {
    const rows = toRows([rec({ time: '2026-10-17T09:00:00Z' }), rec({ time: '2026-10-17T11:00:00Z' })])
    expect(rows.map((r) => r.arriveMs)).toEqual([Date.parse('2026-10-17T11:00:00Z'), Date.parse('2026-10-17T09:00:00Z')])
  })

  it('the scale is the longest wait + run rounded up to 5 minutes, at least 5', () => {
    expect(scaleMinutes([])).toBe(5)
    expect(scaleMinutes(toRows([rec({ waitS: 100, durationS: 300 })]))).toBe(5) // 5 min exactly
    expect(scaleMinutes(toRows([rec({ waitS: 100, durationS: 300 }), rec({ waitS: 60, durationS: 720 })]))).toBe(15)
  })

  it('bar parts are fractions of the one scale; a wait under 3 s has no hatched part', () => {
    const rows = toRows([rec({ waitS: 150, durationS: 300 })])
    expect(barParts(rows[0], 10)).toEqual({ wait: 0.25, run: 0.25 })
    expect(barParts(toRow(rec({ waitS: 2, durationS: 60 }), 0), 5).wait).toBe(0)
  })
})
