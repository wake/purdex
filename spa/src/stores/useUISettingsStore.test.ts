import { describe, it, expect, beforeEach } from 'vitest'
import {
  useUISettingsStore,
  KEEPALIVE_MAX_WEBGL,
  KEEPALIVE_MAX_DOM,
  clampKeepAlive,
  HOST_COLOR_LINE_WIDTH_MIN,
  HOST_COLOR_LINE_WIDTH_MAX,
  clampHostColorLineWidth,
  isHostColorMarkStyle,
} from './useUISettingsStore'
import { STORAGE_KEYS } from '../lib/storage'

describe('useUISettingsStore', () => {
  beforeEach(() => {
    localStorage.clear()
    useUISettingsStore.setState({
      terminalRevealDelay: 300,
      terminalRenderer: 'webgl',
      keepAliveCount: 0,
      keepAlivePinned: false,
      terminalSettingsVersion: 0,
    })
  })

  it('defaults terminalRenderer to webgl', () => {
    expect(useUISettingsStore.getState().terminalRenderer).toBe('webgl')
  })

  it('can set terminalRenderer to dom', () => {
    useUISettingsStore.getState().setTerminalRenderer('dom')
    expect(useUISettingsStore.getState().terminalRenderer).toBe('dom')
  })

  it('persists terminalRenderer across setState', () => {
    useUISettingsStore.getState().setTerminalRenderer('dom')
    useUISettingsStore.getState().setTerminalRenderer('webgl')
    expect(useUISettingsStore.getState().terminalRenderer).toBe('webgl')
  })
})

describe('keep-alive settings', () => {
  beforeEach(() => {
    useUISettingsStore.setState({
      terminalRevealDelay: 300,
      terminalRenderer: 'webgl',
      keepAliveCount: 0,
      keepAlivePinned: false,
      terminalSettingsVersion: 0,
    })
  })

  it('keepAliveCount defaults to 0', () => {
    expect(useUISettingsStore.getState().keepAliveCount).toBe(0)
  })

  it('keepAlivePinned defaults to false', () => {
    expect(useUISettingsStore.getState().keepAlivePinned).toBe(false)
  })

  it('setKeepAliveCount updates value', () => {
    useUISettingsStore.getState().setKeepAliveCount(3)
    expect(useUISettingsStore.getState().keepAliveCount).toBe(3)
  })

  it('setKeepAlivePinned updates value', () => {
    useUISettingsStore.getState().setKeepAlivePinned(true)
    expect(useUISettingsStore.getState().keepAlivePinned).toBe(true)
  })
})

describe('clampKeepAlive', () => {
  it('clamps webgl count to KEEPALIVE_MAX_WEBGL', () => {
    expect(clampKeepAlive('webgl', 8)).toBe(KEEPALIVE_MAX_WEBGL)
  })

  it('does not clamp webgl count within limit', () => {
    expect(clampKeepAlive('webgl', 4)).toBe(4)
  })

  it('clamps webgl count at exact boundary', () => {
    expect(clampKeepAlive('webgl', KEEPALIVE_MAX_WEBGL)).toBe(KEEPALIVE_MAX_WEBGL)
  })

  it('clamps dom count to KEEPALIVE_MAX_DOM', () => {
    expect(clampKeepAlive('dom', 15)).toBe(KEEPALIVE_MAX_DOM)
  })

  it('does not clamp dom count within limit', () => {
    expect(clampKeepAlive('dom', 7)).toBe(7)
  })

  it('does not reduce zero', () => {
    expect(clampKeepAlive('webgl', 0)).toBe(0)
    expect(clampKeepAlive('dom', 0)).toBe(0)
  })
})

describe('onRehydrateStorage clamps keepAliveCount', () => {
  it('clamps keepAliveCount when webgl and count exceeds limit', () => {
    useUISettingsStore.setState({
      terminalRenderer: 'webgl',
      keepAliveCount: 8,
    })
    // Simulate what onRehydrateStorage does
    const state = useUISettingsStore.getState()
    if (state.terminalRenderer === 'webgl' && state.keepAliveCount > KEEPALIVE_MAX_WEBGL) {
      useUISettingsStore.setState({ keepAliveCount: KEEPALIVE_MAX_WEBGL })
    }
    expect(useUISettingsStore.getState().keepAliveCount).toBe(KEEPALIVE_MAX_WEBGL)
  })

  it('does not clamp keepAliveCount when dom renderer with high count', () => {
    useUISettingsStore.setState({
      terminalRenderer: 'dom',
      keepAliveCount: 8,
    })
    const state = useUISettingsStore.getState()
    if (state.terminalRenderer === 'webgl' && state.keepAliveCount > KEEPALIVE_MAX_WEBGL) {
      useUISettingsStore.setState({ keepAliveCount: KEEPALIVE_MAX_WEBGL })
    }
    expect(useUISettingsStore.getState().keepAliveCount).toBe(8)
  })

  it('does not clamp keepAliveCount when webgl and count is within limit', () => {
    useUISettingsStore.setState({
      terminalRenderer: 'webgl',
      keepAliveCount: 4,
    })
    const state = useUISettingsStore.getState()
    if (state.terminalRenderer === 'webgl' && state.keepAliveCount > KEEPALIVE_MAX_WEBGL) {
      useUISettingsStore.setState({ keepAliveCount: KEEPALIVE_MAX_WEBGL })
    }
    expect(useUISettingsStore.getState().keepAliveCount).toBe(4)
  })
})

describe('useUISettingsStore — tab/icon preferences', () => {
  beforeEach(() => {
    useUISettingsStore.setState({
      tabIndicatorStyle: 'badge',
      ccIconVariant: 'bot',
      codexIconVariant: 'openai',
      dynamicTabName: false,
      showAgentTitleInStatusBar: false,
    })
  })

  it('defaults tabIndicatorStyle to badge', () => {
    expect(useUISettingsStore.getState().tabIndicatorStyle).toBe('badge')
  })

  it('setTabIndicatorStyle updates the value', () => {
    useUISettingsStore.getState().setTabIndicatorStyle('dot')
    expect(useUISettingsStore.getState().tabIndicatorStyle).toBe('dot')
    useUISettingsStore.getState().setTabIndicatorStyle('icon')
    expect(useUISettingsStore.getState().tabIndicatorStyle).toBe('icon')
  })

  it('defaults ccIconVariant to bot', () => {
    expect(useUISettingsStore.getState().ccIconVariant).toBe('bot')
  })

  it('setCcIconVariant updates the value', () => {
    useUISettingsStore.getState().setCcIconVariant('star')
    expect(useUISettingsStore.getState().ccIconVariant).toBe('star')
    useUISettingsStore.getState().setCcIconVariant('bot')
    expect(useUISettingsStore.getState().ccIconVariant).toBe('bot')
  })

  it('defaults codexIconVariant to openai', () => {
    expect(useUISettingsStore.getState().codexIconVariant).toBe('openai')
  })

  it('setCodexIconVariant updates the value', () => {
    useUISettingsStore.getState().setCodexIconVariant('codex')
    expect(useUISettingsStore.getState().codexIconVariant).toBe('codex')
    useUISettingsStore.getState().setCodexIconVariant('openai')
    expect(useUISettingsStore.getState().codexIconVariant).toBe('openai')
  })

  it('dynamicTabName toggles the flag', () => {
    expect(useUISettingsStore.getState().dynamicTabName).toBe(false)
    useUISettingsStore.getState().setDynamicTabName(true)
    expect(useUISettingsStore.getState().dynamicTabName).toBe(true)
    useUISettingsStore.getState().setDynamicTabName(false)
    expect(useUISettingsStore.getState().dynamicTabName).toBe(false)
  })

  it('showAgentTitleInStatusBar toggles the flag', () => {
    expect(useUISettingsStore.getState().showAgentTitleInStatusBar).toBe(false)
    useUISettingsStore.getState().setShowAgentTitleInStatusBar(true)
    expect(useUISettingsStore.getState().showAgentTitleInStatusBar).toBe(true)
    useUISettingsStore.getState().setShowAgentTitleInStatusBar(false)
    expect(useUISettingsStore.getState().showAgentTitleInStatusBar).toBe(false)
  })

  it('migration maps old showOscTitle to both new flags', async () => {
    localStorage.setItem(
      STORAGE_KEYS.UI_SETTINGS,
      JSON.stringify({
        state: { showOscTitle: true, terminalRenderer: 'webgl', keepAliveCount: 0 },
        version: 2,
      }),
    )
    await useUISettingsStore.persist.rehydrate()
    expect(useUISettingsStore.getState().dynamicTabName).toBe(true)
    expect(useUISettingsStore.getState().showAgentTitleInStatusBar).toBe(true)
  })
})

describe('KEEPALIVE constants', () => {
  it('KEEPALIVE_MAX_WEBGL is 6', () => {
    expect(KEEPALIVE_MAX_WEBGL).toBe(6)
  })

  it('KEEPALIVE_MAX_DOM is 10', () => {
    expect(KEEPALIVE_MAX_DOM).toBe(10)
  })
})

describe('terminalSettingsVersion', () => {
  beforeEach(() => {
    useUISettingsStore.setState({ terminalSettingsVersion: 0 })
  })

  it('defaults to 0', () => {
    expect(useUISettingsStore.getState().terminalSettingsVersion).toBe(0)
  })

  it('bumpTerminalSettingsVersion increments', () => {
    useUISettingsStore.getState().bumpTerminalSettingsVersion()
    expect(useUISettingsStore.getState().terminalSettingsVersion).toBe(1)
    useUISettingsStore.getState().bumpTerminalSettingsVersion()
    expect(useUISettingsStore.getState().terminalSettingsVersion).toBe(2)
  })
})

describe('host color mark settings', () => {
  beforeEach(() => {
    useUISettingsStore.setState({
      hostColorSidebarStyle: 'gradient',
      hostColorSidebarWidth: 2,
      hostColorTabBarStyle: 'bottom-line',
      hostColorTabBarWidth: 2,
    })
  })

  it('defaults: sidebar gradient, tab bar bottom-line, widths 2', () => {
    const s = useUISettingsStore.getInitialState()
    expect(s.hostColorSidebarStyle).toBe('gradient')
    expect(s.hostColorTabBarStyle).toBe('bottom-line')
    expect(s.hostColorSidebarWidth).toBe(2)
    expect(s.hostColorTabBarWidth).toBe(2)
  })

  it('constants are 1 and 6', () => {
    expect(HOST_COLOR_LINE_WIDTH_MIN).toBe(1)
    expect(HOST_COLOR_LINE_WIDTH_MAX).toBe(6)
  })

  it('clampHostColorLineWidth: 0→1, 9→6, 2.6→3, NaN→2', () => {
    expect(clampHostColorLineWidth(0)).toBe(1)
    expect(clampHostColorLineWidth(9)).toBe(6)
    expect(clampHostColorLineWidth(2.6)).toBe(3)
    expect(clampHostColorLineWidth(NaN)).toBe(2)
    expect(clampHostColorLineWidth(Infinity)).toBe(2)
    expect(clampHostColorLineWidth(4)).toBe(4)
  })

  it('isHostColorMarkStyle accepts only the four styles', () => {
    for (const v of ['gradient', 'left-line', 'bottom-line', 'none']) {
      expect(isHostColorMarkStyle(v)).toBe(true)
    }
    for (const v of ['evil', '', 'Gradient', null, undefined, 2, {}]) {
      expect(isHostColorMarkStyle(v)).toBe(false)
    }
  })

  it('style setters update valid values and ignore invalid', () => {
    const store = useUISettingsStore.getState()
    store.setHostColorSidebarStyle('left-line')
    store.setHostColorTabBarStyle('none')
    expect(useUISettingsStore.getState().hostColorSidebarStyle).toBe('left-line')
    expect(useUISettingsStore.getState().hostColorTabBarStyle).toBe('none')
    store.setHostColorSidebarStyle('evil' as never)
    store.setHostColorTabBarStyle('evil' as never)
    expect(useUISettingsStore.getState().hostColorSidebarStyle).toBe('left-line')
    expect(useUISettingsStore.getState().hostColorTabBarStyle).toBe('none')
  })

  it('width setters clamp and round', () => {
    const store = useUISettingsStore.getState()
    store.setHostColorSidebarWidth(9)
    store.setHostColorTabBarWidth(0)
    expect(useUISettingsStore.getState().hostColorSidebarWidth).toBe(6)
    expect(useUISettingsStore.getState().hostColorTabBarWidth).toBe(1)
    store.setHostColorSidebarWidth(2.6)
    store.setHostColorTabBarWidth(NaN)
    expect(useUISettingsStore.getState().hostColorSidebarWidth).toBe(3)
    expect(useUISettingsStore.getState().hostColorTabBarWidth).toBe(2)
  })
})

describe('link detection settings', () => {
  beforeEach(() => {
    useUISettingsStore.setState({
      linkDetectAbsolute: true,
      linkDetectTilde: true,
      linkDetectRelativeSlash: true,
      linkDetectBareFilename: false,
    })
  })

  it('defaults: absolute=true, tilde=true, relative-slash=true, bare=false', () => {
    const s = useUISettingsStore.getInitialState()
    expect(s.linkDetectAbsolute).toBe(true)
    expect(s.linkDetectTilde).toBe(true)
    expect(s.linkDetectRelativeSlash).toBe(true)
    expect(s.linkDetectBareFilename).toBe(false)
  })

  it('setters toggle the flags in both directions', () => {
    const store = useUISettingsStore.getState()
    store.setLinkDetectAbsolute(false)
    expect(useUISettingsStore.getState().linkDetectAbsolute).toBe(false)
    store.setLinkDetectAbsolute(true)
    expect(useUISettingsStore.getState().linkDetectAbsolute).toBe(true)

    store.setLinkDetectTilde(false)
    expect(useUISettingsStore.getState().linkDetectTilde).toBe(false)
    store.setLinkDetectTilde(true)
    expect(useUISettingsStore.getState().linkDetectTilde).toBe(true)

    store.setLinkDetectRelativeSlash(false)
    expect(useUISettingsStore.getState().linkDetectRelativeSlash).toBe(false)
    store.setLinkDetectRelativeSlash(true)
    expect(useUISettingsStore.getState().linkDetectRelativeSlash).toBe(true)
    store.setLinkDetectBareFilename(true)
    expect(useUISettingsStore.getState().linkDetectBareFilename).toBe(true)
  })
})
