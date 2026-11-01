// spa/src/components/approval/RelayBrief.tsx — the pieces UP-5a adds to a relay card: the session's title beside its id,
// the 重新申請 mark. Pure display; the host passes ids and strings.
import { useI18nStore } from '../../stores/useI18nStore'
import { clipForDisplay } from '../../lib/team/types'

/** The title the session wrote for itself, cleaned for display; renders nothing when blank. Optionally followed by the 重新申請 mark. */
export function RelayTitle({ title, reask }: { title: string; reask?: boolean }) {
  const t = useI18nStore((s) => s.t)
  const shown = clipForDisplay(title, 80).trim()
  if (shown === '' && !reask) return null
  return (
    <>
      {shown !== '' && <span data-testid="approval-title" dir="auto" className="ml-2 break-all font-sans text-text-secondary">{shown}</span>}
      {reask === true && <span data-testid="approval-reask" className="ml-2 rounded bg-surface-hover px-1 font-sans text-[10px] text-text-secondary">{t('approval.dialog.reask')}</span>}
    </>
  )
}

