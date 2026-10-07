import { describe, it, expect, beforeEach, vi } from 'vitest'
import {
  checkRelayPromptBody,
  fetchRelayPrompts,
  normalizeRelayPromptBody,
  relayPromptBytes,
  relayPromptValueToStore,
  RelayPromptsApiError,
  type RelayPrompts,
} from './relay-prompts-api'
import { useHostStore } from '../stores/useHostStore'

const H = 'h1'

const PROMPTS: RelayPrompts = {
  write: 'custom write', fix: 'fix {{path}}', seed: 'seed {{old_ref}}',
  defaults: { write: 'default write', fix: 'fix {{path}}', seed: 'seed {{old_ref}}' },
  fixed: {
    write: { head: '[pdx-relay op={{op}} n={{nonce}}] ', tail: '# HANDOFF' },
    fix: { head: '[pdx-relay op={{op}} n={{nonce}}] ', tail: '缺少段落：{{missing}}' },
    seed: { head: '↪ 接手自 {{old_ref}}\n[pdx-relay seed op={{op}} n={{nonce}}] ', tail: '' },
  },
  variables: ['path', 'old_ref', 'old_session', 'context', 'whoami'],
}

const json = (body: unknown, status = 200) =>
  new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } })

beforeEach(() => {
  vi.unstubAllGlobals()
  useHostStore.setState({
    hosts: { [H]: { id: H, name: 'mlab', ip: '100.64.0.2', port: 7860, token: 'tok', order: 0 } },
    hostOrder: [H], activeHostId: H,
  })
})

describe('fetchRelayPrompts', () => {
  it('GETs /api/relay/prompts on that host, with auth, and returns the whole answer', async () => {
    const fetchMock = vi.fn(async () => json(PROMPTS))
    vi.stubGlobal('fetch', fetchMock)
    await expect(fetchRelayPrompts(H)).resolves.toEqual(PROMPTS)
    const [url, init] = fetchMock.mock.calls[0] as unknown as [string, RequestInit]
    expect(url).toBe('http://100.64.0.2:7860/api/relay/prompts')
    expect(new Headers(init.headers).get('Authorization')).toBe('Bearer tok')
  })

  it('a plain-text 404 (a daemon from before P9a-1, Go mux) is unsupported', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => new Response('404 page not found\n', { status: 404 })))
    await expect(fetchRelayPrompts(H)).resolves.toBe('unsupported')
  })

  it('a JSON error is thrown with its detail, a JSON 404 included', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => json({ error: 'storage_error', detail: 'unreadable; see the daemon log' }, 500)))
    await expect(fetchRelayPrompts(H)).rejects.toMatchObject({ name: 'RelayPromptsApiError', status: 500, message: 'unreadable; see the daemon log' })
    vi.stubGlobal('fetch', vi.fn(async () => json({ error: 'not_found', detail: 'x' }, 404)))
    await expect(fetchRelayPrompts(H)).rejects.toBeInstanceOf(RelayPromptsApiError)
  })

  it.each([
    ['a body that is not a string', { ...PROMPTS, fix: null }],
    ['a default missing', { ...PROMPTS, defaults: { write: 'w', fix: 'f' } }],
    ['a fixed tail that is not a string', { ...PROMPTS, fixed: { ...PROMPTS.fixed, seed: { head: 'h' } } }],
    ['a fixed kind missing', { ...PROMPTS, fixed: { write: PROMPTS.fixed.write, fix: PROMPTS.fixed.fix } }],
    ['variables not an array', { ...PROMPTS, variables: 'path' }],
    ['a variable that is not a string', { ...PROMPTS, variables: ['path', 1] }],
    ['not an object', ['write']],
  ])('the body is checked whole: %s throws', async (_label, body) => {
    vi.stubGlobal('fetch', vi.fn(async () => json(body)))
    await expect(fetchRelayPrompts(H)).rejects.toBeInstanceOf(RelayPromptsApiError)
  })

  it('refuses a host this device does not have, without fetching', async () => {
    const fetchMock = vi.fn()
    vi.stubGlobal('fetch', fetchMock)
    await expect(fetchRelayPrompts('ghost')).rejects.toThrow()
    expect(fetchMock).not.toHaveBeenCalled()
  })
})

// The client mirror of internal/team/relay_prompts.go ValidateRelayPromptBody.
describe('checkRelayPromptBody', () => {
  it('16 384 UTF-8 bytes pass, 16 385 are too long (bytes, not characters)', () => {
    expect(checkRelayPromptBody('a'.repeat(16384))).toBeNull()
    expect(checkRelayPromptBody('a'.repeat(16385))).toBe('too_long')
    expect(relayPromptBytes('中')).toBe(3)
    expect(checkRelayPromptBody('中'.repeat(5462))).toBe('too_long') // 16 386 bytes, 5 462 characters
  })

  it.each([['\r'], ['\x00'], ['\x7f'], ['\u0085'], ['\x1b']])('control character %j is refused', (c) => {
    expect(checkRelayPromptBody(`a${c}b`)).toBe('control_chars')
  })

  it('newline and tab pass', () => {
    expect(checkRelayPromptBody('a\n\tb')).toBeNull()
  })

  it.each([['[pdx-relay'], ['x [pdx-relay:control] y'], ['line\n[pdx-relay seed op=1 n=2]']])('the tag %j is refused wherever it stands', (s) => {
    expect(checkRelayPromptBody(s)).toBe('has_tag')
  })
})

describe('what a box saves', () => {
  it('CRLF becomes LF', () => {
    expect(normalizeRelayPromptBody('a\r\nb\r\n')).toBe('a\nb\n')
  })

  it('the default text, or whitespace only, is stored as "" (open question 12; spec U21 (a))', () => {
    expect(relayPromptValueToStore('default write', 'default write')).toBe('')
    expect(relayPromptValueToStore('a\r\nb', 'a\nb')).toBe('')
    expect(relayPromptValueToStore(' \n\t', 'default write')).toBe('')
    // Go's strings.TrimSpace, not JS trim(): U+0085 (NEL) is space to the daemon, which stores this as "" too.
    expect(relayPromptValueToStore('\u0085　', 'default write')).toBe('')
    expect(relayPromptValueToStore('mine\r\n', 'default write')).toBe('mine\n')
  })
})
