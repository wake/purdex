import { act, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { EditorPane } from '../EditorPane'
import { useEditorStore } from '../../../stores/useEditorStore'
import type { Pane } from '../../../types/tab'

const inAppBackend = vi.hoisted(() => ({
  openDocument: vi.fn(),
  saveDocument: vi.fn(),
  stat: vi.fn(),
}))

vi.mock('../../../lib/fs-backend', () => ({
  getFsBackend: vi.fn(() => undefined),
}))

vi.mock('../../../lib/fs-backend-inapp', () => ({
  getInAppBackend: vi.fn(() => inAppBackend),
}))

vi.mock('../MonacoWrapper', () => ({
  MonacoWrapper: () => <div data-testid="monaco-wrapper" />,
}))

vi.mock('../DiffView', () => ({
  DiffView: () => <div data-testid="diff-view" />,
}))

vi.mock('../EditorStatusBar', () => ({
  EditorStatusBar: () => <div data-testid="editor-status-bar" />,
}))

function createPane(filePath = '/notes/old.md'): Pane {
  return {
    id: 'pane-editor',
    content: {
      kind: 'editor',
      source: { type: 'inapp' },
      docId: 'doc-1',
      filePath,
    },
  }
}

describe('EditorPane', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    useEditorStore.getState().clearAllBuffers()
    inAppBackend.stat.mockResolvedValue({
      isFile: true,
      isDirectory: false,
      size: 12,
      mtime: 123,
    })
  })

  it('renders the latest in-app path resolved by docId instead of the stale pane cache', async () => {
    inAppBackend.openDocument.mockResolvedValue({
      docId: 'doc-1',
      path: '/notes/renamed.md',
      text: 'hello world',
      version: 2,
      bindingStatus: 'active',
    })

    render(<EditorPane pane={createPane()} isActive />)

    await waitFor(() => {
      expect(screen.getByText('renamed.md')).toBeInTheDocument()
    })

    expect(screen.queryByText('old.md')).not.toBeInTheDocument()
  })

  it('marks the in-app buffer orphaned when save requires Save As', async () => {
    inAppBackend.openDocument.mockResolvedValue({
      docId: 'doc-1',
      path: '/notes/original.md',
      text: 'hello world',
      version: 3,
      bindingStatus: 'active',
    })
    inAppBackend.saveDocument.mockRejectedValue(new Error('Save As required: path already exists'))

    render(<EditorPane pane={createPane('/notes/original.md')} isActive />)

    await waitFor(() => {
      expect(useEditorStore.getState().buffers['doc-1']).toBeDefined()
    })

    act(() => {
      useEditorStore.getState().updateContent('doc-1', 'changed content')
    })

    fireEvent.click(screen.getByTitle('Save (⌘S)'))

    await waitFor(() => {
      expect(useEditorStore.getState().buffers['doc-1']?.bindingStatus).toBe('orphaned')
    })
  })
})
