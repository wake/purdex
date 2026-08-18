// spa/src/components/editor/__tests__/editor-pane-harness.tsx
//
// Shared fixture for the EditorPane suites that drive the component with the
// REAL i18n catalogue and the REAL RenamePopover (they assert on rendered
// English and on the popover's own input).
//
// The mock *registrations* stay in each test file — `vi.mock` is hoisted
// per-file — and the spies/stubs they reach for live in `editor-pane-mocks.tsx`
// (which must not import EditorPane; see the note there). Everything else the
// suites share is here.
import { act, render, waitFor } from '@testing-library/react'
import { expect, vi } from 'vitest'
import { EditorPane } from '../EditorPane'
import { useEditorStore } from '../../../stores/useEditorStore'
import { useTabStore } from '../../../stores/useTabStore'
import { useEditorSettingsStore } from '../../../stores/useEditorSettingsStore'
import { useRecentFilesStore } from '../../../stores/useRecentFilesStore'
import { bufferKey } from '../../../lib/editor-buffer-key'
import { getFsBackendMock, monacoPropsSpy, tiptapPropsSpy } from './editor-pane-mocks'
import type { Pane } from '../../../types/tab'
import type { FsBackend } from '../../../lib/fs-backend'

// --- pane / buffer fixtures -------------------------------------------------

export function createPane(filePath = '/notes/editor.md', paneId = 'pane-editor'): Pane {
  return {
    id: paneId,
    content: {
      kind: 'editor',
      source: { type: 'inapp' },
      filePath,
    },
  }
}

export function createUntitledPane(name = 'Untitled', suggestedExtension: '.txt' | '.md' = '.md', paneId = 'pane-editor'): Pane {
  return {
    id: paneId,
    content: {
      kind: 'editor',
      source: { type: 'inapp' },
      filePath: `untitled:${name}`,
      untitled: {
        name,
        suggestedExtension,
        hasBeenRenamed: false,
      },
    },
  }
}

export function createBackend(): FsBackend & {
  read: ReturnType<typeof vi.fn>
  write: ReturnType<typeof vi.fn>
  stat: ReturnType<typeof vi.fn>
  rename: ReturnType<typeof vi.fn>
} {
  const read = vi.fn(async (_path: string) => new Uint8Array())
  const write = vi.fn(async (_path: string, _content: Uint8Array) => {})
  const stat = vi.fn(async (_path: string) => ({
    isFile: true,
    isDirectory: false,
    size: 0,
    mtime: 0,
  }))

  return {
    id: 'test-backend',
    label: 'Test Backend',
    available: vi.fn(() => true),
    read,
    write,
    stat,
    list: vi.fn(),
    mkdir: vi.fn(),
    delete: vi.fn(),
    rename: vi.fn(),
    createUnique: vi.fn(),
    mkdirUnique: vi.fn(),
  } as FsBackend & {
    read: ReturnType<typeof vi.fn>
    write: ReturnType<typeof vi.fn>
    stat: ReturnType<typeof vi.fn>
    rename: ReturnType<typeof vi.fn>
  }
}

export function getBufferKey(filePath: string): string {
  return bufferKey({ type: 'inapp' }, filePath)
}

/**
 * Model a first-save target that only exists once it has been written. The
 * untitled first save probes `stat` BEFORE writing so it can refuse to clobber
 * a file that is already there, so a fixture whose `stat` always resolves would
 * describe a world where every new name is already taken.
 */
export function statMissingUntilWritten(
  backend: ReturnType<typeof createBackend>,
  stat: { size: number; mtime: number },
): void {
  backend.stat.mockImplementation(async () => {
    if (backend.write.mock.calls.length === 0) {
      throw Object.assign(new Error('ENOENT'), { code: 'ENOENT' })
    }
    return { isFile: true, isDirectory: false, ...stat }
  })
}

export function registerTabPane(pane: Pane, tabId = 'tab-1') {
  useTabStore.setState({
    tabs: {
      [tabId]: {
        id: tabId,
        pinned: false,
        locked: false,
        createdAt: 1,
        layout: { type: 'leaf', pane },
      },
    },
    tabOrder: [tabId],
    activeTabId: tabId,
    visitHistory: [],
  })
}

/** The reset every EditorPane suite ran in its own `beforeEach`. */
export function resetEditorPaneStores() {
  vi.clearAllMocks()
  useEditorStore.getState().clearAllBuffers()
  useTabStore.setState({ tabs: {}, tabOrder: [], activeTabId: null, visitHistory: [] })
  useEditorSettingsStore.setState({ contentWidth: 'narrow' })
  useRecentFilesStore.setState({ files: [] })
}

// --- render helpers ---------------------------------------------------------

export function renderEditorPane(pane: Pane, isActive = true) {
  return render(<EditorPane pane={pane} isActive={isActive} />)
}

/**
 * The editors register `handleSave` as their save command, so invoking the
 * captured prop IS the keyboard path. Monaco owns the raw surface and Tiptap the
 * Live-Mode one; neither passes an anchorRect.
 */
export async function pressCmdS(surface: 'monaco' | 'tiptap' = 'monaco') {
  const spy = surface === 'monaco' ? monacoPropsSpy : tiptapPropsSpy
  const props = spy.mock.calls.at(-1)?.[0] as { onSave: () => void | Promise<void> }
  await act(async () => {
    await props.onSave()
  })
}

/** Render a pane whose file loads cleanly, and wait for its buffer to exist. */
export async function renderLoaded(pane: Pane, body = 'hello') {
  const backend = createBackend()
  backend.read.mockResolvedValue(new TextEncoder().encode(body))
  backend.write.mockResolvedValue(undefined)
  backend.stat.mockResolvedValue({ isFile: true, isDirectory: false, size: body.length, mtime: 1 })
  getFsBackendMock.mockReturnValue(backend)
  registerTabPane(pane)
  renderEditorPane(pane)
  const filePath = pane.content.kind === 'editor' ? pane.content.filePath : ''
  await waitFor(() => {
    expect(useEditorStore.getState().buffers[getBufferKey(filePath)]).toBeDefined()
  })
  return backend
}

/** Render a never-named untitled pane whose target only exists once written. */
export async function renderUntitled(paneId: string, extension: '.txt' | '.md' = '.txt') {
  const pane = createUntitledPane('Untitled', extension, paneId)
  const backend = createBackend()
  backend.write.mockResolvedValue(undefined)
  statMissingUntilWritten(backend, { size: 0, mtime: 7 })
  getFsBackendMock.mockReturnValue(backend)
  registerTabPane(pane)
  renderEditorPane(pane)
  await waitFor(() => {
    expect(useEditorStore.getState().buffers[getBufferKey('untitled:Untitled')]).toBeDefined()
  })
  return backend
}
