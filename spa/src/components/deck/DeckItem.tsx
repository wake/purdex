// spa/src/components/deck/DeckItem.tsx — one conversation item, drawn the deck's way (U3 spec §4). An item type this build
// does not know draws nothing (the daemon may add kinds; older clients skip them, U1 §8.1).
import { useI18nStore } from '../../stores/useI18nStore'
import { isKnownItem, type ConversationItem } from '../../lib/conversations/types'
import RoomProse, { RoomProseLight } from '../room/RoomProse'
import { StepView, type StepActions } from './StepViews'
import { SystemRow, ThinkingRow } from './ThinkingSystem'
import { UserBlock } from './UserBlock'
import { AgentHeader } from './AgentHeader'
import { agentUnitId } from '../../lib/conversations/find-units'

/** `light`: the agent text is drawn as plain text (a turn far off screen, #2469); streaming text is always drawn in full. */
export function DeckItem({ item, actions, light, agentModel, agentHeader, midTurn }: { item: ConversationItem; /** a user message that is not the first of its turn */ midTurn?: boolean; actions?: StepActions; light?: boolean; /** proto v2: the first agent text of a turn carries the head line */ agentModel?: string; agentHeader?: boolean }) {
  const t = useI18nStore((s) => s.t)
  if (!isKnownItem(item)) return null
  switch (item.type) {
    case 'user': return <UserBlock item={item} midTurn={midTurn} />
    case 'agent_text':
      return (
        <div data-testid="deck-agent-text" className="deck-md">
          {agentHeader && <AgentHeader model={agentModel} at={item.at} />}
          {light && !item.streaming
            ? <RoomProseLight content={item.markdown} cjk />
            : <RoomProse content={item.markdown} streaming={item.streaming} cacheKey={item.id} searchUnit={agentUnitId(item.id)} cjk />}
          {item.truncated && <div className="deck-t-down text-text-muted">{t('deck.output.cut')}</div>}
        </div>
      )
    case 'thinking': return <ThinkingRow item={item} />
    case 'step': return <StepView step={item} actions={actions} />
    case 'system': return <SystemRow item={item} />
  }
}
