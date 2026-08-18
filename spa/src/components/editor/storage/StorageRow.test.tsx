import { describe, it, expect, vi, beforeEach } from 'vitest'
import type { Mock } from 'vitest'
import { render, screen, fireEvent, within } from '@testing-library/react'
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
let onRename: Mock
let onDelete: Mock
let onToggleSelect: Mock

beforeEach(() => {
  onToggle = vi.fn()
  onSelect = vi.fn()
  onOpen = vi.fn()
  onRename = vi.fn()
  onDelete = vi.fn()
  onToggleSelect = vi.fn()
})

/** The per-test callback set, read at call time (the spies are re-made each test). */
function handlers() {
  return { onToggle, onSelect, onOpen, onRename, onDelete, onToggleSelect }
}

function renderFolder(expanded = false) {
  render(
    <StorageRow
      node={folder()}
      depth={0}
      selected={false}
      expanded={expanded}
      {...handlers()}
    />,
  )
}

describe('StorageRow folder selection (T0-1)', () => {
  it('clicking the folder name selects it (does NOT toggle expand)', () => {
    renderFolder()
    fireEvent.click(screen.getByTestId('buffer-row'))
    // Plain click → non-additive select (codex B5).
    expect(onSelect).toHaveBeenCalledWith('/buffer/dir', false)
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
        {...handlers()}
      />,
    )
    fireEvent.click(screen.getByTestId('buffer-row'))
    expect(onSelect).toHaveBeenCalledWith('/buffer/note.md', false)
    expect(onOpen).not.toHaveBeenCalled()
  })

  it('a modifier (cmd/ctrl) click selects additively (codex B5)', () => {
    render(
      <StorageRow
        node={file()}
        depth={0}
        selected={false}
        expanded={false}
        {...handlers()}
      />,
    )
    fireEvent.click(screen.getByTestId('buffer-row'), { metaKey: true })
    expect(onSelect).toHaveBeenCalledWith('/buffer/note.md', true)
  })

  it('double-click opens a file (no caret rendered)', () => {
    render(
      <StorageRow
        node={file()}
        depth={0}
        selected={false}
        expanded={false}
        {...handlers()}
      />,
    )
    expect(screen.queryByTestId('buffer-caret')).toBeNull()
    fireEvent.doubleClick(screen.getByTestId('buffer-row'))
    expect(onOpen).toHaveBeenCalledWith('/buffer/note.md')
  })
})

describe('StorageRow keyboard parity (codex B6)', () => {
  it('Enter on a file opens it', () => {
    render(
      <StorageRow node={file()} depth={0} selected={false} expanded={false}
        {...handlers()} />,
    )
    fireEvent.keyDown(screen.getByTestId('buffer-row'), { key: 'Enter' })
    expect(onOpen).toHaveBeenCalledWith('/buffer/note.md')
    expect(onToggle).not.toHaveBeenCalled()
  })

  it('Enter on a folder toggles its expansion (never opens)', () => {
    renderFolder()
    fireEvent.keyDown(screen.getByTestId('buffer-row'), { key: 'Enter' })
    expect(onToggle).toHaveBeenCalledWith('/buffer/dir')
    expect(onOpen).not.toHaveBeenCalled()
  })

  it('Space selects the row (non-additive without a modifier)', () => {
    render(
      <StorageRow node={file()} depth={0} selected={false} expanded={false}
        {...handlers()} />,
    )
    fireEvent.keyDown(screen.getByTestId('buffer-row'), { key: ' ' })
    expect(onSelect).toHaveBeenCalledWith('/buffer/note.md', false)
  })

  it('Space with a modifier selects additively', () => {
    renderFolder()
    fireEvent.keyDown(screen.getByTestId('buffer-row'), { key: ' ', ctrlKey: true })
    expect(onSelect).toHaveBeenCalledWith('/buffer/dir', true)
  })
})

describe('StorageRow drop target dir (codex B1)', () => {
  it('a folder row publishes ITS OWN path as the drop targetDir', () => {
    renderFolder()
    expect(screen.getByTestId('buffer-row').getAttribute('data-target-dir')).toBe('/buffer/dir')
  })

  it('a file row publishes its PARENT dir as the drop targetDir (drop = into its folder, not root)', () => {
    render(
      <StorageRow node={file()} depth={0} selected={false} expanded={false}
        {...handlers()} />,
    )
    // file is /buffer/note.md → parent /buffer.
    expect(screen.getByTestId('buffer-row').getAttribute('data-target-dir')).toBe('/buffer')
  })

  it('a NESTED file row publishes its immediate parent folder, not the root', () => {
    const nested: TreeNode = { path: '/buffer/dir/sub/x.md', name: 'x.md', isDir: false, size: 2 }
    render(
      <StorageRow node={nested} depth={2} selected={false} expanded={false}
        {...handlers()} />,
    )
    expect(screen.getByTestId('buffer-row').getAttribute('data-target-dir')).toBe('/buffer/dir/sub')
  })
})

describe('StorageRow per-row action cluster (T4.1)', () => {
  it('a file row exposes Open / Rename / Delete, each targeting its own path', () => {
    render(<StorageRow node={file()} depth={0} selected={false} expanded={false} {...handlers()} />)
    const actions = screen.getByTestId('row-actions')
    fireEvent.click(within(actions).getByTestId('row-action-open'))
    expect(onOpen).toHaveBeenCalledWith('/buffer/note.md')
    fireEvent.click(within(actions).getByTestId('row-action-delete'))
    expect(onDelete).toHaveBeenCalledWith('/buffer/note.md')
    fireEvent.click(within(actions).getByTestId('row-action-rename'))
    // Rename hands back the button's own rect so the popover anchors to the row.
    expect(onRename).toHaveBeenCalledWith('/buffer/note.md', expect.anything())
  })

  it('a folder row has Rename / Delete but no Open', () => {
    render(<StorageRow node={folder()} depth={0} selected={false} expanded={false} {...handlers()} />)
    const actions = screen.getByTestId('row-actions')
    expect(within(actions).queryByTestId('row-action-open')).toBeNull()
    fireEvent.click(within(actions).getByTestId('row-action-delete'))
    expect(onDelete).toHaveBeenCalledWith('/buffer/dir')
  })

  it('the cluster is revealed by hover AND keyboard focus, and the row is a hover group', () => {
    render(<StorageRow node={file()} depth={0} selected={false} expanded={false} {...handlers()} />)
    const actions = screen.getByTestId('row-actions')
    expect(actions.className).toContain('group-hover:opacity-100')
    expect(actions.className).toContain('group-focus-within:opacity-100')
    expect(screen.getByTestId('buffer-row').className.split(/\s+/)).toContain('group')
  })

  it('REGRESSION: an action click/dblclick/keydown never reaches the row handlers', () => {
    render(<StorageRow node={file()} depth={0} selected={false} expanded={false} {...handlers()} />)
    const rename = screen.getByTestId('row-action-rename')
    fireEvent.click(rename)
    fireEvent.doubleClick(rename)
    fireEvent.keyDown(rename, { key: 'Enter' })
    fireEvent.keyDown(rename, { key: ' ' })
    expect(onSelect).not.toHaveBeenCalled()
    expect(onToggle).not.toHaveBeenCalled()
    expect(onOpen).not.toHaveBeenCalled()
  })
})

describe('StorageRow selection checkbox (T4.3)', () => {
  it('reflects the selected flag and toggles through onToggleSelect', () => {
    const { rerender } = render(
      <StorageRow node={file()} depth={0} selected={false} expanded={false} {...handlers()} />,
    )
    const box = screen.getByTestId('row-checkbox') as HTMLInputElement
    expect(box.checked).toBe(false)
    fireEvent.click(box)
    expect(onToggleSelect).toHaveBeenCalledWith('/buffer/note.md')

    rerender(<StorageRow node={file()} depth={0} selected expanded={false} {...handlers()} />)
    expect((screen.getByTestId('row-checkbox') as HTMLInputElement).checked).toBe(true)
  })

  it('REGRESSION: the checkbox gesture never reaches the row select / open handlers', () => {
    render(<StorageRow node={file()} depth={0} selected={false} expanded={false} {...handlers()} />)
    const box = screen.getByTestId('row-checkbox')
    fireEvent.click(box)
    fireEvent.doubleClick(box)
    fireEvent.keyDown(box, { key: ' ' })
    fireEvent.keyDown(box, { key: 'Enter' })
    expect(onSelect).not.toHaveBeenCalled()
    expect(onOpen).not.toHaveBeenCalled()
    expect(onToggle).not.toHaveBeenCalled()
  })

  it('a folder row carries a checkbox too (folders are selectable targets)', () => {
    render(
      <StorageRow node={folder()} depth={0} selected={false} expanded={false} {...handlers()} />,
    )
    fireEvent.click(screen.getByTestId('row-checkbox'))
    expect(onToggleSelect).toHaveBeenCalledWith('/buffer/dir')
    expect(onToggle).not.toHaveBeenCalled()
  })
})
