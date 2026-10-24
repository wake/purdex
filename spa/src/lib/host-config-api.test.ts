import { describe, it, expect, beforeEach, vi } from 'vitest'
import {
  checkHostPath,
  fetchHostConfig,
  HostConfigApiError,
  HostConfigConflictError,
  putHostConfig,
} from './host-config-api'
import { useHostStore } from '../stores/useHostStore'

const H = 'h1'

function json(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } })
}

beforeEach(() => {
  vi.unstubAllGlobals()
  useHostStore.setState({
    hosts: { [H]: { id: H, name: 'mlab', ip: '100.64.0.2', port: 7860, token: 'tok', order: 0 } },
    hostOrder: [H],
    activeHostId: H,
  })
})

describe('fetchHostConfig', () => {
  it('GETs /api/hostconfig with auth and returns the payload', async () => {
    const payload = {
      projects: { items: [{ id: 'p1', name: 'P', slug: 'p', path: '~/p' }], revision: 2 },
      commands: { items: [], revision: 0 },
      resumeTemplates: { items: {}, revision: 0 },
    }
    const fetchMock = vi.fn(async () => json(payload))
    vi.stubGlobal('fetch', fetchMock)
    await expect(fetchHostConfig(H)).resolves.toEqual(payload)
    const [url, init] = fetchMock.mock.calls[0] as unknown as [string, RequestInit]
    expect(url).toBe('http://100.64.0.2:7860/api/hostconfig')
    expect(new Headers(init.headers).get('Authorization')).toBe('Bearer tok')
  })

  it('maps 404 (old daemon) to HostConfigApiError status 404', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => new Response('404 page not found', { status: 404 })))
    await expect(fetchHostConfig(H)).rejects.toMatchObject({ name: 'HostConfigApiError', status: 404 })
  })

  it('refuses an unknown host without fetching (hostFetch would fall back to the active host)', async () => {
    const fetchMock = vi.fn()
    vi.stubGlobal('fetch', fetchMock)
    await expect(fetchHostConfig('ghost')).rejects.toBeInstanceOf(HostConfigApiError)
    expect(fetchMock).not.toHaveBeenCalled()
  })
})

describe('putHostConfig', () => {
  it('quick-replies: every item goes out as {id, title, content, text: content}', async () => {
    const fetchMock = vi.fn(async () => json({ items: [], revision: 1 }))
    vi.stubGlobal('fetch', fetchMock)
    await putHostConfig(H, 'quick-replies', [{ id: 'a', title: 'A', content: 'do a' }, { id: 'b', title: 'B', content: 'do b' }], 0)
    const init = (fetchMock.mock.calls[0] as unknown as [string, RequestInit])[1]
    expect(JSON.parse(init.body as string)).toEqual({
      items: [{ id: 'a', title: 'A', content: 'do a', text: 'do a' }, { id: 'b', title: 'B', content: 'do b', text: 'do b' }],
      baseRevision: 0,
    })
  })

  it('relay: member_auto true is left out of the body (an old daemon/App must not see it)', async () => {
    const fetchMock = vi.fn(async () => json({ items: {}, revision: 1 }))
    vi.stubGlobal('fetch', fetchMock)
    await putHostConfig(H, 'relay', { self_solo: true, self_lead: false, member_auto: true, prompt_write: 'w' }, 2)
    const init = (fetchMock.mock.calls[0] as unknown as [string, RequestInit])[1]
    expect(JSON.parse(String(init.body))).toEqual({ items: { self_solo: true, self_lead: false, prompt_write: 'w' }, baseRevision: 2 })
  })

  it('relay: member_auto false goes out as false', async () => {
    const fetchMock = vi.fn(async () => json({ items: {}, revision: 1 }))
    vi.stubGlobal('fetch', fetchMock)
    await putHostConfig(H, 'relay', { self_solo: true, self_lead: true, member_auto: false }, 2)
    const init = (fetchMock.mock.calls[0] as unknown as [string, RequestInit])[1]
    expect(JSON.parse(String(init.body))).toEqual({ items: { self_solo: true, self_lead: true, member_auto: false }, baseRevision: 2 })
  })

  it('PUTs items + baseRevision and returns the stored copy', async () => {
    const fetchMock = vi.fn(async () => json({ items: [], revision: 4 }))
    vi.stubGlobal('fetch', fetchMock)
    await expect(putHostConfig(H, 'commands', [], 3)).resolves.toEqual({ items: [], revision: 4 })
    const [url, init] = fetchMock.mock.calls[0] as unknown as [string, RequestInit]
    expect(url).toBe('http://100.64.0.2:7860/api/hostconfig/commands')
    expect(init.method).toBe('PUT')
    expect(JSON.parse(String(init.body))).toEqual({ items: [], baseRevision: 3 })
  })

  it('409 → HostConfigConflictError carrying the server copy', async () => {
    const current = { items: { cc: { exact: 'x {id}', fallback: 'x' } }, revision: 9 }
    vi.stubGlobal('fetch', vi.fn(async () => json(current, 409)))
    const err = await putHostConfig(H, 'resume-templates', {}, 1).catch((e) => e)
    expect(err).toBeInstanceOf(HostConfigConflictError)
    expect((err as HostConfigConflictError).current).toEqual(current)
  })

  it('400 → HostConfigApiError with the daemon reason as message', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => new Response('slug "a b" invalid', { status: 400 })))
    await expect(putHostConfig(H, 'projects', [], 0)).rejects.toMatchObject({ status: 400, message: 'slug "a b" invalid' })
  })
})

describe('checkHostPath', () => {
  it('POSTs the path and returns the verdict', async () => {
    const fetchMock = vi.fn(async () => json({ status: 'dir', resolved: '/Users/wake/w' }))
    vi.stubGlobal('fetch', fetchMock)
    await expect(checkHostPath(H, '~/w')).resolves.toEqual({ status: 'dir', resolved: '/Users/wake/w' })
    const [, init] = fetchMock.mock.calls[0] as unknown as [string, RequestInit]
    expect(JSON.parse(String(init.body))).toEqual({ path: '~/w' })
  })

  it('404 and network failure are unverifiable, not errors', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => new Response('', { status: 404 })))
    await expect(checkHostPath(H, '/x')).resolves.toEqual({ status: 'unverifiable', resolved: '' })
    vi.stubGlobal('fetch', vi.fn(async () => { throw new TypeError('Failed to fetch') }))
    await expect(checkHostPath(H, '/x')).resolves.toEqual({ status: 'unverifiable', resolved: '' })
  })

  it('400 (relative path) is reported as error with the reason', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => new Response('path must be absolute', { status: 400 })))
    await expect(checkHostPath(H, 'rel')).resolves.toEqual({ status: 'error', resolved: '', reason: 'path must be absolute' })
  })
})
