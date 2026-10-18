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

/** What the form was opened on: the host that held the shown collection, with its revision and stamp. */
export interface EditBase { hostId: string; revision: number; stamp: PersonaCollection['stamp'] }

/** The page takes this when it opens a form; `null` when no host has a collection yet. */
export function editBase(): EditBase | null {
  const src = currentSource(usePersonaStore.getState().byHost)
  return src && { hostId: src.hostId, revision: src.collection.revision, stamp: src.collection.stamp }
}

/** The collection the form was built on is no longer the shown one: another host's newer copy, or a newer revision. */
export class PersonaStaleError extends Error {
  constructor() {
    super('personas changed since the form was opened')
    this.name = 'PersonaStaleError'
  }
}

function sameBase(a: EditBase | null, b: EditBase | null): boolean {
  if (a === null || b === null) return a === b
  return a.hostId === b.hostId && a.revision === b.revision && a.stamp.at === b.stamp.at && a.stamp.host === b.stamp.host
}

/**
 * `base` is `editBase()` as it was when the form opened. If the shown collection is not that one any more, the items
 * were built on something that has been replaced: a write under the new revision would pass the CAS and silently drop
 * the newer edits, so nothing is sent. `fallbackHostId` is where a first collection goes when no host has one yet.
 * A conflict (the host's copy moved) is rethrown after the store re-read that host.
 */
export async function savePersonas(
  items: Persona[], base: EditBase | null, fallbackHostId: string,
): Promise<{ hostId: string; base: EditBase | null }> {
  const issues = collectionIssues(items)
  if (issues.length > 0) throw new PersonaValidationError(issues)
  if (!sameBase(base, editBase())) throw new PersonaStaleError()
  const hostId = base?.hostId ?? fallbackHostId
  try {
    await putPersonas(hostId, items, base?.revision ?? 0)
  } catch (e) {
    if (e instanceof PersonaConflictError) await usePersonaStore.getState().load(hostId)
    throw e
  }
  await usePersonaStore.getState().load(hostId)
  // The reload moved the revision and stamp; a form that stays open continues from this base. It is the saved host's
  // own, and only while that host's collection is still the shown one: when another host's newer copy won meanwhile,
  // the open form's items were built on the old one, so there is no base to continue from and the page rebuilds it.
  const after = editBase()
  return { hostId, base: after?.hostId === hostId ? after : null }
}
