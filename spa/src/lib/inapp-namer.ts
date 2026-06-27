/**
 * inapp-namer — the single entry point for "create a new uniquely-named file"
 * in the In-App storage. All three new-file UI affordances (the Storage pane,
 * the editor "new buffer" popover, and the empty-editor new-tab section)
 * converge here so the eager-reservation model and the #854 race fix live in
 * exactly one place.
 *
 * The atomic uniqueness guarantee is provided by `InAppBackend.createUnique`
 * (IDB `store.add`); this wrapper only resolves the backend and forwards the
 * bare extension (`'md' | 'txt'`, no leading dot — the backend forms the path).
 */
import { getFsBackend } from './fs-backend'

/**
 * Reserve a new unique empty `Untitled[-N].<ext>` file under `dir` and return
 * its full path. Throws if the In-App backend is unavailable so callers can
 * surface the failure instead of silently creating nothing.
 */
export async function createUniqueInAppFile(dir: string, ext: 'md' | 'txt'): Promise<string> {
  const backend = getFsBackend({ type: 'inapp' })
  if (!backend) throw new Error('InApp backend unavailable')
  return backend.createUnique(dir, 'Untitled', ext)
}
