// spa/src/lib/names/persona-import.ts — importing a `personas.json` (name pool v2 spec §4.1): `id` becomes `code`, the
// rest keeps its names. The file is the user's own data; it is read from a file the user picks and never bundled.
import { CODE_RULE, PERSONA_LIMITS, validatePersona } from './persona-limits'
import type { Persona } from './persona-types'
import { canonical } from './persona-hash'

export type SkipReason = 'code' | 'duplicate' | 'invalid' | 'full'

export interface SkippedEntry { index: number; code: string; reason: SkipReason; detail?: string }

export interface ImportPlan {
  added: Persona[]
  changed: Persona[]
  unchanged: number
  skipped: SkippedEntry[]
  /**
   * Codes whose image must be drawn again from the recipe now in `result` (the file's recipe is new or differs).
   * Their `avatar_data` is still the old image until the page has redrawn it; each counts as changed or added.
   */
  redraw: string[]
  /** The whole collection after the import, in the order: existing ones, then the added. */
  result: Persona[]
}

type Raw = Record<string, unknown>

/** The list of entries in a file: a bare array, or an object holding `items` or `personas`. */
export function parsePersonasJson(text: string): Raw[] {
  const doc: unknown = JSON.parse(text)
  const list = Array.isArray(doc) ? doc : (doc as { items?: unknown; personas?: unknown } | null)?.items ?? (doc as { personas?: unknown } | null)?.personas
  if (!Array.isArray(list)) throw new Error('not a list of personas')
  return list.filter((e): e is Raw => typeof e === 'object' && e !== null && !Array.isArray(e))
}

function str(v: unknown): string { return typeof v === 'string' ? v : '' }
function obj(v: unknown): Raw { return typeof v === 'object' && v !== null ? (v as Raw) : {} }
/** A nested object field by field: a key the file leaves out keeps the existing value (else empty). */
function mix<K extends string>(r: Raw, existing: Record<K, string> | undefined, keys: readonly K[]): Record<K, string> {
  return Object.fromEntries(keys.map((k) => [k, k in r ? str(r[k]) : existing?.[k] ?? ''])) as Record<K, string>
}

/**
 * A file entry as a Persona. A key the entry leaves out keeps the existing persona's value, so a partial or older file
 * never blanks what is already there; the stored avatar and the retired mark are always the existing ones.
 */
function fromRaw(r: Raw, existing: Persona | undefined, now: number): Persona {
  const has = (k: string) => k in r
  const sex = str(r.sex)
  const nat = obj(r.nationality)
  const tpl = obj(r.template)
  return {
    code: str(r.id) || str(r.code),
    name: has('name') ? str(r.name).trim() : existing?.name ?? '',
    sex: has('sex') ? (sex === 'male' || sex === 'female' ? sex : '') : existing?.sex ?? '',
    lead: has('lead') ? r.lead === true : existing?.lead ?? false,
    retired: existing?.retired ?? r.retired === true,
    nationality: mix(nat, existing?.nationality, ['code', 'zh', 'en']),
    template: mix(tpl, existing?.template, ['name', 'name_zh', 'field_zh', 'field_en']),
    personality: mix(obj(r.personality), existing?.personality, ['zh', 'en']),
    method: mix(obj(r.method), existing?.method, ['zh', 'en']),
    interests: mix(obj(r.interests), existing?.interests, ['zh', 'en']),
    avatar: fileRecipe(r) ?? existing?.avatar ?? { source: '' },
    avatar_data: existing?.avatar_data ?? '',
    updated_at: now,
  }
}

/** The recipe an entry carries, or `undefined` when it has none. */
function fileRecipe(r: Raw): Persona['avatar'] | undefined {
  return typeof r.avatar === 'object' && r.avatar !== null && !Array.isArray(r.avatar) ? (r.avatar as Persona['avatar']) : undefined
}

function sameContent(a: Persona, b: Persona): boolean {
  return canonical({ ...a, updated_at: 0 }) === canonical({ ...b, updated_at: 0 })
}

export function planImport(existing: Persona[], entries: Raw[], now = Date.now()): ImportPlan {
  const byCode = new Map(existing.map((p) => [p.code, p]))
  const seen = new Set<string>()
  const plan: ImportPlan = { added: [], changed: [], unchanged: 0, skipped: [], redraw: [], result: [...existing] }
  entries.forEach((r, index) => {
    const code = str(r.id) || str(r.code)
    if (!CODE_RULE.test(code)) { plan.skipped.push({ index, code, reason: 'code' }); return }
    if (seen.has(code)) { plan.skipped.push({ index, code, reason: 'duplicate' }); return }
    seen.add(code)
    const before = byCode.get(code)
    const next = fromRaw(r, before, now)
    const issue = validatePersona(next)[0]
    if (issue) { plan.skipped.push({ index, code, reason: 'invalid', detail: issue.field }); return }
    // A recipe in the file that differs from the stored one is drawn again; a persona without one shows initials.
    const recipe = fileRecipe(r)
    const redraw = recipe !== undefined && canonical(recipe) !== canonical(before?.avatar)
    if (!before) {
      if (plan.result.length >= PERSONA_LIMITS.count) { plan.skipped.push({ index, code, reason: 'full' }); return }
      plan.added.push(next)
      plan.result.push(next)
      if (redraw) plan.redraw.push(code)
    } else if (sameContent(before, next)) {
      plan.unchanged++
    } else {
      plan.changed.push(next)
      plan.result[plan.result.findIndex((p) => p.code === code)] = next
      if (redraw) plan.redraw.push(code)
    }
  })
  return plan
}
