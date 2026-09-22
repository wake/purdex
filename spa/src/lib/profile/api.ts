// spa/src/lib/profile/api.ts — the client of the daemon's `profiles` routes
// (Profile Sync spec §4.6 / §4.8; P2b plan Task 3), compare-and-set included.
//
// NOTHING HERE THROWS OR REJECTS. Every function resolves to a discriminated
// union, including when `hostFetch` itself throws synchronously. Two reasons:
// the executor must turn one flight into exactly one terminal event, and "the
// list request failed" must be unrepresentable as "the list is empty".
//
// That covers BUILDING the request too. `encodeURIComponent` throws URIError on
// a lone surrogate and `JSON.stringify` throws TypeError on a BigInt or a cycle
// — values that corrupted persisted state can hand us. So no exported function
// encodes a path or serializes a body itself: each passes `request` a builder,
// which runs it inside its guard and answers `rejected` (status 0, nothing
// sent) when it throws. A throw there would leave a section `inFlight` forever.
//
// Status → body, per route, as written by internal/module/profiles/handler.go
// and handler_sections.go (every 200 and every 409 is JSON via writeJSONStatus;
// every other status is `http.Error` text/plain, the message plus "\n"):
//
//   GET    /api/profiles
//     200  {profiles: [{id, name, createdAt, updatedAt, sections: SectionMeta[],
//          attachments: Attachment[]}]} — both arrays are `[]`, never null
//     500  "internal error"
//   POST   /api/profiles                          body {name}
//     200  Profile {id, name, createdAt, updatedAt}
//     400  unreadable body / invalid JSON / name invalid · 413 body > 64 KiB · 500
//   PATCH  /api/profiles/{id}                     body {name}
//     200  {id, name}
//     400  bad id / body / name · 404 "profile not found" · 413 · 500
//   DELETE /api/profiles/{id}
//     200  {deleted: true}
//     400  bad id · 404 · 409 {reason:"attached", attachments: Attachment[]}
//          (may be [] — they can detach between the refusal and the read) · 500
//   GET    /api/profiles/{id}
//     200  {sections: {<name>: Section}} — live sections only, WITH payloads
//     400  bad id · 404 unknown profile · 503 + Retry-After: 1 (the route answers
//          through writeStoreError) · 500
//   GET    /api/profiles/{id}/sections/{section}
//     200  Section {section, rev, hash, fingerprint, ordinal, payload, writer, updatedAt}
//     400  bad id / section · 404 "section not found" — for a tombstone, a section
//          that never existed AND an unknown profile alike · 500
//   PUT    /api/profiles/{id}/sections/{section}
//          body {clientId, baseRev, hash, fingerprint, ordinal, payload}
//     200  {rev, applied: true} (written) · {rev, applied: false} (converged)
//     400  bad id / section / JSON / missing baseRev or ordinal / bad field /
//          payload not an object
//     404  unknown profile · 413 body or payload over the cap
//     409  {reason:"conflict", rev, hash?, payload?} — against an absent section
//          exactly {reason, rev: 0} (`omitempty` drops both)
//     409  {reason:"schema", fingerprint, ordinal}
//     503  + Retry-After: 1, text/plain — a normal outcome, nothing was written
//     500
//   DELETE /api/profiles/{id}/sections/{section}?baseRev=N&clientId=c_…
//     200  {rev} — NO `applied`; already-gone is a 200 too
//     400 · 404 unknown profile · 409 conflict (same body; never schema) · 503 · 500
//   PUT    /api/profiles/{id}/attachment          body {clientId, deviceName}
//     200  {attached: true}
//     400 · 404 unknown profile · 413 · 500
//   DELETE /api/profiles/{id}/attachment?clientId=c_…
//     200  {detached: boolean} — false when the client is attached elsewhere
//     400 · 404 unknown profile · 500
//
// 401 / 403 come from the daemon's auth middleware, in front of every route.
//
// A `Response` body can be read once. It is read exactly once here, as text,
// and only AFTER the status has picked the branch; JSON is parsed from that
// text, so "the body failed to arrive" (network) and "the body is not what the
// protocol says" (malformed) stay distinguishable.
//
// Timeouts are an AbortController + setTimeout, not `AbortSignal.timeout`:
// measured under vitest 4 / jsdom 29 / Node 24, `AbortSignal.any` and
// `.timeout` both exist, but `.timeout` runs on a runtime-internal timer that
// fake timers cannot advance (measured: 2 s advanced past a 1 s timeout, signal
// still not aborted), so a timeout test would verify nothing. The result is decided
// by which cause fired first, not by what the rejected fetch looks like, and
// the call is raced against that cause so a transport that ignores its signal
// still ends on time.
//
// AN UNKNOWN HOST IS NEVER SENT TO. `hostFetch` resolves its address through
// `useHostStore.getDaemonBase` (stores/useHostStore.ts:318-325), which does not
// throw for a hostId that is not in the store: it falls back to
// `activeHostId ?? hostOrder[0]`, and failing that to 'http://127.0.0.1:7860'.
// For Profile Sync that is a data-safety hole — if the master host leaves the
// store (the user deleted it, or an applied `hosts` section removed it), a
// compare-and-set PUT / DELETE would land on ANOTHER daemon, writing this
// profile's section into the wrong source of truth, with that host's token.
// So `request`, the one entry every function here goes through, checks
// `hosts[hostId]` before anything is sent and answers `unknown-host` instead.
// That check is this file's only use of the store (it is the transport layer,
// not the P2a pure core).

import { useHostStore } from '../../stores/useHostStore'
import { endpointOfHost } from '../../stores/useProfileStore'
import { hostFetch } from '../host-api'

/* ─── wire types ─── */

export interface Profile {
  id: string
  name: string
  createdAt: number
  updatedAt: number
}

/** A client attached to a profile (store.go `Attachment`). */
export interface Attachment {
  clientId: string
  profileId: string
  deviceName: string
  attachedAt: number
  lastSeen: number
}

/** A section without its payload (sections.go `SectionMeta`). */
export interface SectionMeta {
  section: string
  rev: number
  hash: string
  fingerprint: string
  ordinal: number
  writer: string
  updatedAt: number
}

/** A live section (sections.go `Section`). */
export interface Section extends SectionMeta {
  payload: Record<string, unknown>
}

/** One row of `GET /api/profiles`. Tombstones are not listed. */
export interface ProfileIndexEntry extends Profile {
  sections: SectionMeta[]
  attachments: Attachment[]
}

/** The body of a section PUT. The keys are the daemon's JSON tags. */
export interface PutSectionBody {
  clientId: string
  /** 0 = create; otherwise the rev this write is based on. */
  baseRev: number
  hash: string
  fingerprint: string
  ordinal: number
  payload: Record<string, unknown>
}

export interface DeleteSectionParams {
  baseRev: number
  clientId: string
}

export interface PutAttachmentBody {
  clientId: string
  deviceName: string
}

/* ─── result types ─── */

export type FailureReason =
  | 'network' //      fetch rejected, or the body failed mid-read
  | 'timeout' //      `timeoutMs` elapsed
  | 'aborted' //      the caller's signal fired
  | 'contended' //    503 — nothing written, come back after `retryAfterMs`
  | 'not-found' //    404
  | 'too-large' //    413
  | 'rejected' //     400 (and any other 4xx): the request itself is wrong
  | 'unauthorized' // 401 / 403
  | 'server' //       any other 5xx
  | 'malformed' //    the answer is not what the protocol says it is
  | 'unknown-host' // the hostId is not in the host store; nothing was sent
  | 'endpoint-changed' // `expectEndpoint` was given and the host is not at it any more; nothing was sent

export interface Failure {
  kind: 'failed'
  reason: FailureReason
  /** The HTTP status; 0 when no response was received (or no request sent). */
  status: number
  message: string
  /** Only with `reason: 'contended'`. */
  retryAfterMs?: number
}

export type Result<T> = { kind: 'ok'; value: T } | Failure

export type PutOutcome =
  | { kind: 'applied'; rev: number }
  | { kind: 'converged'; rev: number }
  /** `rev: 0, hash: null, payload: null` = the section is absent (or a tombstone). */
  | { kind: 'conflict'; rev: number; hash: string | null; payload: Record<string, unknown> | null }
  | { kind: 'schema'; fingerprint: string; ordinal: number }
  | Failure

/** A delete is never `converged` (the route has no `applied`) and never `schema`. */
export type DeleteOutcome = Exclude<PutOutcome, { kind: 'converged' } | { kind: 'schema' }>

export type DeleteProfileOutcome =
  | { kind: 'deleted' }
  /** Refused: these clients are attached (possibly `[]` if they just left). */
  | { kind: 'attached'; attachments: Attachment[] }
  | Failure

export interface RequestOptions {
  signal?: AbortSignal
  /** Default 15 000. */
  timeoutMs?: number
  /**
   * `"<ip>:<port>"` (`endpointOfHost`) the request must go to. Compared with where the host is in the SAME
   * synchronous step that resolves its address for the fetch; any other → nothing is sent, `endpoint-changed`.
   * A caller that checked the endpoint earlier cannot close that gap itself: the address could move away and back
   * between its check and the fetch (P3d-4b review A3).
   */
  expectEndpoint?: string
}

export const DEFAULT_TIMEOUT_MS = 15_000
const DEFAULT_RETRY_AFTER_MS = 1000

/* ─── field validation ─── */

const SHA256_HEX = /^[0-9a-f]{64}$/

function isPlainObject(v: unknown): v is Record<string, unknown> {
  return typeof v === 'object' && v !== null && !Array.isArray(v)
}

function isRev(v: unknown): v is number {
  return typeof v === 'number' && Number.isSafeInteger(v) && v >= 0
}

function isOrdinal(v: unknown): v is number {
  return typeof v === 'number' && Number.isSafeInteger(v) && v >= 1
}

function isSha256(v: unknown): v is string {
  return typeof v === 'string' && SHA256_HEX.test(v)
}

function isText(v: unknown): v is string {
  return typeof v === 'string' && v.length > 0
}

/** Each parser returns a fresh object of the known fields, or null. */
function parseProfile(v: unknown): Profile | null {
  if (!isPlainObject(v)) return null
  const { id, name, createdAt, updatedAt } = v
  if (!isText(id) || !isText(name) || !isRev(createdAt) || !isRev(updatedAt)) return null
  return { id, name, createdAt, updatedAt }
}

function parseAttachment(v: unknown): Attachment | null {
  if (!isPlainObject(v)) return null
  const { clientId, profileId, deviceName, attachedAt, lastSeen } = v
  if (!isText(clientId) || !isText(profileId) || typeof deviceName !== 'string') return null
  if (!isRev(attachedAt) || !isRev(lastSeen)) return null
  return { clientId, profileId, deviceName, attachedAt, lastSeen }
}

function parseSectionMeta(v: unknown): SectionMeta | null {
  if (!isPlainObject(v)) return null
  const { section, rev, hash, fingerprint, ordinal, writer, updatedAt } = v
  if (!isText(section) || !isRev(rev) || !isSha256(hash) || !isSha256(fingerprint)) return null
  if (!isOrdinal(ordinal) || typeof writer !== 'string' || !isRev(updatedAt)) return null
  return { section, rev, hash, fingerprint, ordinal, writer, updatedAt }
}

function parseSection(v: unknown): Section | null {
  const meta = parseSectionMeta(v)
  if (!meta || !isPlainObject(v) || !isPlainObject(v.payload)) return null
  return { ...meta, payload: v.payload }
}

/** All-or-nothing: one bad element fails the list, it is never dropped. */
function parseArray<T>(v: unknown, parseOne: (item: unknown) => T | null): T[] | null {
  if (!Array.isArray(v)) return null
  const out: T[] = []
  for (const item of v) {
    const parsed = parseOne(item)
    if (parsed === null) return null
    out.push(parsed)
  }
  return out
}

function parseIndexEntry(v: unknown): ProfileIndexEntry | null {
  const profile = parseProfile(v)
  if (!profile || !isPlainObject(v)) return null
  const sections = parseArray(v.sections, parseSectionMeta)
  const attachments = parseArray(v.attachments, parseAttachment)
  if (!sections || !attachments) return null
  return { ...profile, sections, attachments }
}

/* ─── transport ─── */

function failure(reason: FailureReason, status: number, message: string): Failure {
  return { kind: 'failed', reason, status, message }
}

function malformed(status: number, what: string): Failure {
  return failure('malformed', status, `malformed response: ${what}`)
}

function errorMessage(err: unknown): string {
  return err instanceof Error && err.message ? err.message : String(err)
}

/** `Retry-After` in whole seconds → ms. An HTTP date, junk or nothing → 1000. */
function retryAfterMs(res: Response): number {
  const raw = res.headers.get('Retry-After')?.trim() ?? ''
  if (!/^\d+$/.test(raw)) return DEFAULT_RETRY_AFTER_MS
  const ms = Number(raw) * 1000
  return Number.isSafeInteger(ms) ? ms : DEFAULT_RETRY_AFTER_MS
}

/** A response once its body has been read — the single read of that body. */
interface Answer {
  status: number
  text: string
  /** `undefined` when the text is not JSON (JSON itself has no `undefined`). */
  json: unknown
}

/** The outcome of a status no route treats specially (not 200, not 409). */
function statusFailure(res: Response, text: string): Failure {
  const status = res.status
  const message = text.trim() || `${status} ${res.statusText}`.trim()
  if (status === 503) return { ...failure('contended', status, message), retryAfterMs: retryAfterMs(res) }
  if (status === 401 || status === 403) return failure('unauthorized', status, message)
  if (status === 404) return failure('not-found', status, message)
  if (status === 413) return failure('too-large', status, message)
  if (status >= 500) return failure('server', status, message)
  if (status >= 400) return failure('rejected', status, message)
  return malformed(status, `unexpected status ${status}`)
}

type Interpret<T> = (answer: Answer) => T | Failure

/** Null when the host is in the store; see the file header for why this gate exists. */
function unknownHost(hostId: string): Failure | null {
  if (useHostStore.getState().hosts[hostId]) return null
  return failure('unknown-host', 0, `unknown host: ${hostId}`)
}

/** The path and init of one request. Built lazily — see `request`. */
interface Built {
  path: string
  init: RequestInit
}

/**
 * Sends one request and interprets the answer — the single entry point of every
 * exported function, and therefore where the unknown-host gate lives. `on200` and `on409` see the
 * parsed body; any other status becomes a Failure — except those in `others`,
 * which a route may claim (getSection's 404).
 *
 * `build` is called HERE, inside the guard, never by the caller: encoding a
 * path and serializing a body can both throw (file header).
 */
async function request<T>(
  hostId: string,
  build: () => Built,
  opts: RequestOptions | undefined,
  on200: Interpret<T>,
  on409?: Interpret<T>,
  others?: Record<number, () => T>,
): Promise<T | Failure> {
  const controller = new AbortController()
  let cause: 'timeout' | 'aborted' | null = null
  let interrupt: (f: Failure) => void = () => {}
  const interrupted = new Promise<Failure>((resolve) => {
    interrupt = resolve
  })
  const stop = (why: 'timeout' | 'aborted'): void => {
    if (cause) return
    cause = why
    controller.abort()
    interrupt(failure(why, 0, why === 'timeout' ? 'request timed out' : 'request aborted'))
  }
  const onExternalAbort = (): void => stop('aborted')
  const external = opts?.signal
  let timer: ReturnType<typeof setTimeout> | undefined

  try {
    const gone = unknownHost(hostId)
    if (gone) return gone
    if (external?.aborted) return failure('aborted', 0, 'request aborted')
    let built: Built
    try {
      built = build()
    } catch (err) {
      // Ours to blame, not the network's: this request cannot be expressed.
      return failure('rejected', 0, `cannot build request: ${errorMessage(err)}`)
    }
    const { path, init } = built
    external?.addEventListener('abort', onExternalAbort, { once: true })
    timer = setTimeout(() => stop('timeout'), opts?.timeoutMs ?? DEFAULT_TIMEOUT_MS)

    const exchange = async (): Promise<T | Failure> => {
      // Synchronous up to `hostFetch`, which reads the address right here: the endpoint compared is the one used.
      if (opts?.expectEndpoint !== undefined) {
        const host = useHostStore.getState().hosts[hostId]
        if (host === undefined || endpointOfHost(host) !== opts.expectEndpoint) {
          return failure('endpoint-changed', 0, `host ${hostId} is not at ${opts.expectEndpoint} any more`)
        }
      }
      const res = await hostFetch(hostId, path, { ...init, signal: controller.signal })
      const claimed = others?.[res.status]
      if (claimed) return claimed()
      // Branch on the status FIRST; the body is read once, below.
      const interpret = res.status === 200 ? on200 : res.status === 409 ? on409 : undefined
      let text: string
      try {
        text = await res.text()
      } catch (err) {
        return failure('network', res.status, errorMessage(err))
      }
      if (!interpret) {
        return res.status === 409 ? malformed(409, 'unexpected 409') : statusFailure(res, text)
      }
      let json: unknown
      try {
        json = JSON.parse(text)
      } catch {
        return malformed(res.status, 'body is not JSON')
      }
      return interpret({ status: res.status, text, json })
    }

    // Raced, so a transport that ignores its signal still ends on time.
    return await Promise.race([exchange(), interrupted])
  } catch (err) {
    // `cause` is set by callbacks, which the compiler's flow analysis cannot see.
    const why = cause as 'timeout' | 'aborted' | null
    if (why) return failure(why, 0, why === 'timeout' ? 'request timed out' : 'request aborted')
    return failure('network', 0, errorMessage(err))
  } finally {
    if (timer !== undefined) clearTimeout(timer)
    external?.removeEventListener('abort', onExternalAbort)
  }
}

function jsonInit(method: string, body: unknown): RequestInit {
  return { method, headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(body) }
}

function ok<T>(value: T): Result<T> {
  return { kind: 'ok', value }
}

/* ─── paths ─── */

const BASE = '/api/profiles'

function profilePath(profileId: string): string {
  return `${BASE}/${encodeURIComponent(profileId)}`
}

function sectionPath(profileId: string, section: string): string {
  return `${profilePath(profileId)}/sections/${encodeURIComponent(section)}`
}

function query(params: Record<string, string | number>): string {
  return Object.entries(params)
    .map(([k, v]) => `${encodeURIComponent(k)}=${encodeURIComponent(String(v))}`)
    .join('&')
}

/* ─── profiles ─── */

export function listProfiles(hostId: string, opts?: RequestOptions): Promise<Result<ProfileIndexEntry[]>> {
  return request(hostId, () => ({ path: BASE, init: { method: 'GET' } }), opts, ({ status, json }) => {
    if (!isPlainObject(json)) return malformed(status, 'profile list')
    // One bad row fails the list: a shorter list would read as "that profile is gone".
    const profiles = parseArray(json.profiles, parseIndexEntry)
    return profiles ? ok(profiles) : malformed(status, 'profile list')
  })
}

export function createProfile(hostId: string, name: string, opts?: RequestOptions): Promise<Result<Profile>> {
  return request(hostId, () => ({ path: BASE, init: jsonInit('POST', { name }) }), opts, ({ status, json }) => {
    const profile = parseProfile(json)
    return profile ? ok(profile) : malformed(status, 'profile')
  })
}

export function renameProfile(
  hostId: string,
  profileId: string,
  name: string,
  opts?: RequestOptions,
): Promise<Result<{ id: string; name: string }>> {
  const build = (): Built => ({ path: profilePath(profileId), init: jsonInit('PATCH', { name }) })
  return request(hostId, build, opts, ({ status, json }) => {
    if (!isPlainObject(json) || !isText(json.id) || !isText(json.name)) return malformed(status, 'rename')
    return ok({ id: json.id, name: json.name })
  })
}

export function deleteProfile(
  hostId: string,
  profileId: string,
  opts?: RequestOptions,
): Promise<DeleteProfileOutcome> {
  return request<DeleteProfileOutcome>(
    hostId,
    () => ({ path: profilePath(profileId), init: { method: 'DELETE' } }),
    opts,
    ({ status, json }) =>
      isPlainObject(json) && json.deleted === true ? { kind: 'deleted' } : malformed(status, 'delete'),
    ({ status, json }) => {
      if (!isPlainObject(json) || json.reason !== 'attached') return malformed(status, '409 reason')
      const attachments = parseArray(json.attachments, parseAttachment)
      return attachments ? { kind: 'attached', attachments } : malformed(status, 'attachments')
    },
  )
}

/** Every live section of a profile, with payloads. An unknown profile is `not-found`. */
export function getProfileSections(
  hostId: string,
  profileId: string,
  opts?: RequestOptions,
): Promise<Result<Record<string, Section>>> {
  const build = (): Built => ({ path: profilePath(profileId), init: { method: 'GET' } })
  return request(hostId, build, opts, ({ status, json }) => {
    if (!isPlainObject(json) || !isPlainObject(json.sections)) return malformed(status, 'sections')
    const out: Record<string, Section> = {}
    for (const [name, raw] of Object.entries(json.sections)) {
      const section = parseSection(raw)
      if (!section || section.section !== name) return malformed(status, `section ${name}`)
      out[name] = section
    }
    return ok(out)
  })
}

/* ─── sections ─── */

/**
 * One live section, or `null` on 404. The daemon answers 404 for a tombstone,
 * a section that never existed and an unknown profile alike; the client cannot
 * tell them apart, and the state machine does not need to.
 */
export function getSection(
  hostId: string,
  profileId: string,
  section: string,
  opts?: RequestOptions,
): Promise<Result<Section | null>> {
  return request<Result<Section | null>>(
    hostId,
    () => ({ path: sectionPath(profileId, section), init: { method: 'GET' } }),
    opts,
    ({ status, json }) => {
      const parsed = parseSection(json)
      return parsed && parsed.section === section ? ok(parsed) : malformed(status, 'section')
    },
    undefined,
    { 404: () => ok(null) },
  )
}

/** The 409 of a section write. `allowSchema` is false for DELETE. */
function interpretWriteConflict(allowSchema: boolean): Interpret<PutOutcome> {
  return ({ status, json }) => {
    if (!isPlainObject(json)) return malformed(status, '409 body')
    if (json.reason === 'conflict') {
      const { rev, hash, payload } = json
      if (!isRev(rev)) return malformed(status, 'conflict rev')
      if (hash === undefined && payload === undefined) return { kind: 'conflict', rev, hash: null, payload: null }
      if (!isSha256(hash) || !isPlainObject(payload)) return malformed(status, 'conflict hash/payload')
      return { kind: 'conflict', rev, hash, payload }
    }
    if (json.reason === 'schema' && allowSchema) {
      const { fingerprint, ordinal } = json
      if (!isSha256(fingerprint) || !isOrdinal(ordinal)) return malformed(status, 'schema body')
      return { kind: 'schema', fingerprint, ordinal }
    }
    return malformed(status, '409 reason')
  }
}

/** The compare-and-set write of spec §4.6. */
export function putSection(
  hostId: string,
  profileId: string,
  section: string,
  body: PutSectionBody,
  opts?: RequestOptions,
): Promise<PutOutcome> {
  return request<PutOutcome>(
    hostId,
    () => {
      const { clientId, baseRev, hash, fingerprint, ordinal, payload } = body
      return {
        path: sectionPath(profileId, section),
        init: jsonInit('PUT', { clientId, baseRev, hash, fingerprint, ordinal, payload }),
      }
    },
    opts,
    ({ status, json }) => {
      if (!isPlainObject(json) || !isRev(json.rev)) return malformed(status, 'rev')
      if (typeof json.applied !== 'boolean') return malformed(status, 'applied')
      return { kind: json.applied ? 'applied' : 'converged', rev: json.rev }
    },
    interpretWriteConflict(true),
  )
}

/** The compare-and-set delete of spec §4.6.3. Its 200 is `{rev}` alone → `applied`. */
export async function deleteSection(
  hostId: string,
  profileId: string,
  section: string,
  params: DeleteSectionParams,
  opts?: RequestOptions,
): Promise<DeleteOutcome> {
  const out = await request<PutOutcome>(
    hostId,
    () => ({
      path: `${sectionPath(profileId, section)}?${query({ baseRev: params.baseRev, clientId: params.clientId })}`,
      init: { method: 'DELETE' },
    }),
    opts,
    ({ status, json }) =>
      isPlainObject(json) && isRev(json.rev) ? { kind: 'applied', rev: json.rev } : malformed(status, 'rev'),
    interpretWriteConflict(false),
  )
  // Unreachable by construction (no interpreter above yields them); stated so
  // the narrowing is checked rather than cast.
  if (out.kind === 'converged' || out.kind === 'schema') return malformed(0, `delete answered ${out.kind}`)
  return out
}

/* ─── attachment ─── */

export function putAttachment(
  hostId: string,
  profileId: string,
  body: PutAttachmentBody,
  opts?: RequestOptions,
): Promise<Result<{ attached: true }>> {
  return request<Result<{ attached: true }>>(
    hostId,
    () => {
      const { clientId, deviceName } = body
      return { path: `${profilePath(profileId)}/attachment`, init: jsonInit('PUT', { clientId, deviceName }) }
    },
    opts,
    ({ status, json }) =>
      isPlainObject(json) && json.attached === true ? ok({ attached: true }) : malformed(status, 'attached'),
  )
}

/** `detached: false` = this client is attached to some other profile; nothing was removed. */
export function deleteAttachment(
  hostId: string,
  profileId: string,
  clientId: string,
  opts?: RequestOptions,
): Promise<Result<{ detached: boolean }>> {
  return request(
    hostId,
    () => ({ path: `${profilePath(profileId)}/attachment?${query({ clientId })}`, init: { method: 'DELETE' } }),
    opts,
    ({ status, json }) =>
      isPlainObject(json) && typeof json.detached === 'boolean'
        ? ok({ detached: json.detached })
        : malformed(status, 'detached'),
  )
}
