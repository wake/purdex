import { getModules } from '../module-registry'
import {
  registerFileOpener,
  unregisterByOwner,
} from '../file-opener-registry'
import { useModuleEnabledStore } from '../../stores/useModuleEnabledStore'

/**
 * Reconcile the file-opener registry with the current module + enable state.
 *
 * For every registered module:
 *   - Drop any existing entries owned by the module (so re-applying after a
 *     module mutation never duplicates or strands openers).
 *   - Skip the module if it has no `fileOpeners`.
 *   - Skip the module if it is `disableable` and currently disabled.
 *   - Otherwise register each opener with `ownerModuleId = m.id`.
 *
 * Idempotent: calling twice in a row produces the same registry state.
 */
export function applyModuleFileOpeners(): void {
  const isEnabled = useModuleEnabledStore.getState().isEnabled
  for (const m of getModules()) {
    unregisterByOwner(m.id)
    if (!m.fileOpeners || m.fileOpeners.length === 0) continue
    if (m.disableable && !isEnabled(m.id)) continue
    for (const spec of m.fileOpeners) {
      registerFileOpener({ ...spec, ownerModuleId: m.id })
    }
  }
}
