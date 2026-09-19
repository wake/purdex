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
// destroys it (the user may re-attach); the first WRITE for another profile
// replaces it wholesale.
//
// VALIDATED, NOT CAST. A section that fails validation is dropped ALONE. The
// trade-off: losing one section's base only makes that section start from
// `{0, null}` and be re-judged after the next reindex — cheap, and at worst a
// conflict the user resolves once. Throwing the whole document away would do
// that to every section, and lose every retained conflict snapshot, because of
// one bad entry. Only a document that is unreadable at the top level is empty.
//
// WRITES NEVER THROW. Each returns whether the value is now stored. A refused
// `setItem` (quota, blocked site data) leaves the PREVIOUS value in place —
// `localStorage.setItem` is atomic, there is no half-written document — so the
// caller loses this one update and nothing else.
import { browserStorage } from '../storage/browser-backend'
import { STORAGE_KEYS } from '../storage/keys'
import type { Held, SectionConflict } from './sync-state'

export interface PersistedSection {
  base: Held
  currentHash: string | null
  conflict?: SectionConflict
}

export interface SectionStoreData {
  profileId: string
  sections: Record<string, PersistedSection>
  /** Payloads by hash — only the ones `retainedHashes` asks the driver to keep. */
  stash: Record<string, unknown>
}

/** A single stashed payload may not serialise to more than this many UTF-8
 *  bytes. The daemon refuses a larger section anyway, so such a payload could
 *  never be pushed — and it would eat the whole localStorage quota. */
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

function emptyData(profileId: string): SectionStoreData {
  return { profileId, sections: {}, stash: {} }
}

/** `Object.fromEntries` defines own data properties, so a key named
 *  `__proto__` stays a key instead of becoming the record's prototype. */
function recordOf<T>(entries: Array<[string, T]>): Record<string, T> {
  return Object.fromEntries(entries) as Record<string, T>
}

// --- storage ----------------------------------------------------------------

/** The stored document, validated — whichever profile it belongs to. */
function readDocument(): SectionStoreData | null {
  let parsed: unknown
  try {
    const raw = browserStorage.getItem(STORAGE_KEYS.PROFILE_SECTIONS)
    if (typeof raw !== 'string') return null
    parsed = JSON.parse(raw)
  } catch {
    return null
  }
  if (!isRecord(parsed) || !isProfileId(parsed.profileId)) return null
  if (!isRecord(parsed.sections) || !isRecord(parsed.stash)) return null

  const sections: Array<[string, PersistedSection]> = []
  for (const [key, value] of Object.entries(parsed.sections)) {
    const section = key === '' ? null : parseSection(value)
    if (section !== null) sections.push([key, section]) // a bad section is dropped alone
  }
  const stash: Array<[string, unknown]> = []
  for (const [hash, payload] of Object.entries(parsed.stash)) {
    if (isHash(hash) && isPlainObject(payload)) stash.push([hash, payload])
  }
  return { profileId: parsed.profileId, sections: recordOf(sections), stash: recordOf(stash) }
}

function writeDocument(data: SectionStoreData): boolean {
  try {
    browserStorage.setItem(STORAGE_KEYS.PROFILE_SECTIONS, JSON.stringify(data))
    return true
  } catch {
    // Quota, blocked site data, or a payload JSON cannot express. setItem is
    // atomic: the previous document is still there, whole.
    return false
  }
}

/** For a write: this profile's document, or a fresh one if storage holds
 *  nothing usable or another profile's data (which the write then replaces). */
function ownedOrFresh(profileId: string): SectionStoreData {
  const doc = readDocument()
  return doc !== null && doc.profileId === profileId ? doc : emptyData(profileId)
}

/** For a change that only removes: null when there is nothing of this profile's to touch. */
function ownedOrNull(profileId: string): SectionStoreData | null {
  const doc = readDocument()
  return doc !== null && doc.profileId === profileId ? doc : null
}

// --- API --------------------------------------------------------------------

/** Another profile's data, bad data and no data all read as empty. Never writes. */
export function loadSectionStore(profileId: string): SectionStoreData {
  return ownedOrNull(profileId) ?? emptyData(profileId)
}

/** `false` = not stored (malformed input, or storage refused the write). */
export function saveSection(profileId: string, key: string, s: PersistedSection): boolean {
  if (!isProfileId(profileId) || typeof key !== 'string' || key === '') return false
  const section = parseSection(s)
  if (section === null) return false
  const doc = ownedOrFresh(profileId)
  return writeDocument({ ...doc, sections: recordOf([...Object.entries(doc.sections).filter(([k]) => k !== key), [key, section]]) })
}

/** `true` = the section is not stored (any more). Another profile's data is left alone. */
export function dropSection(profileId: string, key: string): boolean {
  const doc = ownedOrNull(profileId)
  if (doc === null || !Object.prototype.hasOwnProperty.call(doc.sections, key)) return true
  return writeDocument({ ...doc, sections: recordOf(Object.entries(doc.sections).filter(([k]) => k !== key)) })
}

/** `false` = not stashed: bad hash, not a plain object, not serialisable, over
 *  `MAX_STASH_PAYLOAD_BYTES`, or storage refused the write. */
export function putStash(profileId: string, hash: string, payload: unknown): boolean {
  if (!isProfileId(profileId) || !isHash(hash) || !isPlainObject(payload)) return false
  let serialised: string
  try {
    serialised = JSON.stringify(payload)
  } catch {
    return false // a cycle, a BigInt
  }
  // Every code unit is at least one byte, so the cheap check spares encoding the obvious cases.
  if (serialised.length > MAX_STASH_PAYLOAD_BYTES) return false
  if (new TextEncoder().encode(serialised).length > MAX_STASH_PAYLOAD_BYTES) return false
  const doc = ownedOrFresh(profileId)
  return writeDocument({ ...doc, stash: recordOf([...Object.entries(doc.stash).filter(([h]) => h !== hash), [hash, payload]]) })
}

export function getStash(profileId: string, hash: string): unknown | undefined {
  const doc = ownedOrNull(profileId)
  if (doc === null || !Object.prototype.hasOwnProperty.call(doc.stash, hash)) return undefined
  return doc.stash[hash]
}

/** Drop every stashed payload whose hash is not in `keep`. `true` = storage now
 *  holds nothing outside `keep`. Another profile's data is left alone. */
export function pruneStash(profileId: string, keep: ReadonlySet<string>): boolean {
  const doc = ownedOrNull(profileId)
  if (doc === null) return true
  const kept = Object.entries(doc.stash).filter(([h]) => keep.has(h))
  if (kept.length === Object.keys(doc.stash).length) return true
  return writeDocument({ ...doc, stash: recordOf(kept) })
}

/** Detach / a change of master: everything goes, whichever profile it was. */
export function clearSectionStore(): void {
  try {
    browserStorage.removeItem(STORAGE_KEYS.PROFILE_SECTIONS)
  } catch {
    // Unwritable storage holds nothing worth clearing that a later write would not replace.
  }
}
