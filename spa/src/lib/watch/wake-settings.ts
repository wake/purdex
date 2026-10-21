// spa/src/lib/watch/wake-settings.ts — the watcher's wake settings document (`/api/watch/wake-settings`, spec §5): its shape, how it is read and written, and the daemons that serve it.
// The `settings` object is kept as the daemon sent it (never rebuilt from a type), so a field this App does not know goes back unchanged.
import { pinnedHostFetch } from '../host-api'
import { useHostStore } from '../../stores/useHostStore'
import { useNexHostStore } from '../../stores/useNexHostStore'

export const WAKE_SETTINGS_CAPABILITY = 'watch.wake_settings.v1'
export const WAKE_RULES_CAPABILITY = 'watch.wake_rules.v1'

/** The connected daemons announcing `cap`, in host order (a daemon whose `/api/info` is not read since the last connection is not believed). */
function daemonsWith(cap: string): string[] {
  const { byHost } = useNexHostStore.getState()
  const { hostOrder, runtime } = useHostStore.getState()
  return hostOrder.filter((id) => runtime[id]?.status === 'connected' && (byHost[id]?.fetchedAt ?? 0) > 0 && byHost[id]?.daemonCapabilities?.includes(cap))
}
export const wakeSettingsDaemons = (): string[] => daemonsWith(WAKE_SETTINGS_CAPABILITY)
export const wakeRulesDaemons = (): string[] => daemonsWith(WAKE_RULES_CAPABILITY)

export type WakeSettingsObject = Record<string, unknown>
export interface KnownKind { kind: string; levels: string[]; label: string }
export interface SuppressedRecord { at: string; host: string; reason: string; why: string; detail: string }
export interface PatrolStatus { lastOkAt: string | null; lastOkHost: string | null; lastFailAt: string | null; lastFailCode: string | null; lastFailDetail: string | null }

export interface WakeSettingsDoc {
  settings: WakeSettingsObject
  etag: string
  revision: number
  source: string
  /** The source daemon's host_id; `""` is "never read": it names nobody and is not compared with a `daemonId`. */
  sourceId: string
  isSource: boolean
  stale: boolean
  readonly: boolean
  fetchedAt: string | null
  knownKinds: KnownKind[]
  suppressed: SuppressedRecord[]
  patrolStatus: PatrolStatus
  /** A document of another version, a `readonly` one or one never read cannot be edited. */
  writable: boolean
}

export interface InvalidField { path: string; code: string; message: string }

export type WakeResult =
  | { kind: 'ok'; doc: WakeSettingsDoc }
  | { kind: 'conflict'; current: WakeSettingsDoc }
  | { kind: 'invalid'; fields: InvalidField[] }
  | { kind: 'forbidden' }
  | { kind: 'unreachable'; source: string }
  | { kind: 'app_mismatch' }
  | { kind: 'server_error'; status: number }
  | { kind: 'no_answer' }

const SUPPORTED_VERSION = 1

const isObj = (v: unknown): v is Record<string, unknown> => typeof v === 'object' && v !== null && !Array.isArray(v)
const isStr = (v: unknown): v is string => typeof v === 'string'
const strOrNull = (v: unknown): string | null | undefined => (v === null || isStr(v) ? v : undefined)

function parseStatus(v: unknown): PatrolStatus | null {
  if (!isObj(v)) return null
  const [lastOkAt, lastOkHost, lastFailAt, lastFailCode, lastFailDetail] = ['last_ok_at', 'last_ok_host', 'last_fail_at', 'last_fail_code', 'last_fail_detail'].map((k) => strOrNull(v[k]))
  if ([lastOkAt, lastOkHost, lastFailAt, lastFailCode, lastFailDetail].some((x) => x === undefined)) return null
  return { lastOkAt: lastOkAt!, lastOkHost: lastOkHost!, lastFailAt: lastFailAt!, lastFailCode: lastFailCode!, lastFailDetail: lastFailDetail! }
}

const isBool = (v: unknown): v is boolean => typeof v === 'boolean'
const isNum = (v: unknown): v is number => typeof v === 'number'
const isStrList = (v: unknown): boolean => Array.isArray(v) && v.every(isStr)
type Check = (v: unknown) => boolean
const hasShape = (v: unknown, shape: Record<string, Check>): v is Record<string, unknown> => isObj(v) && Object.entries(shape).every(([k, check]) => check(v[k]))
const objOf = (shape: Record<string, Check>): Check => (v) => hasShape(v, shape)
const SITUATIONS = { resting: isNum, present: isNum, away: isNum, busy: isNum, calm: isNum }
const RULES: Record<string, Check> = {
  patrol: objOf({ prompt: isStr, interval_min: objOf(SITUATIONS), max_gap_hours: isNum, times: isStrList, tz: isStr }),
  disk_low: objOf({ enabled: isBool, prompt: isStr, below_gb: isNum }),
  wwd_alert: objOf({ enabled: isBool, prompt: isStr, levels: isStrList, kinds_off: isStrList }),
  unattended_changed: objOf({ enabled: isBool, prompt: isStr }),
  user_back: objOf({ enabled: isBool, prompt: isStr, away_min: isNum }),
}
/** Every field this App draws is there with its type; fields it does not know are not looked at. A document of another version has its own shape and is only read (it is never writable). */
function validSettings(s: Record<string, unknown>): boolean {
  if (!isNum(s.version)) return false
  if (s.version !== SUPPORTED_VERSION) return true
  return isBool(s.enabled) && isBool(s.auto_start) && hasShape(s.rules, RULES)
}

/** A GET body (and a PUT 200's, and a 409's `current`, which have the same shape); `null` when it is not that shape. */
export function parseWakeSettingsResponse(body: unknown): WakeSettingsDoc | null {
  if (!isObj(body) || !isObj(body.settings) || !validSettings(body.settings)) return null
  const { etag, revision, source, source_id: sourceId, is_source: isSource, stale, readonly: ro, fetched_at: fetchedAt } = body
  if (!isStr(etag) || typeof revision !== 'number' || !isStr(source) || !isStr(sourceId)) return null
  if (typeof isSource !== 'boolean' || typeof stale !== 'boolean' || typeof ro !== 'boolean') return null
  const fetched = strOrNull(fetchedAt)
  if (fetched === undefined) return null
  if (!Array.isArray(body.known_kinds) || !Array.isArray(body.suppressed)) return null
  const knownKinds: KnownKind[] = []
  for (const k of body.known_kinds) {
    if (!isObj(k) || !isStr(k.kind) || !isStr(k.label) || !Array.isArray(k.levels) || !k.levels.every(isStr)) return null
    knownKinds.push({ kind: k.kind, levels: k.levels, label: k.label })
  }
  const suppressed: SuppressedRecord[] = []
  for (const r of body.suppressed) {
    if (!isObj(r) || !isStr(r.at) || !isStr(r.host) || !isStr(r.reason) || !isStr(r.why) || !isStr(r.detail)) return null
    suppressed.push({ at: r.at, host: r.host, reason: r.reason, why: r.why, detail: r.detail })
  }
  const patrolStatus = parseStatus(body.patrol_status)
  if (!patrolStatus) return null
  const neverRead = fetched === null && stale
  return {
    settings: body.settings, etag, revision, source, sourceId, isSource, stale, readonly: ro, fetchedAt: fetched, knownKinds, suppressed, patrolStatus,
    writable: body.settings.version === SUPPORTED_VERSION && !ro && !neverRead,
  }
}

const segments = (path: string): string[] => path.split('.')

/** The value at a dotted path (`rules.patrol.max_gap_hours`), or `undefined`. */
export function readPath(obj: unknown, path: string): unknown {
  let cur: unknown = obj
  for (const s of segments(path)) {
    if (!isObj(cur)) return undefined
    cur = cur[s]
  }
  return cur
}

/** A copy of `obj` with the dotted path set to `value`; everything else (fields this App does not know too) is carried over. */
export function withPath<T extends Record<string, unknown>>(obj: T, path: string, value: unknown): T {
  const [head, ...rest] = segments(path)
  if (rest.length === 0) return { ...obj, [head]: value }
  const child = isObj(obj[head]) ? obj[head] : {}
  return { ...obj, [head]: withPath(child, rest.join('.'), value) }
}

const URL_PATH = '/api/watch/wake-settings'

async function jsonOf(res: Response): Promise<unknown> {
  try { return await res.json() } catch { return undefined }
}

/** Classify one response by status. A body that is not what its status promises is `app_mismatch`; only a `fetch` that throws is `no_answer`. */
async function classify(res: Response): Promise<WakeResult> {
  const { status } = res
  if (status !== 200 && status !== 400 && status !== 403 && status !== 409 && status !== 503) return { kind: 'server_error', status }
  const body = await jsonOf(res)
  if (status === 200) {
    const doc = parseWakeSettingsResponse(body)
    return doc ? { kind: 'ok', doc } : { kind: 'app_mismatch' }
  }
  if (!isObj(body)) return { kind: 'app_mismatch' }
  if (status === 409) {
    const current = body.error === 'etag_mismatch' ? parseWakeSettingsResponse(body.current) : null
    return current ? { kind: 'conflict', current } : { kind: 'app_mismatch' }
  }
  if (status === 403) return body.error === 'device_forbidden' ? { kind: 'forbidden' } : { kind: 'app_mismatch' }
  if (status === 503) return body.error === 'source_unreachable' && isStr(body.source) ? { kind: 'unreachable', source: body.source } : { kind: 'app_mismatch' }
  // 400
  if (body.error !== 'invalid' || !Array.isArray(body.fields)) return { kind: 'app_mismatch' }
  const fields: InvalidField[] = []
  for (const f of body.fields) {
    if (!isObj(f) || !isStr(f.path) || !isStr(f.code)) return { kind: 'app_mismatch' }
    fields.push({ path: f.path, code: f.code, message: isStr(f.message) ? f.message : '' })
  }
  return { kind: 'invalid', fields }
}

async function call(hostId: string, init?: RequestInit): Promise<WakeResult> {
  let res: Response
  // Pinned: a host removed since it was picked must not turn into "the active host", or a PUT would write another daemon's settings.
  try { res = await pinnedHostFetch(hostId, URL_PATH, init) } catch { return { kind: 'no_answer' } }
  try { return await classify(res) } catch { return { kind: 'no_answer' } }
}

export const fetchWakeSettings = (hostId: string): Promise<WakeResult> => call(hostId)

export const putWakeSettings = (hostId: string, settings: WakeSettingsObject, expectEtag: string): Promise<WakeResult> =>
  call(hostId, { method: 'PUT', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ settings, expect_etag: expectEtag }) })
