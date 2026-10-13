// electron/badge.ts — the Mac Dock badge, main side (app-badge spec §3.2). Kept free of `electron` imports so it can be
// unit-tested; badge-ipc.ts wires it to ipcMain and `app.setBadgeCount`.
//
// Every window reports the session keys it resolved and every open card by identity. The badge is |S|, S being the
// union over windows of `sessions`, plus for each card id the session some window resolved it to (else its fallback).
// A window's report is dropped when its window closes, its renderer goes away, or its main frame starts navigating
// (a reload): the page that comes next reports afresh.

export interface BadgeCard {
  /** `hostId NUL approvalId`: the same in every window. */
  id: string
  /** The `hostId:code` this window resolved the card to; absent when it could not. */
  session?: string
  /** `c:hostId:<session_id>` or `a:hostId:<approval id>`: what counts when no window resolved the card. */
  fallback: string
}

export interface BadgeReport {
  sessions: string[]
  cards: BadgeCard[]
}

/** Far above what any window holds (a session is a tab or a card); a bigger report is not ours. */
const MAX_ENTRIES = 10_000
/** Far above any host id + session code / approval id / session id; the entry cap alone does not bound the bytes. */
export const MAX_STRING = 512

const isShortString = (v: unknown): v is string => typeof v === 'string' && v.length <= MAX_STRING

/** The IPC boundary: a well-formed report, or null (the report is then ignored whole). Extra fields are dropped. */
export function parseBadgeReport(raw: unknown): BadgeReport | null {
  if (typeof raw !== 'object' || raw === null) return null
  const { sessions, cards } = raw as Record<string, unknown>
  if (!Array.isArray(sessions) || !Array.isArray(cards)) return null
  if (sessions.length > MAX_ENTRIES || cards.length > MAX_ENTRIES) return null
  if (!sessions.every(isShortString)) return null
  const out: BadgeCard[] = []
  for (const c of cards) {
    if (typeof c !== 'object' || c === null) return null
    const { id, session, fallback } = c as Record<string, unknown>
    if (!isShortString(id) || !isShortString(fallback)) return null
    if (session !== undefined && !isShortString(session)) return null
    out.push({ id, fallback, ...(session !== undefined ? { session } : {}) })
  }
  return { sessions: [...sessions], cards: out }
}

/**
 * The badge number for these reports. Entries are namespaced (`s|` a session key, `f|` a fallback) so a session key
 * that happens to read like a fallback never merges with it.
 */
export function badgeCount(reports: Iterable<BadgeReport>): number {
  const s = new Set<string>()
  const resolved = new Map<string, string>()
  const fallbacks = new Map<string, string>()
  for (const r of reports) {
    for (const k of r.sessions) s.add(`s|${k}`)
    for (const c of r.cards) {
      fallbacks.set(c.id, c.fallback)
      // Two windows may resolve one card differently (a stale session list in one): the smallest key wins, so the
      // count never depends on which window reported first.
      if (c.session !== undefined && (resolved.get(c.id) === undefined || c.session < resolved.get(c.id)!)) resolved.set(c.id, c.session)
    }
  }
  for (const [id, fallback] of fallbacks) {
    const session = resolved.get(id)
    s.add(session !== undefined ? `s|${session}` : `f|${fallback}`)
  }
  return s.size
}

/** The last report of each window, keyed by `webContents.id`; applies the count whenever it changes. */
export class BadgeCenter {
  private readonly reports = new Map<number, BadgeReport>()
  private applied: number | null = null

  constructor(private readonly apply: (count: number) => void) {}

  set(windowId: number, report: BadgeReport): void {
    this.reports.set(windowId, report)
    this.recompute()
  }

  drop(windowId: number): void {
    if (this.reports.delete(windowId)) this.recompute()
  }

  /** Start-up: nothing is reported yet, and a crashed run may have left a stale Dock badge behind. */
  clear(): void {
    this.recompute()
  }

  private recompute(): void {
    const n = badgeCount(this.reports.values())
    if (n === this.applied) return
    this.applied = n
    this.apply(n)
  }
}

/** The part of `webContents` this needs. */
export interface BadgeSender {
  id: number
  on(event: string, listener: (...args: any[]) => void): unknown // eslint-disable-line @typescript-eslint/no-explicit-any
}

/** What main knows of one window's documents: the id the last accepted report carried, and the ids that are over. */
interface SenderState {
  current?: string
  /** Oldest first. A document that old has long been gone, so the list is bounded. */
  stale: string[]
}
const MAX_STALE_DOCS = 8
const senders = new WeakMap<object, SenderState>()

/**
 * Drop `sender`'s report when its window closes (`destroyed`), its renderer goes away (`render-process-gone`) or its
 * main frame starts navigating — a reload; not a sub-frame, not the SPA's own same-document routing. The last two also
 * mark the document that was reporting as over (`SenderState.stale`), see `submitBadgeReport`. Once per sender:
 * returns true the one time it attached, so a caller can hang its own per-sender listeners exactly once as well.
 */
export function attachBadgeSender(center: BadgeCenter, sender: BadgeSender): boolean {
  if (senders.has(sender)) return false
  const state: SenderState = { stale: [] }
  senders.set(sender, state)
  const drop = () => center.drop(sender.id)
  const documentOver = () => {
    if (state.current !== undefined) {
      state.stale.push(state.current)
      if (state.stale.length > MAX_STALE_DOCS) state.stale.shift()
      state.current = undefined
    }
    drop()
  }
  sender.on('destroyed', drop)
  sender.on('render-process-gone', documentOver)
  sender.on('did-start-navigation', (details: { isMainFrame?: boolean; isSameDocument?: boolean }) => {
    if (details.isMainFrame === true && details.isSameDocument !== true) documentOver()
  })
  return true
}

/**
 * One `badge:set` from `sender`: false (and nothing recorded) for a malformed report, which is ignored whole — the
 * window's earlier report stays, as it is still the last state it told us. `onFirst` runs once per sender, on its first
 * valid report; a window reports on every change, so per-report listeners would pile up until it closes.
 *
 * `doc` is the id the window's preload minted for the document that sent the report (one per document: the preload
 * runs again on every navigation). A report already queued by a document that is going away can reach main after the
 * navigation started and dropped its report; carrying an id main has marked over, it is refused instead of bringing the
 * dropped report back. The next document has a new id, so its first report is taken whatever the order of events. No
 * `doc` (an older preload) is never refused.
 */
export function submitBadgeReport(center: BadgeCenter, sender: BadgeSender, raw: unknown, doc?: unknown, onFirst?: () => void): boolean {
  const report = parseBadgeReport(raw)
  if (!report) return false
  if (attachBadgeSender(center, sender)) onFirst?.()
  const state = senders.get(sender)!
  const id = isShortString(doc) ? doc : undefined
  if (id !== undefined) {
    if (state.stale.includes(id)) return false
    state.current = id
  }
  center.set(sender.id, report)
  return true
}
