// spa/src/lib/fleet/types.ts — the wire shapes of the daemon's fleet module (fleet spec §6, capability `fleet.v1`).
import type { HostConfig } from '../../stores/useHostStore'
import type { IconWeight } from '../../types/tab'

export const FLEET_CAPABILITY = 'fleet.v1'
/** `HostEvent.type` of the push that carries a whole `FleetPayload`. */
export const FLEET_EVENT_TYPE = 'fleet.changed'

export type FleetZone = 'normal' | 'warning' | 'danger' | 'unknown'
export type FleetTrend = 'rising' | 'flat' | 'falling' | 'unknown'
export type ProbeStatus = 'ok' | 'offline' | 'error'
/** Spec §5. The daemon never sends SSH's own words, only one of these. */
export const PROBE_ERROR_CODES = ['timeout', 'unreachable', 'host_key', 'auth', 'remote_exit', 'parse', 'alias_gone'] as const
export type ProbeErrorCode = (typeof PROBE_ERROR_CODES)[number]

/** The numbers every row carries; null = the daemon did not have one (a probe that never answered). */
export interface FleetNumbers {
  cpu: number | null
  mem: number | null
  disk: number | null
  load1: number | null
  ncpu: number | null
  zone: FleetZone
  trend: FleetTrend
  sampled_at: number | null
}

export type FleetSelf = FleetNumbers

/** A probe-only host's definition as the prober's hostconfig stores it (`probeHosts` collection). */
export interface ProbeHostDef {
  id: string
  name: string
  ssh_alias: string
  icon?: string
  icon_weight?: IconWeight
  colors?: HostConfig['colors']
}

export interface FleetProbe extends FleetNumbers, ProbeHostDef {
  os: string
  status: ProbeStatus
  last_ok_at: number | null
  error: ProbeErrorCode | null
}

export interface FleetPayload {
  self: FleetSelf | null
  probes: FleetProbe[]
}

export interface VersionedProbeHosts {
  items: ProbeHostDef[]
  revision: number
}
