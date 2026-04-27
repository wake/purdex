import { getModules } from '../module-registry'
import {
  registerFileOpener,
  unregisterByOwner,
} from '../file-opener-registry'
import { useModuleEnabledStore } from '../../stores/useModuleEnabledStore'

/**
 * Reconcile the file-opener registry with `ModuleDefinition.fileOpeners`.
 *
 * For every registered module the helper is authoritative: it owns every
 * entry whose `ownerModuleId` matches a module id, and the module's current
 * declaration is the only input that decides what those entries should be.
 *
 *   - First, drop the module's existing entries via `unregisterByOwner(m.id)`
 *     unconditionally. This guarantees that swapping a definition from
 *     `fileOpeners: [opener]` to one without `fileOpeners` (HMR, runtime
 *     mutation, plugin removal, etc.) cannot leave a stale opener behind.
 *   - Then re-register each opener in `m.fileOpeners` with
 *     `ownerModuleId = m.id`, unless the module is `disableable` and
 *     currently disabled (in which case the entries simply stay gone).
 *
 * Owners not in the module registry (plugin hosts, future external sources)
 * are out of scope and left untouched.
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
