import { describe, it, expect, beforeEach } from 'vitest'
import { terminalLinkRegistry } from './registry'
import { registerBuiltinTerminalLinks, __resetBuiltinTerminalLinks } from './register'

describe('registerBuiltinTerminalLinks', () => {
  beforeEach(() => __resetBuiltinTerminalLinks())

  it('registers both url and file-path matchers', () => {
    registerBuiltinTerminalLinks({
      urlOpener: { isElectron: false, openBrowserTab: () => {}, openMiniWindow: () => {} },
      filePathOpener: {
        getDefaultOpener: () => null,
        openSingletonTab: () => 't',
        insertTab: () => {},
        getActiveWorkspaceId: () => null,
        computeInsertTarget: () => undefined,
        fetchPaneCwd: async (_h: string, _s: string, _sig?: AbortSignal) => '',
        fetchPaneHome: async (_h: string, _s: string, _sig?: AbortSignal) => '',
      },
    })
    const types = terminalLinkRegistry.getMatchers().map((m) => m.type)
    expect(types).toContain('url')
    expect(types).toContain('file')
  })

  it('is idempotent — double call does not double-register matchers or openers', () => {
    const deps = {
      urlOpener: { isElectron: false, openBrowserTab: () => {}, openMiniWindow: () => {} },
      filePathOpener: {
        getDefaultOpener: () => null,
        openSingletonTab: () => 't',
        insertTab: () => {},
        getActiveWorkspaceId: () => null,
        computeInsertTarget: () => undefined,
        fetchPaneCwd: async (_h: string, _s: string, _sig?: AbortSignal) => '',
        fetchPaneHome: async (_h: string, _s: string, _sig?: AbortSignal) => '',
      },
    }
    registerBuiltinTerminalLinks(deps)
    const firstCount = terminalLinkRegistry.getMatchers().length
    registerBuiltinTerminalLinks(deps)
    const ids = terminalLinkRegistry.getMatchers().map((m) => m.id)
    expect(ids.length).toBe(firstCount)
    expect(new Set(ids).size).toBe(ids.length)
    // Openers: dispatch a url token — should route to exactly one opener (built-in one)
    const urlToken = { type: 'url', text: 'https://x', range: { startCol: 0, endCol: 9 } }
    expect(terminalLinkRegistry.dispatch(urlToken, {}, new MouseEvent('click'))).toBe(true)
  })
})

describe('registerBuiltinTerminalLinks — 4 file-path matchers', () => {
  beforeEach(() => __resetBuiltinTerminalLinks())

  it('registers all 4 file-path matchers', () => {
    registerBuiltinTerminalLinks({
      urlOpener: { isElectron: false, openBrowserTab: () => {}, openMiniWindow: () => {} },
      filePathOpener: {
        getDefaultOpener: () => null,
        openSingletonTab: () => 'tab',
        insertTab: () => {},
        getActiveWorkspaceId: () => 'ws',
        computeInsertTarget: () => undefined,
        fetchPaneCwd: async () => '/cwd',
        fetchPaneHome: async () => '/home/user',
      },
    })
    const ids = terminalLinkRegistry.getMatchers().map((m) => m.id)
    expect(ids).toContain('builtin:file-path-absolute')
    expect(ids).toContain('builtin:file-path-tilde')
    expect(ids).toContain('builtin:file-path-relative-slash')
    expect(ids).toContain('builtin:file-path-bare')
  })

  // P3 codex round-1 finding: when Editor is disabled at bootstrap,
  // the migrated Editor settings UI is gated by module-enabled state,
  // so the three Editor-owned matchers must NOT register or the
  // runtime will detect paths the user has no way to toggle off.
  it('skips Editor-owned matchers when editorFilePathMatchersEnabled=false but keeps bare', () => {
    registerBuiltinTerminalLinks({
      urlOpener: { isElectron: false, openBrowserTab: () => {}, openMiniWindow: () => {} },
      filePathOpener: {
        getDefaultOpener: () => null,
        openSingletonTab: () => 'tab',
        insertTab: () => {},
        getActiveWorkspaceId: () => 'ws',
        computeInsertTarget: () => undefined,
        fetchPaneCwd: async () => '/cwd',
        fetchPaneHome: async () => '/home/user',
      },
      editorFilePathMatchersEnabled: false,
    })
    const ids = terminalLinkRegistry.getMatchers().map((m) => m.id)
    expect(ids).not.toContain('builtin:file-path-absolute')
    expect(ids).not.toContain('builtin:file-path-tilde')
    expect(ids).not.toContain('builtin:file-path-relative-slash')
    expect(ids).toContain('builtin:file-path-bare')
  })
})
