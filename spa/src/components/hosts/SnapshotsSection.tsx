import { useCallback, useEffect, useMemo, useState } from 'react'
import { useI18nStore } from '../../stores/useI18nStore'
import { selectDevHostId, useHostStore } from '../../stores/useHostStore'
import { useHostConfigStore } from '../../stores/useHostConfigStore'
import { useRebuildStore } from '../../stores/useRebuildStore'
import { useTabStore } from '../../stores/useTabStore'
import { readSnapshot, setSessionMetaCwd, writeSnapshot } from '../../lib/snapshot/storage'
import { rebuildAllSessions, SNAPSHOT_LOCK_OWNER } from '../../lib/snapshot/restore'
import { filterSnapshotByHost, selectSnapshotClientHostId } from '../../lib/snapshot/filter'
import type { WorkspaceSnapshot } from '../../lib/snapshot/types'
import { listSessions } from '../../lib/host-api'
import { BATCH_LOCK_OWNER, groupForBatch, planForRecord, runBatchRebuild } from '../../lib/rebuild/batch'
import { batchCandidates, collectRecordRows, type BatchCandidate } from '../../lib/rebuild/eligibility'
import { rebuildPane } from '../../lib/rebuild/engine'
import { resumeLookupFor } from '../../lib/resume-templates'
import { restoreAction, StatusLine, useSnapshotActions, type HostLive, type Status } from '../settings/snapshot/shared'
import { RebuildRecordsBlock } from '../settings/snapshot/RebuildRecordsBlock'
import { TmuxBlock } from '../settings/snapshot/TmuxBlock'
import { ClientSnapshotBlock } from '../settings/snapshot/ClientSnapshotBlock'

/** One live-session lookup, tagged with what it was for so a stale one reads as loading. */
interface LiveResult {
  hostId: string
  snap: WorkspaceSnapshot | null
  live: HostLive
}

/**
 * Host › Snapshots (host-launcher spec §4.3). The host-scoped part — per-tab
 * rebuild records and captured tmux sessions — shows and acts on THIS host
 * only. The client-scoped block (whole device, all hosts) is rendered on the
 * dev host's page, or the first host when no dev host is set.
 *
 * This section owns the page's ONE `useSnapshotActions` instance and hands it
 * to the client block, so capture / restore there and a rebuild here share a
 * single-flight guard and never interleave (plan amendment A1).
 */
export function SnapshotsSection({ hostId }: { hostId: string }) {
  const t = useI18nStore((s) => s.t)
  const [snap, setSnap] = useState<WorkspaceSnapshot | null>(() => readSnapshot())
  // Storage returns a fresh object, so this bumps `snap` → the live lookup below
  // re-runs and every handler closes over the latest snapshot.
  const refresh = useCallback(() => setSnap(readSnapshot()), [])
  const clientHostId = useHostStore(selectSnapshotClientHostId)
  const devHostId = useHostStore(selectDevHostId)
  const actions = useSnapshotActions(t)
  const { busy, busyRef, status, lockedOut, run } = actions

  const hostSnap = useMemo(() => (snap ? filterSnapshotByHost(snap, hostId) : null), [snap, hostId])
  // The per-tab rebuild records — a view over the LIVE tab tree, not the
  // captured snapshot, so it renders whether or not a snapshot exists.
  const tabs = useTabStore((s) => s.tabs)
  const rows = useMemo(() => collectRecordRows(tabs).filter((r) => r.hostId === hostId), [tabs, hostId])
  const { groups, excluded } = useMemo(() => groupForBatch(batchCandidates(rows)), [rows])
  const hasHostData = rows.length > 0 || Object.keys(hostSnap?.sessionMeta ?? {}).length > 0

  // Reconcile this host once per snapshot against its live session list. A
  // rejection means offline → every row ⚪ and nothing is ever created here.
  // setState only happens in the async callbacks; a result for an older
  // snapshot / host is ignored by the derivation below and reads as loading.
  const [liveResult, setLiveResult] = useState<LiveResult | null>(null)
  useEffect(() => {
    if (!hasHostData) return
    let cancelled = false
    listSessions(hostId)
      .then((sessions) => { if (!cancelled) setLiveResult({ hostId, snap, live: sessions }) })
      .catch(() => { if (!cancelled) setLiveResult({ hostId, snap, live: 'offline' }) })
    return () => { cancelled = true }
  }, [hostId, snap, hasHostData])
  const live: HostLive = liveResult && liveResult.hostId === hostId && liveResult.snap === snap ? liveResult.live : 'loading'
  const liveByHost = useMemo(() => ({ [hostId]: live }), [hostId, live])

  // Persist a manual cwd edit, then re-read so the row's health reflects it.
  const handleCommitCwd = (h: string, code: string, value: string) => {
    // An in-flight action closed over the pre-edit snapshot and would overwrite
    // this write; a held lock means a rebuild is re-pointing panes right now.
    if (busyRef.current || useRebuildStore.getState().lockedBy !== null) return
    const cur = readSnapshot()
    if (!cur) return
    // Written to the FULL snapshot; the filtered copy is never persisted.
    writeSnapshot(setSessionMetaCwd(cur, h, code, value))
    refresh()
  }

  const handleRebuildSessions = () => {
    if (!snap) return
    void run(SNAPSHOT_LOCK_OWNER.rebuildAll, 'settings.snapshot.toast.restoring',
      () => restoreAction(t, () => rebuildAllSessions(filterSnapshotByHost(snap, hostId))), refresh)
  }

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
  })

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
  })

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

      {!hostSnap ? (
        <p data-testid="snapshot-empty" className="text-xs text-text-muted mt-4">{t('settings.snapshot.empty')}</p>
      ) : (
        <>
          {/* None of the legacy snapshot actions ever runs an agent command —
              they recreate the session as a bare shell. */}
          <p data-testid="snapshot-legacy-shell-only" className="mt-6 text-xs text-status-warning">
            {t('rebuild.legacy_shell_only')}
          </p>
          <TmuxBlock
            snap={hostSnap}
            liveByHost={liveByHost}
            busy={busy || lockedOut(SNAPSHOT_LOCK_OWNER.rebuildAll)}
            onRebuild={handleRebuildSessions}
            onCommitCwd={handleCommitCwd}
            t={t}
          />
        </>
      )}

      {clientHostId === hostId && (
        <ClientSnapshotBlock
          snap={snap}
          onRefresh={refresh}
          showNoDevHint={devHostId === null}
          actions={actions}
          t={t}
        />
      )}

      <StatusLine status={status} />
    </div>
  )
}
