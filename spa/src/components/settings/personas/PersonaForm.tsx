// spa/src/components/settings/personas/PersonaForm.tsx — the right side of Settings › Personas: one persona's fields,
// each with a counter against the daemon's limit (name pool v2 spec §4.1). It edits a draft the page owns and writes
// nothing. There is no delete: a persona is retired, never removed (L5). The avatar picker is its own block (NA-1b-2c).
import type { ReactNode } from 'react'
import { Avatar } from '../../Avatar'
import { useI18nStore } from '../../../stores/useI18nStore'
import { personaColor } from '../../../lib/names/persona-color'
import { countUnits, PERSONA_LIMITS, type PersonaIssue } from '../../../lib/names/persona-limits'
import type { Persona, PersonaText } from '../../../lib/names/persona-types'

interface Props {
  draft: Persona
  /** A persona being created: its code is still editable. Once saved the code is fixed. */
  isNew: boolean
  issues: PersonaIssue[]
  saving: boolean
  /** Rendered between the preview and the text fields (the avatar picker). */
  avatarPicker?: ReactNode
  onChange: (next: Persona) => void
  onSave: () => void
  onToggleRetired: () => void
}

const INPUT =
  'w-full rounded border border-border-default bg-surface-secondary px-2 py-1 text-sm text-text-primary disabled:opacity-60'
const BTN =
  'shrink-0 rounded-md border border-border-default px-3 py-1 text-xs text-text-secondary hover:text-text-primary hover:border-border-active cursor-pointer disabled:opacity-50 disabled:cursor-not-allowed'

function Field({ label, counter, error, hint, children, testId }: {
  label: string
  counter?: { n: number; max: number }
  error?: string
  hint?: string
  children: ReactNode
  testId?: string
}) {
  const t = useI18nStore((s) => s.t)
  const over = counter !== undefined && counter.n > counter.max
  return (
    <label className="flex flex-col gap-1" data-testid={testId}>
      <span className="flex items-baseline justify-between text-xs text-text-secondary">
        <span>{label}</span>
        {counter && (
          <span data-testid={testId ? `${testId}-counter` : undefined} className={over ? 'text-red-500' : 'text-text-muted'}>
            {t('settings.personas.counter', { n: String(counter.n), max: String(counter.max) })}
          </span>
        )}
      </span>
      {children}
      {hint && !error && <span className="text-xs text-text-muted">{hint}</span>}
      {error && <span className="text-xs text-red-500">{error}</span>}
    </label>
  )
}

type TextKey = 'personality' | 'method' | 'interests'

export function PersonaForm({ draft, isNew, issues, saving, avatarPicker, onChange, onSave, onToggleRetired }: Props) {
  const t = useI18nStore((s) => s.t)
  const set = (patch: Partial<Persona>) => onChange({ ...draft, ...patch })
  const issueOf = (field: string) => issues.find((i) => i.field === field)?.message
  const setText = (key: TextKey, lang: keyof PersonaText, value: string) =>
    set({ [key]: { ...draft[key], [lang]: value } } as Pick<Persona, TextKey>)

  const textRow = (key: TextKey) => (
    <div className="grid grid-cols-2 gap-3" data-testid={`persona-${key}`}>
      {(['zh', 'en'] as const).map((lang) => (
        <Field
          key={lang}
          label={`${t(`settings.personas.field.${key}`)} · ${t(`settings.personas.field.${lang}`)}`}
          counter={{ n: countUnits(draft[key][lang], lang), max: PERSONA_LIMITS[key] }}
          error={issueOf(`${key}.${lang}`)}
          testId={`persona-${key}-${lang}`}
        >
          <textarea
            rows={2}
            className={INPUT}
            value={draft[key][lang]}
            onChange={(e) => setText(key, lang, e.target.value)}
          />
        </Field>
      ))}
    </div>
  )

  return (
    <form
      className="flex flex-col gap-3"
      data-testid="persona-form"
      onSubmit={(e) => { e.preventDefault(); if (!saving) onSave() }}
    >
      <div className="flex items-center gap-3">
        <Avatar size={96} code={draft.code || '?'} name={draft.name} src={draft.avatar_data} fallbackColor={personaColor(draft.code)} />
        <div className="min-w-0 flex-1">{avatarPicker}</div>
      </div>

      <div className="grid grid-cols-2 gap-3">
        <Field label={t('settings.personas.field.code')} hint={isNew ? t('settings.personas.field.code_hint') : undefined} error={issueOf('code')} testId="persona-code">
          <input
            className={INPUT}
            value={draft.code}
            disabled={!isNew}
            maxLength={12}
            autoCapitalize="none"
            onChange={(e) => set({ code: e.target.value })}
          />
        </Field>
        <Field
          label={t('settings.personas.field.name')}
          counter={{ n: Array.from(draft.name).length, max: PERSONA_LIMITS.name }}
          error={issueOf('name')}
          testId="persona-name"
        >
          <input className={INPUT} value={draft.name} onChange={(e) => set({ name: e.target.value })} />
        </Field>
      </div>

      <div className="grid grid-cols-2 gap-3">
        <Field label={t('settings.personas.field.sex')} testId="persona-sex">
          <select className={INPUT} value={draft.sex} onChange={(e) => set({ sex: e.target.value as Persona['sex'] })}>
            <option value="">{t('settings.personas.field.sex.none')}</option>
            <option value="male">{t('settings.personas.field.sex.male')}</option>
            <option value="female">{t('settings.personas.field.sex.female')}</option>
          </select>
        </Field>
        <div className="grid grid-cols-3 gap-2" data-testid="persona-nationality">
          <Field label={t('settings.personas.field.nationality_code')}>
            <input className={INPUT} value={draft.nationality.code} onChange={(e) => set({ nationality: { ...draft.nationality, code: e.target.value } })} />
          </Field>
          <Field label={`${t('settings.personas.field.nationality')} · ${t('settings.personas.field.zh')}`}>
            <input className={INPUT} value={draft.nationality.zh} onChange={(e) => set({ nationality: { ...draft.nationality, zh: e.target.value } })} />
          </Field>
          <Field label={`${t('settings.personas.field.nationality')} · ${t('settings.personas.field.en')}`}>
            <input className={INPUT} value={draft.nationality.en} onChange={(e) => set({ nationality: { ...draft.nationality, en: e.target.value } })} />
          </Field>
        </div>
      </div>

      {textRow('personality')}
      {textRow('method')}
      {textRow('interests')}

      <label className="flex items-center gap-2 text-sm text-text-primary" data-testid="persona-lead">
        <input type="checkbox" checked={draft.lead} onChange={(e) => set({ lead: e.target.checked })} />
        {t('settings.personas.field.lead')}
      </label>

      <div className="flex items-center justify-end gap-2">
        {!isNew && (
          <button type="button" className={BTN} data-testid="persona-retire" disabled={saving} onClick={onToggleRetired}>
            {draft.retired ? t('settings.personas.restore') : t('settings.personas.retire')}
          </button>
        )}
        <button type="submit" className={BTN} data-testid="persona-save" disabled={saving}>
          {t('settings.personas.save')}
        </button>
      </div>
    </form>
  )
}
