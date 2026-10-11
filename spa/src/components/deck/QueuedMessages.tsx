// spa/src/components/deck/QueuedMessages.tsx — the local echo of what was sent but is not in the transcript yet (U3 plan D7):
// 「你 · 排隊中」 (grey, no card) while the App still holds it, 「傳送中」 once it is handed out, 「可能已送出」 when the answer was lost, the reason when it was not sent.
// A message that MAY have run is only resent after an explicit confirmation (it can arrive twice), and as a new message.
import { useState } from 'react'
import { useI18nStore } from '../../stores/useI18nStore'
import { outcomeMessage } from '../../lib/conversations/send'
import type { QueueEntry } from '../../lib/conversations/send-queue'

interface Props {
  entries: readonly QueueEntry[]
  onUndo: (id: string) => void
  /** Resend as a NEW message (a new client_msg_id). */
  onResend: (id: string) => void
  onDismiss: (id: string) => void
}

export function QueuedMessages({ entries, onUndo, onResend, onDismiss }: Props) {
  const t = useI18nStore((s) => s.t)
  const [confirming, setConfirming] = useState<string | null>(null)
  const shown = entries.filter((e) => e.state !== 'superseded')
  if (shown.length === 0) return null
  const btn = 'rounded px-1.5 deck-t-down text-accent hover:underline'
  return (
    <div data-testid="queued-messages" className="flex flex-col gap-1 px-3 pb-1">
      {shown.map((e) => {
        const m = e.outcome && (e.state === 'maybe' || e.state === 'failed') ? outcomeMessage(e.outcome) : null
        const asking = confirming === e.id && e.state === 'maybe'
        // What the App still holds (undo window, waiting for idle) is the terminal's grey queued line: no card, Up in an empty box brings it back.
        // "Queued" is only ever said of these; a message already handed out says it is on its way.
        if (e.state === 'undo' || e.state === 'waiting') {
          return (
            <div key={e.id} data-testid="queued-message" data-state={e.state} className="text-text-system">
              <div className="text-xs">{e.state === 'waiting' ? t('deck.send.waiting') : t('deck.user.queued')}</div>
              <div className="whitespace-pre-wrap break-words text-sm">{e.text}</div>
              <button type="button" className="rounded px-0 text-xs underline-offset-2 hover:underline" onClick={() => onUndo(e.id)}>{t('deck.send.undo')}</button>
            </div>
          )
        }
        const caption = asking ? t('deck.send.resend_confirm') : m ? t(m.key, m.params) : e.state === 'sent' ? t('deck.send.accepted') : t('deck.send.sending')
        return (
          <div key={e.id} data-testid="queued-message" data-state={e.state} className="rounded-lg border border-border-subtle bg-surface-secondary px-3 py-1.5">
            <div className={`deck-t-down ${m?.tone === 'error' ? 'text-status-error' : 'text-text-muted'}`}>{caption}</div>
            <div className="whitespace-pre-wrap break-words deck-t-up text-text-primary">{e.text}</div>
            <div className="flex gap-1">
              {e.state === 'failed' && <button type="button" className={btn} onClick={() => onResend(e.id)}>{t('deck.send.retry')}</button>}
              {e.state === 'maybe' && !asking && <button type="button" className={btn} onClick={() => setConfirming(e.id)}>{t('deck.send.resend')}</button>}
              {asking && (
                <>
                  <button type="button" className={btn} onClick={() => { setConfirming(null); onResend(e.id) }}>{t('deck.send.resend_yes')}</button>
                  <button type="button" className={btn} onClick={() => setConfirming(null)}>{t('deck.send.resend_no')}</button>
                </>
              )}
              {(e.state === 'maybe' || e.state === 'failed') && !asking && <button type="button" className={btn} onClick={() => onDismiss(e.id)}>{t('deck.send.dismiss')}</button>}
            </div>
          </div>
        )
      })}
    </div>
  )
}
