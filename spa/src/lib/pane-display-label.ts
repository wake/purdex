// spa/src/lib/pane-display-label.ts — one pane's display label, read from the stores now (shell cleanup spec §10).
//
// For dialogs that name several panes at once (the layout-change confirm and keep picker): `getPaneLabel` with the
// session looked up on the pane's own host, and a worker named by its worker title (as its tab is), so two workers in
// one list can be told apart. A snapshot, not a subscription: the dialogs are short-lived and modal.
import { getPaneLabel, type TFunction } from './pane-labels'
import { readWorkerSummary, workerTitleOf } from './nex/worker-summary'
import { resolveExecutionHostId } from './nex/resolve-host'
import { useSessionStore } from '../stores/useSessionStore'
import { useWorkspaceStore } from '../stores/useWorkspaceStore'
import { selectSessionTitleSupported, useNexHostStore } from '../stores/useNexHostStore'
import type { PaneContent } from '../types/tab'

export function paneDisplayLabelNow(content: PaneContent, t: TFunction): string {
  if (content.kind === 'execution') {
    const hostId = resolveExecutionHostId(content.host)
    const supported = selectSessionTitleSupported(hostId)(useNexHostStore.getState())
    const title = workerTitleOf(content, readWorkerSummary(hostId, content.executionId), supported)
    if (title) return title
  }
  // Session codes collide across hosts: look the code up on the pane's host only.
  const sessions = content.kind === 'tmux-session' ? useSessionStore.getState().sessions[content.hostId] ?? [] : []
  const { workspaces } = useWorkspaceStore.getState()
  return getPaneLabel(
    content,
    { getByCode: (code) => sessions.find((s) => s.code === code) },
    { getById: (id) => workspaces.find((w) => w.id === id) },
    t,
  )
}
