// spa/src/lib/api.test.ts
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { listSessions, createSession, deleteSession, type Session } from './api'

const mockSession: Session = {
  id: 1, name: 'test', tmux_target: 'test:0',
  cwd: '/tmp', mode: 'term', group_id: 0, sort_order: 0,
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
    const s = await createSession('http://localhost:7860', 'test', '/tmp', 'term')
    expect(s.name).toBe('test')
  })
})

describe('deleteSession', () => {
  it('sends DELETE request', async () => {
    const spy = vi.spyOn(globalThis, 'fetch').mockResolvedValue(
      new Response(null, { status: 204 })
    )
    await deleteSession('http://localhost:7860', 1)
    expect(spy).toHaveBeenCalledWith(
      'http://localhost:7860/api/sessions/1',
      expect.objectContaining({ method: 'DELETE' })
    )
  })
})
