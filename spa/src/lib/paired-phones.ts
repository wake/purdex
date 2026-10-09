// spa/src/lib/paired-phones.ts — the device rows of several hosts, grouped into one entry per phone (pairing_id). Pure.
// Revoked rows are not shown; a pairing whose every row is revoked disappears.
//
// state: 'paired' — some token was used; 'waiting' — never used and some token can still be (not past use_by on at least
// one host); 'unused-expired' — never used and past use_by on every host.
import type { DeviceRow } from './devices-api'

export type PairedState = 'waiting' | 'paired' | 'unused-expired'

export interface PairedHostEntry {
  hostId: string
  deviceId: string
  used: boolean
  revoked: boolean
  /** The token's last use (ms); 0 = never. */
  usedAt: number
}

export interface PairedPhone {
  pairingId: string
  label: string
  /** The earliest `created_at` among the rows. */
  createdAt: number
  /** The earliest first use across hosts; 0 = never used. */
  firstUsedAt: number
  /** The latest use across hosts; 0 = never used. */
  lastUsedAt: number
  state: PairedState
  perHost: PairedHostEntry[]
}

export function groupPairedPhones(
  perHostRows: ReadonlyArray<{ hostId: string; rows: readonly DeviceRow[] }>,
  now: number,
): PairedPhone[] {
  const groups = new Map<string, { hostId: string; row: DeviceRow }[]>()
  for (const { hostId, rows } of perHostRows) {
    for (const row of rows) {
      if (row.revoked_at > 0) continue
      const g = groups.get(row.pairing_id)
      if (g) g.push({ hostId, row })
      else groups.set(row.pairing_id, [{ hostId, row }])
    }
  }
  const phones: PairedPhone[] = []
  for (const [pairingId, members] of groups) {
    const used = members.filter((m) => m.row.first_used_at > 0)
    const firstUsedAt = used.length === 0 ? 0 : Math.min(...used.map((m) => m.row.first_used_at))
    const lastUsedAt = used.length === 0 ? 0 : Math.max(...used.map((m) => Math.max(m.row.last_used_at, m.row.first_used_at)))
    const state: PairedState =
      used.length > 0 ? 'paired' : members.every((m) => m.row.use_by <= now) ? 'unused-expired' : 'waiting'
    phones.push({
      pairingId,
      label: members[0].row.label,
      createdAt: Math.min(...members.map((m) => m.row.created_at)),
      firstUsedAt,
      lastUsedAt,
      state,
      perHost: members.map((m) => ({
        hostId: m.hostId,
        deviceId: m.row.id,
        used: m.row.first_used_at > 0,
        revoked: false,
        usedAt: m.row.first_used_at > 0 ? Math.max(m.row.last_used_at, m.row.first_used_at) : 0,
      })),
    })
  }
  return phones.sort((a, b) => b.createdAt - a.createdAt)
}
