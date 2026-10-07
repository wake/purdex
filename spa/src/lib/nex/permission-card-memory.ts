// spa/src/lib/nex/permission-card-memory.ts — what the reader did on a
// permission request card (PermissionRequestCard), kept outside the component:
// the deny note typed so far, whether the note field is open, and whether the
// input preview is expanded. A worker pane unmounts when its tab is switched
// away (keepAliveCount 0 is the default; an execution pane is not a "light"
// kind), and the card's own state would come back empty. Memory only, like
// worker-draft-memory: a reload starts fresh.
//
// Keyed `${hostId}:${executionId}:${requestId}`, so another request — of this
// worker or of another — never inherits it. ExecutionView prunes an
// execution's entries once their request no longer waits there (answered,
// resolved, cancelled, expired) or the worker has ended, so the map cannot
// grow without bound. Pruning matches the `${hostId}:${executionId}:` prefix;
// Nexen execution ids carry no ':' that could make one pair's prefix another's.

export interface PermissionCardMemo {
  note: string
  noteOpen: boolean
  expanded: boolean
}

const DEFAULTS: PermissionCardMemo = { note: '', noteOpen: false, expanded: false }

const memos = new Map<string, PermissionCardMemo>()

export const permissionCardKey = (hostId: string, executionId: string, requestId: string) => `${hostId}:${executionId}:${requestId}`

export function readPermissionCard(key: string): PermissionCardMemo {
  return memos.get(key) ?? DEFAULTS
}

/** Merge a change; an entry back at the defaults is forgotten. */
export function writePermissionCard(key: string, change: Partial<PermissionCardMemo>): void {
  const next = { ...readPermissionCard(key), ...change }
  if (next.note === '' && !next.noteOpen && !next.expanded) memos.delete(key)
  else memos.set(key, next)
}

/** Forget the entries of `hostId`/`executionId` whose request `keep` rejects — every one of them without `keep`. */
export function prunePermissionCards(hostId: string, executionId: string, keep?: (requestId: string) => boolean): void {
  const prefix = `${hostId}:${executionId}:`
  for (const key of [...memos.keys()]) {
    if (!key.startsWith(prefix)) continue
    if (keep?.(key.slice(prefix.length))) continue
    memos.delete(key)
  }
}

/** Tests only: how many entries are held. */
export function permissionCardCount(): number {
  return memos.size
}

/** Tests only: module state outlives a test, and a leaked entry seeds the next one's card. */
export function clearAllPermissionCards(): void {
  memos.clear()
}
