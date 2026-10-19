// spa/src/components/settings/personas/PersonasSection.tsx — Settings › Personas (name pool v2 spec §9.1): the list on the
// left, one persona's form on the right. It edits the collection the App shows (the highest stamp), saves it whole on the
// host that holds it, and says where it went. A persona is retired, never deleted (L5).
//
// The form edits a DRAFT held in `persona-draft-memory`, so what is half typed survives the settings tab being switched
// away. Each draft remembers the `editBase()` it was opened on; `savePersonas` refuses a save whose base is no longer the
// shown collection, and a save that comes back with no base (another copy won meanwhile) rebuilds the form from the shown
// collection instead of carrying the old items forward.
import { useEffect, useMemo, useRef, useState, useSyncExternalStore } from 'react'
import { useI18nStore } from '../../../stores/useI18nStore'
import { useHostStore } from '../../../stores/useHostStore'
import { hostLabel, hostLookOf } from '../../../lib/host-look'
import { selectPersonasV1, useNexHostStore } from '../../../stores/useNexHostStore'
import { currentCollection, usePersonaStore } from '../../../stores/usePersonaStore'
import { useSourceId } from '../../../stores/usePersonaSourceStore'
import { PersonaApiError, PersonaConflictError } from '../../../lib/names/persona-api'
import {
  beginSave, endSave, forgetDraft, getOutcome, recallDraft, rememberDraft, savePending, setOutcome, subscribeSave,
} from '../../../lib/names/persona-draft-memory'
import { PERSONA_LIMITS, validatePersona, type PersonaIssue } from '../../../lib/names/persona-limits'
import { editBase, PersonaStaleError, PersonaValidationError, savePersonas, type EditBase } from '../../../lib/names/persona-save'
import type { Persona } from '../../../lib/names/persona-types'
import { PersonaForm } from './PersonaForm'
import { PersonaList } from './PersonaList'

const BTN =
  'shrink-0 rounded-md border border-border-default px-2.5 py-1 text-xs text-text-secondary hover:text-text-primary hover:border-border-active cursor-pointer disabled:opacity-50 disabled:cursor-not-allowed'

function blankPersona(): Persona {
  return {
    code: '', name: '', sex: '', lead: false, retired: false,
    nationality: { code: '', zh: '', en: '' },
    template: { name: '', name_zh: '', field_zh: '', field_en: '' },
    personality: { zh: '', en: '' }, method: { zh: '', en: '' }, interests: { zh: '', en: '' },
    avatar: { source: '' }, avatar_data: '', updated_at: 0,
  }
}

/** The issue fields the form draws a message on: code, name, the three bilingual texts, the nationality texts. */
const FORM_FIELDS = /^(code|name|(personality|method|interests)\.(zh|en)|nationality\.(code|zh|en))$/

interface Editing {
  /** The persona's code; `''` while a new one is being made. */
  key: string
  draft: Persona
  base: EditBase | null
}

export function PersonasSection() {
  const t = useI18nStore((s) => s.t)
  const sourceId = useSourceId()
  const collection = usePersonaStore((s) => currentCollection(s.byHost, sourceId))
  const nex = useNexHostStore((s) => s.byHost)
  const hostOrder = useHostStore((s) => s.hostOrder)
  const [editing, setEditing] = useState<Editing | null>(() => recallDraft())
  const [issues, setIssues] = useState<PersonaIssue[]>([])
  // The note and the error of the last save live outside the component with the lock: a save that ends while this page
  // is away reports to whichever instance is mounted when it does.
  const { note, error } = useSyncExternalStore(subscribeSave, getOutcome)
  // Held outside the component (see persona-draft-memory): a save that is still out when the page is left and re-entered
  // keeps this instance locked too.
  const saving = useSyncExternalStore(subscribeSave, savePending)

  // A save that ended while this instance was unmounted wrote its result to the draft memory; take it from there.
  const wasSaving = useRef(saving)
  useEffect(() => {
    if (wasSaving.current && !saving) setEditing(recallDraft())
    wasSaving.current = saving
  }, [saving])

  // Where a first collection would go: the first host that says it serves personas.
  const fallbackHostId = useMemo(
    () => hostOrder.find((id) => (nex[id]?.fetchedAt ?? 0) > 0 && selectPersonasV1(id)({ byHost: nex })) ?? null,
    [hostOrder, nex],
  )
  const items = collection?.items ?? []
  const unsupported = collection === null && fallbackHostId === null
  // Retired personas keep their codes for good (L5), so they count towards the daemon's 64.
  const atCap = items.length >= PERSONA_LIMITS.count

  const hold = (next: Editing | null) => {
    setEditing(next)
    if (next) rememberDraft(next)
    else forgetDraft()
  }

  const open = (key: string) => {
    const p = key === '' ? blankPersona() : items.find((x) => x.code === key)
    if (!p) return
    setIssues([]); setOutcome({ note: null, error: null })
    hold({ key, draft: structuredClone(p), base: editBase() })
  }

  /**
   * A save came back stale, or lost the compare-and-set: another copy of the collection is now the shown one. What the
   * user typed is KEPT and moved onto the new base; the next save then writes it over this persona, while every other
   * persona stays at the shown (newer) collection. A persona that is not in the shown collection is one being created,
   * so it goes on as a new one. Nothing typed is thrown away, and the note says what saving again will do.
   */
  const keepTyped = (key: string, typed: Persona) => {
    const stored = usePersonaStore.getState().current()?.items.some((x) => x.code === key)
    hold({ key: stored ? key : '', draft: typed, base: editBase() })
    setOutcome({ note: t('settings.personas.kept') })
  }

  /** A save went through, but the shown collection is no longer the one it wrote: show what is shown now. */
  const showShown = (key: string, saved: Persona) => {
    const now = usePersonaStore.getState().current()?.items.find((x) => x.code === key)
    hold(now ? { key, draft: structuredClone(now), base: editBase() } : { key: '', draft: saved, base: editBase() })
    setOutcome({ note: t('settings.personas.rebuilt') })
  }

  const save = async (draft: Persona) => {
    if (!editing || savePending()) return // read straight from the slot: a double submit can beat the next render
    const found = validatePersona(draft)
    if (editing.key === '' && items.some((p) => p.code === draft.code)) found.push({ field: 'code', message: draft.code })
    setIssues(found)
    setOutcome({ note: null, error: null })
    if (found.length > 0) {
      // Fields the form has no input for (template texts, the avatar) cannot be marked in red, so they are named here.
      const hidden = found.filter((i) => !FORM_FIELDS.test(i.field)).map((i) => `${i.field}: ${i.message}`)
      setOutcome({ error: [t('settings.personas.error.invalid'), ...hidden].join(' ') })
      return
    }
    const stamped = { ...draft, updated_at: Date.now() }
    const next = editing.key === '' ? [...items, stamped] : items.map((p) => (p.code === editing.key ? stamped : p))
    if (!beginSave()) return
    try {
      const res = await savePersonas(next, editing.base, fallbackHostId ?? '')
      if (res.base === null) { showShown(draft.code, stamped); return }
      hold({ key: draft.code, draft: stamped, base: res.base })
      // The host's name as the rest of the App shows it (`lib/host-look.ts`; a screen never reads `host.name` itself).
      setOutcome({ note: t('settings.personas.saved', { host: hostLabel(res.hostId, hostLookOf(res.hostId)) }) })
    } catch (e) {
      if (e instanceof PersonaStaleError || e instanceof PersonaConflictError) keepTyped(editing.key, draft)
      else if (e instanceof PersonaValidationError) {
        setOutcome({ error: [t('settings.personas.error.invalid'), ...e.issues.map((i) => `${i.field}: ${i.message}`)].join(' ') })
      } else if (e instanceof PersonaApiError) {
        const row = e.rowIndex()
        setOutcome({
          error: row === null
            ? t('settings.personas.error.whole', { message: e.message })
            : t('settings.personas.error.row', { field: e.field ?? String(row), message: e.message }),
        })
      } else setOutcome({ error: t('settings.personas.error.whole', { message: e instanceof Error ? e.message : String(e) }) })
    } finally {
      endSave()
    }
  }

  return (
    <div data-testid="personas-section">
      <h2 className="text-lg text-text-primary">{t('settings.section.personas')}</h2>
      <p className="text-xs text-text-secondary">{t('settings.personas.description')}</p>

      <div className="mt-3 flex items-center justify-end gap-2">
        <button type="button" className={BTN} data-testid="persona-new" disabled={unsupported || atCap || saving} onClick={() => open('')}>
          {t('settings.personas.new')}
        </button>
      </div>
      {atCap && <p className="mt-2 text-xs text-text-secondary" data-testid="personas-cap">{t('settings.personas.cap')}</p>}
      {unsupported && <p className="mt-2 text-xs text-text-secondary" data-testid="personas-unsupported">{t('settings.personas.unsupported')}</p>}

      <div className="mt-3 grid grid-cols-[minmax(0,330px)_minmax(0,1fr)] gap-4">
        <div>
          {items.length === 0 && !unsupported && <p className="text-xs text-text-secondary">{t('settings.personas.empty')}</p>}
          <PersonaList items={items} selected={editing?.key ?? null} disabled={saving} onSelect={open} />
        </div>
        <div>
          {editing ? (
            <PersonaForm
              draft={editing.draft}
              isNew={editing.key === ''}
              issues={issues}
              saving={saving}
              onChange={(draft) => hold({ ...editing, draft })}
              onSave={() => void save(editing.draft)}
              onToggleRetired={() => void save({ ...editing.draft, retired: !editing.draft.retired })}
            />
          ) : (
            <p className="text-xs text-text-secondary">{t('settings.personas.form.pick')}</p>
          )}
          {note && <p className="mt-2 text-xs text-text-secondary" data-testid="personas-note">{note}</p>}
          {error && <p className="mt-2 text-xs text-red-500" data-testid="personas-error">{error}</p>}
        </div>
      </div>
    </div>
  )
}
