// Transactional "set tray visibility" — pure logic, no `electron` import, so
// it is unit-testable. The IPC layer (tray-ipc.ts) wires the real deps.
//
// Ordering is save-first: the prefs file is the source of truth for the next
// launch, so if it cannot be written we must not touch the runtime tray —
// otherwise the UI (which rolls back on a rejected IPC) and the actual menu
// bar would disagree. Only once the intent is durable do we create/destroy.
import type { AppPrefs } from './app-prefs'

export interface TrayVisibilityDeps {
  isVisible: () => boolean
  /** true = the tray actually exists afterwards (createTray returned non-null). */
  create: () => boolean
  destroy: () => void
  loadPrefs: () => AppPrefs
  /** May throw (disk full, permissions, ...). */
  savePrefs: (p: AppPrefs) => void
}

/**
 * Returns the visibility that is actually in effect afterwards.
 * If savePrefs throws, the error is rethrown as-is and the runtime tray is
 * left untouched.
 */
export function applyTrayVisibility(requested: boolean, deps: TrayVisibilityDeps): boolean {
  // 1. Persist intent first. Always — even when the runtime already matches —
  //    so a drifted prefs file gets reconciled (idempotent).
  deps.savePrefs({ ...deps.loadPrefs(), showTray: requested })

  // 2. Apply to the runtime.
  if (!requested) {
    if (deps.isVisible()) deps.destroy()
    return false
  }
  if (deps.isVisible()) return true
  if (deps.create()) return true

  // 3. Creation failed (icon missing, ...): the tray is not there, so do not
  //    leave `showTray: true` on disk — the UI would show "on" for a tray that
  //    does not exist. If even this write fails, the worst case is one more
  //    doomed attempt on the next launch, which is acceptable.
  try {
    deps.savePrefs({ ...deps.loadPrefs(), showTray: false })
  } catch (err) {
    console.warn('[tray] could not roll back showTray after failed create:', err)
  }
  return false
}
