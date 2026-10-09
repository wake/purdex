// The Hosts page's 已配對的手機 (QR pairing spec §2 step 4, §3.4, §4.3): one card per paired phone (pairing_id) across
// every connected host that has a token. 撤銷 revokes the pairing on every host that has rows of it; a host that cannot be
// reached gets a pending revocation (usePendingRevocationsStore), retried by lib/pending-revocation-retry.ts. Rows carry
// no token, and nothing here renders one.
//
// A pending entry is persisted, so its card is built from the entry (label, host name, createdAt) and merged with the live
// list by pairing_id: the card survives unmount and reload for as long as the entry does. An entry that cannot be matched to
// the daemon it was made on (host removed / re-pointed / legacy) is shown as needing attention, and only the user's
// confirmed 放棄追蹤 removes it. The definite "no phone is paired" line is shown only when every host with an admin token
// was listed and nothing is pending; otherwise the section says it cannot confirm.
import { useEffect, useMemo, useRef, useState } from 'react'
import { useHostStore, type HostConfig } from '../../stores/useHostStore'
import { useDateLocale, useI18nStore } from '../../stores/useI18nStore'
import { usePendingRevocationsStore, type PendingRevocation } from '../../stores/usePendingRevocationsStore'
import { useHostLookResolver } from '../../lib/host-look'
import { listDevices, revokePairing, type DeviceRow } from '../../lib/devices-api'
import { groupPairedPhones } from '../../lib/paired-phones'
import { resolvePendingTarget, retryPendingRevocations } from '../../lib/pending-revocation-retry'

type Listed = ReturnType<typeof groupPairedPhones>[number]
/** A live card, or one rebuilt from pending entries alone (`state: null`: nothing is known of the rows). */
type PairedPhone = Omit<Listed, 'state'> & { state: Listed['state'] | null }
type HostRows = { hostId: string; rows: DeviceRow[] }
type Loaded = { perHost: HostRows[]; unreachable: number; at: number }

async function fetchAll(ids: string[]): Promise<Loaded> {
  const results = await Promise.all(ids.map(async (hostId) => ({ hostId, r: await listDevices(hostId) })))
  const perHost: HostRows[] = []
  let unreachable = 0
  for (const { hostId, r } of results) {
    if (r.kind === 'ok') perHost.push({ hostId, rows: r.rows })
    else if (r.reason !== 'unsupported') unreachable++
  }
  return { perHost, unreachable, at: Date.now() }
}

function hasToken(h: HostConfig): boolean {
  return typeof h.token === 'string' && h.token !== ''
}

export function PairedPhonesSection() {
  const t = useI18nStore((s) => s.t)
  const dateLocale = useDateLocale()
  const lookOf = useHostLookResolver()
  const hosts = useHostStore((s) => s.hosts)
  const hostOrder = useHostStore((s) => s.hostOrder)
  const runtime = useHostStore((s) => s.runtime)
  const pending = usePendingRevocationsStore((s) => s.items)

  const eligibleIds = hostOrder.filter((id) => hosts[id] && hasToken(hosts[id]) && runtime[id]?.status === 'connected')
  const eligibleKey = JSON.stringify(eligibleIds)

  const [loaded, setLoaded] = useState<Loaded | null>(null)
  const [revoking, setRevoking] = useState<ReadonlySet<string>>(new Set())
  const mounted = useRef(true)
  useEffect(() => {
    mounted.current = true
    return () => {
      mounted.current = false
    }
  }, [])

  // Load when the set of reachable hosts changes (and on mount), and again when `reloadTick` says a host's rows changed.
  const [reloadTick, setReloadTick] = useState(0)
  useEffect(() => {
    let live = true
    void fetchAll(JSON.parse(eligibleKey) as string[]).then((r) => {
      if (live) setLoaded(r)
    })
    return () => {
      live = false
    }
  }, [eligibleKey, reloadTick])

  // Retry the pending revocations once on mount (later retries are the host-connected subscription's).
  useEffect(() => {
    void retryPendingRevocations()
  }, [])

  // A cleared pending revocation means a host now has fewer rows of that phone: reload.
  useEffect(
    () =>
      usePendingRevocationsStore.subscribe((next, prev) => {
        if (next.items.length < prev.items.length) setReloadTick((n) => n + 1)
      }),
    [],
  )

  const listed = useMemo(() => (loaded ? groupPairedPhones(loaded.perHost, loaded.at) : []), [loaded])
  const phones = useMemo<PairedPhone[]>(() => {
    const ids = new Set(listed.map((p) => p.pairingId))
    const fromPending = new Map<string, PairedPhone>()
    for (const item of pending) {
      if (ids.has(item.pairingId)) continue
      const cur = fromPending.get(item.pairingId)
      if (cur) {
        if (cur.label === '' && item.label) cur.label = item.label
        if (item.createdAt !== undefined && item.createdAt < (cur.createdAt || Infinity)) cur.createdAt = item.createdAt
        continue
      }
      fromPending.set(item.pairingId, {
        pairingId: item.pairingId, label: item.label ?? '', createdAt: item.createdAt ?? 0,
        firstUsedAt: 0, lastUsedAt: 0, state: null, perHost: [],
      })
    }
    const kept = [...fromPending.values()].sort((a, b) => b.createdAt - a.createdAt)
    return [...listed, ...kept]
  }, [listed, pending])

  const nameOf = (id: string) => (hosts[id] ? (lookOf(id).name ?? id) : id)
  const labelOf = (p: PairedPhone) => (p.label === '' ? t('hosts.pairedPhones.unknown_label') : p.label)
  const timeText = (ms: number) => new Date(ms).toLocaleString(dateLocale)

  // Every configured host with an admin token is a target, whether or not it listed rows of the phone: the revoke is
  // idempotent, and a host that failed to list (or is offline right now) may still hold the pairing.
  const targetIds = hostOrder.filter((id) => hosts[id] && hasToken(hosts[id]))
  const tokenlessIds = hostOrder.filter((id) => hosts[id] && !hasToken(hosts[id]))

  const handleRevoke = async (phone: PairedPhone) => {
    const { pairingId } = phone
    const label = labelOf(phone)
    const tokenless = tokenlessIds.map(nameOf).join(', ')
    const msg =
      tokenless === ''
        ? t('hosts.pairedPhones.revoke_confirm', { label })
        : t('hosts.pairedPhones.revoke_confirm_no_token', { label, hosts: tokenless })
    if (!window.confirm(msg)) return
    setRevoking((s) => new Set(s).add(pairingId))
    const results = await Promise.all(
      targetIds.map(async (hostId) => {
        if (useHostStore.getState().runtime[hostId]?.status !== 'connected') return { hostId, done: false }
        const r = await revokePairing(hostId, pairingId)
        return { hostId, done: r.kind === 'ok' || r.kind === 'unsupported' }
      }),
    )
    const done = new Set<string>()
    for (const { hostId, done: ok } of results) {
      if (ok) done.add(hostId)
      else {
        usePendingRevocationsStore.getState().add(hostId, pairingId, { label })
      }
    }
    if (!mounted.current) return
    setRevoking((s) => {
      const next = new Set(s)
      next.delete(pairingId)
      return next
    })
    setLoaded((cur) =>
      cur && {
        ...cur,
        perHost: cur.perHost.map((h) => (done.has(h.hostId) ? { ...h, rows: h.rows.filter((r) => r.pairing_id !== pairingId) } : h)),
      },
    )
  }

  const handleAbandon = (item: PendingRevocation, label: string, host: string) => {
    if (!window.confirm(t('hosts.pairedPhones.abandon_confirm', { label, host }))) return
    usePendingRevocationsStore.getState().remove(item.hostId, item.pairingId)
  }

  // Hosts with an admin token that this section could not list (offline) or failed to list.
  const offlineManageable = targetIds.filter((id) => runtime[id]?.status !== 'connected').length
  const uncertainCount = (loaded?.unreachable ?? 0) + offlineManageable

  return (
    <div className="max-w-2xl space-y-4">
      <h2 className="text-lg font-semibold">{t('hosts.pairedPhones.title')}</h2>

      {loaded && loaded.unreachable > 0 && (
        <p data-testid="paired-unreachable" className="text-xs text-yellow-400">
          {t('hosts.pairedPhones.unreachable', { count: loaded.unreachable })}
        </p>
      )}

      {loaded === null && <p className="text-sm text-text-muted">{t('hosts.pairedPhones.loading')}</p>}
      {loaded !== null && phones.length === 0 && uncertainCount === 0 && (
        <p data-testid="paired-empty" className="text-sm text-text-muted">{t('hosts.pairedPhones.empty')}</p>
      )}
      {loaded !== null && phones.length === 0 && uncertainCount > 0 && (
        <p data-testid="paired-unconfirmed" className="text-sm text-yellow-400">{t('hosts.pairedPhones.unconfirmed', { count: uncertainCount })}</p>
      )}

      <ul className="space-y-3">
        {phones.map((p) => {
          const hostIds = [...new Set(p.perHost.map((h) => h.hostId))]
          const entries = pending
            .filter((i) => i.pairingId === p.pairingId)
            .map((item) => {
              const target = resolvePendingTarget(item)
              // A re-pointed or removed host id no longer names the daemon the entry was made on: show the stored name.
              const live = hosts[item.hostId] && target.kind !== 'repointed'
              return { item, target, name: live ? nameOf(item.hostId) : (item.hostName ?? item.hostId) }
            })
          const pendingHosts = entries.filter((e) => e.target.kind === 'retry').map((e) => e.name)
          const attention = entries.filter((e) => e.target.kind !== 'retry')
          return (
            <li key={p.pairingId} data-testid={`paired-phone-${p.pairingId}`} className="border border-border-default rounded-lg p-4 space-y-1 bg-surface-secondary">
              <div className="flex items-center justify-between gap-3">
                <div className="min-w-0">
                  <span className="text-sm font-medium text-text-primary truncate block">{labelOf(p)}</span>
                  {p.state !== null && (
                    <span data-testid="paired-state" className="text-xs text-text-secondary">{t(`hosts.pairedPhones.state.${p.state}`)}</span>
                  )}
                </div>
                <button
                  onClick={() => void handleRevoke(p)}
                  disabled={revoking.has(p.pairingId)}
                  className="px-3 py-1.5 rounded text-xs border border-red-400/50 text-red-400 hover:bg-red-400/10 cursor-pointer disabled:opacity-50"
                >
                  {t('hosts.pairedPhones.revoke')}
                </button>
              </div>
              {p.firstUsedAt > 0 && (
                <p data-testid="paired-first" className="text-xs text-text-muted">{t('hosts.pairedPhones.first_used', { time: timeText(p.firstUsedAt) })}</p>
              )}
              {p.lastUsedAt > 0 && (
                <p data-testid="paired-last" className="text-xs text-text-muted">{t('hosts.pairedPhones.last_used', { time: timeText(p.lastUsedAt) })}</p>
              )}
              {p.state === null && p.createdAt > 0 && (
                <p data-testid="paired-created" className="text-xs text-text-muted">{t('hosts.pairedPhones.created_at', { time: timeText(p.createdAt) })}</p>
              )}
              {hostIds.length > 0 && (
                <p data-testid="paired-hosts" className="text-xs text-text-muted">
                  {t('hosts.pairedPhones.hosts', { hosts: hostIds.map(nameOf).join(', ') })}
                </p>
              )}
              {pendingHosts.length > 0 && (
                <p data-testid="paired-pending" className="text-xs text-yellow-400">
                  {t('hosts.pairedPhones.pending', { hosts: pendingHosts.join(', ') })}
                </p>
              )}
              {attention.map(({ item, target, name }) => (
                <div key={item.hostId} data-testid="paired-attention" className="flex items-center justify-between gap-3 text-xs text-yellow-400">
                  <span>{t(`hosts.pairedPhones.attention.${target.kind === 'retry' ? 'removed' : target.kind}`, { host: name })}</span>
                  <button
                    onClick={() => handleAbandon(item, labelOf(p), name)}
                    className="shrink-0 px-2 py-1 rounded border border-border-default text-text-secondary hover:bg-surface-hover cursor-pointer"
                  >
                    {t('hosts.pairedPhones.abandon')}
                  </button>
                </div>
              ))}
              {tokenlessIds.length > 0 && (
                <p data-testid="paired-no-token" className="text-xs text-yellow-400">
                  {t('hosts.pairedPhones.no_token', { hosts: tokenlessIds.map(nameOf).join(', ') })}
                </p>
              )}
            </li>
          )
        })}
      </ul>
    </div>
  )
}
