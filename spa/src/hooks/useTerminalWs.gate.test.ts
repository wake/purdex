// spa/src/hooks/useTerminalWs.gate.test.ts — the attach gate (spec §4.6).
//
// A tmux-session pane must not open its terminal WS until a `sessions` payload
// from the current host-connection epoch has been processed, otherwise it can
// attach to a stranger that reused the session code after a tmux restart.
// `canReconnect` only guards the *retry* path (ws.ts:44); the first connect
// runs unconditionally, so the gate has to sit on both.
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { createElement, type RefObject } from 'react'
import { render, act, waitFor } from '@testing-library/react'
import type { Terminal } from '@xterm/xterm'
import type { FitAddon } from '@xterm/addon-fit'
import { canReconnectTerminal, useTerminalWs } from './useTerminalWs'
import { useHostStore } from '../stores/useHostStore'

const HOST = 'h1'

const ctor = vi.fn()

function fakeTerm() {
  return {
    cols: 80,
    rows: 24,
    write: vi.fn(),
    focus: vi.fn(),
    onData: vi.fn(() => ({ dispose: vi.fn() })),
    onResize: vi.fn(() => ({ dispose: vi.fn() })),
  } as unknown as Terminal
}

interface PaneRefs {
  term: RefObject<Terminal | null>
  fit: RefObject<FitAddon | null>
  container: RefObject<HTMLDivElement | null>
}

/** Plain ref objects, built outside React so the harness never touches a ref
 *  during render. The container only needs to be a real element the hook can
 *  query and attach listeners to, so a detached one is enough. */
function makeRefs(): PaneRefs {
  return {
    term: { current: fakeTerm() },
    fit: { current: { fit: vi.fn() } as unknown as FitAddon },
    container: { current: document.createElement('div') },
  }
}

interface HarnessProps {
  hostId: string
  wsUrl: string
  refs: PaneRefs
  getTicket?: () => Promise<string>
}

function Harness({ hostId, wsUrl, refs, getTicket }: HarnessProps) {
  useTerminalWs({
    wsUrl,
    termRef: refs.term,
    fitAddonRef: refs.fit,
    containerRef: refs.container,
    hostId,
    onReady: () => {},
    onDisconnect: () => {},
    onReconnect: () => {},
    getTicket,
  })
  return createElement('div')
}

function renderTerminalPane(hostId: string, sessionCode: string, getTicket?: () => Promise<string>) {
  return render(createElement(Harness, {
    hostId,
    wsUrl: `ws://1.2.3.4:7860/ws/terminal/${sessionCode}`,
    refs: makeRefs(),
    getTicket,
  }))
}

function seedHost(attachReady: boolean) {
  useHostStore.setState({
    hosts: { [HOST]: { id: HOST, name: 'Host', ip: '1.2.3.4', port: 7860, order: 0 } },
    hostOrder: [HOST],
    runtime: { [HOST]: { status: 'connected', attachReady } },
    activeHostId: HOST,
  })
}

beforeEach(() => {
  ctor.mockClear()
  vi.stubGlobal('WebSocket', class {
    static OPEN = 1
    url: string
    readyState = 0
    binaryType = ''
    onopen: (() => void) | null = null
    onclose: (() => void) | null = null
    onmessage: ((e: { data: unknown }) => void) | null = null
    onerror: (() => void) | null = null
    constructor(url: string) { this.url = url; ctor(url) }
    send() {}
    close() {}
  } as never)
})

afterEach(() => {
  vi.unstubAllGlobals()
  useHostStore.getState().reset()
})

describe('useTerminalWs attach gate', () => {
  it('constructs no terminal socket until the gate opens', async () => {
    seedHost(false)
    renderTerminalPane(HOST, 'abc123')
    expect(ctor).not.toHaveBeenCalled()

    act(() => { useHostStore.getState().setRuntime(HOST, { attachReady: true }) })
    await waitFor(() => expect(ctor).toHaveBeenCalledTimes(1))
  })

  it('does not attach when the gate closed while the ticket was in flight', async () => {
    seedHost(true)
    let releaseTicket!: (t: string) => void
    const getTicket = () => new Promise<string>((r) => { releaseTicket = r })
    renderTerminalPane(HOST, 'abc123', getTicket)

    act(() => { useHostStore.getState().setRuntime(HOST, { attachReady: false }) })
    releaseTicket('tk')
    await new Promise((r) => setTimeout(r, 0))

    expect(ctor).not.toHaveBeenCalled()
  })

  it('attaches immediately when the gate is already open', async () => {
    seedHost(true)
    renderTerminalPane(HOST, 'abc123')
    await waitFor(() => expect(ctor).toHaveBeenCalledTimes(1))
  })

  // Host ownership (#1400 attacker review, deferred to H1b): the first connect checks that the host exists BEFORE it
  // asks for a ticket, as a reconnect does — a ticket for a host this device lacks would come from another daemon.
  describe('a host this device does not have', () => {
    it.each(['gone-host', 'toString', '__proto__'])('%s: the first connect asks for no ticket and opens no socket', async (id) => {
      useHostStore.setState({
        hosts: { [HOST]: { id: HOST, name: 'Host', ip: '1.2.3.4', port: 7860, order: 0 } },
        hostOrder: [HOST],
        activeHostId: HOST,
        // a stale runtime row keeps the attach gate open for that id
        runtime: { [HOST]: { status: 'connected', attachReady: true }, [id]: { status: 'connected', attachReady: true } } as never,
      })
      const getTicket = vi.fn(async () => 'tk')
      renderTerminalPane(id, 'abc123', getTicket)
      await new Promise((r) => setTimeout(r, 0))
      expect(getTicket).not.toHaveBeenCalled()
      expect(ctor).not.toHaveBeenCalled()
    })

    it.each(['gone-host', 'toString', 'constructor', '__proto__'])('canReconnectTerminal(%s) is false for a host not configured, even with an open gate', (id) => {
      useHostStore.setState({ hosts: {}, hostOrder: [], runtime: { [id]: { status: 'connected', attachReady: true } } as never })
      expect(canReconnectTerminal(id)).toBe(false)
    })

    it('a host named like a prototype member, deleted while its ticket is in flight, is not attached', async () => {
      const id = 'toString'
      useHostStore.setState({
        hosts: { [id]: { id, name: 'Odd', ip: '1.2.3.4', port: 7860, order: 0 } },
        hostOrder: [id],
        activeHostId: id,
        runtime: { [id]: { status: 'connected', attachReady: true } } as never,
      })
      let releaseTicket!: (t: string) => void
      const getTicket = () => new Promise<string>((r) => { releaseTicket = r })
      renderTerminalPane(id, 'abc123', getTicket)
      act(() => { useHostStore.setState({ hosts: {}, hostOrder: [] }) })
      releaseTicket('tk')
      await new Promise((r) => setTimeout(r, 0))
      expect(ctor).not.toHaveBeenCalled()
    })
  })
})
