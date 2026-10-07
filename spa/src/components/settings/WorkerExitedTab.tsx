// spa/src/components/settings/WorkerExitedTab.tsx — Settings → Worker → 已退出: every ended conversation on one host,
// terminal or worker (conversation entity spec §13.3), from the daemon's GET /api/nex/conversations?state=ended.
// Fetched on mount and on retry only (R-4-12): no live-execution subscription. Searchable over title, cwd (as shown
// and as it is), first prompt and session id.
//
// 重建… (§13.4): a worker-last row with a stint opens that stint's exited screen, as P2-2 does; every other row opens
// the conversation's closed-terminal rebuild tab (R-4-3, R-4-5). A root the daemon could not list keeps the rows but
// disables every rebuild while the error stands (R-4-1). Conversations whose owner could not be verified are listed
// nowhere and counted under the list (R-4-15).
import { useEffect, useMemo, useState } from 'react'
import { useConversations } from '../../hooks/useConversations'
import { useI18nStore } from '../../stores/useI18nStore'
import { matchesConversationQuery } from '../../lib/nex/conversation-search'
import { openConversationRebuild } from '../../lib/nex/open-conversation-rebuild'
import { openWorkerTab } from '../../features/workspace/lib/open-worker-tab'
import type { ConversationRow as ConversationRowData } from '../../lib/nex/conversations-api'
import { ConversationRow } from './ConversationRow'

const AGE_TICK_MS = 60_000

/** 重建… on an ended row: the latest stint's exited screen for a worker-last row that has one, else the rebuild tab. */
function rebuildConversation(hostId: string, row: ConversationRowData): void {
  if (row.last_in === 'worker' && row.latest_execution_id) {
    openWorkerTab({ kind: 'execution', executionId: row.latest_execution_id, host: hostId })
    return
  }
  // Never rejects: the host-config load and the home lookup it waits on swallow their own failures.
  void openConversationRebuild(hostId, row)
}

export function WorkerExitedTab({ hostId }: { hostId?: string }) {
  if (!hostId) return null
  return <ExitedList key={hostId} hostId={hostId} />
}

function ExitedList({ hostId }: { hostId: string }) {
  const t = useI18nStore((s) => s.t)
  const { page, phase, error, unavailable, refetch } = useConversations(hostId, 'ended', 'normal')
  const [query, setQuery] = useState('')
  const [now, setNow] = useState(() => Date.now())
  useEffect(() => {
    const id = setInterval(() => setNow(Date.now()), AGE_TICK_MS)
    return () => clearInterval(id)
  }, [])

  const home = page?.home ?? ''
  const rows = useMemo(
    () => (page?.conversations ?? []).filter((r) => matchesConversationQuery(r, query, home)),
    [page, query, home],
  )
  const rootError = page?.root_error
  const unknownOwner = page?.unknown_owner ?? 0

  return (
    <div className="flex flex-col gap-2">
      <input
        type="search"
        data-testid="worker-exited-search"
        value={query}
        onChange={(e) => setQuery(e.target.value)}
        placeholder={t('settings.worker.exited.search')}
        aria-label={t('settings.worker.exited.search')}
        className="w-full rounded border border-border-default bg-surface-secondary px-2 py-1 text-sm text-text-primary"
      />
      {unavailable ? (
        <p data-testid="worker-exited-unavailable" className="text-xs text-text-muted">
          {t('settings.worker.conversations.unavailable')}
        </p>
      ) : phase === 'error' && (
        <div data-testid="worker-exited-error" className="flex items-center gap-2 text-xs text-red-400">
          <span className="flex-1 min-w-0 truncate">{t('newtab.workers.error', { error: error ?? '' })}</span>
          <button type="button" data-testid="worker-exited-retry" onClick={refetch}
            className="shrink-0 px-1.5 py-0.5 rounded text-text-secondary hover:text-text-primary hover:bg-surface-hover cursor-pointer">
            {t('newtab.workers.retry')}
          </button>
        </div>
      )}
      {rootError && (
        <p data-testid="worker-exited-root-error" className="text-xs text-amber-400 break-words">
          {t('settings.worker.conversations.root_error', { error: rootError })}
        </p>
      )}
      {page?.truncated && (
        <p data-testid="worker-exited-truncated" className="text-xs text-text-muted">
          {t('settings.worker.conversations.truncated')}
        </p>
      )}
      {/* The first load, or a retry after an error, which keeps its rows below this line. */}
      {phase === 'loading' && (
        <p data-testid="worker-exited-loading" className="text-xs text-text-muted" aria-busy="true">{t('settings.worker.conversations.loading')}</p>
      )}
      {phase === 'ready' && rows.length === 0 && (
        <p data-testid="worker-exited-empty" className="text-xs text-text-muted">{t('settings.worker.exited.empty')}</p>
      )}
      {rows.length > 0 && (
        <div role="list" aria-busy={phase === 'loading' ? 'true' : undefined} className="flex flex-col">
          {rows.map((row) => (
            <ConversationRow key={row.session_id} row={row} state="ended" home={home} now={now}
              disabled={!!rootError} onRebuild={() => rebuildConversation(hostId, row)} />
          ))}
        </div>
      )}
      {unknownOwner > 0 && (
        <p data-testid="worker-exited-unknown-owner" className="text-xs text-text-muted">
          {t('settings.worker.exited.unknown_owner', { n: unknownOwner })}
        </p>
      )}
    </div>
  )
}
