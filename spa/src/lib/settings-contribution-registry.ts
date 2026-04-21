import type {
  SettingsContribution,
  SettingsScope,
} from './settings-contribution-types'

const LOCAL_ID_RE = /^[a-zA-Z][a-zA-Z0-9_-]*$/

const contributions = new Map<string, SettingsContribution>()

export function assertValidSettingsContribution(def: SettingsContribution): void {
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

export function registerSettingsContribution(def: SettingsContribution): void {
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

export function listContributions(scope: SettingsScope): SettingsContribution[] {
  const out: SettingsContribution[] = []
  for (const c of contributions.values()) {
    if (c.scope === scope) out.push(c)
  }
  out.sort((a, b) => a.order - b.order)
  return out
}

export function getContribution(id: string): SettingsContribution | undefined {
  return contributions.get(id)
}

export function clearContributions(): void {
  contributions.clear()
}
