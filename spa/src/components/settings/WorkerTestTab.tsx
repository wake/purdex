// spa/src/components/settings/WorkerTestTab.tsx — Settings → Worker → 測試用: the conversations whose cwd is under
// /tmp (daemon `scope=test`) in three sections — running (the live execution list, same filter), exited, gone —
// so the Workers / 已退出 / 已消失 tabs stay free of them. One search box filters all three.
//
// Needs a daemon with `conversations.scope.v1`: on an older one the tab only explains that and calls nothing.
// Rows and actions are the 已退出 / 已消失 ones (`ConversationRow`, `rebuildConversation`).
import { useEffect, useMemo, useState } from 'react'
import { useConversations, type UseConversations } from '../../hooks/useConversations'
import { useHostExecutions } from '../../hooks/useHostExecutions'
import { useI18nStore } from '../../stores/useI18nStore'
import { selectConversationsScope, useNexHostStore } from '../../stores/useNexHostStore'
import { matchesConversationQuery } from '../../lib/nex/conversation-search'
import { filterLiveRows } from '../../lib/nex/live-workers'
import { rebuildConversation } from '../../lib/nex/rebuild-conversation'
import { openWorkerTab } from '../../features/workspace/lib/open-worker-tab'
import { HostWorkerRows } from '../HostWorkerRows'
import { ConversationRow } from './ConversationRow'

const AGE_TICK_MS = 60_000

export function WorkerTestTab({ hostId }: { hostId?: string }) {
  if (!hostId) return null
  return <TestGate key={hostId} hostId={hostId} />
}

/** Decides, from the host's cached readiness, whether the sections may mount (and so call anything). */
function TestGate({ hostId }: { hostId: string }) {
  const t = useI18nStore((s) => s.t)
  const entry = useNexHostStore((s) => s.byHost[hostId])
  const scoped = useNexHostStore(selectConversationsScope(hostId))
  useEffect(() => { void useNexHostStore.getState().ensure(hostId) }, [hostId])

  const phase = entry?.phase ?? 'loading'
  if (phase === 'disabled') {
    return <p data-testid="worker-test-disabled" className="text-xs text-text-muted">{t('newtab.headless.disabled')}</p>
  }
  if (phase === 'unavailable') {
    return <p data-testid="worker-test-unavailable" className="text-xs text-red-400">{t('newtab.headless.unavailable', { error: entry?.error ?? '' })}</p>
  }
  if (phase !== 'ready') {
    return <p data-testid="worker-test-loading" className="text-xs text-text-muted" aria-busy="true">{t('settings.worker.conversations.loading')}</p>
  }
  if (!scoped) {
    return <p data-testid="worker-test-unsupported" className="text-xs text-text-muted">{t('settings.worker.test.unsupported')}</p>
  }
  return <TestSections hostId={hostId} />
}

function SectionHeading({ id, label }: { id: string; label: string }) {
  return <h3 data-testid={`worker-test-section-${id}`} className="text-xs font-medium text-text-secondary pt-1">{label}</h3>
}

interface ListNoticesProps {
  prefix: string
  list: UseConversations
}

/** Unavailable / error+retry / loading lines of one conversations section (the 已退出 tab's, per section). */
function ListNotices({ prefix, list }: ListNoticesProps) {
  const t = useI18nStore((s) => s.t)
  const { page, phase, error, unavailable, refetch } = list
  return (
    <>
      {unavailable ? (
        <p data-testid={`${prefix}-unavailable`} className="text-xs text-text-muted">{t('settings.worker.conversations.unavailable')}</p>
      ) : phase === 'error' && (
        <div data-testid={`${prefix}-error`} className="flex items-center gap-2 text-xs text-red-400">
          <span className="flex-1 min-w-0 truncate">{t('newtab.workers.error', { error: error ?? '' })}</span>
          <button type="button" data-testid={`${prefix}-retry`} onClick={refetch}
            className="shrink-0 px-1.5 py-0.5 rounded text-text-secondary hover:text-text-primary hover:bg-surface-hover cursor-pointer">
            {t('newtab.workers.retry')}
          </button>
        </div>
      )}
      {page?.root_error && (
        <p data-testid={`${prefix}-root-error`} className="text-xs text-amber-400 break-words">
          {t('settings.worker.conversations.root_error', { error: page.root_error })}
        </p>
      )}
      {!page?.root_error && page?.truncated && (
        <p data-testid={`${prefix}-truncated`} className="text-xs text-text-muted">{t('settings.worker.conversations.truncated')}</p>
      )}
      {phase === 'loading' && (
        <p data-testid={`${prefix}-loading`} className="text-xs text-text-muted" aria-busy="true">{t('settings.worker.conversations.loading')}</p>
      )}
    </>
  )
}

function TestSections({ hostId }: { hostId: string }) {
  const t = useI18nStore((s) => s.t)
  const ended = useConversations(hostId, 'ended', 'test')
  const gone = useConversations(hostId, 'gone', 'test')
  const exec = useHostExecutions(hostId)
  const [query, setQuery] = useState('')
  const [now, setNow] = useState(() => Date.now())
  useEffect(() => {
    const id = setInterval(() => setNow(Date.now()), AGE_TICK_MS)
    return () => clearInterval(id)
  }, [])

  const endedHome = ended.page?.home ?? ''
  const goneHome = gone.page?.home ?? ''
  const endedRootError = ended.page?.root_error
  const goneRootError = gone.page?.root_error
  const endedRows = useMemo(
    () => (ended.page?.conversations ?? []).filter((r) => matchesConversationQuery(r, query, endedHome)),
    [ended.page, query, endedHome],
  )
  // A root the daemon could not list makes the gone list untrustworthy: no rows (R-4-1), as in 已消失.
  const goneRows = useMemo(
    () => (goneRootError ? [] : (gone.page?.conversations ?? []).filter((r) => matchesConversationQuery(r, query, goneHome))),
    [gone.page, goneRootError, query, goneHome],
  )
  const liveCount = useMemo(
    () => filterLiveRows(exec.items, { filter: 'test', query, home: endedHome || goneHome }).length,
    [exec.items, query, endedHome, goneHome],
  )
  const allEmpty = liveCount === 0 && endedRows.length === 0 && goneRows.length === 0
    && exec.phase === 'ready' && ended.phase === 'ready' && gone.phase === 'ready'

  return (
    <div className="flex flex-col gap-2">
      <input
        type="search"
        data-testid="worker-test-search"
        value={query}
        onChange={(e) => setQuery(e.target.value)}
        placeholder={t('settings.worker.test.search')}
        aria-label={t('settings.worker.test.search')}
        className="w-full rounded border border-border-default bg-surface-secondary px-2 py-1 text-sm text-text-primary"
      />

      {liveCount > 0 && <SectionHeading id="live" label={t('settings.worker.test.section.live')} />}
      <HostWorkerRows
        hostId={hostId}
        testIdPrefix="worker-test-live"
        filter="test"
        query={query}
        home={endedHome || goneHome}
        hideEmpty
        onOpen={(id) => { openWorkerTab({ kind: 'execution', executionId: id, host: hostId }) }}
      />

      {endedRows.length > 0 && <SectionHeading id="exited" label={t('settings.worker.test.section.exited')} />}
      <ListNotices prefix="worker-test-exited" list={ended} />
      {endedRows.length > 0 && (
        <div role="list" aria-busy={ended.phase === 'loading' ? 'true' : undefined} className="flex flex-col">
          {endedRows.map((row) => (
            <ConversationRow key={row.session_id} row={row} state="ended" home={endedHome} now={now}
              disabled={!!endedRootError} onRebuild={() => rebuildConversation(hostId, row)} />
          ))}
        </div>
      )}

      {goneRows.length > 0 && <SectionHeading id="gone" label={t('settings.worker.test.section.gone')} />}
      <ListNotices prefix="worker-test-gone" list={gone} />
      {goneRows.length > 0 && (
        <div role="list" aria-busy={gone.phase === 'loading' ? 'true' : undefined} className="flex flex-col">
          {goneRows.map((row) => (
            <ConversationRow key={row.session_id} row={row} state="gone" home={goneHome} now={now} disabled />
          ))}
        </div>
      )}

      {(ended.page?.unknown_owner ?? 0) > 0 && (
        <p data-testid="worker-test-unknown-owner" className="text-xs text-text-muted">
          {t('settings.worker.exited.unknown_owner', { n: ended.page?.unknown_owner ?? 0 })}
        </p>
      )}

      {allEmpty && (
        <p data-testid="worker-test-empty" className="text-xs text-text-muted">{t('settings.worker.test.empty')}</p>
      )}
    </div>
  )
}
