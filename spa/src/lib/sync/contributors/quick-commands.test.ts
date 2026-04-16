// =============================================================================
// Sync Architecture — QuickCommandsContributor Tests
// =============================================================================

import { describe, it, expect, beforeEach } from 'vitest'
import { createQuickCommandsContributor } from './quick-commands'
import { useQuickCommandStore } from '../../../stores/useQuickCommandStore'
import type { FullPayload } from '../types'

// ---------------------------------------------------------------------------
// Default state (matches store defaults)
// ---------------------------------------------------------------------------

const DEFAULT_STATE = {
  global: [
    { id: 'start-cc', name: 'Start Claude Code', command: 'claude -p --verbose --output-format stream-json', category: 'agent' },
    { id: 'start-codex', name: 'Start Codex', command: 'codex', category: 'agent' },
  ],
  byHost: {} as Record<string, unknown[]>,
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

function resetStore() {
  useQuickCommandStore.setState(DEFAULT_STATE)
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

describe('createQuickCommandsContributor', () => {
  let contributor: ReturnType<typeof createQuickCommandsContributor>

  beforeEach(() => {
    resetStore()
    contributor = createQuickCommandsContributor()
  })

  // -------------------------------------------------------------------------
  // Identity & strategy
  // -------------------------------------------------------------------------

  it('has id "quick-commands"', () => {
    expect(contributor.id).toBe('quick-commands')
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

    expect(keys).toContain('global')
    expect(keys).toContain('byHost')

    // Must NOT contain action functions
    expect(keys).not.toContain('addCommand')
    expect(keys).not.toContain('removeCommand')
    expect(keys).not.toContain('updateCommand')
    expect(keys).not.toContain('getCommands')

    // All values must be non-function
    for (const key of keys) {
      expect(typeof payload.data[key]).not.toBe('function')
    }
  })

  it('serialize reflects current store state', () => {
    useQuickCommandStore.getState().addCommand({ id: 'my-cmd', name: 'My Command', command: 'echo hello' })
    const payload = contributor.serialize() as FullPayload
    const global = payload.data.global as Array<{ id: string }>
    expect(global.some((c) => c.id === 'my-cmd')).toBe(true)
  })

  // -------------------------------------------------------------------------
  // deserialize — full-replace
  // -------------------------------------------------------------------------

  it('deserialize with full-replace overwrites store state', () => {
    const incoming: FullPayload = {
      version: 1,
      data: {
        global: [{ id: 'remote-cmd', name: 'Remote Command', command: 'remote' }],
        byHost: { 'host-1': [{ id: 'host-cmd', name: 'Host Command', command: 'ls' }] },
      },
    }

    contributor.deserialize(incoming, { type: 'full-replace' })

    const state = useQuickCommandStore.getState()
    const globalCmds = state.global as Array<{ id: string }>
    expect(globalCmds.some((c) => c.id === 'remote-cmd')).toBe(true)
    expect(globalCmds.some((c) => c.id === 'start-cc')).toBe(false)
    expect(state.byHost['host-1']).toHaveLength(1)
  })

  // -------------------------------------------------------------------------
  // deserialize — field-merge
  // -------------------------------------------------------------------------

  it('deserialize with field-merge only applies resolved remote fields', () => {
    useQuickCommandStore.setState({
      global: [{ id: 'local-cmd', name: 'Local', command: 'local' }],
      byHost: {},
    })

    const incoming: FullPayload = {
      version: 1,
      data: {
        global: [{ id: 'remote-cmd', name: 'Remote', command: 'remote' }],
        byHost: { 'h-1': [{ id: 'host-cmd', name: 'Host', command: 'host' }] },
      },
    }

    // Only apply byHost from remote; global stays local
    contributor.deserialize(incoming, {
      type: 'field-merge',
      resolved: {
        global: 'local',
        byHost: 'remote',
      },
    })

    const state = useQuickCommandStore.getState()
    const globalCmds = state.global as Array<{ id: string }>
    // global stays local
    expect(globalCmds.some((c) => c.id === 'local-cmd')).toBe(true)
    expect(globalCmds.some((c) => c.id === 'remote-cmd')).toBe(false)
    // byHost updated from remote
    expect(state.byHost['h-1']).toHaveLength(1)
  })

  it('deserialize with field-merge ignores fields not present in resolved', () => {
    useQuickCommandStore.setState({
      global: [{ id: 'local-cmd', name: 'Local', command: 'local' }],
      byHost: {},
    })

    const incoming: FullPayload = {
      version: 1,
      data: {
        global: [{ id: 'remote-cmd', name: 'Remote', command: 'remote' }],
        byHost: { 'h-1': [] },
      },
    }

    // Only global=remote; byHost not mentioned
    contributor.deserialize(incoming, {
      type: 'field-merge',
      resolved: { global: 'remote' },
    })

    const state = useQuickCommandStore.getState()
    const globalCmds = state.global as Array<{ id: string }>
    expect(globalCmds.some((c) => c.id === 'remote-cmd')).toBe(true)
    // byHost untouched
    expect(state.byHost['h-1']).toBeUndefined()
  })
})
