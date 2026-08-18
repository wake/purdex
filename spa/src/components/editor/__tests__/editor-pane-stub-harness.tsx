// spa/src/components/editor/__tests__/editor-pane-stub-harness.tsx
//
// Fixtures and render helpers for the *stubbed-surface* EditorPane suites. The
// stubs and mock functions themselves live in `editor-pane-stub-mocks.tsx`
// (which must stay free of any EditorPane import; see the note there).
//
// This is a different world from `editor-pane-harness.tsx`, which drives the
// real i18n catalogue and the real RenamePopover. The two cannot be merged
// without rewriting one side's assertions.
import { act, render, screen, fireEvent } from '@testing-library/react'
import { EditorPane } from '../EditorPane'
import { useEditorStore } from '../../../stores/useEditorStore'
import { useTabStore } from '../../../stores/useTabStore'
import { useWorkspaceStore } from '../../../features/workspace/store'
import { useUndoToast } from '../../../stores/useUndoToast'
import { bufferKey } from '../../../lib/editor-buffer-key'
import { renamePopover } from './editor-pane-stub-mocks'
import type { Pane, UntitledDocumentState } from '../../../types/tab'

export const PANE_ID = 'pane-1'
export const TAB_ID = 'tab-1'
export const WS_ID = 'ws-1'
export const FILE = '/buffer/note.md'
export const OTHER_FILE = '/buffer/other.md'
export const FILE_KEY = bufferKey({ type: 'inapp' }, FILE)

export const UNTITLED_PATH = 'untitled:Untitled'
export const UNTITLED: UntitledDocumentState = {
  name: 'Untitled',
  suggestedExtension: '.md',
  hasBeenRenamed: false,
}
export const TARGET_PATH = '/buffer/report.md'
export const UNTITLED_KEY = bufferKey({ type: 'inapp' }, UNTITLED_PATH)
export const TARGET_KEY = bufferKey({ type: 'inapp' }, TARGET_PATH)

export function makePane(): Pane {
  return { id: PANE_ID, content: { kind: 'editor', source: { type: 'inapp' }, filePath: FILE } }
}

export function makeOtherPane(): Pane {
  return { id: PANE_ID, content: { kind: 'editor', source: { type: 'inapp' }, filePath: OTHER_FILE } }
}

export function makeUntitledPane(): Pane {
  return {
    id: PANE_ID,
    content: {
      kind: 'editor',
      source: { type: 'inapp' },
      filePath: UNTITLED_PATH,
      untitled: UNTITLED,
    },
  }
}

export function seedTab(pane: Pane) {
  useTabStore.setState({
    tabs: {
      [TAB_ID]: {
        id: TAB_ID,
        pinned: false,
        locked: false,
        createdAt: 0,
        layout: { type: 'leaf', pane },
      },
    },
  })
  useWorkspaceStore.setState({
    workspaces: [
      { id: WS_ID, name: 'WS', tabs: [TAB_ID], activeTabId: TAB_ID, moduleConfig: {} },
    ],
    activeWorkspaceId: WS_ID,
  })
}

/** Seed a loaded (non-dirty) buffer at `FILE` so the pane renders past its Loading guard. */
export function seedLoadedBuffer(content = '# hi', language = 'markdown') {
  useEditorStore.setState({ buffers: {}, paneStates: {} })
  useEditorStore.getState().openBuffer(FILE_KEY, content, { language })
}

/** Seed the never-named untitled buffer the first-save suite works on. */
export function seedUntitledBuffer(content = 'draft body') {
  useEditorStore.setState({ buffers: {}, paneStates: {} })
  useEditorStore.getState().openBuffer(UNTITLED_KEY, content, {
    language: 'markdown',
    untitled: UNTITLED,
  })
}

export function renderEditorPane(pane: Pane, isActive = false) {
  return render(<EditorPane pane={pane} isActive={isActive} />)
}

export function renderPane() {
  return renderEditorPane(makePane())
}

export function notFound(): Error {
  return Object.assign(new Error('ENOENT'), { code: 'ENOENT' })
}

export function toastMessage(): string | undefined {
  return useUndoToast.getState().toast?.message
}

export function clickSave() {
  fireEvent.click(screen.getByTitle('Save (⌘S)'))
}

/** Open the naming popover from the Save button and confirm it with `name`. */
export async function saveUntitledAs(name: string) {
  clickSave()
  await screen.findByTestId('rename-popover')
  await act(async () => {
    await renamePopover.props?.onConfirm(name)
  })
}
