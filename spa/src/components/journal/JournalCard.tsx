// spa/src/components/journal/JournalCard.tsx — the body of the journal card (J-2c, spec §11): role line, filters, the pinned band and the
// stream. The title (live dot + unit name), the roadmap / tasks toggles and the close button are the frame's (CardStack). Every
// piece of view state — filter, scroll, pinned-to-bottom, the open detail — is the unit's view memo (lib/journal/view-memory.ts).
import { useCallback, useMemo } from 'react'
import { JournalFilters } from './JournalFilters'
import { PinnedBand } from './PinnedBand'
import { JournalStream } from './JournalStream'
import type { RowContext } from './JournalRow'
import type { JournalTarget } from './useJournalTarget'
import { matchesFilter } from '../../lib/journal/filters'
import { patchViewMemo, readViewMemo } from '../../lib/journal/view-memory'
import { useViewMemo } from '../../lib/journal/use-view-memo'
import { useHostStore } from '../../stores/useHostStore'
import { useI18nStore } from '../../stores/useI18nStore'
import { useJournalStore, type UnitState } from '../../stores/useJournalStore'
import type { TaskHover } from '../../lib/journal/tasks'
import type { JournalEntry } from '../../lib/journal/types'

/** The frame's title: the connection dot of the host the journal is read from, and the unit's name. */
export function JournalTitle({ target }: { target: JournalTarget }) {
  const t = useI18nStore((s) => s.t)
  const live = useHostStore((s) => (target.memoHost === null ? false : s.runtime[target.memoHost]?.status === 'connected'))
  return (
    <span className="flex items-center gap-2 min-w-0">
      <span data-testid="journal-live-dot" data-live={live} title={t(live ? 'journal.card.live' : 'journal.card.offline')} className={`w-2 h-2 rounded-full shrink-0 ${live ? 'bg-status-success-text' : 'bg-text-muted'}`} />
      <span className="truncate">{target.unit?.unit.label ?? ''}</span>
    </span>
  )
}

function RoleLine({ target, unit }: { target: JournalTarget; unit: UnitState }) {
  const t = useI18nStore((s) => s.t)
  const team = target.team
  const text = unit.unit.kind === 'solo'
    ? t('journal.card.role.solo')
    : team ? t('journal.card.role.team', { name: team.lead.title, count: team.members.length }) : t('journal.card.role.teamBare')
  return <div data-testid="journal-role" className="px-3 pb-1 -mt-0.5 text-[11px] text-text-muted truncate">{text}</div>
}

interface CardProps {
  target: JournalTarget
  unit: UnitState
  /** The tasks tab's hover link, shared with the tasks card by the stack (momentary, not part of the view memo). */
  hover?: TaskHover | null
  onHover?: (h: TaskHover | null) => void
}

export function JournalCard({ target, unit, hover, onHover }: CardProps) {
  const { memoHost: host, memoKey: key, read } = target
  const memo = useViewMemo(host, key)
  const shown = useMemo(() => unit.entries.filter((e) => matchesFilter(e, memo.filter)), [unit.entries, memo.filter])
  const readHost = read?.hostId
  const readSession = read?.sessionId

  const onToggleDetail = useCallback((entryId: string) => {
    if (host === null || key === null) return
    patchViewMemo(host, key, { detailId: readViewMemo(host, key).detailId === entryId ? null : entryId })
  }, [host, key])
  const onTogglePin = useCallback((entry: JournalEntry) => {
    if (readHost === undefined || readSession === undefined) return
    const pinnedNow = useJournalStore.getState().byHost[readHost]?.units[`${unit.unit.kind}:${unit.unit.id}`]?.pinned.some((p) => p.id === entry.id) ?? entry.pinned
    void useJournalStore.getState().setPin(readHost, readSession, entry.id, !pinnedNow)
  }, [readHost, readSession, unit.unit.kind, unit.unit.id])
  const onLoadOlder = useCallback(
    () => (readHost !== undefined && readSession !== undefined ? useJournalStore.getState().loadJournal(readHost, readSession, { older: true }) : Promise.resolve()),
    [readHost, readSession],
  )
  const ctx: RowContext = useMemo(
    () => ({ personName: unit.personName, remoteUrl: unit.project.remoteUrl, selectedId: memo.detailId, onToggleDetail, onTogglePin, hover, onHover }),
    [unit.personName, unit.project.remoteUrl, memo.detailId, onToggleDetail, onTogglePin, hover, onHover],
  )

  return (
    <div data-testid="journal-card-body" className="h-full flex flex-col">
      <RoleLine target={target} unit={unit} />
      <JournalFilters host={host} unitKey={key} openCount={unit.openConfirms} />
      <PinnedBand pinned={unit.pinned} ctx={ctx} />
      <JournalStream host={host} unitKey={key} entries={shown} filter={memo.filter} more={unit.more} onLoadOlder={onLoadOlder} ctx={ctx} />
    </div>
  )
}
