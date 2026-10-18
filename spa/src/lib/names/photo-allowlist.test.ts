import { describe, it, expect } from 'vitest'
import { photoRouteOf, photoRedirectAllowed } from './photo-allowlist'

describe('photoRouteOf', () => {
  it.each([
    ['https://picsum.photos/seed/hiro/128', 'fetch'],
    ['https://picsum.photos/id/237/128/128.jpg', 'fetch'],
    ['https://placecats.com/128/128', 'ipc'],
    ['https://placedog.net/128', 'ipc'],
    ['https://PICSUM.PHOTOS/x', 'fetch'], // the host is case-insensitive; it parses to the allowed host
  ])('allows %s through the %s route', (url, route) => {
    expect(photoRouteOf(url)).toBe(route)
  })

  it.each([
    'http://picsum.photos/x',
    'ftp://picsum.photos/x',
    'https://example.com/a.png',
    'https://picsum.photos.evil.com/x',
    'https://evil.com/?u=https://picsum.photos/',
    'https://evil.com#@picsum.photos/',
    'https://user@picsum.photos/x',
    'https://user:pw@picsum.photos/x',
    'https://picsum.photos:8443/x',
    'https://picsum.photos:443/x', // an explicit default port is still a port; URL would normalise it away
    'https://picsum.photos:/x',
    'https://picsum.photos./x', // trailing dot
    'https://picsum.photos\\@evil.com/',
    'https:\\\\picsum.photos\\x',
    'https://picsum.photos/x y',
    'https://pic​sum.photos/x', // zero-width character
    'https://picsum.photos。evil.com/', // ideographic full stop
    'https://xn--picsum-9ya.photos/x',
    'https://127.0.0.1/x',
    'https://[::1]/x',
    '//picsum.photos/x',
    'picsum.photos/x',
    '',
    'not a url',
  ])('refuses %s', (url) => {
    expect(photoRouteOf(url)).toBeNull()
  })
})

describe('photoRedirectAllowed', () => {
  it('allows the allowlist itself and the host Picsum redirects to', () => {
    expect(photoRedirectAllowed('https://picsum.photos/x')).toBe(true)
    expect(photoRedirectAllowed('https://fastly.picsum.photos/id/1/128/128.jpg?hmac=abc')).toBe(true)
    expect(photoRedirectAllowed('https://placecats.com/128/128')).toBe(true)
  })

  it('refuses a hop that leaves the list: other hosts, loopback, private ranges, http, other schemes, a port', () => {
    for (const u of [
      'https://evil.com/a.png', 'http://picsum.photos/x', 'https://127.0.0.1/x', 'https://10.0.0.5/x',
      'https://169.254.169.254/latest/meta-data', 'https://localhost/x', 'file:///etc/passwd', 'data:image/png;base64,AA==',
      'https://fastly.picsum.photos:8443/x', 'https://notpicsum.photos/x', 'https://a.fastly.picsum.photos/x',
    ]) {
      expect(photoRedirectAllowed(u)).toBe(false)
    }
  })
})
