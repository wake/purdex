import { describe, it, expect, beforeEach, vi } from 'vitest'
import { emptyHostConfigEntry, useHostConfigStore } from './useHostConfigStore'
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

beforeEach(() => {
  vi.mocked(api.fetchHostConfig).mockReset()
  vi.mocked(api.putHostConfig).mockReset()
  useHostConfigStore.setState({ byHost: {} })
})

describe('load', () => {
  it('stores items and revisions and becomes ready', async () => {
    vi.mocked(api.fetchHostConfig).mockResolvedValue(payload)
    await useHostConfigStore.getState().load(H)
    const e = useHostConfigStore.getState().byHost[H]
    expect(e.status).toBe('ready')
    expect(e.projects).toEqual(payload.projects.items)
    expect(e.resumeTemplates).toEqual(payload.resumeTemplates.items)
    expect(e.revisions).toEqual({ projects: 3, commands: 0, resumeTemplates: 1 })
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

  it('refuses to save a host that is not ready', async () => {
    await expect(useHostConfigStore.getState().saveCommands('h-unloaded', [])).rejects.toThrow(/not loaded/)
    expect(api.putHostConfig).not.toHaveBeenCalled()
  })
})

it('forget drops the host entry', () => {
  useHostConfigStore.setState({ byHost: { [H]: emptyHostConfigEntry('ready') } })
  useHostConfigStore.getState().forget(H)
  expect(useHostConfigStore.getState().byHost[H]).toBeUndefined()
})
