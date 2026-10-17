// spa/src/components/journal/JournalCard.tsx — the body of the journal card (J-2c, spec §11): role line, filters, the 待確認 band and the
// stream. The title (live dot + unit name), the roadmap / tasks toggles and the close button are the frame's (CardStack). Every
// piece of view state — filter, scroll, pinned-to-bottom, the open detail — is the unit's view memo (lib/journal/view-memory.ts).
import { useCallback, useEffect, useMemo, useState } from 'react'
import { JournalFilters } from './JournalFilters'
import { ConfirmBand } from './ConfirmBand'
import { JournalStream } from './JournalStream'
import { PageError, PageHeader, PagePager } from './JournalPages'
import { useSingleFlight } from './useSingleFlight'
import type { RowContext } from './JournalRow'
import type { JournalTarget } from './useJournalTarget'
import { matchesFilter } from '../../lib/journal/filters'
import { patchViewMemo, readViewMemo } from '../../lib/journal/view-memory'
import { useViewMemo } from '../../lib/journal/use-view-memo'
import { useHostStore } from '../../stores/useHostStore'
import { useI18nStore } from '../../stores/useI18nStore'
import { sessionKey, useJournalStore, type UnitState } from '../../stores/useJournalStore'
import type { JournalEntry, SessionLink } from '../../lib/journal/types'

const NO_ENTRIES: readonly JournalEntry[] = []

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

export function JournalCard({ target, unit }: { target: JournalTarget; unit: UnitState }) {
  const t = useI18nStore((s) => s.t)
  const { memoHost: host, memoKey: key, read } = target
  const memo = useViewMemo(host, key)
  const readHost = read?.hostId
  const readSession = read?.sessionId

  // Session pages (journal.sessions.v1): the stream shows ONE leader session. `unit.entries` is the current one (live); another session's
  // rows are `unit.pages[id]`, read when the person goes there. The page being read is the unit's view memo (null = follow the current one).
  const sessionMode = useJournalStore((s) => readHost !== undefined && s.sessionsSupport[readHost] === true) && unit.currentPage !== ''
  const viewPage = sessionMode ? (memo.page ?? unit.currentPage) : ''
  const onCurrent = viewPage === unit.currentPage
  const pageRec = sessionMode && !onCurrent ? unit.pages[viewPage] : undefined
  const pageEntries = !sessionMode || onCurrent ? unit.entries : (pageRec?.entries ?? NO_ENTRIES)
  const shown = useMemo(() => pageEntries.filter((e) => matchesFilter(e, memo.filter)), [pageEntries, memo.filter])
  const sessions = sessionMode ? unit.sessions : null
  const at = sessions ? sessions.findIndex((x) => x.sessionId === viewPage) : -1
  const prevLink = sessions && !unit.sessionsFailed && at > 0 ? sessions[at - 1] : null
  const nextLink = sessions && !unit.sessionsFailed && at >= 0 && at < sessions.length - 1 ? sessions[at + 1] : null
  const goTo = useCallback((link: SessionLink) => {
    if (host === null || key === null) return
    patchViewMemo(host, key, { page: link.sessionId === unit.currentPage ? null : link.sessionId })
  }, [host, key, unit.currentPage])

  // Another session's page: read when not held, and again when the unit changed since it was read (a hand check, a late entry).
  const stale = pageRec !== undefined && pageRec.readAt < unit.readAt.entries
  const [failedPage, setFailedPage] = useState<string | null>(null)
  const [retry, setRetry] = useState(0)
  const held = pageRec !== undefined
  useEffect(() => {
    if (!sessionMode || onCurrent || readHost === undefined || readSession === undefined || (held && !stale)) return
    let live = true
    void useJournalStore.getState().loadPage(readHost, readSession, viewPage).then((r) => {
      if (live) setFailedPage(r.kind === 'failed' || r.kind === 'none' ? viewPage : null)
    })
    return () => { live = false }
  }, [sessionMode, onCurrent, readHost, readSession, viewPage, held, stale, retry])
  const readFailed = useJournalStore((s) => readHost !== undefined && readSession !== undefined && s.readFailed[sessionKey(readHost, readSession)] === true)
  const pageFailed = sessionMode && !onCurrent && !held && failedPage === viewPage
  const loading = sessionMode && !onCurrent && !held && !pageFailed
  const errored = sessionMode && (onCurrent ? readFailed : pageFailed)
  const onRetry = useCallback(() => {
    if (readHost === undefined || readSession === undefined) return
    if (onCurrent) void useJournalStore.getState().loadJournal(readHost, readSession)
    else { setFailedPage(null); setRetry((n) => n + 1) }
  }, [readHost, readSession, onCurrent])

  const onToggleDetail = useCallback((entryId: string) => {
    if (host === null || key === null) return
    patchViewMemo(host, key, { detailId: readViewMemo(host, key).detailId === entryId ? null : entryId })
  }, [host, key])
  // Hand checks need journal.confirm.v1 on the host the journal is read from; without it the boxes are read-only.
  const canCheck = useJournalStore((s) => readHost !== undefined && s.confirmSupport[readHost] === true)
  const onToggleItem = useCallback((entry: JournalEntry, idx: number, done: boolean) => {
    if (readHost === undefined || readSession === undefined) return
    void useJournalStore.getState().setConfirm(readHost, readSession, entry.id, idx, done)
  }, [readHost, readSession])
  const onLoadOlder = useCallback(
    () => (!sessionMode && readHost !== undefined && readSession !== undefined ? useJournalStore.getState().loadJournal(readHost, readSession, { older: true }) : Promise.resolve()),
    [sessionMode, readHost, readSession],
  )
  const retrySessions = useSingleFlight(`${readHost}\u0000${readSession}\u0000sessions`, useCallback(
    () => (readHost !== undefined && readSession !== undefined ? useJournalStore.getState().loadSessions(readHost, readSession) : Promise.resolve()),
    [readHost, readSession],
  ))
  const ctx: RowContext = useMemo(
    () => ({ personName: unit.personName, remoteUrl: unit.project.remoteUrl, selectedId: memo.detailId, onToggleDetail, onToggleItem: canCheck ? onToggleItem : undefined }),
    [unit.personName, unit.project.remoteUrl, memo.detailId, onToggleDetail, onToggleItem, canCheck],
  )

  return (
    <div data-testid="journal-card-body" className="h-full flex flex-col">
      <RoleLine target={target} unit={unit} />
      <JournalFilters host={host} unitKey={key} openCount={unit.openConfirms} />
      <ConfirmBand entries={unit.confirms} ctx={ctx} />
      {sessionMode && (
        <PageHeader
          link={at >= 0 ? sessions?.[at] : undefined}
          current={onCurrent}
          incomplete={at === 0 && unit.sessionsIncomplete}
          truncated={onCurrent ? unit.truncated : pageRec?.truncated === true}
        />
      )}
      {errored && <PageError onRetry={onRetry} />}
      {loading && <div data-testid="journal-page-loading" className="px-3 py-6 text-center text-text-muted">{t('journal.page.loading')}</div>}
      {!loading && !(errored && pageEntries.length === 0)
        && <JournalStream host={host} unitKey={key} page={sessionMode ? viewPage : undefined} streamKey={sessionMode ? `${host}\0${key}\0${viewPage}` : undefined} entries={shown} filter={memo.filter} more={sessionMode ? false : unit.more} onLoadOlder={onLoadOlder} ctx={ctx} />}
      {errored && pageEntries.length === 0 && <div className="flex-1" />}
      {sessionMode && unit.sessionsFailed && <PageError onRetry={retrySessions} testId="journal-sessions-error" messageKey="journal.page.sessionsError" />}
      {sessionMode && <PagePager prev={prevLink} next={nextLink} onGo={goTo} />}
    </div>
  )
}
