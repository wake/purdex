import { describe, expect, it, vi } from 'vitest'
import { fetchAvatar, handleAvatarFetch, AvatarFetchError, MAX_AVATAR_BYTES, MAX_HOPS, type FetchLike } from './avatar-fetch'

const PNG = new Uint8Array([0x89, 0x50, 0x4e, 0x47, 1, 2, 3])

function reply(over: { status?: number; type?: string | null; length?: string | null; body?: Uint8Array; location?: string } = {}): Response {
  const headers = new Headers()
  if (over.type !== null) headers.set('content-type', over.type ?? 'image/png')
  if (over.length !== undefined && over.length !== null) headers.set('content-length', over.length)
  if (over.location) headers.set('location', over.location)
  const body = over.body ?? PNG
  return new Response(body as BodyInit, { status: over.status ?? 200, headers })
}

const fetchOf = (...replies: Response[]) => {
  const queue = [...replies]
  return vi.fn<FetchLike>(async () => queue.shift() ?? reply({ status: 500 }))
}

async function reason(p: Promise<unknown>): Promise<string> {
  const e = await p.catch((x) => x)
  expect(e).toBeInstanceOf(AvatarFetchError)
  return (e as AvatarFetchError).code
}

describe('fetchAvatar: what it accepts', () => {
  it('returns a data URL for an image from an allowed host, asking for no redirects to be followed', async () => {
    const f = fetchOf(reply())
    const out = await fetchAvatar('https://placecats.com/128/128', { fetch: f })
    expect(out).toBe(`data:image/png;base64,${Buffer.from(PNG).toString('base64')}`)
    expect(f).toHaveBeenCalledWith('https://placecats.com/128/128', expect.objectContaining({ redirect: 'manual', credentials: 'omit' }))
  })

  it('follows a redirect hop that stays on the list, resolving a relative Location', async () => {
    const f = fetchOf(reply({ status: 302, location: 'https://fastly.picsum.photos/id/1/128/128.jpg' }), reply({ type: 'image/jpeg' }))
    const out = await fetchAvatar('https://picsum.photos/seed/x/128', { fetch: f })
    expect(out.startsWith('data:image/jpeg;base64,')).toBe(true)
    expect(f.mock.calls[1][0]).toBe('https://fastly.picsum.photos/id/1/128/128.jpg')
    const g = fetchOf(reply({ status: 301, location: '/cat/2' }), reply())
    await fetchAvatar('https://placecats.com/1', { fetch: g })
    expect(g.mock.calls[1][0]).toBe('https://placecats.com/cat/2')
  })

  it('takes the content type without its parameters', async () => {
    const out = await fetchAvatar('https://placedog.net/1', { fetch: fetchOf(reply({ type: 'image/webp; charset=binary' })) })
    expect(out.startsWith('data:image/webp;base64,')).toBe(true)
  })
})

describe('fetchAvatar: each refusal', () => {
  const never = () => fetchOf()

  it.each([
    ['http, not https', 'http://placecats.com/1'],
    ['a host that is not on the list', 'https://example.com/a.png'],
    ['a look-alike host', 'https://placecats.com.evil.com/1'],
    ['a port', 'https://placecats.com:8443/1'],
    ['an explicit default port', 'https://placecats.com:443/1'],
    ['credentials', 'https://user@placecats.com/1'],
    ['a trailing dot', 'https://placecats.com./1'],
    ['a backslash', 'https://placecats.com\\@evil.com/'],
    ['not a URL', 'not a url'],
    ['the CDN host as a starting point (it is only a redirect target)', 'https://fastly.picsum.photos/id/1/1/1.jpg'],
  ])('refuses %s before any request', async (_why, url) => {
    const f = never()
    expect(await reason(fetchAvatar(url, { fetch: f }))).toBe('bad-url')
    expect(f).not.toHaveBeenCalled()
  })

  it.each([
    ['another host', 'https://evil.example/a.png'],
    ['plain http', 'http://placecats.com/a.png'],
    ['loopback', 'https://127.0.0.1/a.png'],
    ['a private address', 'https://10.0.0.5/a.png'],
    ['the metadata address', 'https://169.254.169.254/latest/meta-data'],
    ['localhost', 'https://localhost/a.png'],
    ['a file URL', 'file:///etc/passwd'],
    ['a port on an allowed host', 'https://placecats.com:8443/a.png'],
  ])('refuses a redirect to %s, and does not request it', async (_why, to) => {
    const f = fetchOf(reply({ status: 302, location: to }), reply())
    expect(await reason(fetchAvatar('https://placecats.com/1', { fetch: f }))).toBe('redirect')
    expect(f).toHaveBeenCalledTimes(1)
  })

  it('refuses a redirect without a Location, and too many hops', async () => {
    expect(await reason(fetchAvatar('https://placecats.com/1', { fetch: fetchOf(reply({ status: 302 })) }))).toBe('redirect')
    const loop = vi.fn<FetchLike>(async () => reply({ status: 302, location: 'https://placecats.com/again' }))
    expect(await reason(fetchAvatar('https://placecats.com/1', { fetch: loop }))).toBe('redirect')
    expect(loop).toHaveBeenCalledTimes(MAX_HOPS + 1)
  })

  it('refuses a non-200 answer', async () => {
    expect(await reason(fetchAvatar('https://placecats.com/1', { fetch: fetchOf(reply({ status: 404 })) }))).toBe('status')
  })

  it.each([
    ['text/html', 'text/html'],
    ['an SVG (scriptable)', 'image/svg+xml'],
    ['no content type', null],
    ['a type that only starts like an image', 'imagex/png'],
  ])('refuses %s', async (_why, type) => {
    expect(await reason(fetchAvatar('https://placecats.com/1', { fetch: fetchOf(reply({ type })) }))).toBe('type')
  })

  it('refuses a body over 2 MB: by its declared length before reading, and by what actually arrives', async () => {
    const declared = fetchOf(reply({ length: String(MAX_AVATAR_BYTES + 1) }))
    expect(await reason(fetchAvatar('https://placecats.com/1', { fetch: declared }))).toBe('size')
    const sneaky = fetchOf(reply({ length: '10', body: new Uint8Array(MAX_AVATAR_BYTES + 1) }))
    expect(await reason(fetchAvatar('https://placecats.com/1', { fetch: sneaky }))).toBe('size')
  })

  it('turns a network failure into a code, not a raw error', async () => {
    const down = vi.fn<FetchLike>(async () => { throw new Error('ECONNRESET') })
    expect(await reason(fetchAvatar('https://placecats.com/1', { fetch: down }))).toBe('network')
  })
})

describe('handleAvatarFetch (what the IPC channel calls)', () => {
  it('refuses an argument that is not a string, before anything else', async () => {
    const f = fetchOf(reply())
    for (const bad of [undefined, null, 42, {}, ['https://placecats.com/1'], { toString: () => 'https://placecats.com/1' }]) {
      await expect(handleAvatarFetch(bad, { fetch: f })).rejects.toThrow(/bad-url/)
    }
    expect(f).not.toHaveBeenCalled()
  })

  it('rejects with a plain Error whose message starts with the code, so it survives the IPC boundary', async () => {
    const e = await handleAvatarFetch('https://example.com/a.png', { fetch: fetchOf() }).catch((x) => x)
    expect(e).toBeInstanceOf(Error)
    expect((e as Error).message.startsWith('avatar-fetch:bad-url')).toBe(true)
  })

  it('returns the data URL on success', async () => {
    const out = await handleAvatarFetch('https://placedog.net/1', { fetch: fetchOf(reply()) })
    expect(out.startsWith('data:image/png;base64,')).toBe(true)
  })
})
