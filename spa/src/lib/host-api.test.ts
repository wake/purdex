// spa/src/lib/host-api.test.ts — Unit tests for host-aware API functions
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { useHostStore } from '../stores/useHostStore'
import {
  listSessions, createSession, deleteSession, switchMode,
  handoff, fetchHistory, getConfig, updateConfig, agentUpload,
  fetchAgentHookStatus, setupAgentHook,
  type Session,
} from './host-api'

const HOST_ID = 'test-host'
const BASE = 'http://100.64.0.2:7860'
const TOKEN = 'tbox_test_token'

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

describe('switchMode', () => {
  it('sends POST with auth and returns session', async () => {
    const updated = { ...mockSession, mode: 'stream' }
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(
      new Response(JSON.stringify(updated), { status: 200 }),
    )
    const result = await switchMode(HOST_ID, 'abc123', 'stream')
    expect(result.mode).toBe('stream')
    expectAuthFetch(`${BASE}/api/sessions/abc123/mode`, { method: 'POST' })
  })

  it('throws on error status', async () => {
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(
      new Response('error', { status: 400, statusText: 'Bad Request' }),
    )
    await expect(switchMode(HOST_ID, 'abc123', 'invalid')).rejects.toThrow('400')
  })
})

describe('handoff', () => {
  it('sends POST with mode and preset', async () => {
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(
      new Response(JSON.stringify({ handoff_id: 'abc123' }), { status: 202 }),
    )
    const result = await handoff(HOST_ID, 'abc123', 'stream', 'cc')
    expect(result.handoff_id).toBe('abc123')
    expectAuthFetch(`${BASE}/api/sessions/abc123/handoff`, { method: 'POST' })
  })

  it('omits preset when not provided', async () => {
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(
      new Response(JSON.stringify({ handoff_id: 'def456' }), { status: 202 }),
    )
    await handoff(HOST_ID, 'abc123', 'terminal')
    const call = (globalThis.fetch as ReturnType<typeof vi.fn>).mock.calls[0]
    expect(JSON.parse(call[1].body)).toEqual({ mode: 'terminal' })
  })

  it('throws with status and response text on error', async () => {
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(
      new Response('preset not found', { status: 400 }),
    )
    await expect(handoff(HOST_ID, 'abc123', 'stream', 'bad'))
      .rejects.toThrow('handoff failed: 400 preset not found')
  })

  it('throws gracefully when error response body is unreadable', async () => {
    const badResponse = new Response(null, { status: 500 })
    vi.spyOn(badResponse, 'text').mockRejectedValue(new Error('body consumed'))
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(badResponse)
    await expect(handoff(HOST_ID, 'abc123', 'stream'))
      .rejects.toThrow('handoff failed: 500')
  })
})

describe('fetchHistory', () => {
  it('fetches history with auth', async () => {
    const msgs = [{ type: 'text', content: 'hello' }]
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(
      new Response(JSON.stringify(msgs), { status: 200 }),
    )
    const result = await fetchHistory(HOST_ID, 'abc123')
    expect(result).toEqual(msgs)
    expectAuthFetch(`${BASE}/api/sessions/abc123/history`)
  })

  it('returns empty array on error', async () => {
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(
      new Response('error', { status: 500 }),
    )
    const result = await fetchHistory(HOST_ID, 'abc123')
    expect(result).toEqual([])
  })
})

describe('getConfig', () => {
  it('fetches config with auth', async () => {
    const config = { bind: '0.0.0.0', port: 7860, stream: { presets: [] }, detect: { cc_commands: [], poll_interval: 5 } }
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
    const config = { bind: '0.0.0.0', port: 7860, stream: { presets: [] }, detect: { cc_commands: [], poll_interval: 5 } }
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

describe('fetchAgentHookStatus', () => {
  it('fetches with auth', async () => {
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(
      new Response(JSON.stringify({ installed: true }), { status: 200 }),
    )
    const res = await fetchAgentHookStatus(HOST_ID)
    expect(res.ok).toBe(true)
    expectAuthFetch(`${BASE}/api/agent/hook-status`)
  })
})

describe('setupAgentHook', () => {
  it('sends POST with auth', async () => {
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(
      new Response(JSON.stringify({ installed: true }), { status: 200 }),
    )
    const res = await setupAgentHook(HOST_ID, 'cc', 'install')
    expect(res.ok).toBe(true)
    expectAuthFetch(`${BASE}/api/agent/hook-setup`, { method: 'POST' })
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
