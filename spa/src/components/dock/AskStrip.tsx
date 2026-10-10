// spa/src/components/dock/AskStrip.tsx — the terminal view draws no dock; while a question is open it shows this thin strip at its
// top (U3 spec §7). The pane's own subscription feeds it (plan D4), so it needs no view of the deck.
import { useI18nStore } from '../../stores/useI18nStore'

export function AskStrip({ open }: { open: boolean }) {
  const t = useI18nStore((s) => s.t)
  if (!open) return null
  return (
    <div data-testid="ask-strip" role="status" className="pointer-events-none absolute inset-x-0 top-0 z-10 flex justify-center">
      <span className="rounded-b bg-status-warning/90 px-3 py-0.5 text-xs font-medium text-black">{t('deck.dock.strip')}</span>
    </div>
  )
}
