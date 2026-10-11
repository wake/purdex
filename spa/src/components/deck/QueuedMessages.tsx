// spa/src/components/deck/QueuedMessages.tsx — the local echo of what was sent but is not in the transcript yet (U3 plan D7, K-2 plan D6):
// the person's first-cut block (accent frame — not accepted yet) from the send buffer until the transcript has it: 「你 · 排隊中」 while the App
// still holds it, 「傳送中」 once it is handed out, 「已送出」 once the daemon / worker took it (`keys` mode adds 「未經 mod 確認」). While Claude Code's own
// queue line renders the same text, the entry is hidden (`matched`): the line stands for it. 「可能已送出」 when the answer was lost, the reason when it
// was not sent. A message that MAY have run is only resent after an explicit confirmation (it can arrive twice), and as a new message.
import { useState } from 'react'
import { useI18nStore } from '../../stores/useI18nStore'
import { outcomeMessage } from '../../lib/conversations/send'
import type { QueueEntry } from '../../lib/conversations/send-queue'
import { PENDING_USER_BOX, PENDING_USER_CAPTION } from './UserBlock'

interface Props {
  entries: readonly QueueEntry[]
  onUndo: (id: string) => void
  /** Resend as a NEW message (a new client_msg_id). */
  onResend: (id: string) => void
  onDismiss: (id: string) => void
  /** The wrapper's classes (the stream's tail draws it without the footer's side padding). */
  className?: string
  /** False: the back-end offers no recall, so no Undo is drawn (default true). */
  canUndo?: boolean
}

export function QueuedMessages({ entries, onUndo, onResend, onDismiss, className = 'flex flex-col gap-1 px-3 pb-1', canUndo = true }: Props) {
  const t = useI18nStore((s) => s.t)
  const [confirming, setConfirming] = useState<string | null>(null)
  const shown = entries.filter((e) => e.state !== 'superseded' && !e.matched)
  if (shown.length === 0) return null
  const btn = 'rounded px-1.5 deck-t-down text-accent hover:underline'
  return (
    <div data-testid="queued-messages" className={className}>
      {shown.map((e) => {
        const m = e.outcome && (e.state === 'maybe' || e.state === 'failed') ? outcomeMessage(e.outcome) : null
        const asking = confirming === e.id && e.state === 'maybe'
        // Until the transcript has it, the message keeps the not-yet-accepted frame. "Queued" is only ever said of what the App still holds (the send
        // buffer, with its Undo); a message already handed out says it is on its way, or sent.
        if (e.state === 'undo' || e.state === 'sending' || e.state === 'sent') {
          const label = e.state === 'undo' ? t('deck.user.queued') : e.state === 'sending' ? t('deck.send.sending') : e.mode === 'keys' ? t('deck.send.handed_keys') : t('deck.send.accepted')
          return (
            <div key={e.id} data-testid="queued-message" data-state={e.state} data-mode={e.mode} className={`${PENDING_USER_BOX} px-3 py-2`}>
              <div className={`mb-1 deck-t-down font-medium ${PENDING_USER_CAPTION}`}>{label}</div>
              <div className="whitespace-pre-wrap break-words deck-t-up text-text-primary">{e.text}</div>
              {e.state === 'undo' && canUndo && <button type="button" className="rounded px-0 deck-t-down text-accent underline-offset-2 hover:underline" onClick={() => onUndo(e.id)}>{t('deck.send.undo')}</button>}
              {/* the transcript's row is what takes it away; one whose row never comes (or reads differently) must not leave it here for good */}
              {e.state === 'sent' && <button type="button" className={btn} onClick={() => onDismiss(e.id)}>{t('deck.send.dismiss')}</button>}
            </div>
          )
        }
        const caption = asking ? t('deck.send.resend_confirm') : m ? t(m.key, m.params) : t('deck.send.sending')
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
