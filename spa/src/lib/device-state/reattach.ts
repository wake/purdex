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

// No mode gate on either side since P-D.3: every pane is a terminal pane,
// and a live session is a tmux session whatever a pre-P-D.2 peer daemon
// still reports as its mode — the terminal WS attaches to it the same way.
// Snapshot restore, revive and the session picker apply the same policy, so
// one remote state cannot look alive from one entry point and lost from
// another (codex F1 on P-D.3a).
function compatible(_meta: SessionMeta, _s: Session): boolean {
  return true
}

/** Upper bound for one host's session listing; a hung host must not hold the
 *  operation lock (`withOperationLock`) around a restore forever. */
export const REATTACH_LIST_TIMEOUT_MS = 10_000

/** Resolves to the host's sessions, or `null` when the listing throws or does
 *  not settle within `timeoutMs`. The timer is always cleared on settle. */
async function listWithTimeout(hostId: string, timeoutMs: number): Promise<Session[] | null> {
  let timer: ReturnType<typeof setTimeout> | undefined
  const timeout = new Promise<null>((resolve) => {
    timer = setTimeout(() => resolve(null), timeoutMs)
  })
  try {
    return await Promise.race([listSessions(hostId).catch(() => null), timeout])
  } finally {
    clearTimeout(timer)
  }
}

export async function reattachByName(
  sessionMeta: WorkspaceSnapshot['sessionMeta'],
  opts?: { timeoutMs?: number },
): Promise<{ remap: Remap; report: EnsureReport }> {
  const timeoutMs = opts?.timeoutMs ?? REATTACH_LIST_TIMEOUT_MS
  const remap: Remap = {}
  const report: EnsureReport = { reattached: 0, rebuilt: 0, failed: 0 }

  const hosts = Object.entries(sessionMeta)
  // Hosts are listed concurrently so the total wait is bounded by the slowest
  // single host, not the sum; each host fails in isolation.
  const listings = await Promise.all(hosts.map(([hostId]) => listWithTimeout(hostId, timeoutMs)))

  hosts.forEach(([hostId, perHost], i) => {
    const live = listings[i]
    const perHostRemap: Record<string, RemapEntry> = {}
    remap[hostId] = perHostRemap

    for (const [oldCode, meta] of Object.entries(perHost)) {
      // Several saved codes may share a name and all map to the one live
      // session of that name (tmux names are unique per server). More than one
      // usable live candidate means malformed daemon data: refuse to guess.
      const matches = live?.filter((s) => s.name === meta.name && usable(s) && compatible(meta, s)) ?? []
      if (matches.length === 1) {
        const match = matches[0]
        perHostRemap[oldCode] = { status: 'reattached', newCode: match.code, session: match }
        report.reattached++
      } else {
        perHostRemap[oldCode] = { status: 'failed' }
        report.failed++
      }
    }
  })

  return { remap, report }
}
