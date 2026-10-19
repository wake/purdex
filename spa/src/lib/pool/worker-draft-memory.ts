// spa/src/lib/pool/worker-draft-memory.ts — what the reader typed into the worker settings and has not sent (the owner-rule value; the 「暫停」
// form joins it in 7b1-5b), kept outside the component: the host tab unmounts when the reader switches away (CLAUDE.md tab-hosted checklist,
// `useTabAlivePool` keepAliveCount 0). Memory only, like lib/watch/draft-memory.ts. Keyed by pool host alias and field.
const drafts = new Map<string, string>()

export const workerDraftKey = (alias: string, field: string): string => `${alias}\n${field}`
export const readWorkerDraft = (key: string): string | undefined => drafts.get(key)
export const writeWorkerDraft = (key: string, text: string): void => { drafts.set(key, text) }
export const forgetWorkerDraft = (key: string): void => { drafts.delete(key) }
/** Tests only. */
export const clearWorkerDrafts = (): void => { drafts.clear() }
