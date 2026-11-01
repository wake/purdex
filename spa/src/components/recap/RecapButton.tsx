// spa/src/components/recap/RecapButton.tsx — the ways in to a session's recap history (plan T14b): the clock at the end of a team row, and the clock in the title bar
// (which opens the recap of the session the active tab shows, even while the panel sits in the title bar: opening brings it out). Both need `dialogue.v1` on the
// session's host; without it they are not drawn.
import { ClockCounterClockwise } from '@phosphor-icons/react'
import { selectDialogueEntry, selectDialogueSupport, useDialogueStore } from '../../stores/useDialogueStore'
import { isRecapOpen, openRecapView, useRecapViewStore, type RecapTarget } from '../../stores/useRecapViewStore'
import { useI18nStore } from '../../stores/useI18nStore'
import { keepFocus } from '../../lib/keep-focus'
import { BUTTON, IDLE, PRESSED } from '../title-bar-styles'

const NO_DRAG = { WebkitAppRegion: 'no-drag' } as React.CSSProperties

function useRecapOpen(target: RecapTarget): boolean {
  return useRecapViewStore((s) => isRecapOpen(s.open, target.scope, target.sessionId))
}

const toggle = (target: RecapTarget, open: boolean) => {
  if (open) useRecapViewStore.getState().close()
  else openRecapView(target)
}

/** The clock at the end of a team row's first line: opens the seat's recap history (a click goes no further than the button: the row behind it switches tabs). */
export function RecapRowButton({ target }: { target: RecapTarget }) {
  const t = useI18nStore((s) => s.t)
  const supported = useDialogueStore((s) => target.hostId !== '' && target.sessionId !== '' && selectDialogueSupport(target.hostId, s))
  const open = useRecapOpen(target)
  if (!supported) return null
  const label = t(open ? 'recap.row.close' : 'recap.row.open')
  return (
    <button
      type="button"
      data-testid="recap-row-button"
      aria-pressed={open}
      aria-label={label}
      title={label}
      onMouseDown={keepFocus}
      onClick={(e) => { e.stopPropagation(); toggle(target, open) }}
      onKeyDown={(e) => { if (e.key === 'Enter' || e.key === ' ') e.stopPropagation() }}
      className={`p-0.5 rounded flex-shrink-0 cursor-pointer hover:bg-surface-hover ${open ? 'text-text-primary' : 'text-text-secondary hover:text-text-primary'}`}
    >
      <ClockCounterClockwise size={14} weight={open ? 'fill' : 'regular'} />
    </button>
  )
}

/**
 * The title bar's recap button, next to the panel's own: lit while the history is shown (`panelFull`: the area is in the pane in its full form, where the history sits),
 * with a small dot in the corner while a recap is being written.
 */
export function TitleBarRecapButton({ target, name, panelFull }: { target: RecapTarget; name: string; panelFull: boolean }) {
  const t = useI18nStore((s) => s.t)
  const supported = useDialogueStore((s) => target.hostId !== '' && target.sessionId !== '' && selectDialogueSupport(target.hostId, s))
  const generating = useDialogueStore((s) => selectDialogueEntry(s, target.hostId, target.sessionId)?.generating === true)
  const shown = useRecapOpen(target)
  if (!supported) return null
  const open = shown && panelFull
  const label = open ? t('recap.button.close') : t('recap.button.open', { name })
  return (
    <div data-testid="recap-button-wrap" className="shrink-0 flex items-center mr-0.5" style={NO_DRAG}>
      <button
        type="button"
        data-testid="recap-button"
        aria-pressed={open}
        aria-label={label}
        title={label}
        className={`relative ${BUTTON} ${open ? PRESSED : IDLE}`}
        onMouseDown={keepFocus}
        onClick={() => toggle(target, open)}
      >
        <ClockCounterClockwise size={14} weight={open ? 'fill' : 'regular'} />
        {generating && <span data-testid="recap-button-dot" className="absolute top-0.5 right-0.5 w-1.5 h-1.5 rounded-full bg-accent-base ring-[1.5px] ring-surface-secondary" />}
      </button>
    </div>
  )
}
