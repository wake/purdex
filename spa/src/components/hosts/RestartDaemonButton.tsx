// spa/src/components/hosts/RestartDaemonButton.tsx — the restart entry point
// rendered by the host page (R1), the Development page (R2) and the Nex
// config screen (R3) (daemon restart spec §3.2). Click → count running
// workers → confirm → useDaemonRestartStore.restart. While that host
// restarts, every instance for it shows the spinner and is disabled.
//
// The count and the confirm dialog belong to the situation they were made
// for: the same host, no restart of it started or finished since the click
// (another entry point may restart it), and the caller not locked. Any change
// drops a pending count, closes an open dialog, and Confirm re-checks it all.
import { useEffect, useLayoutEffect, useRef, useState } from 'react'
import { ArrowsClockwise } from '@phosphor-icons/react'
import { useI18nStore } from '../../stores/useI18nStore'
import { useDaemonRestartStore } from '../../stores/useDaemonRestartStore'
import { selectOpenCountFor, useApprovalStore } from '../../stores/useApprovalStore'
import { fetchInflight } from '../../lib/team/approval-api'
import { countRunningWorkers } from '../../lib/daemon-restart'
import { hostLabel, useHostLook } from '../../lib/host-look'
import { ConfirmDialog } from '../ConfirmDialog'

interface Props {
  hostId: string
  label?: string
  testId?: string
  className?: string
  /** Caller-imposed lock (e.g. its section is busy); the restarting/counting states are unchanged. Locking cancels a pending count or an open confirm. */
  disabled?: boolean
  /** True once a count starts; false once neither a count nor a confirm is pending (cancelled, confirmed, dropped as stale, or unmounted). */
  onActiveChange?: (active: boolean) => void
}

/** The open confirm: its host, that host's `settled` generation at the click, and the count. */
interface PendingConfirm {
  hostId: string
  gen: number
  // null = the count is unknown (cautious line); 0 = nothing running (no line).
  workers: number | null
  // GET /api/team/inflight's approvals_open (lead-team spec §9.5); null = the call failed or timed out, and the
  // line falls back to the store's count for this host.
  approvals: number | null
}

const btnClass = 'px-3 py-1.5 text-xs rounded-md bg-surface-input border border-border-default text-text-primary hover:bg-surface-hover disabled:opacity-50 cursor-pointer disabled:cursor-default'
// Appended to whatever class is in effect, so a caller's className keeps the spinner layout and the counting dim.
const dimClass = 'inline-flex items-center gap-1 aria-disabled:opacity-50 aria-disabled:cursor-default'

export function RestartDaemonButton({ hostId, label, testId = 'restart-daemon', className, disabled = false, onActiveChange }: Props) {
  const t = useI18nStore((s) => s.t)
  const name = hostLabel(hostId, useHostLook(hostId))
  const restarting = useDaemonRestartStore((s) => s.restarting[hostId] === true)
  const settled = useDaemonRestartStore((s) => s.settled[hostId] ?? 0)
  const restart = useDaemonRestartStore((s) => s.restart)
  // Fallback for the inflight count (lead-team spec §9.5): the open requests the WS snapshot keeps for THIS host.
  const storeOpenApprovals = useApprovalStore(selectOpenCountFor(hostId))
  // The host and generation the pending count was started for; null = no count in flight.
  const [countingFor, setCountingFor] = useState<{ hostId: string; gen: number } | null>(null)
  const counting = countingFor !== null
  const [confirm, setConfirm] = useState<PendingConfirm | null>(null)

  // An open confirm that no longer fits is cleared during render (the "adjust state when a prop changes" idiom,
  // as useNexHostData's prevStatus), not just hidden — a hidden one would reappear once the restart ends or the lock lifts.
  const stale = confirm !== null && (confirm.hostId !== hostId || confirm.gen !== settled || restarting || disabled)
  if (stale) setConfirm(null)
  // Likewise a pending count: dropped at once (not when its request settles), so the button and the caller's lock free up now.
  const staleCount = countingFor !== null && (countingFor.hostId !== hostId || countingFor.gen !== settled || restarting || disabled)
  if (staleCount) setCountingFor(null)

  // The props as of the last commit, for the awaited count and for Confirm (synced before any await resumes).
  const latest = useRef({ hostId, disabled, onActiveChange })
  useLayoutEffect(() => { latest.current = { hostId, disabled, onActiveChange } })
  // Request token: a count applies its result only if no invalidation happened since its click (also h1 -> h2 -> h1).
  // Bumped in a layout effect (not during render, which react-hooks/refs forbids), i.e. at commit, before any awaited count resumes.
  const requestRef = useRef(0)
  useLayoutEffect(() => { requestRef.current++ }, [hostId, settled, restarting, disabled])
  const mounted = useRef(true)
  useEffect(() => {
    mounted.current = true
    return () => { mounted.current = false }
  }, [])

  /** The flow started for `target` at generation `gen` still holds: same host, no restart started or finished since, caller not locked. */
  const stillValid = (target: string, gen: number) => {
    const s = useDaemonRestartStore.getState()
    return target === latest.current.hostId && !latest.current.disabled && !s.restarting[target] && (s.settled[target] ?? 0) === gen
  }

  const open = async () => {
    const target = hostId
    if (counting || disabled || useDaemonRestartStore.getState().restarting[target]) return
    const gen = useDaemonRestartStore.getState().settled[target] ?? 0
    const mine = ++requestRef.current
    setCountingFor({ hostId: target, gen })
    // Both inside the dialog's 3 s budget: countRunningWorkers races its own timer, fetchInflight aborts its own
    // request (approval-api.ts INFLIGHT_TIMEOUT_MS). Any inflight failure is null → the store's count below.
    const [workers, approvals] = await Promise.all([
      countRunningWorkers(target),
      fetchInflight(target).then((r) => r.approvals_open, () => null),
    ])
    // Invalidated meanwhile: a newer state owns `countingFor`, touch nothing.
    if (!mounted.current || requestRef.current !== mine) return
    setCountingFor(null)
    if (stillValid(target, gen)) setConfirm({ hostId: target, gen, workers, approvals })
  }

  const onConfirm = () => {
    const c = confirm
    setConfirm(null)
    if (c && stillValid(c.hostId, c.gen)) void restart(c.hostId, name)
  }

  // Tell the caller while a flow is active, so it can lock its other controls. The callback that heard `true` hears `false`.
  const active = counting || confirm !== null
  useEffect(() => {
    if (!active) return
    const report = latest.current.onActiveChange
    report?.(true)
    return () => report?.(false)
  }, [active])

  // Open approval requests on THIS host (lead-team spec §9.5): they survive the restart (leases are extended at
  // boot), so the line informs, it does not block. The daemon's answer first; the store when it could not be asked.
  const openApprovals = confirm === null ? 0 : (confirm.approvals ?? storeOpenApprovals)

  return (
    <>
      <button type="button" data-testid={testId} disabled={restarting || disabled} aria-disabled={counting || undefined} aria-busy={counting || undefined} onClick={() => void open()} className={`${className ?? btnClass} ${dimClass}`}>
        {restarting
          ? <><ArrowsClockwise size={12} aria-hidden="true" className="animate-spin" />{t('hosts.restart.restarting')}</>
          : (label ?? t('hosts.restart.button'))}
      </button>
      {confirm && !stale && (
        <ConfirmDialog
          testIdPrefix={`${testId}-confirm`}
          title={t('hosts.restart.confirm_title', { host: name })}
          body={t('hosts.restart.confirm_body')}
          confirmLabel={t('hosts.restart.confirm')}
          onCancel={() => setConfirm(null)}
          onConfirm={onConfirm}
        >
          {confirm.workers !== 0 && (
            <p data-testid={`${testId}-workers`} className="mt-2 text-xs text-amber-400">
              {confirm.workers === null
                ? t('hosts.restart.confirm_workers_unknown')
                : t('hosts.restart.confirm_workers', { count: confirm.workers })}
            </p>
          )}
          {openApprovals > 0 && (
            <p data-testid={`${testId}-approvals`} className="mt-1 text-xs text-amber-400">
              {t('approval.restart.pending', { count: openApprovals })}
            </p>
          )}
        </ConfirmDialog>
      )}
    </>
  )
}
