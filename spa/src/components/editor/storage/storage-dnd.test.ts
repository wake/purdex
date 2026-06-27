import { describe, it, expect } from 'vitest'
import { computeMoveFromDragEnd } from './storage-dnd'

// The resolver reads the drop target's directory from `over.data.current.targetDir`
// (codex B1) — the authoritative value each droppable publishes — rather than
// inferring it from the over-id. A folder row publishes its own path; a file row
// publishes its PARENT dir; the root region publishes STORAGE_ROOT.
describe('computeMoveFromDragEnd (authoritative over.data.targetDir — B1 / H3)', () => {
  it('maps a file dropped on a folder to {from, targetDir=folder path}', () => {
    expect(
      computeMoveFromDragEnd(
        { id: '/buffer/a.md' },
        { id: '/buffer/dir', data: { current: { targetDir: '/buffer/dir' } } },
      ),
    ).toEqual({ from: '/buffer/a.md', targetDir: '/buffer/dir' })
  })

  it('B1: a file dropped on ANOTHER FILE row targets that file\'s PARENT dir (not root)', () => {
    // The file row at /buffer/dir/b.md publishes its parent /buffer/dir as the
    // drop target, so dropping a.md onto it moves a.md INTO /buffer/dir — it must
    // NOT fall through to the storage root.
    expect(
      computeMoveFromDragEnd(
        { id: '/buffer/a.md' },
        { id: '/buffer/dir/b.md', data: { current: { targetDir: '/buffer/dir' } } },
      ),
    ).toEqual({ from: '/buffer/a.md', targetDir: '/buffer/dir' })
  })

  it('B1: dropping a file onto a SAME-DIR sibling resolves to their shared parent (move is a no-op downstream)', () => {
    // Both files live in /buffer; the sibling publishes /buffer as targetDir, so
    // the resolver returns targetDir === the source's own parent — `moveStorageEntry`
    // then collapses it to a no-op (targetDir === parentOf(from)).
    expect(
      computeMoveFromDragEnd(
        { id: '/buffer/a.md' },
        { id: '/buffer/b.md', data: { current: { targetDir: '/buffer' } } },
      ),
    ).toEqual({ from: '/buffer/a.md', targetDir: '/buffer' })
  })

  it('maps the root region to a STORAGE_ROOT targetDir', () => {
    expect(
      computeMoveFromDragEnd(
        { id: '/buffer/dir/a.md' },
        { id: '/buffer', data: { current: { targetDir: '/buffer' } } },
      ),
    ).toEqual({ from: '/buffer/dir/a.md', targetDir: '/buffer' })
  })

  it('returns null when there is no drop target (dropped on empty space)', () => {
    expect(computeMoveFromDragEnd({ id: '/buffer/a.md' }, null)).toBeNull()
  })

  it('returns null when the drop target publishes no targetDir', () => {
    expect(computeMoveFromDragEnd({ id: '/buffer/a.md' }, { id: '/buffer/dir' })).toBeNull()
  })
})
