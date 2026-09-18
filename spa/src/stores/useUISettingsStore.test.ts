import { describe, it, expect, beforeEach } from 'vitest'
import {
  useUISettingsStore,
  KEEPALIVE_MAX_WEBGL,
  KEEPALIVE_MAX_DOM,
  clampKeepAlive,
  HOST_BADGE_LINE_OPACITY_MIN,
  HOST_BADGE_LINE_OPACITY_MAX,
  HOST_BADGE_LINE_OPACITY_DEFAULT,
  HOST_BADGE_BG_OPACITY_MIN,
  HOST_BADGE_BG_OPACITY_MAX,
  HOST_BADGE_BG_OPACITY_DEFAULT,
  HOST_BADGE_BOX_MIN,
  HOST_BADGE_BOX_MAX,
  HOST_BADGE_BOX_DEFAULT,
  HOST_BADGE_INSET_MIN,
  HOST_BADGE_INSET_MAX,
  HOST_BADGE_INSET_DEFAULT,
  HOST_BADGE_RADIUS_MIN,
  HOST_BADGE_RADIUS_MAX,
  HOST_BADGE_RADIUS_DEFAULT,
  HOST_BADGE_DEFAULTS,
  clampHostBadgeLineOpacity,
  clampHostBadgeBgOpacity,
  clampHostBadgeBox,
  clampHostBadgeInset,
  clampHostBadgeRadius,
  isHostBadgeLineColor,
  sanitizeHostBadgePrefs,
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

  it('stripAgentTitleMarker defaults to true', () => {
    expect(useUISettingsStore.getState().stripAgentTitleMarker).toBe(true)
  })

  it('setStripAgentTitleMarker toggles the flag', () => {
    useUISettingsStore.getState().setStripAgentTitleMarker(false)
    expect(useUISettingsStore.getState().stripAgentTitleMarker).toBe(false)
    useUISettingsStore.getState().setStripAgentTitleMarker(true)
    expect(useUISettingsStore.getState().stripAgentTitleMarker).toBe(true)
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

describe('host badge settings', () => {
  beforeEach(() => {
    localStorage.clear()
    useUISettingsStore.setState({ ...HOST_BADGE_DEFAULTS })
  })

  it('exposes the documented bounds as constants', () => {
    expect([HOST_BADGE_LINE_OPACITY_MIN, HOST_BADGE_LINE_OPACITY_MAX, HOST_BADGE_LINE_OPACITY_DEFAULT]).toEqual([20, 100, 100])
    expect([HOST_BADGE_BG_OPACITY_MIN, HOST_BADGE_BG_OPACITY_MAX, HOST_BADGE_BG_OPACITY_DEFAULT]).toEqual([0, 100, 22])
    expect([HOST_BADGE_BOX_MIN, HOST_BADGE_BOX_MAX, HOST_BADGE_BOX_DEFAULT]).toEqual([12, 24, 16])
    expect([HOST_BADGE_INSET_MIN, HOST_BADGE_INSET_MAX, HOST_BADGE_INSET_DEFAULT]).toEqual([0, 5, 2])
    expect([HOST_BADGE_RADIUS_MIN, HOST_BADGE_RADIUS_MAX, HOST_BADGE_RADIUS_DEFAULT]).toEqual([0, 8, 4])
  })

  it('defaults all 14 fields per surface', () => {
    const s = useUISettingsStore.getInitialState()
    for (const surface of ['Sidebar', 'TabBar'] as const) {
      expect(s[`hostBadge${surface}Enabled`]).toBe(true)
      expect(s[`hostBadge${surface}LineColor`]).toBe('host')
      expect(s[`hostBadge${surface}LineOpacity`]).toBe(100)
      expect(s[`hostBadge${surface}BgOpacity`]).toBe(22)
      expect(s[`hostBadge${surface}Box`]).toBe(16)
      expect(s[`hostBadge${surface}Inset`]).toBe(2)
      expect(s[`hostBadge${surface}Radius`]).toBe(4)
    }
  })

  it('HOST_BADGE_DEFAULTS lists exactly the 14 fields', () => {
    expect(Object.keys(HOST_BADGE_DEFAULTS).sort()).toEqual(
      [
        'hostBadgeSidebarEnabled',
        'hostBadgeSidebarLineColor',
        'hostBadgeSidebarLineOpacity',
        'hostBadgeSidebarBgOpacity',
        'hostBadgeSidebarBox',
        'hostBadgeSidebarInset',
        'hostBadgeSidebarRadius',
        'hostBadgeTabBarEnabled',
        'hostBadgeTabBarLineColor',
        'hostBadgeTabBarLineOpacity',
        'hostBadgeTabBarBgOpacity',
        'hostBadgeTabBarBox',
        'hostBadgeTabBarInset',
        'hostBadgeTabBarRadius',
      ].sort(),
    )
  })

  it('isHostBadgeLineColor accepts only host / neutral', () => {
    expect(isHostBadgeLineColor('host')).toBe(true)
    expect(isHostBadgeLineColor('neutral')).toBe(true)
    for (const v of ['evil', '', 'Host', null, undefined, 2, {}]) {
      expect(isHostBadgeLineColor(v)).toBe(false)
    }
  })

  it('clamp helpers round then clamp, non-finite falls back to the default', () => {
    expect(clampHostBadgeBox(8)).toBe(12)
    expect(clampHostBadgeBox(40)).toBe(24)
    expect(clampHostBadgeBox(15.6)).toBe(16)
    expect(clampHostBadgeBox(NaN)).toBe(16)
    expect(clampHostBadgeBox(Infinity)).toBe(16)

    expect(clampHostBadgeInset(-1)).toBe(0)
    expect(clampHostBadgeInset(9)).toBe(5)
    expect(clampHostBadgeInset(2.6)).toBe(3)
    expect(clampHostBadgeInset(NaN)).toBe(2)

    expect(clampHostBadgeLineOpacity(5)).toBe(20)
    expect(clampHostBadgeLineOpacity(200)).toBe(100)
    expect(clampHostBadgeLineOpacity(50.4)).toBe(50)
    expect(clampHostBadgeLineOpacity(NaN)).toBe(100)

    expect(clampHostBadgeBgOpacity(-5)).toBe(0)
    expect(clampHostBadgeBgOpacity(200)).toBe(100)
    expect(clampHostBadgeBgOpacity(NaN)).toBe(22)

    expect(clampHostBadgeRadius(20)).toBe(8)
    expect(clampHostBadgeRadius(-1)).toBe(0)
    expect(clampHostBadgeRadius(2.6)).toBe(3)
    expect(clampHostBadgeRadius(NaN)).toBe(4)
  })

  it('Enabled setters toggle each surface independently', () => {
    useUISettingsStore.getState().setHostBadgeSidebarEnabled(false)
    expect(useUISettingsStore.getState().hostBadgeSidebarEnabled).toBe(false)
    expect(useUISettingsStore.getState().hostBadgeTabBarEnabled).toBe(true)
    useUISettingsStore.getState().setHostBadgeTabBarEnabled(false)
    useUISettingsStore.getState().setHostBadgeSidebarEnabled(true)
    expect(useUISettingsStore.getState().hostBadgeSidebarEnabled).toBe(true)
    expect(useUISettingsStore.getState().hostBadgeTabBarEnabled).toBe(false)
  })

  it('LineColor setters accept the union and ignore anything else', () => {
    const store = useUISettingsStore.getState()
    store.setHostBadgeSidebarLineColor('neutral')
    store.setHostBadgeTabBarLineColor('neutral')
    expect(useUISettingsStore.getState().hostBadgeSidebarLineColor).toBe('neutral')
    expect(useUISettingsStore.getState().hostBadgeTabBarLineColor).toBe('neutral')
    store.setHostBadgeSidebarLineColor('evil' as never)
    store.setHostBadgeTabBarLineColor('' as never)
    expect(useUISettingsStore.getState().hostBadgeSidebarLineColor).toBe('neutral')
    expect(useUISettingsStore.getState().hostBadgeTabBarLineColor).toBe('neutral')
    store.setHostBadgeSidebarLineColor('host')
    expect(useUISettingsStore.getState().hostBadgeSidebarLineColor).toBe('host')
  })

  it('numeric setters round then clamp on both surfaces', () => {
    const store = useUISettingsStore.getState()
    store.setHostBadgeSidebarBox(8)
    store.setHostBadgeTabBarBox(40)
    store.setHostBadgeSidebarInset(-1)
    store.setHostBadgeTabBarInset(9)
    store.setHostBadgeSidebarLineOpacity(5)
    store.setHostBadgeTabBarLineOpacity(200)
    store.setHostBadgeSidebarBgOpacity(-5)
    store.setHostBadgeTabBarBgOpacity(200)
    store.setHostBadgeSidebarRadius(20)
    store.setHostBadgeTabBarRadius(2.6)

    const s = useUISettingsStore.getState()
    expect(s.hostBadgeSidebarBox).toBe(12)
    expect(s.hostBadgeTabBarBox).toBe(24)
    expect(s.hostBadgeSidebarInset).toBe(0)
    expect(s.hostBadgeTabBarInset).toBe(5)
    expect(s.hostBadgeSidebarLineOpacity).toBe(20)
    expect(s.hostBadgeTabBarLineOpacity).toBe(100)
    expect(s.hostBadgeSidebarBgOpacity).toBe(0)
    expect(s.hostBadgeTabBarBgOpacity).toBe(100)
    expect(s.hostBadgeSidebarRadius).toBe(8)
    expect(s.hostBadgeTabBarRadius).toBe(3)

    store.setHostBadgeSidebarBox(NaN)
    expect(useUISettingsStore.getState().hostBadgeSidebarBox).toBe(16)
  })

  it('sanitizeHostBadgePrefs drops invalid enums / booleans / non-finite numbers and clamps the rest', () => {
    const out = sanitizeHostBadgePrefs({
      hostBadgeSidebarEnabled: 'yes',
      hostBadgeSidebarLineColor: 'evil',
      hostBadgeSidebarLineOpacity: '80',
      hostBadgeSidebarBgOpacity: NaN,
      hostBadgeSidebarBox: 99,
      hostBadgeSidebarInset: -3,
      hostBadgeSidebarRadius: 2.6,
      hostBadgeTabBarEnabled: false,
      hostBadgeTabBarLineColor: 'neutral',
      hostBadgeTabBarBox: Infinity,
      other: 'keep',
    })
    expect('hostBadgeSidebarEnabled' in out).toBe(false)
    expect('hostBadgeSidebarLineColor' in out).toBe(false)
    expect('hostBadgeSidebarLineOpacity' in out).toBe(false)
    expect('hostBadgeSidebarBgOpacity' in out).toBe(false)
    expect('hostBadgeTabBarBox' in out).toBe(false)
    expect(out.hostBadgeSidebarBox).toBe(24)
    expect(out.hostBadgeSidebarInset).toBe(0)
    expect(out.hostBadgeSidebarRadius).toBe(3)
    expect(out.hostBadgeTabBarEnabled).toBe(false)
    expect(out.hostBadgeTabBarLineColor).toBe('neutral')
    expect(out.other).toBe('keep')
  })

  it('sanitizeHostBadgePrefs leaves absent fields absent and does not mutate its input', () => {
    expect(sanitizeHostBadgePrefs({})).toEqual({})
    const input = { hostBadgeSidebarBox: 99 }
    const out = sanitizeHostBadgePrefs(input)
    expect(out).toEqual({ hostBadgeSidebarBox: 24 })
    expect(input.hostBadgeSidebarBox).toBe(99)
  })

  it('rehydrate resets a corrupt persisted payload to defaults and clamps out-of-range values', async () => {
    localStorage.setItem(
      STORAGE_KEYS.UI_SETTINGS,
      JSON.stringify({
        state: {
          hostBadgeSidebarEnabled: 'yes',
          hostBadgeSidebarLineColor: 'evil',
          hostBadgeSidebarLineOpacity: null,
          hostBadgeSidebarBgOpacity: 999,
          hostBadgeSidebarBox: 3,
          hostBadgeSidebarInset: 42,
          hostBadgeSidebarRadius: 'big',
          hostBadgeTabBarEnabled: false,
          hostBadgeTabBarLineColor: 'neutral',
          hostBadgeTabBarLineOpacity: 1,
          hostBadgeTabBarBox: 100,
        },
        version: 3,
      }),
    )
    await useUISettingsStore.persist.rehydrate()
    const s = useUISettingsStore.getState()
    expect(s.hostBadgeSidebarEnabled).toBe(true)
    expect(s.hostBadgeSidebarLineColor).toBe('host')
    expect(s.hostBadgeSidebarLineOpacity).toBe(100)
    expect(s.hostBadgeSidebarBgOpacity).toBe(100)
    expect(s.hostBadgeSidebarBox).toBe(12)
    expect(s.hostBadgeSidebarInset).toBe(5)
    expect(s.hostBadgeSidebarRadius).toBe(4)
    // valid persisted values survive
    expect(s.hostBadgeTabBarEnabled).toBe(false)
    expect(s.hostBadgeTabBarLineColor).toBe('neutral')
    expect(s.hostBadgeTabBarLineOpacity).toBe(20)
    expect(s.hostBadgeTabBarBox).toBe(24)
  })

  it('rehydrate leaves a clean persisted payload untouched', async () => {
    localStorage.setItem(
      STORAGE_KEYS.UI_SETTINGS,
      JSON.stringify({
        state: {
          hostBadgeSidebarEnabled: false,
          hostBadgeSidebarLineColor: 'neutral',
          hostBadgeSidebarLineOpacity: 60,
          hostBadgeSidebarBgOpacity: 10,
          hostBadgeSidebarBox: 20,
          hostBadgeSidebarInset: 1,
          hostBadgeSidebarRadius: 8,
          hostBadgeTabBarEnabled: true,
          hostBadgeTabBarLineColor: 'host',
          hostBadgeTabBarLineOpacity: 100,
          hostBadgeTabBarBgOpacity: 22,
          hostBadgeTabBarBox: 16,
          hostBadgeTabBarInset: 2,
          hostBadgeTabBarRadius: 4,
        },
        version: 3,
      }),
    )
    await useUISettingsStore.persist.rehydrate()
    const s = useUISettingsStore.getState()
    expect(s.hostBadgeSidebarEnabled).toBe(false)
    expect(s.hostBadgeSidebarLineColor).toBe('neutral')
    expect(s.hostBadgeSidebarLineOpacity).toBe(60)
    expect(s.hostBadgeSidebarBgOpacity).toBe(10)
    expect(s.hostBadgeSidebarBox).toBe(20)
    expect(s.hostBadgeSidebarInset).toBe(1)
    expect(s.hostBadgeSidebarRadius).toBe(8)
    expect(s.hostBadgeTabBarEnabled).toBe(true)
    expect(s.hostBadgeTabBarLineColor).toBe('host')
  })

  it('rehydrate with no persisted host badge keys leaves every field at its default', async () => {
    localStorage.setItem(
      STORAGE_KEYS.UI_SETTINGS,
      JSON.stringify({ state: { terminalRenderer: 'webgl' }, version: 3 }),
    )
    await useUISettingsStore.persist.rehydrate()
    const s = useUISettingsStore.getState() as unknown as Record<string, unknown>
    for (const [k, v] of Object.entries(HOST_BADGE_DEFAULTS)) {
      expect(s[k]).toBe(v)
    }
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
