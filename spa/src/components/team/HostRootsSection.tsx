// spa/src/components/team/HostRootsSection.tsx — "other hosts" under a team's own roots (XR-3): for every paired host, the folders it
// granted this lead host (applied), the changes still on their way (pending), and the ones that failed or were cancelled. The grant is
// the lead HOST's, shared by every team on it, so two teams' editors show the same data (lib/team/host-roots.ts keeps it by lead host).
// Hidden while the lead daemon has no routes (404) or has not answered yet.
import { X } from '@phosphor-icons/react'
import { useI18nStore } from '../../stores/useI18nStore'
import { hostLabel, useHostLook } from '../../lib/host-look'
import { midEllipsis } from '../../lib/team/roots'
import { changeHostRoot, dismissHostRootChange, useHostRootsStore, viewOf, type HostRootChange, type HostStatus, type PairedHostRoots } from '../../lib/team/host-roots'
import { hostRootsErrorText } from '../../lib/team/host-roots-text'
import { useHostRootsSync } from '../../hooks/useHostRootsSync'

type T = (key: string, params?: Record<string, string | number>) => string

function pendingText(status: HostStatus, alias: string, t: T): string {
  if (status === 'unsupported') return t('unattended.hostroots.needs_update', { alias })
  if (status === 'unauthorized') return t('unattended.hostroots.pairing_lost', { alias })
  if (status === 'ok') return t('unattended.hostroots.applying')
  return t('unattended.hostroots.wait_online', { alias }) // offline, consent_off, unknown: it is not there to take it yet
}

interface Row { key: string; path: string; change: HostRootChange | null; kind: 'applied' | 'removing' | 'adding' | 'refused' | 'dropped' }

/** The cached roots (applied; a pending remove marks its row), then the changes that are not just a root with a mark on it. */
function rowsOf(h: PairedHostRoots): Row[] {
  const rows: Row[] = []
  for (const path of h.roots) {
    const removing = h.changes.find((c) => c.state === 'pending' && c.op === 'remove' && c.path === path) ?? null
    rows.push({ key: `r:${path}`, path, change: removing, kind: removing ? 'removing' : 'applied' })
  }
  for (const c of h.changes) {
    if (c.state === 'pending') {
      if (c.op === 'add' && !h.roots.includes(c.path)) rows.push({ key: `c:${c.id}`, path: c.path, change: c, kind: 'adding' })
    } else {
      rows.push({ key: `c:${c.id}`, path: c.path, change: c, kind: c.state })
    }
  }
  return rows
}

export function HostRootsSection({ leadId }: { leadId: string }) {
  useHostRootsSync(leadId)
  const t = useI18nStore((s) => s.t) as T
  const view = useHostRootsStore((s) => viewOf(s, leadId))
  const leadName = hostLabel(leadId, useHostLook(leadId))
  if (view.load !== 'ok' || view.hosts.length === 0) return null
  return (
    <div data-testid="host-roots" className="flex flex-col gap-1">
      <div className="text-text-muted">{t('unattended.hostroots.label')}</div>
      {view.hosts.map((h) => (
        <section key={h.hostId || h.alias} data-testid="host-roots-host" data-alias={h.alias} className="flex flex-col gap-0.5">
          <div className="flex flex-wrap items-baseline gap-x-2">
            <span className="font-medium text-text-primary">{h.alias}</span>
            <span className="text-text-muted">{t('unattended.hostroots.shared', { alias: h.alias, lead: leadName })}</span>
          </div>
          {h.status === 'consent_off' && <div data-testid="host-roots-consent-off" className="text-status-warning-text">{t('unattended.hostroots.consent_off', { alias: h.alias, lead: leadName })}</div>}
          <ul className="flex flex-col gap-0.5">
            {rowsOf(h).map((r) => {
              const rowErr = view.rowError !== null && view.rowError.alias === h.alias && view.rowError.path === r.path ? view.rowError.error : null
              return (
                <li key={r.key} data-testid="host-roots-row" data-kind={r.kind} className="flex flex-col">
                  <div className="flex items-center gap-1">
                    <span data-testid="host-roots-path" title={r.path} className="min-w-0 flex-1 truncate font-mono text-text-primary">{midEllipsis(r.path)}</span>
                    <RowTail row={r} host={h} leadId={leadId} writing={view.writing} t={t} />
                  </div>
                  {rowErr && <div data-testid="host-roots-row-error" role="alert" className="text-status-error">{hostRootsErrorText(rowErr, t)}</div>}
                </li>
              )
            })}
          </ul>
        </section>
      ))}
    </div>
  )
}

function RowTail({ row, host, leadId, writing, t }: { row: Row; host: PairedHostRoots; leadId: string; writing: boolean; t: T }) {
  const status = (txt: string, tone = 'text-text-muted') => <span data-testid="host-roots-status" className={`shrink-0 ${tone}`}>{txt}</span>
  const remove = (
    <button
      type="button" data-testid="host-roots-remove" aria-label={t('unattended.roots.remove', { path: row.path })} disabled={writing}
      onClick={() => { void changeHostRoot(leadId, host, 'remove', row.path) }}
      className="inline-flex h-5 w-5 shrink-0 cursor-pointer items-center justify-center rounded text-text-secondary hover:bg-surface-hover hover:text-text-primary disabled:pointer-events-none disabled:opacity-30"
    ><X size={12} /></button>
  )
  const dismiss = (id: string) => (
    <button
      type="button" data-testid="host-roots-dismiss" disabled={writing} onClick={() => { void dismissHostRootChange(leadId, id) }}
      className="shrink-0 cursor-pointer rounded border border-border-subtle px-1.5 text-text-secondary hover:text-text-primary disabled:pointer-events-none disabled:opacity-40"
    >{t('unattended.hostroots.dismiss')}</button>
  )
  switch (row.kind) {
    case 'applied': return remove
    case 'adding': return <>{status(pendingText(host.status, host.alias, t))}{remove}</>
    case 'removing': return status(t('unattended.hostroots.removing', { status: pendingText(host.status, host.alias, t) }))
    case 'refused': return <>{status(t('unattended.hostroots.failed', { reason: row.change?.detail || row.change?.code || '' }), 'text-status-error')}{row.change && dismiss(row.change.id)}</>
    case 'dropped': return <>{status(t('unattended.hostroots.dropped'))}{row.change && dismiss(row.change.id)}</>
  }
}
