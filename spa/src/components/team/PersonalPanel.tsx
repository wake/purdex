// spa/src/components/team/PersonalPanel.tsx — the personal panel inside the panel area (task groups spec §7, TG-5b; design: personal-panel-design.html R6–R15).
//
// For a tab whose agent session is no team seat. Two pane forms, in the one value all personal panels share (`sharedPanelMode`): the one-line form — the group's capsule, the session's avatar cell, then its name and title —
// and the full form (TG-5c). The title-bar form is the strip's (`PersonalTitleStrip`). The header is the team panel's: one 34 px row, a click on it toggles line <-> full, a click on the capsule waits for a double-click,
// the ↑ moves the area to the title bar.
import { ToTitleBarButton } from './TeamPanel'
import { GroupCapsule, PersonalCell, PersonalNameLine } from './PersonalParts'
import { useHeaderGestures } from './useHeaderGestures'
import { CAPSULE_MAX_W, CELL_GAP, HEADER_GAP, HEADER_H, HEADER_PX } from './panel-layout'
import { usePersonalGroup } from './usePersonalPanel'
import { useI18nStore } from '../../stores/useI18nStore'
import type { PaneMode, PanelMode } from '../../stores/useTeamUiStore'
import type { PersonalTarget } from '../../lib/task-groups/personal'

interface Props {
  target: PersonalTarget
  mode: PaneMode
  onSetMode: (mode: PanelMode) => void
}

const HEADER_CLASS = 'flex items-center cursor-pointer select-none'
const headerStyle = { height: HEADER_H, paddingInline: HEADER_PX, columnGap: HEADER_GAP } as const

export function PersonalPanel({ target, mode, onSetMode }: Props) {
  const t = useI18nStore((s) => s.t)
  const { group } = usePersonalGroup(target)
  // The rename on a double-click arrives with the settings block (TG-5c): until then a double-click only cancels the wait.
  const { rootRef, hdr } = useHeaderGestures({ teamKey: target.tabId, mode, onSetMode, canEdit: false })
  return (
    <div ref={rootRef} data-testid="personal-panel" data-mode={mode} className="text-xs text-text-primary">
      {mode === 'line' ? (
        <div data-testid="team-panel-header" className={HEADER_CLASS} style={headerStyle} {...hdr}>
          <GroupCapsule group={group} className="flex-shrink-0" style={{ maxWidth: CAPSULE_MAX_W }} />
          <span className="flex items-center flex-shrink-0" style={{ columnGap: CELL_GAP }}>
            <PersonalCell target={target} />
          </span>
          <PersonalNameLine target={target} className="flex-1" />
          <span className="flex items-center gap-0.5 flex-shrink-0">
            <ToTitleBarButton onClick={() => onSetMode('titlebar')} />
          </span>
        </div>
      ) : (
        <div data-testid="team-panel-header" className={HEADER_CLASS} style={headerStyle} {...hdr}>
          <GroupCapsule group={group} />
          {group.kind === 'manual' && (
            <span data-testid="personal-count" className="text-text-muted whitespace-nowrap">· {t('personal.sessions', { count: group.count })}</span>
          )}
          <span className="ml-auto flex items-center gap-0.5">
            <ToTitleBarButton onClick={() => onSetMode('titlebar')} />
          </span>
        </div>
      )}
    </div>
  )
}
