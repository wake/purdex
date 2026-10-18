// spa/src/lib/names/persona-limits.ts — the limits the daemon checks on a persona (name pool v2 spec §4.1), mirrored so
// the form can show counters and refuse before a PUT does. The daemon stays the authority.
import type { Persona } from './persona-types'

export const PERSONA_LIMITS = {
  name: 24,
  personality: 50,
  method: 50,
  interests: 10,
  /** `avatar_data` as a string. */
  avatarData: 32 * 1024,
  /** The avatar recipe serialised. */
  recipe: 2 * 1024,
  /** Personas in a collection, retired ones included. */
  count: 64,
} as const

export const CODE_RULE = /^[a-z]{3,12}$/
const AVATAR_DATA = /^data:image\/(webp|png|jpeg);base64,([A-Za-z0-9+/]+={0,2})$/

/** The first bytes the declared type must start with (the daemon checks the magic bytes, spec §4.1). */
function magicMatches(type: string, base64: string): boolean {
  let head: string
  try { head = atob(base64.slice(0, 16)) } catch { return false }
  if (type === 'png') return head.startsWith('\x89PNG')
  if (type === 'jpeg') return head.startsWith('\xff\xd8\xff')
  return head.startsWith('RIFF') && head.slice(8, 12) === 'WEBP'
}

function avatarDataOk(data: string): boolean {
  if (data.length > PERSONA_LIMITS.avatarData) return false
  const m = AVATAR_DATA.exec(data)
  return m !== null && magicMatches(m[1], m[2])
}

export interface PersonaIssue { field: string; message: string }

/** zh counts runes; en counts whitespace-separated words (spec §4.1: "runes / words"). */
export function countUnits(text: string, lang: 'zh' | 'en'): number {
  if (lang === 'zh') return Array.from(text).length
  return text.split(/\s+/).filter(Boolean).length
}

function printable(s: string): boolean {
  // eslint-disable-next-line no-control-regex
  return !/[\u0000-\u001f\u007f-\u009f]/.test(s)
}

export function validatePersona(p: Persona): PersonaIssue[] {
  const out: PersonaIssue[] = []
  if (!CODE_RULE.test(p.code)) out.push({ field: 'code', message: 'code must be 3-12 lowercase letters' })
  const name = p.name
  const nameLen = Array.from(name.trim()).length
  if (name !== name.trim() || nameLen < 1 || nameLen > PERSONA_LIMITS.name || !printable(name)) {
    out.push({ field: 'name', message: `name must be 1-${PERSONA_LIMITS.name} printable characters` })
  }
  for (const key of ['personality', 'method', 'interests'] as const) {
    for (const lang of ['zh', 'en'] as const) {
      if (countUnits(p[key][lang], lang) > PERSONA_LIMITS[key]) {
        out.push({ field: `${key}.${lang}`, message: `${key} (${lang}) is over ${PERSONA_LIMITS[key]}` })
      }
    }
  }
  if (p.avatar_data !== '' && !avatarDataOk(p.avatar_data)) {
    out.push({ field: 'avatar_data', message: 'avatar image must be a webp/png/jpeg data URL of at most 32 KB' })
  }
  if (new TextEncoder().encode(JSON.stringify(p.avatar)).length > PERSONA_LIMITS.recipe) {
    out.push({ field: 'avatar', message: 'avatar recipe is over 2 KB' })
  }
  return out
}
