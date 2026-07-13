import { beforeEach, describe, expect, it, vi } from 'vitest'
import { createSession, listSessions } from '../host-api'
import type { Session } from '../host-api'
import type { SessionMeta } from './types'
import { ensureSessions } from './restore'

vi.mock('../host-api', async (importOriginal) => ({
  ...(await importOriginal<typeof import('../host-api')>()),
  listSessions: vi.fn(),
  createSession: vi.fn(),
}))

function session(overrides: Partial<Session> & { code: string }): Session {
  return {
    name: overrides.name ?? overrides.code,
    cwd: overrides.cwd ?? '/tmp',
    mode: overrides.mode ?? 'terminal',
    cc_session_id: '',
    cc_model: '',
    has_relay: false,
    ...overrides,
  }
}

function meta(overrides: Partial<SessionMeta> & { hostId: string; sessionCode: string }): SessionMeta {
  return {
    name: overrides.name ?? overrides.sessionCode,
    mode: overrides.mode ?? 'terminal',
    cwd: overrides.cwd ?? '/work',
    restorable: overrides.restorable ?? true,
    ...overrides,
  }
}

describe('ensureSessions', () => {
  beforeEach(() => {
    vi.mocked(listSessions).mockReset()
    vi.mocked(createSession).mockReset()
  })

  it('1. all sessions live → all reattached; createSession never called', async () => {
    const sessionMeta: Record<string, Record<string, SessionMeta>> = {
      hostA: {
        code1: meta({ hostId: 'hostA', sessionCode: 'code1' }),
        code2: meta({ hostId: 'hostA', sessionCode: 'code2' }),
      },
    }
    vi.mocked(listSessions).mockResolvedValue([
      session({ code: 'code1', name: 'live1' }),
      session({ code: 'code2', name: 'live2' }),
    ])

    const { remap, report } = await ensureSessions(sessionMeta)

    expect(listSessions).toHaveBeenCalledTimes(1)
    expect(createSession).not.toHaveBeenCalled()
    expect(remap.hostA.code1.status).toBe('reattached')
    expect(remap.hostA.code2.status).toBe('reattached')
    if (remap.hostA.code1.status === 'reattached') {
      expect(remap.hostA.code1.newCode).toBe('code1')
      expect(remap.hostA.code1.session.name).toBe('live1')
    }
    expect(report).toEqual({ reattached: 2, rebuilt: 0, failed: 0 })
  })

  it('2. rebuild scope: 3 restorable dead (incl orphans) + 1 live → createSession exactly 3', async () => {
    const sessionMeta: Record<string, Record<string, SessionMeta>> = {
      hostA: {
        live1: meta({ hostId: 'hostA', sessionCode: 'live1' }),
        dead1: meta({ hostId: 'hostA', sessionCode: 'dead1', name: 'd1', cwd: '/a' }),
        dead2: meta({ hostId: 'hostA', sessionCode: 'dead2', name: 'd2', cwd: '/b' }),
        orphan: meta({ hostId: 'hostA', sessionCode: 'orphan', name: 'd3', cwd: '/c' }),
      },
    }
    vi.mocked(listSessions).mockResolvedValue([session({ code: 'live1', name: 'live1' })])
    vi.mocked(createSession).mockImplementation(async (_h, name) =>
      session({ code: `new-${name}`, name }),
    )

    const { remap, report } = await ensureSessions(sessionMeta)

    expect(createSession).toHaveBeenCalledTimes(3)
    expect(remap.hostA.live1.status).toBe('reattached')
    expect(remap.hostA.dead1.status).toBe('rebuilt')
    expect(remap.hostA.dead2.status).toBe('rebuilt')
    expect(remap.hostA.orphan.status).toBe('rebuilt')
    expect(report).toEqual({ reattached: 1, rebuilt: 3, failed: 0 })
  })

  it('3. dead entry with restorable=false → failed; createSession not called for it', async () => {
    const sessionMeta: Record<string, Record<string, SessionMeta>> = {
      hostA: {
        dead: meta({ hostId: 'hostA', sessionCode: 'dead', restorable: false, cwd: undefined }),
      },
    }
    vi.mocked(listSessions).mockResolvedValue([])

    const { remap, report } = await ensureSessions(sessionMeta)

    expect(createSession).not.toHaveBeenCalled()
    expect(remap.hostA.dead.status).toBe('failed')
    expect(report).toEqual({ reattached: 0, rebuilt: 0, failed: 1 })
  })

  it('4. rebuild:false → dead entries failed, createSession never called; live still reattached', async () => {
    const sessionMeta: Record<string, Record<string, SessionMeta>> = {
      hostA: {
        live1: meta({ hostId: 'hostA', sessionCode: 'live1' }),
        dead1: meta({ hostId: 'hostA', sessionCode: 'dead1' }),
      },
    }
    vi.mocked(listSessions).mockResolvedValue([session({ code: 'live1', name: 'live1' })])

    const { remap, report } = await ensureSessions(sessionMeta, { rebuild: false })

    expect(createSession).not.toHaveBeenCalled()
    expect(remap.hostA.live1.status).toBe('reattached')
    expect(remap.hostA.dead1.status).toBe('failed')
    expect(report).toEqual({ reattached: 1, rebuilt: 0, failed: 1 })
  })

  it('5. createSession rejects for one entry → that entry failed, the rest proceed', async () => {
    const sessionMeta: Record<string, Record<string, SessionMeta>> = {
      hostA: {
        dead1: meta({ hostId: 'hostA', sessionCode: 'dead1', name: 'd1' }),
        dead2: meta({ hostId: 'hostA', sessionCode: 'dead2', name: 'd2' }),
      },
    }
    vi.mocked(listSessions).mockResolvedValue([])
    vi.mocked(createSession).mockImplementation(async (_h, name) => {
      if (name === 'd1') throw new Error('daemon boom')
      return session({ code: `new-${name}`, name })
    })

    const { remap, report } = await ensureSessions(sessionMeta)

    expect(createSession).toHaveBeenCalledTimes(2)
    expect(remap.hostA.dead1.status).toBe('failed')
    expect(remap.hostA.dead2.status).toBe('rebuilt')
    expect(report).toEqual({ reattached: 0, rebuilt: 1, failed: 1 })
  })

  it('6. host offline (listSessions throws) → every entry failed; createSession never called', async () => {
    const sessionMeta: Record<string, Record<string, SessionMeta>> = {
      hostA: {
        a: meta({ hostId: 'hostA', sessionCode: 'a' }),
        b: meta({ hostId: 'hostA', sessionCode: 'b' }),
      },
    }
    vi.mocked(listSessions).mockRejectedValue(new Error('host unreachable'))

    const { remap, report } = await ensureSessions(sessionMeta)

    expect(createSession).not.toHaveBeenCalled()
    expect(remap.hostA.a.status).toBe('failed')
    expect(remap.hostA.b.status).toBe('failed')
    expect(report).toEqual({ reattached: 0, rebuilt: 0, failed: 2 })
  })

  it('7. cross-host same code: A live (reattached), B dead (rebuilt), no contamination', async () => {
    const sessionMeta: Record<string, Record<string, SessionMeta>> = {
      A: { shared: meta({ hostId: 'A', sessionCode: 'shared', name: 'a-name' }) },
      B: { shared: meta({ hostId: 'B', sessionCode: 'shared', name: 'b-name', cwd: '/b' }) },
    }
    vi.mocked(listSessions).mockImplementation(async (hostId: string) => {
      if (hostId === 'A') return [session({ code: 'shared', name: 'a-live' })]
      if (hostId === 'B') return []
      throw new Error(`unexpected host ${hostId}`)
    })
    vi.mocked(createSession).mockImplementation(async (_h, name) =>
      session({ code: 'b-new-code', name }),
    )

    const { remap, report } = await ensureSessions(sessionMeta)

    expect(remap.A.shared.status).toBe('reattached')
    expect(remap.B.shared.status).toBe('rebuilt')
    if (remap.B.shared.status === 'rebuilt') {
      expect(remap.B.shared.newCode).toBe('b-new-code')
    }
    expect(remap.A.shared).not.toEqual(remap.B.shared)
    expect(report).toEqual({ reattached: 1, rebuilt: 1, failed: 0 })
  })

  it('8. daemon auto-rename (§8.1): trust returned object for newCode + name', async () => {
    const sessionMeta: Record<string, Record<string, SessionMeta>> = {
      hostA: {
        dead1: meta({ hostId: 'hostA', sessionCode: 'dead1', name: 'requested-name', cwd: '/w' }),
      },
    }
    vi.mocked(listSessions).mockResolvedValue([])
    vi.mocked(createSession).mockResolvedValue(
      session({ code: 'daemon-assigned-code', name: 'daemon-renamed' }),
    )

    const { remap } = await ensureSessions(sessionMeta)

    const entry = remap.hostA.dead1
    expect(entry.status).toBe('rebuilt')
    if (entry.status === 'rebuilt') {
      expect(entry.newCode).toBe('daemon-assigned-code')
      expect(entry.session.name).toBe('daemon-renamed')
      expect(entry.session.code).toBe('daemon-assigned-code')
    }
  })
})
