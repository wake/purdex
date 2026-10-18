// spa/src/lib/names/persona-draft-memory.ts — the persona form's unsaved draft, kept outside the component. A settings
// page unmounts when its tab is switched away (`useTabAlivePool` keeps nothing alive by default), and a component's own
// `useState` would lose what the user was typing; this holds it for the page to restore (CLAUDE.md, tab-hosted checklist).
// Memory only: a draft does not survive a reload, and it is never sent anywhere. One slot is enough: the Personas page
// is a section of the global settings tab, which opens as a singleton (`openSingletonTab`), so a window has one at a time.
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

// A save in flight is held here, not in the component, for the same reason as the draft: choosing another settings
// section unmounts the page, and coming back builds a new instance whose own flag would read "nothing pending" while the
// request is still out. Whoever mounts reads `savePending()` and stays locked until `endSave()`.
let pending = false
const listeners = new Set<() => void>()

function notify(): void {
  for (const fn of [...listeners]) fn()
}

export function savePending(): boolean {
  return pending
}

/** Claims the one save slot; `false` when a save is already in flight. */
export function beginSave(): boolean {
  if (pending) return false
  pending = true
  notify()
  return true
}

export function endSave(): void {
  if (!pending) return
  pending = false
  notify()
}

export function subscribeSave(fn: () => void): () => void {
  listeners.add(fn)
  return () => { listeners.delete(fn) }
}

// What the last save said, held with the lock for the same reason: a save that finishes while the page is away reports
// its result (saved, kept your input, failed) to whoever is mounted when it ends. A component's own state would drop it.
// `getOutcome` returns the same object until something changes, as `useSyncExternalStore` needs.
export interface SaveOutcome {
  note: string | null
  error: string | null
}

let outcome: SaveOutcome = { note: null, error: null }

export function getOutcome(): SaveOutcome {
  return outcome
}

export function setOutcome(next: Partial<SaveOutcome>): void {
  outcome = { ...outcome, ...next }
  notify()
}

/** Test-only. */
export function clearDraftMemory(): void {
  held = null
  pending = false
  outcome = { note: null, error: null }
}
