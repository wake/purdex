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
}

export function listContributions<S extends SettingsScope>(
  scope: S,
): Array<SettingsContribution<S>> {
  const out: Array<SettingsContribution<S>> = []
  for (const c of contributions.values()) {
    if (c.scope === scope) out.push(c as unknown as SettingsContribution<S>)
  }
  out.sort((a, b) => a.order - b.order)
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
  contributions.clear()
}
