// spa/src/components/HandoffConfirmDialog.tsx — the confirm step in front of
// "Hand to nex" (P-C.3 spec §4.4). The handoff exits Claude Code in the pane
// and continues it headless with no permission prompts, so it is never a
// one-click action. The dialog owns only the busy state, the "keep the tmux
// session" choice (G4: checked on every open, never remembered) and the
// toasts; the request, the pane swap and the single-flight live in
// `lib/nex/handoff.ts`; the modal shell is the shared `ConfirmDialog`.
import { useRef, useState } from 'react'
import { useI18nStore } from '../stores/useI18nStore'
import { useUndoToast } from '../stores/useUndoToast'
import { useTabStore } from '../stores/useTabStore'
import { useSessionStore } from '../stores/useSessionStore'
import { useAgentStore } from '../stores/useAgentStore'
import { compositeKey } from '../lib/composite-key'
import { stripAgentTitleMarker } from '../lib/agent-title-marker'
import { countPanesOnSession } from '../lib/pane-tree'
import { HandoffApiError } from '../lib/nex/handoff-api'
import {
  handToNex,
  executionContentFor,
  handoffFromFor,
  handoffErrorMessage,
  manualResumeHint,
  type HandToNexArgs,
} from '../lib/nex/handoff'
import { isRefShownNow, landOnHostsPageIfHidden } from '../lib/shown-hosts'
import { ConfirmDialog } from './ConfirmDialog'

interface Props extends Omit<HandToNexArgs, 'keepSession' | 'fromTitle'> {
  onClose: () => void
}

export function HandoffConfirmDialog({ onClose, ...args }: Props) {
  const t = useI18nStore((s) => s.t)
  const [busy, setBusy] = useState(false)
  // Plain state, so a fresh mount is a fresh default (user ruling 2026-09-19:
  // 預設保留、每次都問); nothing persists it.
  const [keepSession, setKeepSession] = useState(true)
  const otherPanes = useTabStore((s) => countPanesOnSession(s.tabs, args.hostId, args.sessionCode, args.paneId))
  // The session's own pane title, recorded on the execution pane as its
  // pre-handoff title (worker theme spec §8.4), with the agent marker
  // stripped. Looked up by (hostId, sessionCode) directly — not through the
  // tab's displayTitle, which composes a suffix and describes whichever pane
  // is primary — so this is unaffected by the dynamicTabName /
  // stripAgentTitleMarker display settings and applies equally to a
  // secondary-pane handoff on a split.
  const rawPaneTitle = useSessionStore((s) => s.sessions[args.hostId]?.find((sess) => sess.code === args.sessionCode)?.pane_title)
  const agentType = useAgentStore((s) => s.agentTypes[compositeKey(args.hostId, args.sessionCode)])
  const fromTitle = rawPaneTitle ? stripAgentTitleMarker(rawPaneTitle, agentType) : undefined
  // Ref, not state: two clicks in one event burst both see `busy === false`
  // before React commits the first setBusy.
  const inFlight = useRef(false)

  const confirm = async () => {
    if (inFlight.current) return
    // Host ownership H2d-3: the host was hidden in the workbench while the dialog was open → nothing is handed off.
    if (!isRefShownNow(args.hostId)) {
      onClose()
      return
    }
    inFlight.current = true
    setBusy(true)
    const toast = useUndoToast.getState()
    try {
      const { result, swapped } = await handToNex({ ...args, keepSession, fromTitle })
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
            useTabStore.getState().openSingletonTab(executionContentFor(args.hostId, result.execution_id, from, fromTitle))
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
      body={t('handoff.confirm_body')}
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
      {otherPanes > 0 && (
        <p data-testid="handoff-other-panes" className="mt-1 text-xs text-status-warning">
          {t('handoff.other_panes', { count: otherPanes })}
        </p>
      )}
    </ConfirmDialog>
  )
}
