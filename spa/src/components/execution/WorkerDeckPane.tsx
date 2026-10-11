// spa/src/components/execution/WorkerDeckPane.tsx — a worker pane in its 指揮台 (deck) view (U4-1b, U4-2): the execution's own Claude Code conversation
// drawn by the deck, the worker's light as its status (the daemon's `header.backend = "execution"`), and the deck's own input as the footer, running on
// the worker's adapter (`worker-adapter`: Nexen send / interrupt / upload under the pane's lease, the worker's own draft memory).
// The worker is subscribed, leased and answered through `useWorkerSession` — the same hook the room / chat view calls, one of the two mounted
// at a time. A worker has no terminal view: the deck gets no `onSwitchToTerminal`. Before the session id exists (queued, starting) the lifecycle
// says so instead of an empty deck. A permission request the worker waits on is a card in the dock (U4-3): above the input, where a question's card
// is, answered through Nexen's permission channel under the pane's lease (`usePermissionAnswer`, the room's path).
import { useEffect, useMemo, useRef, useState } from 'react'
import { CaretDown } from '@phosphor-icons/react'
import { useI18nStore } from '../../stores/useI18nStore'
import { useWorkerSession } from '../../hooks/useWorkerSession'
import { useExecutionConversation } from '../../hooks/useExecutionConversation'
import { useSendQueueDriver } from '../../hooks/useSendQueueDriver'
import { bindAdapter } from '../../lib/conversations/deck-adapter'
import { draftKey } from '../../lib/conversations/draft-memory'
import { retireStaleSessions } from '../../lib/conversations/pane-release'
import { registerWorkerLease, workerAdapter } from '../../lib/conversations/worker-adapter'
import { runningToolName, workerBackground } from '../../lib/nex/worker-live'
import { DeckPane } from '../deck/DeckPane'
import type { DeckFooterContext } from '../deck/footer-context'
import { SessionInput } from '../deck/SessionInput'
import { StatusRow } from '../deck/StatusRow'
import ViewModeMenu, { ViewModeLabel } from './ViewModeMenu'
import { WorkerEndedPane, workerEndedKind } from './WorkerEndedPane'
import PermissionRequestCard, { PermissionExpiredNotice } from './PermissionRequestCard'
import { permissionCardKey } from '../../lib/nex/permission-card-memory'
import type { ExecutionViewMode } from '../../types/tab'

export interface WorkerDeckPaneProps {
  hostId: string
  executionId: string
  isActive: boolean
  isFocusTarget?: boolean
  tabId: string
  paneId: string
  /** Writes the chosen view back to the pane content (ExecutionPaneWrapper). */
  onModeChange: (mode: ExecutionViewMode) => void
}

export default function WorkerDeckPane({ hostId, executionId, isActive, isFocusTarget = false, tabId, paneId, onModeChange }: WorkerDeckPaneProps) {
  const t = useI18nStore((s) => s.t)
  const { st, problem, lease, permission, shownPermission } = useWorkerSession(hostId, executionId, isActive)
  const conversation = useExecutionConversation(hostId, executionId)
  // The pane now reads another session (the first turn reports it, a resume moves it): what it kept for the old one is let go (#2457).
  const readSession = conversation.state === 'ready' ? conversation.sessionId : ''
  useEffect(() => { retireStaleSessions(paneId, hostId, readSession) }, [paneId, hostId, readSession])

  // The input of this pane runs on the worker's adapter: bound to the input's key BEFORE anything reads the queue or the draft for it (this
  // render's driver, the stream's pending tail, the input itself). Binding is idempotent.
  const adapter = useMemo(() => workerAdapter(hostId, executionId), [hostId, executionId])
  if (readSession) bindAdapter(draftKey(paneId, hostId, readSession), adapter)
  // The adapter's port finds the lease through the registry at call time, so a queue that outlives this view never holds a stale one.
  const leaseRef = useRef(lease)
  useEffect(() => { leaseRef.current = lease })
  useEffect(() => registerWorkerLease(hostId, executionId, {
    ensureLease: () => leaseRef.current.ensureLease(),
    forget: () => leaseRef.current.forget(),
  }), [hostId, executionId])
  // The transcript's user rows settle the messages the queue is showing (a worker's reply rows come back as the same text).
  const readyDoc = conversation.state === 'ready' ? conversation.entry?.doc : undefined
  const readyItems = useMemo(() => (readyDoc ? readyDoc.turns.flatMap((turn) => turn.items) : []), [readyDoc])
  useSendQueueDriver(paneId, hostId, readSession, readyDoc?.header?.status === 'idle', readyItems)

  // The live line speaks from the worker's own events: the tool that runs and the background work (U4-4a), fresher than the transcript's steps.
  const live = useMemo(() => ({ tool: runningToolName(st.tools), background: workerBackground(st.tasks) }), [st.tools, st.tasks])

  const [menuOpen, setMenuOpen] = useState(false)
  const menuAnchor = useRef<HTMLButtonElement>(null)

  if (problem) {
    const text = problem === 'not_found' ? t('execution.not_found')
      : problem === 'host_removed' ? t('execution.host_removed')
      : problem === 'nex_disabled' ? t('execution.nex_disabled')
      : t('execution.nex_unavailable', { error: st.sseError ?? '' })
    return <div data-testid="execution-problem" className="flex h-full items-center justify-center text-sm text-text-muted">{text}</div>
  }
  // An exited / failed-to-start worker has its own screen, in every view (conversation entity spec §6, §7).
  if (st.summary && workerEndedKind(st.summary)) {
    return <WorkerEndedPane hostId={hostId} executionId={executionId} summary={st.summary} tabId={tabId} paneId={paneId} />
  }
  // No session id yet: the lifecycle, in the words the executions list already uses.
  if (conversation.state === 'resolving') {
    const text = !st.summary ? t('execution.loading') : st.summary.state === 'queued' ? t('executions.activity.queued') : t('executions.activity.starting')
    return <div data-testid="worker-deck-placeholder" className="flex h-full items-center justify-center text-sm text-text-muted">{text}</div>
  }

  const permissionShown = st.historyLoaded && shownPermission !== undefined
  const footer = (ctx: DeckFooterContext) => (
    <div data-testid="worker-deck-footer">
      {/* The request the worker waits on, in the dock's place (above the input, as a question's card is); one card at a time, the earliest first. An
          answer from here or from another client closes it; its note, open note field and expanded preview outlive a tab switch (permission-card-memory). */}
      {permissionShown ? (
        <div data-testid="permission-dock" className="border-t border-border-subtle pt-2">
          <PermissionRequestCard key={shownPermission.requestId} request={shownPermission}
            memoryKey={permissionCardKey(hostId, executionId, shownPermission.requestId)}
            agentLabel={shownPermission.agentId ? (st.tasks[shownPermission.agentId]?.description || shownPermission.agentId) : undefined}
            disabled={permission.busy}
            error={permission.error?.requestId === shownPermission.requestId ? permission.error : undefined}
            onAllow={() => { void permission.answer(shownPermission, 'allow') }}
            onDeny={(note) => { void permission.answer(shownPermission, 'deny', note) }} />
        </div>
      ) : st.historyLoaded && st.expiredNotice && <PermissionExpiredNotice timeoutS={st.expiredNotice.timeoutS} />}
      {st.leaseError?.code === 'lease_held' && (
        <div data-testid="lease-held" className="mx-2 mb-1 text-xs text-status-warning-text">{t('execution.lease_held', { principal: st.leaseError.heldBy ?? '' })}</div>
      )}
      <SessionInput {...ctx} asking={ctx.asking || permissionShown} pendingInStream />
      <div className="flex items-center">
        <div className="min-w-0 flex-1">
          <StatusRow cwd={st.summary?.cwd} model={ctx.usage?.model ?? null} effort={ctx.usage?.effort ?? null} cost={st.summary?.cost_usd ?? null} />
        </div>
        {/* a narrow window hides the status bar's mode buttons: the way back to the room is here too */}
        <button ref={menuAnchor} type="button" data-testid="worker-deck-view-menu" aria-haspopup="menu" aria-expanded={menuOpen}
          onClick={() => setMenuOpen((o) => !o)}
          className="mr-2 flex shrink-0 cursor-pointer items-center gap-1 rounded px-1.5 text-xs text-text-secondary hover:bg-surface-hover hover:text-text-primary">
          <ViewModeLabel mode="deck" /> <CaretDown size={10} />
        </button>
        {menuOpen && <ViewModeMenu anchorRef={menuAnchor} mode="deck" onModeChange={onModeChange} onClose={() => setMenuOpen(false)} />}
      </div>
    </div>
  )

  return <DeckPane paneId={paneId} conversation={conversation} isActive={isActive} isFocusTarget={isFocusTarget} footer={footer} live={live} />
}
