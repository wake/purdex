// spa/src/components/editor/hooks/useRenamePopoverState.ts
//
// The naming popover is driven from three places — the toolbar rename affordance,
// the save flow (first save of an untitled document) and the rename flow — so its
// four pieces of UI state live together behind a stable control surface instead of
// being threaded around as four raw setters.
import { useCallback, useEffect, useMemo, useState } from 'react'

export type RenamePopoverMode = 'rename' | 'save'

export interface RenamePopoverControls {
  /** Open in rename mode, anchored at `rect`, with no seeded draft. */
  openRename: (rect: DOMRect) => void
  /** Open in save mode, anchored at `rect`, seeded with the suggested name. */
  openSave: (rect: DOMRect, suggestedName: string) => void
  /** Dismiss and drop the seeded draft + warning. */
  close: () => void
  setWarning: (warning: string | undefined) => void
}

export interface RenamePopoverState {
  anchorRect: DOMRect | null
  mode: RenamePopoverMode
  initialValue: string | undefined
  warning: string | undefined
  controls: RenamePopoverControls
}

export function useRenamePopoverState(filePath: string): RenamePopoverState {
  const [anchorRect, setAnchorRect] = useState<DOMRect | null>(null)
  const [mode, setMode] = useState<RenamePopoverMode>('rename')
  const [initialValue, setInitialValue] = useState<string>()
  const [warning, setWarning] = useState<string>()

  // Dismiss the popover whenever the pane points at a different file. Kept as a
  // synchronous reset (rather than derived-during-render) because that is the
  // behaviour EditorPane has always had; the rule only started firing once the
  // effect moved out of the large component the react-hooks compiler bailed on.
  useEffect(() => {
    // eslint-disable-next-line react-hooks/set-state-in-effect -- reset-on-file-switch, preserved verbatim from EditorPane
    setAnchorRect(null)
    setWarning(undefined)
  }, [filePath])

  const openRename = useCallback((rect: DOMRect) => {
    setMode('rename')
    setAnchorRect(rect)
    setInitialValue(undefined)
    setWarning(undefined)
  }, [])

  const openSave = useCallback((rect: DOMRect, suggestedName: string) => {
    setMode('save')
    setAnchorRect(rect)
    setInitialValue(suggestedName)
    setWarning(undefined)
  }, [])

  const close = useCallback(() => {
    setAnchorRect(null)
    setInitialValue(undefined)
    setWarning(undefined)
  }, [])

  const controls = useMemo<RenamePopoverControls>(
    () => ({ openRename, openSave, close, setWarning }),
    [openRename, openSave, close],
  )

  return { anchorRect, mode, initialValue, warning, controls }
}
