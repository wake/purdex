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
  const fetchPaneCwd = vi.fn(async () => '/home/user/proj')
  return { openSingletonTab, insertTab, getDefaultOpener, getActiveWorkspaceId, fetchPaneCwd, fakeOpener, paneContent }
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

  it('requires hostId in ctx to open', async () => {
    const deps = makeDeps()
    const o = createFilePathOpener(deps)
    await o.open(fileToken, {}, new MouseEvent('click'))
    expect(deps.getDefaultOpener).not.toHaveBeenCalled()
  })

  it('looks up FileOpener and opens singleton tab in active workspace', async () => {
    const deps = makeDeps()
    const o = createFilePathOpener(deps)
    await o.open(fileToken, { hostId: 'h1' }, new MouseEvent('click'))

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

  it('no-op when no FileOpener matches', async () => {
    const deps = makeDeps()
    deps.getDefaultOpener.mockReturnValue(null)
    const o = createFilePathOpener(deps)
    await o.open(fileToken, { hostId: 'h1' }, new MouseEvent('click'))
    expect(deps.openSingletonTab).not.toHaveBeenCalled()
  })

  it('no-op when no active workspace', async () => {
    const deps = makeDeps()
    deps.getActiveWorkspaceId.mockReturnValue(null)
    const o = createFilePathOpener(deps)
    await o.open(fileToken, { hostId: 'h1' }, new MouseEvent('click'))
    expect(deps.insertTab).not.toHaveBeenCalled()
  })

  it('no-op when direct open without meta.path (canOpen bypass)', async () => {
    const deps = makeDeps()
    const o = createFilePathOpener(deps)
    await o.open({ ...fileToken, meta: undefined }, { hostId: 'h1' }, new MouseEvent('click'))
    expect(deps.getDefaultOpener).not.toHaveBeenCalled()
  })
})

describe('file-path opener — relative path cwd resolution', () => {
  beforeEach(() => vi.restoreAllMocks())

  const relToken: LinkToken = {
    type: 'file',
    text: 'src/App.tsx',
    range: { startCol: 0, endCol: 11 },
    meta: { path: 'src/App.tsx' },
  }

  it('absolute path: does NOT fetch cwd', async () => {
    const deps = makeDeps()
    const o = createFilePathOpener(deps)
    await o.open(fileToken, { hostId: 'h1', sessionCode: 'c1' }, new MouseEvent('click'))
    expect(deps.fetchPaneCwd).not.toHaveBeenCalled()
  })

  it('relative path: fetches cwd and prepends', async () => {
    const deps = makeDeps()
    const o = createFilePathOpener(deps)
    await o.open(relToken, { hostId: 'h1', sessionCode: 'c1' }, new MouseEvent('click'))
    expect(deps.fetchPaneCwd).toHaveBeenCalledWith('h1', 'c1')
    // verify the path passed to FileOpener.createContent is the joined absolute path
    const createContentCalls = (deps.fakeOpener.createContent as ReturnType<typeof vi.fn>).mock.calls
    expect(createContentCalls[0][1].path).toBe('/home/user/proj/src/App.tsx')
  })

  it('relative path without sessionCode: no-op', async () => {
    const deps = makeDeps()
    const o = createFilePathOpener(deps)
    await o.open(relToken, { hostId: 'h1' }, new MouseEvent('click'))
    expect(deps.fetchPaneCwd).not.toHaveBeenCalled()
    expect(deps.openSingletonTab).not.toHaveBeenCalled()
  })

  it('fetchPaneCwd throws: no-op (does not crash)', async () => {
    const deps = makeDeps()
    deps.fetchPaneCwd.mockRejectedValueOnce(new Error('boom'))
    const o = createFilePathOpener(deps)
    await o.open(relToken, { hostId: 'h1', sessionCode: 'c1' }, new MouseEvent('click'))
    expect(deps.openSingletonTab).not.toHaveBeenCalled()
  })

  it('fetched cwd is not absolute: no-op', async () => {
    const deps = makeDeps()
    deps.fetchPaneCwd.mockResolvedValueOnce('not-absolute')
    const o = createFilePathOpener(deps)
    await o.open(relToken, { hostId: 'h1', sessionCode: 'c1' }, new MouseEvent('click'))
    expect(deps.openSingletonTab).not.toHaveBeenCalled()
  })

  it('cwd with trailing slash: no double-slash in joined path', async () => {
    const deps = makeDeps()
    deps.fetchPaneCwd.mockResolvedValueOnce('/home/user/proj/')
    const o = createFilePathOpener(deps)
    await o.open(relToken, { hostId: 'h1', sessionCode: 'c1' }, new MouseEvent('click'))
    const createContentCalls = (deps.fakeOpener.createContent as ReturnType<typeof vi.fn>).mock.calls
    expect(createContentCalls[0][1].path).toBe('/home/user/proj/src/App.tsx')
  })
})
