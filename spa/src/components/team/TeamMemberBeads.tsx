// spa/src/components/team/TeamMemberBeads.tsx — the members under a lead row in the sidebar (plan TI-3, spec §4.3).
//
// One bead per member: the agent icon with its light (plus the host icon when the setting is on), no name — the name is the
// tooltip. Beads are never faded and carry no "has a tab" mark (P8): like the App's sidebar rows, the member being looked at is
// highlighted and every other bead looks the same. Click opens or switches (R3); drag reorders within the team (R4, R5).
// The tick runs down to the last wrapped row; a click on it or on the blank area around the beads folds the team (P9).
import { useCallback, useLayoutEffect, useRef, useState } from 'react'
import type { TeamSeatView } from './team-display'
import { HOOK_PAD, TeamHook } from './TeamHook'
import { TeamSeatHostBadge, TeamSeatIcon } from './TeamSeatIcon'
import { useMemberDrag } from './useMemberDrag'
import { useI18nStore } from '../../stores/useI18nStore'

interface Props {
  teamKey: string
  color: string
  members: TeamSeatView[]
  activeTabId: string | null
  withHost: boolean
  onOpen: (sessionId: string) => void
  onReorder: (sessionIds: string[]) => void
  onBlankClick: () => void
}

/** The member's title; a seat that is still joining / being released / killed adds the state, with the host alias only when remote. */
function tooltipOf(m: TeamSeatView, t: (key: string, params?: Record<string, string | number>) => string): string {
  if (m.role !== 'member' || !['joining', 'releasing', 'killing'].includes(m.state)) return m.title
  const word = t(`team.seat_state.${m.state}`)
  return `${m.title} · ${m.hostAlias !== '' ? t('team.seat_state_suffix', { alias: m.hostAlias, state: word }) : word}`
}

export function TeamMemberBeads({ teamKey, color, members, activeTabId, withHost, onOpen, onReorder, onBlankClick }: Props) {
  const t = useI18nStore((s) => s.t)
  const box = useRef<HTMLDivElement>(null)
  const [rows, setRows] = useState(1)
  // Count the wrapped bead rows (distinct offsetTop) so the tick can draw one mark per row. Layout-derived, not state to keep.
  useLayoutEffect(() => {
    const el = box.current
    if (!el) return
    const count = () => {
      const tops = new Set<number>()
      el.querySelectorAll<HTMLElement>('[data-testid="team-bead"]').forEach((b) => tops.add(b.offsetTop))
      setRows(Math.max(1, tops.size))
    }
    count()
    if (typeof ResizeObserver === 'undefined') return
    const ro = new ResizeObserver(count)
    ro.observe(el)
    return () => ro.disconnect()
  }, [members.length, withHost])
  const order = members.map((m) => m.sessionId)
  const reorder = useCallback((ids: string[]) => onReorder(ids), [onReorder])
  const { propsFor, over, draggingId } = useMemberDrag(teamKey, order, reorder, 'x')
  if (members.length === 0) return null
  return (
    <div
      ref={box}
      data-testid="team-beads"
      title={t('team.sidebar.fold_hint')}
      onClick={(e) => { if (!(e.target as HTMLElement).closest('[data-testid="team-bead"]')) onBlankClick() }}
      className="relative flex flex-wrap items-center content-start gap-0.5 ml-[18px] mr-2 mb-0.5 cursor-pointer"
      style={{ paddingLeft: HOOK_PAD }}
    >
      <TeamHook rows={rows} />
      {members.map((m) => {
        const isActive = m.tabId !== null && m.tabId === activeTabId
        const ins = over?.id === m.sessionId ? (over.after ? 'after' : 'before') : null
        return (
          <button
            key={m.sessionId}
            type="button"
            data-testid="team-bead"
            data-session-id={m.sessionId}
            data-active={String(isActive)}
            title={tooltipOf(m, t)}
            onClick={() => onOpen(m.sessionId)}
            {...propsFor(m.sessionId)}
            className={`group relative flex items-center gap-1 h-6 px-1.5 rounded-md cursor-pointer transition-colors ${
              isActive ? 'bg-surface-active text-white' : 'text-text-muted hover:bg-surface-hover hover:text-text-primary'
            } ${draggingId === m.sessionId ? 'opacity-30' : ''}`}
          >
            {ins && (
              <span
                className="absolute top-1 bottom-1 w-0.5 rounded"
                style={{ background: color, [ins === 'before' ? 'left' : 'right']: -2 }}
              />
            )}
            <TeamSeatIcon hostId={m.hostId} sessionCode={m.sessionCode} isActive={isActive} />
            {withHost && <TeamSeatHostBadge hostId={m.hostId} sessionCode={m.sessionCode} />}
          </button>
        )
      })}
    </div>
  )
}
