import { useEffect, useState } from 'react'
import {
  ArrowsClockwise,
  Camera,
  CheckCircle,
  CircleDashed,
  CircleNotch,
  Warning,
  WarningCircle,
} from '@phosphor-icons/react'
import { SettingItem } from './SettingItem'
import { useI18nStore } from '../../stores/useI18nStore'
import { readSnapshot } from '../../lib/snapshot/storage'
import { listSessions } from '../../lib/host-api'
import type { Session } from '../../lib/host-api'
import { collectLeaves } from '../../lib/pane-tree'
import type { SessionMeta, WorkspaceSnapshot } from '../../lib/snapshot/types'
import type { PaneContent } from '../../types/tab'

/** Per-host live-session lookup state: still loading, host offline, or the list. */
type HostLive = 'loading' | 'offline' | Session[]

/** Four-state (plus loading) health of a captured session vs. the live world. */
type Health = 'loading' | 'live' | 'dead' | 'structure' | 'offline'

/**
 * Reconcile one captured {@link SessionMeta} against its host's live list. Mirrors
 * the engine's reattach rule (code AND name must match — a code-only match is a
 * different session after a tmux restart, so it is NOT "live").
 *
 * Precedence: an unreachable host wins (every row ⚪) → a live code+name match is
 * 🟢 → a non-restorable entry is ⚠️ (no cwd, cannot rebuild) → otherwise 🔴
 * (restorable + dead, so "Rebuild all" can recreate it).
 */
function computeHealth(meta: SessionMeta, live: HostLive): Health {
  if (live === 'loading') return 'loading'
  if (live === 'offline') return 'offline'
  const match = live.some((s) => s.code === meta.sessionCode && s.name === meta.name)
  if (match) return 'live'
  if (!meta.restorable) return 'structure'
  return 'dead'
}

const HEALTH_ICON: Record<Health, typeof CheckCircle> = {
  loading: CircleNotch,
  live: CheckCircle,
  dead: WarningCircle,
  structure: Warning,
  offline: CircleDashed,
}

const HEALTH_COLOR: Record<Health, string> = {
  loading: 'text-text-muted',
  live: 'text-green-500',
  dead: 'text-red-500',
  structure: 'text-yellow-500',
  offline: 'text-text-muted',
}

/** Human label for a leaf pane, used in the Tabs tree (block 2). */
function leafLabel(content: PaneContent): string {
  switch (content.kind) {
    case 'tmux-session':
      return content.cachedName || content.sessionCode
    case 'editor':
    case 'image-preview':
    case 'pdf-preview':
      return content.filePath
    case 'browser':
      return content.url
    default:
      return content.kind
  }
}

function formatRelativeTime(t: ReturnType<typeof useI18nStore.getState>['t'], ms: number): string {
  const diffSec = Math.floor((Date.now() - ms) / 1000)
  if (diffSec < 60) return t('settings.snapshot.time.secondsAgo', { n: diffSec })
  const diffMin = Math.floor(diffSec / 60)
  if (diffMin < 60) return t('settings.snapshot.time.minutesAgo', { n: diffMin })
  const diffHr = Math.floor(diffMin / 60)
  if (diffHr < 24) return t('settings.snapshot.time.hoursAgo', { n: diffHr })
  const diffDay = Math.floor(diffHr / 24)
  return t('settings.snapshot.time.daysAgo', { n: diffDay })
}

export function SnapshotSettingsSection() {
  const t = useI18nStore((s) => s.t)
  const [snap] = useState<WorkspaceSnapshot | null>(() => readSnapshot())
  // Seed every captured host to 'loading' up front (lazy init) so the effect
  // only ever calls setState asynchronously from the resolve/reject callbacks.
  const [liveByHost, setLiveByHost] = useState<Record<string, HostLive>>(() =>
    snap
      ? Object.fromEntries(Object.keys(snap.sessionMeta).map((h) => [h, 'loading' as HostLive]))
      : {},
  )

  // On mount (per snapshot), reconcile each captured host exactly once against
  // its live session list. A rejection means the host is offline → every row for
  // that host renders ⚪ and no createSession is ever attempted here.
  useEffect(() => {
    if (!snap) return
    const hostIds = Object.keys(snap.sessionMeta)
    let cancelled = false
    for (const hostId of hostIds) {
      listSessions(hostId)
        .then((sessions) => {
          if (!cancelled) setLiveByHost((prev) => ({ ...prev, [hostId]: sessions }))
        })
        .catch(() => {
          if (!cancelled) setLiveByHost((prev) => ({ ...prev, [hostId]: 'offline' }))
        })
    }
    return () => {
      cancelled = true
    }
  }, [snap])

  return (
    <div>
      <h2 className="text-lg text-text-primary">{t('settings.section.snapshot')}</h2>
      <p className="text-xs text-text-secondary mb-6">{t('settings.snapshot.description')}</p>

      <SettingItem
        label={t('settings.snapshot.capture')}
        description={
          snap
            ? t('settings.snapshot.capturedAt', { time: formatRelativeTime(t, snap.capturedAt) })
            : t('settings.snapshot.neverCaptured')
        }
      >
        <button
          type="button"
          data-testid="snapshot-capture-btn"
          className="flex items-center gap-1.5 px-3 py-1.5 rounded-md border border-border-default text-text-secondary text-xs hover:text-text-primary hover:border-border-active disabled:opacity-50 disabled:cursor-not-allowed"
        >
          <Camera size={14} />
          {t('settings.snapshot.capture')}
        </button>
      </SettingItem>

      {!snap ? (
        <p data-testid="snapshot-empty" className="text-xs text-text-muted mt-4">
          {t('settings.snapshot.empty')}
        </p>
      ) : (
        <>
          <TmuxBlock snap={snap} liveByHost={liveByHost} t={t} />
          <TabsBlock snap={snap} t={t} />
        </>
      )}
    </div>
  )
}

function TmuxBlock({
  snap,
  liveByHost,
  t,
}: {
  snap: WorkspaceSnapshot
  liveByHost: Record<string, HostLive>
  t: ReturnType<typeof useI18nStore.getState>['t']
}) {
  const rows: SessionMeta[] = []
  for (const perHost of Object.values(snap.sessionMeta)) {
    for (const meta of Object.values(perHost)) rows.push(meta)
  }

  return (
    <div data-testid="snapshot-tmux-block" className="mt-6">
      <div className="flex items-center justify-between mb-2">
        <h3 className="text-sm text-text-primary">{t('settings.snapshot.tmux.title')}</h3>
        <button
          type="button"
          data-testid="snapshot-rebuild-btn"
          className="flex items-center gap-1.5 px-3 py-1.5 rounded-md border border-border-default text-text-secondary text-xs hover:text-text-primary hover:border-border-active disabled:opacity-50 disabled:cursor-not-allowed"
        >
          <ArrowsClockwise size={14} />
          {t('settings.snapshot.tmux.rebuildAll')}
        </button>
      </div>

      {rows.length === 0 ? (
        <p className="text-xs text-text-muted">{t('settings.snapshot.tmux.none')}</p>
      ) : (
        <div className="overflow-x-auto">
          <table className="w-full text-xs text-text-secondary">
            <thead>
              <tr className="text-text-muted text-left">
                <th className="py-1 pr-3 font-normal">{t('settings.snapshot.col.host')}</th>
                <th className="py-1 pr-3 font-normal">{t('settings.snapshot.col.name')}</th>
                <th className="py-1 pr-3 font-normal">{t('settings.snapshot.col.cwd')}</th>
                <th className="py-1 pr-3 font-normal">{t('settings.snapshot.col.command')}</th>
                <th className="py-1 font-normal">{t('settings.snapshot.col.health')}</th>
              </tr>
            </thead>
            <tbody>
              {rows.map((meta) => {
                const health = computeHealth(meta, liveByHost[meta.hostId] ?? 'loading')
                const Icon = HEALTH_ICON[health]
                return (
                  <tr key={`${meta.hostId}:${meta.sessionCode}`} className="border-t border-border-default">
                    <td className="py-1 pr-3 text-text-primary">{meta.hostId}</td>
                    <td className="py-1 pr-3">{meta.name}</td>
                    <td className="py-1 pr-3 font-mono">{meta.cwd ?? '—'}</td>
                    <td className="py-1 pr-3 font-mono">{meta.currentCommand ?? '—'}</td>
                    <td className="py-1">
                      <span
                        data-testid={`snapshot-health-${meta.hostId}-${meta.sessionCode}`}
                        data-health={health}
                        title={t(`settings.snapshot.health.${health}`)}
                        className={`inline-flex items-center gap-1 ${HEALTH_COLOR[health]}`}
                      >
                        <Icon size={14} className={health === 'loading' ? 'animate-spin' : ''} />
                        {t(`settings.snapshot.health.${health}`)}
                      </span>
                    </td>
                  </tr>
                )
              })}
            </tbody>
          </table>
        </div>
      )}
    </div>
  )
}

function TabsBlock({
  snap,
  t,
}: {
  snap: WorkspaceSnapshot
  t: ReturnType<typeof useI18nStore.getState>['t']
}) {
  return (
    <div data-testid="snapshot-tabs-block" className="mt-6">
      <div className="flex items-center justify-between mb-2">
        <h3 className="text-sm text-text-primary">{t('settings.snapshot.tabs.title')}</h3>
        <button
          type="button"
          data-testid="snapshot-restore-tab-btn"
          className="flex items-center gap-1.5 px-3 py-1.5 rounded-md border border-border-default text-text-secondary text-xs hover:text-text-primary hover:border-border-active disabled:opacity-50 disabled:cursor-not-allowed"
        >
          {t('settings.snapshot.tabs.restoreLayout')}
        </button>
      </div>

      <ul className="text-xs text-text-secondary flex flex-col gap-2">
        {snap.workspaces.map((ws) => (
          <li key={ws.id} data-testid={`snapshot-ws-${ws.id}`}>
            <span className="text-text-primary">{ws.name}</span>
            <ul className="ml-4 mt-1 flex flex-col gap-1">
              {ws.tabs.map((tabId) => {
                const tab = snap.tabs[tabId]
                if (!tab) return null
                const leaves = collectLeaves(tab.layout)
                return (
                  <li key={tabId} data-testid={`snapshot-tab-${tabId}`}>
                    <ul className="flex flex-col gap-0.5">
                      {leaves.map((pane) => (
                        <li key={pane.id} className="font-mono text-text-muted" data-testid="snapshot-pane-leaf">
                          {leafLabel(pane.content)}
                        </li>
                      ))}
                    </ul>
                  </li>
                )
              })}
            </ul>
          </li>
        ))}
      </ul>
    </div>
  )
}
