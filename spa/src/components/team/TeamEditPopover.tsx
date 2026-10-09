// spa/src/components/team/TeamEditPopover.tsx — the small form under the panel header: a team's name, short label and
// colour (TI-7, team-interface spec §4.12). Opened by a double-click on the name when the lead's host has `team.edit.v1`.
//
// It never keeps a display copy: the form starts from the roster's values, Save sends all three to the lead's host and
// the panel keeps rendering whatever the roster's next frame says. Save / Enter sends; Cancel / Esc / a click outside
// drops the draft. The terminal's focus is remembered on open and handed back on close; the form's own inputs may take it.
import { useEffect, useLayoutEffect, useRef, useState } from 'react'
import { createPortal } from 'react-dom'
import { TEAM_COLORS } from './team-display'
import { POPOVER_W, placeBelow } from './panel-layout'
import { saveAppearance, type AppearanceField } from '../../lib/team/appearance-api'
import { TEAM_LABEL_MAX_WIDTH, goTrim } from '../../lib/team/label'
import { cellWidth } from '../../lib/textwidth'
import { useI18nStore } from '../../stores/useI18nStore'
import { useUndoToast } from '../../stores/useUndoToast'

export interface TeamEditTarget {
  hostId: string
  teamId: string
  /** The roster's current values: what the form starts from and what an untouched field sends back. */
  name: string
  label: string
  color: number | null
}

interface Props {
  target: TeamEditTarget
  /** The live header element the form hangs under (it is a different element in each mode, so it is asked for each time). */
  anchor: () => HTMLElement | null
  onClose: () => void
}

type FieldErrors = Partial<Record<AppearanceField, string>>

export function TeamEditPopover({ target, anchor, onClose }: Props) {
  const t = useI18nStore((s) => s.t)
  const [name, setName] = useState(target.name)
  const [label, setLabel] = useState(target.label)
  const [color, setColor] = useState<number | null>(target.color)
  const [errors, setErrors] = useState<FieldErrors>({})
  const [formError, setFormError] = useState('')
  const [saving, setSaving] = useState(false)
  const box = useRef<HTMLDivElement>(null)
  const alive = useRef(true)

  // Remember where the focus was; hand it back when the form goes away.
  useEffect(() => {
    const prev = document.activeElement instanceof HTMLElement ? document.activeElement : null
    box.current?.querySelector<HTMLInputElement>('input')?.focus()
    alive.current = true
    return () => {
      alive.current = false
      if (prev?.isConnected) prev.focus()
    }
  }, [])

  // The form follows its header: placed after every render (the panel's width / mode / enlarge re-render it), and again when
  // the window resizes, anything scrolls, or the header's own box changes. A header that is gone closes the form.
  useLayoutEffect(() => {
    const el = box.current
    const head = anchor()
    if (!el || !head) return
    const r = head.getBoundingClientRect()
    const at = placeBelow(r, { w: POPOVER_W, h: el.offsetHeight }, { w: window.innerWidth, h: window.innerHeight })
    el.style.left = `${at.left}px`
    el.style.top = `${at.top}px`
  })
  useEffect(() => {
    const place = () => {
      const el = box.current
      const head = anchor()
      if (!head) { onClose(); return }
      if (!el) return
      const at = placeBelow(head.getBoundingClientRect(), { w: POPOVER_W, h: el.offsetHeight }, { w: window.innerWidth, h: window.innerHeight })
      el.style.left = `${at.left}px`
      el.style.top = `${at.top}px`
    }
    window.addEventListener('resize', place)
    window.addEventListener('scroll', place, true)
    const head = anchor()
    const ro = head && typeof ResizeObserver !== 'undefined' ? new ResizeObserver(place) : null
    if (head) ro?.observe(head)
    return () => {
      window.removeEventListener('resize', place)
      window.removeEventListener('scroll', place, true)
      ro?.disconnect()
    }
  }, [anchor, onClose])

  // A press outside drops the draft.
  useEffect(() => {
    const down = (e: MouseEvent) => {
      if (box.current && !box.current.contains(e.target as Node)) onClose()
    }
    document.addEventListener('mousedown', down)
    return () => document.removeEventListener('mousedown', down)
  }, [onClose])

  const width = cellWidth(goTrim(label))
  const tooWide = width > TEAM_LABEL_MAX_WIDTH

  async function submit() {
    if (saving || tooWide) return
    setSaving(true)
    setErrors({})
    setFormError('')
    const r = await saveAppearance(target.hostId, target.teamId, { name, label, color })
    if (!alive.current) return
    setSaving(false)
    if (r.ok) { onClose(); return }
    if (r.kind === 'ended') {
      useUndoToast.getState().show(t('team.edit.ended'))
      onClose()
      return
    }
    if (r.kind === 'field') setErrors({ [r.field]: r.message })
    else setFormError(t('team.edit.failed', { message: r.message }))
  }

  const onKeyDown = (e: React.KeyboardEvent) => {
    if (e.key === 'Escape') { e.preventDefault(); e.stopPropagation(); onClose() }
    else if (e.key === 'Enter' && (e.target as HTMLElement).tagName === 'INPUT') { e.preventDefault(); void submit() }
  }

  const input = 'w-full px-1.5 py-1 rounded bg-surface-primary border border-border-default text-text-primary text-xs outline-none focus:border-border-active'
  return createPortal(
    <div
      ref={box}
      role="dialog"
      aria-label={t('team.edit.title')}
      data-testid="team-edit-popover"
      onKeyDown={onKeyDown}
      className="fixed z-50 flex flex-col gap-2 p-2.5 rounded-lg border border-border-default bg-surface-elevated shadow-xl text-xs text-text-primary font-sans"
      style={{ width: POPOVER_W }}
    >
      <label className="flex flex-col gap-1">
        <span className="text-text-secondary">{t('team.edit.name')}</span>
        <input data-testid="team-edit-name" className={input} value={name} onChange={(e) => setName(e.target.value)} />
        {errors.name && <span data-testid="team-edit-name-error" role="alert" className="text-red-400">{errors.name}</span>}
      </label>
      <label className="flex flex-col gap-1">
        <span className="flex items-baseline justify-between text-text-secondary">
          <span>{t('team.edit.label')}</span>
          <span data-testid="team-edit-label-width" className={tooWide ? 'text-red-400' : 'text-text-muted'}>
            {t('team.edit.label_width', { used: String(width), max: String(TEAM_LABEL_MAX_WIDTH) })}
          </span>
        </span>
        <input data-testid="team-edit-label" className={input} value={label} onChange={(e) => setLabel(e.target.value)} />
        {tooWide && <span data-testid="team-edit-label-wide" role="alert" className="text-red-400">{t('team.edit.label_too_wide', { max: String(TEAM_LABEL_MAX_WIDTH) })}</span>}
        {errors.label && <span data-testid="team-edit-label-error" role="alert" className="text-red-400">{errors.label}</span>}
      </label>
      <div className="flex flex-col gap-1">
        <span className="text-text-secondary">{t('team.edit.color')}</span>
        <div className="flex flex-wrap items-center gap-1.5">
          <button
            type="button"
            data-testid="team-edit-color-auto"
            aria-pressed={color === null}
            onClick={() => setColor(null)}
            className={`px-1.5 py-0.5 rounded border cursor-pointer ${color === null ? 'border-text-primary text-text-primary' : 'border-border-default text-text-secondary'}`}
          >
            {t('team.edit.color_auto')}
          </button>
          {TEAM_COLORS.map((c, i) => (
            <button
              key={c}
              type="button"
              data-testid={`team-edit-color-${i}`}
              aria-pressed={color === i}
              aria-label={t('team.edit.color_n', { n: String(i + 1) })}
              onClick={() => setColor(i)}
              className={`w-5 h-5 rounded-full cursor-pointer border-2 ${color === i ? 'border-text-primary' : 'border-transparent'}`}
              style={{ background: c }}
            />
          ))}
        </div>
        {errors.color && <span data-testid="team-edit-color-error" role="alert" className="text-red-400">{errors.color}</span>}
      </div>
      {formError && <span data-testid="team-edit-form-error" role="alert" className="text-red-400">{formError}</span>}
      <div className="flex justify-end gap-2">
        <button type="button" data-testid="team-edit-cancel" onClick={onClose} className="px-2 py-1 rounded text-text-secondary hover:bg-surface-hover cursor-pointer">
          {t('team.edit.cancel')}
        </button>
        <button
          type="button"
          data-testid="team-edit-save"
          disabled={saving || tooWide}
          onClick={() => void submit()}
          className="px-2 py-1 rounded bg-surface-active text-white cursor-pointer disabled:opacity-50 disabled:cursor-default"
        >
          {saving ? t('team.edit.saving') : t('team.edit.save')}
        </button>
      </div>
    </div>,
    document.body,
  )
}
