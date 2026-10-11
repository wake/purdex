import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { DestructiveGuard, SUBMIT_TIMEOUT_MS, interruptSession, outcomeMessage, submitTyped, typedSendPort, mayHaveRun } from './send'
import { isDestructive, normalizePrompt, planSend } from './send-plan'

const fetchMock = vi.hoisted(() => vi.fn())
vi.mock('../host-api', () => ({ pinnedHostFetch: fetchMock }))

const SID = '11111111-2222-4333-8444-555555555555'
const json = (status: number, body: unknown) => Promise.resolve(new Response(JSON.stringify(body), { status }))

beforeEach(() => fetchMock.mockReset())
afterEach(() => vi.useRealTimers())

describe('submitTyped timeout', () => {
  it('waits at least 25 s for the daemon (it answers within 10 s), then gives up as `network`', async () => {
    vi.useFakeTimers()
    let signal: AbortSignal | undefined
    fetchMock.mockImplementationOnce((_h, _p, init) => {
      signal = init.signal
      return new Promise((_res, rej) => init.signal.addEventListener('abort', () => rej(new DOMException('aborted', 'AbortError'))))
    })
    expect(SUBMIT_TIMEOUT_MS).toBeGreaterThanOrEqual(25_000)
    const p = submitTyped('h', { code: 'dev001', instance: 'g' }, 'hi', 'c1')
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

// U4-5a (coverage F): a worker's refusal codes are said in words, one per code, as the room said them (`execution.error.*`).
describe('outcomeMessage — a worker\'s refusal codes', () => {
  it.each(['invalid_text', 'execution_archived', 'execution_terminal', 'turn_failed_to_launch', 'turn_stalled', 'interrupt_unconfirmed'])(
    '%s has its own readable message, not the bare code', (code) => {
      const m = outcomeMessage({ kind: 'rejected', status: 409, code })
      expect(m.key).toBe(`execution.error.${code}`)
      expect(m.params).toBeUndefined()
    })
  it('a code it does not know still shows the code', () => {
    expect(outcomeMessage({ kind: 'rejected', status: 500, code: 'weird' })).toMatchObject({ key: 'deck.send.rejected', params: { code: 'weird' } })
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
  it.each([
    'rm -r --force target', 'rm --recursive -f target', 'rm -r  -f x', 'rm -R -f x', 'rm -f -r x', 'rm -r x -f', 'rm   -rf   x',
    'rm --recursive --force x', 'rm -rv -f x', 'sudo rm -r -f x', 'ls && rm -r -f x', 'echo hi; rm --force -r x',
    'rm -r \\\n  -f x', 'rm -r \\\r\n--force x',
    'git push --force-with-lease', 'git push origin main -f', 'git push -uf origin x', 'git push origin +main', 'git -C repo push --force',
  ])('%j is destructive (flags are read, not pattern-matched)', (t) => {
    expect(isDestructive(t)).toBe(true)
  })
  it.each(['rm -r dir', 'rm -f file', 'rm -i foo', 'rm -r -- -f', 'echo rm -r', 'git pull -f', 'git push', 'git push origin main', 'ls -rf', 'rmdir -p x -f'])('%j is not', (t) => {
    expect(isDestructive(t)).toBe(false)
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

describe('submitTyped (POST /api/sessions/{code}/submit)', () => {
  const T = { code: 'dev001', instance: '6901:1789205013' }
  const run = () => submitTyped('h', T, 'hi', 'c1')

  it('posts text, client_msg_id and expected_tmux_instance to the session code on the pinned host', async () => {
    fetchMock.mockImplementation(() => json(200, { outcome: 'handed', mode: 'fill' }))
    await run()
    const [host, p, init] = fetchMock.mock.calls[0]
    expect(host).toBe('h')
    expect(p).toBe('/api/sessions/dev001/submit')
    expect(init.method).toBe('POST')
    expect(JSON.parse(init.body)).toEqual({ text: 'hi', client_msg_id: 'c1', expected_tmux_instance: '6901:1789205013' })
  })

  it.each([
    [200, { outcome: 'handed', mode: 'fill' }, { kind: 'handed', mode: 'fill' }],
    [200, { outcome: 'handed', mode: 'keys' }, { kind: 'handed', mode: 'keys' }],
    [200, { outcome: 'unknown', mode: 'fill' }, { kind: 'unknown' }],
    [200, { outcome: 'unknown', reason: 'recall_empty' }, { kind: 'unknown', reason: 'recall_empty' }],
    [409, { outcome: 'refused', reason: 'input_not_empty', error: 'input_not_empty' }, { kind: 'refused', reason: 'input_not_empty', status: 409 }],
    [409, { outcome: 'refused', reason: 'text_left_in_terminal', error: 'text_left_in_terminal' }, { kind: 'refused', reason: 'text_left_in_terminal', status: 409 }],
    [429, { outcome: 'refused', reason: 'busy', error: 'busy' }, { kind: 'refused', reason: 'busy', status: 429 }],
    [429, { reason: 'too_many_pending', error: 'too_many_pending' }, { kind: 'rejected', status: 429, code: 'too_many_pending' }],
    [400, { reason: 'needs_terminal', error: 'needs_terminal' }, { kind: 'needs_terminal' }],
    [400, { reason: 'empty_text', error: 'empty_text' }, { kind: 'invalid', code: 'empty_text' }],
    [400, { reason: 'too_long', error: 'too_long' }, { kind: 'invalid', code: 'too_long' }],
    [400, { reason: 'bad_text', error: 'bad_text' }, { kind: 'invalid', code: 'bad_text' }],
    [400, { reason: 'bad_client_msg_id', error: 'bad_client_msg_id' }, { kind: 'rejected', status: 400, code: 'bad_client_msg_id' }],
    [400, {}, { kind: 'rejected', status: 400, code: '' }],
    [404, { reason: 'not_found', error: 'not_found' }, { kind: 'rejected', status: 404, code: 'not_found' }],
    [500, { reason: 'internal', error: 'internal' }, { kind: 'rejected', status: 500, code: 'internal' }],
    [503, { reason: 'unavailable', error: 'unavailable' }, { kind: 'rejected', status: 503, code: 'unavailable' }],
  ])('%i %j -> %j', async (status, body, want) => {
    fetchMock.mockImplementation(() => json(status, body))
    await expect(run()).resolves.toEqual(want)
  })

  it('a plain-text 404 (Go mux, a daemon without the submit route) is daemon_outdated, with a readable message', async () => {
    fetchMock.mockImplementation(() => Promise.resolve(new Response('404 page not found\n', { status: 404 })))
    const o = await run()
    expect(o).toEqual({ kind: 'rejected', status: 404, code: 'daemon_outdated' })
    expect(outcomeMessage(o)).toEqual({ key: 'deck.send.daemon_outdated', tone: 'error' })
  })

  it('a JSON 404 session_not_found keeps its own mapping', async () => {
    fetchMock.mockImplementation(() => json(404, { error: 'session_not_found' }))
    await expect(run()).resolves.toEqual({ kind: 'rejected', status: 404, code: 'session_not_found' })
  })

  it('a 200 whose body cannot be read may have run: unknown', async () => {
    fetchMock.mockImplementation(() => Promise.resolve(new Response('not json', { status: 200 })))
    await expect(run()).resolves.toEqual({ kind: 'unknown' })
  })

  it('a lost connection is `network`', async () => {
    fetchMock.mockRejectedValueOnce(new TypeError('fetch failed'))
    await expect(run()).resolves.toEqual({ kind: 'network' })
  })

  it('may have run: unknown, network and text_left_in_terminal; every other refusal did not', () => {
    expect(mayHaveRun({ kind: 'unknown' })).toBe(true)
    expect(mayHaveRun({ kind: 'network' })).toBe(true)
    expect(mayHaveRun({ kind: 'refused', reason: 'text_left_in_terminal', status: 409 })).toBe(true)
    expect(mayHaveRun({ kind: 'refused', reason: 'input_not_empty', status: 409 })).toBe(false)
    expect(mayHaveRun({ kind: 'handed', mode: 'fill' })).toBe(false)
    expect(mayHaveRun({ kind: 'needs_terminal' })).toBe(false)
    expect(mayHaveRun({ kind: 'rejected', status: 503, code: 'x' })).toBe(false)
  })
})

describe('typedSendPort', () => {
  it('reads the target at each submit: a generation stamped later is the one sent', async () => {
    fetchMock.mockImplementation(() => json(200, { outcome: 'handed', mode: 'fill' }))
    let target: { code: string; instance: string } | null = { code: 'dev001', instance: '' }
    const port = typedSendPort('h', SID, () => target)
    await expect(port.submit('a', 'c1')).resolves.toEqual({ kind: 'no_instance' })
    expect(fetchMock).not.toHaveBeenCalled()
    target = { code: 'dev001', instance: 'g2' }
    await expect(port.submit('a', 'c2')).resolves.toEqual({ kind: 'handed', mode: 'fill' })
    expect(JSON.parse(fetchMock.mock.calls[0][2].body).expected_tmux_instance).toBe('g2')
    target = null
    await expect(port.submit('a', 'c3')).resolves.toEqual({ kind: 'no_instance' })
    expect(fetchMock).toHaveBeenCalledTimes(1)
  })

  it('interrupt stays on the mod route', async () => {
    fetchMock.mockImplementation(() => json(200, { status: 'accepted' }))
    await typedSendPort('h', SID, () => null).interrupt()
    expect(fetchMock.mock.calls[0][1]).toBe(`/api/conversations/claude/${SID}/interrupt`)
  })
})

describe('outcomeMessage for the typed route', () => {
  it('handed, keys caption, no_instance and every classified refusal have their own message; an unknown reason carries its code', () => {
    const reasons = ['input_not_empty', 'dialog_open', 'pane_busy', 'waiting', 'generation', 'no_owner_yet', 'text_left_in_terminal', 'busy']
    const keys = [
      outcomeMessage({ kind: 'handed', mode: 'fill' }).key, outcomeMessage({ kind: 'handed', mode: 'keys' }).key, outcomeMessage({ kind: 'no_instance' }).key,
      ...reasons.map((r) => outcomeMessage({ kind: 'refused', reason: r, status: 409 }).key),
    ]
    expect(new Set(keys).size).toBe(keys.length)
    expect(outcomeMessage({ kind: 'refused', reason: 'pane_mismatch', status: 409 })).toMatchObject({ key: 'deck.send.refused_other', params: { code: 'pane_mismatch' } })
    expect(outcomeMessage({ kind: 'refused', reason: 'text_left_in_terminal', status: 409 }).tone).toBe('warn')
  })
})
