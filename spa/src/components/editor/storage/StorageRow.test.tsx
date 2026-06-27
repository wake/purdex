import { describe, it, expect, vi, beforeEach } from 'vitest'
import type { Mock } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import { StorageRow } from './StorageRow'
import type { TreeNode } from '../../../lib/storage-tree'
import type { FsBackend } from '../../../lib/fs-backend'

// StorageRow word-count reads bytes for text files; stub the backend so the
// effect never throws. Selection/expand behavior is what these tests exercise.
vi.mock('../../../lib/fs-backend', () => ({
  getFsBackend: () => ({ read: vi.fn().mockResolvedValue(new Uint8Array(0)) }) as unknown as FsBackend,
  registerFsBackend: vi.fn(),
}))

function folder(): TreeNode {
  return { path: '/buffer/dir', name: 'dir', isDir: true, size: 0, children: [] }
}
function file(): TreeNode {
  return { path: '/buffer/note.md', name: 'note.md', isDir: false, size: 4 }
}

let onToggle: Mock
let onSelect: Mock
let onOpen: Mock

beforeEach(() => {
  onToggle = vi.fn()
  onSelect = vi.fn()
  onOpen = vi.fn()
})

function renderFolder(expanded = false) {
  render(
    <StorageRow
      node={folder()}
      depth={0}
      selected={false}
      expanded={expanded}
      onToggle={onToggle}
      onSelect={onSelect}
      onOpen={onOpen}
    />,
  )
}

describe('StorageRow folder selection (T0-1)', () => {
  it('clicking the folder name selects it (does NOT toggle expand)', () => {
    renderFolder()
    fireEvent.click(screen.getByTestId('buffer-row'))
    expect(onSelect).toHaveBeenCalledWith('/buffer/dir')
    expect(onToggle).not.toHaveBeenCalled()
  })

  it('clicking the caret toggles expand independently (does NOT select)', () => {
    renderFolder()
    fireEvent.click(screen.getByTestId('buffer-caret'))
    expect(onToggle).toHaveBeenCalledWith('/buffer/dir')
    expect(onSelect).not.toHaveBeenCalled()
  })

  it('double-clicking a folder toggles expand but never opens a tab', () => {
    renderFolder()
    fireEvent.doubleClick(screen.getByTestId('buffer-row'))
    expect(onToggle).toHaveBeenCalledWith('/buffer/dir')
    expect(onOpen).not.toHaveBeenCalled()
  })
})

describe('StorageRow file behavior (unchanged)', () => {
  it('single-click selects a file', () => {
    render(
      <StorageRow
        node={file()}
        depth={0}
        selected={false}
        expanded={false}
        onToggle={onToggle}
        onSelect={onSelect}
        onOpen={onOpen}
      />,
    )
    fireEvent.click(screen.getByTestId('buffer-row'))
    expect(onSelect).toHaveBeenCalledWith('/buffer/note.md')
    expect(onOpen).not.toHaveBeenCalled()
  })

  it('double-click opens a file (no caret rendered)', () => {
    render(
      <StorageRow
        node={file()}
        depth={0}
        selected={false}
        expanded={false}
        onToggle={onToggle}
        onSelect={onSelect}
        onOpen={onOpen}
      />,
    )
    expect(screen.queryByTestId('buffer-caret')).toBeNull()
    fireEvent.doubleClick(screen.getByTestId('buffer-row'))
    expect(onOpen).toHaveBeenCalledWith('/buffer/note.md')
  })
})
