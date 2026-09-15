import { useState } from 'react'
import { CheckCircle, CircleNotch, Question, Warning, XCircle } from '@phosphor-icons/react'
import { suggestSlug, validateProject, type FieldErrors } from '../../lib/host-config-validate'
import type { HostProject } from '../../lib/host-config-api'
import { useI18nStore } from '../../stores/useI18nStore'
import { usePathCheck, type PathVerdict } from './usePathCheck'

type Field = 'name' | 'slug' | 'path'

const PATH_STATUS: Record<PathVerdict, { Icon: typeof CheckCircle; cls: string; key: string }> = {
  dir: { Icon: CheckCircle, cls: 'text-status-success', key: 'projects.path.dir' },
  not_dir: { Icon: Warning, cls: 'text-status-warning', key: 'projects.path.not_dir' },
  missing: { Icon: XCircle, cls: 'text-status-error', key: 'projects.path.missing' },
  error: { Icon: Question, cls: 'text-text-muted', key: 'projects.path.unknown' },
  unverifiable: { Icon: Question, cls: 'text-text-muted', key: 'projects.path.unknown' },
  checking: { Icon: CircleNotch, cls: 'text-text-muted animate-spin', key: 'projects.path.checking' },
  idle: { Icon: Question, cls: 'text-text-muted', key: 'projects.path.unknown' },
}

export function PathStatusIcon({ status, testId }: { status: PathVerdict; testId: string }) {
  const t = useI18nStore((s) => s.t)
  const { Icon, cls, key } = PATH_STATUS[status]
  return (
    <span data-testid={testId} data-status={status} title={`${t(key)} · ${t('projects.path_hint')}`} className="inline-flex shrink-0">
      <Icon size={14} className={cls} />
    </span>
  )
}

export function ProjectEditDialog({ hostId, initial, others, busy, error, onSave, onCancel }: {
  hostId: string
  initial: HostProject
  /** Every project on the host (may include `initial` itself). */
  others: readonly HostProject[]
  busy: boolean
  /** Save failure text (e.g. the daemon's 400 body), shown inline. */
  error: string | null
  onSave: (project: HostProject) => void
  onCancel: () => void
}) {
  const t = useI18nStore((s) => s.t)
  const [draft, setDraft] = useState(initial)
  // A new project's slug follows its name until the user types in the slug.
  const [slugTouched, setSlugTouched] = useState(initial.slug !== '')
  const [errors, setErrors] = useState<FieldErrors<Field>>({})
  const pathStatus = usePathCheck(hostId, draft.path)
  const isNew = !others.some((o) => o.id === initial.id)
  const title = t(isNew ? 'projects.add_title' : 'projects.edit_title')

  const submit = () => {
    if (busy) return
    const next = { ...draft, name: draft.name.trim(), path: draft.path.trim() }
    const found = validateProject(next, others)
    setErrors(found)
    if (Object.keys(found).length === 0) onSave(next)
  }

  const input = 'mt-1 w-full bg-surface-primary border border-border-default rounded px-2 py-1.5 text-sm text-text-primary'
  const fieldError = (field: Field) => errors[field]
    ? <p data-testid={`project-error-${field}`} className="mt-1 text-xs text-red-400">{t(errors[field]!)}</p>
    : null

  return (
    <div role="dialog" aria-label={title}
      className="p-4 bg-surface-secondary border border-border-default rounded-lg mb-4"
      onKeyDown={(e) => { if (e.key === 'Escape') onCancel() }}>
      <h3 className="text-sm font-semibold mb-3">{title}</h3>
      <label className="block text-xs text-text-secondary mb-2">{t('projects.field.name')}
        <input data-testid="project-field-name" autoFocus className={input} value={draft.name}
          onChange={(e) => {
            const name = e.target.value
            setDraft((d) => ({ ...d, name, slug: slugTouched ? d.slug : suggestSlug(name) }))
          }} />
        {fieldError('name')}
      </label>
      <label className="block text-xs text-text-secondary mb-2">{t('projects.field.slug')}
        <input data-testid="project-field-slug" className={`${input} font-mono`} value={draft.slug}
          onChange={(e) => { const slug = e.target.value; setSlugTouched(true); setDraft((d) => ({ ...d, slug })) }} />
        {fieldError('slug')}
      </label>
      <label className="block text-xs text-text-secondary mb-2">{t('projects.field.path')}
        <span className="flex items-center gap-2">
          <input data-testid="project-field-path" className={`${input} font-mono`} value={draft.path}
            onChange={(e) => { const path = e.target.value; setDraft((d) => ({ ...d, path })) }}
            onKeyDown={(e) => { if (e.key === 'Enter') submit() }} />
          <PathStatusIcon status={pathStatus} testId="project-dialog-path-status" />
        </span>
        {fieldError('path')}
      </label>
      {error && <p data-testid="project-dialog-error" className="mt-2 text-xs text-red-400 whitespace-pre-wrap">{error}</p>}
      <div className="flex gap-2 mt-3">
        <button type="button" data-testid="project-save" onClick={submit} disabled={busy}
          className="px-3 py-1.5 rounded text-xs bg-accent text-white cursor-pointer disabled:opacity-50">{t('common.save')}</button>
        <button type="button" onClick={onCancel}
          className="px-3 py-1.5 rounded text-xs bg-surface-tertiary text-text-secondary cursor-pointer">{t('common.cancel')}</button>
      </div>
    </div>
  )
}
