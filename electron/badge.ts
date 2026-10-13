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

/** The IPC boundary: a well-formed report, or null (the report is then ignored whole). Extra fields are dropped. */
export function parseBadgeReport(raw: unknown): BadgeReport | null {
  if (typeof raw !== 'object' || raw === null) return null
  const { sessions, cards } = raw as Record<string, unknown>
  if (!Array.isArray(sessions) || !Array.isArray(cards)) return null
  if (sessions.length > MAX_ENTRIES || cards.length > MAX_ENTRIES) return null
  if (!sessions.every((s): s is string => typeof s === 'string')) return null
  const out: BadgeCard[] = []
  for (const c of cards) {
    if (typeof c !== 'object' || c === null) return null
    const { id, session, fallback } = c as Record<string, unknown>
    if (typeof id !== 'string' || typeof fallback !== 'string') return null
    if (session !== undefined && typeof session !== 'string') return null
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

const attached = new WeakSet<object>()

/**
 * Drop `sender`'s report when its window closes (`destroyed`), its renderer goes away (`render-process-gone`) or its
 * main frame starts navigating — a reload; not a sub-frame, not the SPA's own same-document routing. Once per sender.
 */
export function attachBadgeSender(center: BadgeCenter, sender: BadgeSender): void {
  if (attached.has(sender)) return
  attached.add(sender)
  const drop = () => center.drop(sender.id)
  sender.on('destroyed', drop)
  sender.on('render-process-gone', drop)
  sender.on('did-start-navigation', (details: { isMainFrame?: boolean; isSameDocument?: boolean }) => {
    if (details.isMainFrame === true && details.isSameDocument !== true) drop()
  })
}
