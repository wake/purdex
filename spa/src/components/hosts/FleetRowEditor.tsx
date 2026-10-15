import { useEffect, useState } from 'react'
import { useFleetConfigStore, type DroppedNotice } from '../../lib/fleet/config-store'
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
  const [order, setOrder] = useState('')
  const [peer, setPeer] = useState('')
  const [enabled, setEnabled] = useState(true)
  const [busy, setBusy] = useState(false)
  const [message, setMessage] = useState<{ kind: 'error' | 'conflict' | 'saved'; text?: string } | null>(null)
  const [confirm, setConfirm] = useState<DroppedNotice[] | null>(null)

  useEffect(() => {
    if (supported && online) void useFleetConfigStore.getState().load(hostId)
  }, [hostId, supported, online])

  // The form follows the daemon's copy whenever it is (re)read.
  const items = cfg?.status === 'ready' ? cfg.fleet.items : null
  const revision = cfg?.fleet.revision
  useEffect(() => {
    if (!items) return
    setOrder(items.order.join(', '))
    setPeer(items.peer_ssh_alias)
    setEnabled(items.enabled)
    // eslint-disable-next-line react-hooks/exhaustive-deps -- re-sync on a new revision only, not on every render's `items`
  }, [revision, cfg?.status])

  if (!supported || !cfg) return null
  const aliases = cfg.aliases
  const options = peer && !aliases.includes(peer) ? [peer, ...aliases] : aliases

  const save = async (confirmDropped: boolean) => {
    setBusy(true)
    setMessage(null)
    try {
      const r = await useFleetConfigStore.getState().saveFleetRow(hostId, { order: splitOrder(order), peer_ssh_alias: peer, enabled }, { confirmDropped })
      if (r.kind === 'confirm') setConfirm(r.notices)
      else {
        setConfirm(null)
        setMessage(r.kind === 'error' ? { kind: 'error', text: r.text } : { kind: r.kind })
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
            onChange={(e) => setOrder(e.target.value)} />
          <span className="block mt-1 text-text-muted">{t('fleet_hosts.row.order_hint')}</span>
        </label>
        <label className="block text-xs text-text-secondary">{t('fleet_hosts.row.peer_alias')}
          <select data-testid="fleet-row-peer" className={`${input} mt-1`} value={peer} onChange={(e) => setPeer(e.target.value)}>
            <option value="">{t('fleet_hosts.row.peer_none')}</option>
            {options.map((a) => <option key={a} value={a}>{a}</option>)}
          </select>
        </label>
        <div className="flex items-center justify-between text-xs text-text-secondary">
          {t('fleet_hosts.row.enabled')}
          <ToggleSwitch label={t('fleet_hosts.row.enabled')} checked={enabled} onChange={setEnabled} />
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
              <button type="button" onClick={() => setConfirm(null)}
                className="px-3 py-1 rounded bg-surface-tertiary text-text-secondary cursor-pointer">{t('common.cancel')}</button>
            </div>
          </div>
        )}
        {message?.kind === 'error' && <p data-testid="fleet-row-error" className="text-xs text-red-400 whitespace-pre-wrap">{message.text}</p>}
        {message?.kind === 'conflict' && <p data-testid="fleet-row-conflict" className="text-xs text-status-warning-text">{t('fleet_hosts.row.conflict')}</p>}
        {message?.kind === 'saved' && <p className="text-xs text-text-muted">{t('fleet_hosts.row.saved', { host: hostLabel(hostId, hostLookOf(hostId)) })}</p>}
        <button type="button" data-testid="fleet-row-save" disabled={busy || !online || cfg.status !== 'ready'} onClick={() => void save(false)}
          className="px-3 py-1.5 rounded text-xs bg-accent text-white cursor-pointer disabled:opacity-50">{t('common.save')}</button>
      </div>
    </Section>
  )
}
