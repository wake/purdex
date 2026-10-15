// spa/src/components/journal/JournalButtons.tsx — the title bar's 「團隊」 and 「日誌」 buttons on a journal.v1 host (J-2b T-2b.1,
// spec §11). They replace the notebook buttons. 團隊 (team tabs only) opens / closes the team card; 日誌 (any tab with a `cc`
// session) opens / closes the journal card of the tab's unit and carries the number of open 待確認 items (Q-B; nothing at 0).
import { Notebook, UsersThree } from '@phosphor-icons/react'
import { BUTTON, IDLE, PRESSED } from '../title-bar-styles'
import { keepFocus } from '../../lib/keep-focus'
import { useI18nStore } from '../../stores/useI18nStore'
import { useJournalUiStore } from '../../stores/useJournalUiStore'
import { patchUnitMemo, useViewMemo } from '../../lib/journal/use-view-memo'
import { useJournalTarget, type JournalTarget } from './useJournalTarget'

const NO_DRAG = { WebkitAppRegion: 'no-drag' } as React.CSSProperties

export function JournalButtons({ target }: { target?: JournalTarget }) {
  const own = useJournalTarget()
  const t = target ?? own
  return (
    <>
      {t.team && <TeamCardButton teamKey={t.team.teamKey} />}
      {t.origin && <JournalCardButton target={t} />}
    </>
  )
}

function TeamCardButton({ teamKey }: { teamKey: string }) {
  const tr = useI18nStore((s) => s.t)
  const open = useJournalUiStore((s) => s.teamOpen[teamKey] === true)
  const label = tr(open ? 'journal.team.close' : 'journal.team.open')
  return (
    <div data-testid="journal-team-wrap" className="shrink-0 flex items-center mr-0.5" style={NO_DRAG}>
      <button
        type="button"
        data-testid="journal-team-button"
        aria-pressed={open}
        aria-label={label}
        title={label}
        className={`${BUTTON} ${open ? PRESSED : IDLE}`}
        onMouseDown={keepFocus}
        onClick={() => useJournalUiStore.getState().setTeamOpen(teamKey, !open)}
      >
        <UsersThree size={14} weight={open ? 'fill' : 'regular'} />
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
    <div data-testid="journal-journal-wrap" className="shrink-0 flex items-center mr-0.5" style={NO_DRAG}>
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
