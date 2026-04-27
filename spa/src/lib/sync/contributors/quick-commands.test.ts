// =============================================================================
// Sync Architecture — QuickCommandsContributor Tests
// =============================================================================

import { describe, it, expect, beforeEach } from 'vitest'
import { createQuickCommandsContributor } from './quick-commands'
import { useQuickCommandStore, sanitizeBindings } from '../../../stores/useQuickCommandStore'
import type { QuickCommand } from '../../../stores/useQuickCommandStore'
import type { FullPayload } from '../types'
import { QUICK_COMMAND_SLOTS } from '../../../lib/quick-command-slots'

// ---------------------------------------------------------------------------
// Default state (matches store defaults)
// ---------------------------------------------------------------------------

const DEFAULT_STATE = {
  global: [
    { id: 'start-cc', name: 'Start Claude Code', command: 'claude -p --verbose --output-format stream-json', category: 'agent' },
    { id: 'start-codex', name: 'Start Codex', command: 'codex', category: 'agent' },
  ],
  byHost: {} as Record<string, QuickCommand[]>,
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

describe('createQuickCommandsContributor — bindings field (v2)', () => {
  let contributor: ReturnType<typeof createQuickCommandsContributor>

  beforeEach(() => {
    useQuickCommandStore.setState({
      global: [],
      byHost: {},
      bindings: {},
    })
    contributor = createQuickCommandsContributor()
  })

  it('serialize includes bindings field', () => {
    useQuickCommandStore.getState().addCommand({ id: 'cmd-a', name: 'A', command: 'a' })
    useQuickCommandStore.getState().setBinding('cmd-a', [QUICK_COMMAND_SLOTS.WORKSPACE_ACTIONS])
    const payload = contributor.serialize() as FullPayload
    expect(payload.data.bindings).toEqual({ 'cmd-a': ['workspace.actions'] })
  })

  it('serialize keys exclude action functions but include bindings', () => {
    const payload = contributor.serialize() as FullPayload
    const keys = Object.keys(payload.data)
    expect(keys).toContain('global')
    expect(keys).toContain('byHost')
    expect(keys).toContain('bindings')
    expect(keys).not.toContain('setBinding')
    expect(keys).not.toContain('getBoundCommands')
  })

  it('deserialize full-replace with bindings overwrites local bindings', () => {
    useQuickCommandStore.setState({
      global: [{ id: 'local', name: 'L', command: 'l' }],
      byHost: {},
      bindings: { 'local': ['workspace.actions'] },
    })
    const incoming: FullPayload = {
      version: 1,
      data: {
        global: [{ id: 'remote', name: 'R', command: 'r' }],
        byHost: {},
        bindings: { 'remote': ['host.actions'] },
      },
    }
    contributor.deserialize(incoming, { type: 'full-replace' })
    const state = useQuickCommandStore.getState()
    expect(state.bindings).toEqual({ 'remote': ['host.actions'] })
  })

  it('deserialize sanitizes incoming bindings (drops malformed entries)', () => {
    const incoming: FullPayload = {
      version: 1,
      data: {
        global: [{ id: 'cmd-a', name: 'A', command: 'a' }],
        byHost: {},
        bindings: {
          'cmd-a': ['workspace.actions'],
          // hostile payload variants — must all be dropped:
          '__proto__': ['host.actions'],
          'cmd-bad-targets': 'not-an-array' as unknown as string[],
          '': ['host.actions'],
        },
      },
    }
    contributor.deserialize(incoming, { type: 'full-replace' })
    const state = useQuickCommandStore.getState()
    expect(state.bindings).toEqual({ 'cmd-a': ['workspace.actions'] })
  })

  it('field-merge: cross-field dangling — global=local + bindings=remote → getBoundCommands returns empty', () => {
    // local has cmd-A only; remote bindings reference cmd-B (not in local global)
    useQuickCommandStore.setState({
      global: [{ id: 'cmd-a', name: 'A', command: 'a' }],
      byHost: {},
      bindings: {},
    })
    const incoming: FullPayload = {
      version: 1,
      data: {
        global: [{ id: 'cmd-b', name: 'B', command: 'b' }],
        byHost: {},
        bindings: { 'cmd-b': ['workspace.actions'] },
      },
    }
    contributor.deserialize(incoming, {
      type: 'field-merge',
      resolved: { global: 'local', bindings: 'remote' },
    })
    const state = useQuickCommandStore.getState()
    // global stayed local — only cmd-a
    expect(state.global.map((c) => c.id)).toEqual(['cmd-a'])
    // bindings took remote — references cmd-b
    expect(state.bindings['cmd-b']).toEqual(['workspace.actions'])
    // BUT getBoundCommands filters dangling at read-time → empty
    const bound = state.getBoundCommands(QUICK_COMMAND_SLOTS.WORKSPACE_ACTIONS, 'host-1')
    expect(bound).toEqual([])
  })

  it('sanitizeBindings is idempotent on already-clean payload', () => {
    const clean = { 'cmd-a': ['workspace.actions'] }
    expect(sanitizeBindings(clean)).toEqual(clean)
  })

  // codex round-1 P2: missing/undefined bindings normalization
  it('full-replace defaults missing bindings to {} (older payload bundle)', () => {
    // simulate older sync bundle without bindings field
    useQuickCommandStore.setState({
      global: [{ id: 'cmd-a', name: 'A', command: 'a' }],
      byHost: {},
      bindings: { 'cmd-a': ['workspace.actions'] }, // local has stale bindings
    })
    const incoming: FullPayload = {
      version: 1,
      data: {
        global: [{ id: 'cmd-b', name: 'B', command: 'b' }],
        byHost: {},
        // bindings absent — older format
      } as unknown as Record<string, unknown>,
    }
    contributor.deserialize(incoming, { type: 'full-replace' })
    const state = useQuickCommandStore.getState()
    // bindings reset to {} — must be a record, NEVER undefined
    expect(state.bindings).toEqual({})
    expect(typeof state.bindings).toBe('object')
  })

  it('full-replace defaults null bindings to {} (corrupted payload)', () => {
    useQuickCommandStore.setState({
      global: [{ id: 'cmd-a', name: 'A', command: 'a' }],
      byHost: {},
      bindings: { 'cmd-a': ['workspace.actions'] },
    })
    const incoming: FullPayload = {
      version: 1,
      data: {
        global: [],
        byHost: {},
        bindings: null,
      } as unknown as Record<string, unknown>,
    }
    contributor.deserialize(incoming, { type: 'full-replace' })
    expect(useQuickCommandStore.getState().bindings).toEqual({})
  })

  it('field-merge with missing bindings field leaves local bindings untouched', () => {
    // Even when resolved says bindings='remote', if incoming omits the field,
    // patch must NOT clear local (preserves field-merge "absent = no change" semantic).
    useQuickCommandStore.setState({
      global: [{ id: 'cmd-a', name: 'A', command: 'a' }],
      byHost: {},
      bindings: { 'cmd-a': ['workspace.actions'] },
    })
    const incoming: FullPayload = {
      version: 1,
      data: {
        global: [{ id: 'cmd-a', name: 'A', command: 'a' }],
        byHost: {},
        // bindings absent
      } as unknown as Record<string, unknown>,
    }
    contributor.deserialize(incoming, {
      type: 'field-merge',
      resolved: { bindings: 'remote' },
    })
    // Local bindings preserved (field absent → no patch applied)
    expect(useQuickCommandStore.getState().bindings).toEqual({
      'cmd-a': ['workspace.actions'],
    })
  })
})
