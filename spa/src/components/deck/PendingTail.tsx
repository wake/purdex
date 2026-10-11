// spa/src/components/deck/PendingTail.tsx — what is waiting to be read, drawn where the terminal draws it: at the end of the stream, inside the scroll
// box, after the live line (c9d9c7-62). Two kinds, Claude Code's own queue first (it got in earlier), then what the App still holds:
//  - header.queued: messages typed in the terminal (or sent by a peer) while a turn runs, in Claude Code's queue. Grey, no Undo (the App cannot
//    reach them); a peer's keeps its teal, faded. Only while the conversation runs or waits for the person: an idle one has an empty queue.
//  - the App's send queue (QueuedMessages): grey with Undo, Up in an empty box calls them back.
import { useI18nStore } from '../../stores/useI18nStore'
import { draftKey } from '../../lib/conversations/draft-memory'
import { hostSendPort } from '../../lib/conversations/send'
import { sendQueueFor, type QueueEntry } from '../../lib/conversations/send-queue'
import { recallHeld } from '../../lib/conversations/draft-recall'
import type { HeaderQueued } from '../../lib/conversations/types'
import { QueuedMessages } from './QueuedMessages'

function HeaderQueuedLine({ item }: { item: HeaderQueued }) {
  const t = useI18nStore((s) => s.t)
  if (item.source === 'peer') {
    return (
      <div data-testid="header-queued" data-source="peer" className="rounded-md border border-peer/40 bg-peer/5 px-3 py-2 opacity-70">
        <div className="mb-1 deck-t-down font-medium text-peer">{t('deck.user.from_queued', { name: item.from || t('deck.user.peer') })}</div>
        <div className="whitespace-pre-wrap break-words deck-t-up text-text-secondary">{item.text}</div>
      </div>
    )
  }
  return (
    <div data-testid="header-queued" data-source="user" className="text-text-system">
      <div className="deck-t-down">{t('deck.user.queued')}</div>
      <div className="whitespace-pre-wrap break-words deck-t-up">{item.text}</div>
      <div className="deck-t-down opacity-80">{t('deck.user.queued_terminal')}</div>
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
  const key = draftKey(paneKey, hostId, sessionId)
  const queue = sendQueueFor(key, () => hostSendPort(hostId, sessionId))
  const shownEntries = entries.filter((e) => e.state !== 'superseded')
  if (queued.length === 0 && shownEntries.length === 0) return null
  return (
    <div data-testid="pending-tail" className="space-y-3">
      {queued.map((q, i) => <HeaderQueuedLine key={`${q.at}:${i}`} item={q} />)}
      <QueuedMessages
        entries={entries}
        className="flex flex-col gap-2"
        onUndo={(id) => { recallHeld(key, queue, id) }}
        onResend={(id) => { queue.resend(id) }}
        onDismiss={(id) => queue.dismiss(id)}
      />
    </div>
  )
}
