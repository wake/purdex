// spa/src/components/hosts/nex/NexExecutionsTable.tsx — the
// third card of the Host → Nex sub-page. Lists executions for `hostId` from
// the shared per-host list (`useHostExecutions` → `useExecutionListStore`,
// P-C spec §4.3), which owns the one site-wide Nex SSE, its debounce and the
// non-archived rows. Only the "show archived" view is queried here: the
// store holds non-archived rows only, so while the toggle is on the table
// runs its own guarded query, re-issued whenever the store commits a
// refresh (`refreshRevision`).
import { useCallback, useEffect, useRef, useState } from 'react'
import { ArrowsClockwise } from '@phosphor-icons/react'
import { useI18nStore } from '../../../stores/useI18nStore'
import { useHostExecutions } from '../../../hooks/useHostExecutions'
import { openExecutionDetailTab } from '../../../lib/deeplink/deeplinkResolver'
import { archiveExecution, attachControl, listExecutions, releaseLease, terminateExecution } from '../../../lib/nex/nex-api'
import { NexApiError, type ExecutionSummary } from '../../../lib/nex/types'
import { sanitizeExecutionsPage } from '../../../lib/nex/validate-executions'
import { isRefShownNow, useIsRefShown } from '../../../lib/shown-hosts'
import NexExecutionRow from './NexExecutionRow'

export interface NexExecutionsTableProps {
  hostId: string
  enabled: boolean
}

interface ActionError {
  action: string
  code: string
}

/** Result of the table-local archived query; `hostId` says which host it belongs to. */
interface ArchivedList {
  hostId: string
  items: ExecutionSummary[]
  error: string | null
}

function errorCode(err: unknown): string {
  return err instanceof NexApiError ? err.code : 'network'
}

export default function NexExecutionsTable({ hostId, enabled }: NexExecutionsTableProps) {
  const t = useI18nStore((s) => s.t)
  const [includeArchived, setIncludeArchived] = useState(false)
  const [archived, setArchived] = useState<ArchivedList | null>(null)
  const [actionError, setActionError] = useState<ActionError | null>(null)
  const [confirmTerminateId, setConfirmTerminateId] = useState<string | null>(null)
  const [pendingId, setPendingId] = useState<string | null>(null)
  // A host hidden in this workbench keeps its executions listed and manageable; only "open" (it creates a tab) is
  // not offered (plan H2d-2, §0.21 user rules 1 / 5).
  const shown = useIsRefShown(hostId)

  // Same gate as before the store migration: a host that is not nex-ready
  // subscribes nothing (the store would refuse to open anyway, but staying
  // off it keeps the refcount honest for the sidebar view).
  const shared = useHostExecutions(hostId, { enabled })
  const { refetch, refreshRevision } = shared

  // Kept in sync every render (not via effect) so async action handlers
  // always read the latest host without being an effect dependency.
  const hostIdRef = useRef(hostId)
  hostIdRef.current = hostId

  // React 19 StrictMode dev-double-invokes every effect (mount -> cleanup ->
  // mount) to surface missing cleanup. A cleanup-only effect body (no setup
  // statement) leaves this permanently false after that synthetic cycle, so
  // every subsequent action `finally` guard silently no-ops. Setting it back
  // to true in the setup half fixes that for both the real mount and
  // StrictMode's extra one.
  const mountedRef = useRef(true)
  useEffect(() => {
    mountedRef.current = true
    return () => { mountedRef.current = false }
  }, [])

  // Guards a stale archived answer (previous host, superseded by a newer
  // request, or issued before the host was disabled / the toggle went off)
  // from landing after the request it belongs to is no longer the latest
  // one in flight. Bumped synchronously on every change of the three inputs
  // — in the query effect's cleanup and again at toggle-off — and the commit
  // guard also re-reads the live inputs, so an answer that arrives between
  // the click and React's cleanup is dropped too.
  const archivedTokenRef = useRef(0)
  const enabledRef = useRef(enabled)
  enabledRef.current = enabled
  const includeArchivedRef = useRef(includeArchived)
  includeArchivedRef.current = includeArchived

  // Host-scoped UI reset: a host switch must not leave the previous host's
  // action error / confirm / pending state on screen. The rows themselves
  // are per host in the store (and `archived` carries its own hostId).
  useEffect(() => {
    setActionError(null)
    setConfirmTerminateId(null)
    setPendingId(null)
  }, [hostId])

  // Table-local archived query (plan task 3): keyed on `refreshRevision` so
  // every refresh cycle the store runs — SSE frame, reconnect, post-action
  // refetch — refreshes this view too. Toggling off issues nothing: the
  // shared rows are rendered directly.
  useEffect(() => {
    if (!enabled || !includeArchived) return
    const token = ++archivedTokenRef.current
    const stillWanted = () =>
      mountedRef.current
      && token === archivedTokenRef.current
      && hostId === hostIdRef.current
      && enabledRef.current
      && includeArchivedRef.current
    listExecutions(hostId, { includeArchived: true, limit: 100 })
      .then((page) => {
        if (!stillWanted()) return
        const { items, dropped } = sanitizeExecutionsPage(page)
        if (dropped > 0) console.warn('nex: archived executions page dropped malformed row(s)', { hostId, dropped })
        setArchived({ hostId, items, error: null })
      })
      .catch((err: unknown) => {
        if (!stillWanted()) return
        setArchived((prev) => ({ hostId, items: prev?.hostId === hostId ? prev.items : [], error: errorCode(err) }))
      })
    return () => { archivedTokenRef.current += 1 }
  }, [hostId, enabled, includeArchived, refreshRevision])

  const handleIncludeArchived = (checked: boolean) => {
    setIncludeArchived(checked)
    if (!checked) {
      archivedTokenRef.current += 1
      setArchived(null)
    }
  }

  const showArchived = includeArchived && archived?.hostId === hostId
  const items = showArchived ? archived.items : shared.items
  // In archived mode the shared refresh is still what drives every re-query
  // (its revision), so its failure is the one worth showing first.
  const loadError = showArchived ? (shared.error ?? archived.error) : shared.error

  const handleOpen = (row: ExecutionSummary) => {
    if (!isRefShownNow(hostId)) return
    // Spec §4.4.3: go through the same helper the deeplink resolver uses
    // (spa/src/lib/deeplink/deeplinkResolver.ts) rather than re-implementing
    // openSingletonTab here — it already activates the tab, so no separate
    // setActiveTab call is needed.
    openExecutionDetailTab(row.id, hostId)
  }

  const handleTerminateConfirm = useCallback(async (row: ExecutionSummary) => {
    const startHostId = hostId
    setConfirmTerminateId(null)
    setActionError(null)
    setPendingId(row.id)
    try {
      // attachControl -> terminateExecution -> releaseLease (spec §4.3.3)
      // (best-effort). A terminated execution may already have dropped its
      // lease, so releaseLease's own rejection is swallowed below and must
      // never surface as an action error; a failure of the first two steps
      // (e.g. attachControl's `lease_held`) does surface, via the outer catch.
      const lease = await attachControl(startHostId, row.id)
      await terminateExecution(startHostId, row.id, lease.lease_id)
      await releaseLease(startHostId, row.id, lease.lease_id).catch(() => {})
      // The host may have changed while the awaits above were in flight —
      // the store already fetched that host on subscribe, and `refetch` is
      // bound to the host this action started on (a no-op once it has no
      // subscriber), so a stale refresh is skipped here and guarded again
      // in the store.
      if (hostIdRef.current === startHostId) refetch()
    } catch (err) {
      if (mountedRef.current && hostIdRef.current === startHostId) {
        setActionError({ action: t('hosts.nex.executions.terminate'), code: errorCode(err) })
      }
    } finally {
      if (mountedRef.current) setPendingId(null)
    }
  }, [hostId, refetch, t])

  const handleArchiveToggle = useCallback(async (row: ExecutionSummary) => {
    const startHostId = hostId
    setActionError(null)
    setPendingId(row.id)
    try {
      await archiveExecution(startHostId, row.id, row.archived)
      if (hostIdRef.current === startHostId) refetch()
    } catch (err) {
      if (mountedRef.current && hostIdRef.current === startHostId) {
        const label = row.archived ? t('hosts.nex.executions.unarchive') : t('hosts.nex.executions.archive')
        setActionError({ action: label, code: errorCode(err) })
      }
    } finally {
      if (mountedRef.current) setPendingId(null)
    }
  }, [hostId, refetch, t])

  return (
    <div>
      <div className="flex items-center justify-between mb-3">
        <h3 className="text-sm font-semibold text-text-primary">{t('hosts.nex.executions.title')}</h3>
        <div className="flex items-center gap-3">
          <label className="inline-flex items-center gap-1.5 text-xs text-text-secondary cursor-pointer">
            <input
              type="checkbox"
              checked={includeArchived}
              onChange={(e) => handleIncludeArchived(e.target.checked)}
            />
            {t('hosts.nex.executions.include_archived')}
          </label>
          <button
            type="button"
            onClick={() => { if (enabled) refetch() }}
            className="flex items-center gap-1 text-xs text-text-secondary hover:text-accent cursor-pointer"
          >
            <ArrowsClockwise size={12} />
            {t('hosts.nex.executions.refresh')}
          </button>
        </div>
      </div>

      {loadError && (
        <p className="text-xs text-red-400 mb-2">
          {t('hosts.load_failed')}: {loadError}
        </p>
      )}
      {!shown && (
        <p data-testid="nex-executions-open-hint" className="text-xs text-text-muted mb-2">{t('hosts.shown.open_executions_hint')}</p>
      )}
      {actionError && (
        <p data-testid="nex-executions-action-error" className="text-xs text-red-400 mb-2">
          {t('hosts.nex.executions.action_failed', { action: actionError.action, code: actionError.code })}
        </p>
      )}

      {items.length === 0 ? (
        <p className="text-sm text-text-muted">{t('hosts.nex.executions.empty')}</p>
      ) : (
        <div className="border border-border-subtle rounded-lg overflow-x-auto">
          <table className="w-full text-sm">
            <thead>
              <tr className="bg-surface-tertiary text-text-secondary text-xs">
                <th className="text-left px-3 py-2">{t('hosts.nex.executions.col.state')}</th>
                <th className="text-left px-3 py-2">{t('hosts.nex.executions.col.id')}</th>
                <th className="text-left px-3 py-2">{t('hosts.nex.executions.col.provider')}</th>
                <th className="text-left px-3 py-2">{t('hosts.nex.executions.col.cwd')}</th>
                <th className="text-left px-3 py-2">{t('hosts.nex.executions.col.brief')}</th>
                <th className="text-right px-3 py-2">{t('hosts.nex.executions.col.observers')}</th>
                <th className="text-left px-3 py-2">{t('hosts.nex.executions.col.lease')}</th>
                <th className="text-left px-3 py-2">{t('hosts.nex.executions.col.last_turn')}</th>
                <th className="text-left px-3 py-2">{t('hosts.nex.executions.col.updated')}</th>
                <th className="text-right px-3 py-2"></th>
              </tr>
            </thead>
            <tbody>
              {items.map((row) => (
                <NexExecutionRow
                  key={row.id}
                  row={row}
                  confirmingTerminate={confirmTerminateId === row.id}
                  pending={pendingId === row.id}
                  onOpen={shown ? () => handleOpen(row) : undefined}
                  onTerminateClick={() => setConfirmTerminateId(row.id)}
                  onTerminateConfirm={() => void handleTerminateConfirm(row)}
                  onArchiveToggle={() => void handleArchiveToggle(row)}
                />
              ))}
            </tbody>
          </table>
        </div>
      )}
    </div>
  )
}
