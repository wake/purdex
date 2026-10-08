// spa/src/components/team/TeamTabGroup.tsx — a lead/member group on the TabBar.
//
// The group starts with the team-name label (Chrome's tab-group label); clicking it collapses or
// expands the group. The wrapper draws the group per TeamGroupStyle: an outline ("frame"), one shared
// tinted plate ("plate"), or nothing ("tint" / "topbar" / "label" decorate the tabs themselves).
import type { ReactNode } from 'react'
import type { TeamTabMark } from './team-display'

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
  const style =
    mark.style === 'frame'
      ? { border: `1.5px solid ${mark.color}`, borderRadius: 9, padding: '0 2px 0 0', height: 34 }
      : mark.style === 'plate'
        ? { background: `color-mix(in srgb, ${mark.color} 16%, transparent)`, borderRadius: 9, padding: '0 2px 0 0', height: 34 }
        : { height: '100%' }
  return (
    <div data-testid="team-tab-group" data-team-key={mark.teamKey} data-group-style={mark.style} className="flex items-center flex-shrink-0" style={style}>
      {children}
    </div>
  )
}
