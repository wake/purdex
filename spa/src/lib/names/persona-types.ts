// spa/src/lib/names/persona-types.ts — the persona collection's wire shape (name pool v2 spec §4.1). The daemon
// checks the limits; the App only reads and writes the shape.

export interface PersonaText { zh: string; en: string }

export interface PersonaAvatarRecipe {
  /** A DiceBear style id, or a photo source id; opaque to the daemon (≤ 2 KB in all). */
  source: string
  seed?: string
  options?: Record<string, unknown>
  url?: string
}

export interface Persona {
  code: string
  name: string
  sex: 'male' | 'female' | ''
  lead: boolean
  retired: boolean
  nationality: { code: string; zh: string; en: string }
  template: { name: string; name_zh: string; field_zh: string; field_en: string }
  personality: PersonaText
  method: PersonaText
  interests: PersonaText
  avatar: PersonaAvatarRecipe
  /** `data:image/(webp|png|jpeg);base64,…`, ≤ 32 KB as a string; `""` means the App draws initials. */
  avatar_data: string
  updated_at: number
}

/** Last-writer-wins stamp: `at` first, then `host` (spec §4.3). */
export interface PersonaStamp { at: number; host: string }

export interface PersonaCollection {
  items: Persona[]
  stamp: PersonaStamp
  revision: number
}
