// spa/src/components/settings/personas/PersonaImport.tsx — import a `personas.json` (name pool v2 spec §4.1, plan NA-1b):
// the user picks the file, a dry run says what would change, and only then is anything drawn or saved. The file is the
// user's own data, read here and never bundled or sent anywhere but the save.
//
// Importing merges, never replaces: a persona the file does not mention stays, a retired one stays retired, a field the
// file leaves out keeps its value (`planImport`). The avatars the plan marks are drawn first, then the whole collection is
// saved through `savePersonas`, like any other save: against the base the plan was made on (a collection that moved since
// is refused, and the user picks the file again), mirrored to the other hosts, and with the first-save notice when the
// source has no persona yet. The page's save lock is held from the click on, so nothing else can be saved or edited
// while the images are being drawn.
import { useRef, useState } from 'react'
import { useI18nStore } from '../../../stores/useI18nStore'
import { beginSave, endSave, setOutcome } from '../../../lib/names/persona-draft-memory'
import { drawRecipe } from '../../../lib/names/persona-draw'
import { parsePersonasJson, planImport, type ImportPlan } from '../../../lib/names/persona-import'
import { redrawForImport } from '../../../lib/names/persona-import-run'
import { editBase, savePersonas, type EditBase } from '../../../lib/names/persona-save'
import { saveErrorMessage, savedNote } from '../../../lib/names/persona-save-report'
import type { Persona } from '../../../lib/names/persona-types'
import { FirstSaveConfirm } from './FirstSaveConfirm'

const BTN =
  'shrink-0 rounded-md border border-border-default px-2.5 py-1 text-xs text-text-secondary hover:text-text-primary hover:border-border-active cursor-pointer disabled:opacity-50 disabled:cursor-not-allowed'

interface Props {
  /** The collection the page shows: what the file is merged into. */
  items: Persona[]
  /** Where a first collection goes when no host holds one yet (see the page). */
  fallbackHostId: string | null
  disabled: boolean
}

interface Planned { plan: ImportPlan; base: EditBase | null }

function readText(file: File): Promise<string> {
  return new Promise((resolve, reject) => {
    const r = new FileReader()
    r.onload = () => resolve(String(r.result))
    r.onerror = () => reject(new Error('the file could not be read'))
    r.readAsText(file)
  })
}

export function PersonaImport({ items, fallbackHostId, disabled }: Props) {
  const t = useI18nStore((s) => s.t)
  const input = useRef<HTMLInputElement>(null)
  const [planned, setPlanned] = useState<Planned | null>(null)
  const [problem, setProblem] = useState<string | null>(null)
  const [confirmHost, setConfirmHost] = useState<string | null>(null)

  const close = () => { setPlanned(null); setConfirmHost(null) }

  const choose = async (file: File | undefined) => {
    if (input.current) input.current.value = '' // the same file can be picked again after a change
    if (!file) return
    setProblem(null); setConfirmHost(null)
    try {
      const entries = parsePersonasJson(await readText(file))
      // The base is taken now: a save from this plan is refused if the collection moves before it is confirmed.
      setPlanned({ plan: planImport(items, entries), base: editBase() })
    } catch {
      setPlanned(null)
      setProblem(t('settings.personas.import.notList'))
    }
  }

  const run = async (confirmed: boolean) => {
    if (!planned) return
    const { plan, base } = planned
    const target = base?.hostId ?? fallbackHostId ?? ''
    // The first persona on a source that has none renames its live conversations: asked first, as in the form's save.
    if (!confirmed && items.length === 0 && plan.result.length > 0 && target !== '') {
      setConfirmHost(target)
      return
    }
    setConfirmHost(null)
    if (!beginSave()) return
    setOutcome({ note: null, error: null })
    try {
      const { result, failed } = await redrawForImport(plan, items, drawRecipe)
      const res = await savePersonas(result, base, fallbackHostId ?? '')
      const parts = [savedNote(t, res)]
      if (failed.length > 0) parts.push(t('settings.personas.import.redrawFailed', { codes: failed.join(', ') }))
      setOutcome({ note: parts.join(' ') })
    } catch (e) {
      setOutcome({ error: saveErrorMessage(t, e) ?? t('settings.personas.import.stale') })
    } finally {
      endSave()
      close()
    }
  }

  const plan = planned?.plan
  const nothingToDo = plan !== undefined && plan.added.length + plan.changed.length === 0

  return (
    <>
      <button type="button" className={BTN} data-testid="persona-import" disabled={disabled} onClick={() => input.current?.click()}>
        {t('settings.personas.import.button')}
      </button>
      <input ref={input} type="file" accept="application/json,.json" className="hidden" data-testid="persona-import-file" disabled={disabled} onChange={(e) => void choose(e.target.files?.[0])} />
      {problem && <p className="basis-full text-xs text-red-500" data-testid="persona-import-problem">{problem}</p>}
      {plan && (
        <div className="basis-full rounded-md border border-border-default p-3 text-xs text-text-secondary" data-testid="persona-import-summary">
          <p className="text-text-primary">{t('settings.personas.import.title')}</p>
          <p className="mt-1" data-testid="persona-import-counts">
            {t('settings.personas.import.summary', { added: plan.added.length, changed: plan.changed.length, unchanged: plan.unchanged, skipped: plan.skipped.length })}
            {plan.redraw.length > 0 && ` ${t('settings.personas.import.redraw', { count: plan.redraw.length })}`}
          </p>
          {plan.skipped.length > 0 && (
            <ul className="mt-1" data-testid="persona-import-skipped">
              {plan.skipped.map((s) => (
                <li key={`${s.index}-${s.code}`}>{`${s.code || `#${s.index + 1}`}: ${t(`settings.personas.import.skip.${s.reason}`)}${s.detail ? ` (${s.detail})` : ''}`}</li>
              ))}
            </ul>
          )}
          {nothingToDo && <p className="mt-1" data-testid="persona-import-nothing">{t('settings.personas.import.nothing')}</p>}
          {confirmHost !== null && (
            <div className="mt-2">
              <FirstSaveConfirm hostId={confirmHost} onCancel={() => setConfirmHost(null)} onConfirm={() => void run(true)} />
            </div>
          )}
          <div className="mt-2 flex justify-end gap-2">
            <button type="button" className={BTN} data-testid="persona-import-cancel" onClick={close}>{t('settings.personas.import.cancel')}</button>
            <button type="button" className={BTN} data-testid="persona-import-confirm" disabled={nothingToDo || disabled} onClick={() => void run(false)}>
              {t('settings.personas.import.confirm')}
            </button>
          </div>
        </div>
      )}
    </>
  )
}
