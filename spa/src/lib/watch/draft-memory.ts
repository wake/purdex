// spa/src/lib/watch/draft-memory.ts — the text typed into a 設定 field and not sent yet, kept outside the component (CLAUDE.md
// tab-hosted checklist). The host tab unmounts when the reader switches away (keepAliveCount 0), so a draft in `useState` would be
// gone on return. Memory only, like lib/nex/worker-draft-memory.ts; keyed App host id + daemon identity + field. A host id does not
// name one daemon for good (a re-point keeps the id and changes the daemon), so the key carries the daemon id too, and a re-point or
// a removal forgets the host's drafts outright (`forgetWatchHost`). A field forgets its draft once the write ended (sent, refused with
// a 400, or reloaded after a 409) or the box was put back to the stored value.

const drafts = new Map<string, string>()

export const watchDraftKey = (hostId: string, daemonId: string, field: string): string => `${hostId}\n${daemonId}\n${field}`
export const readWatchDraft = (key: string): string | undefined => drafts.get(key)
export const writeWatchDraft = (key: string, text: string): void => { drafts.set(key, text) }
export const forgetWatchDraft = (key: string): void => { drafts.delete(key) }
/** Every draft of one App host (its endpoint changed, or it was removed). */
export function forgetWatchHost(hostId: string): void {
  const prefix = `${hostId}\n`
  for (const k of [...drafts.keys()]) if (k.startsWith(prefix)) drafts.delete(k)
}
/** Tests only. */
export const clearWatchDrafts = (): void => { drafts.clear() }
