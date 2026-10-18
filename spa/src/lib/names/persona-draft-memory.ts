// spa/src/lib/names/persona-draft-memory.ts — the persona form's unsaved draft, kept outside the component. A settings
// page unmounts when its tab is switched away (`useTabAlivePool` keeps nothing alive by default), and a component's own
// `useState` would lose what the user was typing; this holds it for the page to restore (CLAUDE.md, tab-hosted checklist).
// Memory only: a draft does not survive a reload, and it is never sent anywhere.
import type { EditBase } from './persona-save'
import type { Persona } from './persona-types'

export interface PersonaDraft {
  /** The persona's code, or `''` for a persona that is being created. */
  key: string
  draft: Persona
  /** `editBase()` as it was when the form was opened. */
  base: EditBase | null
}

let held: PersonaDraft | null = null

function copyOf(d: PersonaDraft): PersonaDraft {
  return structuredClone(d)
}

export function rememberDraft(d: PersonaDraft): void {
  held = copyOf(d)
}

export function recallDraft(): PersonaDraft | null {
  return held ? copyOf(held) : null
}

export function forgetDraft(): void {
  held = null
}

/** Test-only. */
export function clearDraftMemory(): void {
  held = null
}
