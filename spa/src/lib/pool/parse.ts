// spa/src/lib/pool/parse.ts — the trust boundary of the pool wire. A malformed host row is dropped whole; malformed facts make
// that host's facts null (nothing is invented); optional #3478 fields that do not parse are left out; a malformed envelope is null.
import {
  ZONES,
  type FactsWorker, type HeadroomReasons, type PoolDisk, type PoolException, type PoolFacts, type PoolHost, type PoolLoad,
  type PoolOwner, type PoolProc, type PoolPsi, type PoolResident, type PoolSeries, type PoolWorker, type PoolZone, type SeriesPoint, type Slots,
} from './types'

type Rec = Record<string, unknown>
const isRec = (v: unknown): v is Rec => typeof v === 'object' && v !== null && !Array.isArray(v)
const num = (v: unknown): v is number => typeof v === 'number' && Number.isFinite(v)
const str = (v: unknown): v is string => typeof v === 'string'
const numOrNull = (v: unknown): number | null => (num(v) ? v : null)
const time = (v: unknown): number | null => {
  if (!str(v) || !/^\d{4}-\d\d-\d\dT/.test(v)) return null
  const t = Date.parse(v)
  return Number.isFinite(t) ? t : null
}

/** A zone the App does not know reads as `unknown`, never as an error. */
export const zoneOf = (v: unknown): PoolZone => ((ZONES as readonly unknown[]).includes(v) ? (v as PoolZone) : 'unknown')

const slots = (v: unknown): Slots | null => (isRec(v) && num(v.heavy) && num(v.light) ? { heavy: v.heavy, light: v.light } : null)

function worker(v: unknown): PoolWorker | null {
  if (!isRec(v) || typeof v.enabled !== 'boolean') return null
  return { enabled: v.enabled, slots: slots(v.slots) ?? { heavy: 0, light: 0 }, owner_rule: str(v.owner_rule) ? v.owner_rule : '' }
}

function factsWorker(v: unknown): FactsWorker | null {
  const w = worker(v)
  const running = isRec(v) ? slots(v.running) : null
  return w && running ? { ...w, running } : null
}

function proc(p: unknown): PoolProc | null {
  if (!isRec(p) || !num(p.pid) || !str(p.name) || !num(p.cpu_pct) || !num(p.mem_mb)) return null
  const o = p.owner
  const owner = isRec(o) && str(o.kind)
    ? { kind: o.kind, ...(str(o.id) ? { id: o.id } : {}), ...(str(o.session) ? { session: o.session } : {}) }
    : null
  return { pid: p.pid, name: p.name, cpu_pct: p.cpu_pct, mem_mb: p.mem_mb, mem_kind: str(p.mem_kind) ? p.mem_kind : 'rss', owner }
}

function list<T>(v: unknown, one: (x: unknown) => T | null): T[] {
  return Array.isArray(v) ? v.flatMap((x) => { const r = one(x); return r === null ? [] : [r] }) : []
}

const resident = (r: unknown): PoolResident | null =>
  isRec(r) && str(r.session) && str(r.kind) && num(r.count) && num(r.mem_mb) ? { session: r.session, kind: r.kind, count: r.count, mem_mb: r.mem_mb } : null

const disk = (d: unknown): PoolDisk | null =>
  isRec(d) && str(d.mount) && num(d.free_gb) && num(d.used_pct)
    ? { mount: d.mount, free_gb: d.free_gb, used_pct: d.used_pct, total_bytes: num(d.total_bytes) ? d.total_bytes : 0, avail_bytes: num(d.avail_bytes) ? d.avail_bytes : 0 }
    : null

function exception(e: unknown): PoolException | null {
  if (!isRec(e) || !str(e.name)) return null
  const since = time(e.since)
  return since === null ? null : { name: e.name, since, until: time(e.until) }
}

function psi(v: unknown): PoolPsi | null {
  return isRec(v) && num(v.some10) && num(v.full10) && num(v.some60) ? { some10: v.some10, full10: v.full10, some60: v.some60 } : null
}

function owner(v: unknown): PoolOwner | null {
  return isRec(v) && typeof v.active === 'boolean' && num(v.idle_s) ? { active: v.active, idle_s: v.idle_s, reason: str(v.reason) ? v.reason : '' } : null
}

export function parseFacts(v: unknown): PoolFacts | null {
  if (!isRec(v)) return null
  const at = time(v.at)
  const { cpu, mem, derived } = v
  const w = factsWorker(v.worker)
  if (at === null || !str(v.host_id) || !str(v.alias) || !isRec(cpu) || !isRec(mem) || !isRec(derived) || !w) return null
  const headroom = slots(derived.headroom)
  if (!num(cpu.ncpu) || !num(cpu.load1) || !num(cpu.load5) || !num(cpu.idle_pct) || !num(mem.total_mb) || !num(mem.available_mb) || !headroom) return null
  const tools: PoolFacts['tools'] = {}
  if (isRec(v.tools)) {
    for (const [k, t] of Object.entries(v.tools)) {
      if (t === null || str(t)) tools[k] = t
      else if (Array.isArray(t) && t.every(str)) tools[k] = t
    }
  }
  return {
    host_id: v.host_id, alias: v.alias, at,
    platform: str(v.platform) ? v.platform : '', arch: str(v.arch) ? v.arch : '', os_version: str(v.os_version) ? v.os_version : '',
    cpu: { ncpu: cpu.ncpu, load1: cpu.load1, load5: cpu.load5, idle_pct: cpu.idle_pct },
    mem: {
      total_mb: mem.total_mb, available_mb: mem.available_mb, pressure: num(mem.pressure) ? mem.pressure : 0,
      page_out_rate: numOrNull(mem.page_out_rate), swap_out_rate: numOrNull(mem.swap_out_rate),
      swap_used_mb: num(mem.swap_used_mb) ? mem.swap_used_mb : 0, compressor_mb: numOrNull(mem.compressor_mb), psi: psi(mem.psi),
    },
    disk: list(v.disk, disk),
    procs: list(v.procs, proc),
    residents: list(v.residents, resident),
    tools,
    owner: owner(v.owner),
    exceptions: list(v.exceptions, exception),
    derived: { zone: zoneOf(derived.zone), headroom, sessions_room: num(derived.sessions_room) ? derived.sessions_room : 0 },
    worker: w,
    ...(isRec(v.queue) && num(v.queue.queued) ? { queue: { queued: v.queue.queued } } : {}),
  }
}

const strings = (v: unknown): string[] | null => (Array.isArray(v) && v.every(str) ? v : null)

function reasons(v: unknown): HeadroomReasons | undefined {
  if (!isRec(v)) return undefined
  const heavy = strings(v.heavy)
  const light = strings(v.light)
  return heavy && light ? { heavy, light } : undefined
}

function load(v: unknown): PoolLoad | undefined {
  return isRec(v) && num(v.score) && str(v.driver) ? { score: v.score, driver: v.driver } : undefined
}

function host(v: unknown): PoolHost | null {
  if (!isRec(v) || !isRec(v.info)) return null
  const { info } = v
  if (!str(info.host_id) || !str(info.alias)) return null
  const w = worker(v.worker) ?? worker(info.worker) ?? { enabled: false, slots: { heavy: 0, light: 0 }, owner_rule: '' }
  const hr = reasons(v.headroom_reasons)
  const ld = load(v.load)
  return {
    host_id: info.host_id, alias: info.alias,
    version: str(info.version) ? info.version : '', proto: num(info.proto) ? info.proto : 0,
    platform: str(info.platform) ? info.platform : '', arch: str(info.arch) ? info.arch : '',
    facts: parseFacts(v.facts),
    worker: w,
    labels: strings(v.labels) ?? [],
    reachable: v.reachable === true,
    ...(hr ? { headroom_reasons: hr } : {}),
    ...(ld ? { load: ld } : {}),
  }
}

/** `GET /api/pool/hosts`. */
export function parseHostsAnswer(v: unknown): PoolHost[] | null {
  if (!isRec(v) || !Array.isArray(v.hosts)) return null
  return list(v.hosts, host)
}

function point(p: unknown): SeriesPoint | null {
  if (!isRec(p)) return null
  const at = time(p.at)
  const headroom = slots(p.headroom)
  if (at === null || !num(p.cpu_idle_pct) || !num(p.mem_available_mb) || !headroom) return null
  return {
    at, zone: zoneOf(p.zone), cpu_idle_pct: p.cpu_idle_pct, mem_available_mb: p.mem_available_mb,
    pressure: num(p.pressure) ? p.pressure : 0, page_out_rate: numOrNull(p.page_out_rate), disk_max_used_pct: numOrNull(p.disk_max_used_pct), headroom,
  }
}

/** `GET /api/pool/facts/series`. */
export function parseSeries(v: unknown): PoolSeries | null {
  if (!isRec(v) || !str(v.step) || !Array.isArray(v.points)) return null
  return { step: v.step, points: list(v.points, point), truncated: v.truncated === true }
}
