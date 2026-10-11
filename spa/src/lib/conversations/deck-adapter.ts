// spa/src/lib/conversations/deck-adapter.ts — what the deck's input (SessionInput) needs from the back-end the conversation runs on (U4-2): how a
// message is sent and interrupted (the send queue's port), how a file is saved so the agent can read it, and where the typed text is kept. The
// terminal session is one back-end (`terminal-adapter`), a worker (a Nexen execution) another (`worker-adapter`); a later one (K-2: typing into
// Claude Code) is one more adapter, not another input.
//
// An adapter is BOUND to the input's key (`draftKey(pane, host, session)`, the key every memory of the input already uses) by whoever mounts
// the pane; the input, the send queue and the draft memory look it up by that key, and an unbound key is a terminal session. The registry is
// module state like the memories it sits beside (the tab-hosted rule: the input unmounts on a tab switch) and is freed with them (pane-release).
import type { SendPort } from './send'

/** Saves `file` where the agent can read it; the path goes into the draft. `onProgress` may never be called (a back-end with no progress). */
export type UploadFn = (file: File, o: { signal: AbortSignal; onProgress: (percent: number) => void }) => Promise<{ path: string }>

/** Where the typed text lives, when it is not the deck's own memory (a worker's draft is the worker's: the room view shares it). */
export interface DraftStore {
  read(): string | undefined
  /** An empty text forgets. */
  write(text: string): void
  forget(): void
}

export interface DeckAdapter {
  kind: 'terminal' | 'worker'
  /** The send queue's port: submit (with its client_msg_id) and interrupt. Called when the queue is made. */
  port(): SendPort
  /** Null: this back-end takes no attachments (no paperclip, a drop or a paste of files does nothing). */
  upload: UploadFn | null
  /** Absent: the deck's own draft memory. */
  draft?: DraftStore
}

const bound = new Map<string, DeckAdapter>()

/** The input of `key` runs on `adapter` (idempotent: a re-render binds the same one again). */
export function bindAdapter(key: string, adapter: DeckAdapter): void { bound.set(key, adapter) }

export const adapterOf = (key: string): DeckAdapter | undefined => bound.get(key)

/** Forget the bindings whose key matches (a pane that is gone, or a session it no longer shows). */
export function forgetAdaptersWhere(match: (key: string) => boolean): void {
  for (const key of [...bound.keys()]) if (match(key)) bound.delete(key)
}

/** Tests only: module state outlives a test. */
export const clearAllAdapters = (): void => { bound.clear() }
