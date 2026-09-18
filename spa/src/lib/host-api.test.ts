// spa/src/lib/host-api.test.ts — Unit tests for host-aware API functions
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { useHostStore } from '../stores/useHostStore'
import {
  listSessions, createSession, deleteSession,
  fetchSessionCwd, fetchSessionProvenance, fetchSessionHome, getConfig, updateConfig, agentUpload,
  fetchMonitorSnapshot, fetchMonitorConfig, updateMonitorConfig, fetchPeers,
  type MonitorSnapshot, type Session,
} from './host-api'
import { indexPeerRows } from '../stores/usePeerStore'

const HOST_ID = 'test-host'
const BASE = 'http://100.64.0.2:7860'
const TOKEN = 'purdex_test_token'

const mockSession: Session = {
  code: 'abc123', name: 'test', cwd: '/tmp', mode: 'terminal',
  cc_session_id: '', cc_model: '', has_relay: false,
}

beforeEach(() => {
  vi.restoreAllMocks()
  // Set up host store with test host that has a token
  useHostStore.setState({
    hosts: { [HOST_ID]: { id: HOST_ID, name: 'Test', ip: '100.64.0.2', port: 7860, token: TOKEN, order: 0 } },
    hostOrder: [HOST_ID],
  })
})

/** Helper: assert fetch was called with correct base + auth header */
function expectAuthFetch(url: string, init?: { method?: string; body?: string }) {
  const calls = (globalThis.fetch as ReturnType<typeof vi.fn>).mock.calls
  expect(calls.length).toBeGreaterThan(0)
  const [calledUrl, calledInit] = calls[calls.length - 1]
  expect(calledUrl).toBe(url)
  if (init?.method) expect(calledInit.method).toBe(init.method)
  // Check auth header
  const headers = calledInit.headers as Headers
  expect(headers.get('Authorization')).toBe(`Bearer ${TOKEN}`)
}

describe('listSessions', () => {
  it('fetches sessions with auth', async () => {
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(
      new Response(JSON.stringify([mockSession]), { status: 200 }),
    )
    const result = await listSessions(HOST_ID)
    expect(result).toEqual([mockSession])
    expectAuthFetch(`${BASE}/api/sessions`)
  })

  it('throws on error status', async () => {
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(
      new Response('error', { status: 500, statusText: 'Internal Server Error' }),
    )
    await expect(listSessions(HOST_ID)).rejects.toThrow('500')
  })
})

describe('createSession', () => {
  it('posts with auth and returns session', async () => {
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(
      new Response(JSON.stringify(mockSession), { status: 201 }),
    )
    const result = await createSession(HOST_ID, 'test', '/tmp', 'terminal')
    expect(result.name).toBe('test')
    expectAuthFetch(`${BASE}/api/sessions`, { method: 'POST' })
  })
})

describe('deleteSession', () => {
  it('sends DELETE with auth', async () => {
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(
      new Response(null, { status: 204 }),
    )
    await deleteSession(HOST_ID, 'abc123')
    expectAuthFetch(`${BASE}/api/sessions/abc123`, { method: 'DELETE' })
  })
})

describe('getConfig', () => {
  it('fetches config with auth', async () => {
    const config = { bind: '0.0.0.0', port: 7860, detect: { cc_commands: [], poll_interval: 5 } }
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(
      new Response(JSON.stringify(config), { status: 200 }),
    )
    const result = await getConfig(HOST_ID)
    expect(result.port).toBe(7860)
    expectAuthFetch(`${BASE}/api/config`)
  })

  it('throws on error', async () => {
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(
      new Response('error', { status: 500 }),
    )
    await expect(getConfig(HOST_ID)).rejects.toThrow('get config failed: 500')
  })
})

describe('updateConfig', () => {
  it('sends PUT with auth', async () => {
    const config = { bind: '0.0.0.0', port: 7860, detect: { cc_commands: [], poll_interval: 5 } }
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(
      new Response(JSON.stringify(config), { status: 200 }),
    )
    const result = await updateConfig(HOST_ID, { bind: '0.0.0.0' })
    expect(result.port).toBe(7860)
    expectAuthFetch(`${BASE}/api/config`, { method: 'PUT' })
  })
})

describe('agentUpload', () => {
  it('sends multipart form with auth', async () => {
    const mockResponse = { filename: 'test.png', injected: true }
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(
      new Response(JSON.stringify(mockResponse), { status: 200 }),
    )
    const file = new File(['data'], 'test.png', { type: 'image/png' })
    const result = await agentUpload(HOST_ID, file, 'dev001')
    expect(result).toEqual(mockResponse)
    expectAuthFetch(`${BASE}/api/agent/upload`, { method: 'POST' })

    // Verify FormData contents
    const call = (globalThis.fetch as ReturnType<typeof vi.fn>).mock.calls[0]
    const body = call[1].body as FormData
    expect(body.get('session')).toBe('dev001')
    expect(body.get('file')).toBeInstanceOf(File)
  })

  it('throws on non-ok response', async () => {
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(
      new Response('error', { status: 404, statusText: 'Not Found' }),
    )
    const file = new File(['data'], 'test.png')
    await expect(agentUpload(HOST_ID, file, 'dev001')).rejects.toThrow('404')
  })
})

describe('fetchSessionCwd', () => {
  it('returns cwd and the generation it was sampled in', async () => {
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(
      new Response(JSON.stringify({ cwd: '/home/user/proj', tmux_instance: '222:2000' }), { status: 200 }),
    )
    const res = await fetchSessionCwd(HOST_ID, 'abc123')
    expect(res).toEqual({ cwd: '/home/user/proj', tmuxInstance: '222:2000' })
    expectAuthFetch(`${BASE}/api/sessions/abc123/cwd`)
  })

  it('reports an absent generation as unknown, never as a match', async () => {
    // An older daemon does not send the field at all; "" is what the caller
    // compares against, and "" never equals a real generation.
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(
      new Response(JSON.stringify({ cwd: '/home/user/proj' }), { status: 200 }),
    )
    expect(await fetchSessionCwd(HOST_ID, 'abc123')).toEqual({
      cwd: '/home/user/proj', tmuxInstance: '',
    })
  })

  it('throws on non-ok response', async () => {
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(
      new Response('nope', { status: 500 }),
    )
    await expect(fetchSessionCwd(HOST_ID, 'abc123')).rejects.toThrow('500')
  })
})

describe('fetchSessionProvenance', () => {
  it('returns the owning agent and the generation it was sampled in', async () => {
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(
      new Response(JSON.stringify({
        found: true, agent_type: 'cc', session_id: 'fa657572',
        cwd: '/home/user/proj', tmux_pane_id: '%12',
        tmux_instance: '222:2000', last_seen_at: 1788800000000,
      }), { status: 200 }),
    )
    expect(await fetchSessionProvenance(HOST_ID, 'abc123')).toEqual({
      found: true, agentType: 'cc', sessionId: 'fa657572',
      cwd: '/home/user/proj', tmuxPaneId: '%12',
      tmuxInstance: '222:2000', lastSeenAt: 1788800000000,
    })
    expectAuthFetch(`${BASE}/api/sessions/abc123/provenance`)
  })

  it('fills the omitted fields of a not-found answer', async () => {
    // The daemon omits every `omitempty` field when it found no owner, and an
    // absent generation must read as '' — never as a match.
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(
      new Response(JSON.stringify({ found: false }), { status: 200 }),
    )
    expect(await fetchSessionProvenance(HOST_ID, 'abc123')).toEqual({
      found: false, agentType: '', sessionId: '', cwd: '', tmuxPaneId: '',
      tmuxInstance: '', lastSeenAt: 0,
    })
  })

  it('throws on non-ok response', async () => {
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(new Response('nope', { status: 500 }))
    await expect(fetchSessionProvenance(HOST_ID, 'abc123')).rejects.toThrow('500')
  })
})

describe('fetchSessionHome', () => {
  it('returns home string from /api/sessions/{code}/home', async () => {
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(
      new Response(JSON.stringify({ home: '/Users/wake' }), { status: 200 }),
    )
    const home = await fetchSessionHome(HOST_ID, 'abc123')
    expect(home).toBe('/Users/wake')
    expectAuthFetch(`${BASE}/api/sessions/abc123/home`)
  })

  it('throws on non-ok response', async () => {
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(
      new Response('nope', { status: 500 }),
    )
    await expect(fetchSessionHome(HOST_ID, 'abc123')).rejects.toThrow('500')
  })
})

describe('monitor api', () => {
  const monitorSnapshot: MonitorSnapshot = {
    sampled_at: 180000,
    host: {
      cpu: { percent: null, unavailable_reason: 'pending' },
      memory: { total_bytes: 1024, used_bytes: 256, used_percent: 25, unavailable_reason: null },
      disk: { total_bytes: 4096, used_bytes: 1024, used_percent: 25, unavailable_reason: null },
    },
    sessions: [
      {
        session_code: 'abc123',
        tmux_session: { id: '$1', name: 'work' },
        daemon: {
          cpu_percent: null,
          memory_bytes: 2048,
          process_count: 2,
          unavailable_reason: null,
          top_processes: [
            { pid: 101, ppid: 1, command: 'shell', cpu_percent: 1.5, memory_bytes: 1024 },
          ],
        },
      },
    ],
    config: {
      refresh_interval_ms: 5000,
      top_process_limit: 10,
      bounds: {
        refresh_interval_ms: { min: 1000, max: 60000 },
        top_process_limit: { min: 1, max: 50 },
      },
    },
  }

  it('fetches monitor snapshot with auth', async () => {
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(
      new Response(JSON.stringify(monitorSnapshot), { status: 200 }),
    )

    const result = await fetchMonitorSnapshot(HOST_ID)

    expect(result).toEqual(monitorSnapshot)
    expectAuthFetch(`${BASE}/api/monitor/snapshot`)
  })

  it('throws when monitor snapshot request fails', async () => {
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(
      new Response('error', { status: 503 }),
    )

    await expect(fetchMonitorSnapshot(HOST_ID)).rejects.toThrow('fetchMonitorSnapshot failed: 503')
  })

  it('fetches monitor config with auth', async () => {
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(
      new Response(JSON.stringify(monitorSnapshot.config), { status: 200 }),
    )

    const result = await fetchMonitorConfig(HOST_ID)

    expect(result).toEqual(monitorSnapshot.config)
    expectAuthFetch(`${BASE}/api/monitor/config`)
  })

  it('updates monitor config with JSON body', async () => {
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(
      new Response(JSON.stringify({ ...monitorSnapshot.config, top_process_limit: 25 }), { status: 200 }),
    )

    const result = await updateMonitorConfig(HOST_ID, { top_process_limit: 25 })

    expect(result.top_process_limit).toBe(25)
    expectAuthFetch(`${BASE}/api/monitor/config`, { method: 'PUT' })
    const call = (globalThis.fetch as ReturnType<typeof vi.fn>).mock.calls[0]
    expect((call[1].headers as Headers).get('Content-Type')).toBe('application/json')
    expect(JSON.parse(call[1].body)).toEqual({ top_process_limit: 25 })
  })

  it('throws when monitor config requests fail', async () => {
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(
      new Response('error', { status: 500 }),
    )

    await expect(fetchMonitorConfig(HOST_ID)).rejects.toThrow('fetchMonitorConfig failed: 500')
    await expect(updateMonitorConfig(HOST_ID, { refresh_interval_ms: 2000 }))
      .rejects.toThrow('updateMonitorConfig failed: 500')
  })
})

describe('hostFetch auth header', () => {
  it('includes Bearer token when host has token', async () => {
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(
      new Response(JSON.stringify([]), { status: 200 }),
    )
    await listSessions(HOST_ID)
    const call = (globalThis.fetch as ReturnType<typeof vi.fn>).mock.calls[0]
    const headers = call[1].headers as Headers
    expect(headers.get('Authorization')).toBe(`Bearer ${TOKEN}`)
  })

  it('omits Bearer token when host has no token', async () => {
    useHostStore.setState({
      hosts: { [HOST_ID]: { id: HOST_ID, name: 'Test', ip: '100.64.0.2', port: 7860, order: 0 } },
    })
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(
      new Response(JSON.stringify([]), { status: 200 }),
    )
    await listSessions(HOST_ID)
    const call = (globalThis.fetch as ReturnType<typeof vi.fn>).mock.calls[0]
    const headers = call[1].headers as Headers
    expect(headers.get('Authorization')).toBeNull()
  })
})

describe('fetchPeers', () => {
  // The `pdx peers --json` contract this feature reads: two session rows plus a
  // synthetic entry row. If the daemon renames a field, this fails here rather
  // than silently rendering blanks in the status bar.
  //
  // NOT a verbatim capture. It began as one from the mini-lab daemon at
  // 1.0.0-alpha.364 and every address-bearing field has been hand-written to
  // the v4 shape since: a six-digit `ref` in place of the eight-digit
  // `canonical`, `<host>/<name>` addresses in place of the retired
  // `<host>/<canonical>:<suffix>`, no `suffix`, no `'default'` title source.
  // Re-capture it against a v4 daemon when one is deployed — until then the
  // shapes below are asserted rather than observed, which is what the
  // v3-shape guard at the end of this block is for.
  const realEnvelope = {
    host_id: 'mini-lab:278cbm',
    ok: true,
    partial: false,
    peers: [
      {
        host: 'mini-lab',
        host_id: 'mini-lab:278cbm',
        address: 'mini-lab/tmux:ai-chat2',
        row_kind: 'session',
        ref: '',
        title: '',
        title_source: '',
        title_rev: 0,
        session_code: 'qorh3k',
        session_name: 'ai-chat2',
        tmux_instance: '6901:1789205013',
        cwd: '/Users/wake/Workspace/wake/ai-chat-story',
        agent: null,
        deliverable: false,
        reason: 'no_agent',
      },
      {
        host: 'mini-lab',
        host_id: 'mini-lab:278cbm',
        address: 'mini-lab/ai-chat-story-3a',
        row_kind: 'session',
        ref: '_3k9f2m',
        title: 'ai-chat4',
        title_source: 'user',
        title_rev: 0,
        session_code: 'z141yl',
        session_name: 'ai-chat4',
        tmux_instance: '6901:1789205013',
        cwd: '/Users/wake/Workspace/wake/ai-chat-story',
        agent: {
          type: 'cc',
          session_id: 'f5ddb1d6-be90-4b36-9927-a260075c3b0f',
          peer_name: 'ai-chat-story-3a',
          pid: 42603,
          proc_start: 'Sun Sep 13 18:57:56 2026',
          inbox: '/tmp/cc-socks/42603.sock',
          status: 'idle',
          version: '2.1.270',
        },
        deliverable: true,
        reason: '',
      },
      {
        host: 'mini-lab',
        host_id: 'mini-lab:278cbm',
        address: 'mini-lab/outside-tmux',
        row_kind: 'entry',
        ref: '_7p2wq5',
        title: 'loose',
        title_source: 'user',
        title_rev: 7,
        session_code: '',
        session_name: '',
        tmux_instance: '',
        agent: {
          type: 'cc',
          session_id: '11111111-2222-3333-4444-555555555555',
          peer_name: 'outside-tmux',
          pid: 999,
          proc_start: 'Mon Sep 15 10:00:00 2026',
          inbox: '/tmp/cc-socks/999.sock',
          status: 'busy',
          version: '2.1.273',
        },
        deliverable: true,
        reason: '',
      },
    ],
    daemon_version: '1.0.0-alpha.364',
    unknown_registry_files: [],
    titles_unavailable: false,
  }

  // The guard the comment above points at. A fixture in the retired v3 shape
  // passes every other assertion in this block and proves only that the code
  // echoes whatever it was handed — so the shapes themselves are asserted.
  // `_` + exactly six base36 digits for a ref; `<host>/<name>`,
  // `<host>/_<ref>` or `<host>/tmux:<name>` for an address, and nothing else.
  it('holds v4 refs and v4 addresses, not the v3 shapes it was captured in', () => {
    for (const row of realEnvelope.peers) {
      if (row.ref !== '') expect(row.ref).toMatch(/^_[0-9a-z]{6}$/)
      expect(row.address).toMatch(/^[a-z0-9][a-z0-9.-]*\/(tmux:.+|_[0-9a-z]{6}|[a-z0-9][a-z0-9-]*)$/)
    }
  })

  it('fetches /api/peers with auth and returns the envelope', async () => {
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(
      new Response(JSON.stringify(realEnvelope), { status: 200 }),
    )
    const env = await fetchPeers(HOST_ID)
    expect(env.host_id).toBe('mini-lab:278cbm')
    expect(env.peers).toHaveLength(3)
    expectAuthFetch(`${BASE}/api/peers`)
  })

  it('a real envelope parses into the fields the store reads', async () => {
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(
      new Response(JSON.stringify(realEnvelope), { status: 200 }),
    )
    const env = await fetchPeers(HOST_ID)
    expect(env.partial).toBe(false)
    expect(env.titles_unavailable).toBe(false)
    expect(env.unknown_registry_files).toEqual([])
    expect(indexPeerRows(env.peers)).toEqual({
      qorh3k: {
        address: 'mini-lab/tmux:ai-chat2',
        ref: '',
        title: '',
        titleSource: '',
        deliverable: false,
        reason: 'no_agent',
        tmuxInstance: '6901:1789205013',
        agent: null,
      },
      z141yl: {
        address: 'mini-lab/ai-chat-story-3a',
        ref: '_3k9f2m',
        title: 'ai-chat4',
        titleSource: 'user',
        deliverable: true,
        reason: '',
        tmuxInstance: '6901:1789205013',
        agent: { type: 'cc', peerName: 'ai-chat-story-3a', status: 'idle' },
      },
    })
  })

  it('throws on error status', async () => {
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(
      new Response('nope', { status: 503 }),
    )
    await expect(fetchPeers(HOST_ID)).rejects.toThrow('fetchPeers failed: 503')
  })
})
