import { useSyncExternalStore } from 'react'
import type {
  AnySettingsContribution,
  SettingsContribution,
  SettingsScope,
} from './settings-contribution-types'
import { SETTINGS_LOCAL_ID_RE } from './settings-contribution-types'

const contributions = new Map<string, AnySettingsContribution>()

/**
 * Validate a prepared contribution. Shared by the dispatch pass and the
 * direct `registerSettingsContribution` path.
 *
 * @internal
 *   Contribution registration has a single public entry point — the
 *   `ModuleDefinition.settings` declaration flushed by
 *   `dispatchSettingsContributions()`. The validator is wired inside that
 *   path and is not part of the public surface. See #539.
 */
export function assertValidSettingsContribution(def: AnySettingsContribution): void {
  if (!def.moduleId) {
    throw new Error('settings-contribution-registry: moduleId must be a non-empty string')
  }
  if (!def.localId) {
    throw new Error('settings-contribution-registry: localId must be a non-empty string')
  }
  if (!def.id) {
    throw new Error('settings-contribution-registry: id must be a non-empty string')
  }
  const expectedId = `${def.moduleId}.${def.localId}`
  if (def.id !== expectedId) {
    throw new Error(
      `settings-contribution-registry: id "${def.id}" does not match "${expectedId}" (moduleId.localId)`,
    )
  }
  if (!SETTINGS_LOCAL_ID_RE.test(def.localId)) {
    throw new Error(
      `settings-contribution-registry: localId "${def.localId}" is invalid; ` +
        `must match ${SETTINGS_LOCAL_ID_RE.source} ` +
        `(lowercase ASCII, digits, hyphen; 1-32 chars — same grammar as parseRoute)`,
    )
  }
}

/**
 * Insert a fully-formed `SettingsContribution` into the registry.
 *
 * @internal
 *   The only supported public write path is declaring
 *   `settings: [...]` on a `ModuleDefinition` (for module-authored
 *   contributions) or calling `registerSettingsSection()` (for the legacy
 *   adapter) and letting `dispatchSettingsContributions()` flush. Direct
 *   calls from outside `dispatch-settings-contributions.ts` /
 *   `settings-section-registry.ts` / test files are considered internal
 *   and subject to removal without notice. See #539.
 */
export function registerSettingsContribution(def: AnySettingsContribution): void {
  assertValidSettingsContribution(def)

  const existing = contributions.get(def.id)
  if (existing !== undefined) {
    if (existing === def) {
      // Idempotent: same object reference (e.g. HMR / double-import). Silent skip.
      return
    }
    throw new Error(
      `settings-contribution-registry: duplicate contribution id "${def.id}"`,
    )
  }

  contributions.set(def.id, def)
  wireVisibility(def)
}

// === Visibility that moves while the shell is open ===
//
// `visible()` is read when the shell renders, and a change made elsewhere — another window's write arriving as a
// rehydrate — renders nothing here. So a contribution may say WHEN to look again (`subscribeVisibility`), and the
// shell subscribes ONCE, to all of them, through `useContributionVisibility()`. Wired only while somebody
// listens; a contribution registered meanwhile (a re-dispatch) is wired too, and `clearContributions()` lets go
// of every one — so nothing of a cleared registry keeps a store subscription alive.
const visibilityListeners = new Set<() => void>()
const visibilityStops = new Map<string, () => void>()
let visibilityVersion = 0

function visibilityMoved(): void {
  visibilityVersion += 1
  for (const listener of [...visibilityListeners]) listener()
}

function wireVisibility(def: AnySettingsContribution): void {
  if (def.subscribeVisibility === undefined || visibilityListeners.size === 0 || visibilityStops.has(def.id)) return
  visibilityStops.set(def.id, def.subscribeVisibility(visibilityMoved))
}

function unwireVisibility(): void {
  for (const stop of visibilityStops.values()) stop()
  visibilityStops.clear()
}

function subscribeVisibility(listener: () => void): () => void {
  visibilityListeners.add(listener)
  for (const def of contributions.values()) wireVisibility(def)
  return () => {
    visibilityListeners.delete(listener)
    if (visibilityListeners.size === 0) unwireVisibility()
  }
}

/** For the shell that lists contributions: re-renders it whenever a contribution says its visibility may have moved. */
export function useContributionVisibility(): void {
  useSyncExternalStore(subscribeVisibility, () => visibilityVersion, () => visibilityVersion)
}

/** `visible()` absent → listed; false, or throwing → not (a section that cannot say is not shown). */
function isVisible(c: AnySettingsContribution): boolean {
  if (c.visible === undefined) return true
  try {
    return c.visible() === true
  } catch {
    return false
  }
}

export function listContributions<S extends SettingsScope>(
  scope: S,
): Array<SettingsContribution<S>> {
  const out: Array<SettingsContribution<S>> = []
  for (const c of contributions.values()) {
    if (c.scope === scope && isVisible(c)) out.push(c as unknown as SettingsContribution<S>)
  }
  // Spec §I2 (modules-switchboard sidebar alignment) — deterministic
  // comparator: (order, moduleId, localId). Centralized here so every
  // consumer (SettingsSidebar, GlobalSettingsPage default-mount logic,
  // tests) sees the exact same ordering. Without the moduleId/localId
  // tie-break, two contributions sharing an `order` would be returned in
  // insertion order — which the sidebar then re-sorts deterministically,
  // letting the visible row order diverge from the auto-mounted default
  // section.
  out.sort((a, b) => {
    if (a.order !== b.order) return a.order - b.order
    if (a.moduleId !== b.moduleId) return a.moduleId.localeCompare(b.moduleId)
    return a.localId.localeCompare(b.localId)
  })
  return out
}

export function getContribution(id: string): AnySettingsContribution | undefined {
  return contributions.get(id)
}

/**
 * Clear all registered contributions. Used by (a) the dispatch pass as the
 * first step of each flush and (b) the HMR dispose hook in
 * `register-modules.tsx`. Tests may also call it for isolation.
 *
 * @internal
 *   Not for production consumer code. See #539.
 */
export function clearContributions(): void {
  unwireVisibility()
  contributions.clear()
}
