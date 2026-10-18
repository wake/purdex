// spa/src/components/deck/QueuedMessages.tsx — the local echo of what was sent but is not in the transcript yet (U3 plan D7, K-2 plan D6):
// the person's first-cut block (accent frame — not accepted yet) from the send buffer until the transcript has it: 「你 · 排隊中」 while the App
// still holds it, 「傳送中」 once it is handed out, 「已送出」 once the daemon / worker took it (`keys` mode adds 「未經 mod 確認」). While Claude Code's own
// queue line renders the same text, the entry is hidden (`matched`): the line stands for it. 「可能已送出」 when the answer was lost, the reason when it
// was not sent. A message that MAY have run is only resent after an explicit confirmation (it can arrive twice), and as a new message.
import { useState } from 'react'
import { CircleNotch } from '@phosphor-icons/react'
import { useI18nStore } from '../../stores/useI18nStore'
import { outcomeMessage } from '../../lib/conversations/send'
import type { QueueEntry } from '../../lib/conversations/send-queue'
import { stageMarkOf } from '../../lib/conversations/stage-mark'
import { formatClock } from '../../lib/conversations/deck-format'
import { TAKE_BACK_BOX, activateProps } from './take-back-ui'
import { PENDING_USER_BOX, PENDING_USER_BOX_CHAT, PENDING_USER_CAPTION } from './UserBlock'

/** The spinner on a row whose take-back is on its way. */
export const TakeBackSpinner = () => <CircleNotch data-testid="take-back-spinner" size={12} className="inline animate-spin text-accent" />

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
  /** K-2b: Claude Code's queue can be taken back; an entry the daemon handed over (and no header row stands for) is clickable. Absent: not clickable. */
  onTakeBack?: () => void
  /** A take-back is on its way (the rows show a spinner). */
  busy?: boolean
  /** 'chat': the rows sit on the right in the delivered bubble's shape (the caller's className must align them). Default 'deck': full-width blocks. */
  layout?: 'deck' | 'chat'
}

export function QueuedMessages({ entries, onUndo, onResend, onDismiss, className = 'flex flex-col gap-1 px-3 pb-1', canUndo = true, onTakeBack, busy = false, layout = 'deck' }: Props) {
  const chat = layout === 'chat'
  const t = useI18nStore((s) => s.t)
  const [confirming, setConfirming] = useState<string | null>(null)
  // a staged entry is THE bubble even while Claude Code's queue line renders its message (PendingTail covers that row instead)
  const shown = entries.filter((e) => e.state !== 'superseded' && (!e.matched || e.stage !== undefined))
  if (shown.length === 0) return null
  const btn = 'rounded px-1.5 deck-t-down text-accent hover:underline'
  return (
    <div data-testid="queued-messages" className={className}>
      {shown.map((e) => {
        const mark = e.stage ? stageMarkOf(e.stage) : null // the daemon's word on where it is (message stages); null for `read` (a normal message) and without stages
        const m = e.outcome && (e.state === 'maybe' || e.state === 'failed') ? outcomeMessage(e.outcome) : null
        const asking = confirming === e.id && e.state === 'maybe'
        // Until the transcript has it, the message keeps the not-yet-accepted frame. "Queued" is only ever said of what the App still holds (the send
        // buffer, with its Undo); a message already handed out says it is on its way, or sent.
        if (e.state === 'undo' || e.state === 'sending' || e.state === 'sent') {
          // On a host that reports stages the caption keeps who and when (「你 · 時間」) and the stage goes bottom-right of the bubble (message-stages D1);
          // without stages the caption carries the state, as it always did.
          const staged = e.stage !== undefined || (e.stagesHost === true && (e.state === 'undo' || e.state === 'sending'))
          const stageText = !staged ? '' : e.stage ? (mark ? t(mark.key, mark.params) : '') : t('deck.stage.local')
          const label = staged ? t('deck.user.you', { time: formatClock(e.createdAt) }) : e.state === 'undo' ? t('deck.user.queued') : e.state === 'sending' ? t('deck.send.sending') : e.mode === 'keys' ? t('deck.send.handed_keys') : t('deck.send.accepted')
          // the whole block takes that entry back: the held one (no daemon call), or a handed one (Claude Code's queue)
          const held = e.state === 'undo' && canUndo
          const handed = e.state === 'sent' && e.mode !== undefined && !!onTakeBack
          const act = held ? activateProps(() => onUndo(e.id), t('deck.recall.title')) : handed ? activateProps(onTakeBack!, t('deck.recall.title')) : {}
          return (
            <div key={e.id} data-testid="queued-message" data-state={e.state} data-mode={e.mode} aria-busy={handed && busy ? true : undefined} {...act} className={`${chat ? PENDING_USER_BOX_CHAT : PENDING_USER_BOX} px-3 py-2 ${held || handed ? TAKE_BACK_BOX : ''}`}>
              <div data-testid="queued-caption" className={`mb-1 deck-t-down font-medium ${PENDING_USER_CAPTION}`}>{label}{handed && busy && <> <TakeBackSpinner /></>}</div>
              <div className="whitespace-pre-wrap break-words deck-t-up text-text-primary">{e.text}</div>
              <div className="flex items-baseline gap-2">
                {e.state === 'undo' && canUndo && <button type="button" className="rounded px-0 deck-t-down text-accent underline-offset-2 hover:underline" onClick={(ev) => { ev.stopPropagation(); onUndo(e.id) }}>{t('deck.send.undo')}</button>}
                {/* the transcript's row is what takes it away; one whose row never comes (or reads differently) must not leave it here for good */}
                {e.state === 'sent' && <button type="button" className={btn} onClick={(ev) => { ev.stopPropagation(); onDismiss(e.id) }}>{t('deck.send.dismiss')}</button>}
                {stageText !== '' && <span data-testid="stage-mark" className="ml-auto text-right deck-t-down font-medium text-accent">{stageText}</span>}
              </div>
            </div>
          )
        }
        const caption = asking ? t('deck.send.resend_confirm') : mark ? t('deck.user.you', { time: formatClock(e.createdAt) }) : m ? t(m.key, m.params) : t('deck.send.sending')
        return (
          <div key={e.id} data-testid="queued-message" data-state={e.state} className={`${chat ? 'max-w-[80%] rounded-2xl rounded-br-sm' : 'rounded-lg'} border border-border-subtle bg-surface-secondary px-3 py-1.5`}>
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
              {mark && <span data-testid="stage-mark" className="ml-auto text-right deck-t-down font-medium text-status-warning-text">{t(mark.key, mark.params)}</span>}
            </div>
          </div>
        )
      })}
    </div>
  )
}
