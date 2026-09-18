import { describe, it, expect, vi, beforeEach } from 'vitest'
import { launchSession, MAX_GENERATED_NAME_RETRIES, SEND_UNSUPPORTED, type LaunchDeps } from './session-launch'
import { HostApiError, type Session } from './host-api'
import { GenerationConflictError } from './rebuild/transport'
import { useHostStore } from '../stores/useHostStore'
import { useSessionStore } from '../stores/useSessionStore'
import type { HostCommand, HostProject } from './host-config-api'

const H = 'h1'
const PROJECT: HostProject = { id: 'p1', name: 'Purdex', slug: 'purdex', path: '~/w/purdex' }
const COMMAND: HostCommand = { id: 'c1', name: 'Claude', command: 'claude', icon: { kind: 'agent', value: 'cc-bot' } }

function session(over: Partial<Session> = {}): Session {
  return { code: 'abc', name: 'purdex-1', cwd: '~/w/purdex', mode: 'terminal', tmux_instance: '111:1000', ...over }
}

function fakePin() {
  const createSession = vi.fn<(name: string, cwd: string, mode: string) => Promise<Session>>(
    async (name) => session({ name }),
  )
  const sendKeys = vi.fn<(code: string, command: string, expected: string) => Promise<void>>(async () => {})
  const pin = vi.fn((_hostId: string) => ({ createSession, sendKeys }))
  return { pin, createSession, sendKeys }
}

beforeEach(() => {
  useSessionStore.setState({ sessions: {} })
})

describe('launchSession', () => {
  it('Enter path: typed name, cwd ~, terminal, no send', async () => {
    const f = fakePin()
    const out = await launchSession(H, { name: ' dev ' }, { pin: f.pin })
    expect(f.pin).toHaveBeenCalledWith(H)
    expect(f.createSession).toHaveBeenCalledWith('dev', '~', 'terminal')
    expect(f.sendKeys).not.toHaveBeenCalled()
    expect(out).toMatchObject({ status: 'created', session: { name: 'dev' } })
  })

  it('empty or invalid typed name without a project creates nothing', async () => {
    const f = fakePin()
    expect(await launchSession(H, { name: '  ' }, { pin: f.pin })).toMatchObject({ status: 'failed', reason: 'invalid_name' })
    expect(await launchSession(H, { name: 'a b' }, { pin: f.pin })).toMatchObject({ status: 'failed', reason: 'invalid_name' })
    expect(await launchSession(H, { name: 'bad/x', project: PROJECT }, { pin: f.pin })).toMatchObject({ status: 'failed', reason: 'invalid_name' })
    expect(f.createSession).not.toHaveBeenCalled()
  })

  it('project + command: generated name, raw project path, guarded send with the created generation', async () => {
    const f = fakePin()
    const out = await launchSession(H, { name: '', project: PROJECT, command: COMMAND }, {
      pin: f.pin, liveNames: () => ['purdex-1', 'other'],
    })
    expect(f.createSession).toHaveBeenCalledWith('purdex-2', '~/w/purdex', 'terminal')
    expect(f.sendKeys).toHaveBeenCalledWith('abc', 'claude', '111:1000')
    expect(out).toEqual({ status: 'created', session: session({ name: 'purdex-2' }) })
  })

  it('project name only: cwd launch, no send; a typed name wins over generation', async () => {
    const f = fakePin()
    await launchSession(H, { name: 'mine', project: PROJECT }, { pin: f.pin })
    expect(f.createSession).toHaveBeenCalledWith('mine', '~/w/purdex', 'terminal')
    expect(f.sendKeys).not.toHaveBeenCalled()
  })

  it('reads live names from the session store by default', async () => {
    useSessionStore.setState({ sessions: { [H]: [session({ name: 'purdex' })] } })
    const f = fakePin()
    await launchSession(H, { name: '', project: PROJECT }, { pin: f.pin })
    expect(f.createSession).toHaveBeenCalledWith('purdex-2', '~/w/purdex', 'terminal')
  })

  it('409 on a generated name retries with the next N, up to the cap', async () => {
    const f = fakePin()
    f.createSession
      .mockRejectedValueOnce(new HostApiError(409, 'Conflict'))
      .mockRejectedValueOnce(new HostApiError(409, 'Conflict'))
    const out = await launchSession(H, { name: '', project: PROJECT }, { pin: f.pin, liveNames: () => [] })
    expect(f.createSession.mock.calls.map(([n]) => n)).toEqual(['purdex-1', 'purdex-2', 'purdex-3'])
    expect(out).toMatchObject({ status: 'created' })

    // Persistent 409: one initial attempt plus MAX_GENERATED_NAME_RETRIES retries.
    const g = fakePin()
    g.createSession.mockRejectedValue(new HostApiError(409, 'Conflict'))
    const capped = await launchSession(H, { name: '', project: PROJECT }, { pin: g.pin, liveNames: () => [] })
    expect(g.createSession.mock.calls.map(([n]) => n)).toEqual([
      'purdex-1', 'purdex-2', 'purdex-3', 'purdex-4', 'purdex-5', 'purdex-6',
    ])
    expect(g.createSession).toHaveBeenCalledTimes(1 + MAX_GENERATED_NAME_RETRIES)
    expect(capped).toMatchObject({ status: 'failed', reason: 'create_failed', error: '409 Conflict' })
  })

  it('a refused name is remembered, so each retry advances past names the cache never knew', async () => {
    const f = fakePin()
    // The daemon knows `purdex-4` and `purdex-5`; the cached list knows neither,
    // so a retry that only recounts the cache would offer `purdex-4` forever.
    f.createSession.mockImplementation(async (name) => {
      if (name === 'purdex-4' || name === 'purdex-5') throw new HostApiError(409, 'Conflict')
      return session({ name })
    })
    const out = await launchSession(H, { name: '', project: PROJECT }, {
      pin: f.pin, liveNames: () => ['purdex-1', 'purdex-3'],
    })
    expect(f.createSession.mock.calls.map(([n]) => n)).toEqual(['purdex-4', 'purdex-5', 'purdex-6'])
    expect(out).toMatchObject({ status: 'created', session: { name: 'purdex-6' } })
  })

  it('409 on a typed name is not retried; 400/500 are never retried', async () => {
    const f = fakePin()
    f.createSession.mockRejectedValue(new HostApiError(409, 'Conflict'))
    expect(await launchSession(H, { name: 'dev' }, { pin: f.pin })).toMatchObject({ status: 'failed', reason: 'create_failed', error: '409 Conflict' })
    expect(f.createSession).toHaveBeenCalledTimes(1)

    const g = fakePin()
    g.createSession.mockRejectedValue(new HostApiError(500, 'Internal Server Error'))
    await launchSession(H, { name: '', project: PROJECT }, { pin: g.pin, liveNames: () => [] })
    expect(g.createSession).toHaveBeenCalledTimes(1)
  })

  it('a blank code from create is a failure', async () => {
    const f = fakePin()
    f.createSession.mockResolvedValue(session({ code: '' }))
    expect(await launchSession(H, { name: 'dev' }, { pin: f.pin })).toMatchObject({ status: 'failed', reason: 'create_failed' })
  })

  it('send failure keeps the session and reports sendError', async () => {
    const f = fakePin()
    f.sendKeys.mockRejectedValue(new GenerationConflictError('abc', '111:1000'))
    const out = await launchSession(H, { name: '', project: PROJECT, command: COMMAND }, { pin: f.pin, liveNames: () => [] })
    expect(out.status).toBe('created')
    expect(out.status === 'created' && out.sendError).toMatch(/tmux generation/)
  })

  it('a session with no tmux_instance (old daemon) is never sent to: the unsupported marker, no send at all', async () => {
    for (const generation of [undefined, '']) {
      const f = fakePin()
      f.createSession.mockResolvedValue(session({ tmux_instance: generation }))
      const out = await launchSession(H, { name: 'dev', project: PROJECT, command: COMMAND }, { pin: f.pin })
      // The transport rejects a send with no generation to assert; that raw
      // internal error must never reach the user.
      expect(f.sendKeys).not.toHaveBeenCalled()
      expect(out).toEqual({ status: 'created', session: session({ tmux_instance: generation }), sendError: SEND_UNSUPPORTED })
    }
  })

  it('a real send failure stays distinguishable from the unsupported marker', async () => {
    const f = fakePin()
    f.sendKeys.mockRejectedValue(new GenerationConflictError('abc', '111:1000'))
    const out = await launchSession(H, { name: 'dev', project: PROJECT, command: COMMAND }, { pin: f.pin })
    expect(out.status === 'created' && out.sendError).not.toBe(SEND_UNSUPPORTED)
  })

  it('an unknown host fails before any request (real pinHost)', async () => {
    useHostStore.setState({ hosts: {}, hostOrder: [], activeHostId: null })
    const fetchSpy = vi.spyOn(globalThis, 'fetch')
    const out = await launchSession('ghost', { name: 'dev' }, {} as LaunchDeps)
    expect(out).toMatchObject({ status: 'failed', reason: 'host' })
    expect(fetchSpy).not.toHaveBeenCalled()
    fetchSpy.mockRestore()
  })
})
