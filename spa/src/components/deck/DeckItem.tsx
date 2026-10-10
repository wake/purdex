// spa/src/components/deck/DeckItem.tsx — one conversation item, drawn the deck's way (U3 spec §4). An item type this build
// does not know draws nothing (the daemon may add kinds; older clients skip them, U1 §8.1).
import { useI18nStore } from '../../stores/useI18nStore'
import { isKnownItem, type ConversationItem } from '../../lib/conversations/types'
import RoomProse from '../room/RoomProse'
import { StepView, type StepActions } from './StepViews'
import { SystemRow, ThinkingRow } from './ThinkingSystem'
import { UserBlock } from './UserBlock'

export function DeckItem({ item, actions }: { item: ConversationItem; actions?: StepActions }) {
  const t = useI18nStore((s) => s.t)
  if (!isKnownItem(item)) return null
  switch (item.type) {
    case 'user': return <UserBlock item={item} />
    case 'agent_text':
      return (
        <div data-testid="deck-agent-text" className="deck-md">
          <RoomProse content={item.markdown} streaming={item.streaming} />
          {item.truncated && <div className="text-xs text-text-muted">{t('deck.output.cut')}</div>}
        </div>
      )
    case 'thinking': return <ThinkingRow item={item} />
    case 'step': return <StepView step={item} actions={actions} />
    case 'system': return <SystemRow item={item} />
  }
}
