// spa/src/components/AdoptionWaitCard.tsx — what a remote adopt shows after 「核准」 (cross-host spec §4.3, X3c-App):
// the consent is given, the member host has not answered. Not modal (the approval it came from is closed): a small
// card in the corner, 「等待 <alias> 回覆…」, then the outcome. 「先關閉」 hides it; the wait goes on in
// `lib/team/adoption-wait.ts` and the outcome arrives as a toast.
import { ArrowsClockwise } from '@phosphor-icons/react'
import { useAdoptionWait, adoptionWaitText } from '../lib/team/adoption-wait'
import { useI18nStore } from '../stores/useI18nStore'

export function AdoptionWaitCard() {
  const t = useI18nStore((s) => s.t)
  const entry = useAdoptionWait((s) => Object.values(s.entries).find((e) => !e.dismissed))
  if (!entry) return null
  const waiting = entry.state === 'waiting'
  const tone = entry.state === 'active' ? 'text-status-success' : waiting ? 'text-text-secondary' : 'text-status-warning'
  return (
    <div
      role="status"
      data-testid="adoption-wait-card"
      data-state={entry.state}
      className="fixed bottom-4 right-4 z-50 w-[320px] rounded-lg border border-border-default bg-surface-primary p-3 shadow-lg"
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
