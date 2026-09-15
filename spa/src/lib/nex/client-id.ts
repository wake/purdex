// spa/src/lib/nex/client-id.ts — the per-tab principal suffix sent as
// X-Pdx-Client on every /api/nex request (P-A spec §4.3). One id per browser
// tab / Electron window so the daemon can arbitrate control leases between
// them; deliberately NOT the per-device sync clientId, which would collapse
// every tab into one principal.
const STORAGE_KEY = 'purdex-nex-client-id'

/** Mirrors the daemon's clientIDPattern (internal/module/nex). */
export const NEX_CLIENT_ID_RE = /^[A-Za-z0-9._-]{1,64}$/

let memoryId: string | null = null

function generate(): string {
  const bytes = new Uint8Array(6)
  crypto.getRandomValues(bytes)
  let n = 0
  for (const b of bytes) n = n * 256 + b
  return `t-${n.toString(36).padStart(8, '0').slice(-8)}`
}

/** Stable for the life of this tab; survives reload via sessionStorage. */
export function getNexClientId(): string {
  if (memoryId) return memoryId
  try {
    const stored = sessionStorage.getItem(STORAGE_KEY)
    if (stored && NEX_CLIENT_ID_RE.test(stored)) {
      memoryId = stored
      return stored
    }
    const fresh = generate()
    sessionStorage.setItem(STORAGE_KEY, fresh)
    memoryId = fresh
    return fresh
  } catch {
    // sessionStorage unavailable (privacy mode, sandboxed frame): same
    // semantics for this page load, just not surviving a reload.
    memoryId = generate()
    return memoryId
  }
}

/** Test hook: forget the memoised id so the next call re-reads storage. */
export function resetNexClientIdForTests(): void {
  memoryId = null
}
