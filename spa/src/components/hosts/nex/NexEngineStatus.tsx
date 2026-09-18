import { useEffect, useState } from 'react'
import { ArrowsClockwise } from '@phosphor-icons/react'
import { useI18nStore } from '../../../stores/useI18nStore'
import { useNexHostStore } from '../../../stores/useNexHostStore'
import { fetchNexHost } from '../../../lib/nex/nex-api'
import type { NexHostInfo } from '../../../lib/nex/types'
import type { NexInfo } from '../../../lib/host-api'
import { Field } from '../form-fields'
import { isNexReady } from './nex-ready'

export interface NexEngineStatusProps {
  hostId: string
  info: NexInfo | null
  onRefresh: () => void
}

type BadgeState = 'disabled' | 'not_running' | 'unavailable' | 'ready'

function badgeState(info: NexInfo | null): BadgeState {
  if (!info || !info.configured) return 'disabled'
  if (!info.mounted) return 'not_running'
  if (!isNexReady(info)) return 'unavailable'
  return 'ready'
}

const BADGE_CLASSES: Record<BadgeState, string> = {
  disabled: 'bg-surface-secondary text-text-muted',
  not_running: 'bg-amber-500/10 text-amber-400',
  unavailable: 'bg-red-500/10 text-red-400',
  ready: 'bg-green-500/10 text-green-400',
}

function QuotaBar({ pct, unknownLabel }: { pct: number | null; unknownLabel: string }) {
  if (pct === null) {
    return <span className="text-sm text-text-muted">{unknownLabel}</span>
  }
  const clamped = Math.min(100, Math.max(0, pct))
  return (
    <div className="flex items-center gap-2 max-w-xs">
      <div className="flex-1 h-1.5 bg-surface-secondary rounded overflow-hidden">
        <div className="h-full bg-accent" style={{ width: `${clamped}%` }} />
      </div>
      <span className="text-xs text-text-secondary w-12 text-right">{pct}%</span>
    </div>
  )
}

export default function NexEngineStatus({ hostId, info, onRefresh }: NexEngineStatusProps) {
  const t = useI18nStore((s) => s.t)
  const [tick, setTick] = useState(0)
  const [host, setHost] = useState<NexHostInfo | null>(null)
  // Capabilities (phase, roots, providers) come from the shared readiness
  // store (spec §4.1) — already scoped per host, null while the engine is
  // unavailable — so the card renders whatever it holds and only asks it to
  // `ensure`. The account/quota card is not part of readiness and stays a
  // local fetch.
  const caps = useNexHostStore((s) => s.byHost[hostId]?.capabilities ?? null)
  const ensure = useNexHostStore((s) => s.ensure)

  const ready = isNexReady(info)

  // Clear the previous fetch's host the moment a new one starts (host
  // switch, ready flip, Refresh) — using the render-time adjust-state idiom
  // so the effect below only fetches — so another host's account is never
  // shown while this host's request is pending or after it fails.
  const fetchKey = `${hostId}:${ready}:${tick}`
  const [prevFetchKey, setPrevFetchKey] = useState(fetchKey)
  if (fetchKey !== prevFetchKey) {
    setPrevFetchKey(fetchKey)
    setHost(null)
  }

  useEffect(() => {
    if (!ready) return
    let cancelled = false
    void ensure(hostId)
    fetchNexHost(hostId)
      .then((h) => {
        if (cancelled) return
        setHost(h)
      })
      .catch((err) => {
        // Nexen can be down (network, 503) even when `info.ready` said it
        // was up moments ago — the card degrades to empty rows, never throws.
        if (cancelled) return
        setHost(null)
        console.warn('NexEngineStatus: failed to load Nexen host', err)
      })
    return () => {
      cancelled = true
    }
  }, [hostId, ready, tick, ensure])

  const state = badgeState(info)

  // `onRefresh` is the page's refresh, which invalidates the store entry
  // (one refetch per click — the card must not invalidate it a second time);
  // the tick refetches the account card and re-ensures, a no-op while that
  // refetch is in flight.
  const handleRefresh = () => {
    setTick((n) => n + 1)
    onRefresh()
  }

  return (
    <div className="max-w-2xl">
      <div className="flex items-center justify-between mb-3">
        <h3 className="text-sm font-semibold text-text-primary">{t('hosts.nex.status.title')}</h3>
        <button
          onClick={handleRefresh}
          className="flex items-center gap-1 text-xs text-text-secondary hover:text-accent cursor-pointer"
        >
          <ArrowsClockwise size={12} />
          {t('hosts.nex.status.refresh')}
        </button>
      </div>

      <div className="mb-3">
        <span data-testid="nex-status-badge" className={`inline-block px-2 py-0.5 rounded text-xs font-medium ${BADGE_CLASSES[state]}`}>
          {t(`hosts.nex.status.${state}`)}
        </span>
      </div>

      {state === 'unavailable' && info?.init_error && (
        <pre className="text-xs text-red-400 whitespace-pre-wrap bg-surface-secondary rounded p-2 mb-3">{info.init_error}</pre>
      )}

      {state === 'ready' && (
        <div>
          <Field label={t('hosts.nex.status.phase')}>
            <span className="text-sm text-text-primary">{caps ? `${caps.phase} · ${caps.host_id}` : '—'}</span>
          </Field>
          {host?.credential_warning && (
            <div
              data-testid="nex-credential-warning"
              className="text-sm text-amber-400 bg-amber-500/10 rounded p-2 mb-3"
            >
              {host.credential_warning}
            </div>
          )}
          <Field label={t('hosts.nex.status.account')}>
            <span className="text-sm text-text-primary">{host?.active_account || '—'}</span>
          </Field>
          {host?.credential_source && (
            <Field label={t('hosts.nex.status.credential_source')}>
              <span className="text-sm text-text-primary">
                {host.credential_source}
                {host.account_id ? ` · ${host.account_id}` : ''}
              </span>
            </Field>
          )}
          <Field label={t('hosts.nex.status.quota_5h')}>
            <QuotaBar pct={host?.quota?.five_hour_pct ?? null} unknownLabel={t('hosts.nex.status.quota_unknown')} />
          </Field>
          <Field label={t('hosts.nex.status.quota_7d')}>
            <QuotaBar pct={host?.quota?.seven_day_pct ?? null} unknownLabel={t('hosts.nex.status.quota_unknown')} />
          </Field>
          <Field label={t('hosts.nex.status.roots')}>
            <div className="text-sm text-text-primary space-y-0.5">
              {caps && caps.roots.length > 0 ? caps.roots.map((r) => <div key={r.path}>{r.path}</div>) : <span>—</span>}
            </div>
          </Field>
          <Field label={t('hosts.nex.status.profiles')}>
            <span className="text-sm text-text-primary">
              {info?.effective ? `${info.effective.default_profile} / ${info.effective.max_profile}` : '—'}
            </span>
          </Field>
          <Field label={t('hosts.nex.status.lease_ttl')}>
            <span className="text-sm text-text-primary">{info?.effective?.lease_ttl ?? '—'}</span>
          </Field>
          <Field label={t('hosts.nex.status.providers')}>
            <span className="text-sm text-text-primary">{caps && caps.providers.length > 0 ? caps.providers.join(', ') : '—'}</span>
          </Field>
          {info?.effective && (
            <Field label={t('hosts.nex.status.effective')}>
              <div className="text-xs text-text-muted space-y-0.5 font-mono">
                <div>{info.effective.data_dir}</div>
                <div>{info.effective.claude_bin || 'claude (via PATH)'}</div>
                <div>{info.effective.path_prefix}</div>
              </div>
            </Field>
          )}
        </div>
      )}
    </div>
  )
}
