import { useEffect, useState } from 'react'
import { useI18nStore } from '../../stores/useI18nStore'
import { fetchSshAliases } from '../../lib/fleet/api'
import { defOfDraft, draftColor, draftIcon, useProbeDraftStore, type ProbeDraft } from '../../lib/fleet/probe-draft-memory'
import { saveProbeHosts, upsertProbe } from '../../lib/fleet/probe-hosts-save'
import { Field } from './form-fields'
import { HostColorPicker } from './HostColorPicker'
import { HostIconPicker } from './HostIconPicker'
import { ProbeHostBadge } from './ProbeHostBadge'

const MAX_NAME = 40
const input = 'mt-1 w-full bg-surface-primary border border-border-default rounded px-2 py-1.5 text-sm text-text-primary'

/**
 * The add / edit dialog of a probe-only host (fleet spec §7.2): name, SSH host (a picker of the prober's SSH config),
 * icon and colour. Inline like the other host-config dialogs. The draft is not this component's state: it lives in
 * `probe-draft-memory` so a tab switch (which unmounts the Hosts page) does not lose it. Saved with the whole-list PUT
 * (`saveProbeHosts`); a 409 reloads the list and says so, a 400's text is shown as the daemon sent it.
 */
export function ProbeHostDialog({ hostId, draft }: { hostId: string; draft: ProbeDraft }) {
  const t = useI18nStore((s) => s.t)
  const [aliases, setAliases] = useState<string[] | null>(null)
  const [aliasFailed, setAliasFailed] = useState(false)
  const [saving, setSaving] = useState(false)
  const [fieldError, setFieldError] = useState<'name' | 'alias' | null>(null)
  const { patch, close } = useProbeDraftStore.getState()
  const isNew = draft.probeId === ''
  const title = t(isNew ? 'hosts.probe.add_title' : 'hosts.probe.edit_title')

  useEffect(() => {
    const ctl = new AbortController()
    fetchSshAliases(hostId, ctl.signal).then(
      (list) => { if (!ctl.signal.aborted) setAliases(list) },
      () => { if (!ctl.signal.aborted) setAliasFailed(true) },
    )
    return () => ctl.abort()
  }, [hostId])

  const edit = (fn: (d: ProbeDraft) => ProbeDraft) => patch(hostId, (d) => ({ ...fn(d), error: null }))

  const submit = async () => {
    if (saving) return
    const name = draft.name.trim()
    if (name === '' || name.length > MAX_NAME) { setFieldError('name'); return }
    if (draft.ssh_alias === '') { setFieldError('alias'); return }
    setFieldError(null)
    setSaving(true)
    try {
      const res = await saveProbeHosts(hostId, upsertProbe(defOfDraft(draft)))
      // The draft may be gone (the host was forgotten while the PUT was out): nothing to tell then.
      if (!useProbeDraftStore.getState().byHost[hostId]) return
      if (res.kind === 'ok') close(hostId)
      else if (res.kind === 'conflict') patch(hostId, (d) => ({ ...d, error: t('hosts.probe.conflict') }))
      else if (res.kind === 'invalid') patch(hostId, (d) => ({ ...d, error: res.text }))
      else if (res.kind === 'limit') patch(hostId, (d) => ({ ...d, error: t('hosts.probe.limit') }))
      else patch(hostId, (d) => ({ ...d, error: t('hosts.probe.save_failed', { reason: res.reason }) }))
    } finally {
      setSaving(false)
    }
  }

  const options = aliases ?? []
  const withCurrent = draft.ssh_alias !== '' && !options.includes(draft.ssh_alias) ? [draft.ssh_alias, ...options] : options

  return (
    <div role="dialog" aria-label={title} data-testid="probe-dialog"
      className="p-4 bg-surface-secondary border border-border-default rounded-lg mb-4"
      onKeyDown={(e) => { if (e.key === 'Escape') close(hostId) }}>
      <h3 className="text-sm font-semibold mb-3">{title}</h3>
      <label className="block text-xs text-text-secondary mb-2">{t('hosts.probe.field.name')}
        <input data-testid="probe-field-name" autoFocus className={input} value={draft.name} maxLength={MAX_NAME + 20}
          onChange={(e) => { const name = e.target.value; edit((d) => ({ ...d, name })) }} />
        {fieldError === 'name' && <p data-testid="probe-error-name" className="mt-1 text-xs text-red-400">{t('hosts.probe.err.name', { max: MAX_NAME })}</p>}
      </label>
      <label className="block text-xs text-text-secondary mb-2">{t('hosts.probe.field.ssh_alias')}
        <select data-testid="probe-field-alias" className={`${input} font-mono`} value={draft.ssh_alias}
          onChange={(e) => { const ssh_alias = e.target.value; edit((d) => ({ ...d, ssh_alias })) }}>
          <option value="">{t(aliases === null && !aliasFailed ? 'hosts.probe.ssh_loading' : 'hosts.probe.ssh_pick')}</option>
          {withCurrent.map((a) => <option key={a} value={a}>{a}</option>)}
        </select>
        {aliasFailed && <p data-testid="probe-alias-failed" className="mt-1 text-xs text-text-muted">{t('hosts.probe.ssh_failed')}</p>}
        {aliases !== null && aliases.length === 0 && <p className="mt-1 text-xs text-text-muted">{t('hosts.probe.ssh_none')}</p>}
        {fieldError === 'alias' && <p data-testid="probe-error-alias" className="mt-1 text-xs text-red-400">{t('hosts.probe.err.alias')}</p>}
      </label>
      <Field label={t('hosts.icon.label')}>
        <div className="flex items-start gap-6">
          <HostIconPicker icon={draft.icon} weight={draft.icon_weight} onChange={(icon, weight) => edit((d) => draftIcon(d, icon, weight))} />
          <ProbeHostBadge look={draft} mode={draft.mode} testId="probe-dialog-badge" />
        </div>
      </Field>
      <Field label={t('hosts.color.label')}>
        <HostColorPicker
          colors={draft.colors}
          mode={draft.mode}
          onModeChange={(mode) => patch(hostId, (d) => ({ ...d, mode }))}
          onSetLayer={(mode, layer, value) => edit((d) => draftColor(d, mode, layer, value))}
          onClearMode={(mode) => edit((d) => draftColor(d, mode, 'main', null))}
        />
      </Field>
      {draft.error && <p data-testid="probe-dialog-error" className="mt-2 text-xs text-red-400 whitespace-pre-wrap">{draft.error}</p>}
      <div className="flex gap-2 mt-3">
        <button type="button" data-testid="probe-save" onClick={() => void submit()} disabled={saving}
          className="px-3 py-1.5 rounded text-xs bg-accent text-white cursor-pointer disabled:opacity-50">{t('common.save')}</button>
        <button type="button" data-testid="probe-cancel" onClick={() => close(hostId)}
          className="px-3 py-1.5 rounded text-xs bg-surface-tertiary text-text-secondary cursor-pointer">{t('common.cancel')}</button>
      </div>
    </div>
  )
}
