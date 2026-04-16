// =============================================================================
// Sync Architecture — PreferencesContributor Tests
// =============================================================================

import { describe, it, expect, beforeEach } from 'vitest'
import { createPreferencesContributor } from './preferences'
import { useUISettingsStore } from '../../../stores/useUISettingsStore'
import type { FullPayload } from '../types'

// ---------------------------------------------------------------------------
// Default state (mirrors store initial values)
// ---------------------------------------------------------------------------

const DEFAULT_STATE = {
  terminalRevealDelay: 300,
  terminalRenderer: 'webgl' as const,
  keepAliveCount: 0,
  keepAlivePinned: false,
  terminalSettingsVersion: 0,
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

function resetStore() {
  useUISettingsStore.setState(DEFAULT_STATE)
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

describe('createPreferencesContributor', () => {
  let contributor: ReturnType<typeof createPreferencesContributor>

  beforeEach(() => {
    resetStore()
    contributor = createPreferencesContributor()
  })

  // -------------------------------------------------------------------------
  // Identity & strategy
  // -------------------------------------------------------------------------

  it('has id "preferences"', () => {
    expect(contributor.id).toBe('preferences')
  })

  it('has strategy "full"', () => {
    expect(contributor.strategy).toBe('full')
  })

  // -------------------------------------------------------------------------
  // getVersion
  // -------------------------------------------------------------------------

  it('getVersion returns 1', () => {
    expect(contributor.getVersion()).toBe(1)
  })

  // -------------------------------------------------------------------------
  // serialize
  // -------------------------------------------------------------------------

  it('serialize returns FullPayload with version 1', () => {
    const payload = contributor.serialize() as FullPayload
    expect(payload.version).toBe(1)
    expect(payload.data).toBeDefined()
  })

  it('serialize only includes data fields (no functions)', () => {
    const payload = contributor.serialize() as FullPayload
    const keys = Object.keys(payload.data)

    // Must contain all data fields
    expect(keys).toContain('terminalRevealDelay')
    expect(keys).toContain('terminalRenderer')
    expect(keys).toContain('keepAliveCount')
    expect(keys).toContain('keepAlivePinned')
    expect(keys).toContain('terminalSettingsVersion')

    // Must NOT contain setter functions
    expect(keys).not.toContain('setTerminalRevealDelay')
    expect(keys).not.toContain('setTerminalRenderer')
    expect(keys).not.toContain('setKeepAliveCount')
    expect(keys).not.toContain('setKeepAlivePinned')
    expect(keys).not.toContain('bumpTerminalSettingsVersion')

    // All values must be non-function
    for (const key of keys) {
      expect(typeof payload.data[key]).not.toBe('function')
    }
  })

  it('serialize reflects current store state', () => {
    useUISettingsStore.setState({ terminalRevealDelay: 500, terminalRenderer: 'dom' })
    const payload = contributor.serialize() as FullPayload
    expect(payload.data.terminalRevealDelay).toBe(500)
    expect(payload.data.terminalRenderer).toBe('dom')
  })

  // -------------------------------------------------------------------------
  // deserialize — full-replace
  // -------------------------------------------------------------------------

  it('deserialize with full-replace overwrites store state', () => {
    const incoming: FullPayload = {
      version: 1,
      data: {
        terminalRevealDelay: 100,
        terminalRenderer: 'dom',
        keepAliveCount: 3,
        keepAlivePinned: true,
        terminalSettingsVersion: 5,
      },
    }

    contributor.deserialize(incoming, { type: 'full-replace' })

    const state = useUISettingsStore.getState()
    expect(state.terminalRevealDelay).toBe(100)
    expect(state.terminalRenderer).toBe('dom')
    expect(state.keepAliveCount).toBe(3)
    expect(state.keepAlivePinned).toBe(true)
    expect(state.terminalSettingsVersion).toBe(5)
  })

  // -------------------------------------------------------------------------
  // deserialize — field-merge
  // -------------------------------------------------------------------------

  it('deserialize with field-merge only applies resolved remote fields', () => {
    // Set up a known initial local state
    useUISettingsStore.setState({
      terminalRevealDelay: 300,
      terminalRenderer: 'webgl',
      keepAliveCount: 2,
      keepAlivePinned: false,
      terminalSettingsVersion: 1,
    })

    const incoming: FullPayload = {
      version: 1,
      data: {
        terminalRevealDelay: 800,  // remote value
        terminalRenderer: 'dom',   // remote value
        keepAliveCount: 5,         // remote value
        keepAlivePinned: true,     // remote value
        terminalSettingsVersion: 9, // remote value
      },
    }

    // Only apply terminalRevealDelay and keepAlivePinned from remote
    contributor.deserialize(incoming, {
      type: 'field-merge',
      resolved: {
        terminalRevealDelay: 'remote',
        keepAlivePinned: 'remote',
        terminalRenderer: 'local',
        keepAliveCount: 'local',
        terminalSettingsVersion: 'local',
      },
    })

    const state = useUISettingsStore.getState()
    // Remote-resolved fields should be updated
    expect(state.terminalRevealDelay).toBe(800)
    expect(state.keepAlivePinned).toBe(true)
    // Local-resolved fields should remain unchanged
    expect(state.terminalRenderer).toBe('webgl')
    expect(state.keepAliveCount).toBe(2)
    expect(state.terminalSettingsVersion).toBe(1)
  })

  it('deserialize with field-merge ignores fields not present in resolved', () => {
    useUISettingsStore.setState({
      terminalRevealDelay: 300,
      keepAliveCount: 2,
    })

    const incoming: FullPayload = {
      version: 1,
      data: {
        terminalRevealDelay: 999,
        keepAliveCount: 9,
      },
    }

    // resolved only mentions terminalRevealDelay=remote; keepAliveCount not mentioned
    contributor.deserialize(incoming, {
      type: 'field-merge',
      resolved: { terminalRevealDelay: 'remote' },
    })

    const state = useUISettingsStore.getState()
    expect(state.terminalRevealDelay).toBe(999) // remote applied
    expect(state.keepAliveCount).toBe(2)         // untouched
  })
})
