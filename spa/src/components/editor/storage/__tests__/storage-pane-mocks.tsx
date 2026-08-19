// spa/src/components/editor/storage/__tests__/storage-pane-mocks.tsx
//
// The module every `vi.mock` factory in the StoragePane suites reaches into.
// `vi.mock` registrations are hoisted per test FILE, so they cannot be shared
// directly — but their factory BODIES can: each suite registers the same one
// liner (`vi.mock('x', async () => (await import('./storage-pane-mocks')).xMock())`)
// and the wiring lives here exactly once.
//
// It deliberately imports nothing from the component under test: a factory runs
// while `../StoragePane` is still being evaluated, so pulling StoragePane in
// here would deadlock on its own import promise. Fixtures and the shared
// `beforeEach` reset live next door in `storage-pane-harness.tsx`.
import { useState } from 'react'
import { vi } from 'vitest'
import type { Mock } from 'vitest'
import type { ReactNode } from 'react'
import type { DragEndEvent } from '@dnd-kit/core'
import type { FsBackend } from '../../../../lib/fs-backend'
import type { FileEntry, FileStat, FileSource } from '../../../../types/fs'
import type { Tab } from '../../../../types/tab'

/**
 * Ordered trace of the store/backend mutations a flow triggers, so ordering
 * assertions (close-the-pane BEFORE backend.delete) have something to read.
 */
export const eventLog: string[] = []

// --- dnd-kit ---------------------------------------------------------------

/**
 * The `onDragEnd` the mocked `DndContext` captured, so a suite can drive a drop
 * synthetically (a real pointer drag is impractical in jsdom — the plan endorses
 * a pure handler + an onDragEnd-capture smoke).
 */
export let mockDragEnd: ((event: DragEndEvent) => void | Promise<void>) | undefined

export function resetDragEnd(): void {
  mockDragEnd = undefined
}

/**
 * dnd-kit reduced to a lightweight harness: DndContext captures `onDragEnd`,
 * and the draggable/droppable hooks become no-ops so rows still render and the
 * click/double-click suites keep passing.
 */
export function dndKitMock() {
  return {
    DndContext: ({ children, onDragEnd }: { children: ReactNode; onDragEnd: (e: DragEndEvent) => void }) => {
      mockDragEnd = onDragEnd
      return children
    },
    useDraggable: () => ({
      attributes: {},
      listeners: {},
      setNodeRef: () => undefined,
      transform: null,
      isDragging: false,
    }),
    useDroppable: () => ({ setNodeRef: () => undefined, isOver: false }),
    PointerSensor: class {},
    useSensor: () => ({}),
    useSensors: () => [],
    closestCenter: () => [],
  }
}

// --- fs-backend ------------------------------------------------------------

export type MockBackend = {
  list: Mock
  write: Mock
  delete: Mock
  rename: Mock
  stat: Mock
  read: Mock
  mkdir: Mock
  mkdirUnique: Mock
  createUnique: Mock
  id: 'inapp'
  label: string
  available: () => boolean
}

/** Re-created per test by `resetStoragePaneMocks`; read live by the mock below. */
export let mockBackend: MockBackend = makeMockBackend()

export function makeMockBackend(): MockBackend {
  return {
    id: 'inapp',
    label: 'In-App Storage',
    available: () => true,
    list: vi.fn().mockResolvedValue([] as FileEntry[]),
    write: vi.fn().mockResolvedValue(undefined),
    delete: vi.fn().mockImplementation(async (path: string) => {
      eventLog.push(`delete:${path}`)
    }),
    rename: vi.fn().mockResolvedValue(undefined),
    stat: vi.fn().mockResolvedValue({ size: 0, mtime: 0, isDirectory: false, isFile: true } as FileStat),
    read: vi.fn().mockResolvedValue(new Uint8Array(0)),
    mkdir: vi.fn().mockResolvedValue(undefined),
    mkdirUnique: vi.fn().mockResolvedValue('/buffer/New Folder'),
    createUnique: vi.fn().mockResolvedValue('/buffer/Untitled.md'),
  }
}

export function setMockBackend(next: MockBackend): void {
  mockBackend = next
}

/**
 * list/write/delete/rename/read are per-test spies. The capability guards
 * (codex H1) narrow a resolved backend; mirror the real typeof check against
 * the mock backend.
 */
export function fsBackendMock() {
  return {
    getFsBackend: () => mockBackend as unknown as FsBackend,
    registerFsBackend: vi.fn(),
    supportsCreateUnique: (b: { createUnique?: unknown } | undefined) =>
      typeof b?.createUnique === 'function',
    supportsMkdirUnique: (b: { mkdirUnique?: unknown } | undefined) =>
      typeof b?.mkdirUnique === 'function',
  }
}

// --- open routing / download -----------------------------------------------

/**
 * Open routing is the StoragePane's collaboration boundary: the pane resolves
 * the workspace id and hands (path, wsId) to `openInAppFile`. The registry
 * kind-dispatch (md→editor / png→image-preview / pdf→pdf-preview) +
 * open-or-focus + insertTab placement are exercised in
 * `lib/open-in-app-file.test.ts`; the suites here only assert the pane routes
 * through it.
 */
export function openInAppFileMock() {
  return { openInAppFile: vi.fn(() => 'opened-tab') }
}

/**
 * The Download toolbar button routes through the real `downloadStorageFile`
 * (which reads via the mocked backend) but the final OS download is the shared
 * `triggerDownload` util — stub it so jsdom's missing createObjectURL /
 * navigation never fires, and so the dispatch is assertable.
 */
export function downloadFileMock() {
  return { triggerDownload: vi.fn() }
}

// --- workspace store -------------------------------------------------------

/**
 * `StoragePane.resolveWorkspaceId` maps the owning tab → its workspace (R2-2 —
 * no activeWorkspaceId guess). The Storage pane lives in `storageTab` (seeded
 * into the tab store), which this mock maps to ws1; any other / missing tab
 * resolves to null (the "no owning workspace" case).
 */
export function workspaceStoreMock() {
  return {
    useWorkspaceStore: {
      getState: () => ({
        activeWorkspaceId: 'ws1',
        findWorkspaceByTab: (tabId: string) => (tabId === 'storageTab' ? { id: 'ws1' } : null),
      }),
    },
  }
}

// --- i18n ------------------------------------------------------------------

/**
 * A spy so the upload banner tests (C6) can assert the interpolation PARAMS
 * (name / cap / counts), not just the key. It still returns the key with any
 * `{{var}}` placeholders substituted, so `getByText('editor.buffers.…')`
 * assertions (keys carry no placeholders) hold.
 */
export const tSpy = vi.fn((key: string, params?: Record<string, string | number>): string => {
  if (!params) return key
  return key.replace(/\{\{(\w+)\}\}/g, (_, k) => String(params[k] ?? ''))
})

export function i18nStoreMock() {
  return {
    useI18nStore: (
      selector: (s: { t: (k: string, p?: Record<string, string | number>) => string }) => unknown,
    ) => selector({ t: tSpy }),
  }
}

// --- tab store -------------------------------------------------------------

export const setPaneContentSpy = vi.fn()
export const setActiveTabSpy = vi.fn()
export const addTabSpy = vi.fn()
export const closePaneSpy = vi.fn()
export const renameEditorPanesSpy = vi.fn()

export type TabStoreState = {
  tabs: Record<string, Tab>
  tabOrder: string[]
  activeTabId: string | null
  setPaneContent: typeof setPaneContentSpy
  setActiveTab: typeof setActiveTabSpy
  addTab: typeof addTabSpy
  closePane: typeof closePaneSpy
  renameEditorPanes: typeof renameEditorPanesSpy
}

/** Re-created per test by `resetStoragePaneMocks`; read live by the mock below. */
export let tabStoreState: TabStoreState = makeTabStoreState()

export function makeTabStoreState(): TabStoreState {
  return {
    tabs: {},
    tabOrder: [],
    activeTabId: null,
    setPaneContent: setPaneContentSpy,
    setActiveTab: setActiveTabSpy,
    addTab: addTabSpy,
    closePane: closePaneSpy,
    renameEditorPanes: renameEditorPanesSpy,
  }
}

export function setTabStoreState(next: TabStoreState): void {
  tabStoreState = next
}

export function tabStoreMock() {
  return { useTabStore: { getState: () => tabStoreState } }
}

/**
 * The layout half of a rename: re-point every `editor` pane sitting on
 * `oldPath`. Mirrors the real store action closely enough for the Storage
 * suites, which assert on the resulting `tabStoreState.tabs`.
 */
export function applyRenameEditorPanes(source: FileSource, oldPath: string, newPath: string): void {
  eventLog.push(`renameEditorPanes:${oldPath}:${newPath}`)
  const nextTabs: Record<string, Tab> = {}
  for (const [tabId, tab] of Object.entries(tabStoreState.tabs)) {
    if (tab.layout.type === 'leaf') {
      const c = tab.layout.pane.content
      if (c.kind === 'editor' && c.source.type === source.type && c.filePath === oldPath) {
        nextTabs[tabId] = {
          ...tab,
          layout: {
            type: 'leaf',
            pane: { ...tab.layout.pane, content: { ...c, filePath: newPath } },
          },
        }
        continue
      }
    }
    nextTabs[tabId] = tab
  }
  tabStoreState.tabs = nextTabs
}

// --- RenamePopover ---------------------------------------------------------

/**
 * A controlled stand-in for the real popover: it renders the input, surfaces
 * `validateName` inline, and exposes confirm / cancel buttons so the suites can
 * drive a rename without the real popover's portal + focus machinery.
 */
export function RenameHarness({
  currentName,
  onConfirm,
  onCancel,
  validateName,
  externalError,
}: {
  currentName: string
  onConfirm: (name: string) => Promise<void>
  onCancel: () => void
  validateName?: (name: string, cur: string) => string | undefined
  externalError?: string
}) {
  const [value, setValue] = useState(currentName)
  const validationError = validateName?.(value.trim(), currentName)
  const error = validationError ?? externalError
  return (
    <div data-testid="rename-popover-harness">
      <input
        data-testid="rename-input"
        value={value}
        onChange={(e: React.ChangeEvent<HTMLInputElement>) => setValue(e.target.value)}
      />
      {error && <p data-testid="rename-error">{error}</p>}
      <button data-testid="rename-confirm" disabled={!!validationError} onClick={() => onConfirm(value.trim())}>
        confirm
      </button>
      <button data-testid="rename-cancel" onClick={onCancel}>
        cancel
      </button>
    </div>
  )
}

export function renamePopoverMock() {
  return {
    RenamePopover: ({
      currentName,
      onConfirm,
      onCancel,
      validateName,
      error,
    }: {
      currentName: string
      onConfirm: (name: string) => Promise<void>
      onCancel: () => void
      validateName?: (name: string, cur: string) => string | undefined
      error?: string
    }) => (
      <RenameHarness
        currentName={currentName}
        onConfirm={onConfirm}
        onCancel={onCancel}
        validateName={validateName}
        externalError={error}
      />
    ),
  }
}
