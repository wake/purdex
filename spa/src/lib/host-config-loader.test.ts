import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { startHostConfigLoader } from './host-config-loader'
import { useHostStore } from '../stores/useHostStore'
import { emptyHostConfigEntry, useHostConfigStore } from '../stores/useHostConfigStore'
import { clearAllRelayPromptDrafts, readRelayPromptDraft, relayPromptDraftKey, writeRelayPromptDraft } from './relay-prompt-draft-memory'

const host = (id: string, ip = '100.64.0.2') => ({ id, name: id, ip, port: 7860, token: null, order: 0 })
let stop: () => void = () => {}
const load = vi.fn(async (_hostId: string) => {})

beforeEach(() => {
  load.mockClear()
  useHostConfigStore.setState({ byHost: {}, load })
  useHostStore.setState({ hosts: { h1: host('h1'), h2: host('h2') }, hostOrder: ['h1', 'h2'], runtime: { h2: { status: 'connected' } } })
})
afterEach(() => { stop(); clearAllRelayPromptDrafts() })

describe('startHostConfigLoader', () => {
  it('loads hosts already connected at start', () => {
    stop = startHostConfigLoader()
    expect(load).toHaveBeenCalledWith('h2')
    expect(load).not.toHaveBeenCalledWith('h1')
  })

  it('loads a host when it transitions to connected, once', () => {
    stop = startHostConfigLoader()
    load.mockClear()
    useHostStore.getState().setRuntime('h1', { status: 'connected' })
    useHostStore.getState().setRuntime('h1', { latency: 3 })
    expect(load).toHaveBeenCalledTimes(1)
    expect(load).toHaveBeenCalledWith('h1')
  })

  it('forgets a removed host', () => {
    useHostConfigStore.setState({ byHost: { h1: emptyHostConfigEntry('ready') } })
    stop = startHostConfigLoader()
    useHostStore.setState({ hosts: { h2: host('h2') }, hostOrder: ['h2'] })
    expect(useHostConfigStore.getState().byHost.h1).toBeUndefined()
  })

  // P9a-3 review: the relay prompt drafts are module memory, outside the store; forgetting a host drops them too.
  it('a removed host re-added under the same id does not bring back its relay prompt drafts', () => {
    writeRelayPromptDraft(relayPromptDraftKey('h1', 'write'), 'typed for the old h1')
    writeRelayPromptDraft(relayPromptDraftKey('h2', 'seed'), 'typed for h2')
    stop = startHostConfigLoader()
    useHostStore.setState({ hosts: { h2: host('h2') }, hostOrder: ['h2'] })
    useHostStore.setState({ hosts: { h1: host('h1'), h2: host('h2') }, hostOrder: ['h1', 'h2'] })
    expect(readRelayPromptDraft(relayPromptDraftKey('h1', 'write'))).toBeUndefined()
    expect(readRelayPromptDraft(relayPromptDraftKey('h2', 'seed'))).toBe('typed for h2')
  })

  it('an endpoint change drops that host\'s relay prompt drafts', () => {
    writeRelayPromptDraft(relayPromptDraftKey('h2', 'fix'), 'typed for the old daemon')
    stop = startHostConfigLoader()
    useHostStore.setState({ hosts: { h1: host('h1'), h2: host('h2', '100.64.0.4') } })
    expect(readRelayPromptDraft(relayPromptDraftKey('h2', 'fix'))).toBeUndefined()
  })

  it('an endpoint change forgets the host and reloads it if connected', () => {
    useHostConfigStore.setState({ byHost: { h2: emptyHostConfigEntry('ready') } })
    stop = startHostConfigLoader()
    load.mockClear()
    useHostStore.setState({ hosts: { h1: host('h1'), h2: host('h2', '100.64.0.4') } })
    expect(useHostConfigStore.getState().byHost.h2).toBeUndefined()
    expect(load).toHaveBeenCalledWith('h2')
  })
})
