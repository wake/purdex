// spa/src/hooks/useSessionEventWs.test.ts
import { describe, it, expect, beforeEach } from 'vitest'
import { useStreamStore } from '../stores/useStreamStore'
import { useSessionStore } from '../stores/useSessionStore'
import type { StreamMessage } from '../lib/stream-ws'

// Test the event handler logic that useMultiHostEventWs uses.
// We test the store operations directly since the hook is a thin wrapper
// around connectSessionEvents + store dispatches.

const HOST = 'local'

const emptyState = {
  sessions: {},
  relayStatus: {},
  handoffProgress: {},
}

beforeEach(() => {
  useStreamStore.setState(emptyState)
  useSessionStore.setState({ sessions: {}, activeHostId: null, activeCode: null })
})

describe('session event handler logic', () => {
  it('relay event sets relayStatus keyed by composite key', () => {
    const code = 'abc001'
    useStreamStore.getState().setRelayStatus(HOST, code, true)
    expect(useStreamStore.getState().relayStatus[`${HOST}:${code}`]).toBe(true)
  })

  it('handoff connected clears progress and finds session by code', () => {
    const code = 'abc001'
    // Set up session with code (nested under hostId)
    useSessionStore.setState({
      sessions: {
        [HOST]: [
          { code, name: 'dev', cwd: '/tmp', mode: 'stream', cc_session_id: 'sid1', cc_model: '', has_relay: false },
        ],
      },
      activeHostId: HOST,
      activeCode: null,
    })

    // Simulate handoff:connected event behavior
    useStreamStore.getState().setHandoffProgress(HOST, code, '')

    // Verify the .find() by code works within host's sessions
    const sess = (useSessionStore.getState().sessions[HOST] ?? []).find((s) => s.code === code)
    expect(sess).toBeDefined()
    expect(sess!.name).toBe('dev')
    expect(sess!.mode).toBe('stream')
  })

  it('handoff connected with term mode clears session', () => {
    const code = 'abc001'
    useStreamStore.getState().addMessage(HOST, code, { type: 'assistant' } as StreamMessage)

    // Session is in term mode — should clear
    useSessionStore.setState({
      sessions: {
        [HOST]: [
          { code, name: 'dev', cwd: '/tmp', mode: 'term', cc_session_id: '', cc_model: '', has_relay: false },
        ],
      },
    })

    const sess = (useSessionStore.getState().sessions[HOST] ?? []).find((s) => s.code === code)
    if (!sess || sess.mode === 'term') {
      useStreamStore.getState().clearSession(HOST, code)
    }

    expect(useStreamStore.getState().sessions[`${HOST}:${code}`]).toBeUndefined()
  })

  it('handoff progress event updates handoffProgress by composite key', () => {
    const code = 'abc001'
    useStreamStore.getState().setHandoffProgress(HOST, code, 'detecting')
    expect(useStreamStore.getState().handoffProgress[`${HOST}:${code}`]).toBe('detecting')
  })

  it('handoff failed clears progress', () => {
    const code = 'abc001'
    useStreamStore.getState().setHandoffProgress(HOST, code, 'detecting')

    // Simulate failed event
    const value = 'failed:no CC running'
    if (value.startsWith('failed')) {
      useStreamStore.getState().setHandoffProgress(HOST, code, '')
    }

    expect(useStreamStore.getState().handoffProgress[`${HOST}:${code}`]).toBe('')
  })
})
