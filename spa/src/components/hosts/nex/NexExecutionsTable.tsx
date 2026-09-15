// spa/src/components/hosts/nex/NexExecutionsTable.tsx — P-B.3 Task 6: the
// third card of the Host → Nex sub-page. Lists executions for `hostId` and
// keeps the list fresh via the site-wide Nex SSE stream, which is a refresh
// signal only (nexen/api/sse.go:118,195) — frame contents are never applied,
// only its durable cursor is kept so a reconnect does not replay history
// from seq 0.
import { useCallback, useEffect, useRef, useState } from 'react'
import { ArrowsClockwise } from '@phosphor-icons/react'
import { useI18nStore } from '../../../stores/useI18nStore'
import { useTabStore } from '../../../stores/useTabStore'
import { archiveExecution, attachControl, listExecutions, releaseLease, terminateExecution } from '../../../lib/nex/nex-api'
import { openNexSse } from '../../../lib/nex/nex-sse'
import { NexApiError, type ExecutionSummary } from '../../../lib/nex/types'
import NexExecutionRow from './NexExecutionRow'

export interface NexExecutionsTableProps {
  hostId: string
  enabled: boolean
}

/** Trailing debounce applied to an SSE-triggered refetch (brief §Task 6). */
export const LIST_REFRESH_DEBOUNCE_MS = 500

interface ActionError {
  action: string
  code: string
}

function errorCode(err: unknown): string {
  return err instanceof NexApiError ? err.code : 'network'
}

export default function NexExecutionsTable({ hostId, enabled }: NexExecutionsTableProps) {
  const t = useI18nStore((s) => s.t)
  const [items, setItems] = useState<ExecutionSummary[]>([])
  const [includeArchived, setIncludeArchived] = useState(false)
  const [loadError, setLoadError] = useState<string | null>(null)
  const [actionError, setActionError] = useState<ActionError | null>(null)
  const [confirmTerminateId, setConfirmTerminateId] = useState<string | null>(null)
  const [pendingId, setPendingId] = useState<string | null>(null)

  // Kept in sync every render (not via effect) so async callbacks — the SSE
  // debounce, action handlers — always read the latest value without
  // themselves being an effect dependency (which would tear down/reopen the
  // SSE connection or re-run fetches for changes they don't care about).
  const hostIdRef = useRef(hostId)
  hostIdRef.current = hostId
  const includeArchivedRef = useRef(includeArchived)
  includeArchivedRef.current = includeArchived

  const mountedRef = useRef(true)
  useEffect(() => () => { mountedRef.current = false }, [])

  // Cursor for Last-Event-ID: only frames carrying a non-null `id` (durable
  // events) may advance it — transient snapshot/stream frames never do
  // (spa-context.md: sse.go:74 always points at the last durable seq).
  const lastIdRef = useRef<number | null>(null)
  const debounceRef = useRef<ReturnType<typeof setTimeout> | null>(null)
  // Guards a stale response (previous host, or superseded by a newer
  // request) from landing after the request it belongs to is no longer the
  // latest one in flight.
  const requestTokenRef = useRef(0)

  // Reset the cursor only for an actual host change — a durable seq from one
  // host's log means nothing on another. Declared (and thus runs) before the
  // data+SSE effect below so a hostId change clears the cursor in the same
  // commit, before that effect reads it to open the new connection. An
  // `enabled` toggle alone (same host) must NOT reset it — that would defeat
  // the point of keeping a cursor: a pause/resume would replay the whole
  // site's durable history from seq 0 instead of picking up where it left off.
  useEffect(() => {
    lastIdRef.current = null
  }, [hostId])

  const refetch = useCallback((forHostId: string, includeArchivedValue: boolean) => {
    const token = ++requestTokenRef.current
    listExecutions(forHostId, { includeArchived: includeArchivedValue, limit: 100 })
      .then((page) => {
        if (!mountedRef.current || token !== requestTokenRef.current) return
        setItems(page.items)
        setLoadError(null)
      })
      .catch((err: unknown) => {
        if (!mountedRef.current || token !== requestTokenRef.current) return
        setLoadError(errorCode(err))
      })
  }, [])

  // Data + SSE lifecycle: (re)connect and do the initial fetch whenever
  // `enabled` flips true, or `hostId` changes while enabled. Deliberately
  // NOT re-run for an `includeArchived` toggle alone — that only needs a
  // refetch (handled below), not a stream reconnect.
  useEffect(() => {
    if (!enabled) return
    refetch(hostId, includeArchivedRef.current)

    const handle = openNexSse({
      hostId,
      url: '/api/nex/v1/events',
      getLastEventId: () => lastIdRef.current,
      onFrame: (frame) => {
        if (frame.id != null) {
          lastIdRef.current = Math.max(lastIdRef.current ?? 0, Number(frame.id))
        }
        if (debounceRef.current) clearTimeout(debounceRef.current)
        debounceRef.current = setTimeout(() => {
          debounceRef.current = null
          refetch(hostId, includeArchivedRef.current)
        }, LIST_REFRESH_DEBOUNCE_MS)
      },
      onStatus: () => {},
    })

    return () => {
      handle.close()
      if (debounceRef.current) {
        clearTimeout(debounceRef.current)
        debounceRef.current = null
      }
    }
  }, [hostId, enabled, refetch])

  // Refetch on an include-archived toggle only — skips the very first run
  // (the effect above already did the initial fetch) and reads the latest
  // hostId/enabled from refs so it never fires for a host/enabled change it
  // does not own.
  const skipFirstArchivedEffect = useRef(true)
  useEffect(() => {
    if (skipFirstArchivedEffect.current) {
      skipFirstArchivedEffect.current = false
      return
    }
    if (!enabled) return
    refetch(hostIdRef.current, includeArchived)
    // eslint-disable-next-line react-hooks/exhaustive-deps -- hostId/enabled read via ref on purpose (see comment above)
  }, [includeArchived, refetch])

  const handleOpen = (row: ExecutionSummary) => {
    const tabId = useTabStore.getState().openSingletonTab({ kind: 'execution', executionId: row.id, host: hostId })
    useTabStore.getState().setActiveTab(tabId)
  }

  const handleTerminateConfirm = useCallback(async (row: ExecutionSummary) => {
    setConfirmTerminateId(null)
    setActionError(null)
    setPendingId(row.id)
    try {
      // Ruling C: attachControl -> terminateExecution -> releaseLease
      // (best-effort). A terminated execution may already have dropped its
      // lease, so releaseLease's own rejection is swallowed below and must
      // never surface as an action error; a failure of the first two steps
      // (e.g. attachControl's `lease_held`) does surface, via the outer catch.
      const lease = await attachControl(hostId, row.id)
      await terminateExecution(hostId, row.id, lease.lease_id)
      await releaseLease(hostId, row.id, lease.lease_id).catch(() => {})
      refetch(hostId, includeArchivedRef.current)
    } catch (err) {
      setActionError({ action: t('hosts.nex.executions.terminate'), code: errorCode(err) })
    } finally {
      if (mountedRef.current) setPendingId(null)
    }
  }, [hostId, refetch, t])

  const handleArchiveToggle = useCallback(async (row: ExecutionSummary) => {
    setActionError(null)
    setPendingId(row.id)
    try {
      await archiveExecution(hostId, row.id, row.archived)
      refetch(hostId, includeArchivedRef.current)
    } catch (err) {
      const label = row.archived ? t('hosts.nex.executions.unarchive') : t('hosts.nex.executions.archive')
      setActionError({ action: label, code: errorCode(err) })
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
              onChange={(e) => setIncludeArchived(e.target.checked)}
            />
            {t('hosts.nex.executions.include_archived')}
          </label>
          <button
            type="button"
            onClick={() => refetch(hostId, includeArchived)}
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
                  onOpen={() => handleOpen(row)}
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
