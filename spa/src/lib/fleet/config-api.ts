// spa/src/lib/fleet/config-api.ts — the fleet's host-config rows on one daemon (fleet spec §6, §8.2): `probeHosts` (the hosts
// only probed over SSH) and `fleet` (order, the other daemon's SSH alias, the switch), read from `GET /api/hostconfig`, written
// by `PUT /api/hostconfig/probe-hosts` and `/fleet` with `{items, baseRevision}`, and the SSH aliases the daemon accepts.
import { hostFetch } from '../host-api'
import { HostConfigApiError, HostConfigConflictError } from '../host-config-api'
import type { HostConfig } from '../../stores/useHostStore'

export interface ProbeRow { id: string; name: string; ssh_alias: string; icon: string; icon_weight: string; colors?: HostConfig['colors'] }
export interface FleetRow { order: string[]; peer_ssh_alias: string; enabled: boolean }
/** `dropped`: rows the daemon left out of its answer; `invalid`: the stored value is not this row at all. */
export interface Marked { dropped: { count: number; reasons: string[] } | null; invalid: boolean }
export interface Versioned<T> extends Marked { items: T; revision: number }
export interface FleetConfigRead { probe: Versioned<ProbeRow[]>; fleet: Versioned<FleetRow> }

const isObj = (v: unknown): v is Record<string, unknown> => typeof v === 'object' && v !== null && !Array.isArray(v)
const str = (v: unknown): string => (typeof v === 'string' ? v : '')

function marks(v: Record<string, unknown>): Marked {
  const d = v.dropped
  const dropped = isObj(d) && typeof d.count === 'number' && d.count > 0
    ? { count: d.count, reasons: Array.isArray(d.reasons) ? d.reasons.filter((r): r is string => typeof r === 'string') : [] }
    : null
  return { dropped, invalid: v.invalid === true }
}

function parseProbeRow(v: unknown): ProbeRow | null {
  if (!isObj(v) || typeof v.id !== 'string' || !v.id) return null
  const row: ProbeRow = { id: v.id, name: str(v.name), ssh_alias: str(v.ssh_alias), icon: str(v.icon), icon_weight: str(v.icon_weight) }
  if (isObj(v.colors)) row.colors = v.colors as HostConfig['colors']
  return row
}

export function parseFleetConfig(body: unknown): FleetConfigRead {
  const b = isObj(body) ? body : {}
  const p = isObj(b.probeHosts) ? b.probeHosts : {}
  const f = isObj(b.fleet) ? b.fleet : {}
  const fi = isObj(f.items) ? f.items : {}
  return {
    probe: {
      items: Array.isArray(p.items) ? p.items.map(parseProbeRow).filter((r): r is ProbeRow => r !== null) : [],
      revision: typeof p.revision === 'number' ? p.revision : 0,
      ...marks(p),
    },
    fleet: {
      items: {
        order: Array.isArray(fi.order) ? fi.order.filter((a): a is string => typeof a === 'string') : [],
        peer_ssh_alias: str(fi.peer_ssh_alias),
        enabled: fi.enabled !== false,
      },
      revision: typeof f.revision === 'number' ? f.revision : 0,
      ...marks(f),
    },
  }
}

export async function fetchFleetConfig(hostId: string): Promise<FleetConfigRead> {
  const res = await hostFetch(hostId, '/api/hostconfig')
  if (!res.ok) throw new HostConfigApiError(res.status, `GET /api/hostconfig: ${res.status}`)
  return parseFleetConfig(await res.json())
}

export async function fetchSshAliases(hostId: string): Promise<string[]> {
  const res = await hostFetch(hostId, '/api/fleet/ssh-aliases')
  if (!res.ok) throw new HostConfigApiError(res.status, `GET /api/fleet/ssh-aliases: ${res.status}`)
  const body: unknown = await res.json()
  const list = isObj(body) && Array.isArray(body.aliases) ? body.aliases : []
  return list.filter((a): a is string => typeof a === 'string')
}

/** 409 → HostConfigConflictError (carrying the daemon's copy); other non-2xx → HostConfigApiError with the plain-text body. */
async function put(hostId: string, path: 'probe-hosts' | 'fleet', items: unknown, baseRevision: number): Promise<void> {
  const res = await hostFetch(hostId, `/api/hostconfig/${path}`, {
    method: 'PUT',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ items, baseRevision }),
  })
  if (res.status === 409) {
    throw new HostConfigConflictError((await res.json().catch(() => ({ items: null, revision: 0 }))) as { items: unknown; revision: number })
  }
  if (!res.ok) {
    const text = (await res.text().catch(() => '')).trim()
    throw new HostConfigApiError(res.status, text || `${res.status} ${res.statusText}`.trim())
  }
}

export const putProbeHosts = (hostId: string, items: ProbeRow[], baseRevision: number) => put(hostId, 'probe-hosts', items, baseRevision)
export const putFleetRow = (hostId: string, items: FleetRow, baseRevision: number) => put(hostId, 'fleet', items, baseRevision)
