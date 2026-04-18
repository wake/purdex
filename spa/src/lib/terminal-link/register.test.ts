import { describe, it, expect, beforeEach } from 'vitest'
import { terminalLinkRegistry } from './registry'
import { registerBuiltinTerminalLinks, __resetBuiltinTerminalLinks } from './register'

describe('registerBuiltinTerminalLinks', () => {
  beforeEach(() => __resetBuiltinTerminalLinks())

  it('registers both url and file-path matchers', () => {
    registerBuiltinTerminalLinks({
      isElectron: false,
      openBrowserTab: () => {},
      openMiniWindow: () => {},
      getDefaultFileOpener: () => null,
      openSingletonTab: () => 't',
      insertTab: () => {},
      getActiveWorkspaceId: () => null,
    })
    const types = terminalLinkRegistry.getMatchers().map((m) => m.type)
    expect(types).toContain('url')
    expect(types).toContain('file')
  })

  it('is idempotent — double call does not double-register matchers or openers', () => {
    const deps = {
      isElectron: false,
      openBrowserTab: () => {},
      openMiniWindow: () => {},
      getDefaultFileOpener: () => null,
      openSingletonTab: () => 't',
      insertTab: () => {},
      getActiveWorkspaceId: () => null,
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
