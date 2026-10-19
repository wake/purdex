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
import { currentCollection, currentSource, usePersonaStore } from '../../../stores/usePersonaStore'
import { useSourceId } from '../../../stores/usePersonaSourceStore'
import { PersonaApiError, PersonaConflictError } from '../../../lib/names/persona-api'
import {
  beginSave, endSave, forgetDraft, getOutcome, recallDraft, rememberDraft, savePending, setOutcome, subscribeSave,
} from '../../../lib/names/persona-draft-memory'
import { PERSONA_LIMITS, validatePersona, type PersonaIssue } from '../../../lib/names/persona-limits'
import { editBase, PersonaStaleError, PersonaValidationError, savePersonas, type EditBase } from '../../../lib/names/persona-save'
import type { Persona } from '../../../lib/names/persona-types'
import { AvatarPicker } from './AvatarPicker'
import { FirstSaveConfirm } from './FirstSaveConfirm'
import { PersonaForm } from './PersonaForm'
import { PersonaList } from './PersonaList'
import { PersonaSourcePicker } from './PersonaSourcePicker'

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
  // The source is offline and another host's copy stands in: it is shown, not edited. A save from it would go to that
  // host as if it were the source, and the source's own copy would overwrite it (or be overwritten) when it returns.
  const readOnly = usePersonaStore((s) => currentSource(s.byHost, sourceId)?.readOnly ?? false)
  const nex = useNexHostStore((s) => s.byHost)
  const hostOrder = useHostStore((s) => s.hostOrder)
  const [editing, setEditing] = useState<Editing | null>(() => recallDraft())
  const [issues, setIssues] = useState<PersonaIssue[]>([])
  // The first persona on a source that has none renames every live conversation; the user is asked once, here.
  const [confirm, setConfirm] = useState<{ hostId: string } | null>(null)
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

  // Where a first collection would go: the source when it serves personas, else the first host that says it does.
  const fallbackHostId = useMemo(() => {
    const serves = (id: string) => (nex[id]?.fetchedAt ?? 0) > 0 && selectPersonasV1(id)({ byHost: nex })
    return (sourceId !== null && serves(sourceId) ? sourceId : hostOrder.find(serves)) ?? null
  }, [sourceId, hostOrder, nex])
  const items = collection?.items ?? []
  const unsupported = collection === null && fallbackHostId === null
  // Retired personas keep their codes for good (L5), so they count towards the daemon's 64.
  const atCap = items.length >= PERSONA_LIMITS.count

  const hold = (next: Editing | null) => {
    setEditing(next)
    if (next) rememberDraft(next)
    else forgetDraft()
  }

  // A draw finishes later than the click that asked for it, so the avatar is put into the draft as it is NOW (typing in
  // another field meanwhile must survive), not into the one the picker saw when it started.
  const editingNow = useRef(editing)
  editingNow.current = editing
  const setAvatar = (avatar: Persona['avatar'], avatarData: string) => {
    const cur = editingNow.current
    if (cur) hold({ ...cur, draft: { ...cur.draft, avatar, avatar_data: avatarData } })
  }

  const open = (key: string) => {
    const p = key === '' ? blankPersona() : items.find((x) => x.code === key)
    if (!p) return
    setIssues([]); setOutcome({ note: null, error: null }); setConfirm(null)
    hold({ key, draft: structuredClone(p), base: editBase() })
  }

  // A notice asked about one source; another source (the user changed it, or the old one dropped) is another question.
  useEffect(() => { setConfirm(null) }, [sourceId])

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

  const save = async (draft: Persona, confirmed = false) => {
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
    // The first persona on a source that has none: the daemon starts naming its live conversations after personas. Asked
    // once, and always: the number shown comes from the inventory of session rows, but the daemon renames every live
    // conversation, including ones without a session row, so even a count of 0 does not prove nobody is renamed.
    // Nothing is awaited here: the lock below is taken in the same turn as the click, so the form and the source cannot
    // change under a save that is waiting.
    const target = editing.base?.hostId ?? fallbackHostId ?? ''
    if (!confirmed && items.length === 0 && next.length > 0 && target !== '') {
      setConfirm({ hostId: target })
      return
    }
    setConfirm(null)
    if (!beginSave()) return
    try {
      const res = await savePersonas(next, editing.base, fallbackHostId ?? '')
      if (res.base === null) { showShown(draft.code, stamped); return }
      hold({ key: draft.code, draft: stamped, base: res.base })
      // The host's name as the rest of the App shows it (`lib/host-look.ts`; a screen never reads `host.name` itself).
      const label = (id: string) => hostLabel(id, hostLookOf(id))
      // What reached the other hosts is part of the answer: a copy that failed is waiting for that host's next connect,
      // and a role taken into the source from another host changed what the user is looking at.
      const parts = [t('settings.personas.saved', { host: label(res.hostId) })]
      if (res.mirror.failed.length > 0) parts.push(t('settings.personas.mirrorFailed', { hosts: res.mirror.failed.map((f) => label(f.hostId)).join('、') }))
      if (res.mirror.pulled.length > 0) parts.push(t('settings.personas.pulled', { codes: res.mirror.pulled.join(', ') }))
      setOutcome({ note: parts.join(' ') })
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
        <button type="button" className={BTN} data-testid="persona-new" disabled={unsupported || atCap || saving || readOnly} onClick={() => open('')}>
          {t('settings.personas.new')}
        </button>
      </div>
      <PersonaSourcePicker sourceId={sourceId} offline={readOnly} disabled={saving} />
      {atCap && <p className="mt-2 text-xs text-text-secondary" data-testid="personas-cap">{t('settings.personas.cap')}</p>}
      {unsupported && <p className="mt-2 text-xs text-text-secondary" data-testid="personas-unsupported">{t('settings.personas.unsupported')}</p>}

      <div className="mt-3 grid grid-cols-[minmax(0,330px)_minmax(0,1fr)] gap-4">
        <div>
          {items.length === 0 && !unsupported && <p className="text-xs text-text-secondary">{t('settings.personas.empty')}</p>}
          <PersonaList items={items} selected={editing?.key ?? null} disabled={saving || readOnly} onSelect={open} />
        </div>
        <div>
          {editing && !readOnly ? (
            <PersonaForm
              draft={editing.draft}
              isNew={editing.key === ''}
              issues={issues}
              saving={saving}
              avatarPicker={<AvatarPicker key={editing.key} slot={editing.key} recipe={editing.draft.avatar} disabled={saving} onChange={setAvatar} />}
              onChange={(draft) => hold({ ...editing, draft })}
              onSave={() => void save(editing.draft)}
              onToggleRetired={() => void save({ ...editing.draft, retired: !editing.draft.retired })}
            />
          ) : (
            <p className="text-xs text-text-secondary">{t('settings.personas.form.pick')}</p>
          )}
          {confirm && editing && !readOnly && (
            <div className="mt-2">
              <FirstSaveConfirm hostId={confirm.hostId} onCancel={() => setConfirm(null)} onConfirm={() => void save(editing.draft, true)} />
            </div>
          )}
          {note && <p className="mt-2 text-xs text-text-secondary" data-testid="personas-note">{note}</p>}
          {error && <p className="mt-2 text-xs text-red-500" data-testid="personas-error">{error}</p>}
        </div>
      </div>
    </div>
  )
}
