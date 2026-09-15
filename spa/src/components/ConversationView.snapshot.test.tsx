// spa/src/components/ConversationView.snapshot.test.tsx
// I7 guard: the ConversationMessages extraction must not change Stream
// mode's DOM. This snapshot is taken BEFORE the refactor and must stay
// byte-identical after it.
import { describe, it, expect, beforeEach } from 'vitest'
import { render, cleanup, act } from '@testing-library/react'
import ConversationView from './ConversationView'
import { useStreamStore } from '../stores/useStreamStore'
import type { StreamMessage } from '../lib/stream-ws'

const HOST = 'snap-host'
const SESSION = 'snap-session'

const FIXTURE: StreamMessage[] = [
  { type: 'assistant', message: { role: 'assistant', content: [
    { type: 'thinking', thinking: 'let me think' },
    { type: 'text', text: 'Hello **world**' },
    { type: 'tool_use', id: 'tu1', name: 'Read', input: { file_path: '/a.ts' } },
  ], stop_reason: null } },
  { type: 'user', message: { role: 'user', content: [
    { type: 'tool_result', tool_use_id: 'tu1', content: 'file body', is_error: false },
  ], stop_reason: null } },
  { type: 'user', message: { role: 'user', content: [{ type: 'text', text: '[Request interrupted by user]' }], stop_reason: null } },
  { type: 'user', message: { role: 'user', content: [{ type: 'text', text: '/compact' }], stop_reason: null } },
  { type: 'user', message: { role: 'user', content: [{ type: 'text', text: 'plain question' }], stop_reason: null } },
  { type: 'user', message: { role: 'user', content: [{ type: 'tool_result', tool_use_id: 'tu2', content: 'boom', is_error: true }], stop_reason: null } },
] as StreamMessage[]

beforeEach(() => {
  cleanup()
  useStreamStore.setState({ sessions: {}, relayStatus: {}, handoffProgress: {} })
})

describe('ConversationView DOM snapshot (I7)', () => {
  it('connected relay with mixed messages', () => {
    const { container } = render(<ConversationView hostId={HOST} sessionCode={SESSION} />)
    act(() => {
      useStreamStore.getState().setRelayStatus(HOST, SESSION, true)
      for (const m of FIXTURE) useStreamStore.getState().addMessage(HOST, SESSION, m)
    })
    expect(container.innerHTML).toMatchSnapshot()
  })

  it('connected relay, empty, not streaming (waiting hint)', () => {
    const { container } = render(<ConversationView hostId={HOST} sessionCode={SESSION} />)
    act(() => { useStreamStore.getState().setRelayStatus(HOST, SESSION, true) })
    expect(container.innerHTML).toMatchSnapshot()
  })

  it('connected relay, streaming with no assistant message (thinking indicator)', () => {
    const { container } = render(<ConversationView hostId={HOST} sessionCode={SESSION} />)
    act(() => {
      useStreamStore.getState().setRelayStatus(HOST, SESSION, true)
      useStreamStore.getState().setStreaming(HOST, SESSION, true)
    })
    expect(container.innerHTML).toMatchSnapshot()
  })

  it('connected relay with a pending permission prompt', () => {
    const { container } = render(<ConversationView hostId={HOST} sessionCode={SESSION} />)
    act(() => {
      useStreamStore.getState().setRelayStatus(HOST, SESSION, true)
      useStreamStore.getState().addControlRequest(HOST, SESSION, {
        type: 'control_request', request_id: 'r1',
        request: { subtype: 'can_use_tool', tool_name: 'Bash', input: { command: 'ls' } },
      })
    })
    expect(container.innerHTML).toMatchSnapshot()
  })
})
