// spa/src/components/AdoptionWaitCard.tsx — what a remote adopt shows after 「核准」 (cross-host spec §4.3, X3c-App):
// the consent is given, the member host has not answered. Not modal (the approval it came from is closed): small cards
// stacked in the corner, one per adoption not closed yet — 「等待 <alias> 回覆…」, then the outcome. At most
// `MAX_CARDS` show; the rest are counted (「另有 N 筆」) and appear as the others are closed. 「先關閉」 hides a card; the
// wait goes on in `lib/team/adoption-wait.ts` and the outcome arrives as a toast.
import { ArrowsClockwise } from '@phosphor-icons/react'
import { useAdoptionWait, adoptionWaitText, type AdoptionWaitEntry } from '../lib/team/adoption-wait'
import { useI18nStore } from '../stores/useI18nStore'

const MAX_CARDS = 3

function Card({ entry }: { entry: AdoptionWaitEntry }) {
  const t = useI18nStore((s) => s.t)
  const waiting = entry.state === 'waiting'
  const tone = entry.state === 'active' ? 'text-status-success' : waiting ? 'text-text-secondary' : 'text-status-warning'
  return (
    <div
      role="status"
      data-testid="adoption-wait-card"
      data-state={entry.state}
      className="w-[320px] rounded-lg border border-border-default bg-surface-primary p-3 shadow-lg"
    >
      <p data-testid="adoption-wait-target" dir="auto" className="break-words text-xs font-medium text-text-primary">{entry.target}</p>
      <p data-testid="adoption-wait-text" dir="auto" className={`mt-1 flex items-center gap-1.5 break-words text-xs ${tone}`}>
        {waiting && <ArrowsClockwise size={12} aria-hidden="true" className="shrink-0 animate-spin" />}
        {adoptionWaitText(entry)}
      </p>
      <div className="mt-2 flex justify-end">
        <button
          type="button"
          data-testid="adoption-wait-close"
          onClick={() => useAdoptionWait.getState().dismiss(entry.key)}
          className="rounded px-2 py-1 text-xs text-text-secondary hover:bg-surface-hover"
        >
          {t(waiting ? 'approval.dialog.adopt_wait.close_for_now' : 'approval.dialog.adopt_wait.close')}
        </button>
      </div>
    </div>
  )
}

export function AdoptionWaitCard() {
  const t = useI18nStore((s) => s.t)
  const entries = useAdoptionWait((s) => s.entries)
  const open = Object.values(entries).filter((e) => !e.dismissed)
  if (open.length === 0) return null
  const shown = open.slice(0, MAX_CARDS)
  const more = open.length - shown.length
  return (
    <div className="fixed bottom-4 right-4 z-50 flex flex-col items-end gap-2">
      {shown.map((e) => <Card key={e.key} entry={e} />)}
      {more > 0 && <p data-testid="adoption-wait-more" className="text-xs text-text-muted">{t('approval.dialog.adopt_wait.more', { count: more })}</p>}
    </div>
  )
}
