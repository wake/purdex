// spa/src/components/editor/__tests__/editor-pane-stub-mocks.tsx
//
// The module every `vi.mock` factory in the *stubbed-surface* EditorPane suites
// reaches into. Like `editor-pane-mocks.tsx` it imports nothing from the
// component under test: a factory runs while `../EditorPane` is still being
// evaluated, so pulling EditorPane in here would deadlock.
//
// These suites stub the presentation layer — i18n resolves to the raw key,
// RenamePopover is a props probe, portals are inlined — so their assertions pin
// the outcome SIGNAL (which toast key, which popover error) instead of rendered
// English.
import { vi } from 'vitest'
import type { ReactNode } from 'react'

/** The breadcrumb popover renders via createPortal; inline it for jsdom. */
export function inlinePortal(node: ReactNode) {
  return node
}

/** i18n → key identity. */
export function useI18nStoreStub(selector: (s: { t: (k: string) => string }) => unknown) {
  return selector({ t: (k: string) => k })
}

// Stub the heavy editor surfaces so the tests never touch monaco / tiptap.
export function MonacoStub() {
  return <div data-testid="monaco" />
}

export function DiffViewStub() {
  return null
}

export function EditorStatusBarStub() {
  return null
}

export function TiptapStub() {
  return <div data-testid="tiptap" />
}

// The naming / rename popover is the surface every naming outcome lands on, so
// the stub keeps its props reachable (to drive `onConfirm`) and paints `error`
// (so a refused name is assertable).
export const renamePopover = {
  props: null as { onConfirm: (name: string) => void | Promise<void>; error?: string } | null,
}

export function RenamePopoverStub(props: { onConfirm: (name: string) => void | Promise<void>; error?: string }) {
  // eslint-disable-next-line react-hooks/immutability -- the props probe IS the point: the suites drive onConfirm through it
  renamePopover.props = props
  return <div data-testid="rename-popover">{props.error ?? ''}</div>
}

/** The unit under test for the quick-switch: it must route through openInAppFile. */
export const openInAppFileMock = vi.fn()

/** D2: the "new buffer" entry point reserves through the unified atomic namer. */
export const createUniqueInAppFileMock = vi.fn()

// Path-aware backend so the chip click drives `listTreeUnder`. The resolved
// backend is swappable (`backendRef`) because "there is no backend at all" is
// itself a behaviour under test.
export const listMock = vi.fn()
export const readMock = vi.fn()
export const statMock = vi.fn()
export const writeMock = vi.fn()
export const renameMock = vi.fn()
export const backendRef = { value: undefined as unknown }

export function resetBackend() {
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
