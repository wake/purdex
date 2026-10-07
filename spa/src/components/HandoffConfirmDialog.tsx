// spa/src/components/HandoffConfirmDialog.tsx — the confirm step in front of
// "Hand to nex" (P-C.3 spec §4.4). The handoff exits Claude Code in the pane
// and continues it headless with no permission prompts, so it is never a
// one-click action. The dialog owns only the busy state, the "keep the tmux
// session" choice (G4: checked on every open, never remembered) and the
// toasts; the request, the pane swap and the single-flight live in
// `lib/nex/handoff.ts`; the modal shell is the shared `ConfirmDialog`.
import { useId, useRef, useState } from 'react'
import { useI18nStore } from '../stores/useI18nStore'
import { useUndoToast } from '../stores/useUndoToast'
import { useTabStore } from '../stores/useTabStore'
import { useSessionStore } from '../stores/useSessionStore'
import { useAgentStore } from '../stores/useAgentStore'
import { useNexHostStore, selectPermissionAskReady } from '../stores/useNexHostStore'
import { compositeKey } from '../lib/composite-key'
import { stripAgentTitleMarker, stripAnyKnownAgentTitleMarker } from '../lib/agent-title-marker'
import { countPanesOnSession } from '../lib/pane-tree'
import { HandoffApiError } from '../lib/nex/handoff-api'
import {
  handToNex,
  handoffBlockReasonNow,
  executionContentFor,
  handoffFromFor,
  handoffErrorMessage,
  handoffConfirmBodyKey,
  manualResumeHint,
  type HandToNexArgs,
} from '../lib/nex/handoff'
import { isRefShownNow, landOnHostsPageIfHidden } from '../lib/shown-hosts'
import { ConfirmDialog } from './ConfirmDialog'

interface Props extends Omit<HandToNexArgs, 'keepSession' | 'fromTitle' | 'askApproval'> {
  onClose: () => void
}

export function HandoffConfirmDialog({ onClose, ...args }: Props) {
  const t = useI18nStore((s) => s.t)
  const [busy, setBusy] = useState(false)
  // Plain state, so a fresh mount is a fresh default (user ruling 2026-09-19:
  // 預設保留、每次都問); nothing persists it.
  const [keepSession, setKeepSession] = useState(true)
  // 完全放行（預設）/ 需要核准 (permission channel §5.2, PC1): offered only when the host can run handoff_ask; a fresh
  // default on every open. A 需要核准 the host can no longer run is refused by handToNex, never sent as 完全放行.
  const askReady = useNexHostStore(selectPermissionAskReady(args.hostId))
  const [askApproval, setAskApproval] = useState(false)
  const approvalName = useId()
  const nexCapabilities = useNexHostStore((s) => s.byHost[args.hostId]?.capabilities)
  const otherPanes =useTabStore((s) => countPanesOnSession(s.tabs, args.hostId, args.sessionCode, args.paneId))
  // The session's own pane title, recorded on the execution pane as its
  // pre-handoff title (worker theme spec §8.4), with the agent marker
  // stripped. Looked up by (hostId, sessionCode) directly — not through the
  // tab's displayTitle, which composes a suffix and describes whichever pane
  // is primary — so this is unaffected by the dynamicTabName /
  // stripAgentTitleMarker display settings and applies equally to a
  // secondary-pane handoff on a split.
  const rawPaneTitle = useSessionStore((s) => s.sessions[args.hostId]?.find((sess) => sess.code === args.sessionCode)?.pane_title)
  const agentType = useAgentStore((s) => s.agentTypes[compositeKey(args.hostId, args.sessionCode)])
  // agentType can be unclassified yet (useAgentStore hasn't seen this session
  // classify), in which case the typed, agentType-keyed strip is a no-op and
  // a marker like "✳ fix-login" would be recorded verbatim (review finding
  // A2). Fall back to stripping any known agent's marker shape by pattern
  // alone; the typed path stays authoritative once agentType is known.
  const fromTitle = rawPaneTitle
    ? (agentType ? stripAgentTitleMarker(rawPaneTitle, agentType) : stripAnyKnownAgentTitleMarker(rawPaneTitle))
    : undefined
  // Ref, not state: two clicks in one event burst both see `busy === false`
  // before React commits the first setBusy.
  const inFlight = useRef(false)

  const confirm = async () => {
    if (inFlight.current) return
    // The gate, re-read from the stores at the click (P6 review A1): the host closes this dialog when the gate closes,
    // but only after a render, so a click can land in between. The pane no longer holds this session, its host was
    // hidden in the workbench (H2d-3), it terminated, Claude Code no longer runs in it, or Nex stopped being ready →
    // nothing is handed off; the dialog just closes, silently, as the host's own close does.
    if (handoffBlockReasonNow(args.tabId, args.paneId, args) !== null) {
      onClose()
      return
    }
    inFlight.current = true
    setBusy(true)
    const toast = useUndoToast.getState()
    try {
      const { result, swapped } = await handToNex({ ...args, keepSession, fromTitle, ...(askApproval ? { askApproval: true } : {}) })
      if (swapped) {
        toast.show(t('handoff.success'))
      } else if (!isRefShownNow(args.hostId)) {
        // Hidden during the flight (H2d-3): no "open execution" — it would open a tab on a hidden host.
        toast.show(t('handoff.success'))
      } else {
        const from = handoffFromFor(args, result)
        toast.show(
          t('handoff.success'),
          () => {
            // Re-checked at click (H2d-3): hidden since → the Hosts page on that host, never an execution tab.
            if (landOnHostsPageIfHidden(args.hostId)) return
            // The same view the swap would have written (shell cleanup §9.4): a chat handoff opens in chat here too.
            useTabStore.getState().openSingletonTab(executionContentFor(args.hostId, result.execution_id, from, fromTitle, args.mode))
          },
          t('handoff.open_execution'),
        )
      }
      onClose()
    } catch (err) {
      if (err instanceof HandoffApiError) {
        const id = manualResumeHint(err)
        const message = handoffErrorMessage(t, err)
        toast.show(id ? `${message}\n${t('takeback.manual_resume', { id })}` : message)
      } else {
        toast.show(t('handoff.error.generic', { code: 'unknown' }))
      }
      inFlight.current = false
      setBusy(false)
    }
  }

  return (
    <ConfirmDialog
      testIdPrefix="handoff"
      title={t('handoff.confirm_title')}
      body={t(handoffConfirmBodyKey(nexCapabilities))}
      confirmLabel={t('handoff.menu')}
      busy={busy}
      onCancel={onClose}
      onConfirm={() => { void confirm() }}
    >
      <label className="mt-2 flex items-center gap-2 text-xs text-text-secondary cursor-pointer">
        <input
          type="checkbox"
          data-testid="handoff-keep-session"
          checked={keepSession}
          disabled={busy}
          onChange={(e) => setKeepSession(e.target.checked)}
          className="accent-accent"
        />
        {t('handoff.keep_session')}
      </label>
      {askReady && (
        <fieldset data-testid="handoff-approval" className="mt-2 flex flex-col gap-1 text-xs text-text-secondary">
          <legend className="sr-only">{t('handoff.approval.label')}</legend>
          {([['full', false], ['ask', true]] as const).map(([key, ask]) => (
            <label key={key} className="flex items-center gap-2 cursor-pointer">
              <input
                type="radio"
                name={approvalName}
                data-testid={`handoff-approval-${key}`}
                checked={askApproval === ask}
                disabled={busy}
                onChange={() => setAskApproval(ask)}
                className="accent-accent"
              />
              {t(`handoff.approval.${key}`)}
            </label>
          ))}
          <p data-testid="handoff-approval-hint" className="text-text-muted">{t('handoff.approval.ask_hint')}</p>
        </fieldset>
      )}
      {otherPanes > 0 && (
        <p data-testid="handoff-other-panes" className="mt-1 text-xs text-status-warning">
          {t('handoff.other_panes', { count: otherPanes })}
        </p>
      )}
    </ConfirmDialog>
  )
}
