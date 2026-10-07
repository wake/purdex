// spa/src/lib/nex/permission-card-memory.ts — what the reader did on a
// permission request card (PermissionRequestCard), kept outside the component:
// the deny note typed so far, whether the note field is open, and whether the
// input preview is expanded — and whether this pane already closed the request
// (answered it, or found it already ended), read by usePermissionAnswer. A
// worker pane unmounts when its tab is switched away (keepAliveCount 0 is the
// default; an execution pane is not a "light" kind), and the card's own state
// would come back empty — and a closed request, still pending in the store
// until its `permission.resolved` arrives, would come back as a card (spec
// §5.3: after an answer the card goes away). Memory only, like
// worker-draft-memory: a reload starts fresh.
//
// Keyed `${hostId}:${executionId}:${requestId}`, so another request — of this
// worker or of another — never inherits it. ExecutionView prunes an
// execution's entries, drafts and closed marks by one rule: once their request
// no longer waits there (resolved, cancelled, expired) or the worker has
// ended, so neither can grow without bound. Closing drops the draft at once.
// Pruning matches the `${hostId}:${executionId}:` prefix; Nexen execution ids
// carry no ':' that could make one pair's prefix another's.

export interface PermissionCardMemo {
  note: string
  noteOpen: boolean
  expanded: boolean
}

const DEFAULTS: PermissionCardMemo = { note: '', noteOpen: false, expanded: false }

const memos = new Map<string, PermissionCardMemo>()
/** Requests this pane closed itself, by key; kept until the store stops listing them as pending. */
const closed = new Set<string>()

export const permissionCardKey = (hostId: string, executionId: string, requestId: string) => `${hostId}:${executionId}:${requestId}`

const executionPrefix = (hostId: string, executionId: string) => `${hostId}:${executionId}:`

export function readPermissionCard(key: string): PermissionCardMemo {
  return memos.get(key) ?? DEFAULTS
}

/** Merge a change; an entry back at the defaults is forgotten. */
export function writePermissionCard(key: string, change: Partial<PermissionCardMemo>): void {
  const next = { ...readPermissionCard(key), ...change }
  if (next.note === '' && !next.noteOpen && !next.expanded) memos.delete(key)
  else memos.set(key, next)
}

/** This pane closed the request (answered it, or found it already ended): it is hidden from now on, and its draft goes. */
export function closePermissionCard(key: string): void {
  memos.delete(key)
  closed.add(key)
}

export function isPermissionCardClosed(key: string): boolean {
  return closed.has(key)
}

/** The request ids of `hostId`/`executionId` this pane has closed. */
export function closedPermissionRequests(hostId: string, executionId: string): ReadonlySet<string> {
  const prefix = executionPrefix(hostId, executionId)
  const ids = new Set<string>()
  for (const key of closed) if (key.startsWith(prefix)) ids.add(key.slice(prefix.length))
  return ids
}

/** Forget the drafts and closed marks of `hostId`/`executionId` whose request `keep` rejects — every one of them without `keep`. */
export function prunePermissionCards(hostId: string, executionId: string, keep?: (requestId: string) => boolean): void {
  const prefix = executionPrefix(hostId, executionId)
  const drop = (key: string) => key.startsWith(prefix) && !keep?.(key.slice(prefix.length))
  for (const key of [...memos.keys()]) if (drop(key)) memos.delete(key)
  for (const key of [...closed]) if (drop(key)) closed.delete(key)
}

/** Tests only: how many drafts are held. */
export function permissionCardCount(): number {
  return memos.size
}

/** Tests only: module state outlives a test, and a leaked entry seeds the next one's card. */
export function clearAllPermissionCards(): void {
  memos.clear()
  closed.clear()
}
