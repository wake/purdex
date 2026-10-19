// spa/src/lib/names/persona-save-report.ts — what the persona page tells the user after a save: the note for a save that
// went through and the message for one that did not. Pulled out of the page so a save from the import says the same words.
import { hostLabel, hostLookOf } from '../host-look'
import { PersonaApiError, PersonaConflictError } from './persona-api'
import { PersonaStaleError, PersonaValidationError, type savePersonas } from './persona-save'

type T = (key: string, vars?: Record<string, string | number>) => string

/** The host's name as the rest of the App shows it (`lib/host-look.ts`; a screen never reads `host.name` itself). */
const label = (id: string) => hostLabel(id, hostLookOf(id))

/**
 * What reached the other hosts is part of the answer: a copy that failed is waiting for that host's next connect, and a
 * role taken into the source from another host changed what the user is looking at.
 */
export function savedNote(t: T, res: Awaited<ReturnType<typeof savePersonas>>): string {
  const parts = [t('settings.personas.saved', { host: label(res.hostId) })]
  if (res.mirror.failed.length > 0) parts.push(t('settings.personas.mirrorFailed', { hosts: res.mirror.failed.map((f) => label(f.hostId)).join('、') }))
  if (res.mirror.pulled.length > 0) parts.push(t('settings.personas.pulled', { codes: res.mirror.pulled.join(', ') }))
  return parts.join(' ')
}

/** The words for a save that failed, or `null` for a stale or conflicting one, which the caller handles itself. */
export function saveErrorMessage(t: T, e: unknown): string | null {
  if (e instanceof PersonaStaleError || e instanceof PersonaConflictError) return null
  if (e instanceof PersonaValidationError) {
    return [t('settings.personas.error.invalid'), ...e.issues.map((i) => `${i.field}: ${i.message}`)].join(' ')
  }
  if (e instanceof PersonaApiError) {
    const row = e.rowIndex()
    return row === null
      ? t('settings.personas.error.whole', { message: e.message })
      : t('settings.personas.error.row', { field: e.field ?? String(row), message: e.message })
  }
  return t('settings.personas.error.whole', { message: e instanceof Error ? e.message : String(e) })
}
