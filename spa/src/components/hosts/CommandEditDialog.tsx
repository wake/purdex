import { useRef, useState } from 'react'
import { commandWordOf } from '../../lib/command-word'
import { resolveShellCommand, type ShellResolveVerdict } from '../../lib/host-api'
import { validateCommand, type FieldErrors } from '../../lib/host-config-validate'
import type { HostCommand } from '../../lib/host-config-api'
import { useI18nStore } from '../../stores/useI18nStore'
import { ShellVerdict } from '../settings/ShellVerdict'
import { CommandIconPicker } from './CommandIconPicker'
import { CommandIconView } from './CommandIconView'

type Field = 'name' | 'command' | 'icon'

export function CommandEditDialog({ hostId, initial, isNew, busy, error, onSave, onCancel }: {
  hostId: string
  initial: HostCommand
  isNew: boolean
  busy: boolean
  /** Save failure text (e.g. the daemon's 400 body), shown inline. */
  error: string | null
  onSave: (command: HostCommand) => void
  onCancel: () => void
}) {
  const t = useI18nStore((s) => s.t)
  const [draft, setDraft] = useState(initial)
  const [errors, setErrors] = useState<FieldErrors<Field>>({})
  // A verdict is only shown while the word it judged is still the word on
  // screen; a response to an abandoned request (seq moved on) is dropped.
  const [check, setCheck] = useState<{ word: string; verdict: ShellResolveVerdict | 'pending' } | null>(null)
  const seq = useRef(0)
  const word = commandWordOf(draft.command)
  const title = t(isNew ? 'commands.add_title' : 'commands.edit_title')

  const runCheck = async () => {
    if (!word) return
    const mine = ++seq.current
    setCheck({ word, verdict: 'pending' })
    let verdict: ShellResolveVerdict
    try { verdict = await resolveShellCommand(hostId, word) } catch { verdict = { status: 'unverifiable' } }
    if (seq.current === mine) setCheck({ word, verdict })
  }

  // The check is advice only: it never gates a save.
  const submit = () => {
    if (busy) return
    const next = { ...draft, name: draft.name.trim() }
    const found = validateCommand(next)
    setErrors(found)
    if (Object.keys(found).length === 0) onSave(next)
  }

  const input = 'mt-1 w-full bg-surface-primary border border-border-default rounded px-2 py-1.5 text-sm text-text-primary'
  const fieldError = (field: Field) => errors[field]
    ? <p data-testid={`command-error-${field}`} className="mt-1 text-xs text-red-400">{t(errors[field]!)}</p>
    : null

  return (
    <div role="dialog" data-testid="command-dialog" aria-label={title}
      className="p-4 bg-surface-secondary border border-border-default rounded-lg mb-4"
      onKeyDown={(e) => { if (e.key === 'Escape') onCancel() }}>
      <h3 className="text-sm font-semibold mb-3">{title}</h3>
      <label className="block text-xs text-text-secondary mb-2">{t('commands.field.name')}
        <input data-testid="command-field-name" autoFocus className={input} value={draft.name}
          onChange={(e) => { const name = e.target.value; setDraft((d) => ({ ...d, name })) }} />
        {fieldError('name')}
      </label>
      <label className="block text-xs text-text-secondary mb-2">{t('commands.field.command')}
        <input data-testid="command-field-command" className={`${input} font-mono`} value={draft.command} spellCheck={false}
          onChange={(e) => { const command = e.target.value; setDraft((d) => ({ ...d, command })) }} />
        {fieldError('command')}
      </label>
      <div className="mb-3 flex flex-wrap items-center gap-2 text-xs">
        <button type="button" data-testid="command-word-test" disabled={!word || (check?.word === word && check.verdict === 'pending')}
          onClick={() => void runCheck()}
          className="rounded-md border border-border-default px-2 py-1 text-text-secondary hover:border-border-active hover:text-text-primary disabled:opacity-50 disabled:cursor-not-allowed">
          {t('resume_template.test')}
        </button>
        {check && check.word === word
          ? <ShellVerdict testId="command-word-verdict" verdict={check.verdict} t={t} />
          : null}
      </div>
      <div className="text-xs text-text-secondary mb-1 flex items-center gap-2">
        {t('commands.field.icon')}
        <CommandIconView icon={draft.icon} size={16} />
      </div>
      <CommandIconPicker value={draft.icon} onChange={(icon) => setDraft((d) => ({ ...d, icon }))} />
      {fieldError('icon')}
      {error && <p data-testid="command-dialog-error" className="mt-2 text-xs text-red-400 whitespace-pre-wrap">{error}</p>}
      <div className="flex gap-2 mt-3">
        <button type="button" data-testid="command-save" onClick={submit} disabled={busy}
          className="px-3 py-1.5 rounded text-xs bg-accent text-white cursor-pointer disabled:opacity-50">{t('common.save')}</button>
        <button type="button" onClick={onCancel}
          className="px-3 py-1.5 rounded text-xs bg-surface-tertiary text-text-secondary cursor-pointer">{t('common.cancel')}</button>
      </div>
    </div>
  )
}
