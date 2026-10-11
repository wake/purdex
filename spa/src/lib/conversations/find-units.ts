// spa/src/lib/conversations/find-units.ts — what the ⌘F search of the deck and of the chat looks through (#2665): the conversation's
// items as `SearchUnit`s (lib/nex/transcript-search.ts is the matching core). A unit's id is the `data-search-unit` of the element
// that draws its text, and its text is that element's `textContent`, so a match's ordinal finds the same occurrence in the DOM
// (search-highlight.ts). Units come in the order the view draws them. Pure: no React, no store.
//
// What is searched is what the view draws: the deck has the person's words, the agent's prose, and each step's one-line subject
// (a command, a path, a summary); the chat has the bubbles and the peer lines (its work rows are chains of steps, opened in the panel).
import { proseText } from '../nex/markdown-text'
import type { SearchUnit } from '../nex/transcript-search'
import { buildChat } from './chat-model'
import { splitImageRefs, imageMarker } from './image-placeholder'
import { OWN_TEXT_SOURCES, isImagePath } from './image-sniff'
import type { PanelTurn } from './panel-resolve'
import { outputTail, textOutput } from './deck-format'
import { isKnownItem, type AgentTextItem, type ConversationItem, type StepItem, type Turn, type UserItem } from './types'
import type { SubagentState } from '../../stores/useConversationStore'

/** The fold key that makes a far-off turn of the deck draw its agent text in full (the unit is the real markdown's text, not the plain source). */
export const fullTurnKey = (turnId: string): string => `deck-full:${turnId}`

export const userUnitId = (id: string): string => `u:${id}`
export const agentUnitId = (id: string): string => `a:${id}`
export const stepUnitId = (id: string): string => `s:${id}`
/** A folded output's unit is its fold key (OutputFold draws `data-search-unit={foldKey}` on the body): opening the fold is its reveal. */
export const outputKey = (id: string): string => `${id}:out`
/** The key a task line's chevron toggles to show the agent's own steps. */
export const subagentKey = (stepId: string): string => `${stepId}:sub`
/** The key a collapsed peer message toggles (the Mac peer-message collapse and the chat's peer line use it): a hit in its cut lines opens it. */
export const peerKey = (id: string): string => `${id}:peer`

/**
 * The text of a message the way UserText draws it: an image reference that names an image file is a thumbnail (no text of its own), any
 * other stays as the marker it was. Only the person's own messages are drawn that way (UserBlock / UserBubble decide).
 */
export function userText(item: UserItem): string {
  if (item.source === 'bash') return `! ${item.text}`
  if (!OWN_TEXT_SOURCES.has(item.source)) return item.text
  const parts = splitImageRefs(item.text)
  if (!parts.some((p) => p.kind === 'image')) return item.text
  return parts.map((p) => (p.kind === 'text' ? p.text : isImagePath(p.path) ? '' : imageMarker(p.path))).join('')
}

/** What a step draws as its one line of text, or null when it draws none that is searched. */
export function stepSubject(step: StepItem): string | null {
  if (step.question && Array.isArray(step.question.questions)) return null
  switch (step.kind) {
    case 'execute': return `$ ${step.command?.text ?? step.summary}`
    case 'edit': return step.diff ? (step.diff.path ?? step.summary) : step.summary
    case 'task': return step.subagent?.description ?? step.summary
    default: return step.summary
  }
}

function add(out: SearchUnit[], id: string, text: string, reveal: string[] = []): void {
  if (text !== '') out.push({ id, text: text.normalize('NFC'), reveal })
}

/** Steps whose output the deck draws in a fold under the line (the rest — a read, a task, a diff card — draw none). */
const hasOutputFold = (step: StepItem): boolean => !(step.question && Array.isArray(step.question.questions)) && step.kind !== 'read' && step.kind !== 'task' && step.kind !== 'edit'

/** The text an OutputFold body draws, or null when it draws none. */
function outputText(output: StepItem['output']): string | null {
  if (!output || typeof output.text !== 'string' || output.text === '') return null
  return outputTail(output).text
}

/** One item's units (and, for a task line, its unfolded agent's), in the order the deck draws them. `outer`: folds that hide the item itself. */
function itemUnits(out: SearchUnit[], item: ConversationItem, outer: string[], far: string[], subagents: Record<string, SubagentState> | undefined): void {
  if (!isKnownItem(item)) return
  if (item.type === 'user') {
    if (item.source === 'command_output') {
      const text = outputText(textOutput(item.text, item.truncated))
      if (text !== null) add(out, outputKey(item.id), text, [...outer, outputKey(item.id)])
    } else add(out, userUnitId(item.id), userText(item), item.source === 'peer' ? [...outer, peerKey(item.id)] : outer)
  } else if (item.type === 'agent_text') {
    add(out, agentUnitId(item.id), proseText((item as AgentTextItem).markdown), [...outer, ...far])
  } else if (item.type === 'step') {
    const subject = stepSubject(item)
    if (subject !== null) add(out, stepUnitId(item.id), subject, outer)
    const text = hasOutputFold(item) ? outputText(item.output) : null
    if (text !== null) add(out, outputKey(item.id), text, [...outer, outputKey(item.id)])
    // the agent's own steps, once they are loaded (the line's chevron loads them; an unloaded agent has nothing to search yet)
    const agentId = item.kind === 'task' ? item.subagent?.agent_id : undefined
    const st = agentId ? subagents?.[agentId] : undefined
    if (st?.state === 'ready' && st.answer?.items) {
      for (const child of st.answer.items as ConversationItem[]) itemUnits(out, child, [...outer, subagentKey(item.id)], far, undefined)
    }
  }
}

/**
 * The deck's units, in the order it draws them. `eager`: how many of the newest turns are always drawn in full (the older ones need
 * `fullTurnKey` for their prose). `subagents`: what the conversation holds of each agent's own steps.
 */
export function deckUnits(turns: readonly Turn[], eager: number, subagents?: Record<string, SubagentState>): SearchUnit[] {
  const out: SearchUnit[] = []
  turns.forEach((turn, i) => {
    const far = i < turns.length - eager ? [fullTurnKey(turn.id)] : []
    for (const item of turn.items) itemUnits(out, item, [], far, subagents)
  })
  return out
}

/** The chat's units, in the order it draws them. */
export function chatUnits(turns: readonly PanelTurn[]): SearchUnit[] {
  const out: SearchUnit[] = []
  for (const entry of buildChat(turns as PanelTurn[])) {
    if (entry.kind === 'user') add(out, userUnitId(entry.item.id), userText(entry.item))
    else if (entry.kind === 'agent') add(out, agentUnitId(entry.item.id), proseText(entry.item.markdown))
  }
  return out
}
