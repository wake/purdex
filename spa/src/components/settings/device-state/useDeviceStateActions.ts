// spa/src/components/settings/device-state/useDeviceStateActions.ts — the
// device state section's action runner: one shared single-flight guard and
// status line for Replace, Merge and Delete (spec §4.3 / §5.4).
import { useRef, useState } from 'react'
import { useI18nStore } from '../../../stores/useI18nStore'
import { useRebuildStore } from '../../../stores/useRebuildStore'
import type { DeviceStateRecord } from '../../../lib/device-state/api'
import { DEVICE_STATE_LOCK_OWNER, restoreDeviceStateMerge, restoreDeviceStateReplace } from '../../../lib/device-state/restore'
import type { DeviceStateMergeReport, DeviceStateRestoreReport } from '../../../lib/device-state/restore'
import { RestoreError } from '../../../lib/snapshot/types'
import type { WorkspaceSnapshot } from '../../../lib/snapshot/types'

type T = ReturnType<typeof useI18nStore.getState>['t']

export type Tone = 'busy' | 'success' | 'warn' | 'error'
export interface ActionStatus {
  tone: Tone
  message: string
  attrs?: Record<string, number>
}

export type RestoreKind = keyof typeof DEVICE_STATE_LOCK_OWNER

interface RestoreAction<R extends DeviceStateRestoreReport> {
  run: (payload: WorkspaceSnapshot) => Promise<R>
  message: (t: T, report: R) => string
  /** Also fed the partial `RestoreError.report` on failure. */
  attrs: (report: Partial<R>) => Record<string, number>
}

export function errMessage(e: unknown): string {
  return e instanceof Error ? e.message : String(e)
}

function reportAttrs(report: Partial<DeviceStateRestoreReport>): Record<string, number> {
  return {
    'data-reattached': report.reattached ?? 0,
    'data-failed': report.failed ?? 0,
    'data-host-removed': report.hostRemoved ?? 0,
  }
}

const REPLACE: RestoreAction<DeviceStateRestoreReport> = {
  run: (payload) => restoreDeviceStateReplace(payload),
  message: (t, r) =>
    t('settings.device_state.toast.replaced', { reattached: r.reattached, failed: r.failed, hostRemoved: r.hostRemoved }),
  attrs: reportAttrs,
}

const MERGE: RestoreAction<DeviceStateMergeReport> = {
  run: (payload) => restoreDeviceStateMerge(payload),
  message: (t, r) =>
    t('settings.device_state.toast.merged', {
      addedWorkspaces: r.addedWorkspaces,
      addedTabs: r.addedTabs,
      skippedTabs: r.skippedTabs,
    }),
  attrs: (r) => ({
    ...reportAttrs(r),
    'data-added-workspaces': r.addedWorkspaces ?? 0,
    'data-added-tabs': r.addedTabs ?? 0,
    'data-skipped-tabs': r.skippedTabs ?? 0,
  }),
}

export function useDeviceStateActions(onRestored?: () => void) {
  const t = useI18nStore((s) => s.t)
  const lockedBy = useRebuildStore((s) => s.lockedBy)
  const [busy, setBusy] = useState(false)
  // Ref, not state: two clicks in one render share `busy` but not the ref.
  const busyRef = useRef(false)
  const [actionStatus, setActionStatus] = useState<ActionStatus | null>(null)

  const isForeign = (holder: string | null, kind: RestoreKind) =>
    holder !== null && holder !== DEVICE_STATE_LOCK_OWNER[kind]

  /** Runs `fn` unless another action is in flight. Returns false if refused. */
  const exclusive = async (fn: () => Promise<void>): Promise<boolean> => {
    if (busyRef.current) return false
    busyRef.current = true
    setBusy(true)
    try {
      await fn()
    } finally {
      busyRef.current = false
      setBusy(false)
    }
    return true
  }

  const restore = async <R extends DeviceStateRestoreReport>(
    kind: RestoreKind,
    action: RestoreAction<R>,
    load: () => Promise<DeviceStateRecord>,
  ) => {
    if (busyRef.current) return
    // The lock can be taken between the last render and this click.
    const holder = useRebuildStore.getState().lockedBy
    if (holder !== null && isForeign(holder, kind)) {
      setActionStatus({ tone: 'warn', message: t('settings.device_state.toast.locked', { owner: holder }) })
      return
    }
    let attempted = false
    await exclusive(async () => {
      setActionStatus({ tone: 'busy', message: t('settings.snapshot.toast.restoring') })
      try {
        const record = await load()
        attempted = true
        const report = await action.run(record.payload)
        setActionStatus({ tone: 'success', message: action.message(t, report), attrs: action.attrs(report) })
      } catch (e) {
        const message = t('settings.device_state.toast.failed', { message: errMessage(e instanceof RestoreError ? (e.cause ?? e) : e) })
        setActionStatus(
          e instanceof RestoreError
            ? { tone: 'error', message, attrs: action.attrs(e.report as Partial<R>) }
            : { tone: 'error', message },
        )
      }
    })
    // The restore may have written `-prev` and replaced the stores — let the
    // parent re-render so its Undo button sees the new backup.
    if (attempted) onRestored?.()
  }

  return {
    busy,
    actionStatus,
    setActionStatus,
    exclusive,
    replaceLocked: isForeign(lockedBy, 'replace'),
    mergeLocked: isForeign(lockedBy, 'merge'),
    replace: (load: () => Promise<DeviceStateRecord>) => restore('replace', REPLACE, load),
    merge: (load: () => Promise<DeviceStateRecord>) => restore('merge', MERGE, load),
  }
}
