// spa/src/components/journal/JournalButtons.tsx — the title bar's 「團隊」 and 「工作簿」 buttons on a journal.v1 host (J-2b T-2b.1,
// spec §11; JV-2 spec §5 Q1). They replace the notebook buttons. 團隊 (team tabs only) is a one-click minimise / restore of the
// team panel: full or one line -> the title bar, the title bar -> full. 工作簿 (any tab with a `cc` session) opens / closes the
// journal card of the tab's unit and carries the number of open 待確認 items (Q-B; nothing at 0).
import { Notebook, UsersThree } from '@phosphor-icons/react'
import { BUTTON, IDLE, PRESSED } from '../title-bar-styles'
import { keepFocus } from '../../lib/keep-focus'
import { useI18nStore } from '../../stores/useI18nStore'
import { useTeamUiStore } from '../../stores/useTeamUiStore'
import { patchUnitMemo, useViewMemo } from '../../lib/journal/use-view-memo'
import { PersonalButton } from '../team/TeamTitleBar'
import { usePersonalTarget } from '../team/usePersonalPanel'
import { useJournalTarget, type JournalTarget } from './useJournalTarget'
import type { PanelMode } from '../../stores/useTeamUiStore'

const NO_DRAG = { WebkitAppRegion: 'no-drag' } as React.CSSProperties

export function JournalButtons({ target }: { target?: JournalTarget }) {
  const own = useJournalTarget()
  const t = target ?? own
  const personal = usePersonalTarget()
  return (
    <>
      {t.team ? <TeamPanelButton teamKey={t.team.teamKey} mode={t.team.mode} /> : personal && <PersonalButton />}
      {t.origin && <JournalCardButton target={t} />}
    </>
  )
}

/** Lit while the team panel is collapsed to the title bar. */
function TeamPanelButton({ teamKey, mode }: { teamKey: string; mode: PanelMode }) {
  const tr = useI18nStore((s) => s.t)
  const inBar = mode === 'titlebar'
  const label = tr(inBar ? 'team.titlebar.to_pane' : 'team.titlebar.to_titlebar')
  return (
    <div data-testid="journal-team-wrap" className="shrink-0 flex items-center mr-0.5" style={NO_DRAG}>
      <button
        type="button"
        data-testid="journal-team-button"
        aria-pressed={inBar}
        aria-label={label}
        title={label}
        className={`${BUTTON} ${inBar ? PRESSED : IDLE}`}
        onMouseDown={keepFocus}
        onClick={() => useTeamUiStore.getState().setPanelMode(teamKey, inBar ? 'full' : 'titlebar')}
      >
        <UsersThree size={14} weight={inBar ? 'fill' : 'regular'} />
      </button>
    </div>
  )
}

function JournalCardButton({ target }: { target: JournalTarget }) {
  const tr = useI18nStore((s) => s.t)
  const host = target.memoHost
  const memo = useViewMemo(host, target.memoKey)
  const count = target.unit?.openConfirms ?? 0
  const open = memo.journalOpen
  const label = tr(open ? 'journal.journal.close' : 'journal.journal.open')
  return (
    <div data-testid="journal-journal-wrap" data-journal-keep="" className="shrink-0 flex items-center mr-0.5" style={NO_DRAG}>
      <button
        type="button"
        data-testid="journal-journal-button"
        aria-pressed={open}
        aria-label={count > 0 ? `${label} (${tr('journal.journal.pending', { count })})` : label}
        title={label}
        disabled={target.memoKey === null}
        className={`${BUTTON} relative ${open ? PRESSED : IDLE}`}
        onMouseDown={keepFocus}
        onClick={() => patchUnitMemo(host, target.memoKey, { journalOpen: !open })}
      >
        <Notebook size={14} weight={open ? 'fill' : 'regular'} />
        {count > 0 && (
          <span
            data-testid="journal-journal-count"
            className="absolute -top-1 -right-1 min-w-[14px] h-[14px] px-[3px] rounded-full bg-status-warning text-[9px] leading-[14px] text-center font-medium text-black tabular-nums"
          >
            {count > 99 ? '99+' : count}
          </span>
        )}
      </button>
    </div>
  )
}
