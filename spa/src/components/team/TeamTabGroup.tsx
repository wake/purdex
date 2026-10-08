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
import { useThemeStore } from '../../stores/useThemeStore'
import { groupBadge, groupCorner, groupEdge, groupHasCue, groupShadow, useTeamDisplay, type TeamBadgeIcon, type TeamCornerSize, type TeamShadowCompanion, type TeamShadowDepth, type TeamShadowStrength, type TeamTabMark } from './team-display'

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
  if (badge === 'icon' && (display?.badgeIcon ?? 'bookmark') === 'bookmark') {
    // Hanging bookmark: the glyph's top is cut off (1/3 or 1/2) and the ribbon hangs from the tab's very top edge.
    // Phosphor's bookmark occupies the middle 50% of its box, so the wrapper is that wide and the glyph is shifted left/up.
    const full = BADGE_PX.icon[size]
    const cut = display?.bookmarkCut === 'half' ? 0.5 : 1 / 3
    const w = full * 0.5
    const h = full * (1 - cut)
    // Offsets from the tab's right edge: the close X is a 24px-wide slot (glyph centered, 12px wide).
    const pos = display?.bookmarkPos ?? 'above-right'
    const right = pos === 'before-x' ? 26 : pos === 'above-left' ? 14 : 3
    return (
      <span
        data-testid="team-tab-badge"
        data-badge="icon"
        data-bookmark-cut={display?.bookmarkCut ?? 'third'}
        data-bookmark-pos={pos}
        aria-hidden="true"
        className="absolute pointer-events-none z-20 overflow-hidden"
        style={{ top: -1, right, width: w, height: h }}
      >
        <BookmarkSimple weight="fill" size={full} color={mark.color} style={{ position: 'absolute', left: -full * 0.25, top: -full * cut }} />
      </span>
    )
  }
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
  const shadow = groupShadow(mark.style)
  if (shadow) {
    if ((display?.shadowScope ?? 'all') === 'last' && !mark.last) return null
    return <TeamTabShadow mark={mark} dir={shadow} strength={display?.shadowStrength ?? 'medium'} depth={display?.shadowDepth ?? 70} companion={display?.shadowCompanion ?? 'none'} />
  }
  const edge = groupEdge(mark.style)
  if (edge) {
    // Only the group's last tab draws it, as the closing bracket of the whole group.
    if (!mark.last) return null
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

/** Shadow width steps: a crisp solid line (off px) plus an optional soft companion (blur px, 35% alpha). */
const SHADOW_PX: Record<TeamShadowStrength, { off: number; soft: number }> = {
  thin: { off: 1, soft: 0 },
  medium: { off: 1, soft: 2 },
  thick: { off: 2, soft: 2 },
}

/**
 * A team-colored "lifted button" shadow on a tab (the "shadow-*" group styles): a crisp 1-2px solid edge in the team color,
 * optionally with a tiny soft companion. No inset, no glow. Sizes stay inside the TabBar clip (about 2px above, 6px below).
 * In the light theme the pastel colors are darkened so the line reads. An overlay span, so the tab's own background stays.
 */
function TeamTabShadow({ mark, dir, strength, depth, companion }: { mark: TeamTabMark; dir: 'top' | 'bottom' | 'diag' | 'top-right'; strength: TeamShadowStrength; depth: TeamShadowDepth; companion: TeamShadowCompanion }) {
  const light = useThemeStore((s) => s.activeThemeId) === 'light'
  const { off, soft } = SHADOW_PX[strength]
  const base = light ? `color-mix(in oklab, ${mark.color}, black 25%)` : mark.color
  const line = depth === 100 ? base : `color-mix(in oklab, ${base} ${depth}%, transparent)`
  const halo = `color-mix(in srgb, ${base} ${Math.round(0.35 * depth)}%, transparent)`
  const [ox, oy] = dir === 'top' ? [0, -off] : dir === 'bottom' ? [0, off] : dir === 'top-right' ? [off, -off] : [off, off]
  const parts = [`${ox}px ${oy}px 0 ${line}`]
  if (soft) parts.push(`${ox}px ${oy}px ${soft}px ${halo}`)
  if (companion !== 'none') {
    // Fainter bottom-left twin: the main line's colour mixed toward transparent by the chosen fraction.
    const pct = companion === 'half' ? 50 : 33
    parts.push(`${-off}px ${off}px 0 color-mix(in oklab, ${line} ${pct}%, transparent)`)
    if (soft) parts.push(`${-off}px ${off}px ${soft}px color-mix(in srgb, ${halo} ${pct}%, transparent)`)
  }
  return (
    <span
      data-testid="team-tab-shadow"
      data-shadow={dir}
      data-shadow-strength={strength}
      data-shadow-companion={companion}
      aria-hidden="true"
      className="absolute inset-0 pointer-events-none z-10"
      style={{ borderRadius: 6, boxShadow: parts.join(', ') }}
    />
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
