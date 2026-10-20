// spa/src/components/team/PersonalPanel.tsx — the personal panel inside the panel area (task groups spec §7; design: personal-panel-design.html R6–R25).
//
// For a tab whose agent session is no team seat. Two pane forms, in the one value all personal panels share (`sharedPanelMode`): the one-line form — the group's capsule, the session's avatar cell, then its name and title — and the
// full form: the header, one card, the settings block. The title-bar form is the strip's (`PersonalTitleStrip`). The header is the team panel's: one 34 px row, a click on it toggles line <-> full, a click on the capsule waits for a
// double-click (which opens the full form with the name field), the ↑ moves the area to the title bar.
// The card's workbook button: on a journal.v1 host it opens / closes the session's journal card (the title bar's 「工作簿」 button is the same switch); on an older host it opens the session's own workbook inside the panel, with a back
// arrow to the card. That choice is this component's own state: a tab switch drops it, the form (the store's) stays.
import { useState } from 'react'
import { ArrowLeft } from '@phosphor-icons/react'
import { ToTitleBarButton } from './TeamPanel'
import { GroupCapsule, PersonalCell, PersonalNameLine } from './PersonalParts'
import { PersonalCard } from './PersonalCard'
import { PersonalSettings } from './PersonalSettings'
import { OwnWorkbookPanel } from './OwnWorkbookPanel'
import { useHeaderGestures } from './useHeaderGestures'
import { CAPSULE_MAX_W, CELL_GAP, HEADER_GAP, HEADER_H, HEADER_PX } from './panel-layout'
import { keepFocus } from './panel-focus'
import { useLoadedSeatWorkbook } from './own-workbook'
import { usePersonalGroup } from './usePersonalPanel'
import { usePersonalActions } from './usePersonalActions'
import { useJournalTarget } from '../journal/useJournalTarget'
import { patchUnitMemo, useViewMemo } from '../../lib/journal/use-view-memo'
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
  const { group, rootKnown } = usePersonalGroup(target)
  const actions = usePersonalActions(target)
  const [renameRequest, setRenameRequest] = useState(0)
  const [workbookOpen, setWorkbookOpen] = useState(false)
  // canEdit is off: the header's own double-click handling only cancels the single-click wait. A double-click on the capsule of a manual group is ours: the full form with the name field open.
  const { rootRef, hdr } = useHeaderGestures({ teamKey: target.tabId, mode, onSetMode, canEdit: false })
  const onHeaderDoubleClick = (e: React.MouseEvent<HTMLElement>) => {
    hdr.onDoubleClick(e)
    if (group.kind !== 'manual' || !(e.target as HTMLElement).closest('[data-testid="team-panel-name"]')) return
    setRenameRequest((n) => n + 1)
    if (mode !== 'full') onSetMode('full')
  }

  // the workbook button (R14): the journal cards are the title bar's journal target's (the tab's first live cc session); the button is there only when that is this card's session, so it never switches another session's cards
  const jt = useJournalTarget()
  const memo = useViewMemo(jt.memoHost, jt.memoKey)
  const ownWb = useLoadedSeatWorkbook({ hostId: target.hostId, sessionId: target.agentSessionId })
  const workbook = jt.journalHost
    ? jt.origin !== null && jt.origin.hostId === target.hostId && jt.origin.sessionId === target.agentSessionId && jt.memoKey !== null ? { open: memo.journalOpen, onToggle: () => patchUnitMemo(jt.memoHost, jt.memoKey, { journalOpen: !memo.journalOpen }) } : null
    : ownWb.has ? { open: workbookOpen, onToggle: () => setWorkbookOpen((v) => !v) } : null

  return (
    <div ref={rootRef} data-testid="personal-panel" data-mode={mode} className="text-xs text-text-primary">
      {mode === 'line' ? (
        <div data-testid="team-panel-header" className={HEADER_CLASS} style={headerStyle} {...hdr} onDoubleClick={onHeaderDoubleClick}>
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
        <>
          <div data-testid="team-panel-header" className={HEADER_CLASS} style={headerStyle} {...hdr} onDoubleClick={onHeaderDoubleClick}>
            <GroupCapsule group={group} />
            {group.kind === 'manual' && (
              <span data-testid="personal-count" className="text-text-muted whitespace-nowrap">· {t('personal.sessions', { count: group.count })}</span>
            )}
            <span className="ml-auto flex items-center gap-0.5">
              <ToTitleBarButton onClick={() => onSetMode('titlebar')} />
            </span>
          </div>
          {workbookOpen && !jt.journalHost && ownWb.has ? (
            <div data-testid="personal-workbook" className="border-t border-border-subtle flex flex-col flex-1 min-h-0">
              <button
                type="button"
                data-testid="personal-workbook-back"
                onMouseDown={keepFocus}
                onClick={() => setWorkbookOpen(false)}
                title={t('personal.back')}
                aria-label={t('personal.back')}
                className="self-start m-1 p-0.5 rounded text-text-secondary hover:text-text-primary hover:bg-surface-hover cursor-pointer"
              >
                <ArrowLeft size={12} />
              </button>
              <OwnWorkbookPanel hostId={target.hostId} sessionId={target.agentSessionId} mode="full" onSetMode={onSetMode} />
            </div>
          ) : (
            <>
              <div className="border-t border-border-subtle py-1.5">
                <PersonalCard target={target} workbook={workbook} />
              </div>
              <PersonalSettings group={group} rootKnown={rootKnown} actions={actions} renameRequest={renameRequest} />
            </>
          )}
        </>
      )}
    </div>
  )
}
