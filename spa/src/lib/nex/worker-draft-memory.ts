// spa/src/lib/nex/worker-draft-memory.ts — the text typed in a worker pane's
// reply box, kept outside the component. A tab the alive pool does not keep
// (keepAliveCount 0 is the default; an execution pane is not a "light" kind)
// unmounts when the reader switches away, and a remount (or a view switch
// re-keying the pane) would otherwise start with an empty box. Memory only,
// like the scroll memo: a reload starts empty. Keyed `${hostId}:${executionId}`
// so a handoff that swaps a pane's execution does not carry the text across.

const drafts = new Map<string, string>()

export const workerDraftKey = (hostId: string, executionId: string) => `${hostId}:${executionId}`

export function readWorkerDraft(key: string): string | undefined {
  return drafts.get(key)
}

/** Every change of the box lands here; an empty box forgets (a successful send ends up here too). */
export function writeWorkerDraft(key: string, text: string): void {
  if (text === '') drafts.delete(key)
  else drafts.set(key, text)
}

export function forgetWorkerDraft(key: string): void {
  drafts.delete(key)
}

/** Tests only: module state outlives a test, and a leaked draft seeds the next one's reply box. */
export function clearAllWorkerDrafts(): void {
  drafts.clear()
}
