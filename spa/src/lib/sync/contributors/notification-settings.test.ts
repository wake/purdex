// =============================================================================
// Sync Architecture — NotificationSettingsContributor Tests
// =============================================================================

import { describe, it, expect, beforeEach } from 'vitest'
import { createNotificationSettingsContributor } from './notification-settings'
import { useNotificationSettingsStore } from '../../../stores/useNotificationSettingsStore'
import type { FullPayload } from '../types'

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

function resetStore() {
  useNotificationSettingsStore.setState({ agents: {} })
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

describe('createNotificationSettingsContributor', () => {
  let contributor: ReturnType<typeof createNotificationSettingsContributor>

  beforeEach(() => {
    resetStore()
    contributor = createNotificationSettingsContributor()
  })

  // -------------------------------------------------------------------------
  // Identity & strategy
  // -------------------------------------------------------------------------

  it('has id "notification-settings"', () => {
    expect(contributor.id).toBe('notification-settings')
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

  it('serialize only includes expected data fields (no functions)', () => {
    const payload = contributor.serialize() as FullPayload
    const keys = Object.keys(payload.data)

    expect(keys).toContain('agents')

    // Must NOT contain action functions
    expect(keys).not.toContain('getSettingsForAgent')
    expect(keys).not.toContain('setAgentEnabled')
    expect(keys).not.toContain('setEventEnabled')
    expect(keys).not.toContain('setNotifyWithoutTab')
    expect(keys).not.toContain('setReopenTabOnClick')

    // All values must be non-function
    for (const key of keys) {
      expect(typeof payload.data[key]).not.toBe('function')
    }
  })

  it('serialize reflects current store state', () => {
    useNotificationSettingsStore.getState().setAgentEnabled('claude-code', true)
    const payload = contributor.serialize() as FullPayload
    const agents = payload.data.agents as Record<string, { enabled: boolean }>
    expect(agents['claude-code']).toBeDefined()
    expect(agents['claude-code'].enabled).toBe(true)
  })

  // -------------------------------------------------------------------------
  // deserialize — full-replace
  // -------------------------------------------------------------------------

  it('deserialize with full-replace overwrites store state', () => {
    const incoming: FullPayload = {
      version: 1,
      data: {
        agents: {
          'claude-code': { enabled: false, events: { done: true }, notifyWithoutTab: true, reopenTabOnClick: false },
          'codex': { enabled: true, events: {}, notifyWithoutTab: false, reopenTabOnClick: true },
        },
      },
    }

    contributor.deserialize(incoming, { type: 'full-replace' })

    const state = useNotificationSettingsStore.getState()
    expect(state.agents['claude-code']).toBeDefined()
    expect(state.agents['claude-code'].enabled).toBe(false)
    expect(state.agents['codex']).toBeDefined()
    expect(state.agents['codex'].reopenTabOnClick).toBe(true)
  })

  // -------------------------------------------------------------------------
  // deserialize — field-merge
  // -------------------------------------------------------------------------

  it('deserialize with field-merge only applies resolved remote fields', () => {
    useNotificationSettingsStore.setState({
      agents: {
        'local-agent': { enabled: true, events: {}, notifyWithoutTab: false, reopenTabOnClick: false },
      },
    })

    const incoming: FullPayload = {
      version: 1,
      data: {
        agents: {
          'remote-agent': { enabled: false, events: {}, notifyWithoutTab: true, reopenTabOnClick: false },
        },
      },
    }

    contributor.deserialize(incoming, {
      type: 'field-merge',
      resolved: { agents: 'remote' },
    })

    const state = useNotificationSettingsStore.getState()
    expect(state.agents['remote-agent']).toBeDefined()
    expect(state.agents['local-agent']).toBeUndefined()
  })

  it('deserialize with field-merge keeps local when resolved local', () => {
    useNotificationSettingsStore.setState({
      agents: {
        'local-agent': { enabled: true, events: {}, notifyWithoutTab: false, reopenTabOnClick: false },
      },
    })

    const incoming: FullPayload = {
      version: 1,
      data: {
        agents: {
          'remote-agent': { enabled: false, events: {}, notifyWithoutTab: true, reopenTabOnClick: false },
        },
      },
    }

    contributor.deserialize(incoming, {
      type: 'field-merge',
      resolved: { agents: 'local' },
    })

    const state = useNotificationSettingsStore.getState()
    // agents unchanged (local wins)
    expect(state.agents['local-agent']).toBeDefined()
    expect(state.agents['remote-agent']).toBeUndefined()
  })
})
