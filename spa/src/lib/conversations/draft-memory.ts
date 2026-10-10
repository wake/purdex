// spa/src/lib/conversations/draft-memory.ts — the text typed in a session pane's input, kept outside the component (the
// tab-hosted rule: a pane the alive pool does not keep unmounts on a tab switch, and component state would start empty).
// Memory only, like the scroll memo: a reload starts empty. Keyed by the pane (the caller's pane key).

const drafts = new Map<string, string>()

export const readDraft = (key: string): string | undefined => drafts.get(key)

/** Every change of the box lands here; an empty box forgets (a send ends up here too). */
export function writeDraft(key: string, text: string): void {
  if (text === '') drafts.delete(key)
  else drafts.set(key, text)
}

export const forgetDraft = (key: string): void => { drafts.delete(key) }

/** Tests only: module state outlives a test. */
export const clearAllDrafts = (): void => { drafts.clear() }
