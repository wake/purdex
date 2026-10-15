// spa/src/lib/fleet/__fixtures__/builders.ts — wire fixtures written from fleet spec §6 (test-only; the daemon side is not built yet).
import type { FleetAnswer, FleetFast, FleetHost } from '../types'

export const fast = (over: Partial<FleetFast> = {}): FleetFast => ({
  at: 1_000_000, cpu: 20, mem: 50, level: 1, page_rate: 0, disk: 40, disk_free_gb: 100, load1: 1, ncpu: 10, zone: 'normal', load: 50, ...over,
})

export const host = (id: string, over: Partial<FleetHost> = {}): FleetHost => ({
  id, kind: 'daemon', alias: id, name: id, icon: 'desktop', os: 'darwin', status: 'ok', error: null, last_ok_at: 1_000_000,
  fast: fast(), slow: null, ...over,
})

export const primaryAnswer = (term: number, hosts: FleetHost[]): FleetAnswer => ({ role: 'primary', term, at: 1_000_000, hosts })
export const standbyAnswer = (term: number, primary: string): FleetAnswer => ({ role: 'standby', term, primary })

/** A reading as it goes over the wire: `at` is an RFC3339 string (the parsed shape holds ms). */
export const wireFast = (f: FleetFast): Record<string, unknown> => ({ ...f, at: new Date(f.at).toISOString() })
