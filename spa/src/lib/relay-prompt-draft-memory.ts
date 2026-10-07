// spa/src/lib/relay-prompt-draft-memory.ts — the unsaved text of a relay prompt editor on Hosts › 接力 (plan v3
// P9a-3), kept outside the component. The Hosts page is a tab and `hosts` is not a light kind, so under the default
// keepAliveCount 0 a tab switch unmounts the section, and so does a Hosts sub-page switch; a component's own state
// would start over. Memory only, like `nex/worker-draft-memory`: a reload starts from the stored text. Keyed
// `${hostId}:${kind}`. An emptied box is a draft too (`""`), unlike a reply box: it saves as the default.

const drafts = new Map<string, string>()

export const relayPromptDraftKey = (hostId: string, kind: string) => `${hostId}:${kind}`

export function readRelayPromptDraft(key: string): string | undefined {
  return drafts.get(key)
}

export function writeRelayPromptDraft(key: string, text: string): void {
  drafts.set(key, text)
}

/** After a successful save or 還原預設, or when the box is back to the stored text. */
export function forgetRelayPromptDraft(key: string): void {
  drafts.delete(key)
}

/** Tests only: module state outlives a test. */
export function clearAllRelayPromptDrafts(): void {
  drafts.clear()
}
