// spa/src/lib/fleet/api.ts — the fleet module's HTTP calls (fleet spec §6) and the `probe-hosts` collection of the
// daemon's hostconfig. Every call is pinned to a host this device has: `hostFetch` would answer an unknown id from the
// active host, and a probe list written to the wrong daemon is silent corruption.
import { pinnedHostFetch } from '../host-api'
import { parseFleetPayload, parseVersionedProbeHosts } from './parse'
import type { FleetPayload, ProbeHostDef, VersionedProbeHosts } from './types'

async function failure(what: string, res: Response): Promise<Error> {
  let text = ''
  try { text = (await res.text()).trim() } catch { /* body unreadable */ }
  return new Error(`${what}: ${res.status}${text ? ` ${text}` : ''}`)
}

export async function fetchFleet(hostId: string, signal?: AbortSignal): Promise<FleetPayload> {
  const res = await pinnedHostFetch(hostId, '/api/fleet', { signal })
  if (!res.ok) throw await failure('GET /api/fleet', res)
  const payload = parseFleetPayload(await res.json())
  if (!payload) throw new Error('GET /api/fleet: malformed body')
  return payload
}

/** The `Host` names the prober's SSH config offers (admin only). */
export async function fetchSshAliases(hostId: string, signal?: AbortSignal): Promise<string[]> {
  const res = await pinnedHostFetch(hostId, '/api/fleet/ssh-aliases', { signal })
  if (!res.ok) throw await failure('GET /api/fleet/ssh-aliases', res)
  const body: unknown = await res.json()
  const list = typeof body === 'object' && body !== null ? (body as { aliases?: unknown }).aliases : undefined
  return Array.isArray(list) ? list.filter((a): a is string => typeof a === 'string') : []
}

/** `probeHosts` out of GET /api/hostconfig; null = the daemon does not carry the collection. */
export async function fetchProbeHosts(hostId: string, signal?: AbortSignal): Promise<VersionedProbeHosts | null> {
  const res = await pinnedHostFetch(hostId, '/api/hostconfig', { signal })
  if (!res.ok) throw await failure('GET /api/hostconfig', res)
  const body: unknown = await res.json()
  const field = typeof body === 'object' && body !== null ? (body as { probeHosts?: unknown }).probeHosts : undefined
  return parseVersionedProbeHosts(field)
}

export type PutProbeHostsResult =
  | { kind: 'ok'; current: VersionedProbeHosts | null }
  /** 409: `current` is the daemon's copy now. */
  | { kind: 'conflict'; current: VersionedProbeHosts | null }
  /** 400: the daemon's plain-text reason. */
  | { kind: 'invalid'; text: string }

/** Only the keys the daemon's normaliser accepts: it rejects unknown ones, and a row from the payload carries more. */
function wireRow(d: ProbeHostDef): Record<string, unknown> {
  const row: Record<string, unknown> = { id: d.id, name: d.name, ssh_alias: d.ssh_alias }
  if (d.icon) row.icon = d.icon
  if (d.icon_weight) row.icon_weight = d.icon_weight
  if (d.colors) row.colors = d.colors
  return row
}

export async function putProbeHosts(hostId: string, items: readonly ProbeHostDef[], baseRevision: number): Promise<PutProbeHostsResult> {
  const res = await pinnedHostFetch(hostId, '/api/hostconfig/probe-hosts', {
    method: 'PUT',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ items: items.map(wireRow), baseRevision }),
  })
  if (res.status === 409) return { kind: 'conflict', current: parseVersionedProbeHosts(await res.json().catch(() => null)) }
  if (res.status === 400) return { kind: 'invalid', text: (await res.text().catch(() => '')).trim() }
  if (!res.ok) throw await failure('PUT /api/hostconfig/probe-hosts', res)
  return { kind: 'ok', current: parseVersionedProbeHosts(await res.json().catch(() => null)) }
}
