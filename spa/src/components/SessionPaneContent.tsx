import { useEffect } from 'react'
import TerminalView from './TerminalView'
import { TerminatedPane } from './TerminatedPane'
import { MissingHostPane } from './MissingHostPane'
import { useTabStore } from '../stores/useTabStore'
import { useWorkspaceStore } from '../features/workspace/store'
import { fetchWsTicket } from '../lib/host-api'
import { useHostStore } from '../stores/useHostStore'
import { findPane } from '../lib/pane-tree'
import { probeSessionCwd } from '../lib/rebuild/cwd-probe'
import { probeSessionProvenance } from '../lib/rebuild/provenance-probe'
import type { PaneRendererProps } from '../lib/module-registry'

export function SessionPaneContent({ pane, isActive }: PaneRendererProps) {
  const content = pane.content
  const sessionCode = content.kind === 'tmux-session' ? content.sessionCode : ''
  const hostId = content.kind === 'tmux-session' ? content.hostId : ''
  const tmuxInstance = content.kind === 'tmux-session' ? content.tmuxInstance : ''
  const terminated = content.kind === 'tmux-session' ? content.terminated : undefined

  const wsBase = useHostStore((s) => s.getWsBase(hostId))
  // A reference this device cannot resolve (host ownership spec §3.2) is kept
  // verbatim and rendered as missing. It must be checked before anything that
  // reaches the network: `getWsBase` falls back to the active host for an
  // unknown id, so attaching would open a terminal on the WRONG host.
  // `Object.hasOwn`, not a lookup: `toString` / `__proto__` & co. are not hosts.
  const hostKnown = useHostStore((s) => hostId !== '' && Object.hasOwn(s.hosts, hostId))

  // Second of the two cwd-probe triggers (spec §4.4): a pane opened after the
  // session list has settled gets no further `sessions` broadcast, so it would
  // otherwise never learn its own directory. Once per binding — the probe
  // itself deduplicates against the sessions-branch sweep.
  //
  // Gated on the same attach gate as the terminal WS (spec §4.6.2). The other
  // trigger fires from inside the reconciliation that opens the gate, so only
  // this one can run against a connection that has not yet proved which
  // generation owns the pane's code. Subscribed rather than merely read, so
  // the probe fires when the gate opens under an already-mounted pane.
  const attachGateOpen = useHostStore((s) => (hostId ? s.runtime[hostId]?.attachReady === true : true))
  useEffect(() => {
    if (terminated) return
    if (!hostKnown) return
    if (!attachGateOpen) return
    probeSessionCwd(hostId, sessionCode, tmuxInstance)
    // The third provenance trigger (spec §5.4), under the same gate and the
    // same binding: a pane opened after the list settled gets no sweep, and a
    // session whose agent is silent sends no hook, so without this one nothing
    // would ever ask on its behalf. The probe itself decides whether this
    // binding still wants an answer.
    probeSessionProvenance(hostId, sessionCode, tmuxInstance)
  }, [hostId, sessionCode, tmuxInstance, terminated, hostKnown, attachGateOpen])

  // Look up tabId from store (pane renderers don't receive tabId as a prop)
  const tabId = useTabStore((s) => {
    for (const id of Object.keys(s.tabs)) {
      if (findPane(s.tabs[id].layout, pane.id)) return id
    }
    return ''
  })

  // Link-source workspace for terminal link openers (PR-5): use the workspace
  // that owns this tab, not active workspace. `undefined` for standalone tabs.
  const workspaceId = useWorkspaceStore((s) =>
    tabId ? s.findWorkspaceByTab(tabId)?.id : undefined,
  ) ?? undefined

  if (content.kind === 'tmux-session' && content.terminated) {
    return <TerminatedPane content={content} tabId={tabId} paneId={pane.id} />
  }

  if (content.kind !== 'tmux-session') return null

  if (!hostKnown) return <MissingHostPane hostId={hostId} />

  // Terminal is the only view a tmux-session pane has (P-D.3 tore down
  // Stream mode), so the key no longer carries a mode suffix: nothing about
  // the pane can change that would call for a remount here.
  return (
    <TerminalView
      key={pane.id}
      wsUrl={`${wsBase}/ws/terminal/${encodeURIComponent(sessionCode)}`}
      visible={isActive}
      hostId={hostId}
      sessionCode={sessionCode}
      workspaceId={workspaceId}
      getTicket={() => fetchWsTicket(hostId)}
    />
  )
}
