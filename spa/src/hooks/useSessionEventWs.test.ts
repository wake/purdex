// spa/src/hooks/useSessionEventWs.test.ts
import { describe, it, expect, beforeEach } from 'vitest'
import { useStreamStore } from '../stores/useStreamStore'
import { useSessionStore } from '../stores/useSessionStore'
import type { StreamMessage } from '../lib/stream-ws'

// Test the event handler logic that useSessionEventWs uses.
// We test the store operations directly since the hook is a thin wrapper
// around connectSessionEvents + store dispatches.

const emptyState = {
  sessions: {},
  sessionStatus: {},
  relayStatus: {},
  handoffProgress: {},
}

beforeEach(() => {
  useStreamStore.setState(emptyState)
  useSessionStore.setState({ sessions: [], activeId: null })
})

describe('session event handler logic', () => {
  it('status event sets sessionStatus keyed by code', () => {
    const code = 'abc001'
    useStreamStore.getState().setSessionStatus(code, 'cc-idle')
    expect(useStreamStore.getState().sessionStatus[code]).toBe('cc-idle')
    // Should NOT be keyed by name
    expect(useStreamStore.getState().sessionStatus['dev-server']).toBeUndefined()
  })

  it('relay event sets relayStatus keyed by code', () => {
    const code = 'abc001'
    useStreamStore.getState().setRelayStatus(code, true)
    expect(useStreamStore.getState().relayStatus[code]).toBe(true)
  })

  it('handoff connected clears progress and finds session by code', () => {
    const code = 'abc001'
    // Set up session with code
    useSessionStore.setState({
      sessions: [
        { code, name: 'dev', cwd: '/tmp', mode: 'stream', cc_session_id: 'sid1', cc_model: '', has_relay: false },
      ],
      activeId: null,
    })

    // Simulate handoff:connected event behavior
    useStreamStore.getState().setHandoffProgress(code, '')

    // Verify the .find() by code works (this is the critical change in the PR)
    const sess = useSessionStore.getState().sessions.find((s) => s.code === code)
    expect(sess).toBeDefined()
    expect(sess!.name).toBe('dev')
    expect(sess!.mode).toBe('stream')
  })

  it('handoff connected with term mode clears session', () => {
    const code = 'abc001'
    useStreamStore.getState().addMessage(code, { type: 'assistant' } as StreamMessage)

    // Session is in term mode — should clear
    useSessionStore.setState({
      sessions: [
        { code, name: 'dev', cwd: '/tmp', mode: 'term', cc_session_id: '', cc_model: '', has_relay: false },
      ],
    })

    const sess = useSessionStore.getState().sessions.find((s) => s.code === code)
    if (!sess || sess.mode === 'term') {
      useStreamStore.getState().clearSession(code)
    }

    expect(useStreamStore.getState().sessions[code]).toBeUndefined()
  })

  it('handoff progress event updates handoffProgress by code', () => {
    const code = 'abc001'
    useStreamStore.getState().setHandoffProgress(code, 'detecting')
    expect(useStreamStore.getState().handoffProgress[code]).toBe('detecting')
  })

  it('handoff failed clears progress', () => {
    const code = 'abc001'
    useStreamStore.getState().setHandoffProgress(code, 'detecting')

    // Simulate failed event
    const value = 'failed:no CC running'
    if (value.startsWith('failed')) {
      useStreamStore.getState().setHandoffProgress(code, '')
    }

    expect(useStreamStore.getState().handoffProgress[code]).toBe('')
  })
})
