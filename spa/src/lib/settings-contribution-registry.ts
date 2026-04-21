import type {
  AnySettingsContribution,
  SettingsContribution,
  SettingsScope,
} from './settings-contribution-types'

const LOCAL_ID_RE = /^[a-zA-Z][a-zA-Z0-9_-]*$/

const contributions = new Map<string, AnySettingsContribution>()

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
  if (!LOCAL_ID_RE.test(def.localId)) {
    throw new Error(
      `settings-contribution-registry: localId "${def.localId}" is invalid; must match ${LOCAL_ID_RE.source}`,
    )
  }
}

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

export function clearContributions(): void {
  contributions.clear()
}
