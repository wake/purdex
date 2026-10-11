import { describe, it, expect } from 'vitest'
import { usageFor } from './usage-display'

// user 2026-10-12: context from the session itself, 5h / 7d from the host's freshest snapshot.
const snap = (receivedAt: number, ctx: number | null, fh?: number, sd?: number) => ({
  receivedAt,
  raw: {
    ...(ctx === null ? {} : { context_window: { used_percentage: ctx } }),
    rate_limits: {
      ...(fh === undefined ? {} : { five_hour: { used_percentage: fh, resets_at: 1_800_000_000 } }),
      ...(sd === undefined ? {} : { seven_day: { used_percentage: sd, resets_at: 1_800_000_000 } }),
    },
  } as Record<string, unknown>,
})

describe('usageFor', () => {
  it('context comes from the session itself, limits from the newest snapshot on the host', () => {
    const cc = { 'h1:old': snap(100, 10, 20, 30), 'h1:new': snap(200, 55, 40, 50) }
    const u = usageFor('h1', 'old', cc)!
    expect(u.context).toBe(10)
    expect(u.fiveHour?.pct).toBe(40)
    expect(u.sevenDay?.pct).toBe(50)
  })
  it('a field missing from the newest snapshot falls back to the next newest that has it, per ring', () => {
    const cc = { 'h1:a': snap(100, 10, 20, 30), 'h1:b': snap(200, 55, 40) }
    const u = usageFor('h1', 'a', cc)!
    expect(u.fiveHour?.pct).toBe(40)
    expect(u.sevenDay?.pct).toBe(30)
  })
  it('another host never leaks in', () => {
    const cc = { 'h1:a': snap(100, 10, 20, 30), 'h2:b': snap(900, 99, 88, 77) }
    const u = usageFor('h1', 'a', cc)!
    expect(u.fiveHour?.pct).toBe(20)
    expect(u.sevenDay?.pct).toBe(30)
  })
  it('a session with no snapshot of its own still gets the host limits, no context', () => {
    const u = usageFor('h1', 'none', { 'h1:b': snap(200, 55, 40, 50) })!
    expect(u.context).toBeNull()
    expect(u.fiveHour?.pct).toBe(40)
  })
  it('nothing anywhere -> null', () => {
    expect(usageFor('h1', 'a', {})).toBeNull()
    expect(usageFor('h1', 'a', { 'h1:a': snap(1, null) })).toBeNull()
  })
  it('hostIds that contain a colon are matched whole', () => {
    const cc = { 'mlab:x:a': snap(100, 10, 20, 30), 'mlab:b': snap(900, 1, 88, 77) }
    expect(usageFor('mlab:x', 'a', cc)!.fiveHour?.pct).toBe(20)
  })
})

describe('usageFor: untrusted timestamps', () => {
  it('a snapshot without a daemon timestamp never beats a trusted one, even if received later', () => {
    const cc = {
      'h1:a': { ...snap(100, 10, 20, 30), atTrusted: true },
      'h1:b': { ...snap(900, 55, 90, 95), atTrusted: false },
    }
    const u = usageFor('h1', 'a', cc)!
    expect(u.fiveHour?.pct).toBe(20)
    expect(u.sevenDay?.pct).toBe(30)
  })
  it('with no trusted snapshot on the host, the newest untrusted one is used', () => {
    const cc = {
      'h1:a': { ...snap(100, 10, 20, 30), atTrusted: false },
      'h1:b': { ...snap(200, 55, 90, 95), atTrusted: false },
    }
    const u = usageFor('h1', 'a', cc)!
    expect(u.fiveHour?.pct).toBe(90)
    expect(u.sevenDay?.pct).toBe(95)
  })
  it('a trusted snapshot lacking the field still falls back to an untrusted one that has it', () => {
    const cc = {
      'h1:a': { ...snap(100, 10, 20), atTrusted: true },
      'h1:b': { ...snap(900, 55, 90, 95), atTrusted: false },
    }
    const u = usageFor('h1', 'a', cc)!
    expect(u.fiveHour?.pct).toBe(20)
    expect(u.sevenDay?.pct).toBe(95)
  })
})
