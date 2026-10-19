// spa/src/components/watch/jobs/JobMenu.tsx — the 「⋯」 menu of a job's detail (plan "WWD-7 design", Jobs tab): 最前／最後／暫緩／恢復 for a queued job (only for an origin the App has its own daemon for) and 取消. It exists only where a daemon announces
// `jobs.control.v1` and the connection may change things (a device token answers 403: the menu then goes away and the notice says why). Cancelling a job that is going asks first; a queued one is cancelled at once (user Q5).
// What a change did is not read back here: the job store's next read (`jobs.changed`) decides what the job now is; a refusal is told to the detail through `onNotice`, with a retry where one can help.
import { useRef, useState } from 'react'
import { DotsThree } from '@phosphor-icons/react'
import { Menu, type MenuEntry } from '../../Menu'
import { useI18nStore } from '../../../stores/useI18nStore'
import { PoolChangeError, controlDenied } from '../../../lib/pool/control-api'
import { cancelJob, setJobPosition } from '../../../lib/pool/jobs-control-api'
import { menuActions, useJobControl, type MenuAction } from '../../../lib/pool/job-control'
import { candidates } from '../../../lib/pool/jobs-driver'
import { liveTopology } from '../../../lib/pool/jobs-driver-live'
import { useJobsStore } from '../../../lib/pool/jobs-store'
import { errorTextKey } from '../../../lib/pool/words'
import type { PoolJob } from '../../../lib/pool/jobs-types'

/** What the detail says under its header after a change that did not go through. */
export interface MenuNotice { text: string; retry?: () => void }

type T = (key: string, opts?: Record<string, string | number>) => string

/** A refusal in words: wwd's own reason for its 4xx (a job that has ended answers 409), the table's lines for the rest. A 400 is the App's own mistake (it sends nothing it should not): logged, and said plainly. */
function noticeOf(e: unknown, t: T, origin: string, retry: () => void): MenuNotice {
  if (!(e instanceof PoolChangeError)) return { text: t('watch.jobs.menu.failed'), retry }
  const { code, action } = e.poolError
  if (code === 'wwd_rejected') return { text: e.status === 409 ? t('watch.jobs.menu.ended') : (e.detail !== '' ? e.detail : t('watch.jobs.menu.rejected')) }
  if (action === 'read_only') return { text: t('pool.error.forbidden') }
  if (action === 'revert_retry') return { text: e.detail !== '' ? t('watch.jobs.menu.no_answer', { host: origin, reason: e.detail }) : t('pool.error.host_failing', { host: origin }), retry }
  if (code === 'busy') return { text: t('pool.error.busy'), retry }
  console.error('job change refused', e.message)
  const key = errorTextKey(code)
  return { text: key ? t(key, { host: origin }) : t('watch.jobs.menu.failed') }
}

const LABEL: Record<string, string> = { front: 'watch.jobs.menu.front', back: 'watch.jobs.menu.back', hold: 'watch.jobs.menu.hold', normal: 'watch.jobs.menu.normal' }

export function JobMenu({ job, onNotice }: { job: PoolJob; onNotice: (n: MenuNotice | null) => void }) {
  const t = useI18nStore((s) => s.t)
  const ctl = useJobControl(job.origin)
  const triggerRef = useRef<HTMLButtonElement>(null)
  const [open, setOpen] = useState(false)
  const [confirming, setConfirming] = useState(false)
  const [busy, setBusy] = useState(false)
  // A device token was refused (403): the daemon is remembered (control-api), so the menu is gone for it until the App starts again.
  const [, setDeniedSeen] = useState(0)
  const via = (a: MenuAction) => (a.kind === 'cancel' ? ctl.cancelVia : ctl.positionVia)
  const actions = menuActions(job, ctl).filter((a) => { const v = via(a); return v !== null && !controlDenied(v) })
  if (actions.length === 0) return null

  const run = async (a: MenuAction): Promise<void> => {
    const to = via(a)
    if (to === null) return
    setOpen(false); setConfirming(false); onNotice(null); setBusy(true)
    try {
      if (a.kind === 'cancel') await cancelJob(to, job.origin, job.id)
      else await setJobPosition(to, job.origin, job.id, a.position)
      // the job as it is now: the event would bring it too, this is only sooner
      const source = candidates(job.origin, liveTopology())[0]
      if (source) void useJobsStore.getState().readJob(job.origin, source, job.id)
    } catch (e) {
      if (e instanceof PoolChangeError && e.poolError.action === 'read_only') setDeniedSeen((n) => n + 1)
      onNotice(noticeOf(e, t, job.origin, () => { void run(a) }))
    } finally {
      setBusy(false)
    }
  }

  const pick = (a: MenuAction) => {
    if (a.kind === 'cancel' && a.confirm) { setConfirming(true); return }
    setOpen(false)
    void run(a)
  }

  const cancel = actions.find((a) => a.kind === 'cancel')
  const items: MenuEntry[] = confirming && cancel
    ? [
      { id: 'ask', label: t('watch.jobs.menu.confirm_title'), disabled: true, onSelect: () => {} },
      { divider: true },
      { id: 'yes', label: t('watch.jobs.menu.confirm_yes'), testId: 'job-menu-confirm-yes', onSelect: () => { void run(cancel) } },
      { id: 'no', label: t('watch.jobs.menu.confirm_no'), testId: 'job-menu-confirm-no', onSelect: () => { setConfirming(false); setOpen(false) } },
    ]
    : actions.flatMap((a): MenuEntry[] => a.kind === 'position'
      ? [{ id: a.position, label: t(LABEL[a.position]), testId: `job-menu-${a.position}`, onSelect: () => pick(a) }]
      // asking first keeps the menu open: the question is the same menu's next page
      : [{ divider: true }, { id: 'cancel', label: t('watch.jobs.menu.cancel'), testId: 'job-menu-cancel', keepOpen: a.confirm, onSelect: () => pick(a) }])
      // a divider first (no place items offered) is no divider
      .filter((e, i) => !('divider' in e) || i > 0)

  return (
    <>
      <button
        ref={triggerRef} type="button" data-testid="job-menu-trigger" aria-haspopup="menu" aria-expanded={open} aria-label={t('watch.jobs.menu.label')} disabled={busy}
        onClick={() => { setConfirming(false); setOpen((o) => !o) }}
        className="shrink-0 rounded px-1 text-text-muted hover:text-text-primary disabled:opacity-40"
      >
        <DotsThree size={16} weight="bold" />
      </button>
      <Menu trigger={triggerRef} open={open} onClose={() => { setOpen(false); setConfirming(false) }} items={items} label={t('watch.jobs.menu.label')} testId="job-menu" />
    </>
  )
}
