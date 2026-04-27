import { getModules } from '../module-registry'
import {
  registerFileOpener,
  unregisterByOwner,
} from '../file-opener-registry'
import { useModuleEnabledStore } from '../../stores/useModuleEnabledStore'

/**
 * Reconcile the file-opener registry with `ModuleDefinition.fileOpeners`.
 *
 * For every module that declares `fileOpeners`:
 *   - Drop the module's existing entries via `unregisterByOwner(m.id)` so
 *     repeated applies don't duplicate or strand openers.
 *   - Skip the module if it is `disableable` and currently disabled.
 *   - Otherwise register each opener with `ownerModuleId = m.id`.
 *
 * Modules that DO NOT declare `fileOpeners` are left untouched — including
 * any entries they may have registered through a different code path (e.g.
 * the transitional `registerEditorFileOpeners()` until Task 1.3 promotes
 * Editor's openers into `editorModuleDefinition.fileOpeners`). The helper's
 * sole responsibility is to keep the registry in sync with the declarative
 * field; it must never speak for owners that haven't opted in.
 *
 * Idempotent: calling twice in a row produces the same registry state.
 */
export function applyModuleFileOpeners(): void {
  const isEnabled = useModuleEnabledStore.getState().isEnabled
  for (const m of getModules()) {
    if (!m.fileOpeners || m.fileOpeners.length === 0) continue
    unregisterByOwner(m.id)
    if (m.disableable && !isEnabled(m.id)) continue
    for (const spec of m.fileOpeners) {
      registerFileOpener({ ...spec, ownerModuleId: m.id })
    }
  }
}
