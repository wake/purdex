import { describe, expect, it } from 'vitest'
import type { DeviceRow } from './devices-api'
import { groupPairedPhones } from './paired-phones'

const NOW = 1_000_000
let n = 0
function row(over: Partial<DeviceRow>): DeviceRow {
  n++
  return {
    id: `d${n}`, pairing_id: 'P1', profile_id: '', label: 'iPhone', created_at: 100, created_by: 'admin',
    use_by: NOW + 1000, first_used_at: 0, last_used_at: 0, revoked_at: 0, ...over,
  }
}

describe('groupPairedPhones', () => {
  it('groups rows of several hosts by pairing id', () => {
    const phones = groupPairedPhones(
      [
        { hostId: 'a', rows: [row({ id: 'a1' }), row({ id: 'x1', pairing_id: 'P2', label: 'iPad' })] },
        { hostId: 'b', rows: [row({ id: 'b1' })] },
      ],
      NOW,
    )
    expect(phones.map((p) => p.pairingId).sort()).toEqual(['P1', 'P2'])
    const p1 = phones.find((p) => p.pairingId === 'P1')!
    expect(p1.perHost.map((h) => [h.hostId, h.deviceId])).toEqual([['a', 'a1'], ['b', 'b1']])
    expect(p1.label).toBe('iPhone')
  })

  it('state: waiting before use_by when nothing was used; paired when any token was used; unused-expired past use_by on every host', () => {
    const waiting = groupPairedPhones([{ hostId: 'a', rows: [row({ pairing_id: 'W' })] }], NOW)[0]
    expect(waiting.state).toBe('waiting')
    const paired = groupPairedPhones(
      [
        { hostId: 'a', rows: [row({ pairing_id: 'X', first_used_at: 500, last_used_at: 900 })] },
        { hostId: 'b', rows: [row({ pairing_id: 'X', use_by: NOW - 1 })] },
      ],
      NOW,
    )[0]
    expect(paired.state).toBe('paired')
    const expired = groupPairedPhones(
      [
        { hostId: 'a', rows: [row({ pairing_id: 'E', use_by: NOW - 5 })] },
        { hostId: 'b', rows: [row({ pairing_id: 'E', use_by: NOW - 1 })] },
      ],
      NOW,
    )[0]
    expect(expired.state).toBe('unused-expired')
  })

  it('unused and past use_by on only one host is still waiting', () => {
    const p = groupPairedPhones(
      [
        { hostId: 'a', rows: [row({ use_by: NOW - 5 })] },
        { hostId: 'b', rows: [row({ use_by: NOW + 5 })] },
      ],
      NOW,
    )[0]
    expect(p.state).toBe('waiting')
  })

  it('firstUsedAt is the minimum of the non-zero values, lastUsedAt the maximum; 0 when never used', () => {
    const p = groupPairedPhones(
      [
        { hostId: 'a', rows: [row({ first_used_at: 500, last_used_at: 600 })] },
        { hostId: 'b', rows: [row({ first_used_at: 300, last_used_at: 900 })] },
        { hostId: 'c', rows: [row({})] },
      ],
      NOW,
    )[0]
    expect(p.firstUsedAt).toBe(300)
    expect(p.lastUsedAt).toBe(900)
    expect(p.perHost.find((h) => h.hostId === 'c')).toMatchObject({ used: false, usedAt: 0 })
    expect(p.perHost.find((h) => h.hostId === 'b')).toMatchObject({ used: true, usedAt: 900 })
    const never = groupPairedPhones([{ hostId: 'a', rows: [row({ pairing_id: 'N' })] }], NOW)[0]
    expect(never.firstUsedAt).toBe(0)
    expect(never.lastUsedAt).toBe(0)
  })

  it('revoked rows are hidden; a pairing whose every row is revoked disappears', () => {
    const phones = groupPairedPhones(
      [
        { hostId: 'a', rows: [row({ id: 'r1', pairing_id: 'R', revoked_at: 5 }), row({ id: 'k1', pairing_id: 'K' })] },
        { hostId: 'b', rows: [row({ id: 'r2', pairing_id: 'R', revoked_at: 6 }), row({ id: 'k2', pairing_id: 'K', revoked_at: 7 })] },
      ],
      NOW,
    )
    expect(phones.map((p) => p.pairingId)).toEqual(['K'])
    expect(phones[0].perHost.map((h) => h.deviceId)).toEqual(['k1'])
  })

  it('newest first by createdAt (earliest row of the pairing)', () => {
    const phones = groupPairedPhones(
      [{ hostId: 'a', rows: [row({ pairing_id: 'old', created_at: 10 }), row({ pairing_id: 'new', created_at: 99 })] }],
      NOW,
    )
    expect(phones.map((p) => p.pairingId)).toEqual(['new', 'old'])
  })
})
