// spa/src/lib/device-state/reattach.ts — session reconciliation for device-state
// restores (spec §4.2 / §4.3 step 2).
//
// `markMissingHosts` marks every tmux-session pane on a host this computer does
// not know as `host-removed` and drops that host from `sessionMeta`, so
// `reattachByName` never contacts it.
//
// `reattachByName` differs from `ensureSessions` on purpose: it matches by host
// + name (D4), so a same-name session whose code changed is re-pointed, and it
// never creates sessions. Its evidence guards mirror revive-by-name
// (`lib/rebuild/revive.ts`): `remapLayoutSessions` stamps the session's `code`
// and `tmux_instance` onto the pane, so both must be non-empty strings, and a
// terminal pane may only bind to a terminal (or mode-less) session.
import { listSessions } from '../host-api'
import type { Session } from '../host-api'
import { scanPaneTree, updatePaneInLayout } from '../pane-tree'
import type { Tab } from '../../types/tab'
import type { EnsureReport, Remap, RemapEntry, SessionMeta, WorkspaceSnapshot } from '../snapshot/types'

export function markMissingHosts(
  snap: WorkspaceSnapshot,
  hostIds: ReadonlySet<string>,
): { snap: WorkspaceSnapshot; hostRemoved: number } {
  let hostRemoved = 0
  const tabs: Record<string, Tab> = {}

  for (const [tabId, tab] of Object.entries(snap.tabs)) {
    let layout = tab.layout
    scanPaneTree(tab.layout, (pane) => {
      const c = pane.content
      if (c.kind !== 'tmux-session' || hostIds.has(c.hostId)) return
      hostRemoved++
      layout = updatePaneInLayout(layout, pane.id, { ...c, terminated: 'host-removed' })
    })
    tabs[tabId] = layout === tab.layout ? tab : { ...tab, layout }
  }

  const sessionMeta: WorkspaceSnapshot['sessionMeta'] = {}
  for (const [hostId, perHost] of Object.entries(snap.sessionMeta)) {
    if (hostIds.has(hostId)) sessionMeta[hostId] = perHost
  }

  return { snap: { ...snap, tabs, sessionMeta }, hostRemoved }
}

function usable(s: Session): boolean {
  return typeof s.code === 'string' && s.code !== ''
    && typeof s.tmux_instance === 'string' && s.tmux_instance !== ''
}

function compatible(meta: SessionMeta, s: Session): boolean {
  if (meta.mode !== 'terminal') return false // stream panes are never reattached
  return s.mode === undefined || s.mode === 'terminal'
}

export async function reattachByName(
  sessionMeta: WorkspaceSnapshot['sessionMeta'],
): Promise<{ remap: Remap; report: EnsureReport }> {
  const remap: Remap = {}
  const report: EnsureReport = { reattached: 0, rebuilt: 0, failed: 0 }

  for (const [hostId, perHost] of Object.entries(sessionMeta)) {
    const perHostRemap: Record<string, RemapEntry> = {}
    remap[hostId] = perHostRemap

    let live: Session[] | null
    try {
      live = await listSessions(hostId)
    } catch {
      live = null
    }

    for (const [oldCode, meta] of Object.entries(perHost)) {
      const match = live?.find((s) => s.name === meta.name && usable(s) && compatible(meta, s))
      if (match) {
        perHostRemap[oldCode] = { status: 'reattached', newCode: match.code, session: match }
        report.reattached++
      } else {
        perHostRemap[oldCode] = { status: 'failed' }
        report.failed++
      }
    }
  }

  return { remap, report }
}
