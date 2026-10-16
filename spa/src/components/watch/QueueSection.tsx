// spa/src/components/watch/QueueSection.tsx — the host tab's 重指令排隊 section (watcher area spec §6.1), daemon hosts only. A
// 當下｜歷史 switch (歷史 stays disabled until W5d, its choice is kept in view memory), 「排隊 n · 最久 m:ss」, then QueueNow.
// Offline: the one sentence. One interval counts every elapsed / wait time locally from `since` and is cleared on unmount.
import { useEffect, useState } from 'react'
import { useI18nStore } from '../../stores/useI18nStore'
import { Section } from '../hosts/form-fields'
import { SegmentControl } from '../settings/SegmentControl'
import type { FleetQueue } from '../../lib/fleet/types'
import { mss, secondsSince } from '../../lib/watch/queue-time'
import { readQueueTab, writeQueueTab, type QueueTab } from '../../lib/watch/view-memory'
import { QueueNow } from './QueueNow'

// One interval, only while `active` (the 當下 body is shown); cleared on unmount or when it stops being active.
function useNowEverySecond(active: boolean): number {
  const [now, setNow] = useState(() => Date.now())
  useEffect(() => {
    if (!active) return
    const id = setInterval(() => setNow(Date.now()), 1000)
    return () => clearInterval(id)
  }, [active])
  return now
}

function QueueBody({ fleetHostId, queue }: { fleetHostId: string; queue: FleetQueue }) {
  const t = useI18nStore((s) => s.t)
  // The tab outlives this component (the pane unmounts on a tab switch): the memory is the source, the state only re-renders.
  const [tab, setTabState] = useState<QueueTab>(() => readQueueTab(fleetHostId))
  const setTab = (v: QueueTab) => { writeQueueTab(fleetHostId, v); setTabState(v) }
  const now = useNowEverySecond(tab === 'now')
  // When this frame arrived: with no `since` to count from, the daemon's figure keeps ticking from here.
  // (Stamped with the section's own clock, at most a second old, so render stays pure.)
  const [arrived, setArrived] = useState({ queue, at: now })
  if (arrived.queue !== queue) setArrived({ queue, at: now })
  const n = queue.waiters.length
  const known = queue.waiters.map((w) => secondsSince(w.since, now)).filter((s): s is number => s !== null)
  // The local clock where the daemon gave `since`, never below the daemon's figure (a waiter without `since` cannot shrink it);
  // when none has a `since`, the daemon's figure plus the time since its frame arrived.
  const longest = known.length > 0
    ? Math.max(...known, queue.longest_wait_s)
    : queue.longest_wait_s + Math.max(0, (now - arrived.at) / 1000)
  const num = n > 0 ? 'text-status-warning-text font-semibold' : ''
  return (
    <div data-testid="queue-panel">
      <div className="flex items-center gap-3 mb-2 text-xs">
        <span data-testid="queue-tabs" role="group" aria-label={t('watch.queue.tabs_label')}>
          <SegmentControl
            options={[{ value: 'now', label: t('watch.queue.tab.now') }, { value: 'history', label: t('watch.queue.tab.history'), disabled: true }]}
            value={tab} onChange={setTab}
          />
        </span>
        <span data-testid="queue-count" className="text-xs text-text-secondary tabular-nums">
          <span className={num}>{t('watch.queue.count', { n })}</span>
          {n > 0 && <span className={num}>{t('watch.queue.longest', { t: mss(now - longest * 1000, now) })}</span>}
        </span>
      </div>
      {/* 歷史 is W5d's; until then it cannot be picked, and a remembered one shows no body. */}
      {tab === 'now' && <QueueNow queue={queue} now={now} />}
    </div>
  )
}

export function QueueSection({ fleetHostId, queue, offline }: { fleetHostId: string; queue: FleetQueue | undefined; offline: boolean }) {
  const t = useI18nStore((s) => s.t)
  return (
    <Section title={t('watch.queue.title')}>
      {offline || !queue
        ? <p className="text-xs text-text-muted">{t('watch.queue.offline')}</p>
        : <QueueBody fleetHostId={fleetHostId} queue={queue} />}
    </Section>
  )
}
