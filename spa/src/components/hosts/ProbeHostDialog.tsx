import { useState } from 'react'
import { useI18nStore } from '../../stores/useI18nStore'
import { useFleetConfigStore, type ProbeEntry } from '../../lib/fleet/config-store'
import { withColorLayer, withIcon } from '../../stores/useHostStore'
import type { HostColorMode } from '../../lib/host-color'
import { hostLabel, hostLookOf } from '../../lib/host-look'
import type { IconWeight } from '../../types/tab'
import { HostColorPicker } from './HostColorPicker'
import { HostIconPicker } from './HostIconPicker'

/**
 * Edit one probe-only host (fleet spec §8.2): name, icon and colour on a draft (the lifted pickers), and the SSH alias PER
 * DAEMON — each picked from that daemon's own `GET /api/fleet/ssh-aliases`, because each daemon reaches the host through its
 * own SSH config. The 400 text of a failed save is shown here.
 */
export function ProbeHostDialog({ initial, hostIds, isNew, busy, error, onSave, onCancel }: {
  initial: ProbeEntry
  /** The daemon hosts the entry is written to. */
  hostIds: string[]
  isNew: boolean
  busy: boolean
  error: string | null
  onSave: (entry: ProbeEntry) => void
  onCancel: () => void
}) {
  const t = useI18nStore((s) => s.t)
  const byHost = useFleetConfigStore((s) => s.byHost)
  const [draft, setDraft] = useState(initial)
  const [mode, setMode] = useState<HostColorMode>('console')
  const [invalid, setInvalid] = useState<string | null>(null)
  const title = t(isNew ? 'fleet_hosts.probe.add_title' : 'fleet_hosts.probe.edit_title')

  const submit = () => {
    if (busy) return
    const name = draft.name.trim()
    if (!name) return setInvalid('fleet_hosts.probe.err_name')
    if (hostIds.some((id) => !draft.aliases[id])) return setInvalid('fleet_hosts.probe.err_alias')
    setInvalid(null)
    onSave({ ...draft, name })
  }

  const input = 'mt-1 w-full bg-surface-primary border border-border-default rounded px-2 py-1.5 text-sm text-text-primary'

  return (
    <div role="dialog" data-testid="probe-dialog" aria-label={title}
      className="p-4 bg-surface-secondary border border-border-default rounded-lg mb-4"
      onKeyDown={(e) => { if (e.key === 'Escape') onCancel() }}>
      <h3 className="text-sm font-semibold mb-3">{title}</h3>
      <label className="block text-xs text-text-secondary mb-2">{t('fleet_hosts.probe.name')}
        <input data-testid="probe-field-name" autoFocus className={input} value={draft.name}
          onChange={(e) => { const name = e.target.value; setDraft((d) => ({ ...d, name })) }} />
      </label>
      {hostIds.map((id) => {
        const aliases = byHost[id]?.aliases ?? []
        const current = draft.aliases[id] ?? ''
        const options = current && !aliases.includes(current) ? [current, ...aliases] : aliases
        return (
          <label key={id} className="block text-xs text-text-secondary mb-2">
            {t('fleet_hosts.probe.alias_on', { host: hostLabel(id, hostLookOf(id)) })}
            <select data-testid={`probe-alias-${id}`} className={input} value={current}
              onChange={(e) => { const v = e.target.value; setDraft((d) => ({ ...d, aliases: { ...d.aliases, [id]: v } })) }}>
              <option value="">{t('fleet_hosts.probe.alias_pick')}</option>
              {options.map((a) => <option key={a} value={a}>{a}</option>)}
            </select>
          </label>
        )
      })}
      <div className="text-xs text-text-secondary mb-1">{t('hosts.icon.label')}</div>
      <HostIconPicker icon={draft.icon || undefined} weight={draft.icon_weight}
        onChange={(icon, weight) => setDraft((d) => {
          const next = withIcon({ icon: d.icon || undefined, iconWeight: (d.icon_weight || undefined) as IconWeight | undefined }, icon, weight)
          if (next === null) return d
          return { ...d, icon: next.icon ?? '', icon_weight: next.iconWeight ?? '' }
        })} />
      <div className="text-xs text-text-secondary mt-3 mb-1">{t('hosts.color.label')}</div>
      <HostColorPicker colors={draft.colors} mode={mode} onModeChange={setMode}
        onSetLayer={(m, layer, value) => setDraft((d) => {
          const next = withColorLayer({ colors: d.colors }, m, layer, value)
          return next ? { ...d, colors: next.colors } : d
        })}
        onClearMode={(m) => setDraft((d) => {
          const next = withColorLayer({ colors: d.colors }, m, 'main', null)
          return next ? { ...d, colors: next.colors } : d
        })} />
      {invalid && <p data-testid="probe-dialog-invalid" className="mt-2 text-xs text-red-400">{t(invalid)}</p>}
      {error && <p data-testid="probe-dialog-error" className="mt-2 text-xs text-red-400 whitespace-pre-wrap">{error}</p>}
      <div className="flex gap-2 mt-3">
        <button type="button" data-testid="probe-save" onClick={submit} disabled={busy}
          className="px-3 py-1.5 rounded text-xs bg-accent text-white cursor-pointer disabled:opacity-50">{t('common.save')}</button>
        <button type="button" onClick={onCancel}
          className="px-3 py-1.5 rounded text-xs bg-surface-tertiary text-text-secondary cursor-pointer">{t('common.cancel')}</button>
      </div>
    </div>
  )
}
