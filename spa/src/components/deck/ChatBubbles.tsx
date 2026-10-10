// spa/src/components/deck/ChatBubbles.tsx — the chat's bubbles (U3 spec §5): the user on the right in an accent bubble, the
// agent on the left, a peer message as ONE line (iOS 0.6.44) with 「未驗證」 when the daemon could not verify the sender.
// A user item that is not a plain message (a bash input, a schedule wake-up…) is the deck's block, as it reads best there.
import { useI18nStore } from '../../stores/useI18nStore'
import { formatClock } from '../../lib/conversations/deck-format'
import type { AgentTextItem, UserItem } from '../../lib/conversations/types'
import RoomProse from '../room/RoomProse'
import { UserBlock } from './UserBlock'

/** Sources that are not the person's own message: they keep the deck's block (a bash input, a wake-up, a background report). */
const NOT_THE_PERSON = new Set(['bash', 'command_output', 'background', 'task', 'schedule', 'scheduled'])

export function UserBubble({ item }: { item: UserItem }) {
  const t = useI18nStore((s) => s.t)
  // A source this build does not know reads as the person (as the deck's caption does).
  if (NOT_THE_PERSON.has(item.source)) return <UserBlock item={item} />
  return (
    <div data-testid="chat-user" data-source={item.source} className="flex flex-col items-end">
      <div data-testid="chat-user-bubble" className="max-w-[80%] whitespace-pre-wrap break-words rounded-2xl rounded-br-sm bg-accent px-3 py-2 text-sm text-text-inverse">
        {item.text}{item.truncated && '…'}
      </div>
      <div data-testid="chat-user-time" className="mt-0.5 text-xs text-text-muted">
        {item.source === 'queued' ? t('deck.user.queued') : formatClock(item.at)}
      </div>
      {item.images && item.images.length > 0 && <div className="mt-0.5 text-xs text-text-muted">{t('deck.user.images', { n: item.images.length })}</div>}
    </div>
  )
}

export function AgentBubble({ item }: { item: AgentTextItem }) {
  const t = useI18nStore((s) => s.t)
  return (
    <div data-testid="chat-agent" className="flex justify-start">
      <div className="max-w-[85%] rounded-2xl rounded-bl-sm bg-surface-secondary px-3 py-2 text-sm text-text-primary">
        <RoomProse content={item.markdown} streaming={item.streaming} />
        {item.truncated && <div className="text-xs text-text-muted">{t('deck.output.cut')}</div>}
      </div>
    </div>
  )
}

