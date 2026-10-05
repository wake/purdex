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
  const [counting, setCounting] = useState(false)
  const [confirm, setConfirm] = useState<PendingConfirm | null>(null)

  // An open confirm that no longer fits is cleared during render (the "adjust state when a prop changes" idiom,
  // as useNexHostData's prevStatus), not just hidden — a hidden one would reappear once the restart ends or the lock lifts.
  const stale = confirm !== null && (confirm.hostId !== hostId || confirm.gen !== settled || restarting || disabled)
  if (stale) setConfirm(null)

  // The props as of the last commit, for the awaited count and for Confirm (synced before any await resumes).
  const latest = useRef({ hostId, disabled, onActiveChange })
  useLayoutEffect(() => { latest.current = { hostId, disabled, onActiveChange } })
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
    setCounting(true)
    const workers = await countRunningWorkers(target)
    if (!mounted.current) return
    setCounting(false)
    if (stillValid(target, gen)) setConfirm({ hostId: target, gen, workers })
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
        </ConfirmDialog>
      )}
    </>
  )
}
