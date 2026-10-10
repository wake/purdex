// #2410 (inert prerequisite): a seat's reading carries when the host took it, and there is one place that says whether that
// is stale. Nothing is drawn from it yet; the threshold is pending the user's decision.
import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { renderHook, act } from '@testing-library/react'
import { isStale, READING_STALE_AFTER_MS, readingAge, readingOf, useSeatReading } from './team-readings'
import { COARSE_NOW_STEP_MS, coarseNow, useCoarseNow } from '../../hooks/useCoarseNow'
import { useTeamRosterStore } from '../../stores/useTeamRosterStore'
import type { RosterSession, TeamRoster } from '../../lib/team/roster'

const NOW = Date.UTC(2026, 9, 11, 12, 0, 0)
const MIN = 60_000

const seat = (over: Partial<RosterSession> = {}): RosterSession =>
  ({ session_id: 'S', ref: '_aaaaaa', address: 'h/S', live: true, ...over }) as RosterSession
const withContext = (at: number | undefined, used: number | null = 40): RosterSession =>
  seat({ model: 'claude-opus-4', context: { used_percentage: used, window: 200_000, ...(at === undefined ? {} : { at }) } as RosterSession['context'] })

describe('readingOf carries when the host took the reading', () => {
  it('at is context.at', () => {
    expect(readingOf(withContext(NOW - 5 * MIN)).at).toBe(NOW - 5 * MIN)
  })

  it('a seat without a context has no at (unknown)', () => {
    expect(readingOf(seat({ model: 'claude-opus-4' }))).not.toHaveProperty('at')
    expect(readingOf(undefined)).toEqual({})
  })

  it('a context whose at is absent has no at', () => {
    expect(readingOf(withContext(undefined))).not.toHaveProperty('at')
  })

  it.each([0, -5, Number.NaN, Number.POSITIVE_INFINITY])('an at that is not a time (%s) is unknown', (at) => {
    expect(readingOf(withContext(at))).not.toHaveProperty('at')
  })

  it('the other fields are unchanged', () => {
    expect(readingOf(withContext(NOW, 42.4))).toMatchObject({ ctx: 42, model: 'opus' })
  })

  it('a seat whose host did not answer carries no reading at all, at included', () => {
    expect(readingOf(seat({ context_unavailable: true, context: { used_percentage: 10, window: 1, at: NOW } }))).toEqual({ unavailable: true })
  })
})

describe('readingAge', () => {
  it('is now minus at', () => {
    expect(readingAge({ at: NOW - 7 * MIN }, NOW)).toBe(7 * MIN)
  })
  it('is undefined when the time is unknown', () => {
    expect(readingAge({}, NOW)).toBeUndefined()
  })
  it('a time in the future (clock skew between hosts) is age 0, never negative', () => {
    expect(readingAge({ at: NOW + 3_600_000 }, NOW)).toBe(0)
  })
})

describe('isStale', () => {
  it('the threshold is half an hour for now (pending the user\'s decision)', () => {
    expect(READING_STALE_AFTER_MS).toBe(30 * MIN)
  })
  it('an unknown time is not stale: it cannot be told, so it is drawn as before', () => {
    expect(isStale({}, NOW)).toBe(false)
  })
  it('a reading taken just now is fresh', () => {
    expect(isStale({ at: NOW }, NOW)).toBe(false)
  })
  it('exactly the threshold is still fresh; one ms over is stale', () => {
    expect(isStale({ at: NOW - READING_STALE_AFTER_MS }, NOW)).toBe(false)
    expect(isStale({ at: NOW - READING_STALE_AFTER_MS - 1 }, NOW)).toBe(true)
  })
  it('a long-dead reading is stale', () => {
    expect(isStale({ at: NOW - 3 * 24 * 3_600_000 }, NOW)).toBe(true)
  })
  it('a time in the future (clock skew) counts as fresh', () => {
    expect(isStale({ at: NOW + 10 * 3_600_000 }, NOW)).toBe(false)
  })
  it('takes its own threshold', () => {
    expect(isStale({ at: NOW - 2 * MIN }, NOW, MIN)).toBe(true)
    expect(isStale({ at: NOW - 2 * MIN }, NOW, 5 * MIN)).toBe(false)
  })
})

describe('useCoarseNow', () => {
  beforeEach(() => { vi.useFakeTimers(); vi.setSystemTime(NOW + 12_345) })
  afterEach(() => { vi.useRealTimers() })

  it('is the current minute, and changes once a minute, not between', () => {
    const { result } = renderHook(() => useCoarseNow())
    expect(result.current).toBe(coarseNow())
    expect(result.current % COARSE_NOW_STEP_MS).toBe(0)
    const first = result.current
    act(() => { vi.advanceTimersByTime(COARSE_NOW_STEP_MS - 20_000) })
    expect(result.current).toBe(first) // 42 s in: the same minute (the tick fired but the minute has not turned)
    act(() => { vi.advanceTimersByTime(40_000) })
    expect(result.current).toBe(first + COARSE_NOW_STEP_MS)
  })

  it('runs one timer for all subscribers and none when the last one goes', () => {
    const a = renderHook(() => useCoarseNow())
    const b = renderHook(() => useCoarseNow())
    expect(vi.getTimerCount()).toBe(1)
    a.unmount()
    expect(vi.getTimerCount()).toBe(1)
    b.unmount()
    expect(vi.getTimerCount()).toBe(0)
  })
})

describe('useSeatReading carries the stale flag (no component reads it yet)', () => {
  const KEY = 'h1\u0000t1'
  const team = (lead: RosterSession): TeamRoster =>
    ({ id: 't1', host_id: 'd1', created_at: 1, team_name: '', team_label: '', lead, members: [] }) as unknown as TeamRoster

  beforeEach(() => {
    vi.useFakeTimers()
    vi.setSystemTime(NOW)
    useTeamRosterStore.getState().reset()
  })
  afterEach(() => { vi.useRealTimers() })

  it('a fresh reading is not stale, an old one is, one of unknown time is not', () => {
    useTeamRosterStore.getState().apply('h1', [team(withContext(NOW - MIN))])
    expect(renderHook(() => useSeatReading(KEY, 'S')).result.current).toMatchObject({ stale: false, at: NOW - MIN })

    useTeamRosterStore.getState().apply('h1', [team(withContext(NOW - 2 * 3_600_000))])
    expect(renderHook(() => useSeatReading(KEY, 'S')).result.current.stale).toBe(true)

    useTeamRosterStore.getState().apply('h1', [team(withContext(undefined))])
    expect(renderHook(() => useSeatReading(KEY, 'S')).result.current.stale).toBe(false)
  })

  it('a reading turns stale as the clock moves on, with no new frame', () => {
    useTeamRosterStore.getState().apply('h1', [team(withContext(NOW))])
    const { result } = renderHook(() => useSeatReading(KEY, 'S'))
    expect(result.current.stale).toBe(false)
    act(() => { vi.advanceTimersByTime(READING_STALE_AFTER_MS + 2 * MIN) })
    expect(result.current.stale).toBe(true)
  })

  it('a seat that is not in the roster is an empty, not-stale reading', () => {
    expect(renderHook(() => useSeatReading(KEY, 'nobody')).result.current).toEqual({ stale: false })
  })
})
