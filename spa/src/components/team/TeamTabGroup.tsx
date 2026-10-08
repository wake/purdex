// spa/src/components/team/TeamTabGroup.tsx — a lead/member group on the TabBar.
//
// The group starts with the team-name label (Chrome's tab-group label); clicking it collapses or
// expands the group. The wrapper draws the group per TeamGroupStyle. The low-key styles (label / dot /
// endcap / gap / sepcolor / rule / combo) keep tabs untouched and add only small cues: a team-color dot on each
// tab icon (SortableTab), a closing tick after the last tab, extra space around the group with tighter tabs
// inside, team-colored separators inside the group (TabBar), one faint rule under the whole group.
// The older styles: an outline ("frame"), one shared tinted plate ("plate"); "tint" / "topbar" decorate the tabs.
import type { CSSProperties, ReactNode } from 'react'
import { User, UsersThree, BookmarkSimple, Hexagon, Diamond, Circle } from '@phosphor-icons/react'
import { groupBadge, groupCorner, groupEdge, groupHasCue, useTeamDisplay, type TeamBadgeIcon, type TeamCornerSize, type TeamTabMark } from './team-display'

/** Dark text on the pastel team colors, in both themes. */
const LABEL_FG = '#14141f'

export function TeamGroupLabel({ mark, onToggle }: { mark: TeamTabMark; onToggle?: (teamKey: string) => void }) {
  return (
    <button
      type="button"
      data-testid="team-group-label"
      data-team-key={mark.teamKey}
      data-collapsed={String(mark.collapsed)}
      title={`${mark.label}${mark.unnamed ? '（team 沒有名字，暫用 lead 的標題）' : ''} · 點一下${mark.collapsed ? '展開' : '收合'}`}
      onClick={() => onToggle?.(mark.teamKey)}
      className={`flex items-center gap-1 h-[20px] max-w-[120px] px-2 mx-1 rounded-md text-[11px] font-semibold whitespace-nowrap cursor-pointer flex-shrink-0 ${mark.unnamed ? 'italic' : ''}`}
      style={{ background: mark.color, color: LABEL_FG, marginTop: 2, opacity: mark.unnamed ? 0.85 : 1 }}
    >
      <span className="truncate">{mark.label}</span>
      {mark.collapsed && mark.hiddenCount > 0 && <span data-testid="team-group-hidden" className="opacity-70">+{mark.hiddenCount}</span>}
    </button>
  )
}

export function TeamTabGroupFrame({ mark, children }: { mark: TeamTabMark; children: ReactNode }) {
  const gap = groupHasCue(mark.style, 'gap')
  const style: CSSProperties =
    mark.style === 'frame'
      ? { border: `1.5px solid ${mark.color}`, borderRadius: 9, padding: '0 2px 0 0', height: 34 }
      : mark.style === 'plate'
        ? { background: `color-mix(in srgb, ${mark.color} 16%, transparent)`, borderRadius: 9, padding: '0 2px 0 0', height: 34 }
        : { height: '100%' }
  if (gap) { style.marginLeft = 10; style.marginRight = 10; style.paddingRight = 2 }
  return (
    <div data-testid="team-tab-group" data-team-key={mark.teamKey} data-group-style={mark.style} className="relative flex items-center flex-shrink-0" style={style}>
      {children}
      {groupHasCue(mark.style, 'endcap') && !mark.collapsed && (
        <span data-testid="team-group-endcap" className="w-[3px] h-3.5 rounded-full flex-shrink-0 ml-0.5" style={{ background: mark.color }} />
      )}
      {groupHasCue(mark.style, 'rule') && (
        <span data-testid="team-group-rule" className="absolute left-1 right-1 bottom-[1px] h-[2px] rounded-full pointer-events-none" style={{ background: `color-mix(in srgb, ${mark.color} 70%, transparent)` }} />
      )}
    </div>
  )
}

/** Triangle leg length per size; the icon variants are bigger so the inverted member icon fits inside. */
const CORNER_PX: Record<'plain' | 'icon', Record<TeamCornerSize, number>> = {
  plain: { sm: 6, md: 9, lg: 13 },
  icon: { sm: 14, md: 18, lg: 22 },
}

/** Corner-badge diameter per size; the bare icon is a little smaller than the disc. */
const BADGE_PX: Record<'icon' | 'disc', Record<TeamCornerSize, number>> = {
  icon: { sm: 12, md: 16, lg: 20 },
  disc: { sm: 14, md: 18, lg: 22 },
}

/** A folded-corner mark on a tab (the "corner-*" group styles): a team-colored right triangle, optionally holding a member icon. */
export function TeamTabCorner({ mark }: { mark: TeamTabMark }) {
  const corner = groupCorner(mark.style)
  const badge = groupBadge(mark.style)
  const display = useTeamDisplay()
  const size = display?.cornerSize ?? 'md'
  if (badge) {
    const px = BADGE_PX[badge][size]
    // Center sits on the corner, nudged inward by 2px so the TabBar scroller (which clips vertically) keeps the whole badge visible.
    const off = -(px / 2) + 2
    return (
      <span
        data-testid="team-tab-badge"
        data-badge={badge}
        aria-hidden="true"
        className="absolute pointer-events-none z-20 flex items-center justify-center"
        style={{ top: off, right: off, width: px, height: px, borderRadius: '50%', background: badge === 'disc' ? mark.color : undefined }}
      >
        <BadgeGlyph kind={display?.badgeIcon ?? 'bookmark'} disc={badge === 'disc'} px={px} mark={mark} />
      </span>
    )
  }
  const edge = groupEdge(mark.style)
  if (edge) {
    const w = display?.edgeWidth ?? 2
    // Arc: a right-only border on a box with the tab's own corner radius (6px, drawn over the 1px transparent border),
    // so the line follows the active tab's rounded corners and tapers off at the top/bottom like a ")" bracket.
    // Short: a plain bar on the middle of the right edge, no turns. Sits at the very edge; the close X is 12px in.
    return (
      <span
        data-testid="team-tab-edge"
        data-edge={edge}
        data-edge-width={String(w)}
        aria-hidden="true"
        className="absolute pointer-events-none z-10"
        style={edge === 'arc'
          ? { top: -1, bottom: -1, right: -1, width: 8, borderRight: `${w}px solid ${mark.color}`, borderTopRightRadius: 6, borderBottomRightRadius: 6 }
          : { top: 7, bottom: 7, right: -1, width: w, background: mark.color, borderRadius: w }}
      />
    )
  }
  if (!corner) return null
  const px = CORNER_PX[corner.icon ? 'icon' : 'plain'][size]
  const top = corner.pos === 'tr'
  return (
    <span
      data-testid="team-tab-corner"
      data-corner={corner.pos}
      data-corner-icon={String(corner.icon)}
      aria-hidden="true"
      className="absolute right-0 pointer-events-none z-10"
      style={{
        [top ? 'top' : 'bottom']: 0,
        width: px,
        height: px,
        background: mark.color,
        opacity: corner.icon ? 0.95 : 0.85,
        clipPath: top ? 'polygon(0 0, 100% 0, 100% 100%)' : 'polygon(100% 0, 100% 100%, 0 100%)',
        [top ? 'borderTopRightRadius' : 'borderBottomRightRadius']: 6,
      }}
    >
      {corner.icon && (
        <User
          weight="fill"
          size={Math.round(px * 0.42)}
          color={LABEL_FG}
          className="absolute"
          style={{ right: 1, [top ? 'top' : 'bottom']: 1 }}
        />
      )}
    </span>
  )
}

/** The glyph inside a corner badge: bare (team-colored glyph) or on a team-colored disc (knocked-out glyph). */
function BadgeGlyph({ kind, disc, px, mark }: { kind: TeamBadgeIcon; disc: boolean; px: number; mark: TeamTabMark }) {
  const fg = disc ? 'var(--surface-secondary)' : mark.color
  const size = disc ? Math.round(px * 0.62) : px
  const weight = disc ? 'bold' : 'fill'
  switch (kind) {
    case 'users': return <UsersThree weight={weight} size={size} color={fg} />
    case 'bookmark': return <BookmarkSimple weight={weight} size={size} color={fg} />
    case 'hexagon': return <Hexagon weight={weight} size={size} color={fg} />
    case 'diamond': return <Diamond weight={weight} size={size} color={fg} />
    case 'dot': return disc ? <Circle weight="fill" size={Math.round(px * 0.34)} color={fg} /> : <Circle weight="fill" size={Math.round(px * 0.7)} color={fg} />
    case 'letter': {
      const ch = Array.from(mark.label.trim())[0] ?? '?'
      return <span className="font-bold leading-none" style={{ fontSize: Math.round(px * (disc ? 0.6 : 0.8)), color: fg }}>{ch.toUpperCase()}</span>
    }
    default: return <User weight={weight} size={size} color={fg} />
  }
}
