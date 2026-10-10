// spa/src/lib/conversations/dock-memory.ts — what the reader has typed into an open question card (U3 plan D8): the picked
// options, the 「其他」 text, and the reply in words. The dock unmounts with its tab, and the question is still open when the
// reader comes back, so this lives outside the component, keyed by pane and approval (a card is bound to its approval id).
// Memory only; a card that closes forgets its entry.
import type { Pick } from './asks'

export interface DockDraft {
  picks: Pick[]
  /** The card is in 「改成跟 agent 聊聊」 mode. */
  replying: boolean
  reply: string
}

const drafts = new Map<string, DockDraft>()

export const dockKey = (paneKey: string, approvalId: string): string => `${paneKey}\0${approvalId}`

export const emptyDraft = (questions: number): DockDraft => ({ picks: Array.from({ length: questions }, () => ({ chosen: [], other: '' })), replying: false, reply: '' })

export function readDockDraft(key: string, questions: number): DockDraft {
  const d = drafts.get(key)
  if (!d) return emptyDraft(questions)
  // a card whose question count changed is another question: its draft does not fit
  return d.picks.length === questions ? d : emptyDraft(questions)
}

export function writeDockDraft(key: string, draft: DockDraft): void {
  drafts.set(key, draft)
}

export function forgetDockDraft(key: string): void {
  drafts.delete(key)
}

/** Forgets every draft of a pane (it closed). */
export function forgetDockDraftsOfPane(paneKey: string): void {
  for (const key of [...drafts.keys()]) if (key.startsWith(`${paneKey}\0`)) drafts.delete(key)
}
