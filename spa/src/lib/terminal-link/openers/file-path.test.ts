import { describe, it, expect, vi, beforeEach } from 'vitest'
import { createFilePathOpener } from './file-path'
import type { LinkToken } from '../types'
import type { FileOpener } from '../../file-opener-registry'

const fileToken: LinkToken = {
  type: 'file',
  text: '/a/b.ts',
  range: { startCol: 0, endCol: 7 },
  meta: { path: '/a/b.ts' },
}

function makeDeps() {
  const openSingletonTab = vi.fn(() => 'tab-1')
  const insertTab = vi.fn()
  const paneContent = { kind: 'editor', source: { type: 'daemon', hostId: 'h1' }, filePath: '/a/b.ts' }
  const fakeOpener: FileOpener = {
    id: 'fake', label: '', icon: 'File',
    match: () => true, priority: 'default',
    createContent: vi.fn(() => paneContent as never),
  }
  const getDefaultOpener = vi.fn(() => fakeOpener)
  const getActiveWorkspaceId = vi.fn(() => 'ws-1')
  return { openSingletonTab, insertTab, getDefaultOpener, getActiveWorkspaceId, fakeOpener, paneContent }
}

describe('file-path opener', () => {
  beforeEach(() => vi.restoreAllMocks())

  it('canOpen true for type file with path meta', () => {
    const deps = makeDeps()
    const o = createFilePathOpener(deps)
    expect(o.canOpen(fileToken)).toBe(true)
    expect(o.canOpen({ ...fileToken, type: 'url' })).toBe(false)
    expect(o.canOpen({ ...fileToken, meta: undefined })).toBe(false)
  })

  it('requires hostId in ctx to open', () => {
    const deps = makeDeps()
    const o = createFilePathOpener(deps)
    o.open(fileToken, {}, new MouseEvent('click'))
    expect(deps.getDefaultOpener).not.toHaveBeenCalled()
  })

  it('looks up FileOpener and opens singleton tab in active workspace', () => {
    const deps = makeDeps()
    const o = createFilePathOpener(deps)
    o.open(fileToken, { hostId: 'h1' }, new MouseEvent('click'))

    expect(deps.getDefaultOpener).toHaveBeenCalledWith(expect.objectContaining({
      name: 'b.ts',
      path: '/a/b.ts',
      extension: 'ts',
      isDirectory: false,
    }))
    expect(deps.fakeOpener.createContent).toHaveBeenCalledWith(
      { type: 'daemon', hostId: 'h1' },
      expect.objectContaining({ path: '/a/b.ts' }),
    )
    expect(deps.openSingletonTab).toHaveBeenCalledWith(deps.paneContent)
    expect(deps.insertTab).toHaveBeenCalledWith('tab-1', 'ws-1')
  })

  it('no-op when no FileOpener matches', () => {
    const deps = makeDeps()
    deps.getDefaultOpener.mockReturnValue(null)
    const o = createFilePathOpener(deps)
    o.open(fileToken, { hostId: 'h1' }, new MouseEvent('click'))
    expect(deps.openSingletonTab).not.toHaveBeenCalled()
  })

  it('no-op when no active workspace', () => {
    const deps = makeDeps()
    deps.getActiveWorkspaceId.mockReturnValue(null)
    const o = createFilePathOpener(deps)
    o.open(fileToken, { hostId: 'h1' }, new MouseEvent('click'))
    expect(deps.insertTab).not.toHaveBeenCalled()
  })

  it('no-op when direct open without meta.path (canOpen bypass)', () => {
    const deps = makeDeps()
    const o = createFilePathOpener(deps)
    o.open({ ...fileToken, meta: undefined }, { hostId: 'h1' }, new MouseEvent('click'))
    expect(deps.getDefaultOpener).not.toHaveBeenCalled()
  })
})
