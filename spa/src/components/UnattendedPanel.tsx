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

/** How long one host may take to answer a page before it is named as failed (`timeout`) instead of holding the panel. */
export const HOST_READ_TIMEOUT_MS = 10_000

/**
 * One host's page, bounded: a host that has not answered in `HOST_READ_TIMEOUT_MS` fails as `timeout` (its request is
 * aborted) so a single silent daemon never keeps the others' rows, or the panel's busy state, hostage.
 */
function readPage(hostId: string, before: number | undefined): Promise<UnattendedView> {
  const ctl = new AbortController()
  return new Promise<UnattendedView>((resolve, reject) => {
    const timer = setTimeout(() => { ctl.abort(); reject(new ApprovalApiError(0, 'timeout')) }, HOST_READ_TIMEOUT_MS)
    getUnattended(hostId, before === undefined ? undefined : { before }, ctl.signal).then(
      (v) => { clearTimeout(timer); resolve(v) },
      (e: unknown) => { clearTimeout(timer); reject(e) },
    )
  })
}

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
  // Each host commits its own answer the moment it arrives; a host absent here has not answered yet.
  const [pages, setPages] = useState<Record<string, HostPages>>({})
  const [paging, setPaging] = useState(false)
  const alive = useRef(true)
  const pagingNow = useRef(false)

  useEffect(() => {
    alive.current = true
    for (const hostId of hosts) {
      void readPage(hostId, undefined).then(
        (v) => firstPage(v),
        (e: unknown): HostPages => ({ rows: [], since: 0, failed: codeOf(e) }),
      ).then((p) => { if (alive.current) setPages((cur) => ({ ...cur, [hostId]: p })) })
    }
    return () => { alive.current = false }
  }, [hosts])

  const more = useCallback(async () => {
    if (pagingNow.current) return
    // Only the hosts that said "more" (and whose cursor we hold): a host at its last page is not asked again.
    const todo = hosts.filter((h) => pages[h]?.nextBefore !== undefined)
    if (todo.length === 0) return
    pagingNow.current = true
    setPaging(true)
    // Each host's page is committed as it arrives, on top of the rows then held (not the ones held at the click).
    await Promise.all(todo.map(async (hostId) => {
      let patch: (cur: HostPages) => HostPages
      try {
        const v = await readPage(hostId, pages[hostId].nextBefore)
        patch = (cur) => ({ ...cur, rows: [...cur.rows, ...v.approved], nextBefore: v.truncated ? v.next_before : undefined, failed: undefined })
      } catch (e) {
        const failed = codeOf(e)
        patch = (cur) => ({ ...cur, failed }) // the rows and the cursor stay: 「顯示更多」 retries
      }
      if (alive.current) setPages((cur) => ({ ...cur, [hostId]: patch(cur[hostId]) }))
    }))
    pagingNow.current = false
    if (alive.current) setPaging(false)
  }, [hosts, pages])

  const label = (hostId: string) => hostLabel(hostId, hostLookOf(hostId))
  const answered = hosts.filter((h) => pages[h] !== undefined)
  // Busy only while nothing has come back (no hosts to ask = nothing to wait for); "empty" only once every host has.
  const busy = hosts.length > 0 && answered.length === 0
  const settled = answered.length === hosts.length
  const merged = answered.flatMap((hostId) => pages[hostId].rows.map((a) => ({ hostId, a })))
    .sort((x, y) => timeOf(y.a) - timeOf(x.a))
  const failed = answered.filter((h) => pages[h].failed !== undefined)
  const since = Math.min(...answered.map((h) => pages[h].since).filter((s) => s > 0))
  const hasMore = answered.some((h) => pages[h].nextBefore !== undefined)

  return (
    <FloatingPanel title={t('unattended.panel.title')} anchorRef={anchorRef} onClose={onClose} width={PANEL_WIDTH} placement="below" testId="unattended-panel">
      <div aria-busy={busy} className="flex flex-col gap-2 text-xs">
        {Number.isFinite(since) && (
          <div data-testid="unattended-since" className="text-text-muted">{t('unattended.panel.since', { time: sinceText(since) })}</div>
        )}
        {settled && merged.length === 0 && failed.length === 0 && unreachable.length === 0 && (
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
            {t('unattended.panel.host_failed', { host: label(hostId), code: pages[hostId].failed! })}
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
