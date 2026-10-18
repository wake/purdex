// spa/src/lib/names/persona-import.ts — importing a `personas.json` (name pool v2 spec §4.1): `id` becomes `code`, the
// rest keeps its names. The file is the user's own data; it is read from a file the user picks and never bundled.
import { CODE_RULE, PERSONA_LIMITS, validatePersona } from './persona-limits'
import type { Persona } from './persona-types'

export type SkipReason = 'code' | 'duplicate' | 'invalid' | 'full'

export interface SkippedEntry { index: number; code: string; reason: SkipReason; detail?: string }

export interface ImportPlan {
  added: Persona[]
  changed: Persona[]
  unchanged: number
  skipped: SkippedEntry[]
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
function text(v: unknown): { zh: string; en: string } { const o = obj(v); return { zh: str(o.zh), en: str(o.en) } }

/** A file entry as a Persona; the stored avatar and the retired mark are the existing persona's, when there is one. */
function fromRaw(r: Raw, existing: Persona | undefined, now: number): Persona {
  const sex = str(r.sex)
  const nat = obj(r.nationality)
  const tpl = obj(r.template)
  return {
    code: str(r.id) || str(r.code),
    name: str(r.name).trim(),
    sex: sex === 'male' || sex === 'female' ? sex : '',
    lead: r.lead === true,
    retired: existing?.retired ?? r.retired === true,
    nationality: { code: str(nat.code), zh: str(nat.zh), en: str(nat.en) },
    template: { name: str(tpl.name), name_zh: str(tpl.name_zh), field_zh: str(tpl.field_zh), field_en: str(tpl.field_en) },
    personality: text(r.personality),
    method: text(r.method),
    interests: text(r.interests),
    avatar: existing?.avatar ?? (typeof r.avatar === 'object' && r.avatar !== null ? (r.avatar as Persona['avatar']) : { source: '' }),
    avatar_data: existing?.avatar_data ?? '',
    updated_at: now,
  }
}

function sameContent(a: Persona, b: Persona): boolean {
  return JSON.stringify({ ...a, updated_at: 0 }) === JSON.stringify({ ...b, updated_at: 0 })
}

export function planImport(existing: Persona[], entries: Raw[], now = Date.now()): ImportPlan {
  const byCode = new Map(existing.map((p) => [p.code, p]))
  const seen = new Set<string>()
  const plan: ImportPlan = { added: [], changed: [], unchanged: 0, skipped: [], result: [...existing] }
  entries.forEach((r, index) => {
    const code = str(r.id) || str(r.code)
    if (!CODE_RULE.test(code)) { plan.skipped.push({ index, code, reason: 'code' }); return }
    if (seen.has(code)) { plan.skipped.push({ index, code, reason: 'duplicate' }); return }
    seen.add(code)
    const before = byCode.get(code)
    const next = fromRaw(r, before, now)
    const issue = validatePersona(next)[0]
    if (issue) { plan.skipped.push({ index, code, reason: 'invalid', detail: issue.field }); return }
    if (!before) {
      if (plan.result.length >= PERSONA_LIMITS.count) { plan.skipped.push({ index, code, reason: 'full' }); return }
      plan.added.push(next)
      plan.result.push(next)
    } else if (sameContent(before, next)) {
      plan.unchanged++
    } else {
      plan.changed.push(next)
      plan.result[plan.result.findIndex((p) => p.code === code)] = next
    }
  })
  return plan
}
