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
import { selectPersonasV1, useNexHostStore } from '../../../stores/useNexHostStore'
import { currentCollection, currentSource, usePersonaStore } from '../../../stores/usePersonaStore'
import { useSourceId } from '../../../stores/usePersonaSourceStore'
import {
  beginSave, endSave, forgetDraft, getOutcome, recallDraft, rememberDraft, savePending, setOutcome, subscribeSave,
} from '../../../lib/names/persona-draft-memory'
import { PERSONA_LIMITS, validatePersona, type PersonaIssue } from '../../../lib/names/persona-limits'
import { editBase, savePersonas, type EditBase } from '../../../lib/names/persona-save'
import { saveErrorMessage, savedNote } from '../../../lib/names/persona-save-report'
import type { Persona } from '../../../lib/names/persona-types'
import { forgetAvatarOptionsText } from '../../../lib/names/avatar-options-memory'
import { AvatarPicker } from './AvatarPicker'
import { FirstSaveConfirm } from './FirstSaveConfirm'
import { PersonaCredits } from './PersonaCredits'
import { PersonaForm } from './PersonaForm'
import { PersonaImport } from './PersonaImport'
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
  const [pickerEpoch, setPickerEpoch] = useState(0)
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
  const avatarBusy = useRef(false)
  const setAvatarBusy = (busy: boolean) => { avatarBusy.current = busy }
  const setAvatar = (avatar: Persona['avatar'], avatarData: string) => {
    const cur = editingNow.current
    if (cur) hold({ ...cur, draft: { ...cur.draft, avatar, avatar_data: avatarData } })
  }

  const open = (key: string) => {
    const p = key === '' ? blankPersona() : items.find((x) => x.code === key)
    if (!p) return
    setIssues([]); setOutcome({ note: null, error: null }); setConfirm(null)
    // Every new persona has the key '', so React would keep one picker (and its source, seed and options) for the next
    // new persona too; a new epoch makes each opening a fresh picker, and the half-typed options of the last are dropped.
    forgetAvatarOptionsText(key)
    setPickerEpoch((n) => n + 1)
    avatarBusy.current = false // the picker that was drawing is gone with its draw
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
    // The draft now comes from elsewhere, with its own avatar recipe: the picker holds the source, seed and options it
    // was opened with, so it is opened afresh on the new one.
    setPickerEpoch((n) => n + 1)
    setOutcome({ note: t('settings.personas.rebuilt') })
  }

  const save = async (draft: Persona, confirmed = false) => {
    if (!editing || savePending()) return // read straight from the slot: a double submit can beat the next render
    // A change to the avatar is still being drawn: what the draft holds is the PREVIOUS recipe with the PREVIOUS image.
    // Saving that and letting the draw land afterwards would leave an unsaved draft under a "saved" note.
    if (avatarBusy.current) { setOutcome({ note: t('settings.personas.avatar.busy'), error: null }); return }
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
      setOutcome({ note: savedNote(t, res) })
    } catch (e) {
      const message = saveErrorMessage(t, e)
      if (message === null) keepTyped(editing.key, draft) // stale or lost the compare-and-set
      else setOutcome({ error: message })
    } finally {
      endSave()
    }
  }

  return (
    <div data-testid="personas-section">
      <h2 className="text-lg text-text-primary">{t('settings.section.personas')}</h2>
      <p className="text-xs text-text-secondary">{t('settings.personas.description')}</p>

      <div className="mt-3 flex flex-wrap items-center justify-end gap-2">
        <PersonaImport items={items} fallbackHostId={fallbackHostId} disabled={unsupported || saving || readOnly} />
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
              avatarPicker={<AvatarPicker key={`${editing.key}#${pickerEpoch}`} slot={editing.key} recipe={editing.draft.avatar} disabled={saving} onChange={setAvatar} onBusyChange={setAvatarBusy} />}
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
      <PersonaCredits />
    </div>
  )
}
