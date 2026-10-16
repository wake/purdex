// spa/src/components/journal/RoadmapCard.tsx — the body of the roadmap card (J-2d, spec §11): one table per feature, 階段 / 說明 / 狀態.
// The roadmap is read the first time the card opens (`UnitState.roadmap === null`); after that the loader keeps it fresh on
// `journal.changed what=roadmap`. The status colours are theme tokens (lib/journal/roadmap-status.ts).
import { useEffect } from 'react'
import type { JournalTarget } from './useJournalTarget'
import { ROADMAP_STATUS_STYLE } from '../../lib/journal/roadmap-status'
import { useI18nStore } from '../../stores/useI18nStore'
import { useJournalStore, type UnitState } from '../../stores/useJournalStore'

export function RoadmapCard({ target, unit }: { target: JournalTarget; unit: UnitState }) {
  const t = useI18nStore((s) => s.t)
  const readHost = target.read?.hostId
  const readSession = target.read?.sessionId
  const features = unit.roadmap
  const unread = features === null
  useEffect(() => {
    if (unread && readHost !== undefined && readSession !== undefined) void useJournalStore.getState().loadRoadmap(readHost, readSession)
  }, [unread, readHost, readSession])

  if (features === null) return <div data-testid="journal-roadmap-loading" className="text-text-muted py-6 text-center">{t('journal.roadmap.loading')}</div>
  if (features.length === 0) return <div data-testid="journal-roadmap-empty" className="text-text-muted py-6 text-center">{t('journal.roadmap.empty')}</div>
  return (
    <div data-testid="journal-roadmap-body" className="px-3 pb-3 space-y-3">
      {features.map((f, fi) => (
        <section key={`${fi}:${f.name}`} data-testid="journal-roadmap-feature">
          <h3 className="mb-1 text-[11px] font-semibold text-text-primary break-words">{f.name}</h3>
          <table className="w-full text-[11px] border-collapse">
            <thead>
              <tr className="text-left text-text-muted">
                <th className="font-normal pr-2 pb-0.5 w-14">{t('journal.roadmap.col.stage')}</th>
                <th className="font-normal pr-2 pb-0.5">{t('journal.roadmap.col.desc')}</th>
                <th className="font-normal pb-0.5 w-16">{t('journal.roadmap.col.status')}</th>
              </tr>
            </thead>
            <tbody>
              {f.stages.map((s, si) => {
                const style = ROADMAP_STATUS_STYLE[s.status]
                return (
                  <tr key={`${si}:${s.stage}`} data-testid="journal-roadmap-stage" data-status={style.id} className="border-t border-border-subtle/50 align-top">
                    <td className="py-1 pr-2 font-mono text-[10.5px] text-text-secondary break-all">{s.stage}</td>
                    <td className="py-1 pr-2 text-text-primary break-words">{s.description}</td>
                    <td className="py-1">
                      <span data-testid="journal-roadmap-status" className={`inline-flex items-center gap-1 whitespace-nowrap ${style.text}`}>
                        <span className={`w-1.5 h-1.5 rounded-full shrink-0 ${style.dot}`} />{t(`journal.roadmap.status.${style.id}`)}
                      </span>
                    </td>
                  </tr>
                )
              })}
            </tbody>
          </table>
        </section>
      ))}
    </div>
  )
}
