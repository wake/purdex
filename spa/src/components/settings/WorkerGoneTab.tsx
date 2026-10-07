// spa/src/components/settings/WorkerGoneTab.tsx — Settings → Worker → 已消失: every conversation on one host whose
// transcript is gone (conversation entity spec §13.3, state 3), from the daemon's GET /api/nex/conversations?state=gone.
// Fetched on mount and on retry only (R-4-12). Searchable as 已退出 is. Every row is disabled and says why (U3).
//
// A root the daemon could not list makes the gone list untrustworthy: the tab shows the notice and no rows (R-4-1).
// The unknown-owner count is 已退出's line only (R-4-15).
import { useEffect, useMemo, useState } from 'react'
import { useConversations } from '../../hooks/useConversations'
import { useI18nStore } from '../../stores/useI18nStore'
import { matchesConversationQuery } from '../../lib/nex/conversation-search'
import { ConversationRow } from './ConversationRow'

const AGE_TICK_MS = 60_000

export function WorkerGoneTab({ hostId }: { hostId?: string }) {
  if (!hostId) return null
  return <GoneList key={hostId} hostId={hostId} />
}

function GoneList({ hostId }: { hostId: string }) {
  const t = useI18nStore((s) => s.t)
  const { page, phase, error, unavailable, refetch } = useConversations(hostId, 'gone', 'normal')
  const [query, setQuery] = useState('')
  const [now, setNow] = useState(() => Date.now())
  useEffect(() => {
    const id = setInterval(() => setNow(Date.now()), AGE_TICK_MS)
    return () => clearInterval(id)
  }, [])

  const home = page?.home ?? ''
  const rootError = page?.root_error
  const rows = useMemo(
    () => (rootError ? [] : (page?.conversations ?? []).filter((r) => matchesConversationQuery(r, query, home))),
    [page, rootError, query, home],
  )

  return (
    <div className="flex flex-col gap-2">
      <input
        type="search"
        data-testid="worker-gone-search"
        value={query}
        onChange={(e) => setQuery(e.target.value)}
        placeholder={t('settings.worker.gone.search')}
        aria-label={t('settings.worker.gone.search')}
        className="w-full rounded border border-border-default bg-surface-secondary px-2 py-1 text-sm text-text-primary"
      />
      {unavailable ? (
        <p data-testid="worker-gone-unavailable" className="text-xs text-text-muted">
          {t('settings.worker.conversations.unavailable')}
        </p>
      ) : phase === 'error' && (
        <div data-testid="worker-gone-error" className="flex items-center gap-2 text-xs text-red-400">
          <span className="flex-1 min-w-0 truncate">{t('newtab.workers.error', { error: error ?? '' })}</span>
          <button type="button" data-testid="worker-gone-retry" onClick={refetch}
            className="shrink-0 px-1.5 py-0.5 rounded text-text-secondary hover:text-text-primary hover:bg-surface-hover cursor-pointer">
            {t('newtab.workers.retry')}
          </button>
        </div>
      )}
      {rootError && (
        <p data-testid="worker-gone-root-error" className="text-xs text-amber-400 break-words">
          {t('settings.worker.conversations.root_error', { error: rootError })}
        </p>
      )}
      {!rootError && page?.truncated && (
        <p data-testid="worker-gone-truncated" className="text-xs text-text-muted">
          {t('settings.worker.conversations.truncated')}
        </p>
      )}
      {/* The first load, or a retry after an error, which keeps its rows below this line. */}
      {phase === 'loading' && (
        <p data-testid="worker-gone-loading" className="text-xs text-text-muted" aria-busy="true">{t('settings.worker.conversations.loading')}</p>
      )}
      {phase === 'ready' && !rootError && rows.length === 0 && (
        <p data-testid="worker-gone-empty" className="text-xs text-text-muted">{t('settings.worker.gone.empty')}</p>
      )}
      {rows.length > 0 && (
        <div role="list" aria-busy={phase === 'loading' ? 'true' : undefined} className="flex flex-col">
          {rows.map((row) => (
            <ConversationRow key={row.session_id} row={row} state="gone" home={home} now={now} disabled />
          ))}
        </div>
      )}
    </div>
  )
}
