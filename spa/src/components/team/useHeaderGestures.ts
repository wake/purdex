// spa/src/components/team/useHeaderGestures.ts — what a click and a double-click on the panel header mean (TI-7, spec §4.4
// "Header click", §4.12), kept out of the panel's rendering.
//
//   - a click on a button (the switch, enlarge, a cell) is that button's own business and only cancels a pending toggle;
//   - a click anywhere else on the header toggles full / one-line at once;
//   - a click on the team NAME waits NAME_CLICK_DELAY_MS for a double-click: a second click restarts the wait, a
//     double-click cancels it and (when the lead's host can edit and the roster holds the team) opens the edit form;
//   - a pending toggle is dropped by any other header action, by a mode change from outside, and by a change of team;
//     the edit form is closed by a change of team (an A -> B -> A switch must not bring it back).
import { useCallback, useEffect, useRef, useState } from 'react'
import { NAME_CLICK_DELAY_MS } from './panel-layout'

export interface HeaderHandlers {
  onMouseDown: (e: React.MouseEvent) => void
  onClick: (e: React.MouseEvent<HTMLElement>) => void
  onDoubleClick: (e: React.MouseEvent<HTMLElement>) => void
}

interface Input {
  teamKey: string
  mode: 'full' | 'line'
  onSetMode: (mode: 'full' | 'line') => void
  /** The lead's host lists `team.edit.v1` and the roster holds the team. */
  canEdit: boolean
}

const NAME = '[data-testid="team-panel-name"]'

/** A press on the header does not move the terminal's focus (and a double-click does not select the name's text). */
const keepFocus = (e: React.MouseEvent) => e.preventDefault()

export function useHeaderGestures({ teamKey, mode, onSetMode, canEdit }: Input) {
  const rootRef = useRef<HTMLDivElement>(null)
  const [editing, setEditing] = useState<string | null>(null) // the team key the form was opened for
  if (editing !== null && editing !== teamKey) setEditing(null) // adjust during render: another team, no form
  const latest = useRef({ mode, onSetMode })
  useEffect(() => { latest.current = { mode, onSetMode } })
  const timer = useRef<ReturnType<typeof setTimeout> | undefined>(undefined)
  const clearTimer = useCallback(() => {
    if (timer.current !== undefined) { clearTimeout(timer.current); timer.current = undefined }
  }, [])
  useEffect(() => clearTimer, [clearTimer]) // unmount
  useEffect(() => { clearTimer() }, [teamKey, mode, clearTimer]) // another team, or the mode changed under it
  const toggle = () => latest.current.onSetMode(latest.current.mode === 'full' ? 'line' : 'full')

  const hdr: HeaderHandlers = {
    onMouseDown: keepFocus,
    onClick: (e) => {
      const el = e.target as HTMLElement
      if (el.closest('button')) { clearTimer(); return }
      if (el.closest(NAME)) {
        clearTimer() // a second click restarts the wait; the double-click cancels it
        timer.current = setTimeout(() => { timer.current = undefined; toggle() }, NAME_CLICK_DELAY_MS)
        return
      }
      clearTimer()
      toggle()
    },
    onDoubleClick: (e) => {
      if (!(e.target as HTMLElement).closest(NAME)) return
      clearTimer()
      if (canEdit) setEditing(teamKey)
    },
  }
  const close = useCallback(() => setEditing(null), [])
  /** The live header element (it is a different element in each mode), for the form to hang under. */
  const anchor = useCallback(() => rootRef.current?.querySelector<HTMLElement>('[data-testid="team-panel-header"]') ?? null, [])
  return { rootRef, hdr, editOpen: editing === teamKey, close, anchor }
}
