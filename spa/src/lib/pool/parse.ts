// spa/src/lib/pool/parse.ts — the trust boundary of the pool wire. A malformed host row is dropped whole; malformed facts make
// that host's facts null (nothing is invented); optional #3478 fields that do not parse are left out; a malformed envelope is null.
import {
  LIVENESS, ZONES,
  type FactsWorker, type HeadroomReasons, type PoolDisk, type PoolException, type PoolFacts, type PoolFactsHistory, type PoolHost, type PoolLiveness, type PoolLoad,
  type PoolCore, type PoolDiskIO, type PoolOwner, type PoolProc, type PoolSelf, type PoolPsi, type PoolResident, type PoolSeries, type PoolWorker, type PoolZone, type SeriesDisk, type SeriesPoint, type Slots,
} from './types'

type Rec = Record<string, unknown>
export const isRec = (v: unknown): v is Rec => typeof v === 'object' && v !== null && !Array.isArray(v)
export const num = (v: unknown): v is number => typeof v === 'number' && Number.isFinite(v)
export const str = (v: unknown): v is string => typeof v === 'string'
const numOrNull = (v: unknown): number | null => (num(v) ? v : null)
export const time = (v: unknown): number | null => {
  if (!str(v) || !/^\d{4}-\d\d-\d\dT/.test(v)) return null
  const t = Date.parse(v)
  return Number.isFinite(t) ? t : null
}

/** A zone the App does not know reads as `unknown`, never as an error. */
export const zoneOf = (v: unknown): PoolZone => ((ZONES as readonly unknown[]).includes(v) ? (v as PoolZone) : 'unknown')

/** A word the App does not know reads as `unknown`, never as `ok`. */
const livenessOf = (v: unknown): PoolLiveness => ((LIVENESS as readonly unknown[]).includes(v) ? (v as PoolLiveness) : 'unknown')

const slots = (v: unknown): Slots | null => (isRec(v) && num(v.heavy) && num(v.light) ? { heavy: v.heavy, light: v.light } : null)

// Strict: a missing or malformed pair of slots would read as "no capacity", a missing owner_rule as "no rule".
function worker(v: unknown): PoolWorker | null {
  const own = isRec(v) ? slots(v.slots) : null
  if (!isRec(v) || typeof v.enabled !== 'boolean' || !own || !str(v.owner_rule)) return null
  return { enabled: v.enabled, slots: own, owner_rule: v.owner_rule }
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

/** A list the wire must carry: an array, or null (a Go nil slice) read as empty. Anything else — including absent — is undefined. A malformed element is dropped. */
export function list<T>(v: unknown, one: (x: unknown) => T | null): T[] | undefined {
  if (v === null) return []
  return Array.isArray(v) ? v.flatMap((x) => { const r = one(x); return r === null ? [] : [r] }) : undefined
}

/** As `list`, but one malformed element makes the whole list undefined: a shorter disk or exception list would be read as the truth. */
function strictList<T>(v: unknown, one: (x: unknown) => T | null): T[] | undefined {
  const out = list(v, one)
  return out && out.length === (Array.isArray(v) ? v.length : 0) ? out : undefined
}

const resident = (r: unknown): PoolResident | null =>
  isRec(r) && str(r.session) && str(r.kind) && num(r.count) && num(r.mem_mb) ? { session: r.session, kind: r.kind, count: r.count, mem_mb: r.mem_mb } : null

const disk = (d: unknown): PoolDisk | null =>
  isRec(d) && str(d.mount) && num(d.free_gb) && num(d.used_pct) && num(d.total_bytes) && num(d.avail_bytes)
    ? { mount: d.mount, free_gb: d.free_gb, used_pct: d.used_pct, total_bytes: d.total_bytes, avail_bytes: d.avail_bytes }
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

// The host-metrics fields (G-2 to G-4): each is read on its own, and one that is absent or does not parse is left out while the record stays.
const optNum = (v: unknown): number | undefined => (num(v) ? v : undefined)
/** The entries of `o` that are set: an absent field is a missing key, not an `undefined` one. */
const present = <T extends Record<string, unknown>>(o: T): Partial<T> => Object.fromEntries(Object.entries(o).filter(([, x]) => x !== undefined)) as Partial<T>

function core(c: unknown): PoolCore | null {
  if (!isRec(c)) return null
  const share = (x: unknown): number | null | undefined => (x === null ? null : num(x) ? x : undefined)
  const user = share(c.user_pct)
  const system = share(c.system_pct)
  const idle = share(c.idle_pct)
  const kind = c.kind === 'p' || c.kind === 'e' || c.kind === null ? c.kind : undefined
  return user === undefined || system === undefined || idle === undefined || kind === undefined ? null : { user_pct: user, system_pct: system, idle_pct: idle, kind }
}

/** One malformed core takes the whole list out: a list one core short would put every later core under the wrong number. */
const cores = (v: unknown): PoolCore[] | undefined => {
  if (!Array.isArray(v)) return undefined
  const out = strictList(v, core)
  return out && out.length > 0 ? out : undefined
}

function diskIO(d: unknown): PoolDiskIO | null {
  return isRec(d) && str(d.device) && d.device !== ''
    ? { device: d.device, read_bps: numOrNull(d.read_bps), write_bps: numOrNull(d.write_bps), read_iops: numOrNull(d.read_iops), write_iops: numOrNull(d.write_iops) }
    : null
}

/** Each entry names its own disk, so a malformed one is dropped alone. */
const diskIOs = (v: unknown): PoolDiskIO[] | undefined => {
  if (!Array.isArray(v)) return undefined
  const out = list(v, diskIO)
  return out && out.length > 0 ? out : undefined
}

/** How many processes: a non-negative whole number, else absent (facts and series both). */
const procCount = (v: unknown): number | undefined => (num(v) && Number.isInteger(v) && v >= 0 ? v : undefined)

const self = (v: unknown): PoolSelf | undefined => (isRec(v) ? { cpu_pct: numOrNull(v.cpu_pct), rss_mb: numOrNull(v.rss_mb) } : undefined)

/** The facts records this App reads: wire `v` 1 to 2 (internal/wwd/wire doc.go MinProto..FactsProto). A newer one may mean different fields. */
const FACTS_MIN_V = 1
const FACTS_MAX_V = 2

export function parseFacts(v: unknown): PoolFacts | null {
  if (!isRec(v) || !Number.isInteger(v.v) || (v.v as number) < FACTS_MIN_V || (v.v as number) > FACTS_MAX_V) return null
  const at = time(v.at)
  const { cpu, mem, derived } = v
  const w = factsWorker(v.worker)
  if (at === null || !str(v.host_id) || !str(v.alias) || !isRec(cpu) || !isRec(mem) || !isRec(derived) || !w) return null
  const headroom = slots(derived.headroom)
  if (!num(cpu.ncpu) || !num(cpu.load1) || !num(cpu.load5) || !num(cpu.idle_pct) || !num(mem.total_mb) || !num(mem.available_mb) || !headroom) return null
  const disks = strictList(v.disk, disk)
  const procs = list(v.procs, proc)
  const residents = strictList(v.residents, resident)
  const exceptions = strictList(v.exceptions, exception)
  if (!str(v.platform) || !str(v.arch) || !str(v.os_version) || !num(mem.pressure) || !num(derived.sessions_room)) return null
  if (!disks || !procs || !residents || !exceptions || !isRec(v.tools)) return null
  const tools: PoolFacts['tools'] = {}
  for (const [k, t] of Object.entries(v.tools)) {
    if (t === null || str(t)) tools[k] = t
    else if (Array.isArray(t) && t.every(str)) tools[k] = t
  }
  return {
    v: v.v as number, host_id: v.host_id, alias: v.alias, at,
    platform: v.platform, arch: v.arch, os_version: v.os_version,
    cpu: { ncpu: cpu.ncpu, load1: cpu.load1, load5: cpu.load5, idle_pct: cpu.idle_pct, ...present({ load15: optNum(cpu.load15), user_pct: optNum(cpu.user_pct), system_pct: optNum(cpu.system_pct), cores: cores(cpu.cores) }) },
    mem: {
      total_mb: mem.total_mb, available_mb: mem.available_mb, pressure: mem.pressure,
      page_out_rate: numOrNull(mem.page_out_rate), swap_out_rate: numOrNull(mem.swap_out_rate),
      swap_used_mb: numOrNull(mem.swap_used_mb), compressor_mb: numOrNull(mem.compressor_mb), psi: psi(mem.psi),
      ...present({ decompress_rate: optNum(mem.decompress_rate), swap_total_mb: optNum(mem.swap_total_mb), wired_mb: optNum(mem.wired_mb), active_mb: optNum(mem.active_mb), inactive_mb: optNum(mem.inactive_mb), file_cache_mb: optNum(mem.file_cache_mb) }),
    },
    disk: disks,
    procs,
    residents,
    tools,
    owner: owner(v.owner),
    exceptions,
    derived: { zone: zoneOf(derived.zone), headroom, sessions_room: derived.sessions_room },
    worker: w,
    ...(isRec(v.queue) && num(v.queue.queued) ? { queue: { queued: v.queue.queued } } : {}),
    ...present({ procs_total: procCount(v.procs_total), boot_at: time(v.boot_at) ?? undefined, self: self(v.self), disk_io: diskIOs(v.disk_io) }),
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

/** `info.fingerprint`: the string values only; nothing left, or not an object, is no fingerprint. */
function fingerprint(v: unknown): Record<string, string> | undefined {
  if (!isRec(v)) return undefined
  const out = Object.fromEntries(Object.entries(v).filter((e): e is [string, string] => str(e[1])))
  return Object.keys(out).length > 0 ? out : undefined
}

function host(v: unknown): PoolHost | null {
  if (!isRec(v) || !isRec(v.info)) return null
  const { info } = v
  if (!str(info.host_id) || !str(info.alias) || !str(info.version) || !num(info.proto) || !str(info.platform) || !str(info.arch)) return null
  // worker, labels and reachable are what the cell says about capacity and reach: absent or mistyped they would read as "off, none, down".
  const w = worker(v.worker)
  const labels = v.labels === null ? [] : strings(v.labels)
  if (!w || !labels || typeof v.reachable !== 'boolean') return null
  const hr = reasons(v.headroom_reasons)
  const ld = load(v.load)
  const at = time(v.liveness_at)
  const since = time(v.liveness_since)
  const fp = fingerprint(info.fingerprint)
  return {
    host_id: info.host_id, alias: info.alias, version: info.version, proto: info.proto, platform: info.platform, arch: info.arch,
    facts: parseFacts(v.facts),
    worker: w,
    labels,
    reachable: v.reachable,
    ...(hr ? { headroom_reasons: hr } : {}),
    ...(ld ? { load: ld } : {}),
    ...(v.liveness !== undefined ? { liveness: livenessOf(v.liveness) } : {}),
    ...(at !== null ? { liveness_at: at } : {}),
    ...(since !== null ? { liveness_since: since } : {}),
    ...(num(v.rtt_ms) && v.rtt_ms >= 0 ? { rtt_ms: v.rtt_ms } : {}),
    ...(fp ? { fingerprint: fp } : {}),
  }
}

/** `GET /api/pool/hosts`. */
export function parseHostsAnswer(v: unknown): PoolHost[] | null {
  if (!isRec(v) || !Array.isArray(v.hosts)) return null
  const hosts = list(v.hosts, host) ?? []
  // every row malformed: the answer is outside the contract (the caller reads it as wwd_bad_response), not an empty pool
  return v.hosts.length > 0 && hosts.length === 0 ? null : hosts
}

function point(p: unknown): SeriesPoint | null {
  if (!isRec(p)) return null
  const at = time(p.at)
  const headroom = slots(p.headroom)
  if (at === null || !num(p.cpu_idle_pct) || !num(p.mem_available_mb) || !headroom) return null
  const out: SeriesPoint = {
    at, zone: zoneOf(p.zone), cpu_idle_pct: p.cpu_idle_pct, mem_available_mb: p.mem_available_mb,
    pressure: num(p.pressure) ? p.pressure : 0, page_out_rate: numOrNull(p.page_out_rate), disk_max_used_pct: numOrNull(p.disk_max_used_pct), headroom,
  }
  // G-1: a field that is not there or does not parse is left out, the point stays.
  if (num(p.load1)) out.load1 = p.load1
  if (num(p.load5)) out.load5 = p.load5
  if (num(p.swap_used_mb)) out.swap_used_mb = p.swap_used_mb
  if (num(p.swap_out_rate)) out.swap_out_rate = p.swap_out_rate
  const running = slots(p.worker_running)
  if (running) out.worker_running = running
  if (num(p.queue_queued)) out.queue_queued = p.queue_queued
  // G-2 to G-4, the same way.
  for (const k of ['load15', 'cpu_user_pct', 'cpu_system_pct', 'compressor_mb', 'decompress_rate', 'swap_total_mb', 'self_cpu_pct', 'self_rss_mb'] as const) {
    const x = p[k]
    if (num(x)) out[k] = x
  }
  const total = procCount(p.procs_total)
  if (total !== undefined) out.procs_total = total
  const dio = diskIOs(p.disk_io)
  if (dio) out.disk_io = dio
  if (Array.isArray(p.disks)) out.disks = p.disks.flatMap((d): SeriesDisk[] => (isRec(d) && str(d.mount) && num(d.used_pct) && num(d.free_gb) ? [{ mount: d.mount, used_pct: d.used_pct, free_gb: d.free_gb }] : []))
  return out
}

/** `GET /api/pool/facts/series`. */
/** A record that does not parse is dropped, not the whole answer: the chart has a gap, which is honest. */
export function parseFactsHistory(v: unknown): PoolFactsHistory | null {
    if (!isRec(v) || (v.step !== '1m' && v.step !== '5s') || !Array.isArray(v.records)) return null
    const records = v.records.map(parseFacts).filter((f): f is PoolFacts => f !== null)
    return { step: v.step, records }
}

export function parseSeries(v: unknown): PoolSeries | null {
  if (!isRec(v) || (v.step !== '1m' && v.step !== '5s') || typeof v.truncated !== 'boolean') return null
  const points = list(v.points, point)
  return points ? { step: v.step, points, truncated: v.truncated } : null
}
