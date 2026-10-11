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
import { cleanCommandText } from './deck-format'
import { splitImageRefs, imageMarker } from './image-placeholder'
import { OWN_TEXT_SOURCES, isImagePath } from './image-sniff'
import type { PanelTurn } from './panel-resolve'
import { isKnownItem, type AgentTextItem, type StepItem, type Turn, type UserItem } from './types'

/** The fold key that makes a far-off turn of the deck draw its agent text in full (the unit is the real markdown's text, not the plain source). */
export const fullTurnKey = (turnId: string): string => `deck-full:${turnId}`

export const userUnitId = (id: string): string => `u:${id}`
export const agentUnitId = (id: string): string => `a:${id}`
export const stepUnitId = (id: string): string => `s:${id}`

/**
 * The text of a message the way UserText draws it: an image reference that names an image file is a thumbnail (no text of its own), any
 * other stays as the marker it was. Only the person's own messages are drawn that way (UserBlock / UserBubble decide).
 */
export function userText(item: UserItem): string {
  if (item.source === 'bash') return `! ${cleanCommandText(item.text)}`
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

/** The deck's units, in the order it draws them. `eager`: how many of the newest turns are always drawn in full (the older ones need `fullTurnKey` for their prose). */
export function deckUnits(turns: readonly Turn[], eager: number): SearchUnit[] {
  const out: SearchUnit[] = []
  turns.forEach((turn, i) => {
    const far = i < turns.length - eager
    for (const item of turn.items) {
      if (!isKnownItem(item)) continue
      if (item.type === 'user') {
        if (item.source !== 'command_output') add(out, userUnitId(item.id), userText(item))
      } else if (item.type === 'agent_text') {
        const md = (item as AgentTextItem).markdown
        add(out, agentUnitId(item.id), proseText(md), far ? [fullTurnKey(turn.id)] : [])
      } else if (item.type === 'step') {
        const subject = stepSubject(item)
        if (subject !== null) add(out, stepUnitId(item.id), subject)
      }
    }
  })
  return out
}

/** The chat's units, in the order it draws them. */
export function chatUnits(turns: readonly PanelTurn[]): SearchUnit[] {
  const out: SearchUnit[] = []
  for (const entry of buildChat(turns as PanelTurn[])) {
    if (entry.kind === 'user') { if (entry.item.source !== 'command_output') add(out, userUnitId(entry.item.id), userText(entry.item)) }
    else if (entry.kind === 'agent') add(out, agentUnitId(entry.item.id), proseText(entry.item.markdown))
  }
  return out
}
