// spa/src/components/execution/WorkerDeckPane.tsx — a worker pane in its 指揮台 (deck) view (U4-1b): the execution's own Claude Code conversation
// drawn by the deck, the worker's light as its status (the daemon's `header.backend = "execution"`), and the worker's own input as the footer.
// The worker is subscribed, leased and answered through `useWorkerSession` — the same hook the room / chat view calls, one of the two mounted
// at a time — so the send path, the draft memory and the lease are the worker's, not a terminal's. A worker has no terminal view: the deck
// gets no `onSwitchToTerminal`. Before the session id exists (queued, starting) the lifecycle says so instead of an empty deck.
import { useEffect, useRef, useState } from 'react'
import { CaretDown } from '@phosphor-icons/react'
import { useI18nStore } from '../../stores/useI18nStore'
import { useWorkerSession } from '../../hooks/useWorkerSession'
import { useExecutionActions } from '../../hooks/useExecutionActions'
import { useExecutionConversation } from '../../hooks/useExecutionConversation'
import { retireStaleSessions } from '../../lib/conversations/pane-release'
import { readWorkerDraft } from '../../lib/nex/worker-draft-memory'
import { sendErrorText } from '../../lib/nex/send-error-text'
import { useExecutionStore } from '../../stores/useExecutionStore'
import { DeckPane } from '../deck/DeckPane'
import type { DeckFooterContext } from '../deck/footer-context'
import { StatusRow } from '../deck/StatusRow'
import WorkerInput from '../room/WorkerInput'
import ViewModeMenu, { ViewModeLabel } from './ViewModeMenu'
import { WorkerEndedPane, workerEndedKind } from './WorkerEndedPane'
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
  const { key, st, problem, lease, shownPermission, draftKey, onTextChange } = useWorkerSession(hostId, executionId, isActive)
  const { draft, handleSend, handleInterrupt } = useExecutionActions(hostId, executionId, lease)
  const conversation = useExecutionConversation(hostId, executionId)
  // The pane now reads another session (the first turn reports it, a resume moves it): what it kept for the old one is let go (#2457).
  const readSession = conversation.state === 'ready' ? conversation.sessionId : ''
  useEffect(() => { retireStaleSessions(paneId, hostId, readSession) }, [paneId, hostId, readSession])
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

  const streamDead = st.historyLoaded && st.sse === 'closed' && !!st.sseError
  const inputDisabled = st.sendLocked || !st.historyLoaded || streamDead
  const footer = (ctx: DeckFooterContext) => (
    <div data-testid="worker-deck-footer">
      {/* The request card is the room's (U4-3 brings it here): until then the worker is waiting on something this view cannot show, and the lease is held for it */}
      {st.historyLoaded && shownPermission && (
        <div data-testid="worker-deck-permission" role="status" className="mx-2 mb-1 flex items-center gap-2 text-xs text-status-warning-text">
          <span className="min-w-0 flex-1">{t('worker.deck.permission_waiting')}</span>
          <button type="button" data-testid="worker-deck-to-room" onClick={() => onModeChange('room')}
            className="shrink-0 cursor-pointer rounded border border-border-subtle px-2 py-0.5 text-text-primary hover:bg-surface-hover">{t('room.view.room')}</button>
        </div>
      )}
      {st.leaseError?.code === 'lease_held' && (
        <div data-testid="lease-held" className="mx-2 mb-1 text-xs text-status-warning-text">{t('execution.lease_held', { principal: st.leaseError.heldBy ?? '' })}</div>
      )}
      {st.sendError && <div data-testid="send-error" className="mx-2 mb-1 text-xs text-status-error">{sendErrorText(t, st.sendError)}</div>}
      <WorkerInput key={draft ?? ''} initialValue={draft ?? readWorkerDraft(draftKey)}
        // a send refused while another is still on its way keeps the typed text in the box (false), as the room's does
        onSend={(text) => {
          if (useExecutionStore.getState().executions[key]?.sendLocked) return false
          void handleSend(text, { draftText: text })
        }}
        disabled={inputDisabled} pendingSend={st.sendLocked} placeholder={streamDead ? t('execution.input.disconnected') : undefined}
        isActive={isActive} isFocusTarget={isFocusTarget} onTextChange={onTextChange}
        turnLive={st.turnLive} onInterrupt={() => void handleInterrupt()} />
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

  return <DeckPane paneId={paneId} conversation={conversation} isActive={isActive} isFocusTarget={isFocusTarget} footer={footer} />
}
