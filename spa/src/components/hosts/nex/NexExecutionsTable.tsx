// spa/src/components/hosts/nex/NexExecutionsTable.tsx — P-B.3 Task 6: the
// third card of the Host → Nex sub-page. Lists executions for `hostId` and
// keeps the list fresh via the site-wide Nex SSE stream, which is a refresh
// signal only (nexen/api/sse.go:118,195) — frame contents are never applied,
// only its durable cursor is kept so a reconnect does not replay history
// from seq 0.
import { useCallback, useEffect, useRef, useState } from 'react'
import { ArrowsClockwise } from '@phosphor-icons/react'
import { useI18nStore } from '../../../stores/useI18nStore'
import { openExecutionDetailTab } from '../../../lib/deeplink/deeplinkResolver'
import { archiveExecution, attachControl, listExecutions, releaseLease, terminateExecution } from '../../../lib/nex/nex-api'
import { openNexSse, type NexSseStatus } from '../../../lib/nex/nex-sse'
import { NexApiError, type ExecutionSummary } from '../../../lib/nex/types'
import NexExecutionRow from './NexExecutionRow'

export interface NexExecutionsTableProps {
  hostId: string
  enabled: boolean
}

/** Trailing debounce applied to an SSE-triggered refetch (spec §4.4.3). */
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

  // React 19 StrictMode dev-double-invokes every effect (mount -> cleanup ->
  // mount) to surface missing cleanup. A cleanup-only effect body (no setup
  // statement) leaves this permanently false after that synthetic cycle, so
  // every subsequent `refetch`/action `finally` guard silently no-ops and no
  // row ever renders under the dev server's <StrictMode> (spa/src/main.tsx).
  // Setting it back to true in the setup half fixes that for both the real
  // mount and StrictMode's extra one.
  const mountedRef = useRef(true)
  useEffect(() => {
    mountedRef.current = true
    return () => { mountedRef.current = false }
  }, [])

  // Cursor for Last-Event-ID: only frames carrying a non-null `id` (durable
  // events) may advance it — transient snapshot/stream frames never do
  // (spec §4.2.3: the site stream's `id:` is the last durable seq).
  const lastIdRef = useRef<number | null>(null)
  const debounceRef = useRef<ReturnType<typeof setTimeout> | null>(null)
  // Guards a stale response (previous host, or superseded by a newer
  // request) from landing after the request it belongs to is no longer the
  // latest one in flight.
  const requestTokenRef = useRef(0)

  // Host-scoped state reset. Runs before the data+SSE effect below (source
  // order = commit order for effects sharing a dependency change) so a host
  // switch clears the cursor AND any rows/errors left over from the previous
  // host before the new host's fetch is even issued — otherwise a stale row
  // survives on screen (and its Open would target the wrong host, I10) until
  // the new fetch resolves, or forever if it fails. An `enabled` toggle alone
  // (same host) must NOT reset any of this — that would both replay the
  // site's full durable history on the SSE reconnect and throw away rows the
  // user was just looking at for no reason.
  useEffect(() => {
    lastIdRef.current = null
    setItems([])
    setLoadError(null)
    setActionError(null)
    setConfirmTerminateId(null)
    setPendingId(null)
  }, [hostId])

  const refetch = useCallback((forHostId: string, includeArchivedValue: boolean) => {
    const token = ++requestTokenRef.current
    listExecutions(forHostId, { includeArchived: includeArchivedValue, limit: 100 })
      .then((page) => {
        // The host check (not just the token) matters: an action's own
        // post-success refetch is issued with the hostId it captured when
        // the action *started*, which can be stale by the time its awaits
        // resolve (see handleTerminateConfirm/handleArchiveToggle). Guarding
        // on the token alone would still let that stale call win if it
        // happens to be the most recently *issued* one.
        if (!mountedRef.current || token !== requestTokenRef.current || forHostId !== hostIdRef.current) return
        setItems(page.items)
        setLoadError(null)
      })
      .catch((err: unknown) => {
        if (!mountedRef.current || token !== requestTokenRef.current || forHostId !== hostIdRef.current) return
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

    const scheduleRefetch = () => {
      if (debounceRef.current) clearTimeout(debounceRef.current)
      debounceRef.current = setTimeout(() => {
        debounceRef.current = null
        refetch(hostId, includeArchivedRef.current)
      }, LIST_REFRESH_DEBOUNCE_MS)
    }

    // Tracks the previous status so a reconnect (reconnecting -> open) can
    // trigger its own refetch — events during the outage gap have no
    // guarantee of replay unless a durable id was already seen (and even
    // then, only IDs after it replay; a gap before the first-ever durable id
    // replays nothing). Reset per connection so a fresh effect run (host
    // change) doesn't carry over a stale status from the previous one.
    let prevStatus: NexSseStatus | null = null

    const handle = openNexSse({
      hostId,
      url: '/api/nex/v1/events',
      getLastEventId: () => lastIdRef.current,
      onFrame: (frame) => {
        if (frame.id != null) {
          lastIdRef.current = Math.max(lastIdRef.current ?? 0, Number(frame.id))
        }
        scheduleRefetch()
      },
      onStatus: (status) => {
        if (status === 'open' && prevStatus === 'reconnecting') {
          scheduleRefetch()
        }
        prevStatus = status
      },
    })

    return () => {
      handle.close()
      if (debounceRef.current) {
        clearTimeout(debounceRef.current)
        debounceRef.current = null
      }
    }
  }, [hostId, enabled, refetch])

  // Refetch on an include-archived toggle only. Compares against the
  // previous *value* (not a "have I run yet" flag) so StrictMode's
  // mount -> cleanup -> mount dev cycle — which re-runs this effect once
  // more with the same includeArchived value — is naturally a no-op instead
  // of misfiring an extra refetch (a boolean "first run" flag would already
  // be flipped by the synthetic first pass and wrongly treat the real pass
  // as "not first"). Reads hostId/enabled from refs so it never re-fires for
  // a host/enabled change it does not own — that's the effect above's job.
  const prevIncludeArchivedRef = useRef(includeArchived)
  useEffect(() => {
    if (prevIncludeArchivedRef.current === includeArchived) return
    prevIncludeArchivedRef.current = includeArchived
    if (!enabled) return
    refetch(hostIdRef.current, includeArchived)
    // eslint-disable-next-line react-hooks/exhaustive-deps -- hostId/enabled read via ref on purpose (see comment above)
  }, [includeArchived, refetch])

  const handleOpen = (row: ExecutionSummary) => {
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
      // that host's own effect already issued its own initial fetch, so a
      // refetch for the host this action started on would just be a stale
      // response racing to overwrite the current host's rows (guarded again,
      // defense in depth, inside refetch() itself).
      if (hostIdRef.current === startHostId) refetch(startHostId, includeArchivedRef.current)
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
      if (hostIdRef.current === startHostId) refetch(startHostId, includeArchivedRef.current)
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
              onChange={(e) => setIncludeArchived(e.target.checked)}
            />
            {t('hosts.nex.executions.include_archived')}
          </label>
          <button
            type="button"
            onClick={() => { if (enabled) refetch(hostId, includeArchived) }}
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
