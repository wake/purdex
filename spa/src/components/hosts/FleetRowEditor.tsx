import { useEffect, useState } from 'react'
import { useFleetConfigStore } from '../../lib/fleet/config-store'
import { useFleetDraftStore } from '../../lib/fleet/config-draft'
import { fleetRowError } from '../../lib/fleet/config-validate'
import { useFleetStore } from '../../lib/fleet/store'
import { useHostStore } from '../../stores/useHostStore'
import { useI18nStore } from '../../stores/useI18nStore'
import { hostLabel, hostLookOf } from '../../lib/host-look'
import { ToggleSwitch } from '../settings/ToggleSwitch'
import { Section } from './form-fields'

const splitOrder = (text: string): string[] => text.split(',').map((a) => a.trim()).filter(Boolean)

/**
 * The `fleet` host-config row of ONE daemon host (fleet spec §4, §8.2): `order` (1 to 4 aliases, comma separated), the SSH
 * alias this daemon uses for the other daemon host (picked from this daemon's own aliases), and the switch. Shown on the
 * overview of a `fleet.v1` daemon host; a stored row that dropped something is confirmed before it is written back.
 */
export function FleetRowEditor({ hostId }: { hostId: string }) {
  const t = useI18nStore((s) => s.t)
  const supported = useFleetStore((s) => s.byHost[hostId]?.support === 'yes')
  const online = useHostStore((s) => s.runtime[hostId]?.status === 'connected')
  const cfg = useFleetConfigStore((s) => s.byHost[hostId])
  const draft = useFleetDraftStore((s) => s.fleetRows[hostId])
  const confirm = useFleetDraftStore((s) => s.fleetConfirm[hostId]) ?? null
  const [busy, setBusy] = useState(false)
  const [message, setMessage] = useState<{ kind: 'error' | 'conflict' | 'saved' | 'invalid'; text?: string } | null>(null)

  useEffect(() => {
    if (supported && online) void useFleetConfigStore.getState().load(hostId)
  }, [hostId, supported, online])

  // The form shows the reader's edit when there is one (kept outside the component: a tab switch unmounts this), else the
  // daemon's copy. An edit made against an older revision of the row is stale once the row is read again: it is dropped.
  const items = cfg?.status === 'ready' ? cfg.fleet.items : null
  const revision = cfg?.fleet.revision ?? 0
  useEffect(() => {
    if (draft && cfg?.status === 'ready' && draft.rev !== revision) {
      useFleetDraftStore.getState().setFleetRow(hostId, null)
      useFleetDraftStore.getState().setFleetConfirm(hostId, null)
    }
  }, [draft, revision, cfg?.status, hostId])
  const live = draft && draft.rev === revision ? draft : null
  const order = live?.order ?? items?.order.join(', ') ?? ''
  const peer = live?.peer ?? items?.peer_ssh_alias ?? ''
  const enabled = live?.enabled ?? items?.enabled ?? true
  const edit = (patch: Partial<{ order: string; peer: string; enabled: boolean }>) =>
    useFleetDraftStore.getState().setFleetRow(hostId, { order, peer, enabled, ...patch, rev: revision })

  if (!supported || !cfg) return null
  const aliases = cfg.aliases
  const options = peer && !aliases.includes(peer) ? [peer, ...aliases] : aliases

  const save = async (confirmDropped: boolean) => {
    const bad = fleetRowError(splitOrder(order), peer, aliases)
    if (bad) { setMessage({ kind: 'invalid', text: t(bad.key, { alias: bad.alias ?? '' }) }); return }
    setBusy(true)
    setMessage(null)
    try {
      const r = await useFleetConfigStore.getState().saveFleetRow(hostId, { order: splitOrder(order), peer_ssh_alias: peer, enabled }, { confirmDropped })
      if (r.kind === 'confirm') useFleetDraftStore.getState().setFleetConfirm(hostId, r.notices)
      else {
        useFleetDraftStore.getState().setFleetConfirm(hostId, null)
        if (r.kind === 'saved') useFleetDraftStore.getState().setFleetRow(hostId, null)
        // A conflict re-reads the row: the edit (based on the old revision) is stale then, and the notice says so.
        setMessage(r.kind === 'error' ? { kind: 'error', text: r.text } : r.kind === 'conflict' ? { kind: 'conflict' } : { kind: 'saved' })
      }
    } finally {
      setBusy(false)
    }
  }

  const input = 'w-full bg-surface-secondary border border-border-default rounded px-2 py-1 text-sm text-text-primary'
  return (
    <Section title={t('fleet_hosts.row.title')}>
      <div data-testid="fleet-row-editor" className="space-y-3">
        <label className="block text-xs text-text-secondary">{t('fleet_hosts.row.order')}
          <input data-testid="fleet-row-order" className={`${input} mt-1 font-mono`} value={order} spellCheck={false}
            onChange={(e) => edit({ order: e.target.value })} />
          <span className="block mt-1 text-text-muted">{t('fleet_hosts.row.order_hint')}</span>
        </label>
        <label className="block text-xs text-text-secondary">{t('fleet_hosts.row.peer_alias')}
          <select data-testid="fleet-row-peer" className={`${input} mt-1`} value={peer} onChange={(e) => edit({ peer: e.target.value })}>
            <option value="">{t('fleet_hosts.row.peer_none')}</option>
            {options.map((a) => <option key={a} value={a}>{a}</option>)}
          </select>
        </label>
        <div className="flex items-center justify-between text-xs text-text-secondary">
          {t('fleet_hosts.row.enabled')}
          <ToggleSwitch label={t('fleet_hosts.row.enabled')} checked={enabled} onChange={(v) => edit({ enabled: v })} />
        </div>
        {confirm && (
          <div data-testid="fleet-row-dropped-confirm" className="p-3 rounded border border-border-default text-xs">
            <p className="font-medium text-text-primary">{t('fleet_hosts.dropped.title')}</p>
            <ul className="mt-1 list-disc pl-5 text-text-secondary">
              {confirm.map((n) => (
                <li key={n.hostId}>{n.invalid ? `${t('fleet_hosts.dropped.invalid')} ` : ''}{n.reasons.join('; ')}</li>
              ))}
            </ul>
            <p className="mt-1 text-text-muted">{t('fleet_hosts.dropped.warn')}</p>
            <div className="flex gap-2 mt-2">
              <button type="button" data-testid="fleet-row-dropped-save" disabled={busy} onClick={() => void save(true)}
                className="px-3 py-1 rounded bg-accent text-white cursor-pointer disabled:opacity-50">{t('fleet_hosts.dropped.save_anyway')}</button>
              <button type="button" onClick={() => useFleetDraftStore.getState().setFleetConfirm(hostId, null)}
                className="px-3 py-1 rounded bg-surface-tertiary text-text-secondary cursor-pointer">{t('common.cancel')}</button>
            </div>
          </div>
        )}
        {message?.kind === 'invalid' && <p data-testid="fleet-row-invalid" className="text-xs text-red-400">{message.text}</p>}
        {message?.kind === 'error' && <p data-testid="fleet-row-error" className="text-xs text-red-400 whitespace-pre-wrap">{message.text}</p>}
        {message?.kind === 'conflict' && <p data-testid="fleet-row-conflict" className="text-xs text-status-warning-text">{t('fleet_hosts.row.conflict')}</p>}
        {message?.kind === 'saved' && <p className="text-xs text-text-muted">{t('fleet_hosts.row.saved', { host: hostLabel(hostId, hostLookOf(hostId)) })}</p>}
        <button type="button" data-testid="fleet-row-save" disabled={busy || !online || cfg.status !== 'ready'} onClick={() => void save(false)}
          className="px-3 py-1.5 rounded text-xs bg-accent text-white cursor-pointer disabled:opacity-50">{t('common.save')}</button>
      </div>
    </Section>
  )
}
