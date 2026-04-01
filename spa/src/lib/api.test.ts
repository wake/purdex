// spa/src/lib/api.test.ts
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { listSessions, createSession, deleteSession, switchMode, handoff, type Session } from './api'

const mockSession: Session = {
  code: 'abc123', name: 'test',
  cwd: '/tmp', mode: 'terminal', cc_session_id: '',
  cc_model: '', has_relay: false,
}

beforeEach(() => {
  vi.restoreAllMocks()
})

describe('listSessions', () => {
  it('returns sessions from API', async () => {
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(
      new Response(JSON.stringify([mockSession]), { status: 200 })
    )
    const sessions = await listSessions('http://localhost:7860')
    expect(sessions).toEqual([mockSession])
  })

  it('throws on error status', async () => {
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(
      new Response('error', { status: 500, statusText: 'Internal Server Error' })
    )
    await expect(listSessions('http://localhost:7860')).rejects.toThrow('500')
  })
})

describe('createSession', () => {
  it('posts and returns created session', async () => {
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(
      new Response(JSON.stringify(mockSession), { status: 201 })
    )
    const s = await createSession('http://localhost:7860', 'test', '/tmp', 'terminal')
    expect(s.name).toBe('test')
  })
})

describe('deleteSession', () => {
  it('sends DELETE request', async () => {
    const spy = vi.spyOn(globalThis, 'fetch').mockResolvedValue(
      new Response(null, { status: 204 })
    )
    await deleteSession('http://localhost:7860', 'abc123')
    expect(spy).toHaveBeenCalledWith(
      'http://localhost:7860/api/sessions/abc123',
      expect.objectContaining({ method: 'DELETE' })
    )
  })
})

describe('switchMode', () => {
  it('sends POST request with mode and returns updated session', async () => {
    const updated: Session = { ...mockSession, mode: 'stream' }
    const spy = vi.spyOn(globalThis, 'fetch').mockResolvedValue(
      new Response(JSON.stringify(updated), { status: 200 })
    )
    const result = await switchMode('http://localhost:7860', 'abc123', 'stream')
    expect(result.mode).toBe('stream')
    expect(spy).toHaveBeenCalledWith(
      'http://localhost:7860/api/sessions/abc123/mode',
      expect.objectContaining({
        method: 'POST',
        headers: expect.objectContaining({ 'Content-Type': 'application/json' }),
        body: JSON.stringify({ mode: 'stream' }),
      })
    )
  })

  it('throws on error status', async () => {
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(
      new Response('error', { status: 400, statusText: 'Bad Request' })
    )
    await expect(switchMode('http://localhost:7860', 'abc123', 'invalid')).rejects.toThrow('400')
  })
})

describe('handoff', () => {
  it('sends POST with mode and preset', async () => {
    const spy = vi.spyOn(globalThis, 'fetch').mockResolvedValue(
      new Response(JSON.stringify({ handoff_id: 'abc123' }), { status: 202 })
    )
    const result = await handoff('http://localhost:7860', 'abc123', 'stream', 'cc')
    expect(result.handoff_id).toBe('abc123')
    expect(spy).toHaveBeenCalledWith(
      'http://localhost:7860/api/sessions/abc123/handoff',
      expect.objectContaining({
        method: 'POST',
        body: JSON.stringify({ mode: 'stream', preset: 'cc' }),
      })
    )
  })

  it('omits preset when not provided', async () => {
    const spy = vi.spyOn(globalThis, 'fetch').mockResolvedValue(
      new Response(JSON.stringify({ handoff_id: 'def456' }), { status: 202 })
    )
    await handoff('http://localhost:7860', 'abc123', 'terminal')
    expect(spy).toHaveBeenCalledWith(
      'http://localhost:7860/api/sessions/abc123/handoff',
      expect.objectContaining({
        body: JSON.stringify({ mode: 'terminal' }),
      })
    )
  })

  it('throws with status and response text on error', async () => {
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(
      new Response('preset not found', { status: 400 })
    )
    await expect(handoff('http://localhost:7860', 'abc123', 'stream', 'bad'))
      .rejects.toThrow('handoff failed: 400 preset not found')
  })

  it('throws gracefully when error response body is unreadable', async () => {
    const badResponse = new Response(null, { status: 500 })
    vi.spyOn(badResponse, 'text').mockRejectedValue(new Error('body consumed'))
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(badResponse)
    await expect(handoff('http://localhost:7860', 'abc123', 'stream'))
      .rejects.toThrow('handoff failed: 500')
  })
})

describe('agentUpload', () => {
  it('sends multipart form and returns result', async () => {
    const mockResponse = { filename: 'test.png', injected: true }
    globalThis.fetch = vi.fn().mockResolvedValue({
      ok: true,
      json: () => Promise.resolve(mockResponse),
    })

    const { agentUpload } = await import('./api')
    const file = new File(['data'], 'test.png', { type: 'image/png' })
    const result = await agentUpload('http://localhost:7860', file, 'dev001')

    expect(result).toEqual(mockResponse)
    expect(globalThis.fetch).toHaveBeenCalledWith(
      'http://localhost:7860/api/agent/upload',
      expect.objectContaining({ method: 'POST' }),
    )

    // Verify FormData contents.
    const call = (globalThis.fetch as ReturnType<typeof vi.fn>).mock.calls[0]
    const body = call[1].body as FormData
    expect(body.get('session')).toBe('dev001')
    expect(body.get('file')).toBeInstanceOf(File)
  })

  it('throws on non-ok response', async () => {
    globalThis.fetch = vi.fn().mockResolvedValue({
      ok: false,
      status: 404,
      statusText: 'Not Found',
    })

    const { agentUpload } = await import('./api')
    const file = new File(['data'], 'test.png')
    await expect(agentUpload('http://localhost:7860', file, 'dev001')).rejects.toThrow('404')
  })
})
