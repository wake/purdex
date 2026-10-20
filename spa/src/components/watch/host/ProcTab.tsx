// spa/src/components/watch/host/ProcTab.tsx — the host page's 行程 tab (monitoring tabs spec M45, plan MON-8a/8b): the host's top 12 processes by CPU and by memory, side by side, a timeline over the last hour (one record every 15 s),
// and under them the resident programs. It reads the facts of a host that is ok (`facts.procs` is already the top 12 by each, `facts.residents` the programs a session left running); a host that is not ok has nothing to read, so the
// tables say it is offline and the timeline is off. The right column under the tables is for the undeclared heavy commands (MON-8c).
//
// Tab-hosted checklist: the picked record is the reader's own choice, so it lives in `lib/watch/view-memory` as the record's time (not a place on the timeline: the live end keeps moving), keyed by the pool host id.
import { useMemo, useState } from 'react'
import { useI18nStore } from '../../../stores/useI18nStore'
import type { MonitorHost } from '../../../lib/pool/use-monitor-hosts'
import { PROC_STEP_MS, type ProcSnap } from '../../../lib/pool/proc-history'
import { useProcHistory } from '../../../lib/pool/use-proc-history'
import { clock } from '../../../lib/pool/words'
import { readProcAt, writeProcAt } from '../../../lib/watch/view-memory'
import { ResidentsTable } from '../ResidentsTable'
import { ProcList, procRowsOf } from './ProcList'

/** The timeline's positions: the last hour, a step each; the last one is now. */
const STEPS = 240
const HOUR_MS = STEPS * PROC_STEP_MS

/** The record nearest to `at`, or null when there is none. */
function nearest(snaps: readonly ProcSnap[], at: number): ProcSnap | null {
  let best: ProcSnap | null = null
  for (const s of snaps) if (best === null || Math.abs(s.at - at) < Math.abs(best.at - at)) best = s
  return best
}

export function ProcTab({ host, now }: { host: MonitorHost; now: number }) {
  const t = useI18nStore((s) => s.t)
  const facts = host.state.state === 'ok' ? host.state.facts : null
  const hostId = host.hostId
  // The picked time outlives this component (the pane unmounts on a tab switch): the memory is the source, the state only re-renders. Another host is another pick, read again while rendering.
  const [picked, setPickedState] = useState<number | null>(() => readProcAt(hostId))
  const [seen, setSeen] = useState(hostId)
  if (seen !== hostId) { setSeen(hostId); setPickedState(readProcAt(hostId)) }
  const setPicked = (at: number | null) => { writeProcAt(hostId, at); setPickedState(at) }
  // A pick that has slid out of the hour (or of a host that is not ok) is the live record again.
  const at = facts !== null && picked !== null && now - picked <= HOUR_MS ? picked : null
  if (picked !== null && at === null) { writeProcAt(hostId, null); setPickedState(null) }

  const { snaps, load } = useProcHistory(hostId, host.alias, facts, at !== null)
  // The record of a time is one within two steps of it (the 5 s records thin to 15 s, give or take): a gap in the hour has none, and while the hour is still being read the memory may hold only the live records that joined it.
  const near = at === null ? null : nearest(snaps, at)
  const snap = near !== null && at !== null && Math.abs(near.at - at) <= 2 * PROC_STEP_MS ? near : null
  const live = at === null
  const rows = useMemo(() => {
    const procs = live ? (facts?.procs ?? []) : (snap?.procs ?? [])
    return { cpu: procRowsOf(procs, 'cpu'), mem: procRowsOf(procs, 'mem') }
  }, [live, facts, snap])
  const offline = facts === null
  const position = at === null ? STEPS : Math.max(0, Math.min(STEPS, STEPS - Math.round((now - at) / PROC_STEP_MS)))
  const note = at === null ? null
    : load === 'host_not_local' ? t('watch.host.proc.not_local')
    : load === 'error' ? t('watch.host.proc.error')
    : snap === null ? t(load === 'ok' ? 'watch.host.proc.no_record' : 'watch.host.proc.loading')
    : t('watch.host.proc.at', { time: clock(snap.at) })

  return (
    <div data-testid="proc-tab" className="flex flex-col gap-6">
      <section>
        <h2 className="mb-2 flex flex-wrap items-baseline gap-2 text-sm font-medium text-text-primary">
          {t('watch.host.page.procs')}
          <span className="text-[11px] font-normal text-text-muted">{t('watch.host.proc.every')}</span>
          <span className="flex-1" />
          <span className="flex items-center gap-2 text-[11px] font-normal text-text-secondary">
            {note !== null && <span data-testid="proc-note">{note}</span>}
            <input
              type="range" min={0} max={STEPS} step={1} value={position} disabled={offline}
              aria-label={t('watch.host.proc.timeline')} data-testid="proc-timeline" className="w-48 accent-accent"
              onChange={(e) => {
                const p = Number(e.target.value)
                setPicked(p >= STEPS ? null : now - (STEPS - p) * PROC_STEP_MS)
              }}
            />
            <button type="button" data-testid="proc-now" disabled={at === null} onClick={() => setPicked(null)}
              className="rounded border border-border-subtle px-2 py-0.5 text-text-secondary enabled:hover:bg-surface-tertiary disabled:opacity-40">
              {t('watch.host.proc.back_to_now')}
            </button>
          </span>
        </h2>
        <div className="grid grid-cols-2 gap-8">
          <ProcList testId="procs-cpu" title={t('watch.host.top_cpu12')} rows={rows.cpu} by="cpu" offline={offline} />
          <ProcList testId="procs-mem" title={t('watch.host.top_mem12')} rows={rows.mem} by="mem" offline={offline} />
        </div>
      </section>
      <div className="grid grid-cols-2 gap-8">
        <ResidentsTable residents={facts?.residents ?? []} offline={offline} />
        <div />
      </div>
    </div>
  )
}
