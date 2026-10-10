// spa/src/components/team/WorkbookStatus.tsx — 「目前狀況」 and when it was written (WA-2b-2).
import { useI18nStore } from '../../stores/useI18nStore'
import { formatWhen } from '../../lib/workbook/view-model'

interface Props { status: string; statusAt: number; loading: boolean }

export function WorkbookStatus({ status, statusAt, loading }: Props) {
  const t = useI18nStore((s) => s.t)
  const text = status.trim()
  const empty = loading ? t('team.workbook.loading') : t('team.workbook.no_status')
  return (
    <section>
      <div className="flex items-baseline gap-2 text-[10px] text-text-muted mb-0.5">
        <span>{t('team.workbook.status')}</span>
        {text !== '' && <span data-testid="team-seat-workbook-status-time">{formatWhen(statusAt)}</span>}
      </div>
      <div data-testid="team-seat-workbook-status" className="whitespace-pre-wrap break-words">{text !== '' ? status : empty}</div>
    </section>
  )
}
