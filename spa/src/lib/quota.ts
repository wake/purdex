/**
 * isQuotaError — true when `err` is a storage-quota exhaustion thrown by
 * IndexedDB / the Storage API. Lifted out of `sync/snapshot-store.ts` (T1c-4)
 * into this shared leaf so both the Sync snapshot store and the Storage upload
 * path detect a full quota through ONE implementation (no second copy).
 *
 * Matches the standard `DOMException` name `QuotaExceededError` plus the historic
 * numeric codes 22 (standard) and 1014 (Firefox). A non-`DOMException` value
 * (a plain `Error`, a bare object that merely looks like one, etc.) is never a
 * quota error.
 */
export function isQuotaError(err: unknown): boolean {
  if (err instanceof DOMException) {
    if (err.name === 'QuotaExceededError') return true
    // Historic numeric codes: 22 (standard), 1014 (Firefox).
    const code = (err as DOMException & { code?: number }).code
    if (code === 22 || code === 1014) return true
  }
  return false
}
