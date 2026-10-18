// Invented characters only: the user's own personas.json is private and never enters the repo (spec §4.1).
import type { Persona, PersonaCollection } from '../persona-types'

export function makePersona(code: string, over: Partial<Persona> = {}): Persona {
  return {
    code,
    name: code.charAt(0).toUpperCase() + code.slice(1),
    sex: '',
    lead: false,
    retired: false,
    nationality: { code: 'XX', zh: '測試國', en: 'Testland' },
    template: { name: 'Tester', name_zh: '測試員', field_zh: '測試', field_en: 'testing' },
    personality: { zh: '冷靜', en: 'calm' },
    method: { zh: '先寫測試', en: 'tests first' },
    interests: { zh: '茶', en: 'tea' },
    avatar: { source: 'shapes', seed: code },
    avatar_data: '',
    updated_at: 1760000000000,
    ...over,
  }
}

export function makeCollection(
  codes: string[], stamp: { at: number; host: string } = { at: 1, host: 'h1' }, revision = 1,
): PersonaCollection {
  return { items: codes.map((c) => makePersona(c)), stamp, revision }
}
