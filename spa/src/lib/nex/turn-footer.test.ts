// spa/src/lib/nex/turn-footer.test.ts — spec §7.2, F1–F3.
import { describe, it, expect } from 'vitest'
import { formatTurnDuration, turnFooterText, type TurnFooterResult } from './turn-footer'
import type { TurnMeta } from './event-reducer'

// timeZone: 'UTC' pins the rendered clock regardless of the host's zone —
// the brief's own h24/h12 formatters omit it, but this file's endAt fixture
// is a UTC instant and must render deterministically in CI.
const h24 = new Intl.DateTimeFormat('en-GB', { hour: 'numeric', minute: '2-digit', timeZone: 'UTC' })
const h12 = new Intl.DateTimeFormat('en-US', { hour: 'numeric', minute: '2-digit', timeZone: 'UTC' })
const fmt24 = (ms: number) => h24.format(new Date(ms))
const fmt12 = (ms: number) => h12.format(new Date(ms))

const meta = (patch: Partial<TurnMeta>): TurnMeta => ({ startAt: 0, endAt: null, outcome: null, durationMs: null, ...patch })

describe('formatTurnDuration', () => {
  it('under a second → <1s', () => {
    expect(formatTurnDuration(400)).toBe('<1s')
    expect(formatTurnDuration(0)).toBe('<1s')
  })

  it('under a minute → integer seconds, floored, no zero-pad', () => {
    expect(formatTurnDuration(10_000)).toBe('10s')
    expect(formatTurnDuration(59_999)).toBe('59s')
  })

  it('under an hour → Mm Ss, floored, no zero-pad', () => {
    expect(formatTurnDuration(2_282_000)).toBe('38m 2s')
    expect(formatTurnDuration(3_599_999)).toBe('59m 59s')
  })

  it('an hour or more → Hh Mm, floored, no zero-pad', () => {
    expect(formatTurnDuration(3_900_000)).toBe('1h 5m')
    expect(formatTurnDuration(3_600_000)).toBe('1h 0m')
  })

  it('non-finite → <1s', () => {
    expect(formatTurnDuration(Number.NaN)).toBe('<1s')
    expect(formatTurnDuration(-5)).toBe('<1s')
  })
})

describe('turnFooterText', () => {
  // 2026-01-15 17:48 UTC — a time that renders as 17:48 (h24) and 5:48 PM (h12).
  const endAt = Date.UTC(2026, 0, 15, 17, 48, 0)

  it('ok, with duration: "Worked for" + done time, normal tone; follows system 12/24h', () => {
    const m = meta({ startAt: endAt - 2_282_000, endAt, outcome: 'ok', durationMs: 2_282_000 })
    const r24 = turnFooterText(m, fmt24) as TurnFooterResult
    expect(r24.text).toBe('✻ Worked for 38m 2s · done 17:48')
    expect(r24.tone).toBe('normal')
    const r12 = turnFooterText(m, fmt12) as TurnFooterResult
    expect(r12.text).toBe('✻ Worked for 38m 2s · done 5:48 PM')
  })

  it('failed, with duration: "Failed after" + time, error tone', () => {
    const m = meta({ startAt: endAt - 12_000, endAt, outcome: 'failed', durationMs: 12_000 })
    const r = turnFooterText(m, fmt24) as TurnFooterResult
    expect(r.text).toBe('✻ Failed after 12s · 17:48')
    expect(r.tone).toBe('error')
  })

  it('no duration known → "Done" / "Failed" without a duration phrase', () => {
    const ok = turnFooterText(meta({ endAt, outcome: 'ok', durationMs: null }), fmt24) as TurnFooterResult
    expect(ok.text).toBe('✻ Done · 17:48')
    const failed = turnFooterText(meta({ endAt, outcome: 'failed', durationMs: null }), fmt24) as TurnFooterResult
    expect(failed.text).toBe('✻ Failed · 17:48')
  })

  it('title is the full localised date and time, not just the short time', () => {
    const m = meta({ endAt, outcome: 'ok', durationMs: 1000 })
    const r = turnFooterText(m, fmt24) as TurnFooterResult
    expect(r.title).toMatch(/2026/)
    expect(r.title).not.toBe(fmt24(endAt))
  })

  it('interrupted → null (F3: no footer)', () => {
    expect(turnFooterText(meta({ endAt, outcome: 'interrupted', durationMs: 1000 }), fmt24)).toBeNull()
  })

  it('no outcome yet → null', () => {
    expect(turnFooterText(meta({ endAt, outcome: null }), fmt24)).toBeNull()
  })

  it('no end time (the live turn) → null', () => {
    expect(turnFooterText(meta({ startAt: 0, endAt: null, outcome: 'ok', durationMs: null }), fmt24)).toBeNull()
  })
})
