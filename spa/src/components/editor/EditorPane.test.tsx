import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent, waitFor } from '@testing-library/react'
import type { ReactNode } from 'react'
import { EditorPane } from './EditorPane'
import { useEditorStore } from '../../stores/useEditorStore'
import { useTabStore } from '../../stores/useTabStore'
import { useWorkspaceStore } from '../../features/workspace/store'
import { bufferKey } from '../../lib/editor-buffer-key'
import type { Pane } from '../../types/tab'

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
vi.mock('../RenamePopover', () => ({ RenamePopover: () => null }))
vi.mock('./TiptapEditor', () => ({ TiptapEditor: () => <div data-testid="tiptap" /> }))

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

// Path-aware backend so the chip click drives `listTreeUnder`.
const listMock = vi.fn()
vi.mock('../../lib/fs-backend', () => ({
  getFsBackend: () => ({
    list: listMock,
    read: vi.fn().mockRejectedValue(new Error('unused')),
    stat: vi.fn().mockRejectedValue(new Error('unused')),
  }),
  registerFsBackend: vi.fn(),
}))

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

describe('EditorPane — breadcrumb quick-switch (T6)', () => {
  beforeEach(() => {
    openInAppFileMock.mockReset()
    listMock.mockReset()
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
