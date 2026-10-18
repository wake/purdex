// spa/src/lib/conversations/draft-memory.ts — the text typed in a session pane's input, kept outside the component (the
// tab-hosted rule: a pane the alive pool does not keep unmounts on a tab switch, and component state would start empty).
// Memory only, like the scroll memo: a reload starts empty. Keyed by the pane (the caller's pane key).

import { adapterOf } from './deck-adapter'
import { rebaseAnchors } from './insertion-anchor'

const drafts = new Map<string, string>()

/** A draft belongs to a pane AND the session it is bound to: a pane re-pointed at another session must not carry the text over. */
export const draftKey = (paneKey: string, hostId: string, sessionId: string): string => `${paneKey}|${hostId}|${sessionId}`

/** A bound adapter that keeps its own draft (a worker's) answers for its key; every other key is this memory's. */
export const readDraft = (key: string): string | undefined => { const own = adapterOf(key)?.draft; return own ? own.read() : drafts.get(key) }

/** Every change of the box lands here; an empty box forgets (a send ends up here too). */
/** `caret`: the box's own caret after a typed or pasted change (it tells the insertion points where the edit was). */
export function writeDraft(key: string, text: string, caret?: number): void {
  rebaseAnchors(key, readDraft(key) ?? '', text, caret) // the insertion points of the uploads on their way follow the edit
  const own = adapterOf(key)?.draft
  if (own) { own.write(text); return }
  if (text === '') drafts.delete(key)
  else drafts.set(key, text)
}

export const forgetDraft = (key: string): void => { const own = adapterOf(key)?.draft; if (own) own.forget(); else drafts.delete(key) }

/** Forget every draft whose key matches (a pane that is gone, or a session it no longer shows). */
export function forgetDraftsWhere(match: (key: string) => boolean): void {
  for (const key of [...drafts.keys()]) if (match(key)) drafts.delete(key)
}

/** Tests only: module state outlives a test. */
export const clearAllDrafts = (): void => { drafts.clear() }
