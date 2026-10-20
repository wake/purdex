// spa/src/components/watch/jobs/RecentEnded.tsx — the last five jobs that ended, under the queue flow (monitoring tabs spec M34): when, the result in words, the job, where it was sent from and where it ran, how long it took, its class and slot.
// No usage column (G16). A row selects the job like a card does.
import { CheckCircle, XCircle } from '@phosphor-icons/react'
import { useI18nStore } from '../../../stores/useI18nStore'
import { useSidebarHostBadgeGeometry } from '../../../hooks/useSidebarHostBadgeGeometry'
import { resolveHostBadge } from '../../../lib/host-color'
import { mss } from '../../../lib/watch/queue-time'
import { jobLine } from '../../../lib/pool/jobs-lanes'
import { jobRanOn, resultText, resultWord, whenText } from '../../../lib/pool/jobs-view'
import { jobKey } from '../../../lib/pool/jobs-view-memory'
import type { PoolJob } from '../../../lib/pool/jobs-types'
import type { PoolHostResolvedLook } from '../../../lib/pool/host-looks'
import { HostBadge } from '../../HostBadge'

const CLASSES = ['release', 'merge', 'dev']

function Badge({ look }: { look: PoolHostResolvedLook | undefined }) {
  const { lineColor } = useSidebarHostBadgeGeometry()
  const b = resolveHostBadge(look ?? {}, 'console')
  return <HostBadge colors={b.colors} icon={b.icon} iconWeight={b.iconWeight} box={13} inset={2} radius={3} lineColor={lineColor} />
}

export function RecentEnded({ jobs, now, looks, selected, onSelect }: {
  jobs: readonly PoolJob[]
  now: number
  looks: Record<string, PoolHostResolvedLook>
  selected: string | null
  onSelect: (key: string | null) => void
}) {
  const t = useI18nStore((s) => s.t)
  return (
    <section aria-label={t('watch.jobs.flow.recent')} data-testid="flow-recent">
      <h3 className="mb-1.5 text-xs font-medium text-text-secondary">{t('watch.jobs.flow.recent')}</h3>
      {jobs.length === 0
        ? <p data-testid="flow-recent-none" className="text-xs text-text-muted">{t('watch.jobs.flow.recent_none')}</p>
        : (
          <ul className="flex flex-col gap-0.5">
            {jobs.map((j) => {
              const key = jobKey(j.origin, j.id)
              const on = selected === key
              const ok = j.state === 'succeeded'
              const ran = jobRanOn(j)
              const cls = CLASSES.includes(j.class) ? t(`watch.jobs.class.${j.class}`) : j.class
              const slot = t(`watch.jobs.flow.class.${j.slot === 'light' ? 'light' : 'heavy'}`)
              return (
                <li key={key}>
                  <button
                    type="button" aria-pressed={on} onClick={() => onSelect(on ? null : key)} data-testid="flow-recent-row" data-job-id={j.id} title={jobLine(j)}
                    className={`flex w-full items-center gap-2 rounded px-1 py-0.5 text-left text-xs ${on ? 'ring-1 ring-border-active' : ''}`}
                  >
                    <span className="w-12 shrink-0 tabular-nums text-text-muted">{whenText(j.result?.finished_at ?? null, now)}</span>
                    {ok ? <CheckCircle size={14} weight="fill" aria-hidden="true" className="shrink-0 text-status-success-text" /> : <XCircle size={14} weight="fill" aria-hidden="true" className="shrink-0 text-status-error-text" />}
                    <span className="w-24 shrink-0 truncate text-text-secondary">{resultText(resultWord(j), t)}</span>
                    <span className="min-w-0 flex-1 truncate font-mono text-text-primary">{jobLine(j)}</span>
                    <Badge look={looks[j.origin]} />
                    {ran !== '' && <><span className="text-text-muted">→</span><Badge look={looks[ran]} /><span className="text-text-secondary">{ran}</span></>}
                    <span data-testid="flow-recent-took" className="shrink-0 tabular-nums text-text-muted">{j.result ? mss(j.result.started_at, j.result.finished_at ?? now) : '—'}</span>
                    <span className="shrink-0 text-[10px] text-text-muted">{t('watch.jobs.flow.recent_class', { cls, slot })}</span>
                  </button>
                </li>
              )
            })}
          </ul>
        )}
    </section>
  )
}
