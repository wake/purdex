import { useCallback, useEffect, useMemo, useState } from 'react'
import { useI18nStore } from '../../stores/useI18nStore'
import { useHostConfigStore } from '../../stores/useHostConfigStore'
import { useTabStore } from '../../stores/useTabStore'
import { listSessions } from '../../lib/host-api'
import { BATCH_LOCK_OWNER, groupForBatch, planForRecord, runBatchRebuild } from '../../lib/rebuild/batch'
import { batchCandidates, collectRecordRows, type BatchCandidate } from '../../lib/rebuild/eligibility'
import { rebuildPane } from '../../lib/rebuild/engine'
import { resumeLookupFor } from '../../lib/resume-templates'
import { StatusLine, useSnapshotActions, type HostLive, type Status } from '../settings/snapshot/shared'
import { RebuildRecordsBlock } from '../settings/snapshot/RebuildRecordsBlock'

/** One live-session lookup, tagged with what it was for so a stale one reads as loading. */
interface LiveResult {
  hostId: string
  seq: number
  live: HostLive
}

/**
 * Host › Snapshots (host-launcher spec §4.3): the per-tab rebuild records of
 * THIS host, and the actions that rebuild them. Its one `useSnapshotActions`
 * instance is the single-flight guard every rebuild here shares.
 */
export function SnapshotsSection({ hostId }: { hostId: string }) {
  const t = useI18nStore((s) => s.t)
  // Bumped once a rebuild action ends → the live lookup below re-runs.
  const [refreshSeq, setRefreshSeq] = useState(0)
  const refresh = useCallback(() => setRefreshSeq((n) => n + 1), [])
  const { busy, status, lockedOut, run } = useSnapshotActions(t)

  // The per-tab rebuild records — a view over the LIVE tab tree.
  const tabs = useTabStore((s) => s.tabs)
  const rows = useMemo(() => collectRecordRows(tabs).filter((r) => r.hostId === hostId), [tabs, hostId])
  const { groups, excluded } = useMemo(() => groupForBatch(batchCandidates(rows)), [rows])
  const hasHostData = rows.length > 0

  // Reconcile this host against its live session list, again after every
  // rebuild. A rejection means offline → every row ⚪ and nothing is ever
  // created here. setState only happens in the async callbacks; a result for
  // an older lookup / host is ignored by the derivation below and reads as loading.
  const [liveResult, setLiveResult] = useState<LiveResult | null>(null)
  useEffect(() => {
    if (!hasHostData) return
    let cancelled = false
    listSessions(hostId)
      .then((sessions) => { if (!cancelled) setLiveResult({ hostId, seq: refreshSeq, live: sessions }) })
      .catch(() => { if (!cancelled) setLiveResult({ hostId, seq: refreshSeq, live: 'offline' }) })
    return () => { cancelled = true }
  }, [hostId, refreshSeq, hasHostData])
  const live: HostLive = liveResult && liveResult.hostId === hostId && liveResult.seq === refreshSeq ? liveResult.live : 'loading'
  const liveByHost = useMemo(() => ({ [hostId]: live }), [hostId, live])

  const handleRebuildAll = () => void run(BATCH_LOCK_OWNER, 'rebuild.batch_running', async (): Promise<Status> => {
    const report = await runBatchRebuild({}, { hostId })
    if (report.status === 'blocked') {
      return { tone: 'warn', message: t('settings.snapshot.toast.locked', { owner: report.blockedBy ?? '' }) }
    }
    const created = report.groups.filter((g) => g.report.created).length
    // Every pane that ended up on a new session: the group's own source plus
    // each member that still matched its binding.
    const repointed = report.groups.reduce(
      (n, g) => n + (g.report.repointed ? 1 : 0) + g.members.filter((m) => m.repointed).length,
      0,
    )
    return {
      tone: created === report.groups.length ? 'success' : 'warn',
      message: t('rebuild.batch_report', { created, total: report.groups.length, repointed }),
      attrs: { 'data-groups': report.groups.length, 'data-created': created, 'data-repointed': repointed },
    }
  }, refresh)

  const handleRebuildOne = (pane: BatchCandidate) => void run(`rebuild:${pane.paneId}`, 'rebuild.batch_running', async (): Promise<Status> => {
    // What this row was planned from. Loading the host's config is a network
    // wait the pane can move under, so — exactly as the batch path does — the
    // baseline travels into the engine, which checks it in the same
    // synchronous step as the create.
    const expectedBinding = { hostId: pane.hostId, sessionCode: pane.sessionCode, tmuxInstance: pane.tmuxInstance }
    await useHostConfigStore.getState().ensureLoaded(pane.hostId)
    const report = await rebuildPane(
      pane.hostId, pane.tabId, pane.paneId,
      planForRecord(pane.record, resumeLookupFor(pane.hostId)),
      { expectedBinding },
    )
    if (report.steps.create.status === 'failed') {
      return { tone: 'error', message: report.steps.create.error ?? t('settings.snapshot.toast.restoreError') }
    }
    return {
      tone: report.repointed ? 'success' : 'warn',
      message: t('rebuild.batch_report', { created: 1, total: 1, repointed: report.repointed ? 1 : 0 }),
    }
  }, refresh)

  return (
    <div className="max-w-3xl">
      <h2 className="text-lg text-text-primary">{t('hosts.snapshots')}</h2>
      <p className="text-xs text-text-secondary mb-6">{t('hosts.snapshots.host_desc')}</p>

      <RebuildRecordsBlock
        rows={rows}
        groups={groups}
        excluded={excluded}
        liveByHost={liveByHost}
        busy={busy || lockedOut(BATCH_LOCK_OWNER)}
        lockedOut={lockedOut}
        onRebuildAll={handleRebuildAll}
        onRebuildOne={handleRebuildOne}
        t={t}
      />

      <StatusLine status={status} />
    </div>
  )
}
