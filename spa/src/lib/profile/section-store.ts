// spa/src/lib/profile/section-store.ts — the Profile Sync leader's working
// state: per section the agreed `base`, the live hash and an unresolved
// `conflict`; plus the payload stash for the hashes a conflict retains.
//
// PLAIN STORAGE, ON PURPOSE. This is not a zustand store and nothing here is
// registered for cross-window sync. A cross-window rehydrate is a full-state
// replace: it would swap every base under a live driver. Only the lease holder
// writes this key, so nobody needs to be told. (`browserStorage.setItem` does
// broadcast the key; with no store registered under it every window drops that
// message.) What every window must agree on lives in `stores/useProfileStore`.
//
// NO IN-MEMORY CACHE. Every operation is read → change → write. Writes are rare
// (one per section state change, leader only), and a cache would be WRONG the
// moment the lease changes hands: the new leader's writes would be invisible to
// an old leader's cached copy, which would then write stale bases back.
//
// TAGGED WITH ONE PROFILE. The document carries the `profileId` it belongs to.
// Read as any other profile it is empty — a different master never sees these
// bases, which describe agreement with somebody else's SOT. A read never
// destroys it (the user may re-attach). Only `claimSectionStore` changes the
// profile, and doing so replaces the document wholesale.
//
// FENCED. The leader is a localStorage lease, and a takeover can leave two
// windows believing they lead for a moment. Every write here is read → change →
// write of the WHOLE document, so without protection the old leader, writing
// from the document it read earlier, reverts what the new leader just stored: a
// reverted base means the next CAS goes out with a stale rev (a false
// conflict); a reverted stash means an open conflict loses the payload it needs.
// So the document carries a `generation`. A window that wins the lease calls
// `claimSectionStore` once, which bumps it and returns the new value; every
// write passes the generation its caller holds, and is refused as `'fenced'`
// when the document says otherwise. `'fenced'` means "a newer leader has
// claimed — stop the driver".
//   The generation only ever grows for the lifetime of this origin's storage: a
// change of profile continues the count, and `clearSectionStore` writes an
// empty document that KEEPS it instead of removing the key. Starting over would
// make the number an old leader still holds valid again.
//   RESIDUAL RISK, stated plainly: localStorage has no cross-process
// transaction. Between this module's read, its comparison and its write, another
// process can still claim and write, and this write then lands on top of it.
// Fencing shrinks the exposure from "the whole overlap of two leaders" to "a
// cross-process interleaving inside one synchronous JS turn"; on top of that the
// driver stops itself when it loses the lease. The SOT is never at risk either
// way — the daemon's CAS protects it; what is at risk is this working state.
//   Two more holes, both outside what storage lets us close: if the key is
// removed from outside (the user clears site data) or the document becomes
// unreadable, the count restarts from 0.
//
// VALIDATED, NOT CAST. A section that fails validation is dropped ALONE. The
// trade-off: losing one section's base only makes that section start from
// `{0, null}` and be re-judged after the next reindex — cheap, and at worst a
// conflict the user resolves once. Throwing the whole document away would do
// that to every section, and lose every retained conflict snapshot, because of
// one bad entry. Only a document that is unreadable at the top level is empty.
//
// A STORED CONFLICT HAS ITS PAYLOADS. A conflict is only ever written by
// `saveConflict`, in the SAME `setItem` as the payloads it refers to, and
// `saveSection` refuses a section that carries one. Written separately, a quota
// error between the two would leave a lock the user cannot resolve: a restart
// restores `locked:conflict`, the user picks a side, and there is no payload.
//
// WRITES NEVER THROW. Each returns a `WriteResult`. A refused `setItem` (quota,
// blocked site data) leaves the PREVIOUS value in place — `localStorage.setItem`
// is atomic, there is no half-written document — so the caller loses this one
// update and nothing else.
import { browserStorage } from '../storage/browser-backend'
import { STORAGE_KEYS } from '../storage/keys'
import type { Held, SectionConflict } from './sync-state'

export interface PersistedSection {
  base: Held
  currentHash: string | null
  conflict?: SectionConflict
}

/** What a read returns. The generation is deliberately NOT part of it: the only
 *  way to hold one is to claim it. */
export interface SectionStoreData {
  profileId: string
  sections: Record<string, PersistedSection>
  /** Payloads by hash — only the ones `retainedHashes` asks the driver to keep. */
  stash: Record<string, unknown>
}

/** `'ok'`     — storage now holds what was asked for.
 *  `'fenced'` — the stored document is not this caller's: a newer leader has
 *               claimed, or it belongs to another profile / nobody. Nothing was
 *               written. The caller should stop its driver.
 *  `'failed'` — malformed input, or storage refused the read or the write.
 *               Nothing was written. */
export type WriteResult = 'ok' | 'fenced' | 'failed'

/** What storage holds. `profileId: null` = cleared: nobody's, but still counting. */
interface StoredDocument {
  profileId: string | null
  generation: number
  sections: Record<string, PersistedSection>
  stash: Record<string, unknown>
}

/** A single stashed payload may not serialise to more than this many UTF-8
 *  bytes. Measured section payloads are KB-sized, and the stash is only needed
 *  while a conflict is open, so 1 MiB is generous — while the 5 MiB this used to
 *  be was the ENTIRE localStorage quota of most browsers, before the envelope,
 *  the other payloads and every other key of this origin: a payload near it was
 *  all but guaranteed a `QuotaExceededError`. The limit is per payload, not a
 *  budget for the document; the quota is still what finally decides, and
 *  `'failed'` is how it says so.
 *    A conflict whose payload is over the limit cannot be persisted. The caller
 *  (the executor) then keeps that conflict in memory only; after a restart the
 *  reindex judges the section again from the state of that moment. Degraded, but
 *  safe: what is never restored is a lock with a missing payload. */
export const MAX_STASH_PAYLOAD_BYTES = 1024 * 1024

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

function isGeneration(v: unknown): v is number {
  return typeof v === 'number' && Number.isSafeInteger(v) && v >= 0
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

/** A plain object that serialises to at most `MAX_STASH_PAYLOAD_BYTES`. */
function isStashable(payload: unknown): boolean {
  if (!isPlainObject(payload)) return false
  let serialised: string
  try {
    serialised = JSON.stringify(payload)
  } catch {
    return false // a cycle, a BigInt
  }
  // Every code unit is at least one byte, so the cheap check spares encoding the obvious cases.
  if (serialised.length > MAX_STASH_PAYLOAD_BYTES) return false
  return new TextEncoder().encode(serialised).length <= MAX_STASH_PAYLOAD_BYTES
}

/** The hashes whose payloads a conflict cannot be resolved without. `null` is
 *  "does not exist" (a delete was sent / the SOT holds a tombstone): no payload. */
function neededHashes(conflict: SectionConflict): string[] {
  return [conflict.localHash, conflict.sot.hash].filter((h): h is string => h !== null)
}

function emptyData(profileId: string): SectionStoreData {
  return { profileId, sections: {}, stash: {} }
}

/** `Object.fromEntries` defines own data properties, so a key named
 *  `__proto__` stays a key instead of becoming the record's prototype. */
function recordOf<T>(entries: Array<[string, T]>): Record<string, T> {
  return Object.fromEntries(entries) as Record<string, T>
}

// --- storage ----------------------------------------------------------------

/** Nothing stored and nothing readable look the same: nobody's, generation 0. */
const NOTHING: StoredDocument = { profileId: null, generation: 0, sections: {}, stash: {} }

/** The stored document, validated — whichever profile it belongs to. `NOTHING`
 *  when there is none or it is unusable at the top level; `null` only when
 *  storage itself refused the read, which a write must not mistake for "fenced". */
function readDocument(): StoredDocument | null {
  let raw: string | null
  try {
    raw = browserStorage.getItem(STORAGE_KEYS.PROFILE_SECTIONS) as string | null
  } catch {
    return null
  }
  if (typeof raw !== 'string') return NOTHING
  let parsed: unknown
  try {
    parsed = JSON.parse(raw)
  } catch {
    return NOTHING
  }
  if (!isRecord(parsed) || !(parsed.profileId === null || isProfileId(parsed.profileId))) return NOTHING
  if (!isRecord(parsed.sections) || !isRecord(parsed.stash)) return NOTHING
  // A document from before fencing has no generation and reads as 0. One that
  // HAS a generation we cannot trust is a bad document, not a generation-0 one.
  const generation: unknown = parsed.generation === undefined ? 0 : parsed.generation
  if (!isGeneration(generation)) return NOTHING

  const sections: Array<[string, PersistedSection]> = []
  for (const [key, value] of Object.entries(parsed.sections)) {
    const section = key === '' ? null : parseSection(value)
    if (section !== null) sections.push([key, section]) // a bad section is dropped alone
  }
  const stash: Array<[string, unknown]> = []
  for (const [hash, payload] of Object.entries(parsed.stash)) {
    if (isHash(hash) && isPlainObject(payload)) stash.push([hash, payload])
  }
  return { profileId: parsed.profileId, generation, sections: recordOf(sections), stash: recordOf(stash) }
}

function writeDocument(data: StoredDocument): boolean {
  try {
    browserStorage.setItem(STORAGE_KEYS.PROFILE_SECTIONS, JSON.stringify(data))
    return true
  } catch {
    // Quota, blocked site data, or a payload JSON cannot express. setItem is
    // atomic: the previous document is still there, whole.
    return false
  }
}

/** The one gate every write goes through: read, check the fence, change, write.
 *  `change` returns the new document, `null` for "nothing to change", or
 *  `'failed'` when what is stored does not allow the change. */
function fencedWrite(
  profileId: string,
  generation: number,
  change: (doc: StoredDocument) => StoredDocument | null | 'failed',
): WriteResult {
  // 0 is what an unclaimed or pre-fencing document reads as; nobody holds it.
  if (!isProfileId(profileId) || !isGeneration(generation) || generation === 0) return 'failed'
  const doc = readDocument()
  if (doc === null) return 'failed'
  if (doc.generation !== generation || doc.profileId !== profileId) return 'fenced'
  const next = change(doc)
  if (next === null) return 'ok'
  if (next === 'failed') return 'failed'
  return writeDocument(next) ? 'ok' : 'failed'
}

// --- API --------------------------------------------------------------------

/** Another profile's data, bad data and no data all read as empty. Never writes. */
export function loadSectionStore(profileId: string): SectionStoreData {
  const doc = readDocument()
  if (doc === null || doc.profileId !== profileId) return emptyData(profileId)
  return { profileId, sections: doc.sections, stash: doc.stash }
}

/** Call ONCE, on winning the lease. Bumps the generation and returns it — the
 *  token every write must then present. This profile's sections and stash are
 *  kept; another profile's (or nobody's) are discarded, and the count goes on.
 *  `null` = the claim is not stored (storage refused); the previous generation
 *  is still in force and the caller must not write. */
export function claimSectionStore(profileId: string): number | null {
  if (!isProfileId(profileId)) return null
  const doc = readDocument()
  if (doc === null || doc.generation >= Number.MAX_SAFE_INTEGER) return null
  const generation = doc.generation + 1
  const kept = doc.profileId === profileId ? doc : NOTHING
  return writeDocument({ profileId, generation, sections: kept.sections, stash: kept.stash }) ? generation : null
}

/** A section WITHOUT a conflict. One that has a conflict is `'failed'`: it goes
 *  through `saveConflict`, which is what keeps "a stored conflict has its
 *  payloads" true. Saving over a locked section is how the lock is lifted. */
export function saveSection(profileId: string, generation: number, key: string, s: PersistedSection): WriteResult {
  if (typeof key !== 'string' || key === '') return 'failed'
  const section = parseSection(s)
  if (section === null || section.conflict !== undefined) return 'failed'
  return fencedWrite(profileId, generation, (doc) => ({
    ...doc,
    sections: recordOf([...Object.entries(doc.sections).filter(([k]) => k !== key), [key, section]]),
  }))
}

/** A locked section together with the payloads its conflict refers to, in ONE
 *  `setItem`: either all of it is stored or none of it is. `payloads` is by
 *  hash; every non-null hash of `section.conflict` must be in it or already in
 *  the stash, else `'failed'` and nothing is written. */
export function saveConflict(
  profileId: string,
  generation: number,
  key: string,
  s: PersistedSection & { conflict: SectionConflict },
  payloads: Record<string, unknown>,
): WriteResult {
  if (typeof key !== 'string' || key === '' || !isPlainObject(payloads)) return 'failed'
  const section = parseSection(s)
  const conflict = section?.conflict
  if (section === null || conflict === undefined) return 'failed'
  const added = Object.entries(payloads)
  if (!added.every(([hash, payload]) => isHash(hash) && isStashable(payload))) return 'failed'

  const addedHashes = new Set(added.map(([hash]) => hash))
  return fencedWrite(profileId, generation, (doc) => {
    const stash = recordOf([...Object.entries(doc.stash).filter(([h]) => !addedHashes.has(h)), ...added])
    if (neededHashes(conflict).some((h) => !Object.prototype.hasOwnProperty.call(stash, h))) return 'failed'
    return { ...doc, sections: recordOf([...Object.entries(doc.sections).filter(([k]) => k !== key), [key, section]]), stash }
  })
}

/** `'ok'` = the section is not stored (any more). */
export function dropSection(profileId: string, generation: number, key: string): WriteResult {
  return fencedWrite(profileId, generation, (doc) => {
    if (!Object.prototype.hasOwnProperty.call(doc.sections, key)) return null
    return { ...doc, sections: recordOf(Object.entries(doc.sections).filter(([k]) => k !== key)) }
  })
}

/** `'failed'` also covers: bad hash, not a plain object, not serialisable, over
 *  `MAX_STASH_PAYLOAD_BYTES`. */
export function putStash(profileId: string, generation: number, hash: string, payload: unknown): WriteResult {
  if (!isHash(hash) || !isStashable(payload)) return 'failed'
  return fencedWrite(profileId, generation, (doc) => ({
    ...doc,
    stash: recordOf([...Object.entries(doc.stash).filter(([h]) => h !== hash), [hash, payload]]),
  }))
}

export function getStash(profileId: string, hash: string): unknown | undefined {
  const doc = readDocument()
  if (doc === null || doc.profileId !== profileId) return undefined
  if (!Object.prototype.hasOwnProperty.call(doc.stash, hash)) return undefined
  return doc.stash[hash]
}

/** Drop every stashed payload whose hash is not in `keep`. `'ok'` = storage now
 *  holds nothing outside `keep`. */
export function pruneStash(profileId: string, generation: number, keep: ReadonlySet<string>): WriteResult {
  return fencedWrite(profileId, generation, (doc) => {
    const kept = Object.entries(doc.stash).filter(([h]) => keep.has(h))
    if (kept.length === Object.keys(doc.stash).length) return null
    return { ...doc, stash: recordOf(kept) }
  })
}

/** Detach / a change of master: every section and payload goes, whichever
 *  profile it was. Needs no generation — but KEEPS the stored one, in an empty
 *  document that belongs to nobody, rather than removing the key: a removed key
 *  would restart the count at 1, which is exactly the number an old leader may
 *  still be holding. Whoever held the generation is fenced from here on
 *  (`profileId` no longer matches), and the next claim gets a number nobody has
 *  seen.
 *    If storage refuses the write the old document stays. That is tolerable: it
 *  is still tagged with its profile, so another master never reads it, and a
 *  re-attach to the same one is protected by the daemon's CAS. */
export function clearSectionStore(): void {
  const doc = readDocument()
  if (doc === null) return // unreadable storage: writing a 0 over it could only restart the count
  writeDocument({ ...NOTHING, generation: doc.generation })
}
