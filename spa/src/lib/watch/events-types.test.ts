import { describe, it, expect } from 'vitest'
import { parseWatchEvents } from './events-types'

const e1 = { id: 'mlab-1', host: 'mlab', kind: 'unmanaged_heavy', at: '2026-10-17T01:00:00Z', label: 'gate_gap', command: 'vitest run', cwd: '/w', session: 's', peak_cores: 6.5, peak_mb: 900, duration_s: 42, new: true, status: 'pending' }

describe('parseWatchEvents', () => {
  it('reads an E1 row with every field', () => {
    expect(parseWatchEvents({ events: [e1] })).toEqual([expect.objectContaining({
      id: 'mlab-1', host: 'mlab', kind: 'unmanaged_heavy', label: 'gate_gap', command: 'vitest run', cwd: '/w', session: 's',
      peakCores: 6.5, peakMb: 900, durationS: 42, status: 'pending',
    })])
  })
  it('reads an E2 row and a handled row with its note', () => {
    const rows = parseWatchEvents({ events: [
      { id: 'a-2', host: 'a', kind: 'pressure_high', at: '2026-10-17T02:00:00Z', pressure: 2, page_rate: 120, detail: 'swap', status: 'handled', note: 'killed it', handled_at: '2026-10-17T02:05:00Z' },
    ] })!
    expect(rows[0]).toMatchObject({ pressure: 2, pageRate: 120, detail: 'swap', status: 'handled', note: 'killed it', handledAt: '2026-10-17T02:05:00Z' })
  })
  it('drops a row with no id, no kind or a bad time; keeps the rest in order', () => {
    const rows = parseWatchEvents({ events: [{ ...e1, id: '' }, { ...e1, id: 'k', kind: '' }, { ...e1, id: 't', at: 'x' }, 7, null, { ...e1, id: 'ok1' }, { ...e1, id: 'ok2' }] })!
    expect(rows.map((r) => r.id)).toEqual(['ok1', 'ok2'])
  })
  it('keeps a kind it does not know, and reads an unknown status as pending', () => {
    const r = parseWatchEvents({ events: [{ ...e1, kind: 'future_kind', status: 'weird' }] })![0]
    expect(r).toMatchObject({ kind: 'future_kind', status: 'pending' })
  })
  it('an answer without an events array is unreadable', () => {
    expect(parseWatchEvents({})).toBeNull()
    expect(parseWatchEvents(null)).toBeNull()
    expect(parseWatchEvents({ events: 'x' })).toBeNull()
  })
})
