import { describe, it, expect, vi, beforeEach } from 'vitest'
import {
  createOpenFileService,
  FileNotFoundError,
  isNotFoundError,
  type OpenFileContext,
  type PopupSpec,
} from './open-file'
import { usePathCacheStore } from '../../stores/path-cache/usePathCacheStore'
import { useEditorSettingsStore, DEFAULT_EDITOR_SETTINGS } from '../../stores/useEditorSettingsStore'
import type { FileInfo, FileSource } from '../../types/fs'

const mockStatH1 = vi.fn()
const mockStatH2 = vi.fn()
const mockOpenInTab = vi.fn()
const mockShow = vi.fn<(spec: PopupSpec) => AbortController>(() => new AbortController())
const mockHide = vi.fn<() => void>()

const fsBackendFactory = (hostId: string) =>
  ({
    stat: hostId === 'h1' ? mockStatH1 : mockStatH2,
  } as unknown as { stat: (p: string) => Promise<unknown> })

const svc = createOpenFileService({
  fsBackendFactory,
  popupController: { show: mockShow, hide: mockHide },
  tabOpener: mockOpenInTab,
})

const file: FileInfo = {
  path: '/a/b/foo.go',
  name: 'foo.go',
  extension: 'go',
  size: 0,
  isDirectory: false,
}
const source: FileSource = { type: 'inapp' }
const ctx: OpenFileContext = {
  hostId: 'h1',
  cwd: '/a',
  sourceWorkspaceId: 'w1',
  sessionCode: 'sess1',
}

beforeEach(() => {
  // merge-mode reset (per feedback_zustand_harness_setstate) — explicit fields,
  // never replace-mode (would wipe actions).
  usePathCacheStore.setState({ entriesByScope: {} })
  useEditorSettingsStore.setState({
    ...DEFAULT_EDITOR_SETTINGS,
    popupOnMissingFile: true,
    autoSearchLayer1: true,
  })
  mockStatH1.mockReset()
  mockStatH2.mockReset()
  mockOpenInTab.mockReset()
  mockShow.mockReset()
  mockShow.mockImplementation(() => new AbortController())
  mockHide.mockReset()
})

describe('isNotFoundError', () => {
  it('matches ENOENT code and 404 status; rejects others', () => {
    expect(isNotFoundError(Object.assign(new Error('x'), { code: 'ENOENT' }))).toBe(true)
    expect(isNotFoundError(Object.assign(new Error('x'), { status: 404 }))).toBe(true)
    expect(isNotFoundError(Object.assign(new Error('x'), { status: 401 }))).toBe(false)
    expect(isNotFoundError(Object.assign(new Error('x'), { code: 'ENETDOWN' }))).toBe(false)
    expect(isNotFoundError(null)).toBe(false)
  })
})

describe('createOpenFileService', () => {
  it('opens directly when path exists', async () => {
    mockStatH1.mockResolvedValue({ isDirectory: false })
    await svc.tryOpenFile(file, source, ctx)
    expect(mockOpenInTab).toHaveBeenCalledWith(file, source, ctx)
    expect(mockShow).not.toHaveBeenCalled()
  })

  it('throws FileNotFoundError when popupOnMissingFile=off and file missing', async () => {
    useEditorSettingsStore.setState({ popupOnMissingFile: false })
    mockStatH1.mockRejectedValue(Object.assign(new Error('ENOENT'), { code: 'ENOENT' }))
    await expect(svc.tryOpenFile(file, source, ctx)).rejects.toBeInstanceOf(FileNotFoundError)
    expect(mockShow).not.toHaveBeenCalled()
  })

  it('cache 0 hits → ask-expand popup', async () => {
    mockStatH1.mockRejectedValue(Object.assign(new Error('ENOENT'), { code: 'ENOENT' }))
    await svc.tryOpenFile(file, source, ctx)
    expect(mockShow).toHaveBeenCalledTimes(1)
    expect(mockShow.mock.calls[0][0]).toMatchObject({ mode: 'ask-expand' })
  })

  it('cache 1 verified hit → tabOpener with patched path', async () => {
    usePathCacheStore.getState().add('h1', '/a', 'sess1', '/cached/dir')
    const enoent = Object.assign(new Error('ENOENT'), { code: 'ENOENT' })
    mockStatH1.mockImplementation(async (p: string) => {
      if (p === '/cached/dir/foo.go') return { isDirectory: false }
      throw enoent
    })
    await svc.tryOpenFile(file, source, ctx)
    expect(mockOpenInTab).toHaveBeenCalledWith({ ...file, path: '/cached/dir/foo.go' }, source, ctx)
    expect(mockShow).not.toHaveBeenCalled()
  })

  it('cache N>1 verified → layer1-multi popup', async () => {
    usePathCacheStore.getState().add('h1', '/a', 'sess1', '/cached/x')
    usePathCacheStore.getState().add('h1', '/a', 'sess1', '/cached/y')
    const enoent = Object.assign(new Error('ENOENT'), { code: 'ENOENT' })
    mockStatH1.mockImplementation(async (p: string) => {
      if (p === '/cached/x/foo.go' || p === '/cached/y/foo.go') return { isDirectory: false }
      throw enoent
    })
    await svc.tryOpenFile(file, source, ctx)
    expect(mockShow).toHaveBeenCalledTimes(1)
    const spec = mockShow.mock.calls[0][0]
    if (spec.mode !== 'layer1-multi') throw new Error(`expected layer1-multi spec, got ${spec.mode}`)
    expect(spec.hits).toEqual(expect.arrayContaining(['/cached/x/foo.go', '/cached/y/foo.go']))
  })

  it('cache stat ENOENT → pruneStaleCandidate, candidate dropped', async () => {
    usePathCacheStore.getState().add('h1', '/a', 'sess1', '/stale/dir')
    const enoent = Object.assign(new Error('ENOENT'), { code: 'ENOENT' })
    mockStatH1.mockRejectedValue(enoent)
    await svc.tryOpenFile(file, source, ctx)
    // After prune, the stale dir entry is removed (scope cleared when last
    // entry leaves). pruneStaleCandidate keys by (hostId, cwd) per Deviation 1.
    const after = usePathCacheStore.getState().lookup('h1', '/a', 'foo.go')
    expect(after).toEqual([])
    expect(mockShow).toHaveBeenCalledWith(expect.objectContaining({ mode: 'ask-expand' }))
  })

  it('stat 401 (auth) → re-throws original error, popup never opens', async () => {
    const authErr = Object.assign(new Error('unauthorized'), { status: 401 })
    mockStatH1.mockRejectedValue(authErr)
    await expect(svc.tryOpenFile(file, source, ctx)).rejects.toBe(authErr)
    expect(mockShow).not.toHaveBeenCalled()
  })

  it('stat network error → re-throws, popup never opens', async () => {
    const netErr = Object.assign(new Error('network down'), { code: 'ENETDOWN' })
    mockStatH1.mockRejectedValue(netErr)
    await expect(svc.tryOpenFile(file, source, ctx)).rejects.toBe(netErr)
    expect(mockShow).not.toHaveBeenCalled()
  })

  it('host-bound: backend factory called with ctx.hostId once; subsequent stats stay on h1 even if active host conceptually changes', async () => {
    // Cached entry on h1 only.
    usePathCacheStore.getState().add('h1', '/a', 'sess1', '/cached/dir')
    const enoent = Object.assign(new Error('ENOENT'), { code: 'ENOENT' })
    mockStatH1.mockImplementation(async (p: string) => {
      if (p === '/a/b/foo.go') throw enoent
      if (p === '/cached/dir/foo.go') return { isDirectory: false }
      throw enoent
    })
    // mockStatH2 should NEVER be called.
    mockStatH2.mockResolvedValue({ isDirectory: false })

    await svc.tryOpenFile(file, source, ctx)

    expect(mockStatH1).toHaveBeenCalled()
    expect(mockStatH2).not.toHaveBeenCalled()
    // Tab should open the cached path resolved on h1, not via h2.
    expect(mockOpenInTab).toHaveBeenCalledWith(
      { ...file, path: '/cached/dir/foo.go' },
      source,
      ctx,
    )
  })

  it('captures ctx fields at click time (does not re-read store mid-flow)', async () => {
    // The service receives ctx as a value; it must thread that exact ctx
    // through to tabOpener and never read host/workspace from a global store.
    mockStatH1.mockResolvedValue({ isDirectory: false })
    const customCtx: OpenFileContext = {
      hostId: 'h1',
      cwd: '/captured-cwd',
      sourceWorkspaceId: 'captured-ws',
      sessionCode: 'captured-sess',
    }
    await svc.tryOpenFile(file, source, customCtx)
    expect(mockOpenInTab).toHaveBeenCalledWith(file, source, customCtx)
  })
})
