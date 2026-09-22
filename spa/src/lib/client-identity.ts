import { browserStorage } from './storage/browser-backend'
import { STORAGE_KEYS } from './storage/keys'

// ---------------------------------------------------------------------------
// Client identity
//
// The stable id of this browser profile: "c_" + 12 lowercase hex chars. The
// daemon keys storage backups and profile writes on it.
//
// It used to live in `useSyncStore` (persisted under `purdex-sync-state`). The
// Sync module is going away, so the id has its own key here — and, on first
// use, ADOPTS the id the Sync store had persisted, because everything already
// on the daemon is filed under that id.
//
// Storage is the truth. There is deliberately NO permanent in-memory cache:
// every window of one profile shares this key, and two windows racing the very
// first call each generate an id. A cache would let each keep its own forever;
// reading the key on every call makes both converge on the last writer by
// their next call. The stored value is the bare id string (not JSON).
// ---------------------------------------------------------------------------

const CLIENT_ID_PATTERN = /^c_[0-9a-f]{12}$/

function isClientId(value: unknown): value is string {
  return typeof value === 'string' && CLIENT_ID_PATTERN.test(value)
}

export function generateClientId(): string {
  const bytes = crypto.getRandomValues(new Uint8Array(6))
  const hex = Array.from(bytes)
    .map((b) => b.toString(16).padStart(2, '0'))
    .join('')
  return `c_${hex}`
}

/**
 * Only for when storage cannot hold the id (private mode, blocked site data,
 * quota): a generated id kept for the life of this realm so callers at least
 * get a stable answer. It is never consulted while the key holds a valid id,
 * so it is not the cache the header warns about. It does not survive a reload
 * and is not shared with other windows — nothing can be, without storage.
 */
let realmFallbackId: string | null = null

function readStored(): string | null {
  try {
    const raw = browserStorage.getItem(STORAGE_KEYS.CLIENT_IDENTITY)
    return isClientId(raw) ? raw : null
  } catch {
    return null
  }
}

/** `state.clientId` out of zustand persist's `{state, version}` envelope for the Sync store. */
function readLegacySyncClientId(): string | null {
  try {
    const raw = browserStorage.getItem(STORAGE_KEYS.SYNC_STATE)
    if (typeof raw !== 'string') return null
    const parsed = JSON.parse(raw) as { state?: { clientId?: unknown } } | null
    const candidate = parsed?.state?.clientId
    return isClientId(candidate) ? candidate : null
  } catch {
    return null
  }
}

export function getClientId(): string {
  const stored = readStored()
  if (stored) return stored

  const candidate = readLegacySyncClientId() ?? (realmFallbackId ??= generateClientId())
  try {
    // No store is registered with syncManager under this key, so the notify()
    // inside setItem is a broadcast other windows look up and drop.
    browserStorage.setItem(STORAGE_KEYS.CLIENT_IDENTITY, candidate)
  } catch {
    // Unwritable storage — fall through; the read-back decides.
  }
  // Read back rather than trust `candidate`: whatever is stored is the answer.
  return readStored() ?? candidate
}

/**
 * Whether the id `getClientId()` answers with is actually IN storage — i.e.
 * will still be this client's id after a reload and in its other windows.
 * False means the answer is the realm fallback (or a legacy id that could not
 * be copied to its own key): writing THAT to a daemon leaves a row no later
 * session can claim back. `getClientId()` itself cannot say which one it gave.
 *
 * It calls `getClientId()`, so asking also performs the first-use creation and
 * `if (!isClientIdPersisted()) refuse` is the whole check. True iff the key
 * holds a valid id right now and that id is what `getClientId()` returned; a
 * window that rewrites the key between the two reads makes this answer false
 * once, which errs on the safe side — ask again.
 */
export function isClientIdPersisted(): boolean {
  const id = getClientId()
  return readStored() === id
}
