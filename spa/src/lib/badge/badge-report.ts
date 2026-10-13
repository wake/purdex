// spa/src/lib/badge/badge-report.ts — what one window tells Electron main for the Dock badge (app-badge spec §3.2, §4.1).
//
// The badge is one number: the count of distinct sessions that have an unread event on this Mac or an open card. A
// window cannot count by itself (another window may show the same session, a card is the same in every window), so it
// reports its resolved session keys and every open card by identity; main takes the union (electron/badge.ts).
//
// - `sessions`: every `hostId:code` with `unread[k] === true` that some live pane of ANY tab in this window shows
//   (every workspace, every split pane, worker `exec-` panes too; a pane whose session ended is not live), plus the key
//   each card resolved to. A card counts whether or not its session has a tab; an unread event only when it has one.
// - `cards`: every open card, `id` = `hostId NUL approvalId` (the same in every window), `session` = the key this
//   window resolved it to (absent when it could not), `fallback` = what main counts when no window resolved it.
//
// A card resolves to the session whose `name` is the session part of `origin.tmux` in that host's session list. When the
// window knows a Claude Code session id for that key, it must equal `origin.session_id`: otherwise the tmux session was
// recreated under the same name and the card belongs to another run, so it stays unresolved.
import { approvalKey } from '../../stores/useApprovalStore'
import { compositeKey } from '../composite-key'
import { paneAgentKey, scanPaneTree } from '../pane-tree'
import type { Approval } from '../team/types'
import type { Tab } from '../../types/tab'

export interface BadgeCard {
  id: string
  session?: string
  fallback: string
}

export interface BadgeReport {
  sessions: string[]
  cards: BadgeCard[]
}

export interface BadgeReportInput {
  /** `useAgentStore.unread`, keyed `hostId:code`. */
  unread: Record<string, boolean>
  /** Every tab of this window (`useTabStore.tabs`). */
  tabs: Record<string, Pick<Tab, 'layout'>>
  /** Every open card: the approval store's entries and the hook-card slice's. */
  cards: ReadonlyArray<{ hostId: string; approval: Approval }>
  /** Per host, the session list (`useSessionStore.sessions`). */
  sessions: Record<string, ReadonlyArray<{ code: string; name: string }>>
  /** The Claude Code session ids this window has on record for a key (see `knownSessionIdsOf`). */
  knownSessionIds: (key: string) => readonly string[]
}

/**
 * The agent session ids recorded on the panes of `tabs` (`rebuild.agent.sessionId`, written from a SessionStart), per
 * key `hostId:code`. A key shown by several panes may have several; a key with none has no record.
 */
export function knownSessionIdsOf(tabs: Record<string, Pick<Tab, 'layout'>>): (key: string) => readonly string[] {
  const byKey = new Map<string, Set<string>>()
  for (const tab of Object.values(tabs)) {
    scanPaneTree(tab.layout, (pane) => {
      const c = pane.content
      // An ended pane's record belongs to the run that ended; its code may now be a recreated session's (#1840).
      if (c.kind !== 'tmux-session' || c.terminated) return
      const id = c.rebuild?.agent?.sessionId
      if (!id) return
      const key = compositeKey(c.hostId, c.sessionCode)
      const set = byKey.get(key)
      if (set) set.add(id)
      else byKey.set(key, new Set([id]))
    })
  }
  return (key) => [...(byKey.get(key) ?? [])]
}

/** `<session>:@<win>.%<pane>` → the session part; '' when there is none. tmux session names never contain ':'. */
function tmuxSessionName(tmux: string): string {
  const i = tmux.indexOf(':')
  return i < 0 ? '' : tmux.slice(0, i)
}

function resolveCard(hostId: string, a: Approval, input: BadgeReportInput): string | undefined {
  const name = tmuxSessionName(a.origin.tmux)
  if (name === '') return undefined
  const hit = input.sessions[hostId]?.find((s) => s.name === name)
  if (!hit) return undefined
  const key = compositeKey(hostId, hit.code)
  const known = input.knownSessionIds(key)
  if (known.length > 0 && !known.includes(a.origin.session_id)) return undefined
  return key
}

export function badgeReport(input: BadgeReportInput): BadgeReport {
  // Only LIVE panes: `paneAgentKey` gives nothing for a pane whose session ended, where `tabAgentPanes` keeps the
  // primary pane's key for its light. A code is handed out again by a restarted tmux server (#1840), so an ended pane's
  // leftover unread must not count toward whatever session now carries that code.
  const shown = new Set<string>()
  for (const tab of Object.values(input.tabs)) {
    scanPaneTree(tab.layout, (pane) => {
      const a = paneAgentKey(pane.content)
      if (a) shown.add(compositeKey(a.hostId, a.sessionCode))
    })
  }

  const sessions = new Set<string>()
  for (const [key, unread] of Object.entries(input.unread)) if (unread === true && shown.has(key)) sessions.add(key)

  const cards: BadgeCard[] = []
  for (const { hostId, approval: a } of input.cards) {
    const session = resolveCard(hostId, a, input)
    if (session) sessions.add(session)
    const fallback = a.origin.session_id ? `c:${hostId}:${a.origin.session_id}` : `a:${hostId}:${a.id}`
    cards.push({ id: approvalKey(hostId, a.id), ...(session ? { session } : {}), fallback })
  }
  cards.sort((x, y) => (x.id < y.id ? -1 : x.id > y.id ? 1 : 0))
  return { sessions: [...sessions].sort(), cards }
}
