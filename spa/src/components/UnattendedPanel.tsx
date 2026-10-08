// spa/src/components/UnattendedPanel.tsx — the "while you were away" list under the title bar's ▾ (unattended spec
// D-U23-6; plan PU-2c): what each reachable shown host's daemon approved since its switch last turned on, read page by
// page from its audit (`GET /api/team/unattended`), merged newest first: `<host>：<session> · <kind> · <HH:mm>`.
// 「顯示更多」 pages every host that still has more (`next_before`); a host that cannot be read is named, never skipped.
// A shown host that cannot be reached (`unreachableIds`) is not asked but named above the list: its daemon may still be
// approving. When no host can be reached there is no list to be empty, so only those lines show.
//
// Not tab-hosted: opened by a click of UnattendedButton's ▾ and gone when closed. The loaded pages are this
// component's own state — closing drops them and every open fetches afresh (a stale list would hide what the daemon
// approved in between). Which hosts to read is fixed at open (`hostIds`).
import { useCallback, useEffect, useRef, useState, type RefObject } from 'react'
import { FloatingPanel } from './FloatingPanel'
import { useI18nStore } from '../stores/useI18nStore'
import { hostLabel, hostLookOf } from '../lib/host-look'
import { ApprovalApiError } from '../lib/team/approval-api'
import { approvalKindLabel, approvalSessionLabel } from '../lib/team/approval-format'
import { getUnattended } from '../lib/team/unattended-api'
import type { Approval, UnattendedView } from '../lib/team/types'

const PANEL_WIDTH = 360

/** One host's share of the list. */
interface HostPages {
  rows: Approval[]
  /** The cursor of the next page; present while the daemon said `truncated`. */
  nextBefore?: number
  /** The switch's last turn-on (ms), 0 = never. */
  since: number
  /** The code of the last failed read (kept with the rows loaded before it); absent = the last read worked. */
  failed?: string
}

const pad2 = (n: number) => (n < 10 ? `0${n}` : String(n))
const clock = (ms: number) => { const d = new Date(ms); return `${pad2(d.getHours())}:${pad2(d.getMinutes())}` }
const sameDay = (a: Date, b: Date) => a.getFullYear() === b.getFullYear() && a.getMonth() === b.getMonth() && a.getDate() === b.getDate()
/** `HH:mm` today, `M/D HH:mm` on another day (the list can span days; a row's own time stays `HH:mm`). */
function sinceText(ms: number): string {
  const d = new Date(ms)
  return sameDay(d, new Date()) ? clock(ms) : `${d.getMonth() + 1}/${d.getDate()} ${clock(ms)}`
}

const timeOf = (a: Approval) => a.decided_at ?? a.created_at
const codeOf = (e: unknown) => (e instanceof ApprovalApiError ? e.code : 'error')

function firstPage(v: UnattendedView): HostPages {
  return { rows: v.approved, ...(v.truncated ? { nextBefore: v.next_before } : {}), since: v.since }
}

export interface UnattendedPanelProps {
  /** The hosts to read, fixed at open. */
  hostIds: readonly string[]
  /** Shown hosts that cannot be reached (disconnected, support or state unknown): named, not asked. Fixed at open. */
  unreachableIds?: readonly string[]
  anchorRef: RefObject<HTMLElement | null>
  onClose: () => void
}

export function UnattendedPanel({ hostIds, unreachableIds = [], anchorRef, onClose }: UnattendedPanelProps) {
  const t = useI18nStore((s) => s.t)
  // Set once per mount: a re-render with another reachable set must not refetch or drop what is shown.
  const [hosts] = useState(hostIds)
  const [unreachable] = useState(unreachableIds)
  const [pages, setPages] = useState<Record<string, HostPages> | null>(null)
  const [paging, setPaging] = useState(false)
  const alive = useRef(true)

  useEffect(() => {
    alive.current = true
    const ids = hosts
    void Promise.all(ids.map(async (hostId): Promise<[string, HostPages]> => {
      try { return [hostId, firstPage(await getUnattended(hostId))] }
      catch (e) { return [hostId, { rows: [], since: 0, failed: codeOf(e) }] }
    })).then((entries) => { if (alive.current) setPages(Object.fromEntries(entries)) })
    return () => { alive.current = false }
  }, [hosts])

  const more = useCallback(async () => {
    if (paging || pages === null) return
    // Only the hosts that said "more" (and whose cursor we hold): a host at its last page is not asked again.
    const todo = hosts.filter((h) => pages[h]?.nextBefore !== undefined)
    if (todo.length === 0) return
    setPaging(true)
    const answers = await Promise.all(todo.map(async (hostId): Promise<[string, Partial<HostPages>]> => {
      try {
        const v = await getUnattended(hostId, { before: pages[hostId].nextBefore })
        return [hostId, { rows: [...pages[hostId].rows, ...v.approved], nextBefore: v.truncated ? v.next_before : undefined, failed: undefined }]
      } catch (e) { return [hostId, { failed: codeOf(e) }] } // the rows and the cursor stay: 「顯示更多」 retries
    }))
    if (!alive.current) return
    setPages((cur) => {
      const next = { ...cur }
      for (const [hostId, patch] of answers) next[hostId] = { ...next[hostId], ...patch }
      return next
    })
    setPaging(false)
  }, [hosts, pages, paging])

  const label = (hostId: string) => hostLabel(hostId, hostLookOf(hostId))
  const loaded = pages !== null
  const merged = loaded
    ? hosts.flatMap((hostId) => pages[hostId].rows.map((a) => ({ hostId, a })))
        .sort((x, y) => timeOf(y.a) - timeOf(x.a))
    : []
  const failed = loaded ? hosts.filter((h) => pages[h].failed !== undefined) : []
  const since = loaded ? Math.min(...hosts.map((h) => pages[h].since).filter((s) => s > 0)) : Infinity
  const hasMore = loaded && hosts.some((h) => pages[h].nextBefore !== undefined)

  return (
    <FloatingPanel title={t('unattended.panel.title')} anchorRef={anchorRef} onClose={onClose} width={PANEL_WIDTH} placement="below" testId="unattended-panel">
      <div aria-busy={!loaded} className="flex flex-col gap-2 text-xs">
        {loaded && Number.isFinite(since) && (
          <div data-testid="unattended-since" className="text-text-muted">{t('unattended.panel.since', { time: sinceText(since) })}</div>
        )}
        {loaded && merged.length === 0 && failed.length === 0 && unreachable.length === 0 && (
          <div data-testid="unattended-empty" className="text-text-muted">{t('unattended.panel.empty')}</div>
        )}
        {unreachable.map((hostId) => (
          <div key={hostId} data-testid="unattended-host-unreachable" className="text-status-warning">
            {t('unattended.panel.host_unreachable', { host: label(hostId) })}
          </div>
        ))}
        {merged.length > 0 && (
          <ul className="flex flex-col gap-1">
            {merged.map(({ hostId, a }) => (
              <li key={`${hostId}:${a.id}`} data-testid="unattended-row" className="text-text-primary">
                {t('unattended.panel.row', { host: label(hostId), session: approvalSessionLabel(a.origin), kind: approvalKindLabel(t, a.kind), time: clock(timeOf(a)) })}
              </li>
            ))}
          </ul>
        )}
        {failed.map((hostId) => (
          <div key={hostId} data-testid="unattended-host-failed" className="text-status-warning">
            {t('unattended.panel.host_failed', { host: label(hostId), code: pages![hostId].failed! })}
          </div>
        ))}
        {hasMore && (
          <button
            type="button"
            data-testid="unattended-more"
            disabled={paging}
            onClick={() => { void more() }}
            className="self-start px-2 py-1 rounded text-text-secondary hover:text-text-primary hover:bg-surface-hover cursor-pointer disabled:opacity-40 disabled:pointer-events-none"
          >
            {t('unattended.panel.more')}
          </button>
        )}
      </div>
    </FloatingPanel>
  )
}
