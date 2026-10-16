// spa/src/components/watch/QueueLane.tsx — one class's lane of the host tab's queue (watcher area spec §6.1, ported from the
// prototype's Queue.tsx): the name and 「名額 used / total」, one card per slot, the gate line, then the waiting cards.
import { useI18nStore } from '../../stores/useI18nStore'
import type { FleetHolder, FleetWaiter } from '../../lib/fleet/types'
import { mss, WAIT_LONG_S, holderKey, waiterKey, type QueueClass } from '../../lib/watch/queue-time'


type Gate = 'open' | 'wait' | 'held' | 'paused'

const W_HEAVY = 'w-[176px]'
const W_LIGHT = 'w-[148px]'
const size = (small: boolean) => (small ? `h-6 ${W_LIGHT}` : `h-7 ${W_HEAVY}`)
const ring = (hot: boolean) => (hot ? 'ring-1 ring-accent' : '')

interface Hover { hot: string | null; setHot: (k: string | null) => void }

function RunningCard({ h, now, small, hover }: { h: FleetHolder; now: number; small: boolean; hover: Hover }) {
  const k = holderKey(h)
  const hot = hover.hot === k
  return (
    <div
      data-testid="q-holder" data-hot={hot || undefined} title={h.command}
      onMouseEnter={() => hover.setHot(k)} onMouseLeave={() => hover.setHot(null)}
      className={`shrink-0 flex items-center gap-2 ${size(small)} px-2 rounded-md border border-accent/50 bg-accent/10 text-xs font-mono cursor-default ${ring(hot)}`}
    >
      <span className="flex-1 min-w-0 truncate text-text-primary">{h.command}</span>
      <span className="shrink-0 tabular-nums text-text-muted">{mss(h.since, now)}</span>
    </div>
  )
}

function WaitingCard({ w, now, small, hover }: { w: FleetWaiter; now: number; small: boolean; hover: Hover }) {
  const k = waiterKey(w)
  const hot = hover.hot === k
  const long = w.since !== null && (now - w.since) / 1000 > WAIT_LONG_S
  return (
    <div
      data-testid="q-waiting" data-hot={hot || undefined} title={w.command}
      onMouseEnter={() => hover.setHot(k)} onMouseLeave={() => hover.setHot(null)}
      className={`shrink-0 flex items-center gap-1.5 ${size(small)} px-2 rounded-md border border-border-default bg-surface-secondary text-xs font-mono cursor-default ${ring(hot)}`}
    >
      <span data-testid="q-place" className="shrink-0 font-sans text-[10px] tabular-nums text-text-muted">{w.place}</span>
      <span className="flex-1 min-w-0 truncate text-text-primary">{w.command}</span>
      <span data-testid="q-wait" className={`shrink-0 text-[10px] tabular-nums ${long ? 'text-status-warning-text' : 'text-text-muted'}`}>{mss(w.since, now)}</span>
    </div>
  )
}

/**
 * The gate: a 1 px line and, beside it, why it holds. Admission held (the watcher's or the gate's own hold): 「暫停放行」 in
 * status-error, the daemon's reason as its title. A class paused at 0 slots: 「暫停（新指令排隊）」 in status-warning. Slots full:
 * 「等名額」. With a slot free and nothing paused it holds nobody, so it carries no word.
 */
function GateLine({ state, reason }: { state: Gate; reason: string }) {
  const t = useI18nStore((s) => s.t)
  const word = state === 'held' ? t('watch.queue.gate.held') : state === 'paused' ? t('watch.queue.gate.paused') : state === 'wait' ? t('watch.queue.gate.wait') : null
  const lineColor = state === 'held' ? 'bg-status-error' : state === 'paused' ? 'bg-status-warning' : 'bg-border-default'
  const wordColor = state === 'held' ? 'text-status-error-text' : state === 'paused' ? 'text-status-warning-text' : 'text-text-muted'
  return (
    <div data-testid="gate" data-state={state} title={state === 'held' && reason ? reason : undefined} className="shrink-0 flex items-center gap-1.5 min-w-[64px]">
      <span className={`w-px h-5 ${lineColor}`} />
      {word && <span className={`text-[10px] whitespace-nowrap ${wordColor}`}>{word}</span>}
    </div>
  )
}

interface LaneProps {
  cls: QueueClass
  used: number
  total: number
  paused: boolean
  held: boolean
  heldReason: string
  holders: FleetHolder[]
  waiters: FleetWaiter[]
  now: number
  hover: Hover
}

export function QueueLane({ cls, used, total, paused, held, heldReason, holders, waiters, now, hover }: LaneProps) {
  const t = useI18nStore((s) => s.t)
  const small = cls === 'light'
  const isPaused = paused || total === 0
  // Slots are drawn by position: the holders first, then free ones up to the total (a lane never hides a running command).
  const slots = Math.max(total, holders.length)
  const full = !isPaused && holders.length >= total
  // Gate line priority: admission_held > paused > full. The spec does not order them; admission_held is queue-wide (the
  // watcher's or the gate's own hold stops everything, whatever a class's slots say), as the prototype's `blocked` does.
  // A lane that is paused AND held still shows 「已暫停」 beside its name; only the gate line's word follows the rule.
  const gate: Gate = held ? 'held' : isPaused ? 'paused' : full ? 'wait' : 'open'
  return (
    <div data-testid={`lane-${cls}`} className={`flex items-start gap-2.5 ${small ? 'py-1.5' : 'py-2'}`}>
      <div className="w-28 shrink-0">
        <div className="flex items-center gap-1.5">
          <span className={small ? 'text-xs text-text-secondary' : 'text-sm font-medium text-text-primary'}>{t(`watch.queue.lane.${cls}`)}</span>
          {isPaused && <span className="text-[10px] text-status-warning-text">{t('watch.queue.paused')}</span>}
        </div>
        <div className="text-[10px] text-text-muted tabular-nums">{t('watch.queue.slots', { used, total })}</div>
      </div>
      <div className="flex-1 min-w-0 flex flex-col gap-1.5">
        <div data-testid={`slot-line-${cls}`} className="flex flex-wrap items-center gap-1.5 min-w-0">
          {Array.from({ length: slots }).map((_, i) => {
            const h = holders[i]
            return h
              ? <RunningCard key={holderKey(h)} h={h} now={now} small={small} hover={hover} />
              : <div key={`free${i}`} data-testid="q-free" className={`shrink-0 ${size(small)} rounded-md border border-dashed border-border-default grid place-items-center text-[10px] text-text-muted`}>{t('watch.queue.free')}</div>
          })}
          <GateLine state={gate} reason={heldReason} />
        </div>
        <div className="flex flex-wrap items-center gap-1.5 min-w-0" data-testid={`queue-line-${cls}`}>
          {waiters.length === 0 && <span className="text-xs text-text-muted">{t('watch.queue.none')}</span>}
          {waiters.map((w) => <WaitingCard key={`${waiterKey(w)}-${w.place}`} w={w} now={now} small={small} hover={hover} />)}
        </div>
      </div>
    </div>
  )
}
