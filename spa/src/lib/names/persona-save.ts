// spa/src/lib/names/persona-save.ts — saves the persona collection the page edited (name pool v2 spec §4.2, §9.2).
// The page edits the collection it shows, the one with the highest stamp, so it is written back to the host that holds
// it, under that host's revision. The daemon stamps the write and the other daemons converge within about 30 s (§4.3).
import { putPersonas, PersonaConflictError } from './persona-api'
import { PERSONA_LIMITS, validatePersona, type PersonaIssue } from './persona-limits'
import type { Persona, PersonaCollection } from './persona-types'
import { currentSource, usePersonaStore } from '../../stores/usePersonaStore'

export class PersonaValidationError extends Error {
  readonly issues: PersonaIssue[]
  constructor(issues: PersonaIssue[]) {
    super(issues.map((i) => `${i.field}: ${i.message}`).join('; '))
    this.name = 'PersonaValidationError'
    this.issues = issues
  }
}

function collectionIssues(items: Persona[]): PersonaIssue[] {
  const out: PersonaIssue[] = []
  if (items.length > PERSONA_LIMITS.count) out.push({ field: 'count', message: `at most ${PERSONA_LIMITS.count} personas` })
  const seen = new Set<string>()
  for (const p of items) {
    if (seen.has(p.code)) out.push({ field: 'code', message: `code ${p.code} is used twice` })
    seen.add(p.code)
    for (const issue of validatePersona(p)) out.push({ field: `${p.code}.${issue.field}`, message: issue.message })
  }
  return out
}

/**
 * `fallbackHostId` is where a first collection goes when no host has one yet. A conflict (the host's copy moved) is
 * rethrown after the store re-read that host, so the page shows the newer copy and the user decides again.
 */
export async function savePersonas(items: Persona[], fallbackHostId: string): Promise<{ hostId: string }> {
  const issues = collectionIssues(items)
  if (issues.length > 0) throw new PersonaValidationError(issues)
  const source: { hostId: string; collection: PersonaCollection | null } =
    currentSource(usePersonaStore.getState().byHost) ?? { hostId: fallbackHostId, collection: null }
  try {
    await putPersonas(source.hostId, items, source.collection?.revision ?? 0)
  } catch (e) {
    if (e instanceof PersonaConflictError) await usePersonaStore.getState().load(source.hostId)
    throw e
  }
  await usePersonaStore.getState().load(source.hostId)
  return { hostId: source.hostId }
}
