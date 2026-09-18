// spa/src/lib/nex/handoff-api.test.ts — P-C.3b task 1: the SPA wrappers for
// the daemon's nex-handoff / nex-takeback endpoints and the error type every
// caller switches on. The daemon error-code table (plan "Measured baseline")
// is the contract these tests pin.
import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { useHostStore } from '../../stores/useHostStore'
import { HandoffApiError, nexHandoff, nexTakeback, nexTakeToTerminal } from './handoff-api'
import { NEX_CLIENT_ID_RE } from './client-id'

const testGlobal = globalThis as typeof globalThis & { fetch: ReturnType<typeof vi.fn> }

function json(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } })
}

async function rejection(p: Promise<unknown>): Promise<HandoffApiError> {
  try {
    await p
  } catch (e) {
    expect(e).toBeInstanceOf(HandoffApiError)
    return e as HandoffApiError
  }
  throw new Error('expected rejection')
}

const takebackBody = { expected_tmux_instance: 'i', execution_id: 'e', resume_command: 'r {id}' }
const toTerminalBody = { session_name: 'purdex-3', resume_command: 'claude --resume {id}' }
const toTerminalOk = { session: { code: 'ab12cd', name: 'purdex-3', cwd: '/w', mode: 'terminal', tmux_instance: '1:2' }, session_id: 'sid-1', archived: true }

describe('handoff-api', () => {
  let hostId: string

  beforeEach(() => {
    useHostStore.getState().reset()
    hostId = useHostStore.getState().addHost({ id: 'host-mlab', name: 'mlab', ip: '100.64.0.2', port: 7860, token: 'tok-1' })
    vi.stubGlobal('fetch', vi.fn())
  })
  afterEach(() => vi.unstubAllGlobals())

  describe('nexHandoff', () => {
    it('POSTs JSON to /api/sessions/{code}/nex-handoff with Bearer + X-Pdx-Client', async () => {
      testGlobal.fetch.mockResolvedValueOnce(json({ execution_id: 'exc_1', state: 'running', effective_profile: 'handoff', session_id: 'sid-1', cwd: '/w' }))
      await nexHandoff(hostId, 'zk16vd', { expected_tmux_instance: 'inst-1', profile: 'handoff', rollback_command: 'claude --resume {id}' })
      expect(testGlobal.fetch).toHaveBeenCalledTimes(1)
      const [url, init] = testGlobal.fetch.mock.calls[0]
      expect(url).toBe('http://100.64.0.2:7860/api/sessions/zk16vd/nex-handoff')
      expect(init.method).toBe('POST')
      expect(JSON.parse(init.body)).toEqual({ expected_tmux_instance: 'inst-1', profile: 'handoff', rollback_command: 'claude --resume {id}' })
      const h = new Headers(init.headers)
      expect(h.get('Content-Type')).toBe('application/json')
      expect(h.get('Authorization')).toBe('Bearer tok-1')
      expect(h.get('X-Pdx-Client')).toMatch(NEX_CLIENT_ID_RE)
    })

    it('encodes the session code in the path', async () => {
      testGlobal.fetch.mockResolvedValueOnce(json({ execution_id: 'e', state: 'running', session_id: 's', cwd: '/' }))
      await nexHandoff(hostId, 'a b/c', { expected_tmux_instance: 'i' })
      const [url] = testGlobal.fetch.mock.calls[0]
      expect(url).toBe('http://100.64.0.2:7860/api/sessions/a%20b%2Fc/nex-handoff')
    })

    it('sends only the fields given (no undefined optionals on the wire)', async () => {
      testGlobal.fetch.mockResolvedValueOnce(json({ execution_id: 'e', state: 'running', session_id: 's', cwd: '/' }))
      await nexHandoff(hostId, 'c1', { expected_tmux_instance: 'i' })
      const [, init] = testGlobal.fetch.mock.calls[0]
      expect(JSON.parse(init.body)).toEqual({ expected_tmux_instance: 'i' })
    })

    it('200 is parsed into NexHandoffResult', async () => {
      testGlobal.fetch.mockResolvedValueOnce(json({ execution_id: 'exc_1', state: 'running', effective_profile: 'handoff', session_id: 'sid-1', cwd: '/w' }))
      const r = await nexHandoff(hostId, 'c1', { expected_tmux_instance: 'i' })
      expect(r).toEqual({ execution_id: 'exc_1', state: 'running', effective_profile: 'handoff', session_id: 'sid-1', cwd: '/w' })
    })

    it('409 delegate_rejected keeps reject_reason / rolled_back / session_id / infra_error in body', async () => {
      testGlobal.fetch.mockResolvedValueOnce(json({
        error: 'delegate rejected', code: 'delegate_rejected',
        reject_reason: 'cwd outside roots', rolled_back: true, session_id: 'sid-1', infra_error: 'store: boom',
      }, 409))
      const err = await rejection(nexHandoff(hostId, 'c1', { expected_tmux_instance: 'i' }))
      expect(err.status).toBe(409)
      expect(err.code).toBe('delegate_rejected')
      expect(err.message).toBe('delegate rejected')
      expect(err.body).toMatchObject({ reject_reason: 'cwd outside roots', rolled_back: true, session_id: 'sid-1', infra_error: 'store: boom' })
    })

    it('409 tmux_instance_mismatch after exit keeps after_exit / rolled_back / session_id', async () => {
      testGlobal.fetch.mockResolvedValueOnce(json({ error: 'mismatch', code: 'tmux_instance_mismatch', after_exit: true, rolled_back: false, session_id: 'sid-2' }, 409))
      const err = await rejection(nexHandoff(hostId, 'c1', { expected_tmux_instance: 'i' }))
      expect(err.code).toBe('tmux_instance_mismatch')
      expect(err.body).toMatchObject({ after_exit: true, rolled_back: false, session_id: 'sid-2' })
    })

    it('504 cc_exit_timeout keeps step', async () => {
      testGlobal.fetch.mockResolvedValueOnce(json({ error: 'timeout', code: 'cc_exit_timeout', step: 'exit' }, 504))
      const err = await rejection(nexHandoff(hostId, 'c1', { expected_tmux_instance: 'i' }))
      expect(err.status).toBe(504)
      expect(err.code).toBe('cc_exit_timeout')
      expect(err.body.step).toBe('exit')
    })

    it('unparseable 500 → code http_500 with an empty body', async () => {
      testGlobal.fetch.mockResolvedValueOnce(new Response('<html>boom</html>', { status: 500 }))
      const err = await rejection(nexHandoff(hostId, 'c1', { expected_tmux_instance: 'i' }))
      expect(err.status).toBe(500)
      expect(err.code).toBe('http_500')
      expect(err.body).toEqual({})
    })

    it('JSON error without a string code → http_<status>, body kept', async () => {
      testGlobal.fetch.mockResolvedValueOnce(json({ error: 'nope' }, 503))
      const err = await rejection(nexHandoff(hostId, 'c1', { expected_tmux_instance: 'i' }))
      expect(err.code).toBe('http_503')
      expect(err.message).toBe('nope')
      expect(err.body).toEqual({ error: 'nope' })
    })

    it('network failure (fetch rejection) → HandoffApiError(0, "network", {})', async () => {
      testGlobal.fetch.mockRejectedValueOnce(new TypeError('Failed to fetch'))
      const err = await rejection(nexHandoff(hostId, 'c1', { expected_tmux_instance: 'i' }))
      expect(err.status).toBe(0)
      expect(err.code).toBe('network')
      expect(err.message).toBe('Failed to fetch')
      expect(err.body).toEqual({})
    })
  })

  describe('nexTakeback', () => {
    it('POSTs JSON to /api/sessions/{code}/nex-takeback with Bearer + X-Pdx-Client', async () => {
      testGlobal.fetch.mockResolvedValueOnce(json({ session_id: 'sid-1', archived: true }))
      await nexTakeback(hostId, 'zk16vd', { expected_tmux_instance: 'inst-1', execution_id: 'exc_1', resume_command: 'claude --resume {id}', lease_id: 'ls_1' })
      const [url, init] = testGlobal.fetch.mock.calls[0]
      expect(url).toBe('http://100.64.0.2:7860/api/sessions/zk16vd/nex-takeback')
      expect(init.method).toBe('POST')
      expect(JSON.parse(init.body)).toEqual({ expected_tmux_instance: 'inst-1', execution_id: 'exc_1', resume_command: 'claude --resume {id}', lease_id: 'ls_1' })
      const h = new Headers(init.headers)
      expect(h.get('Content-Type')).toBe('application/json')
      expect(h.get('Authorization')).toBe('Bearer tok-1')
      expect(h.get('X-Pdx-Client')).toMatch(NEX_CLIENT_ID_RE)
    })

    it('omits lease_id when not given', async () => {
      testGlobal.fetch.mockResolvedValueOnce(json({ session_id: 'sid-1', archived: false }))
      await nexTakeback(hostId, 'c1', takebackBody)
      const [, init] = testGlobal.fetch.mock.calls[0]
      expect(JSON.parse(init.body)).toEqual(takebackBody)
    })

    it('200 is parsed into NexTakebackResult', async () => {
      testGlobal.fetch.mockResolvedValueOnce(json({ session_id: 'sid-1', archived: false }))
      const r = await nexTakeback(hostId, 'c1', takebackBody)
      expect(r).toEqual({ session_id: 'sid-1', archived: false })
    })

    it('409 held_by keeps principal', async () => {
      testGlobal.fetch.mockResolvedValueOnce(json({ error: 'lease held', code: 'held_by', principal: 'air/t-abc' }, 409))
      const err = await rejection(nexTakeback(hostId, 'c1', takebackBody))
      expect(err.status).toBe(409)
      expect(err.code).toBe('held_by')
      expect(err.body.principal).toBe('air/t-abc')
    })

    it('409 execution_not_bound keeps execution_id / session_code', async () => {
      testGlobal.fetch.mockResolvedValueOnce(json({ error: 'not bound', code: 'execution_not_bound', execution_id: 'e', session_code: 'c1' }, 409))
      const err = await rejection(nexTakeback(hostId, 'c1', takebackBody))
      expect(err.code).toBe('execution_not_bound')
      expect(err.body).toMatchObject({ execution_id: 'e', session_code: 'c1' })
    })

    it('504 cc_start_timeout keeps session_id', async () => {
      testGlobal.fetch.mockResolvedValueOnce(json({ error: 'timeout', code: 'cc_start_timeout', session_id: 'sid-9' }, 504))
      const err = await rejection(nexTakeback(hostId, 'c1', takebackBody))
      expect(err.status).toBe(504)
      expect(err.code).toBe('cc_start_timeout')
      expect(err.body.session_id).toBe('sid-9')
    })

    it('unparseable 500 → http_500; network → network', async () => {
      testGlobal.fetch.mockResolvedValueOnce(new Response('', { status: 500 }))
      let err = await rejection(nexTakeback(hostId, 'c1', takebackBody))
      expect(err).toMatchObject({ status: 500, code: 'http_500', body: {} })

      testGlobal.fetch.mockRejectedValueOnce(new TypeError('Failed to fetch'))
      err = await rejection(nexTakeback(hostId, 'c1', takebackBody))
      expect(err).toMatchObject({ status: 0, code: 'network', body: {} })
    })
  })

  describe('nexTakeToTerminal (exec-to-terminal spec §4.1)', () => {
    it('POSTs JSON to /api/nex/executions/{id}/take-to-terminal with Bearer + X-Pdx-Client', async () => {
      testGlobal.fetch.mockResolvedValueOnce(json(toTerminalOk))
      await nexTakeToTerminal(hostId, 'exc_1', { ...toTerminalBody, lease_id: 'ls_1' })
      const [url, init] = testGlobal.fetch.mock.calls[0]
      expect(url).toBe('http://100.64.0.2:7860/api/nex/executions/exc_1/take-to-terminal')
      expect(init.method).toBe('POST')
      expect(JSON.parse(init.body)).toEqual({ session_name: 'purdex-3', resume_command: 'claude --resume {id}', lease_id: 'ls_1' })
      const h = new Headers(init.headers)
      expect(h.get('Content-Type')).toBe('application/json')
      expect(h.get('Authorization')).toBe('Bearer tok-1')
      expect(h.get('X-Pdx-Client')).toMatch(NEX_CLIENT_ID_RE)
    })

    it('encodes the execution id in the path and omits lease_id when not given', async () => {
      testGlobal.fetch.mockResolvedValueOnce(json(toTerminalOk))
      await nexTakeToTerminal(hostId, 'a b/c', toTerminalBody)
      const [url, init] = testGlobal.fetch.mock.calls[0]
      expect(url).toBe('http://100.64.0.2:7860/api/nex/executions/a%20b%2Fc/take-to-terminal')
      expect(JSON.parse(init.body)).toEqual(toTerminalBody)
    })

    it('200 is parsed into NexTakeToTerminalResult (session + session_id + archived)', async () => {
      testGlobal.fetch.mockResolvedValueOnce(json(toTerminalOk))
      const r = await nexTakeToTerminal(hostId, 'exc_1', toTerminalBody)
      expect(r).toEqual(toTerminalOk)
    })

    it('409 session_exists keeps session_name', async () => {
      testGlobal.fetch.mockResolvedValueOnce(json({ error: 'exists', code: 'session_exists', session_name: 'purdex-3' }, 409))
      const err = await rejection(nexTakeToTerminal(hostId, 'exc_1', toTerminalBody))
      expect(err.status).toBe(409)
      expect(err.code).toBe('session_exists')
      expect(err.body.session_name).toBe('purdex-3')
    })

    it('500 session_create_failed keeps session_name / session_alive', async () => {
      testGlobal.fetch.mockResolvedValueOnce(json({ error: 'list failed', code: 'session_create_failed', session_name: 'purdex-3', session_alive: true }, 500))
      const err = await rejection(nexTakeToTerminal(hostId, 'exc_1', toTerminalBody))
      expect(err.status).toBe(500)
      expect(err.code).toBe('session_create_failed')
      expect(err.body).toMatchObject({ session_name: 'purdex-3', session_alive: true })
    })

    it('504 cc_start_timeout keeps session_id; network → network', async () => {
      testGlobal.fetch.mockResolvedValueOnce(json({ error: 'timeout', code: 'cc_start_timeout', session_id: 'sid-9' }, 504))
      let err = await rejection(nexTakeToTerminal(hostId, 'exc_1', toTerminalBody))
      expect(err).toMatchObject({ status: 504, code: 'cc_start_timeout' })
      expect(err.body.session_id).toBe('sid-9')

      testGlobal.fetch.mockRejectedValueOnce(new TypeError('Failed to fetch'))
      err = await rejection(nexTakeToTerminal(hostId, 'exc_1', toTerminalBody))
      expect(err).toMatchObject({ status: 0, code: 'network', body: {} })
    })

    it('against a host id the store no longer holds → host_removed, no fetch', async () => {
      useHostStore.getState().addHost({ id: 'host-other', name: 'other', ip: '100.64.0.4', port: 7860, token: 'tok-2' })
      useHostStore.getState().removeHost(hostId)
      const err = await rejection(nexTakeToTerminal(hostId, 'exc_1', toTerminalBody))
      expect(err).toMatchObject({ status: 0, code: 'host_removed', body: {} })
      expect(testGlobal.fetch).not.toHaveBeenCalled()
    })
  })

  describe('removed host (R1-3/A1)', () => {
    // A pane can outlive its host entry: the handoff / take-back must not
    // fall through hostFetch's "any host" fallback onto another daemon.
    it('nexHandoff against a host id the store no longer holds → host_removed, no fetch', async () => {
      useHostStore.getState().addHost({ id: 'host-other', name: 'other', ip: '100.64.0.4', port: 7860, token: 'tok-2' })
      useHostStore.getState().removeHost(hostId)
      const err = await rejection(nexHandoff(hostId, 'c1', { expected_tmux_instance: 'i' }))
      expect(err).toMatchObject({ status: 0, code: 'host_removed', body: {} })
      expect(testGlobal.fetch).not.toHaveBeenCalled()
    })

    it('nexTakeback against a host id the store no longer holds → host_removed, no fetch', async () => {
      useHostStore.getState().addHost({ id: 'host-other', name: 'other', ip: '100.64.0.4', port: 7860, token: 'tok-2' })
      useHostStore.getState().removeHost(hostId)
      const err = await rejection(nexTakeback(hostId, 'c1', takebackBody))
      expect(err).toMatchObject({ status: 0, code: 'host_removed', body: {} })
      expect(testGlobal.fetch).not.toHaveBeenCalled()
    })

    it('an unknown host id (never added) is refused the same way', async () => {
      const err = await rejection(nexHandoff('host-never', 'c1', { expected_tmux_instance: 'i' }))
      expect(err.code).toBe('host_removed')
      expect(testGlobal.fetch).not.toHaveBeenCalled()
    })
  })

  it('HandoffApiError is an Error with name/status/code/body', () => {
    const e = new HandoffApiError(409, 'no_cc', { error: 'x', code: 'no_cc' }, 'x')
    expect(e).toBeInstanceOf(Error)
    expect(e.name).toBe('HandoffApiError')
    expect(e.status).toBe(409)
    expect(e.code).toBe('no_cc')
    expect(e.message).toBe('x')
    expect(e.body).toEqual({ error: 'x', code: 'no_cc' })
  })
})
