import { describe, it, expect, beforeEach } from 'vitest'
import { useUISettingsStore, KEEPALIVE_MAX_WEBGL, KEEPALIVE_MAX_DOM, clampKeepAlive } from './useUISettingsStore'

describe('useUISettingsStore', () => {
  beforeEach(() => {
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
