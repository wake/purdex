import { useEditorStore } from '../stores/useEditorStore'
import { usePlaceholderFilesStore } from '../stores/usePlaceholderFilesStore'
import { getFsBackend } from './fs-backend'
import type { FileSource } from '../types/fs'

/**
 * Detach a pane from its editor buffer and, if that buffer was the last thing
 * holding an untouched placeholder open, remove the placeholder file (T5.2).
 *
 * This is the only path in the app that deletes a user file without the user
 * asking, so its shape is deliberate on three points:
 *
 * 1. **The close happens first.** `closePane` removes THIS pane's `paneState`
 *    (and, when it was the last reference, the buffer). Only afterwards is the
 *    sweep decided. Asking "is anyone still holding this buffer?" BEFORE the
 *    close would always find the pane that is in the middle of closing, and the
 *    sweep would never fire at all.
 * 2. **The deciding condition is the reference count, not the unmount.**
 *    `EditorPane`'s cleanup runs on any unmount of that leaf — pane moves and
 *    content swaps included — and in those cases another pane still references
 *    the same buffer. `pane-move.ts` attaches the destination pane BEFORE the
 *    source unmounts precisely so the count never dips, which is what makes the
 *    post-close check correct there: a moved buffer still has a live reference
 *    at step 2 and is therefore not swept.
 * 3. **The fact comes from the registry, never from the buffer's shape.** An
 *    untouched reservation and a file the user deliberately saved empty look
 *    identical in the store (see `usePlaceholderFilesStore`), so only an entry
 *    minted at reservation time and dropped on the first save / rename / delete
 *    may authorize this. Non-in-app sources can never be registered, and the
 *    registry lookup below re-checks the source anyway — remote and local files
 *    are never eligible.
 * 4. **The registry's claim is confirmed against the disk before deleting.**
 *    Every other way an entry ends — a save, a rename, a move, a delete, an
 *    upload, the external-change probe — is an EVENT we happen to observe, and
 *    there is no file watcher: a placeholder written to behind our back while
 *    its tab is never re-activated produces no event at all, and its entry
 *    survives as a standing authorization over a file that now holds someone's
 *    content. So the last thing before the delete is a `stat`, and only a file
 *    that is STILL 0 bytes is removed. This is not the rejected "infer from
 *    buffer shape" predicate, which guesses at history and cannot tell an
 *    untouched reservation from a file the user deliberately emptied and saved;
 *    it is a present-tense fact about the disk, required on top of the registry
 *    entry rather than instead of it. A `stat` that fails — the file is gone, or
 *    the backend errored — deletes nothing either: unconfirmable is not empty.
 *
 * Failure is swallowed: this is housekeeping running inside an unmount cleanup,
 * where a throw would break the teardown, and a file that survives is still
 * reachable through the manual "clear empty files" action (T4.2). The registry
 * entry is dropped either way — one automatic attempt per placeholder, then the
 * file is treated as ordinary.
 */
export function closePaneAndSweepPlaceholder(
  paneId: string,
  key: string,
  source: FileSource,
  filePath: string,
): void {
  useEditorStore.getState().closePane(paneId, key)

  // Defence in depth: the registry already refuses non-in-app sources, but this
  // branch ends in a delete, so the rule is restated at the point of no return —
  // a change on the registry side must not be able to reach a remote file.
  if (source.type !== 'inapp') return
  if (!usePlaceholderFilesStore.getState().isPlaceholder(source, filePath)) return
  const stillOpen = Object.values(useEditorStore.getState().paneStates)
    .some((paneState) => paneState.bufferKey === key)
  if (stillOpen) return

  usePlaceholderFilesStore.getState().unregister(source, filePath)

  const backend = getFsBackend(source)
  if (!backend) return
  try {
    void backend.stat(filePath)
      .then((stat) => {
        // Point 4: the registry's claim is confirmed against the disk before the
        // delete. Anything but a still-empty file — content written behind our
        // back, or a `stat` we cannot complete — leaves the file alone. The
        // entry was already dropped above, so a surviving file is simply
        // ordinary from here on.
        if (stat.size !== 0) return
        void backend.delete(filePath).catch(() => {})
      })
      .catch(() => {})
  } catch {
    // best-effort housekeeping — see the note above
  }
}
