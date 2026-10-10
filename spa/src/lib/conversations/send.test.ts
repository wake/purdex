import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { DestructiveGuard, SUBMIT_TIMEOUT_MS, interruptSession, outcomeMessage, submitPrompt, mayHaveRun } from './send'
import { isDestructive, normalizePrompt, planSend } from './send-plan'

const fetchMock = vi.hoisted(() => vi.fn())
vi.mock('../host-api', () => ({ pinnedHostFetch: fetchMock }))

const SID = '11111111-2222-4333-8444-555555555555'
const json = (status: number, body: unknown) => Promise.resolve(new Response(JSON.stringify(body), { status }))

beforeEach(() => fetchMock.mockReset())
afterEach(() => vi.useRealTimers())

describe('submitPrompt', () => {
  it('posts text and client_msg_id to the pinned host', async () => {
    fetchMock.mockImplementation(() => json(200, { status: 'accepted', client_msg_id: 'c1' }))
    await expect(submitPrompt('h', SID, 'hi', 'c1')).resolves.toEqual({ kind: 'accepted' })
    const [host, p, init] = fetchMock.mock.calls[0]
    expect(host).toBe('h')
    expect(p).toBe(`/api/conversations/claude/${SID}/submit`)
    expect(init.method).toBe('POST')
    expect(JSON.parse(init.body)).toEqual({ text: 'hi', client_msg_id: 'c1' })
  })

  it.each([
    [200, { status: 'dropped', reason: 'session_changed' }, { kind: 'dropped', reason: 'session_changed' }],
    [200, { status: 'busy' }, { kind: 'busy' }],
    [200, { status: 'timeout' }, { kind: 'timeout' }],
    [200, { status: 'unknown', reason: 'no_result' }, { kind: 'unknown', reason: 'no_result' }],
    [200, { status: 'unknown', reason: 'not_owner' }, { kind: 'not_owner' }],
    [409, { error: 'no_mod' }, { kind: 'no_mod' }],
    [409, { error: 'not_owner' }, { kind: 'not_owner' }],
    [400, { error: 'needs_terminal' }, { kind: 'needs_terminal' }],
    [400, { error: 'too_long' }, { kind: 'invalid', code: 'too_long' }],
    [429, { error: 'too_many_pending' }, { kind: 'rejected', status: 429, code: 'too_many_pending' }],
  ])('%i %j -> %j', async (status, body, want) => {
    fetchMock.mockImplementation(() => json(status, body))
    await expect(submitPrompt('h', SID, 'hi', 'c1')).resolves.toEqual(want)
  })

  it('a lost connection is `network`: possibly sent, never auto-resent', async () => {
    fetchMock.mockRejectedValueOnce(new TypeError('fetch failed'))
    const o = await submitPrompt('h', SID, 'hi', 'c1')
    expect(o).toEqual({ kind: 'network' })
    expect(mayHaveRun(o)).toBe(true)
    expect(mayHaveRun({ kind: 'busy' })).toBe(false)
  })

  it('waits at least 25 s for the daemon (it answers within 10 s), then gives up as `network`', async () => {
    vi.useFakeTimers()
    let signal: AbortSignal | undefined
    fetchMock.mockImplementationOnce((_h, _p, init) => {
      signal = init.signal
      return new Promise((_res, rej) => init.signal.addEventListener('abort', () => rej(new DOMException('aborted', 'AbortError'))))
    })
    expect(SUBMIT_TIMEOUT_MS).toBeGreaterThanOrEqual(25_000)
    const p = submitPrompt('h', SID, 'hi', 'c1')
    await vi.advanceTimersByTimeAsync(24_999)
    expect(signal?.aborted).toBe(false)
    await vi.advanceTimersByTimeAsync(SUBMIT_TIMEOUT_MS)
    await expect(p).resolves.toEqual({ kind: 'network' })
    expect(signal?.aborted).toBe(true)
  })
})

describe('interruptSession', () => {
  it('posts an empty body to /interrupt', async () => {
    fetchMock.mockImplementation(() => json(200, { status: 'accepted' }))
    await expect(interruptSession('h', SID)).resolves.toEqual({ kind: 'accepted' })
    expect(fetchMock.mock.calls[0][1]).toBe(`/api/conversations/claude/${SID}/interrupt`)
    expect(JSON.parse(fetchMock.mock.calls[0][2].body)).toEqual({})
  })
})

describe('outcomeMessage', () => {
  it('every outcome has its own message', () => {
    const keys = ([
      { kind: 'accepted' }, { kind: 'dropped', reason: 'x' }, { kind: 'busy' }, { kind: 'timeout' }, { kind: 'unknown' }, { kind: 'not_owner' },
      { kind: 'no_mod' }, { kind: 'needs_terminal' }, { kind: 'invalid', code: 'too_long' }, { kind: 'invalid', code: 'empty_text' },
      { kind: 'rejected', status: 503, code: 'unavailable' }, { kind: 'network' },
    ] as const).map((o) => outcomeMessage(o).key)
    expect(new Set(keys).size).toBe(keys.length)
    expect(outcomeMessage({ kind: 'dropped', reason: 'session_changed' }).params).toEqual({ reason: 'session_changed' })
  })
})

describe('planSend', () => {
  it('strips control characters but keeps newlines, expands tabs, trims', () => {
    expect(normalizePrompt('\n\n  a\u0007b\tc  \n\nline2   \n\n')).toBe('  ab    c\n\nline2')
    expect(planSend('hello\r\nworld')).toEqual({ ok: true, text: 'hello\nworld' })
  })
  it('refuses a draft that is only whitespace', () => {
    expect(planSend('  \n\t \n')).toEqual({ ok: false, refusal: 'empty' })
    expect(planSend('')).toEqual({ ok: false, refusal: 'empty' })
  })
  it('counts UTF-8 bytes, 4000 at most', () => {
    expect(planSend('a'.repeat(4000)).ok).toBe(true)
    expect(planSend('a'.repeat(4001))).toEqual({ ok: false, refusal: 'too_long' })
    expect(planSend('字'.repeat(1334))).toEqual({ ok: false, refusal: 'too_long' }) // 4002 bytes
    expect(planSend('字'.repeat(1333)).ok).toBe(true)
  })
  it('a leading / or ! needs the terminal, also behind blank lines and hidden characters', () => {
    expect(planSend('/model')).toEqual({ ok: false, refusal: 'needs_terminal' })
    expect(planSend('!ls')).toEqual({ ok: false, refusal: 'needs_terminal' })
    expect(planSend('\n​/clear')).toEqual({ ok: false, refusal: 'needs_terminal' })
    expect(planSend('use /tmp').ok).toBe(true)
  })
})

describe('isDestructive', () => {
  it.each(['rm -rf /tmp/x', 'sudo rm -fr .', 'git push --force origin main', 'git push -f', 'git push origin +main', 'git reset --hard HEAD~3', 'please DROP TABLE users', 'mkfs.ext4 /dev/sda', 'dd if=/dev/zero of=/dev/disk2'])('%s', (t) => {
    expect(isDestructive(t)).toBe(true)
    expect(isDestructive(`fine\n${t}\nfine`)).toBe(true)
  })
  it.each(['rm file.txt', 'git push origin main', 'git reset HEAD file', 'drop the table', 'format the text'])('%s is fine', (t) => {
    expect(isDestructive(t)).toBe(false)
  })
})

describe('DestructiveGuard', () => {
  it('asks once, sends on the second press within 5 s', () => {
    let t = 0
    const g = new DestructiveGuard(() => t)
    expect(g.check('hello')).toBe('send')
    expect(g.check('rm -rf x')).toBe('confirm')
    t = 4999
    expect(g.check('rm -rf x')).toBe('send')
    expect(g.check('rm -rf x')).toBe('confirm') // the confirmation was used up
  })
  it('the window resets after 5 s, and a different text does not inherit it', () => {
    let t = 0
    const g = new DestructiveGuard(() => t)
    g.check('rm -rf x')
    t = 5001
    expect(g.check('rm -rf x')).toBe('confirm')
    t = 5500
    expect(g.check('rm -rf y')).toBe('confirm')
  })
})
