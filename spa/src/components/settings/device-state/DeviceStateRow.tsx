// spa/src/components/settings/device-state/DeviceStateRow.tsx — one computer's
// saved state (spec §4.1): summary, lazy tab tree, Replace / Delete confirms.
import { useRef, useState } from 'react'
import { CaretDown, CaretRight } from '@phosphor-icons/react'
import { getDeviceState } from '../../../lib/device-state/api'
import type { DeviceStateRecord, DeviceStateSummary } from '../../../lib/device-state/api'
import { collectLeaves } from '../../../lib/pane-tree'
import { getPaneLabel } from '../../../lib/pane-labels'
import type { WorkspaceSnapshot } from '../../../lib/snapshot/types'
import { useI18nStore } from '../../../stores/useI18nStore'

type T = ReturnType<typeof useI18nStore.getState>['t']

function formatRelativeTime(t: T, ms: number): string {
  const diffSec = Math.max(0, Math.floor((Date.now() - ms) / 1000))
  if (diffSec < 60) return t('settings.snapshot.time.secondsAgo', { n: diffSec })
  const diffMin = Math.floor(diffSec / 60)
  if (diffMin < 60) return t('settings.snapshot.time.minutesAgo', { n: diffMin })
  const diffHr = Math.floor(diffMin / 60)
  if (diffHr < 24) return t('settings.snapshot.time.hoursAgo', { n: diffHr })
  return t('settings.snapshot.time.daysAgo', { n: Math.floor(diffHr / 24) })
}

const BTN =
  'shrink-0 rounded-md border border-border-default px-2.5 py-1 text-xs text-text-secondary hover:text-text-primary hover:border-border-active disabled:opacity-50 disabled:cursor-not-allowed'

type Confirm = 'replace' | 'delete' | null
type Loaded = { kind: 'idle' } | { kind: 'loading' } | { kind: 'error'; message: string } | { kind: 'loaded'; record: DeviceStateRecord }

export interface DeviceStateRowProps {
  hostId: string
  summary: DeviceStateSummary
  isOwn: boolean
  /** A section action is in flight — no new action may start. */
  busy: boolean
  /** The global operation lock is held by someone other than Replace. */
  replaceLocked: boolean
  /** Receives a loader that returns the full record (fetched once, then cached). */
  onReplace: (load: () => Promise<DeviceStateRecord>) => void
  onDelete: (clientId: string) => void
}

export function DeviceStateRow({ hostId, summary, isOwn, busy, replaceLocked, onReplace, onDelete }: DeviceStateRowProps) {
  const t = useI18nStore((s) => s.t)
  const [expanded, setExpanded] = useState(false)
  const [confirm, setConfirm] = useState<Confirm>(null)
  const [loaded, setLoaded] = useState<Loaded>({ kind: 'idle' })
  // The one in-flight/settled fetch, shared by expand and Replace so the record
  // is requested at most once per row.
  const fetchRef = useRef<Promise<DeviceStateRecord> | null>(null)
  const id = summary.clientId

  const load = (): Promise<DeviceStateRecord> => {
    if (!fetchRef.current) {
      setLoaded({ kind: 'loading' })
      const p = getDeviceState(hostId, id)
      fetchRef.current = p
      p.then(
        (record) => setLoaded({ kind: 'loaded', record }),
        (e: unknown) => {
          // Allow a later retry after a failure.
          fetchRef.current = null
          setLoaded({ kind: 'error', message: e instanceof Error ? e.message : String(e) })
        },
      )
    }
    return fetchRef.current
  }

  const toggleExpand = () => {
    const next = !expanded
    setExpanded(next)
    if (next && loaded.kind !== 'loaded') load().catch(() => {})
  }

  const confirmAction = () => {
    const action = confirm
    setConfirm(null)
    if (action === 'replace') onReplace(load)
    else if (action === 'delete') onDelete(id)
  }

  return (
    <li
      data-testid={`device-state-row-${id}`}
      data-own={isOwn ? 'true' : 'false'}
      className="border-t border-border-default py-2 text-xs"
    >
      <div className="flex flex-wrap items-center justify-between gap-2">
        <button
          type="button"
          data-testid={`device-state-expand-${id}`}
          aria-expanded={expanded}
          aria-label={t('settings.device_state.action.expand')}
          onClick={toggleExpand}
          className="flex min-w-0 items-center gap-1.5 text-left text-text-primary"
        >
          {expanded ? <CaretDown size={12} /> : <CaretRight size={12} />}
          <span className="truncate">{summary.deviceName}</span>
          {isOwn && (
            <span
              data-testid={`device-state-own-badge-${id}`}
              className="rounded bg-surface-secondary px-1.5 py-0.5 text-[10px] text-text-secondary"
            >
              {t('settings.device_state.list.this_computer')}
            </span>
          )}
        </button>
        <div className="flex items-center gap-2">
          <button
            type="button"
            data-testid={`device-state-replace-${id}`}
            onClick={() => setConfirm('replace')}
            disabled={busy || replaceLocked}
            className={BTN}
          >
            {t('settings.device_state.action.replace')}
          </button>
          <button
            type="button"
            data-testid={`device-state-delete-${id}`}
            onClick={() => setConfirm('delete')}
            disabled={busy || isOwn}
            className={BTN}
          >
            {t('settings.device_state.action.delete')}
          </button>
        </div>
      </div>

      <div className="mt-0.5 flex flex-wrap gap-x-3 text-text-muted">
        <span data-testid={`device-state-updated-${id}`}>{formatRelativeTime(t, summary.updatedAt)}</span>
        {summary.appVersion && <span className="font-mono">{summary.appVersion}</span>}
        <span
          data-testid={`device-state-counts-${id}`}
          data-workspaces={summary.workspaceCount}
          data-tabs={summary.tabCount}
        >
          {t('settings.device_state.list.counts', { workspaces: summary.workspaceCount, tabs: summary.tabCount })}
        </span>
      </div>

      {confirm && (
        <div
          data-testid={`device-state-${confirm}-confirm-${id}`}
          role="group"
          className="mt-2 flex flex-wrap items-center gap-2 text-status-warning"
        >
          <span>
            {t(confirm === 'replace' ? 'settings.device_state.action.replace_confirm' : 'settings.device_state.action.delete_confirm')}
          </span>
          <button
            type="button"
            data-testid={`device-state-confirm-${id}`}
            onClick={confirmAction}
            disabled={busy || (confirm === 'replace' && replaceLocked)}
            className={BTN}
          >
            {t('settings.device_state.action.confirm')}
          </button>
          <button type="button" data-testid={`device-state-cancel-${id}`} onClick={() => setConfirm(null)} className={BTN}>
            {t('settings.device_state.action.cancel')}
          </button>
        </div>
      )}

      {loaded.kind === 'error' && (
        <p data-testid={`device-state-row-error-${id}`} className="mt-1 text-red-500">
          {t('settings.device_state.list.load_failed', { message: loaded.message })}
        </p>
      )}

      {expanded && loaded.kind === 'loaded' && <TabTree payload={loaded.record.payload} t={t} />}
    </li>
  )
}

function TabTree({ payload, t }: { payload: WorkspaceSnapshot; t: T }) {
  const noSessions = { getByCode: () => undefined }
  const workspaces = { getById: (wsId: string) => payload.workspaces.find((w) => w.id === wsId) }
  const labelOf = (tabId: string): string | null => {
    const tab = payload.tabs[tabId]
    if (!tab) return null
    const first = collectLeaves(tab.layout)[0]
    return first ? getPaneLabel(first.content, noSessions, workspaces, t) : null
  }
  const inWorkspace = new Set(payload.workspaces.flatMap((w) => w.tabs))
  const loose = payload.tabOrder.filter((tabId) => !inWorkspace.has(tabId))

  const tabList = (tabIds: string[]) => (
    <ul className="ml-4 mt-0.5 flex flex-col gap-0.5">
      {tabIds.map((tabId) => {
        const label = labelOf(tabId)
        if (label === null) return null
        return (
          <li key={tabId} data-testid={`device-state-tab-${tabId}`} className="font-mono text-text-muted">
            {label}
          </li>
        )
      })}
    </ul>
  )

  return (
    <ul className="ml-5 mt-2 flex flex-col gap-1.5 text-text-secondary">
      {payload.workspaces.map((ws) => (
        <li key={ws.id} data-testid={`device-state-ws-${ws.id}`}>
          <span className="text-text-primary">{ws.name}</span>
          {tabList(ws.tabs)}
        </li>
      ))}
      {loose.length > 0 && (
        <li data-testid="device-state-no-ws">
          <span className="text-text-primary">{t('settings.device_state.list.no_workspace')}</span>
          {tabList(loose)}
        </li>
      )}
    </ul>
  )
}
