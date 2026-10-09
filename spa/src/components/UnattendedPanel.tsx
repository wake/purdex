// spa/src/components/UnattendedPanel.tsx — the "while you were away" list under the title bar's ▾ (unattended spec
// D-U23-6; plan PU-2c): what each reachable shown host's daemon approved since its switch last turned on, read page by
// page from its audit (`GET /api/team/unattended`), merged newest first: `<host>：<session> · <kind> · <time>`
// (`HH:mm`, with `M/D` when not today). Each host commits its own answer as it arrives and has 10 s to give it: one
// silent daemon is named (`timeout`), it never holds the others' rows back.
// 「顯示更多」 pages every host that still has more (`next_before`); a host that cannot be read is named, never skipped.
// A shown host that cannot be reached (`unreachableIds`) is not asked but named above the list: its daemon may still be
// approving. When no host can be reached there is no list to be empty, so only those lines show.
//
// Above that list (relay quota spec §3.5, plan RQ-A): 「接力額度」 (UnattendedQuotaSection: a stepper per session, for hosts
// whose daemon lists `team.relay_quota.v1`) and 「額度用完，等你核准」 (UnattendedHeldSection, only when the daemon sent held
// requests). The first page's `quotas` seed the quota store (relay-quota.ts) between its beginGet / endGet, so events
// that arrive while the GET is out are applied after it.
//
// Not tab-hosted: opened by a click of UnattendedButton's ▾ and gone when closed. The loaded pages are this
// component's own state — closing drops them and every open fetches afresh (a stale list would hide what the daemon
// approved in between). Which hosts to read is fixed at open (`hostIds`).
import { useCallback, useEffect, useRef, useState, type RefObject } from 'react'
import { FloatingPanel } from './FloatingPanel'
import { UnattendedQuotaSection, type QuotaHostData } from './UnattendedQuotaSection'
import { UnattendedHeldSection, type HeldRow } from './UnattendedHeldSection'
import { useI18nStore } from '../stores/useI18nStore'
import { useUnattendedStore } from '../stores/useUnattendedStore'
import { hostLabel, hostLookOf } from '../lib/host-look'
import { ApprovalApiError } from '../lib/team/approval-api'
import { approvalKindLabel, approvalSessionLabel } from '../lib/team/approval-format'
import { getUnattended } from '../lib/team/unattended-api'
import { useRelayQuotaStore } from '../lib/team/relay-quota'
import { registerRefetch } from '../lib/team/relay-quota-writer'
import { sinceText } from '../lib/team/time-text'
import type { Approval, SessionQuota, UnattendedView } from '../lib/team/types'

const PANEL_WIDTH = 420

/** One host's share of the list. */
interface HostPages {
  rows: Approval[]
  /** The cursor of the next page; present while the daemon said `truncated`. */
  nextBefore?: number
  /** The switch's last turn-on (ms), 0 = never. */
  since: number
  /** The code of the last failed read (kept with the rows loaded before it); absent = the last read worked. */
  failed?: string
  /** Relay quota (the first page's): the rows, or `quotasFailed` when the daemon's array was null / malformed; `held` rows. */
  quotas?: readonly SessionQuota[]
  quotasFailed?: boolean
  held?: readonly Approval[]
}

const timeOf = (a: Approval) => a.decided_at ?? a.created_at
const codeOf = (e: unknown) => (e instanceof ApprovalApiError ? e.code : 'error')

/** How long one host may take to answer a page before it is named as failed (`timeout`) instead of holding the panel. */
export const HOST_READ_TIMEOUT_MS = 10_000

/**
 * What one effect setup owns: every request and timer it started, and whether it was cancelled. A cancelled setup is
 * cancelled for good (the flag is never reset): StrictMode runs setup, cleanup, setup on one mount, and a late answer
 * of the first setup must not read a shared "alive" the second setup has flipped back to true.
 */
class Lifecycle {
  cancelled = false
  private readonly controllers = new Set<AbortController>()
  private readonly timers = new Set<ReturnType<typeof setTimeout>>()

  /** Start a bounded request owned by this lifecycle. */
  track(): { ctl: AbortController; arm: (ms: number, onTimeout: () => void) => () => void } {
    const ctl = new AbortController()
    this.controllers.add(ctl)
    const arm = (ms: number, onTimeout: () => void) => {
      const timer = setTimeout(() => { this.timers.delete(timer); onTimeout() }, ms)
      this.timers.add(timer)
      return () => { clearTimeout(timer); this.timers.delete(timer); this.controllers.delete(ctl) }
    }
    return { ctl, arm }
  }

  cancel(): void {
    this.cancelled = true
    for (const t of this.timers) clearTimeout(t)
    this.timers.clear()
    for (const c of this.controllers) c.abort()
    this.controllers.clear()
  }
}

/**
 * One host's page, bounded: a host that has not answered in `HOST_READ_TIMEOUT_MS` fails as `timeout` (its request is
 * aborted) so a single silent daemon never keeps the others' rows, or the panel's busy state, hostage. The request and
 * its timer belong to `life`: cancelling it aborts the request (rejecting as `aborted`) and clears the timer.
 */
function readPage(life: Lifecycle, hostId: string, before: number | undefined): Promise<UnattendedView> {
  const { ctl, arm } = life.track()
  return new Promise<UnattendedView>((resolve, reject) => {
    ctl.signal.addEventListener('abort', () => reject(new ApprovalApiError(0, 'aborted')), { once: true })
    const done = arm(HOST_READ_TIMEOUT_MS, () => { reject(new ApprovalApiError(0, 'timeout')); ctl.abort() })
    getUnattended(hostId, before === undefined ? undefined : { before }, ctl.signal).then(
      // `list_failed` is the PUT's flag (the daemon's GET never sets it): if it ever comes, `approved` says nothing.
      (v) => { done(); if (v.list_failed === true) reject(new ApprovalApiError(200, 'list_failed')); else resolve(v) },
      (e: unknown) => { done(); reject(e) },
    )
  })
}

function quotaPart(v: UnattendedView): Pick<HostPages, 'quotas' | 'quotasFailed' | 'held'> {
  return {
    ...(v.quotas !== undefined ? { quotas: v.quotas } : {}),
    ...(v.quotasFailed ? { quotasFailed: true } : {}),
    ...(v.held !== undefined ? { held: v.held } : {}),
  }
}

function firstPage(v: UnattendedView): HostPages {
  return { rows: v.approved, ...(v.truncated ? { nextBefore: v.next_before } : {}), since: v.since, ...quotaPart(v) }
}

const quotaHostOf = (hostId: string): boolean => useUnattendedStore.getState().byHost[hostId]?.quotaSupport === 'yes'

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
  // The current effect setup's lifecycle (null between a cleanup and the next setup, and after unmount). Callbacks hold
  // the lifecycle they started under, never this ref, so an old setup's answer cannot commit.
  const lifeRef = useRef<Lifecycle | null>(null)
  const pagingNow = useRef(false)

  useEffect(() => {
    const life = new Lifecycle()
    lifeRef.current = life
    const disposers: Array<() => void> = []
    for (const hostId of hosts) {
      const quotas = quotaHostOf(hostId)
      if (quotas) useRelayQuotaStore.getState().beginGet(hostId)
      void readPage(life, hostId, undefined).then(
        (v) => { if (quotas) useRelayQuotaStore.getState().endGet(hostId, v.quotas ?? null); return firstPage(v) },
        (e: unknown): HostPages => { if (quotas) useRelayQuotaStore.getState().endGet(hostId, null); return { rows: [], since: 0, failed: codeOf(e) } },
      ).then((p) => { if (!life.cancelled) setPages((cur) => ({ ...cur, [hostId]: p })) })
      if (quotas) {
        // After a write that went to a provisional root (`pending_lineage`) the writer asks for this host's view again.
        disposers.push(registerRefetch(hostId, () => {
          useRelayQuotaStore.getState().beginGet(hostId)
          void readPage(life, hostId, undefined).then(
            (v) => { useRelayQuotaStore.getState().endGet(hostId, v.quotas ?? null); if (!life.cancelled) setPages((cur) => ({ ...cur, [hostId]: { ...cur[hostId], quotas: v.quotas, quotasFailed: v.quotasFailed, held: v.held } })) },
            () => { useRelayQuotaStore.getState().endGet(hostId, null) },
          )
        }))
      }
    }
    return () => { life.cancel(); for (const d of disposers) d(); if (lifeRef.current === life) lifeRef.current = null }
  }, [hosts])

  const more = useCallback(async () => {
    const life = lifeRef.current
    if (pagingNow.current || life === null || life.cancelled) return
    // Only the hosts that said "more" (and whose cursor we hold): a host at its last page is not asked again.
    const todo = hosts.filter((h) => pages[h]?.nextBefore !== undefined)
    if (todo.length === 0) return
    pagingNow.current = true
    setPaging(true)
    // Each host's page is committed as it arrives, on top of the rows then held (not the ones held at the click).
    await Promise.all(todo.map(async (hostId) => {
      let patch: (cur: HostPages) => HostPages
      try {
        const v = await readPage(life, hostId, pages[hostId].nextBefore)
        patch = (cur) => ({ ...cur, rows: [...cur.rows, ...v.approved], nextBefore: v.truncated ? v.next_before : undefined, failed: undefined })
      } catch (e) {
        const failed = codeOf(e)
        patch = (cur) => ({ ...cur, failed }) // the rows and the cursor stay: 「顯示更多」 retries
      }
      if (!life.cancelled) setPages((cur) => ({ ...cur, [hostId]: patch(cur[hostId]) }))
    }))
    pagingNow.current = false
    if (!life.cancelled) setPaging(false)
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
  const supportOf = useUnattendedStore((s) => s.byHost)
  const quotaHosts: QuotaHostData[] = answered.filter((h) => supportOf[h]?.quotaSupport === 'yes' && pages[h].failed === undefined)
    .flatMap((hostId): QuotaHostData[] => {
      const p = pages[hostId]
      return p.quotasFailed ? [{ hostId, failed: true }] : p.quotas !== undefined ? [{ hostId, rows: p.quotas }] : []
    })
  const held: HeldRow[] = answered.flatMap((hostId) => (pages[hostId].held ?? []).map((a) => ({ hostId, a })))

  return (
    <FloatingPanel title={t('unattended.panel.title')} anchorRef={anchorRef} onClose={onClose} width={PANEL_WIDTH} placement="below" testId="unattended-panel">
      {/* Three blocks, a line between those that are there: 接力額度, 額度用完，等你核准, and the automatically approved list. */}
      <div aria-busy={busy} className="flex flex-col text-xs divide-y divide-border-subtle [&>*]:py-2 [&>*:first-child]:pt-0 [&>*:last-child]:pb-0">
        <UnattendedQuotaSection hosts={quotaHosts} headings={quotaHosts.length > 1} />
        <UnattendedHeldSection rows={held} />
        <section data-testid="unattended-approved-section" className="flex flex-col gap-2">
          <div data-testid="unattended-approved-title" className="font-medium text-text-primary">{t('unattended.approved.title')}</div>
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
                  {t('unattended.panel.row', { host: label(hostId), session: approvalSessionLabel(a.origin), kind: approvalKindLabel(t, a.kind), time: sinceText(timeOf(a)) })}
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
        </section>
      </div>
    </FloatingPanel>
  )
}
