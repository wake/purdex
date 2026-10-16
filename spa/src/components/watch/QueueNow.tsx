// spa/src/components/watch/QueueNow.tsx — 當下: the two lanes and the table under them (watcher area spec §6.1). `now` comes from the
// section's one clock; a card and its table row light each other on hover (the pairing key lives here).
import { useState } from 'react'
import { useI18nStore } from '../../stores/useI18nStore'
import type { FleetQueue } from '../../lib/fleet/types'
import { mss, secondsSince, WAIT_LONG_S, holderKey, waiterKey, type QueueClass } from '../../lib/watch/queue-time'
import { QueueLane } from './QueueLane'

const TH = 'font-normal py-1 pr-3 border-b border-border-subtle text-left whitespace-nowrap'
const TD = 'py-1 pr-3 border-b border-border-subtle/50'
const CLASSES: QueueClass[] = ['heavy', 'light']

export function QueueNow({ queue: q, now }: { queue: FleetQueue; now: number }) {
  const t = useI18nStore((s) => s.t)
  const [hot, setHot] = useState<string | null>(null)
  const hover = { hot, setHot }
  const waitersOf = (c: QueueClass) => q.waiters.filter((w) => w.class === c).sort((a, b) => a.place - b.place)
  const waiting = CLASSES.flatMap(waitersOf)
  const rowHot = (k: string) => (hot === k ? 'bg-accent-muted' : '')
  const group = (label: string) => <tr><td colSpan={5} className="pt-2.5 pb-1 text-[10.5px] text-text-muted">{label}</td></tr>
  const cls = (c: string) => <span className={c === 'heavy' ? 'text-text-secondary' : 'text-text-muted'}>{t(`watch.queue.class.${c === 'heavy' ? 'heavy' : 'light'}`)}</span>
  const laneProps = (c: QueueClass) => ({
    cls: c, now, hover,
    used: c === 'heavy' ? q.heavy_used : q.light_used,
    total: c === 'heavy' ? q.heavy_total : q.light_total,
    paused: c === 'heavy' ? q.heavy_paused : q.light_paused,
    held: q.admission_held, heldReason: q.admission_reason,
    holders: q.holders.filter((h) => h.class === c),
    waiters: waitersOf(c),
  })
  return (
    <div data-testid="queue-now">
      <div data-testid="queue-lanes" className="divide-y divide-border-subtle/60">
        <QueueLane {...laneProps('heavy')} />
        <QueueLane {...laneProps('light')} />
      </div>
      <table data-testid="queue-table" className="mt-3 w-full border-collapse text-xs">
        <thead>
          <tr className="text-text-muted">
            <th className={`${TH} w-14`}>{t('watch.queue.col_class')}</th><th className={TH}>{t('watch.queue.col_command')}</th>
            <th className={`${TH} w-48`}>{t('watch.queue.col_project')}</th><th className={`${TH} w-32`}>{t('watch.queue.col_time')}</th>
            <th className={`${TH} w-28`}>{t('watch.queue.col_lock')}</th>
          </tr>
        </thead>
        <tbody>
          {group(t('watch.queue.group_running'))}
          {q.holders.map((h) => (
            <tr key={holderKey(h)} data-testid="q-row" data-hot={hot === holderKey(h) || undefined} onMouseEnter={() => setHot(holderKey(h))} onMouseLeave={() => setHot(null)} className={rowHot(holderKey(h))}>
              <td className={TD}>{cls(h.class)}</td>
              <td className={`${TD} font-mono text-text-primary`}>{h.command}</td>
              <td className={`${TD} text-text-secondary`}>{h.project}</td>
              <td className={`${TD} tabular-nums text-text-secondary`}>{t('watch.queue.ran', { t: mss(h.since, now) })}</td>
              <td className={`${TD} tabular-nums text-text-muted`}>{t('watch.queue.pid', { pid: h.pid })}</td>
            </tr>
          ))}
          {group(t('watch.queue.group_waiting', { n: waiting.length }))}
          {waiting.length === 0 && <tr><td colSpan={5} className={`${TD} text-text-muted`}>{t('watch.queue.none')}</td></tr>}
          {waiting.map((w) => {
            const s = secondsSince(w.since, now)
            return (
              <tr key={waiterKey(w)} data-testid="q-row" data-hot={hot === waiterKey(w) || undefined} onMouseEnter={() => setHot(waiterKey(w))} onMouseLeave={() => setHot(null)} className={rowHot(waiterKey(w))}>
                <td className={TD}>{cls(w.class)}</td>
                <td className={`${TD} font-mono text-text-primary`}><span className="font-sans text-text-muted tabular-nums mr-2">{t('watch.queue.place', { n: w.place })}</span>{w.command}</td>
                <td className={`${TD} text-text-secondary`}>{w.project}</td>
                <td className={`${TD} tabular-nums ${s !== null && s > WAIT_LONG_S ? 'text-status-warning-text' : 'text-text-secondary'}`}>{t('watch.queue.waited', { t: mss(w.since, now) })}</td>
                <td className={TD} />
              </tr>
            )
          })}
        </tbody>
      </table>
    </div>
  )
}
