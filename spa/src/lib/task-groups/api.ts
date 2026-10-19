// spa/src/lib/task-groups/api.ts — the daemon's manual task groups (name pool v2 spec §7, NA-6a): a solo session is put into a group by hand; a team is a group by itself and is not stored here (L10).
// Read from `GET /api/hostconfig` (`taskGroups`), written whole by `PUT /api/hostconfig/task-groups` with `{items, baseRevision}`. A group's `roots` are LINEAGE ROOT session ids, so a relay keeps the
// membership; each host keeps its own groups (L9). The daemon is the source of truth and nothing here caches (the store does).
import { hostFetch } from '../host-api'
import { HostConfigApiError, HostConfigConflictError } from '../host-config-api'
import { useHostStore } from '../../stores/useHostStore'

export interface TaskGroup {
  id: string
  name: string
  /** `#rrggbb`; absent = the default colour. */
  color?: string
  /** Lineage root session ids; a root is in at most one group. */
  roots: string[]
  created_at: number
  updated_at: number
}

/** `dropped`: rows the daemon left out of its answer; `invalid`: the stored value is not this row at all. */
export interface TaskGroupsRead {
  items: TaskGroup[]
  revision: number
  dropped: { count: number; reasons: string[] } | null
  invalid: boolean
}

const isObj = (v: unknown): v is Record<string, unknown> => typeof v === 'object' && v !== null && !Array.isArray(v)
const isTime = (v: unknown): v is number => typeof v === 'number' && Number.isFinite(v) && v >= 0
const HEX = /^#[0-9a-fA-F]{6}$/

function parseGroup(v: unknown): TaskGroup | null {
  if (!isObj(v) || typeof v.id !== 'string' || v.id === '' || typeof v.name !== 'string') return null
  if (!Array.isArray(v.roots) || !v.roots.every((r) => typeof r === 'string')) return null
  if (!isTime(v.created_at) || !isTime(v.updated_at)) return null
  if (v.color !== undefined && v.color !== '' && !(typeof v.color === 'string' && HEX.test(v.color))) return null
  return {
    id: v.id, name: v.name, roots: v.roots as string[], created_at: v.created_at, updated_at: v.updated_at,
    ...(typeof v.color === 'string' && v.color !== '' ? { color: v.color } : {}),
  }
}

/** The `taskGroups` row of a `GET /api/hostconfig` body (or of a 409's copy, wrapped the same way). */
export function parseTaskGroups(body: unknown): TaskGroupsRead {
  const row = isObj(body) && isObj(body.taskGroups) ? body.taskGroups : {}
  const d = row.dropped
  const dropped = isObj(d) && typeof d.count === 'number' && d.count > 0
    ? { count: d.count, reasons: Array.isArray(d.reasons) ? d.reasons.filter((r): r is string => typeof r === 'string') : [] }
    : null
  return {
    items: Array.isArray(row.items) ? row.items.map(parseGroup).filter((g): g is TaskGroup => g !== null) : [],
    revision: typeof row.revision === 'number' ? row.revision : 0,
    dropped,
    invalid: row.invalid === true,
  }
}

/** Host config written to the wrong daemon is silent corruption: an unknown id never reaches the network (`hostFetch` would resolve it to the active host). */
function assertKnownHost(hostId: string): void {
  if (!useHostStore.getState().hosts[hostId]) throw new HostConfigApiError(0, `host ${hostId} is not configured`)
}

export async function fetchTaskGroups(hostId: string, signal?: AbortSignal): Promise<TaskGroupsRead> {
  assertKnownHost(hostId)
  const res = await hostFetch(hostId, '/api/hostconfig', { signal })
  if (!res.ok) throw new HostConfigApiError(res.status, `GET /api/hostconfig: ${res.status}`)
  return parseTaskGroups(await res.json())
}

/**
 * Replaces the whole list, guarded by `baseRevision`. Resolves to the stored copy; a lost compare-and-set (409) throws a
 * HostConfigConflictError whose `current` is the daemon's copy as `{items, revision}`; other non-2xx → HostConfigApiError with the daemon's plain-text reason.
 */
export async function putTaskGroups(hostId: string, items: TaskGroup[], baseRevision: number): Promise<TaskGroupsRead> {
  assertKnownHost(hostId)
  const res = await hostFetch(hostId, '/api/hostconfig/task-groups', {
    method: 'PUT',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ items, baseRevision }),
  })
  if (res.status === 409) {
    throw new HostConfigConflictError((await res.json().catch(() => ({ items: [], revision: 0 }))) as { items: unknown; revision: number })
  }
  if (!res.ok) {
    const text = (await res.text().catch(() => '')).trim()
    throw new HostConfigApiError(res.status, text || `${res.status} ${res.statusText}`.trim())
  }
  return parseTaskGroups({ taskGroups: await res.json() })
}
