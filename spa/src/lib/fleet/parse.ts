// spa/src/lib/fleet/parse.ts — strict reading of the fleet payload (fleet spec §6). The body is unchecked JSON from a
// daemon: a value of the wrong type becomes `unknown` / null rather than a wrong number on screen, an error code the App
// does not know becomes `parse`, and nothing here ever surfaces text the daemon sent (SSH's words never leave it).
import { isHostColorSet, isIconWeight, HOST_COLOR_MODES } from '../host-color'
import type { HostConfig } from '../../stores/useHostStore'
import {
  PROBE_ERROR_CODES,
  type FleetNumbers, type FleetPayload, type FleetProbe, type FleetTrend, type FleetZone, type ProbeErrorCode,
  type ProbeHostDef, type ProbeStatus, type VersionedProbeHosts,
} from './types'

const isObj = (v: unknown): v is Record<string, unknown> => typeof v === 'object' && v !== null && !Array.isArray(v)
const num = (v: unknown): number | null => (typeof v === 'number' && Number.isFinite(v) ? v : null)

const ZONES: readonly string[] = ['normal', 'warning', 'danger', 'unknown']
const TRENDS: readonly string[] = ['rising', 'flat', 'falling', 'unknown']
const STATUSES: readonly string[] = ['ok', 'offline', 'error']

function numbers(o: Record<string, unknown>): FleetNumbers {
  return {
    cpu: num(o.cpu), mem: num(o.mem), disk: num(o.disk), load1: num(o.load1), ncpu: num(o.ncpu),
    zone: (typeof o.zone === 'string' && ZONES.includes(o.zone) ? o.zone : 'unknown') as FleetZone,
    trend: (typeof o.trend === 'string' && TRENDS.includes(o.trend) ? o.trend : 'unknown') as FleetTrend,
    sampled_at: num(o.sampled_at),
  }
}

function colorsOf(v: unknown): HostConfig['colors'] {
  if (!isObj(v)) return undefined
  const out: NonNullable<HostConfig['colors']> = {}
  for (const mode of HOST_COLOR_MODES) {
    const set = v[mode]
    if (isHostColorSet(set)) out[mode] = set
  }
  return Object.keys(out).length > 0 ? out : undefined
}

/** One stored probe-only host; a row without a string id / name / ssh_alias is dropped (null). */
export function parseProbeHostDef(v: unknown): ProbeHostDef | null {
  if (!isObj(v)) return null
  if (typeof v.id !== 'string' || v.id === '' || typeof v.name !== 'string' || typeof v.ssh_alias !== 'string') return null
  const def: ProbeHostDef = { id: v.id, name: v.name, ssh_alias: v.ssh_alias }
  if (typeof v.icon === 'string' && v.icon !== '') def.icon = v.icon
  if (isIconWeight(v.icon_weight)) def.icon_weight = v.icon_weight
  const colors = colorsOf(v.colors)
  if (colors) def.colors = colors
  return def
}

export function parseVersionedProbeHosts(v: unknown): VersionedProbeHosts | null {
  if (!isObj(v) || !Array.isArray(v.items) || typeof v.revision !== 'number') return null
  const items = v.items.map(parseProbeHostDef).filter((d): d is ProbeHostDef => d !== null)
  return { items, revision: v.revision }
}

function parseProbe(v: unknown): FleetProbe | null {
  const def = parseProbeHostDef(v)
  if (!def || !isObj(v)) return null
  const knownStatus = typeof v.status === 'string' && STATUSES.includes(v.status)
  const status: ProbeStatus = knownStatus ? (v.status as ProbeStatus) : 'error'
  let error: ProbeErrorCode | null = null
  if (!knownStatus) error = 'parse'
  else if (typeof v.error === 'string' && v.error !== '') {
    error = (PROBE_ERROR_CODES as readonly string[]).includes(v.error) ? (v.error as ProbeErrorCode) : 'parse'
  }
  return {
    ...def, ...numbers(v), status, error,
    os: typeof v.os === 'string' ? v.os : '',
    last_ok_at: num(v.last_ok_at),
  }
}

/** The whole payload, or null when the body is not the shape at all. */
export function parseFleetPayload(v: unknown): FleetPayload | null {
  if (!isObj(v) || !Array.isArray(v.probes) || !('self' in v)) return null
  return {
    self: isObj(v.self) ? numbers(v.self) : null,
    probes: v.probes.map(parseProbe).filter((p): p is FleetProbe => p !== null),
  }
}
