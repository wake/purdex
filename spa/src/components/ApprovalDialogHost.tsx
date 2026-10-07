// spa/src/components/ApprovalDialogHost.tsx — the one approval dialog (lead-team spec §6.3, §8.7), mounted once with the
// app-level overlays, next to HandoffDialogHost. It renders the oldest open request across hosts from
// `useApprovalStore`, which the `approval.request` WS branch and the daemon's snapshot feed (P3b); several requests
// queue, one dialog at a time. The body switches on `kind`: `lead` (reason, editable grant) and `self_relay` (the
// session, its usage, what approving does, and 「這個 session 不再詢問」, which pauses that session's asks — P5a).
//
// It cannot be dismissed: no Escape, no backdrop click. A request ends by a decision — 核准 / 拒絕, one click each on any
// Purdex.app (U5b, U13a) — or by the daemon closing it (decided elsewhere, timeout, cancel), which removes it from the
// store and unmounts this. While the host is not connected (spec §9.4) the buttons dim under `daemon 重啟中…`; a click
// then is queued in the store and sent when the reconnect snapshot re-adds the request (P3b). A send that fails on the
// network while the host is still connected is not queued (nothing would resend it): it toasts and the buttons come back.
//
// Focus: the panel takes focus on open and Tab stays inside, as ConfirmDialog does, so a stray keystroke never
// reaches the pane behind; Escape is swallowed so a dialog beneath does not dismiss. The i18n strings are the spec's.
import { useEffect, useRef, useState } from 'react'
import { ArrowsClockwise } from '@phosphor-icons/react'
import { useI18nStore } from '../stores/useI18nStore'
import { useHostStore } from '../stores/useHostStore'
import { useUndoToast } from '../stores/useUndoToast'
import { approvalKey, selectCurrent, selectOpenCount, useApprovalStore, type ApprovalEntry, type Decision } from '../stores/useApprovalStore'
import { hostLabel, useHostLook } from '../lib/host-look'
import { leadPayloadOf, selfRelayPayloadOf, MAX_MAX_MEMBERS, type Grant } from '../lib/team/types'
import { approvalSessionLabel, formatCountdown, formatOriginAddress } from '../lib/team/approval-format'
import { ApprovalApiError, setSelfRelayPause } from '../lib/team/approval-api'
import { submitDecision } from '../lib/team/approval-decide'

const FOCUSABLE_SELECTOR = 'input, button, textarea, [tabindex]:not([tabindex="-1"])'

function tabStops(panel: HTMLElement): HTMLElement[] {
  return Array.from(panel.querySelectorAll<HTMLElement>(FOCUSABLE_SELECTOR))
    .filter((el) => !(el as HTMLButtonElement).disabled && el.tabIndex >= 0)
}

function parseRoots(text: string): string[] {
  return text.split('\n').map((line) => line.trim()).filter((line) => line !== '')
}

const fieldClass = 'rounded-md border border-border-default bg-surface-input px-2 py-1 text-xs text-text-primary disabled:opacity-50'
// `aria-disabled` dims like RestartDaemonButton's counting state: the button still takes the click (it queues).
const buttonBase = 'px-3 py-1 rounded-md text-xs cursor-pointer disabled:opacity-50 disabled:cursor-default aria-disabled:opacity-50 flex items-center gap-1.5'

export function ApprovalDialogHost() {
  const current = useApprovalStore(selectCurrent)
  if (!current) return null
  // Keyed by the request: the next one is a fresh dialog (the payload's defaults, nothing in flight).
  return <OpenApprovalDialog key={approvalKey(current.hostId, current.approval.id)} entry={current} />
}

function OpenApprovalDialog({ entry }: { entry: ApprovalEntry }) {
  const { hostId, approval } = entry
  const t = useI18nStore((s) => s.t)
  const hostName = hostLabel(hostId, useHostLook(hostId))
  const connected = useHostStore((s) => s.runtime[hostId]?.status === 'connected')
  const queued = useApprovalStore((s) => s.queued[approvalKey(hostId, approval.id)])
  const openCount = useApprovalStore(selectOpenCount)
  const isSelfRelay = approval.kind === 'self_relay'
  const payload = leadPayloadOf(approval)
  const relay = selfRelayPayloadOf(approval)
  const [maxMembers, setMaxMembers] = useState(String(payload.max_members))
  const [rootsText, setRootsText] = useState(payload.roots.join('\n'))
  // 「這個 session 不再詢問」 (spec §8.7 (a)): applied with the decision, whichever it is.
  const [noMoreAsking, setNoMoreAsking] = useState(false)
  const [busy, setBusy] = useState(false)
  const [now, setNow] = useState(() => Date.now())
  // Ref, not state: two clicks in one event burst both see `busy === false` before React commits the first setBusy.
  const inFlight = useRef(false)
  const panelRef = useRef<HTMLDivElement>(null)

  useEffect(() => {
    const id = setInterval(() => setNow(Date.now()), 1000)
    return () => clearInterval(id)
  }, [])

  useEffect(() => { panelRef.current?.focus() }, [])

  // Escape is swallowed, not handled: nothing dismisses this dialog, and nothing beneath it may be dismissed either
  // (ConfirmDialog and FloatingPanel both listen for Escape on `document`; a handoff confirm under this modal would
  // otherwise cancel). Capture phase on `window`, not `document`: capture listeners on one target run in registration
  // order, and the dialog beneath registered first — `window` capture runs before every `document` listener regardless.
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key !== 'Escape') return
      e.preventDefault()
      e.stopImmediatePropagation()
    }
    window.addEventListener('keydown', onKey, { capture: true })
    return () => window.removeEventListener('keydown', onKey, { capture: true })
  }, [])

  // Tab stays inside the panel (ConfirmDialog's rule).
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key !== 'Tab' || e.ctrlKey || e.metaKey || e.altKey) return
      const panel = panelRef.current
      if (!panel) return
      e.preventDefault()
      const stops = tabStops(panel)
      if (stops.length === 0) {
        panel.focus()
        return
      }
      const at = stops.indexOf(document.activeElement as HTMLElement)
      const next = at === -1
        ? (e.shiftKey ? stops.length - 1 : 0)
        : (at + (e.shiftKey ? stops.length - 1 : 1)) % stops.length
      stops[next].focus()
    }
    document.addEventListener('keydown', onKey, { capture: true })
    return () => document.removeEventListener('keydown', onKey, { capture: true })
  }, [])

  const members = maxMembers.trim() === '' ? NaN : Number(maxMembers)
  const membersOk = Number.isInteger(members) && members >= 1 && members <= MAX_MAX_MEMBERS
  const roots = parseRoots(rootsText)
  const rootsOk = roots.length > 0
  // A self relay carries no grant (U13a: one click); only the lead kind validates its fields.
  const grantOk = isSelfRelay || (membersOk && rootsOk)
  const locked = busy || queued !== undefined
  const session = approvalSessionLabel(approval.origin)

  const decide = async (decision: Decision) => {
    if (inFlight.current || locked) return
    if (decision === 'approve' && !grantOk) return
    const grant: Grant | undefined = !isSelfRelay && decision === 'approve' ? { max_members: members, roots } : undefined
    if (!connected) {
      // Spec §9.4: kept locally, sent on reconnect (the snapshot re-adds the request, or shows it gone). A ticked
      // 「這個 session 不再詢問」 is queued WITH the decision and sent before it on reconnect (PR #1742 R1): the
      // person asked for it, and dropping it would let the session ask again.
      const pause = isSelfRelay && noMoreAsking ? approval.origin.session_id : undefined
      useApprovalStore.getState().queueDecision(hostId, approval, decision, grant, pause)
      return
    }
    inFlight.current = true
    setBusy(true)
    if (isSelfRelay && noMoreAsking) {
      // Best effort, before the decision: a pause that fails must not swallow the click.
      try {
        await setSelfRelayPause(hostId, approval.origin.session_id, 'off')
      } catch (e: unknown) {
        const code = e instanceof ApprovalApiError ? e.code : 'network'
        useUndoToast.getState().show(t('approval.dialog.pause_failed', { code }))
      }
    }
    const outcome = await submitDecision(hostId, approval, decision, grant)
    // 'closed' and 'decided_elsewhere' unmount this dialog through the store; the other two keep it.
    if (outcome === 'failed' || outcome === 'queued') {
      inFlight.current = false
      setBusy(false)
    }
  }

  const titleId = 'approval-dialog-title'
  return (
    <div
      className="fixed inset-0 z-50 flex items-center justify-center bg-black/50"
      role="dialog"
      aria-modal="true"
      aria-labelledby={titleId}
      data-testid="approval-dialog"
      data-kind={approval.kind}
      // The backdrop also covers the Electron title bar, a window drag region that would otherwise swallow clicks there.
      style={{ WebkitAppRegion: 'no-drag' } as React.CSSProperties}
    >
      <div
        ref={panelRef}
        tabIndex={-1}
        data-testid="approval-panel"
        className="w-[480px] rounded-lg border border-border-default bg-surface-primary shadow-lg outline-none"
      >
        <div className="border-b border-border-subtle px-4 py-3">
          <h3 id={titleId} className="text-sm font-medium text-text-primary">
            {t(isSelfRelay ? 'approval.dialog.title_self_relay' : 'approval.dialog.title_lead', { host: hostName, session })}
          </h3>
          <dl className="mt-2 grid grid-cols-[auto_1fr] gap-x-3 gap-y-1 text-xs">
            <dt className="text-text-muted">{t('approval.dialog.host')}</dt>
            <dd data-testid="approval-host" className="text-text-primary">{hostName}</dd>
            <dt className="text-text-muted">{t('approval.dialog.session')}</dt>
            <dd data-testid="approval-session" className="text-text-primary">{session}</dd>
            <dt className="text-text-muted">{t('approval.dialog.address')}</dt>
            <dd data-testid="approval-address" className="font-mono text-text-primary">{formatOriginAddress(hostName, approval.origin)}</dd>
            {isSelfRelay && (
              <>
                <dt className="text-text-muted">{t('approval.dialog.ref')}</dt>
                <dd data-testid="approval-ref" className="font-mono text-text-primary">{approval.origin.ref}</dd>
              </>
            )}
            <dt className="text-text-muted">{t('approval.dialog.cwd')}</dt>
            <dd data-testid="approval-cwd" className="font-mono break-all text-text-primary">{approval.origin.cwd}</dd>
            {isSelfRelay ? (
              <>
                <dt className="text-text-muted">{t('approval.dialog.usage')}</dt>
                <dd data-testid="approval-usage" className="text-text-primary">{t('approval.dialog.usage_value', { pct: Math.round(relay.used_percentage) })}</dd>
              </>
            ) : (
              <>
                <dt className="text-text-muted">{t('approval.dialog.tmux')}</dt>
                <dd data-testid="approval-tmux" className="font-mono text-text-primary">{approval.origin.tmux !== '' ? approval.origin.tmux : '—'}</dd>
                <dt className="text-text-muted">{t('approval.dialog.reason')}</dt>
                <dd data-testid="approval-reason" className="whitespace-pre-wrap text-text-primary">{payload.reason}</dd>
              </>
            )}
            <dt className="text-text-muted">{t('approval.dialog.deadline')}</dt>
            <dd data-testid="approval-countdown" className="font-mono text-text-primary">{formatCountdown(approval.deadline_at - now)}</dd>
          </dl>
          {isSelfRelay ? (
            <>
              <p data-testid="approval-self-relay-note" className="mt-3 text-xs text-text-secondary">{t('approval.dialog.self_relay_note')}</p>
              <label className="mt-2 flex items-center gap-2 text-xs text-text-secondary">
                <input
                  type="checkbox"
                  checked={noMoreAsking}
                  disabled={locked}
                  onChange={(e) => setNoMoreAsking(e.target.checked)}
                  data-testid="approval-no-more-asking"
                />
                {t('approval.dialog.no_more_asking')}
              </label>
            </>
          ) : (
            <>
              <label className="mt-3 flex items-center gap-2 text-xs text-text-secondary">
                {t('approval.dialog.max_members')}
                <input
                  type="number"
                  min={1}
                  max={MAX_MAX_MEMBERS}
                  step={1}
                  value={maxMembers}
                  disabled={locked}
                  onChange={(e) => setMaxMembers(e.target.value)}
                  data-testid="approval-max-members"
                  className={`w-16 ${fieldClass}`}
                />
              </label>
              {!membersOk && (
                <p data-testid="approval-max-members-error" className="mt-1 text-xs text-status-warning">{t('approval.dialog.max_members_range', { max: MAX_MAX_MEMBERS })}</p>
              )}
              <label className="mt-2 block text-xs text-text-secondary">
                {t('approval.dialog.roots')}
                <textarea
                  rows={3}
                  value={rootsText}
                  disabled={locked}
                  onChange={(e) => setRootsText(e.target.value)}
                  data-testid="approval-roots"
                  className={`mt-1 block w-full font-mono ${fieldClass}`}
                />
              </label>
              {!rootsOk && (
                <p data-testid="approval-roots-error" className="mt-1 text-xs text-status-warning">{t('approval.dialog.roots_required')}</p>
              )}
            </>
          )}
          {!connected && (
            <p data-testid="approval-disconnected" className="mt-2 text-xs text-status-warning">{t('approval.dialog.daemon_restarting')}</p>
          )}
          {queued && (
            <p data-testid="approval-queued" className="mt-1 text-xs text-text-muted">
              {t('approval.dialog.queued', { decision: t(queued.decision === 'approve' ? 'approval.dialog.approve' : 'approval.dialog.deny') })}
            </p>
          )}
          {openCount > 1 && (
            <p data-testid="approval-more" className="mt-2 text-xs text-text-muted">{t('approval.dialog.more_pending', { count: openCount - 1 })}</p>
          )}
        </div>
        <div className="flex justify-end gap-2 px-4 py-3">
          <button
            type="button"
            data-testid="approval-deny"
            disabled={locked}
            aria-disabled={!connected || undefined}
            onClick={() => void decide('deny')}
            className={`${buttonBase} text-text-secondary hover:bg-surface-hover`}
          >
            {t('approval.dialog.deny')}
          </button>
          <button
            type="button"
            data-testid="approval-approve"
            disabled={locked || !grantOk}
            aria-disabled={!connected || undefined}
            onClick={() => void decide('approve')}
            className={`${buttonBase} bg-accent text-white`}
          >
            {busy && <ArrowsClockwise size={12} aria-hidden="true" className="animate-spin" />}
            {t('approval.dialog.approve')}
          </button>
        </div>
      </div>
    </div>
  )
}
