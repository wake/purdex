// spa/src/components/hosts/RestartDaemonButton.tsx — the restart entry point
// rendered by the host page (R1), the Development page (R2) and the Nex
// config screen (R3) (daemon restart spec §3.2). Click → count running
// workers → confirm → useDaemonRestartStore.restart. While that host
// restarts, every instance for it shows the spinner and is disabled.
import { useEffect, useRef, useState } from 'react'
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
}

const btnClass = 'px-3 py-1.5 text-xs rounded-md bg-surface-input border border-border-default text-text-primary hover:bg-surface-hover disabled:opacity-50 cursor-pointer disabled:cursor-default inline-flex items-center gap-1'

export function RestartDaemonButton({ hostId, label, testId = 'restart-daemon', className }: Props) {
  const t = useI18nStore((s) => s.t)
  const name = hostLabel(hostId, useHostLook(hostId))
  const restarting = useDaemonRestartStore((s) => s.restarting[hostId] === true)
  const restart = useDaemonRestartStore((s) => s.restart)
  const [counting, setCounting] = useState(false)
  // workers: null = the count is unknown (cautious line); 0 = nothing running (no line).
  const [confirm, setConfirm] = useState<{ workers: number | null } | null>(null)
  const mounted = useRef(true)
  useEffect(() => {
    mounted.current = true
    return () => { mounted.current = false }
  }, [])

  const open = async () => {
    setCounting(true)
    const workers = await countRunningWorkers(hostId)
    if (!mounted.current) return
    setCounting(false)
    setConfirm({ workers })
  }

  return (
    <>
      <button type="button" data-testid={testId} disabled={restarting || counting} onClick={() => void open()} className={className ?? btnClass}>
        {restarting
          ? <><ArrowsClockwise size={12} className="animate-spin" />{t('hosts.restart.restarting')}</>
          : (label ?? t('hosts.restart.button'))}
      </button>
      {confirm && (
        <ConfirmDialog
          testIdPrefix={`${testId}-confirm`}
          title={t('hosts.restart.confirm_title', { host: name })}
          body={t('hosts.restart.confirm_body')}
          confirmLabel={t('hosts.restart.confirm')}
          onCancel={() => setConfirm(null)}
          onConfirm={() => { setConfirm(null); void restart(hostId, name) }}
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
