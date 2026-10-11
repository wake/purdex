// spa/src/components/deck/PendingTail.tsx — what is waiting to be read, drawn where the terminal draws it: at the end of the stream, inside the scroll
// box, after the live line (c9d9c7-62). Two kinds, Claude Code's own queue first (it got in earlier), then what the App still holds:
//  - header.queued: messages typed in the terminal (or sent by a peer) while a turn runs, in Claude Code's queue. Accent-framed like the person's first-cut block, no Undo (the App cannot
//    reach them); a peer's keeps its teal, faded. Only while the conversation runs or waits for the person: an idle one has an empty queue.
//  - the App's send queue (QueuedMessages): the same frame with Undo, Up in an empty box calls them back.
import { useEffect } from 'react'
import { useI18nStore } from '../../stores/useI18nStore'
import { draftKey } from '../../lib/conversations/draft-memory'
import { unboundSendPort } from '../../lib/conversations/send'
import { sendQueueFor, type QueueEntry } from '../../lib/conversations/send-queue'
import { canTakeBack, dismissTakeBackNotice, isRowHidden, setHeaderUserRows, takeBackHeld, takeBackQueued, useTakeBackState } from '../../lib/conversations/take-back'
import type { HeaderQueued } from '../../lib/conversations/types'
import { adapterOf } from '../../lib/conversations/deck-adapter'
import { QueuedMessages, TAKE_BACK_BOX, TakeBackSpinner, activateProps } from './QueuedMessages'
import { PENDING_USER_BOX, PENDING_USER_CAPTION } from './UserBlock'

function HeaderQueuedLine({ item, onTakeBack, busy }: { item: HeaderQueued; onTakeBack?: () => void; busy: boolean }) {
  const t = useI18nStore((s) => s.t)
  if (item.source === 'peer') {
    return (
      <div data-testid="header-queued" data-source="peer" className="rounded-md border border-peer/40 bg-peer/5 px-3 py-2 opacity-70">
        <div className="mb-1 deck-t-down font-medium text-peer">{t('deck.user.from_queued', { name: item.from || t('deck.user.peer') })}</div>
        <div className="whitespace-pre-wrap break-words deck-t-up text-text-secondary">{item.text}</div>
      </div>
    )
  }
  const act = onTakeBack ? activateProps(onTakeBack, t('deck.recall.title')) : {}
  return (
    <div data-testid="header-queued" data-source="user" aria-busy={onTakeBack && busy ? true : undefined} {...act} className={`${PENDING_USER_BOX} px-3 py-2 ${onTakeBack ? TAKE_BACK_BOX : ''}`}>
      <div className={`mb-1 deck-t-down font-medium ${PENDING_USER_CAPTION}`}>{t('deck.user.queued')}{onTakeBack && busy && <> <TakeBackSpinner /></>}</div>
      <div className="whitespace-pre-wrap break-words deck-t-up text-text-primary">{item.text}</div>
      <div className="deck-t-down text-text-system">{t('deck.user.queued_terminal')}</div>
    </div>
  )
}

interface Props {
  paneKey: string
  hostId: string
  sessionId: string
  entries: readonly QueueEntry[]
  /** header.queued, already filtered by the status (`shownHeaderQueue`). */
  queued: readonly HeaderQueued[]
}

export function PendingTail({ paneKey, hostId, sessionId, entries, queued }: Props) {
  const t = useI18nStore((s) => s.t)
  const key = draftKey(paneKey, hostId, sessionId)
  const queue = sendQueueFor(key, () => unboundSendPort(hostId, sessionId))
  const tb = useTakeBackState(key)
  // the rows the deck renders, for Up (is there a Claude Code queue to take back?) and to end the hide after a take-back once the header caught up
  const userRowsSig = JSON.stringify(queued.filter((q) => q.source !== 'peer').map((q) => q.text))
  useEffect(() => { setHeaderUserRows(key, JSON.parse(userRowsSig) as string[]) }, [key, userRowsSig])
  const shownEntries = entries.filter((e) => e.state !== 'superseded' && !e.matched)
  const rows = queued.filter((q) => q.source === 'peer' || !isRowHidden(tb, q.text))
  if (rows.length === 0 && shownEntries.length === 0 && !tb.notice) return null
  const takeBack = canTakeBack(key) ? () => { void takeBackQueued(key, queue) } : undefined
  return (
    <div data-testid="pending-tail" className="space-y-3">
      {rows.map((q, i) => <HeaderQueuedLine key={`${q.at}:${i}`} item={q} onTakeBack={q.source === 'peer' ? undefined : takeBack} busy={tb.busy} />)}
      <QueuedMessages
        entries={entries}
        className="flex flex-col gap-2"
        canUndo={adapterOf(key)?.recall !== false}
        onUndo={(id) => { takeBackHeld(key, queue, id) }}
        onResend={(id) => { queue.resend(id) }}
        onDismiss={(id) => queue.dismiss(id)}
        onTakeBack={takeBack}
        busy={tb.busy}
      />
      {tb.notice && (
        <div data-testid="take-back-notice" role="status" className={`flex items-start gap-2 deck-t-down ${tb.notice.tone === 'error' ? 'text-status-error' : 'text-text-muted'}`}>
          <span className="min-w-0 flex-1">{t(tb.notice.key, tb.notice.params)}</span>
          <button type="button" className="rounded px-1 text-accent hover:underline" onClick={() => dismissTakeBackNotice(key)}>{t('deck.recall.dismiss')}</button>
        </div>
      )}
    </div>
  )
}
