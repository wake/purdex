// spa/src/components/team/OwnWorkbookPanel.tsx — what the panel area draws for a tab's OWN conversation workbook in the pane
// (WA-2b-1b, round 3): `line` is one line (the first sentence of the latest status; a click brings the area to full);
// `full` / `max` is the workbook view with the header's two controls (the one move-to-title-bar ArrowLineUp, and enlarge).
// The title bar's one-line form is `OwnTitleStrip` (TeamTitleBar.tsx). Everything the person arranged is the store's.
import { Notebook } from '@phosphor-icons/react'
import { useI18nStore } from '../../stores/useI18nStore'
import { useOwnStatusLine } from './own-workbook'
import { TeamSeatWorkbookView } from './TeamSeatWorkbookView'
import { ExpandButton, ToTitleBarButton } from './TeamPanel'
import type { PaneMode, PanelMode } from '../../stores/useTeamUiStore'

interface Props {
  hostId: string
  sessionId: string
  mode: PaneMode
  onSetMode: (mode: PanelMode) => void
}

export function OwnWorkbookPanel({ hostId, sessionId, mode, onSetMode }: Props) {
  const t = useI18nStore((s) => s.t)
  const line = useOwnStatusLine(hostId, sessionId)
  const controls = (
    <>
      <ToTitleBarButton onClick={() => onSetMode('titlebar')} />
      <ExpandButton expanded={mode === 'max'} onToggle={() => onSetMode(mode === 'max' ? 'full' : 'max')} />
    </>
  )
  if (mode === 'line') {
    const open = t('team.workbook.open')
    return (
      <div data-testid="own-workbook" data-mode="line" className="text-xs text-text-primary">
        <div
          data-testid="own-workbook-line"
          role="button"
          tabIndex={0}
          title={line.full !== '' ? line.full : open}
          onMouseDown={(e) => e.preventDefault()}
          onClick={() => onSetMode('full')}
          onKeyDown={(e) => { if (e.key === 'Enter' || e.key === ' ') onSetMode('full') }}
          className="flex items-center gap-1.5 px-2 h-8 cursor-pointer select-none"
        >
          <Notebook size={13} className="flex-shrink-0 text-text-secondary" />
          <span data-testid="own-workbook-line-text" className="truncate min-w-0 flex-1">{line.text}</span>
          <span className="flex items-center gap-0.5 flex-shrink-0" onClick={(e) => e.stopPropagation()}>{controls}</span>
        </div>
      </div>
    )
  }
  return (
    <div data-testid="own-workbook" data-mode={mode} className="flex flex-col flex-1 min-h-0">
      <TeamSeatWorkbookView hostId={hostId} sessionId={sessionId} title="" trailing={controls} />
    </div>
  )
}
