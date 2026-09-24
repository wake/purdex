// spa/src/lib/profile/section-store.ts — the Profile Sync leader's working
// state: per section the agreed `base`, the live hash and an unresolved
// `conflict`; plus the payloads a conflict (or a flight) retains, by hash.
//
// ONE THING, ONE KEY. There is no document. Storage holds
//
//   purdex-profile-sections:<profileId>:s:<sectionKey>  →  {base, currentHash, conflict?}
//   purdex-profile-sections:<profileId>:p:<hash>        →  the payload with that hash
//
// so a write replaces exactly the thing it names. Saving `settings` cannot
// revert `hosts`; stashing a payload cannot revert any section. The reviewed
// lost update — leader A writes `settings`, leader B, from a copy it read
// earlier, writes `hosts` and puts the old `settings` back — needed one key to
// hold everything. It has no equivalent here: nobody writes a copy of anything
// they did not mean to change.
//   Payloads are content-addressed: one hash is one content, for ever. Writing
// one twice is idempotent, and two leaders writing the same hash at the same
// time write the same thing.
//   The profile is part of the KEY, not a field inside a value. Reading profile
// A never looks at a key of profile B, and no write as A can land on B's data.
// The three parts of a key are validated before one is built — profile id
// `^p_[0-9a-f]{12}$`, section key by the daemon's pattern (`sectionKind`), hash
// 64 lower-case hex — and none of those can contain a `:`, so input cannot
// forge a separator. Anything else is `'failed'` on write and "not there" on
// read.
//
// NO FENCING, NO GENERATION — REMOVED ON PURPOSE. An earlier version carried a
// generation that every write had to present. localStorage has no cross-process
// transaction: "read the generation, compare, write" is three steps, another
// process can claim and write between any two of them, and two windows claiming
// at once can both read n and both hold n+1. A fence on such storage narrows the
// window and closes nothing, while the API reads as if it gave a guarantee.
// With one key per thing there is no unrelated data for a stale write to
// destroy, so the fence has nothing left to protect. Mutual exclusion of leaders
// is the lease's job (`lib/profile/leader.ts`, plan Task 10) and only the
// lease's. This module does not take part in it and does not pretend to.
//
// RESIDUAL RISK — THE SAME SECTION KEY, TWO LEADERS: LAST WRITER WINS. Nothing
// here prevents it, and nothing here detects it. It is accepted because:
//   1. both values are a base that some leader wrote after completing a CAS
//      against the daemon — each was correct when it was written;
//   2. if the OLDER one wins, the next push of that section goes out with an old
//      rev, the daemon answers 409, and the section either converges (same
//      content) or becomes one conflict the user resolves. The SOT is guarded by
//      the daemon's CAS, not by this file: a lost write here costs a false
//      conflict, never a silent loss of data;
//   3. two leaders exist only in the jitter of a lease changing hands (plan
//      Task 10), and the old one stops when it sees it has lost the lease.
// A second, smaller residual in the same window: `pruneStash` by one leader can
// remove a payload another leader has written for a conflict whose section key
// it has not written yet (payloads go first — see below). That conflict is then
// stored without one of its payloads and is dropped at the next load, exactly
// like any other damaged conflict. The leader that wrote it still holds it in
// memory; what is lost is its survival across a restart.
//
// THE TWO SIDES OF A CONFLICT ARE NOT ALIKE, AND ONLY ONE IS REQUIRED. The
// LOCAL side (`conflict.localHash`) is the snapshot that was SENT: the stores
// have moved on since, nothing else holds it, and spec §4.6.2 exists to keep it
// — lose it and the user chooses against a moving target. The SOT side
// (`conflict.sot.hash`) can be fetched again at any time, and `keep:'sot'` does
// exactly that (a fresh pull) rather than use a stored copy. Nor is it always
// there to store: the state machine makes conflicts whose SOT side is a hash
// only — a lock decided from an index (row 8), and any lock that learns of a
// newer SOT rev from an event. So: the local payload must be passed or already
// stored, else `saveConflict` fails; the SOT payload is stored WHEN PASSED and
// its absence is not a failure. The same at load: a conflict is dropped only
// when its LOCAL payload is missing or unreadable. `pruneStash` still protects
// both — what is stored and referred to is not removed.
//
// A STORED CONFLICT HAS ITS (REQUIRED) PAYLOAD — BY WRITE ORDER. Several keys cannot be
// written atomically, so `saveConflict` writes every payload key first and the
// section key, which carries the `conflict`, LAST and only if all of those
// succeeded. Any failure on the way returns `'failed'` with the section key
// untouched. So "a conflict is in storage ⇒ the payloads it needs are in
// storage" holds after every step; a failure can only leave payload keys nobody
// refers to, and `pruneStash` removes those. `saveSection` refuses a section
// that carries a conflict — there is no other way to store one.
//   The same is enforced on the way IN, because storage can be damaged behind
// this module's back: at load, a conflict whose LOCAL payload is missing or
// unreadable is dropped. Only the `conflict` goes — `base` and `currentHash`
// stay and the section comes back unlocked. And `pruneStash` will not remove a
// payload a stored conflict refers to, whatever its caller's `keep` says.
//
// KNOWN LIMITATION — A LARGE CONFLICT MAY NOT SURVIVE A RESTART. THIS IS NOT
// CONFORMANCE. Plan Task 5 and spec §4.6.2 require that a restart restores a
// conflict with THE SNAPSHOT THAT WAS SENT. This module can only do that when
// the payloads fit in localStorage, which is about 5–10 MB per origin and is
// shared with every other `purdex-*` key. The per-payload limit below is the
// daemon's own 5 MiB, not a smaller one made up here; whether a payload is
// actually stored is decided by the browser's quota at `setItem`, and the
// answer can be no. `saveConflict` then returns `'failed'`, the caller keeps the
// conflict in memory, and if the app restarts before the user resolves it THE
// SENT SNAPSHOT IS GONE. A reindex after the restart cannot bring it back: it
// sees the local state and the SOT of that later moment, not what was sent. The
// user is then choosing against a moving target — the very thing §4.6.2 exists
// to prevent. The contract is not met in that case. Tracked in issue #1244
// (move this store to IndexedDB).
//
// PLAIN STORAGE, ON PURPOSE. This is not a zustand store and nothing here is
// registered for cross-window sync: a cross-window rehydrate is a full-state
// replace and would swap every base under a live driver. `browserStorage.setItem`
// / `removeItem` do broadcast the key on the `purdex-sync` channel, and the
// receiving side (`lib/storage/sync.ts`) looks the key up in its registry and
// does nothing when no store is registered under it — none ever is for these
// keys. One small message per write, leader only, at the rate sections change
// state: negligible. What every window must agree on lives in
// `stores/useProfileStore`.
//   `browserStorage` has no way to list keys, so enumeration (`load`, `prune`,
// `clear`) goes to `localStorage` directly, inside try/catch.
//
// NO IN-MEMORY CACHE. Every operation reads storage. A cache would be wrong the
// moment the lease changes hands.
//
// VALIDATED, NOT CAST. Each section is its own key, so a section that fails
// validation is dropped alone as a matter of structure: it starts over from
// `{0, null}` and is re-judged after the next reindex; no other section and no
// payload is affected.
//
// NOTHING THROWS. Writes return a `WriteResult`. A refused `setItem` (quota,
// blocked site data) leaves that key's PREVIOUS value in place — `setItem` is
// atomic per key, there is no half-written value.
import { browserStorage } from '../storage/browser-backend'
import { STORAGE_KEYS } from '../storage/keys'
import { sectionKind } from './projections'
import type { Held, SectionConflict } from './sync-state'

export interface PersistedSection {
  base: Held
  currentHash: string | null
  conflict?: SectionConflict
}

/** What a load returns. Payloads are not part of it: they are read one at a
 *  time with `getStash`, never all into memory. */
export interface SectionStoreData {
  profileId: string
  sections: Record<string, PersistedSection>
}

/** `'ok'`     — storage now holds what was asked for.
 *  `'failed'` — malformed input, or storage refused. For a single-key write
 *               nothing was written; `saveConflict`, `pruneStash` and
 *               `clearSectionStore` say below what a failure half-way leaves. */
export type WriteResult = 'ok' | 'failed'

/** A single payload may not serialise to more than this many UTF-8 bytes — the
 *  daemon's limit for a section, so anything larger could never have been sent
 *  or received. It is NOT a promise that a payload under it can be stored: see
 *  KNOWN LIMITATION in the header. */
export const MAX_STASH_PAYLOAD_BYTES = 5 * 1024 * 1024

const PROFILE_ID_PATTERN = /^p_[0-9a-f]{12}$/
const SHA256_HEX = /^[0-9a-f]{64}$/

// --- validation -------------------------------------------------------------

function isRecord(v: unknown): v is Record<string, unknown> {
  return typeof v === 'object' && v !== null && !Array.isArray(v)
}

/** A JSON-expressible object: `{}` / `Object.create(null)`, not an array, not a class instance. */
function isPlainObject(v: unknown): v is Record<string, unknown> {
  if (!isRecord(v)) return false
  const proto: unknown = Object.getPrototypeOf(v)
  return proto === Object.prototype || proto === null
}

function isProfileId(v: unknown): v is string {
  return typeof v === 'string' && PROFILE_ID_PATTERN.test(v)
}

function isHash(v: unknown): v is string {
  return typeof v === 'string' && SHA256_HEX.test(v)
}

function isHashOrNull(v: unknown): v is string | null {
  return v === null || isHash(v)
}

function parseHeld(v: unknown): Held | null {
  if (!isRecord(v)) return null
  const { rev, hash } = v
  if (typeof rev !== 'number' || !Number.isSafeInteger(rev) || rev < 0) return null
  if (!isHashOrNull(hash)) return null
  return { rev, hash }
}

/** A clean copy with only the known fields, or null if anything is off.
 *  `conflict: null | undefined` means "no conflict"; a conflict that is present
 *  but malformed spoils the section — half a lock is not a lock. */
function parseSection(v: unknown): PersistedSection | null {
  if (!isRecord(v)) return null
  const base = parseHeld(v.base)
  if (base === null) return null
  if (!isHashOrNull(v.currentHash)) return null
  const out: PersistedSection = { base, currentHash: v.currentHash }
  if (v.conflict !== undefined && v.conflict !== null) {
    if (!isRecord(v.conflict)) return null
    const sot = parseHeld(v.conflict.sot)
    if (sot === null || !isHashOrNull(v.conflict.localHash)) return null
    out.conflict = { localHash: v.conflict.localHash, sot }
  }
  return out
}

/** The JSON of a plain object of at most `MAX_STASH_PAYLOAD_BYTES`, else null. */
function serialisePayload(payload: unknown): string | null {
  if (!isPlainObject(payload)) return null
  let serialised: string
  try {
    serialised = JSON.stringify(payload)
  } catch {
    return null // a cycle, a BigInt
  }
  // Every code unit is at least one byte, so the cheap check spares encoding the obvious cases.
  if (serialised.length > MAX_STASH_PAYLOAD_BYTES) return null
  return new TextEncoder().encode(serialised).length <= MAX_STASH_PAYLOAD_BYTES ? serialised : null
}

/** Every payload a conflict REFERS to — what `pruneStash` must leave alone.
 *  `null` is "does not exist" (a delete was sent / the SOT holds a tombstone): no payload. */
function referencedHashes(conflict: SectionConflict): string[] {
  return [conflict.localHash, conflict.sot.hash].filter((h): h is string => h !== null)
}

/** The payload a conflict cannot exist without: the LOCAL side only — see
 *  "THE TWO SIDES ARE NOT ALIKE" in the header. */
function requiredHashes(conflict: SectionConflict): string[] {
  return conflict.localHash === null ? [] : [conflict.localHash]
}

function isSectionKey(v: unknown): v is string {
  return typeof v === 'string' && sectionKind(v) !== null
}

/** `Object.fromEntries` defines own data properties, so a key named
 *  `__proto__` stays a key instead of becoming the record's prototype. */
function recordOf<T>(entries: Array<[string, T]>): Record<string, T> {
  return Object.fromEntries(entries) as Record<string, T>
}

// --- keys and storage -------------------------------------------------------

const ROOT = `${STORAGE_KEYS.PROFILE_SECTIONS}:`

// Callers of these three have validated every part.
const profilePrefix = (profileId: string): string => `${ROOT}${profileId}:`
const sectionPrefix = (profileId: string): string => `${profilePrefix(profileId)}s:`
const payloadPrefix = (profileId: string): string => `${profilePrefix(profileId)}p:`

/** Every key in storage that starts with `prefix`; `null` when storage cannot be listed. */
function keysUnder(prefix: string): string[] | null {
  try {
    const keys: string[] = []
    for (let i = 0; i < localStorage.length; i += 1) {
      const key = localStorage.key(i)
      if (key !== null && key.startsWith(prefix)) keys.push(key)
    }
    return keys
  } catch {
    return null
  }
}

type Read = { ok: true; raw: string | null } | { ok: false }

function readKey(key: string): Read {
  try {
    const raw = browserStorage.getItem(key) as string | null
    return { ok: true, raw: typeof raw === 'string' ? raw : null }
  } catch {
    return { ok: false }
  }
}

function writeKey(key: string, value: string): boolean {
  try {
    browserStorage.setItem(key, value)
    return true
  } catch {
    return false // quota, blocked site data — the key's previous value is still there
  }
}

function removeKey(key: string): boolean {
  try {
    browserStorage.removeItem(key)
    return true
  } catch {
    return false
  }
}

function parseJson(raw: string | null): unknown {
  if (raw === null) return undefined
  try {
    return JSON.parse(raw) as unknown
  } catch {
    return undefined
  }
}

/** `'absent'` covers unreadable too: a payload nobody can parse is as good as none. */
function payloadState(profileId: string, hash: string): 'present' | 'absent' | 'unknown' {
  const read = readKey(`${payloadPrefix(profileId)}${hash}`)
  if (!read.ok) return 'unknown'
  return isPlainObject(parseJson(read.raw)) ? 'present' : 'absent'
}

/** Remove `keys`; `'failed'` if any could not be removed (the others still are). */
function removeAll(keys: string[]): WriteResult {
  let result: WriteResult = 'ok'
  for (const key of keys) if (!removeKey(key)) result = 'failed'
  return result
}

// --- API --------------------------------------------------------------------

/** Every valid section stored for this profile. Bad data, no data, a malformed
 *  profile id and a storage that cannot be read all give an empty result. Never
 *  writes: a section or a conflict dropped here stays in storage until the key
 *  is next written. */
export function loadSectionStore(profileId: string): SectionStoreData {
  const sections: Array<[string, PersistedSection]> = []
  const prefix = isProfileId(profileId) ? sectionPrefix(profileId) : null
  for (const storageKey of prefix === null ? [] : (keysUnder(prefix) ?? [])) {
    const key = storageKey.slice(prefix!.length)
    if (!isSectionKey(key)) continue
    const read = readKey(storageKey)
    const section = read.ok ? parseSection(parseJson(read.raw)) : null
    if (section === null) continue // a bad section is dropped alone
    const intact = section.conflict === undefined || requiredHashes(section.conflict).every((h) => payloadState(profileId, h) === 'present')
    sections.push([key, intact ? section : { base: section.base, currentHash: section.currentHash }])
  }
  return { profileId, sections: recordOf(sections) }
}

/** A section WITHOUT a conflict. One that has a conflict is `'failed'`: it goes
 *  through `saveConflict`, which is what keeps "a stored conflict has its
 *  payloads" true. Saving over a locked section is how the lock is lifted (its
 *  payloads stay until `pruneStash`). */
export function saveSection(profileId: string, key: string, s: PersistedSection): WriteResult {
  if (!isProfileId(profileId) || !isSectionKey(key)) return 'failed'
  const section = parseSection(s)
  if (section === null || section.conflict !== undefined) return 'failed'
  return writeKey(`${sectionPrefix(profileId)}${key}`, JSON.stringify(section)) ? 'ok' : 'failed'
}

/** A locked section and the payloads its conflict refers to. `payloads` is by
 *  hash; `s.conflict.localHash` (if not null) must be in it or already stored
 *  (and readable), else `'failed'` before anything is written. The SOT side is
 *  stored when passed and not missed when it is not (see the header).
 *    ORDER IS THE INVARIANT: each payload key first — one that is already
 *  stored with the same content is skipped — and the section key last, only
 *  once every payload is in. A refusal half-way returns `'failed'` and leaves
 *  the section key as it was; the payloads written before it stay, unreferenced,
 *  for `pruneStash`. */
export function saveConflict(
  profileId: string,
  key: string,
  s: PersistedSection & { conflict: SectionConflict },
  payloads: Record<string, unknown>,
): WriteResult {
  if (!isProfileId(profileId) || !isSectionKey(key) || !isPlainObject(payloads)) return 'failed'
  const section = parseSection(s)
  const conflict = section?.conflict
  if (section === null || conflict === undefined) return 'failed'

  const writes: Array<[string, string]> = []
  for (const [hash, payload] of Object.entries(payloads)) {
    const serialised = isHash(hash) ? serialisePayload(payload) : null
    if (serialised === null) return 'failed'
    writes.push([hash, serialised])
  }
  const passed = new Set(writes.map(([hash]) => hash))
  if (requiredHashes(conflict).some((h) => !passed.has(h) && payloadState(profileId, h) !== 'present')) return 'failed'

  for (const [hash, serialised] of writes) {
    const storageKey = `${payloadPrefix(profileId)}${hash}`
    const existing = readKey(storageKey)
    if (existing.ok && existing.raw === serialised) continue
    if (!writeKey(storageKey, serialised)) return 'failed'
  }
  return writeKey(`${sectionPrefix(profileId)}${key}`, JSON.stringify(section)) ? 'ok' : 'failed'
}

/** `'ok'` = the section is not stored (any more). Its payloads are `pruneStash`'s business. */
export function dropSection(profileId: string, key: string): WriteResult {
  if (!isProfileId(profileId) || !isSectionKey(key)) return 'failed'
  const storageKey = `${sectionPrefix(profileId)}${key}`
  const read = readKey(storageKey)
  if (!read.ok) return 'failed'
  if (read.raw === null) return 'ok'
  return removeKey(storageKey) ? 'ok' : 'failed'
}

/** `'failed'` also covers: bad hash, not a plain object, not serialisable, over
 *  `MAX_STASH_PAYLOAD_BYTES`. Storing what is already stored writes nothing. */
export function putStash(profileId: string, hash: string, payload: unknown): WriteResult {
  if (!isProfileId(profileId) || !isHash(hash)) return 'failed'
  const serialised = serialisePayload(payload)
  if (serialised === null) return 'failed'
  return writeKey(`${payloadPrefix(profileId)}${hash}`, serialised) ? 'ok' : 'failed'
}

/** The payload stored under `hash` for this profile; `undefined` when there is
 *  none, it is unreadable, or an argument is malformed. */
export function getStash(profileId: string, hash: string): unknown | undefined {
  if (!isProfileId(profileId) || !isHash(hash)) return undefined
  const read = readKey(`${payloadPrefix(profileId)}${hash}`)
  const payload = read.ok ? parseJson(read.raw) : undefined
  return isPlainObject(payload) ? payload : undefined
}

/** Remove this profile's payload keys whose hash is not in `keep` — EXCEPT the
 *  ones a stored conflict still refers to, which stay even if the caller forgot
 *  them: removing one would turn a resolvable lock into one that is dropped at
 *  the next load. (The alternative, dropping the conflict along with its
 *  payload, would let a caller's slip silently unlock a section.) Keys under the
 *  payload prefix that are not a hash, or hold something unreadable, go too.
 *    `'failed'` = storage could not be listed or a section could not be read —
 *  then NOTHING is removed, since the unread section might hold a reference —
 *  or a removal was refused (the other removals still happened). */
export function pruneStash(profileId: string, keep: ReadonlySet<string>): WriteResult {
  if (!isProfileId(profileId)) return 'failed'
  const sectionKeys = keysUnder(sectionPrefix(profileId))
  const payloadKeys = keysUnder(payloadPrefix(profileId))
  if (sectionKeys === null || payloadKeys === null) return 'failed'
  if (payloadKeys.length === 0) return 'ok'

  // Referenced = named by any stored conflict that parses, whether or not its
  // other payload is there: erring towards keeping costs a key, not a lock.
  const referenced = new Set<string>()
  for (const storageKey of sectionKeys) {
    const read = readKey(storageKey)
    if (!read.ok) return 'failed'
    const conflict = parseSection(parseJson(read.raw))?.conflict
    if (conflict !== undefined) for (const h of referencedHashes(conflict)) referenced.add(h)
  }
  const prefixLength = payloadPrefix(profileId).length
  return removeAll(payloadKeys.filter((storageKey) => {
    const hash = storageKey.slice(prefixLength)
    return !keep.has(hash) && !referenced.has(hash)
  }))
}

/** Remove exactly the payloads named by `hashes` — no listing, no prune, so a payload another leader has just written
 *  for a conflict whose record is not stored yet is not touched (see the header) UNLESS it has one of these very
 *  hashes: same content, same key — this file cannot tell the two writers apart (#1256, no transactions; the caller
 *  says when that can happen). Unlike `pruneStash` it does NOT
 *  spare a payload a stored conflict refers to: the caller names what goes, and drops the referring record after
 *  (host ownership H3a-2: a retired section's own payloads — they held tokens). A hash not stored is already gone.
 *    `'failed'` = a malformed profile id or hash (nothing is removed), or a removal was refused (the others still
 *  happened). */
export function dropStash(profileId: string, hashes: readonly string[]): WriteResult {
  if (!isProfileId(profileId) || !hashes.every(isHash)) return 'failed'
  return removeAll(hashes.map((hash) => `${payloadPrefix(profileId)}${hash}`))
}

/** Detach / a change of master. With a profile id: every key of that profile
 *  and of no other. Without: every key under `purdex-profile-sections:`,
 *  whichever profile. `'failed'` = a malformed profile id (nothing is removed),
 *  storage could not be listed, or a removal was refused — some keys may remain.
 *  Remaining keys still carry their profile id in the key, so another master
 *  never reads them, and a re-attach to the same one is protected by the
 *  daemon's CAS. */
export function clearSectionStore(profileId?: string): WriteResult {
  if (profileId !== undefined && !isProfileId(profileId)) return 'failed'
  const keys = keysUnder(profileId === undefined ? ROOT : profilePrefix(profileId))
  return keys === null ? 'failed' : removeAll(keys)
}
