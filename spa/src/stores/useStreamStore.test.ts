// spa/src/stores/useStreamStore.test.ts
import { beforeEach, describe, expect, it } from 'vitest'
import { useStreamStore } from './useStreamStore'
import type { StreamMessage, ControlRequest, StreamConnection } from '../lib/stream-ws'

const H = 'test-host'

const emptyState = {
  sessions: {},
  relayStatus: {},
  handoffProgress: {},
}

describe('useStreamStore (per-session)', () => {
  beforeEach(() => {
    useStreamStore.setState(emptyState)
  })

  it('has empty sessions by default', () => {
    expect(useStreamStore.getState().sessions).toEqual({})
  })

  it('addMessage creates session lazily and appends', () => {
    const { addMessage } = useStreamStore.getState()
    const msg = { type: 'assistant' } as StreamMessage
    addMessage(H, 'sess-a', msg)
    const state = useStreamStore.getState().sessions[`${H}:sess-a`]
    expect(state).toBeDefined()
    expect(state.messages).toHaveLength(1)
    expect(state.messages[0]).toBe(msg)
  })

  it('messages are independent per session', () => {
    const { addMessage } = useStreamStore.getState()
    addMessage(H, 'sess-a', { type: 'user' } as StreamMessage)
    addMessage(H, 'sess-b', { type: 'assistant' } as StreamMessage)
    expect(useStreamStore.getState().sessions[`${H}:sess-a`].messages).toHaveLength(1)
    expect(useStreamStore.getState().sessions[`${H}:sess-b`].messages).toHaveLength(1)
  })

  it('setConn stores per session', () => {
    const { setConn } = useStreamStore.getState()
    const mockConn = { send: () => {}, close: () => {} } as unknown as StreamConnection
    setConn(H, 'sess-a', mockConn)
    expect(useStreamStore.getState().sessions[`${H}:sess-a`].conn).toBe(mockConn)
    expect(useStreamStore.getState().sessions[`${H}:sess-b`]?.conn).toBeUndefined()
  })

  it('setStreaming per session', () => {
    const { setStreaming } = useStreamStore.getState()
    setStreaming(H, 'sess-a', true)
    expect(useStreamStore.getState().sessions[`${H}:sess-a`].isStreaming).toBe(true)
  })

  it('loadHistory sets messages for session', () => {
    const { loadHistory } = useStreamStore.getState()
    const msgs = [{ type: 'user' } as StreamMessage, { type: 'assistant' } as StreamMessage]
    loadHistory(H, 'sess-a', msgs)
    expect(useStreamStore.getState().sessions[`${H}:sess-a`].messages).toEqual(msgs)
  })

  it('loadHistory does not overwrite existing messages if already present', () => {
    const { addMessage, loadHistory } = useStreamStore.getState()
    addMessage(H, 'sess-a', { type: 'user' } as StreamMessage)
    // loadHistory replaces messages (initial load from JSONL)
    loadHistory(H, 'sess-a', [{ type: 'assistant' } as StreamMessage])
    expect(useStreamStore.getState().sessions[`${H}:sess-a`].messages).toHaveLength(1)
    expect(useStreamStore.getState().sessions[`${H}:sess-a`].messages[0].type).toBe('assistant')
  })

  it('clearSession closes conn and removes state', () => {
    const { setConn, addMessage, clearSession } = useStreamStore.getState()
    let closed = false
    const mockConn = { send: () => {}, close: () => { closed = true } } as unknown as StreamConnection
    setConn(H, 'sess-a', mockConn)
    addMessage(H, 'sess-a', { type: 'user' } as StreamMessage)
    clearSession(H, 'sess-a')
    expect(closed).toBe(true)
    expect(useStreamStore.getState().sessions[`${H}:sess-a`]).toBeUndefined()
  })

  it('addControlRequest and resolveControlRequest per session', () => {
    const { addControlRequest, resolveControlRequest } = useStreamStore.getState()
    const req = { request_id: 'r1', request: { subtype: 'permission' } } as ControlRequest
    addControlRequest(H, 'sess-a', req)
    expect(useStreamStore.getState().sessions[`${H}:sess-a`].pendingControlRequests).toHaveLength(1)
    resolveControlRequest(H, 'sess-a', 'r1')
    expect(useStreamStore.getState().sessions[`${H}:sess-a`].pendingControlRequests).toHaveLength(0)
  })

  it('setSessionInfo per session', () => {
    const { setSessionInfo } = useStreamStore.getState()
    setSessionInfo(H, 'sess-a', 'cc-uuid', 'opus-4')
    const info = useStreamStore.getState().sessions[`${H}:sess-a`].sessionInfo
    expect(info.ccSessionId).toBe('cc-uuid')
    expect(info.model).toBe('opus-4')
  })

  it('addCost per session', () => {
    const { addCost } = useStreamStore.getState()
    addCost(H, 'sess-a', 0.5)
    addCost(H, 'sess-a', 0.3)
    expect(useStreamStore.getState().sessions[`${H}:sess-a`].cost).toBe(0.8)
  })

  it('handoffProgress is per-session', () => {
    const { setHandoffProgress } = useStreamStore.getState()
    setHandoffProgress(H, 'sess-a', 'detecting')
    setHandoffProgress(H, 'sess-b', 'launching')
    expect(useStreamStore.getState().handoffProgress[`${H}:sess-a`]).toBe('detecting')
    expect(useStreamStore.getState().handoffProgress[`${H}:sess-b`]).toBe('launching')
  })

  it('relayStatus is per-session', () => {
    const { setRelayStatus } = useStreamStore.getState()
    setRelayStatus(H, 'sess-a', true)
    setRelayStatus(H, 'sess-b', false)
    expect(useStreamStore.getState().relayStatus[`${H}:sess-a`]).toBe(true)
    expect(useStreamStore.getState().relayStatus[`${H}:sess-b`]).toBe(false)
  })

})
