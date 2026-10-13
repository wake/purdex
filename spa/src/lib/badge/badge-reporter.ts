// spa/src/lib/badge/badge-reporter.ts — sends this window's Dock-badge report to Electron main (app-badge spec §3.2,
// §4.1). It watches every input of `badgeReport` (unread, tabs, open cards, hook cards, the host session lists) and,
// 300 ms after the last change, sends the report when it differs from the one last sent. The first report goes out
// even when empty: main drops a window's report on reload, so a fresh page always says where it stands.
// Outside Electron — or in an app build without `electronAPI.badge` — nothing is subscribed and nothing is sent.
import { useAgentStore } from '../../stores/useAgentStore'
import { useApprovalStore } from '../../stores/useApprovalStore'
import { useHookCardStore } from '../../stores/useHookCardStore'
import { useSessionStore } from '../../stores/useSessionStore'
import { useTabStore } from '../../stores/useTabStore'
import { badgeReport, knownSessionIdsOf, type BadgeReport } from './badge-report'

export const BADGE_DEBOUNCE_MS = 300

/** The report from the stores' current state. Sorted (see `badgeReport`), so equal states give equal text. */
export function currentBadgeReport(): BadgeReport {
  const tabs = useTabStore.getState().tabs
  return badgeReport({
    unread: useAgentStore.getState().unread,
    tabs,
    cards: [...Object.values(useApprovalStore.getState().entries), ...Object.values(useHookCardStore.getState().entries)],
    sessions: useSessionStore.getState().sessions,
    knownSessionIds: knownSessionIdsOf(tabs),
  })
}

/** Starts reporting; returns the stop function. */
export function startBadgeReporter(): () => void {
  const badge = window.electronAPI?.badge
  if (!badge) return () => {}

  let timer: ReturnType<typeof setTimeout> | null = null
  let lastSent: string | null = null

  const flush = () => {
    timer = null
    const report = currentBadgeReport()
    const text = JSON.stringify(report)
    if (text === lastSent) return
    lastSent = text
    badge.set(report)
  }
  const schedule = () => {
    if (timer !== null) clearTimeout(timer)
    timer = setTimeout(flush, BADGE_DEBOUNCE_MS)
  }

  const unsubs = [
    useAgentStore.subscribe((s, p) => { if (s.unread !== p.unread) schedule() }),
    useTabStore.subscribe((s, p) => { if (s.tabs !== p.tabs) schedule() }),
    useApprovalStore.subscribe((s, p) => { if (s.entries !== p.entries) schedule() }),
    useHookCardStore.subscribe((s, p) => { if (s.entries !== p.entries) schedule() }),
    useSessionStore.subscribe((s, p) => { if (s.sessions !== p.sessions) schedule() }),
  ]
  schedule()

  return () => {
    for (const u of unsubs) u()
    if (timer !== null) clearTimeout(timer)
    timer = null
  }
}
