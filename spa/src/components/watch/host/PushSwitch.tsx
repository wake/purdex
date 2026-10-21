// spa/src/components/watch/host/PushSwitch.tsx — the 設定 tab's 推播 section (monitoring tabs spec M37, M50; plan MON-9a-2b): one switch for `wwd_alerts.push` of every App host that has a daemon. Nothing the reader typed is kept here:
// the state lives in the daemons (and the not-yet-written ones in the module queue of lib/pool/push-switch), so the section reads it again whenever it is shown.
import { useCallback, useEffect, useMemo, useState, useSyncExternalStore } from 'react'
import { useI18nStore } from '../../../stores/useI18nStore'
import { useHostStore } from '../../../stores/useHostStore'
import { useNexHostStore } from '../../../stores/useNexHostStore'
import { usePoolStore } from '../../../lib/pool/store'
import { usePoolHostOwners } from '../../../lib/pool/daemons'
import { pendingPush, pushStateOf, pushTargets, readPush, subscribePending, writePushAll, type PushHost } from '../../../lib/pool/push-switch'
import type { MonitorHost } from '../../../lib/pool/use-monitor-hosts'
import { Section } from '../../hosts/form-fields'
import { SettingItem } from '../../settings/SettingItem'
import { ToggleSwitch } from '../../settings/ToggleSwitch'

export function PushSwitch({ host }: { host: MonitorHost }) {
  const t = useI18nStore((s) => s.t)
  const owners = usePoolHostOwners()
  const poolHosts = usePoolStore((s) => s.hosts)
  const runtime = useHostStore((s) => s.runtime)
  const byHost = useNexHostStore((s) => s.byHost)
  const waiting = useSyncExternalStore(subscribePending, pendingPush)
  const targets = useMemo(() => pushTargets(owners, poolHosts), [owners, poolHosts])
  // Read again when the set of hosts changes or one of them connects or drops.
  const readKey = targets.map((x) => `${x.hostConfigId}:${runtime[x.hostConfigId]?.status === 'connected' ? 1 : 0}`).join('|')
  const [hosts, setHosts] = useState<PushHost[] | null>(null)
  const [busy, setBusy] = useState(false)
  const [errors, setErrors] = useState<Record<string, string>>({})

  const reread = useCallback(async () => { setHosts(await readPush(targets)) }, [targets])
  useEffect(() => {
    let live = true
    void readPush(targets).then((h) => { if (live) setHosts(h) })
    return () => { live = false }
    // eslint-disable-next-line react-hooks/exhaustive-deps -- `readKey` is what `targets` and the connections amount to
  }, [readKey, waiting.length])

  const state = hosts ? pushStateOf(hosts) : 'unknown'
  const press = async () => {
    if (!hosts || busy) return
    setBusy(true)
    setErrors({})
    const results = await writePushAll(state !== 'on', hosts)
    setErrors(Object.fromEntries(results.filter((r) => !r.ok && !r.queued).map((r) => [r.hostConfigId, r.error ?? ''])))
    await reread()
    setBusy(false)
  }

  const aliasOf = (id: string) => targets.find((x) => x.hostConfigId === id)?.alias ?? id
  const queuedHosts = waiting.filter((id) => targets.some((x) => x.hostConfigId === id))

  // A page of a host with no daemon: who delivers its alerts. The deliverer is the daemon whose `/api/info.wwdalerts` says so; unread, the answer is the usual order.
  const noDaemon = host.daemonHostId === null
  const deliverer = targets.find((x) => byHost[x.hostConfigId]?.wwdAlerts?.deliverer === true)
  const other = deliverer ? targets.find((x) => x.hostConfigId !== deliverer.hostConfigId) : undefined

  return (
    <Section title={t('watch.push.title')}>
      <div data-testid="push-switch">
        <SettingItem label={t('watch.push.switch')} description={t('watch.push.desc')} disabled={busy || hosts === null}>
          <div className="flex flex-col items-end gap-1">
            <ToggleSwitch label={t('watch.push.switch')} checked={state === 'on'} testId="push-toggle" onChange={() => void press()} />
            {state === 'partial' && hosts && (
              <span data-testid="push-partial" className="text-xs text-status-warning-text">
                {t('watch.push.partial')}：{hosts.filter((h) => h.on !== null).map((h) => `${h.alias}：${h.on ? t('watch.push.on') : t('watch.push.off')}`).join('・')}
              </span>
            )}
            {state === 'unknown' && hosts && <span data-testid="push-unknown" className="text-xs text-text-muted">{t('watch.push.unknown')}</span>}
          </div>
        </SettingItem>
        {Object.entries(errors).map(([id, reason]) => (
          <p key={id} data-testid={`push-error-${id}`} role="alert" className="mb-1 text-xs text-status-error-text">{t('watch.push.failed', { alias: aliasOf(id), reason })}</p>
        ))}
        {queuedHosts.length > 0 && (
          <p data-testid="push-queued" role="status" className="mb-1 text-xs text-text-muted">{t('watch.push.queued', { n: queuedHosts.length, hosts: queuedHosts.map(aliasOf).join('、') })}</p>
        )}
        {noDaemon && (
          <p data-testid="push-no-daemon" className="mb-1 text-xs text-text-muted">
            {deliverer && other
              ? t('watch.push.no_daemon', { alias: host.alias, deliverer: deliverer.alias, other: other.alias })
              : t('watch.push.no_daemon_fallback', { alias: host.alias })}
          </p>
        )}
      </div>
    </Section>
  )
}
