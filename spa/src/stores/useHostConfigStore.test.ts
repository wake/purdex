import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { emptyHostConfigEntry, useHostConfigStore } from './useHostConfigStore'
import { useHostStore } from './useHostStore'
import * as api from '../lib/host-config-api'
import { HostConfigApiError, HostConfigConflictError } from '../lib/host-config-api'

vi.mock('../lib/host-config-api', async (importOriginal) => ({
  ...(await importOriginal<typeof import('../lib/host-config-api')>()),
  fetchHostConfig: vi.fn(),
  putHostConfig: vi.fn(),
}))

const H = 'h1'
const payload = {
  projects: { items: [{ id: 'p1', name: 'Purdex', slug: 'purdex', path: '~/w/purdex' }], revision: 3 },
  commands: { items: [], revision: 0 },
  resumeTemplates: { items: { cc: { exact: 'cld --resume {id}', fallback: 'cld -c' } }, revision: 1 },
}

function registerHost(ip = '1.2.3.4', port = 7860, token: string | null = 't') {
  useHostStore.setState({
    hosts: { [H]: { id: H, name: 'mlab', ip, port, token, order: 0 } },
    hostOrder: [H],
    runtime: { [H]: { status: 'connected' } },
  })
}

/** A fetch nobody has answered yet, plus the switch that answers it. */
function pendingFetch() {
  let settle!: (p: unknown) => void
  vi.mocked(api.fetchHostConfig).mockReturnValueOnce(new Promise<unknown>((resolve) => { settle = resolve }))
  return { settle: (p: unknown = payload) => settle(p) }
}

beforeEach(() => {
  vi.mocked(api.fetchHostConfig).mockReset()
  vi.mocked(api.putHostConfig).mockReset()
  useHostConfigStore.setState({ byHost: {} })
  registerHost()
  useHostConfigStore.getState().forget(H)
})

describe('load', () => {
  it('stores items and revisions and becomes ready', async () => {
    vi.mocked(api.fetchHostConfig).mockResolvedValue(payload)
    await useHostConfigStore.getState().load(H)
    const e = useHostConfigStore.getState().byHost[H]
    expect(e.status).toBe('ready')
    expect(e.projects).toEqual(payload.projects.items)
    expect(e.resumeTemplates).toEqual(payload.resumeTemplates.items)
    expect(e.revisions).toEqual({ projects: 3, commands: 0, resumeTemplates: 1, quickReplies: 0, relay: 0 })
    expect(e.relaySupported).toBe(false)
    expect(e.relay).toEqual({ self_solo: true, self_lead: true })
    expect(e.problems).toEqual({})
  })

  it('404 → unsupported; never throws', async () => {
    vi.mocked(api.fetchHostConfig).mockRejectedValue(new HostConfigApiError(404, 'nope'))
    await expect(useHostConfigStore.getState().load(H)).resolves.toBeUndefined()
    expect(useHostConfigStore.getState().byHost[H].status).toBe('unsupported')
  })

  it('other failures → error with message; never throws', async () => {
    vi.mocked(api.fetchHostConfig).mockRejectedValue(new Error('boom'))
    await useHostConfigStore.getState().load(H)
    expect(useHostConfigStore.getState().byHost[H]).toMatchObject({ status: 'error', error: 'boom' })
  })

  it('loads quickReplies and its revision', async () => {
    const quickReplies = { items: [{ id: 'go', text: 'go on' }], revision: 2 }
    vi.mocked(api.fetchHostConfig).mockResolvedValue({ ...payload, quickReplies })
    await useHostConfigStore.getState().load(H)
    const e = useHostConfigStore.getState().byHost[H]
    expect(e.quickReplies).toEqual(quickReplies.items)
    expect(e.revisions.quickReplies).toBe(2)
    expect(e.quickRepliesSupported).toBe(true)
  })

  it('marks the collection unsupported on an old daemon payload', async () => {
    vi.mocked(api.fetchHostConfig).mockResolvedValue(payload)
    await useHostConfigStore.getState().load(H)
    const e = useHostConfigStore.getState().byHost[H]
    expect(e.status).toBe('ready')
    expect(e.quickRepliesSupported).toBe(false)
    expect(e.quickReplies).toEqual([])
    expect(e.revisions.quickReplies).toBe(0)
  })

  it('dedupes concurrent loads for one host', async () => {
    vi.mocked(api.fetchHostConfig).mockResolvedValue(payload)
    await Promise.all([useHostConfigStore.getState().load(H), useHostConfigStore.getState().load(H)])
    expect(api.fetchHostConfig).toHaveBeenCalledTimes(1)
  })
})

describe('ensureLoaded', () => {
  it('does nothing when already ready or unsupported', async () => {
    useHostConfigStore.setState({ byHost: { [H]: emptyHostConfigEntry('ready'), h2: emptyHostConfigEntry('unsupported') } })
    await useHostConfigStore.getState().ensureLoaded(H)
    await useHostConfigStore.getState().ensureLoaded('h2')
    expect(api.fetchHostConfig).not.toHaveBeenCalled()
  })

  it('loads an idle or errored host and swallows failures', async () => {
    vi.mocked(api.fetchHostConfig).mockRejectedValue(new Error('down'))
    await expect(useHostConfigStore.getState().ensureLoaded(H)).resolves.toBeUndefined()
    expect(useHostConfigStore.getState().byHost[H].status).toBe('error')
  })
})

describe('save*', () => {
  beforeEach(async () => {
    vi.mocked(api.fetchHostConfig).mockResolvedValue(payload)
    await useHostConfigStore.getState().load(H)
  })

  it('PUTs with the current revision and stores the returned copy', async () => {
    const next = [{ id: 'p2', name: 'B', slug: 'b', path: '/b' }]
    vi.mocked(api.putHostConfig).mockResolvedValue({ items: next, revision: 4 })
    await useHostConfigStore.getState().saveProjects(H, next)
    expect(api.putHostConfig).toHaveBeenCalledWith(H, 'projects', next, 3)
    const e = useHostConfigStore.getState().byHost[H]
    expect(e.projects).toEqual(next)
    expect(e.revisions.projects).toBe(4)
  })

  it('409 replaces local with the server copy and rethrows the conflict', async () => {
    const server = { items: { codex: { exact: 'cx {id}', fallback: 'cx' } }, revision: 7 }
    vi.mocked(api.putHostConfig).mockRejectedValue(new HostConfigConflictError(server))
    await expect(useHostConfigStore.getState().saveResumeTemplates(H, {})).rejects.toBeInstanceOf(HostConfigConflictError)
    const e = useHostConfigStore.getState().byHost[H]
    expect(e.resumeTemplates).toEqual(server.items)
    expect(e.revisions.resumeTemplates).toBe(7)
  })

  it('saveQuickReplies PUTs the quick-replies collection and stores the copy', async () => {
    const next = [{ id: 'go', text: 'go on' }]
    vi.mocked(api.putHostConfig).mockResolvedValue({ items: next, revision: 1 })
    await useHostConfigStore.getState().saveQuickReplies(H, next)
    expect(api.putHostConfig).toHaveBeenCalledWith(H, 'quick-replies', next, 0)
    const e = useHostConfigStore.getState().byHost[H]
    expect(e.quickReplies).toEqual(next)
    expect(e.revisions.quickReplies).toBe(1)
  })

  it('refuses to save a host that is not ready', async () => {
    await expect(useHostConfigStore.getState().saveCommands('h-unloaded', [])).rejects.toThrow(/not loaded/)
    expect(api.putHostConfig).not.toHaveBeenCalled()
  })
})

// The daemon's GET hands back whatever a row holds (#1489): a hand-edited row
// must reach the sections sanitised, with the section told why.
describe('malformed collections', () => {
  const C1 = { id: 'c1', name: 'Claude', command: 'claude', icon: { kind: 'agent', value: 'cc-bot' } }
  const malformed = {
    ...payload,
    projects: { items: {}, revision: 3 },
    commands: { items: [C1, { id: 'c2', command: 'x' }], revision: 2 },
    quickReplies: { items: [{ id: 'q1', text: 42 }], revision: 5 },
    relay: { items: { self_solo: null }, revision: 1 },
  }

  beforeEach(() => { vi.spyOn(console, 'warn').mockImplementation(() => {}) })
  afterEach(() => { vi.mocked(console.warn).mockRestore() })

  it('load: ready and sanitised; each bad collection carries its problem, the good one is intact', async () => {
    vi.mocked(api.fetchHostConfig).mockResolvedValue(malformed)
    await useHostConfigStore.getState().load(H)
    const e = useHostConfigStore.getState().byHost[H]
    expect(e.status).toBe('ready')
    expect(e.projects).toEqual([])
    expect(e.commands).toEqual([C1])
    expect(e.quickReplies).toEqual([])
    // Fails closed like the daemon, which refuses self relay on a value it cannot read.
    expect(e.relay).toEqual({ self_solo: false, self_lead: false })
    expect(e.resumeTemplates).toEqual(payload.resumeTemplates.items)
    expect(e.revisions).toEqual({ projects: 3, commands: 2, resumeTemplates: 1, quickReplies: 5, relay: 1 })
    expect(e.problems).toEqual({
      projects: { kind: 'shape' },
      commands: { kind: 'rows', count: 1 },
      quickReplies: { kind: 'rows', count: 1 },
      relay: { kind: 'relay' },
    })
  })

  it('a body that is not an object is still a load error', async () => {
    vi.mocked(api.fetchHostConfig).mockResolvedValue(null)
    await useHostConfigStore.getState().load(H)
    expect(useHostConfigStore.getState().byHost[H].status).toBe('error')
  })

  it('a successful save clears that field\'s problem and no other', async () => {
    vi.mocked(api.fetchHostConfig).mockResolvedValue(malformed)
    await useHostConfigStore.getState().load(H)
    const next = [{ id: 'p2', name: 'B', slug: 'b', path: '/b' }]
    vi.mocked(api.putHostConfig).mockResolvedValue({ items: next, revision: 4 })
    await useHostConfigStore.getState().saveProjects(H, next)
    const e = useHostConfigStore.getState().byHost[H]
    expect(e.projects).toEqual(next)
    expect(Object.keys(e.problems).sort()).toEqual(['commands', 'quickReplies', 'relay'])
  })

  it('a 409 whose current copy is malformed is sanitised and sets the problem; a clean one clears it', async () => {
    vi.mocked(api.fetchHostConfig).mockResolvedValue(payload)
    await useHostConfigStore.getState().load(H)
    const ok = { id: 'q1', text: 'ok' }
    vi.mocked(api.putHostConfig).mockRejectedValueOnce(new HostConfigConflictError({ items: [ok, { id: 'q2' }], revision: 9 }))
    await expect(useHostConfigStore.getState().saveQuickReplies(H, [])).rejects.toBeInstanceOf(HostConfigConflictError)
    let e = useHostConfigStore.getState().byHost[H]
    expect(e.quickReplies).toEqual([ok])
    expect(e.revisions.quickReplies).toBe(9)
    expect(e.problems).toEqual({ quickReplies: { kind: 'rows', count: 1 } })

    vi.mocked(api.putHostConfig).mockRejectedValueOnce(new HostConfigConflictError({ items: [ok], revision: 10 }))
    await expect(useHostConfigStore.getState().saveQuickReplies(H, [])).rejects.toBeInstanceOf(HostConfigConflictError)
    e = useHostConfigStore.getState().byHost[H]
    expect(e.revisions.quickReplies).toBe(10)
    expect(e.problems).toEqual({})
  })
})

// A response belongs to the endpoint it was asked of. Anything that can make
// that endpoint a different daemon — the host being removed, its address or
// token changing — must make every answer already in flight unusable, or the
// next save would PUT the previous daemon's items under its revision.
describe('stale responses', () => {
  it('a load that resolves after the host was forgotten never repopulates it', async () => {
    const f = pendingFetch()
    const load = useHostConfigStore.getState().load(H)
    expect(useHostConfigStore.getState().byHost[H].status).toBe('loading')
    useHostConfigStore.getState().forget(H)
    f.settle()
    await load
    expect(useHostConfigStore.getState().byHost[H]).toBeUndefined()
  })

  it('a load that resolves after the endpoint moved is discarded; a fresh load fills the new one', async () => {
    const stale = pendingFetch()
    const load = useHostConfigStore.getState().load(H)
    registerHost('5.6.7.8')

    const moved = { ...payload, projects: { items: [{ id: 'p9', name: 'Air', slug: 'air', path: '/a' }], revision: 11 } }
    vi.mocked(api.fetchHostConfig).mockResolvedValue(moved)
    const reload = useHostConfigStore.getState().load(H)
    stale.settle()
    await Promise.all([load, reload])

    const e = useHostConfigStore.getState().byHost[H]
    expect(e.projects).toEqual(moved.projects.items)
    expect(e.revisions.projects).toBe(11)
  })

  it('a save that resolves after the endpoint moved does not write', async () => {
    vi.mocked(api.fetchHostConfig).mockResolvedValue(payload)
    await useHostConfigStore.getState().load(H)

    let finish!: (v: api.Versioned<api.HostProject[]>) => void
    vi.mocked(api.putHostConfig).mockReturnValueOnce(new Promise((resolve) => { finish = resolve }))
    const next = [{ id: 'p2', name: 'B', slug: 'b', path: '/b' }]
    const save = useHostConfigStore.getState().saveProjects(H, next)
    registerHost('5.6.7.8')
    finish({ items: next, revision: 4 })
    await save.catch(() => {})

    const e = useHostConfigStore.getState().byHost[H]
    expect(e.projects).toEqual(payload.projects.items)
    expect(e.revisions.projects).toBe(3)
  })

  // A refresh and a save share one host token, so the token cannot tell them apart: a GET read before a save
  // landed carries the copy the save replaced. Only the field the save wrote is held; the rest is the GET's.
  describe('a refresh sent before a save lands after it', () => {
    const C1 = { id: 'c1', name: 'Claude', command: 'claude', icon: { kind: 'agent', value: 'cc-bot' } }
    const P2 = { id: 'p2', name: 'B', slug: 'b', path: '/b' }
    const Q1 = { id: 'q1', text: 'ok' }
    // What the daemon held before the save: projects malformed, quick replies with a bad row.
    const before = {
      ...payload,
      projects: { items: {}, revision: 4 },
      quickReplies: { items: [Q1, { id: 'q2' }], revision: 6 },
    }

    beforeEach(async () => {
      vi.spyOn(console, 'warn').mockImplementation(() => {})
      vi.mocked(api.fetchHostConfig).mockResolvedValueOnce(before)
      await useHostConfigStore.getState().load(H)
    })
    afterEach(() => { vi.mocked(console.warn).mockRestore() })

    it('a successful save keeps its items, revision and cleared problem; another field takes the GET', async () => {
      const refresh = pendingFetch()
      const load = useHostConfigStore.getState().load(H)
      vi.mocked(api.putHostConfig).mockResolvedValueOnce({ items: [P2], revision: 5 })
      await useHostConfigStore.getState().saveProjects(H, [P2])
      refresh.settle({ ...before, commands: { items: [C1], revision: 2 } })
      await load

      const e = useHostConfigStore.getState().byHost[H]
      expect(e.projects).toEqual([P2])
      expect(e.revisions.projects).toBe(5)
      expect(e.problems.projects).toBeUndefined()
      expect(e.commands).toEqual([C1])
      expect(e.revisions.commands).toBe(2)
      expect(e.problems.quickReplies).toEqual({ kind: 'rows', count: 1 })
    })

    it('a 409 keeps the daemon copy it carried, and the field stays supported', async () => {
      const refresh = pendingFetch()
      const load = useHostConfigStore.getState().load(H)
      vi.mocked(api.putHostConfig).mockRejectedValueOnce(new HostConfigConflictError({ items: [Q1], revision: 8 }))
      await expect(useHostConfigStore.getState().saveQuickReplies(H, [])).rejects.toBeInstanceOf(HostConfigConflictError)
      // The stale answer even lacks the collection: the field the save wrote is held whole, its flag included.
      const { quickReplies: _gone, ...stale } = before
      refresh.settle({ ...stale, commands: { items: [C1], revision: 2 } })
      await load

      const e = useHostConfigStore.getState().byHost[H]
      expect(e.quickReplies).toEqual([Q1])
      expect(e.revisions.quickReplies).toBe(8)
      expect(e.problems.quickReplies).toBeUndefined()
      expect(e.quickRepliesSupported).toBe(true)
      expect(e.commands).toEqual([C1])
      expect(e.revisions.commands).toBe(2)
      expect(e.problems.projects).toEqual({ kind: 'shape' })
    })

    it('a save that failed without a copy holds nothing back', async () => {
      const refresh = pendingFetch()
      const load = useHostConfigStore.getState().load(H)
      vi.mocked(api.putHostConfig).mockRejectedValueOnce(new HostConfigApiError(500, 'unwell'))
      await expect(useHostConfigStore.getState().saveProjects(H, [P2])).rejects.toThrow('unwell')
      refresh.settle({ ...before, projects: { items: [P2], revision: 7 } })
      await load

      const e = useHostConfigStore.getState().byHost[H]
      expect(e.projects).toEqual([P2])
      expect(e.revisions.projects).toBe(7)
      expect(e.problems.projects).toBeUndefined()
    })

    it('a GET sent after the save applies to every field', async () => {
      vi.mocked(api.putHostConfig).mockResolvedValueOnce({ items: [P2], revision: 5 })
      await useHostConfigStore.getState().saveProjects(H, [P2])
      const refresh = pendingFetch()
      const load = useHostConfigStore.getState().load(H)
      refresh.settle({ ...before, projects: { items: [], revision: 9 } })
      await load

      const e = useHostConfigStore.getState().byHost[H]
      expect(e.projects).toEqual([])
      expect(e.revisions.projects).toBe(9)
      expect(e.problems.projects).toBeUndefined()
    })
  })

  it('a load for a host that is not configured touches nothing', async () => {
    await useHostConfigStore.getState().load('h-gone')
    expect(useHostConfigStore.getState().byHost['h-gone']).toBeUndefined()
    expect(api.fetchHostConfig).not.toHaveBeenCalled()
  })
})

it('forget drops the host entry', () => {
  useHostConfigStore.setState({ byHost: { [H]: emptyHostConfigEntry('ready') } })
  useHostConfigStore.getState().forget(H)
  expect(useHostConfigStore.getState().byHost[H]).toBeUndefined()
})
