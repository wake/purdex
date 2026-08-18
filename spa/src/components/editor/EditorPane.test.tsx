import { describe, it, expect, vi, beforeEach } from 'vitest'
import { act, render, screen, fireEvent, waitFor } from '@testing-library/react'
import type { ReactNode } from 'react'
import { EditorPane } from './EditorPane'
import { useEditorStore } from '../../stores/useEditorStore'
import { useTabStore } from '../../stores/useTabStore'
import { useWorkspaceStore } from '../../features/workspace/store'
import { useUndoToast } from '../../stores/useUndoToast'
import { useRecentFilesStore } from '../../stores/useRecentFilesStore'
import { bufferKey } from '../../lib/editor-buffer-key'
import type { Pane, UntitledDocumentState } from '../../types/tab'

// The breadcrumb popover renders via createPortal; inline it for jsdom.
vi.mock('react-dom', async (importOriginal) => {
  const actual = await importOriginal<typeof import('react-dom')>()
  return { ...actual, createPortal: (node: ReactNode) => node }
})

// i18n → key identity.
vi.mock('../../stores/useI18nStore', () => ({
  useI18nStore: (selector: (s: { t: (k: string) => string }) => unknown) =>
    selector({ t: (k: string) => k }),
}))

// Stub the heavy editor surfaces so the test never touches monaco / tiptap.
vi.mock('./MonacoWrapper', () => ({ MonacoWrapper: () => <div data-testid="monaco" /> }))
vi.mock('./DiffView', () => ({ DiffView: () => null }))
vi.mock('./EditorStatusBar', () => ({ EditorStatusBar: () => null }))
vi.mock('./TiptapEditor', () => ({ TiptapEditor: () => <div data-testid="tiptap" /> }))

// The naming / rename popover is the surface every naming outcome lands on, so
// the stub keeps its props reachable (to drive `onConfirm`) and paints `error`
// (so a refused name is assertable).
const renamePopover = vi.hoisted(() => ({
  props: null as { onConfirm: (name: string) => void | Promise<void>; error?: string } | null,
}))
vi.mock('../RenamePopover', () => ({
  RenamePopover: (props: { onConfirm: (name: string) => void | Promise<void>; error?: string }) => {
    renamePopover.props = props
    return <div data-testid="rename-popover">{props.error ?? ''}</div>
  },
}))

// The unit under test: the quick-switch must route through openInAppFile.
const openInAppFileMock = vi.fn()
vi.mock('../../lib/open-in-app-file', () => ({
  openInAppFile: (...args: unknown[]) => openInAppFileMock(...args),
}))

// D2: the "new buffer" entry point reserves through the unified atomic namer.
const createUniqueInAppFileMock = vi.hoisted(() => vi.fn())
vi.mock('../../lib/inapp-namer', () => ({
  createUniqueInAppFile: createUniqueInAppFileMock,
}))

// Path-aware backend so the chip click drives `listTreeUnder`. The resolved
// backend is swappable (`backendRef`) because "there is no backend at all" is
// itself a behaviour under test.
const listMock = vi.fn()
const readMock = vi.fn()
const statMock = vi.fn()
const writeMock = vi.fn()
const renameMock = vi.fn()
const backendRef = vi.hoisted(() => ({ value: undefined as unknown }))
vi.mock('../../lib/fs-backend', () => ({
  getFsBackend: () => backendRef.value,
  registerFsBackend: vi.fn(),
}))

function resetBackend() {
  listMock.mockReset()
  readMock.mockReset().mockRejectedValue(new Error('unused'))
  statMock.mockReset().mockRejectedValue(new Error('unused'))
  writeMock.mockReset().mockResolvedValue(undefined)
  renameMock.mockReset().mockResolvedValue(undefined)
  backendRef.value = {
    list: listMock,
    read: readMock,
    stat: statMock,
    write: writeMock,
    rename: renameMock,
  }
}

const PANE_ID = 'pane-1'
const TAB_ID = 'tab-1'
const WS_ID = 'ws-1'
const FILE = '/buffer/note.md'

function makePane(): Pane {
  return { id: PANE_ID, content: { kind: 'editor', source: { type: 'inapp' }, filePath: FILE } }
}

function renderPane() {
  return render(<EditorPane pane={makePane()} isActive={false} />)
}

function notFound(): Error {
  return Object.assign(new Error('ENOENT'), { code: 'ENOENT' })
}

function toastMessage(): string | undefined {
  return useUndoToast.getState().toast?.message
}

function clickSave() {
  fireEvent.click(screen.getByTitle('Save (⌘S)'))
}

describe('EditorPane — breadcrumb quick-switch (T6)', () => {
  beforeEach(() => {
    openInAppFileMock.mockReset()
    resetBackend()
    listMock.mockImplementation(async (path: string) => {
      if (path === '/buffer') {
        return [
          { name: 'dir', isDir: true, size: 0 },
          { name: 'note.md', isDir: false, size: 0 },
          { name: 'root.md', isDir: false, size: 0 },
        ]
      }
      if (path === '/buffer/dir') {
        return [{ name: 'pic.png', isDir: false, size: 0 }]
      }
      return []
    })

    // Seed a loaded (non-dirty) buffer so EditorPaneInner renders past its
    // Loading guard and the load effect early-returns.
    useEditorStore.setState({ buffers: {}, paneStates: {} })
    useEditorStore.getState().openBuffer(bufferKey({ type: 'inapp' }, FILE), '# hi', {
      language: 'markdown',
    })

    // Seed a tab containing the pane, owned by a workspace.
    useTabStore.setState({
      tabs: {
        [TAB_ID]: {
          id: TAB_ID,
          pinned: false,
          locked: false,
          createdAt: 0,
          layout: { type: 'leaf', pane: makePane() },
        },
      },
    })
    useWorkspaceStore.setState({
      workspaces: [
        { id: WS_ID, name: 'WS', tabs: [TAB_ID], activeTabId: TAB_ID, moduleConfig: {} },
      ],
      activeWorkspaceId: WS_ID,
    })
  })

  it('T6-3: switching to a nested .png routes through openInAppFile with full path + workspace id (not setPaneContent)', async () => {
    const setPaneContentSpy = vi.spyOn(useTabStore.getState(), 'setPaneContent')
    renderPane()

    fireEvent.click(screen.getByRole('button', { name: /Purdex/ }))
    const pngButton = await waitFor(() => screen.getByRole('button', { name: 'dir/pic.png' }))
    fireEvent.click(pngButton)

    expect(openInAppFileMock).toHaveBeenCalledTimes(1)
    expect(openInAppFileMock).toHaveBeenCalledWith('/buffer/dir/pic.png', WS_ID)
    // The old hardcoded `{ kind: 'editor' }` swap must be gone.
    expect(setPaneContentSpy).not.toHaveBeenCalled()
  })

  it('T6-4: switching to a root-level .md still routes through openInAppFile', async () => {
    renderPane()

    fireEvent.click(screen.getByRole('button', { name: /Purdex/ }))
    const mdButton = await waitFor(() => screen.getByRole('button', { name: /^root\.md$/ }))
    fireEvent.click(mdButton)

    expect(openInAppFileMock).toHaveBeenCalledTimes(1)
    expect(openInAppFileMock).toHaveBeenCalledWith('/buffer/root.md', WS_ID)
  })

  // D2 (AC-1b clause 6): the breadcrumb "new buffer" reserves through the atomic
  // namer; two reservations get distinct real paths — never a shared key.
  it('D2: new buffer routes through createUniqueInAppFile with distinct paths (no shared key)', async () => {
    // Empty buffer dir → the popover renders its "new buffer" affordance.
    listMock.mockResolvedValue([])
    createUniqueInAppFileMock
      .mockResolvedValueOnce('/buffer/Untitled.md')
      .mockResolvedValueOnce('/buffer/Untitled-1.md')
    const setPaneContentSpy = vi
      .spyOn(useTabStore.getState(), 'setPaneContent')
      .mockImplementation(() => {})
    renderPane()

    // First reservation.
    fireEvent.click(screen.getByRole('button', { name: /Purdex/ }))
    const firstBtn = await waitFor(() => screen.getByTestId('breadcrumb-popover-new-buffer'))
    fireEvent.click(firstBtn)
    await waitFor(() => expect(setPaneContentSpy).toHaveBeenCalledTimes(1))

    // Second reservation (reopen the popover; the first dismissed it).
    fireEvent.click(screen.getByRole('button', { name: /Purdex/ }))
    const secondBtn = await waitFor(() => screen.getByTestId('breadcrumb-popover-new-buffer'))
    fireEvent.click(secondBtn)
    await waitFor(() => expect(setPaneContentSpy).toHaveBeenCalledTimes(2))

    expect(createUniqueInAppFileMock).toHaveBeenCalledWith('/buffer', 'md')
    const filePaths = setPaneContentSpy.mock.calls.map((c) => (c[2] as { filePath: string }).filePath)
    expect(filePaths).toEqual(['/buffer/Untitled.md', '/buffer/Untitled-1.md'])
    expect(new Set(filePaths).size).toBe(2)
  })

  it('opens a markdown file in Live Mode (wysiwyg) by default', async () => {
    // beforeEach seeds a markdown buffer at FILE. With no explicit user mode
    // choice (paneState.editorMode null), markdown must resolve to wysiwyg.
    renderPane()
    expect(await screen.findByTestId('tiptap')).toBeTruthy()
    expect(screen.queryByTestId('monaco')).toBeNull()
  })

  it('opens a non-markdown file in raw (Monaco) by default', async () => {
    // Same path, but the buffer language is plaintext → default resolves to raw.
    useEditorStore.setState({ buffers: {}, paneStates: {} })
    useEditorStore.getState().openBuffer(bufferKey({ type: 'inapp' }, FILE), 'plain', {
      language: 'plaintext',
    })
    renderPane()
    expect(await screen.findByTestId('monaco')).toBeTruthy()
    expect(screen.queryByTestId('tiptap')).toBeNull()
  })
})

// ---------------------------------------------------------------------------
// Untitled first save: naming a brand-new document must not silently overwrite
// a file that already sits at the target path.
// ---------------------------------------------------------------------------

const UNTITLED_PATH = 'untitled:Untitled'
const UNTITLED: UntitledDocumentState = {
  name: 'Untitled',
  suggestedExtension: '.md',
  hasBeenRenamed: false,
}
const TARGET_PATH = '/buffer/report.md'
const UNTITLED_KEY = bufferKey({ type: 'inapp' }, UNTITLED_PATH)
const TARGET_KEY = bufferKey({ type: 'inapp' }, TARGET_PATH)

function makeUntitledPane(): Pane {
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

function seedTab(pane: Pane) {
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

/** Open the naming popover from the Save button and confirm it with `name`. */
async function saveUntitledAs(name: string) {
  clickSave()
  await screen.findByTestId('rename-popover')
  await act(async () => {
    await renamePopover.props?.onConfirm(name)
  })
}

describe('EditorPane — untitled first save', () => {
  beforeEach(() => {
    resetBackend()
    renamePopover.props = null
    useUndoToast.setState({ toast: null })
    useRecentFilesStore.setState({ files: [] })
    useEditorStore.setState({ buffers: {}, paneStates: {} })
    useEditorStore.getState().openBuffer(UNTITLED_KEY, 'draft body', {
      language: 'markdown',
      untitled: UNTITLED,
    })
    seedTab(makeUntitledPane())
  })

  it('refuses to write when a file already exists at the chosen name', async () => {
    // The target exists on the backend but is NOT open in any pane, so the
    // in-store buffer check cannot see it.
    statMock.mockResolvedValue({ mtime: 111, size: 222, isFile: true, isDirectory: false })
    render(<EditorPane pane={makeUntitledPane()} isActive={false} />)

    await saveUntitledAs('report.md')

    expect(statMock).toHaveBeenCalledWith(TARGET_PATH)
    expect(writeMock).not.toHaveBeenCalled()
    expect(screen.getByTestId('rename-popover').textContent).toBe('File already exists')
    // The untitled buffer stays exactly where it was — nothing was renamed.
    expect(useEditorStore.getState().buffers[UNTITLED_KEY]).toBeTruthy()
    expect(useEditorStore.getState().buffers[TARGET_KEY]).toBeUndefined()
  })

  it('writes normally when nothing occupies the chosen name', async () => {
    // Physical model: the target does not exist until we write it.
    statMock.mockImplementation(async () => {
      if (writeMock.mock.calls.length === 0) throw notFound()
      return { mtime: 5, size: 10, isFile: true, isDirectory: false }
    })
    render(<EditorPane pane={makeUntitledPane()} isActive={false} />)

    await saveUntitledAs('report.md')

    expect(writeMock).toHaveBeenCalledTimes(1)
    expect(writeMock.mock.calls[0][0]).toBe(TARGET_PATH)
    expect(useEditorStore.getState().buffers[TARGET_KEY]).toBeTruthy()
    expect(toastMessage()).toBe('editor.save.saved')
  })
})
