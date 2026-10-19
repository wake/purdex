// spa/src/lib/conversations/deck-speech.ts — which agent_text items of a turn are SPEECH STARTS (name pool v2 spec §9.4, U15): the item before it in the turn is not agent_text. `thinking` counts as a break (the agent
// spoke, thought, spoke again: two starts); `system` does not (it is the stream's own line, not the agent's, so text on either side of it is one run); a tool step and a user message break it. An item this build does
// not know draws nothing, so it is looked through like a system row.
import { isKnownItem, type ConversationItem } from './types'

/**
 * The ids of a turn's agent_text items that begin a run of speech. `afterOmitted`: the turn left its first items out, so what came before the first one in view is not known: a text there is not claimed as a start
 * (it may well continue a run), though the first text after something that is known — a tool, a thought — still is.
 */
export function speechStartIds(items: readonly ConversationItem[], opts: { afterOmitted?: boolean } = {}): Set<string> {
  const out = new Set<string>()
  let prev: ConversationItem['type'] | 'unknown' | null = opts.afterOmitted ? 'unknown' : null
  for (const item of items) {
    if (!isKnownItem(item) || item.type === 'system') continue
    if (item.type === 'agent_text' && prev !== 'agent_text' && prev !== 'unknown') out.add(item.id)
    prev = item.type
  }
  return out
}
