// spa/src/components/watch/AvailRow.tsx — the host tab's 「可接工作」 row (plan WWD-7, 7a-4a): worker, what the host takes (and why not),
// sessions room, owner and the exceptions in force. A bordered info row, not a gauge: these are judgements, not readings.
import type { ReactNode } from 'react'
import { useI18nStore } from '../../stores/useI18nStore'
import type { PoolCellExtra } from '../../lib/pool/fleet-bridge'
import type { PoolException } from '../../lib/pool/types'
import { fallbackWord, headroomReasonLines } from '../../lib/pool/words'

function Item({ id, label, children }: { id: string; label: string; children: ReactNode }) {
  return (
    <div data-testid={`avail-${id}`} className="min-w-0">
      <div className="text-[11px] text-text-muted">{label}</div>
      <div className="text-sm text-text-primary">{children}</div>
    </div>
  )
}

const hhmm = (ms: number) => new Date(ms).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' })

/** `at` is the facts' own time (the daemon's clock): an exception is in force from its start to its end by that time, whatever this App's clock says. */
export function AvailRow({ pool, exceptions: all, at }: { pool: PoolCellExtra; exceptions: PoolException[]; at: number }) {
  const t = useI18nStore((s) => s.t)
  if (pool.state === 'offline') return null
  if (pool.state === 'fallback') {
    return (
      <div data-testid="avail-row" className="mb-5 rounded-lg border border-border-subtle px-4 py-3 text-sm text-text-muted">
        {fallbackWord(t, pool.reason, pool.since)}
      </div>
    )
  }
  const unknown = t('pool.card.unknown')
  const exceptions = all.filter((e) => e.since <= at && (e.until === null || e.until > at))
  const w = pool.worker
  const reasons = headroomReasonLines(pool.headroom, pool.reasons, t)
  return (
    <div data-testid="avail-row" className="mb-5 rounded-lg border border-border-subtle px-4 py-3">
      <div className="text-xs text-text-secondary mb-2">{t('watch.host.avail.title')}</div>
      <div className="flex flex-wrap gap-x-10 gap-y-3">
        <Item id="worker" label={t('pool.card.worker')}>
          {!w ? unknown : w.enabled ? t('pool.card.worker_on', { hr: w.running.heavy, hs: w.slots.heavy, lr: w.running.light, ls: w.slots.light }) : t('pool.card.worker_off')}
        </Item>
        <Item id="headroom" label={t('pool.card.headroom')}>
          {pool.headroom ? t('pool.card.headroom_value', { h: pool.headroom.heavy, l: pool.headroom.light }) : unknown}
          {reasons.map((r) => <span key={r} data-testid="avail-reason" className="block text-xs text-text-muted">{r}</span>)}
        </Item>
        {pool.sessionsRoom !== null && <Item id="sessions" label={t('pool.card.sessions')}>{t('pool.card.sessions_value', { n: pool.sessionsRoom })}</Item>}
        {pool.owner && <Item id="owner" label={t('pool.card.owner')}>{t(pool.owner.active ? 'pool.card.owner_active' : 'pool.card.owner_idle', { s: pool.owner.idle_s })}</Item>}
        {exceptions.length > 0 && (
          <div data-testid="avail-exceptions" className="min-w-0">
            <div className="text-[11px] text-text-muted">{t('pool.pause.title')}</div>
            {exceptions.map((e) => (
              <div key={e.name} data-testid={`avail-exception-${e.name}`} className="text-sm text-text-primary">
                {e.name} <span className="text-xs text-text-muted">{e.until !== null ? t('watch.host.avail.until', { time: hhmm(e.until) }) : t('watch.host.avail.no_end')}</span>
              </div>
            ))}
          </div>
        )}
      </div>
    </div>
  )
}
