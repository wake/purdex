import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { createFilePathOpener } from './file-path'
import type { LinkToken } from '../types'
import type { FileOpener } from '../../file-opener-registry'
import { useHostSettingsStore } from '../../../stores/useHostSettingsStore'
import { useWorkspaceSettingsStore } from '../../../stores/useWorkspaceSettingsStore'

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
  const getDefaultOpener = vi.fn((): FileOpener | null => fakeOpener)
  const getActiveWorkspaceId = vi.fn((): string | null => 'ws-1')
  const fetchPaneCwd = vi.fn(async (_h: string, _s: string, _sig?: AbortSignal) => '/home/user/proj')
  const fetchPaneHome = vi.fn(async (_h: string, _s: string, _sig?: AbortSignal) => '/home/user')
  return { openSingletonTab, insertTab, getDefaultOpener, getActiveWorkspaceId, fetchPaneCwd, fetchPaneHome, fakeOpener, paneContent }
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
    expect(deps.fetchPaneCwd).toHaveBeenCalledWith('h1', 'c1', expect.any(AbortSignal))
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

  it('fetchPaneCwd throws: no-op and warns with context', async () => {
    const deps = makeDeps()
    deps.fetchPaneCwd.mockRejectedValueOnce(new Error('boom'))
    const warnSpy = vi.spyOn(console, 'warn').mockImplementation(() => {})
    const o = createFilePathOpener(deps)
    await o.open(relToken, { hostId: 'h1', sessionCode: 'c1' }, new MouseEvent('click'))
    expect(deps.openSingletonTab).not.toHaveBeenCalled()
    expect(warnSpy).toHaveBeenCalledWith(expect.stringContaining('boom'))
    warnSpy.mockRestore()
  })

  it('fetched cwd is not absolute: no-op and warns', async () => {
    const deps = makeDeps()
    deps.fetchPaneCwd.mockResolvedValueOnce('not-absolute')
    const warnSpy = vi.spyOn(console, 'warn').mockImplementation(() => {})
    const o = createFilePathOpener(deps)
    await o.open(relToken, { hostId: 'h1', sessionCode: 'c1' }, new MouseEvent('click'))
    expect(deps.openSingletonTab).not.toHaveBeenCalled()
    expect(warnSpy).toHaveBeenCalledWith(expect.stringContaining('cwd not absolute'))
    warnSpy.mockRestore()
  })

  it('cwd with trailing slash: no double-slash in joined path', async () => {
    const deps = makeDeps()
    deps.fetchPaneCwd.mockResolvedValueOnce('/home/user/proj/')
    const o = createFilePathOpener(deps)
    await o.open(relToken, { hostId: 'h1', sessionCode: 'c1' }, new MouseEvent('click'))
    const createContentCalls = (deps.fakeOpener.createContent as ReturnType<typeof vi.fn>).mock.calls
    expect(createContentCalls[0][1].path).toBe('/home/user/proj/src/App.tsx')
  })

  it('relative path with .. escaping cwd: no-op and warn', async () => {
    const deps = makeDeps()
    const o = createFilePathOpener(deps)
    const warnSpy = vi.spyOn(console, 'warn').mockImplementation(() => {})

    const traversalToken: LinkToken = {
      type: 'file',
      text: '../../../etc/passwd',
      range: { startCol: 0, endCol: 19 },
      meta: { path: '../../../etc/passwd' },
    }
    await o.open(traversalToken, { hostId: 'h1', sessionCode: 'c1' }, new MouseEvent('click'))
    expect(deps.openSingletonTab).not.toHaveBeenCalled()
    expect(warnSpy).toHaveBeenCalled()
    warnSpy.mockRestore()
  })

  it('relative path with single ".." inside cwd: resolves correctly', async () => {
    const deps = makeDeps()
    deps.fetchPaneCwd.mockResolvedValueOnce('/home/user/proj/sub')
    const o = createFilePathOpener(deps)
    const token: LinkToken = {
      type: 'file',
      text: '../App.tsx',
      range: { startCol: 0, endCol: 10 },
      meta: { path: '../App.tsx' },
    }
    await o.open(token, { hostId: 'h1', sessionCode: 'c1' }, new MouseEvent('click'))
    const createContentCalls = (deps.fakeOpener.createContent as ReturnType<typeof vi.fn>).mock.calls
    expect(createContentCalls[0][1].path).toBe('/home/user/proj/App.tsx')
  })

  it('normalizes ./ and redundant slashes', async () => {
    const deps = makeDeps()
    deps.fetchPaneCwd.mockResolvedValueOnce('/home/user/proj')
    const o = createFilePathOpener(deps)
    const token: LinkToken = {
      type: 'file',
      text: './src/./App.tsx',
      range: { startCol: 0, endCol: 15 },
      meta: { path: './src/./App.tsx' },
    }
    await o.open(token, { hostId: 'h1', sessionCode: 'c1' }, new MouseEvent('click'))
    const createContentCalls = (deps.fakeOpener.createContent as ReturnType<typeof vi.fn>).mock.calls
    expect(createContentCalls[0][1].path).toBe('/home/user/proj/src/App.tsx')
  })

  it('fetchPaneCwd hanging: aborts after 5s timeout and warns', async () => {
    vi.useFakeTimers()
    const deps = makeDeps()
    let signalFromCall: AbortSignal | undefined
    deps.fetchPaneCwd.mockImplementation((_h, _s, signal) => {
      signalFromCall = signal
      return new Promise((_, reject) => {
        signal?.addEventListener('abort', () => {
          const err = new Error('aborted')
          err.name = 'AbortError'
          reject(err)
        })
      })
    })
    const warnSpy = vi.spyOn(console, 'warn').mockImplementation(() => {})
    const o = createFilePathOpener(deps)

    const relToken: LinkToken = {
      type: 'file',
      text: 'src/App.tsx',
      range: { startCol: 0, endCol: 11 },
      meta: { path: 'src/App.tsx' },
    }

    const openPromise = o.open(relToken, { hostId: 'h1', sessionCode: 'c1' }, new MouseEvent('click'))
    await vi.advanceTimersByTimeAsync(5001)
    await openPromise

    expect(signalFromCall?.aborted).toBe(true)
    expect(warnSpy).toHaveBeenCalledWith(expect.stringContaining('timeout'))
    expect(deps.openSingletonTab).not.toHaveBeenCalled()

    warnSpy.mockRestore()
    vi.useRealTimers()
  })
})

describe('file-path opener — tilde path home resolution', () => {
  beforeEach(() => vi.restoreAllMocks())
  afterEach(() => vi.useRealTimers())

  const tildeToken: LinkToken = {
    type: 'file',
    text: '~/foo.ts',
    range: { startCol: 0, endCol: 8 },
    meta: { path: '~/foo.ts' },
  }

  it('tilde path: fetches home and expands to absolute path', async () => {
    const deps = makeDeps()
    deps.fetchPaneHome.mockResolvedValueOnce('/Users/wake')
    const o = createFilePathOpener(deps)
    await o.open(tildeToken, { hostId: 'h1', sessionCode: 'c1' }, new MouseEvent('click'))

    expect(deps.fetchPaneHome).toHaveBeenCalledWith('h1', 'c1', expect.any(AbortSignal))
    expect(deps.fetchPaneCwd).not.toHaveBeenCalled()
    const createContentCalls = (deps.fakeOpener.createContent as ReturnType<typeof vi.fn>).mock.calls
    expect(createContentCalls[0][1].path).toBe('/Users/wake/foo.ts')
  })

  it('home with trailing slash: trims before expansion', async () => {
    const deps = makeDeps()
    deps.fetchPaneHome.mockResolvedValueOnce('/Users/wake/')
    const o = createFilePathOpener(deps)
    await o.open(tildeToken, { hostId: 'h1', sessionCode: 'c1' }, new MouseEvent('click'))

    const createContentCalls = (deps.fakeOpener.createContent as ReturnType<typeof vi.fn>).mock.calls
    expect(createContentCalls[0][1].path).toBe('/Users/wake/foo.ts')
  })

  it('fetchPaneHome throws: falls through with raw path so editor opens a new buffer', async () => {
    const deps = makeDeps()
    deps.fetchPaneHome.mockRejectedValueOnce(new Error('boom'))
    const warnSpy = vi.spyOn(console, 'warn').mockImplementation(() => {})
    const o = createFilePathOpener(deps)

    await o.open(tildeToken, { hostId: 'h1', sessionCode: 'c1' }, new MouseEvent('click'))

    expect(warnSpy).toHaveBeenCalledWith(expect.stringContaining('boom'))
    const createContentCalls = (deps.fakeOpener.createContent as ReturnType<typeof vi.fn>).mock.calls
    expect(createContentCalls[0][1].path).toBe('~/foo.ts')
    expect(deps.openSingletonTab).toHaveBeenCalled()
    warnSpy.mockRestore()
  })

  it('fetchPaneHome hanging: aborts after 5s timeout and still opens as new buffer', async () => {
    vi.useFakeTimers()
    const deps = makeDeps()
    let signalFromCall: AbortSignal | undefined
    deps.fetchPaneHome.mockImplementation((_h, _s, signal) => {
      signalFromCall = signal
      return new Promise((_, reject) => {
        signal?.addEventListener('abort', () => {
          const err = new Error('aborted')
          err.name = 'AbortError'
          reject(err)
        })
      })
    })
    const warnSpy = vi.spyOn(console, 'warn').mockImplementation(() => {})
    const o = createFilePathOpener(deps)

    const openPromise = o.open(tildeToken, { hostId: 'h1', sessionCode: 'c1' }, new MouseEvent('click'))
    await vi.advanceTimersByTimeAsync(5001)
    await openPromise

    expect(signalFromCall?.aborted).toBe(true)
    expect(warnSpy).toHaveBeenCalledWith(expect.stringContaining('timeout'))
    const createContentCalls = (deps.fakeOpener.createContent as ReturnType<typeof vi.fn>).mock.calls
    expect(createContentCalls[0][1].path).toBe('~/foo.ts')
    expect(deps.openSingletonTab).toHaveBeenCalled()

    warnSpy.mockRestore()
  })

  it('home not absolute: falls through with raw path', async () => {
    const deps = makeDeps()
    deps.fetchPaneHome.mockResolvedValueOnce('not-a-path')
    const o = createFilePathOpener(deps)

    await o.open(tildeToken, { hostId: 'h1', sessionCode: 'c1' }, new MouseEvent('click'))

    const createContentCalls = (deps.fakeOpener.createContent as ReturnType<typeof vi.fn>).mock.calls
    expect(createContentCalls[0][1].path).toBe('~/foo.ts')
  })

  it('~/./foo.ts: collapses `.` segment so duplicate tabs are avoided', async () => {
    const deps = makeDeps()
    deps.fetchPaneHome.mockResolvedValueOnce('/Users/wake')
    const o = createFilePathOpener(deps)
    const dotToken: LinkToken = {
      type: 'file',
      text: '~/./foo.ts',
      range: { startCol: 0, endCol: 10 },
      meta: { path: '~/./foo.ts' },
    }

    await o.open(dotToken, { hostId: 'h1', sessionCode: 'c1' }, new MouseEvent('click'))

    const createContentCalls = (deps.fakeOpener.createContent as ReturnType<typeof vi.fn>).mock.calls
    expect(createContentCalls[0][1].path).toBe('/Users/wake/foo.ts')
  })

  it('~/../foo.ts: collapses `..` (explicit: tilde path does not fence in $HOME)', async () => {
    const deps = makeDeps()
    deps.fetchPaneHome.mockResolvedValueOnce('/Users/wake')
    const o = createFilePathOpener(deps)
    const upToken: LinkToken = {
      type: 'file',
      text: '~/../foo.ts',
      range: { startCol: 0, endCol: 11 },
      meta: { path: '~/../foo.ts' },
    }

    await o.open(upToken, { hostId: 'h1', sessionCode: 'c1' }, new MouseEvent('click'))

    const createContentCalls = (deps.fakeOpener.createContent as ReturnType<typeof vi.fn>).mock.calls
    expect(createContentCalls[0][1].path).toBe('/Users/foo.ts')
  })
})

describe('file-path opener — tilde path layered resolve (PR-5)', () => {
  beforeEach(() => {
    vi.restoreAllMocks()
    localStorage.clear()
    useHostSettingsStore.setState({ hosts: {} })
    useWorkspaceSettingsStore.setState({ workspaces: {} })
  })
  afterEach(() => vi.useRealTimers())

  const tildeToken: LinkToken = {
    type: 'file',
    text: '~/foo.ts',
    range: { startCol: 0, endCol: 8 },
    meta: { path: '~/foo.ts' },
  }

  it('workspace override: uses workspace homePath over host and pane shell', async () => {
    useWorkspaceSettingsStore.getState().set('wsA', 'editor', { homePath: '/Users/x' })
    useHostSettingsStore.getState().set('h1', 'editor', { homePath: '/Users/host' })
    const deps = makeDeps()
    deps.fetchPaneHome.mockResolvedValueOnce('/Users/shell')
    const o = createFilePathOpener(deps)

    await o.open(tildeToken, { hostId: 'h1', sessionCode: 'c1', workspaceId: 'wsA' }, new MouseEvent('click'))

    const createContentCalls = (deps.fakeOpener.createContent as ReturnType<typeof vi.fn>).mock.calls
    expect(createContentCalls[0][1].path).toBe('/Users/x/foo.ts')
    expect(deps.fetchPaneHome).not.toHaveBeenCalled()
  })

  it('host override: uses host homePath when workspace empty', async () => {
    useHostSettingsStore.getState().set('h1', 'editor', { homePath: '/home/y' })
    const deps = makeDeps()
    deps.fetchPaneHome.mockResolvedValueOnce('/Users/shell')
    const o = createFilePathOpener(deps)

    await o.open(tildeToken, { hostId: 'h1', sessionCode: 'c1', workspaceId: 'wsA' }, new MouseEvent('click'))

    const createContentCalls = (deps.fakeOpener.createContent as ReturnType<typeof vi.fn>).mock.calls
    expect(createContentCalls[0][1].path).toBe('/home/y/foo.ts')
    expect(deps.fetchPaneHome).not.toHaveBeenCalled()
  })

  it('all layers empty + fetchPaneHome rejects: opens raw ~/foo.ts as new buffer', async () => {
    const deps = makeDeps()
    deps.fetchPaneHome.mockRejectedValueOnce(new Error('boom'))
    const o = createFilePathOpener(deps)

    await o.open(tildeToken, { hostId: 'h1', sessionCode: 'c1', workspaceId: 'wsA' }, new MouseEvent('click'))

    const createContentCalls = (deps.fakeOpener.createContent as ReturnType<typeof vi.fn>).mock.calls
    expect(createContentCalls[0][1].path).toBe('~/foo.ts')
    expect(deps.openSingletonTab).toHaveBeenCalled()
  })

  it('multi-workspace: reads link-source workspace (wsB), not active (wsA)', async () => {
    useWorkspaceSettingsStore.getState().set('wsA', 'editor', { homePath: '/Users/x' })
    useHostSettingsStore.getState().set('h1', 'editor', { homePath: '/home/host' })
    const deps = makeDeps()
    const o = createFilePathOpener(deps)

    await o.open(tildeToken, { hostId: 'h1', sessionCode: 'c1', workspaceId: 'wsB' }, new MouseEvent('click'))

    const createContentCalls = (deps.fakeOpener.createContent as ReturnType<typeof vi.fn>).mock.calls
    expect(createContentCalls[0][1].path).toBe('/home/host/foo.ts')
    expect(deps.fetchPaneHome).not.toHaveBeenCalled()
  })

  it('standalone pane (workspaceId undefined): skips workspace layer', async () => {
    useWorkspaceSettingsStore.getState().set('wsA', 'editor', { homePath: '/Users/x' })
    useHostSettingsStore.getState().set('h1', 'editor', { homePath: '/home/host' })
    const deps = makeDeps()
    const o = createFilePathOpener(deps)

    await o.open(tildeToken, { hostId: 'h1', sessionCode: 'c1' }, new MouseEvent('click'))

    const createContentCalls = (deps.fakeOpener.createContent as ReturnType<typeof vi.fn>).mock.calls
    expect(createContentCalls[0][1].path).toBe('/home/host/foo.ts')
    expect(deps.fetchPaneHome).not.toHaveBeenCalled()
  })

  it('fallback to fetchPaneHome still works (PR #530 regression)', async () => {
    const deps = makeDeps()
    deps.fetchPaneHome.mockResolvedValueOnce('/Users/wake')
    const o = createFilePathOpener(deps)

    await o.open(tildeToken, { hostId: 'h1', sessionCode: 'c1', workspaceId: 'wsA' }, new MouseEvent('click'))

    const createContentCalls = (deps.fakeOpener.createContent as ReturnType<typeof vi.fn>).mock.calls
    expect(createContentCalls[0][1].path).toBe('/Users/wake/foo.ts')
  })
})
