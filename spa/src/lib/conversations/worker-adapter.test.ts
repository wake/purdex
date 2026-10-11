// U4-2: the worker's deck adapter (what the deck's input does on a Nexen execution) and the registry the input finds its adapter in.
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { adapterOf, bindAdapter, clearAllAdapters, forgetAdaptersWhere, pickAdapter, unavailableAdapter } from './deck-adapter'
import { draftKey, forgetDraft, readDraft, writeDraft, clearAllDrafts } from './draft-memory'
import { SendQueue, UNDO_MS, clearAllSendQueues, sendQueueFor } from './send-queue'
import { SUBMIT_TIMEOUT_MS, outcomeMessage } from './send'
import type { UserItem } from './types'
import { releasePane, retireStaleSessions } from './pane-release'
import { terminalAdapter } from './terminal-adapter'
import { clearAllWorkerLeases, registerWorkerLease, workerAdapter } from './worker-adapter'
import { clearAllWorkerDrafts, readWorkerDraft, writeWorkerDraft } from '../nex/worker-draft-memory'
import { NexApiError } from '../nex/types'
import * as api from '../nex/nex-api'

vi.mock('../nex/nex-api', () => ({ sendMessage: vi.fn(), interruptExecution: vi.fn(), uploadWorkerFile: vi.fn() }))

const H = 'h', E = 'exc_1'
const lease = () => ({ ensureLease: vi.fn(async () => 'lease-1'), forget: vi.fn() })

beforeEach(() => {
  clearAllAdapters(); clearAllWorkerLeases(); clearAllDrafts(); clearAllWorkerDrafts(); clearAllSendQueues()
  vi.mocked(api.sendMessage).mockReset().mockResolvedValue({} as never)
  vi.mocked(api.interruptExecution).mockReset().mockResolvedValue({} as never)
  vi.mocked(api.uploadWorkerFile).mockReset().mockResolvedValue({ path: '/w/a.txt', name: 'a.txt', size: 1 })
})

describe('workerAdapter', () => {
  it('submit takes the lease and posts the text to the execution', async () => {
    const l = lease(); registerWorkerLease(H, E, l)
    expect(await workerAdapter(H, E).port().submit('hi', 'cm1')).toEqual({ kind: 'accepted' })
    expect(api.sendMessage).toHaveBeenCalledWith(H, E, 'lease-1', 'hi')
  })

  it('interrupt takes the lease and interrupts the execution', async () => {
    registerWorkerLease(H, E, lease())
    expect(await workerAdapter(H, E).port().interrupt()).toEqual({ kind: 'accepted' })
    expect(api.interruptExecution).toHaveBeenCalledWith(H, E, 'lease-1')
  })

  it('a daemon refusal is a refusal with its code; a stale lease is dropped so the next send takes a new one', async () => {
    const l = lease(); registerWorkerLease(H, E, l)
    vi.mocked(api.sendMessage).mockRejectedValueOnce(new NexApiError(409, 'lease_held', 'held'))
    expect(await workerAdapter(H, E).port().submit('hi', 'cm1')).toEqual({ kind: 'rejected', status: 409, code: 'lease_held' })
    expect(l.forget).not.toHaveBeenCalled()
    vi.mocked(api.sendMessage).mockRejectedValueOnce(new NexApiError(409, 'lease_expired', 'gone'))
    await workerAdapter(H, E).port().submit('hi', 'cm2')
    expect(l.forget).toHaveBeenCalledTimes(1)
  })

  it('a failure that is not the daemon\'s answer is a network outcome', async () => {
    registerWorkerLease(H, E, lease())
    vi.mocked(api.sendMessage).mockRejectedValueOnce(new TypeError('failed to fetch'))
    expect(await workerAdapter(H, E).port().submit('hi', 'cm1')).toEqual({ kind: 'network' })
  })

  it('a request that got no answer may have run: network (the queue leaves it `maybe`), also when it is Nexen\'s own network error', async () => {
    registerWorkerLease(H, E, lease())
    vi.mocked(api.sendMessage).mockRejectedValueOnce(new NexApiError(0, 'network', 'dropped'))
    expect(await workerAdapter(H, E).port().submit('hi', 'cm1')).toEqual({ kind: 'network' })
  })

  it('a request that never answers lets go after the submit timeout, so the messages behind it are not held for good', async () => {
    vi.useFakeTimers()
    try {
      registerWorkerLease(H, E, lease())
      vi.mocked(api.sendMessage).mockReturnValueOnce(new Promise(() => {}))
      const out = workerAdapter(H, E).port().submit('hi', 'cm1')
      await vi.advanceTimersByTimeAsync(SUBMIT_TIMEOUT_MS + 1)
      expect(await out).toEqual({ kind: 'network' })
    } finally { vi.useRealTimers() }
  })

  it('two views of one worker each offer a lease: the newest answers, and the older one when the newest goes', async () => {
    const older = lease(), newer = { ensureLease: vi.fn(async () => 'lease-2'), forget: vi.fn() }
    registerWorkerLease(H, E, older)
    const takeBack = registerWorkerLease(H, E, newer)
    await workerAdapter(H, E).port().submit('a', 'c1')
    expect(vi.mocked(api.sendMessage).mock.calls.at(-1)![2]).toBe('lease-2')
    takeBack()
    await workerAdapter(H, E).port().submit('b', 'c2')
    expect(vi.mocked(api.sendMessage).mock.calls.at(-1)![2]).toBe('lease-1')
  })

  it('a send refused because the view is gone has its own plain wording, not a bare code', () => {
    expect(outcomeMessage({ kind: 'rejected', status: 0, code: 'pane_closed' })).toEqual({ key: 'deck.send.pane_closed', tone: 'warn' })
    expect(outcomeMessage({ kind: 'rejected', status: 409, code: 'lease_held' }).key).toBe('deck.send.rejected')
  })

  it('declares how its echo comes back: `user` or `queued`, however late Nexen hands the message over', () => {
    const echo = workerAdapter(H, E).port().echo!
    expect(echo.sources).toEqual(['user', 'queued'])
    expect(echo.afterMs).toBeGreaterThanOrEqual(5 * 60_000)
    expect(terminalAdapter(H, 's1', 'c').port().echo).toBeUndefined() // the terminal's window is unchanged
  })

  it('with no worker view mounted (its lease is gone) a send is refused, not lost: nothing is sent', async () => {
    expect(await workerAdapter(H, E).port().submit('hi', 'cm1')).toEqual({ kind: 'rejected', status: 0, code: 'pane_closed' })
    expect(await workerAdapter(H, E).port().interrupt()).toEqual({ kind: 'rejected', status: 0, code: 'pane_closed' })
    expect(api.sendMessage).not.toHaveBeenCalled()
  })

  it('a lease taken back by an older view does not remove a newer view\'s', async () => {
    const first = registerWorkerLease(H, E, lease())
    registerWorkerLease(H, E, lease())
    first()
    expect(await workerAdapter(H, E).port().submit('hi', 'cm1')).toEqual({ kind: 'accepted' })
  })

  it('upload is the worker upload route; its path is what goes into the text', async () => {
    const up = workerAdapter(H, E).upload!
    expect(await up(new File(['x'], 'a.txt'), { signal: new AbortController().signal, onProgress: () => {} })).toEqual({ path: '/w/a.txt' })
    expect(api.uploadWorkerFile).toHaveBeenCalledWith(H, E, expect.any(File))
  })

  it('the draft is the worker\'s own memory (host:execution), the same the room view reads', () => {
    const d = workerAdapter(H, E).draft!
    writeWorkerDraft(`${H}:${E}`, 'from the room')
    expect(d.read()).toBe('from the room')
    d.write('from the deck')
    expect(readWorkerDraft(`${H}:${E}`)).toBe('from the deck')
    d.write('')
    expect(readWorkerDraft(`${H}:${E}`)).toBeUndefined()
  })
})

describe('the worker\'s echo in the send queue', () => {
  const row = (text: string, at: number, source = 'user'): UserItem => ({ type: 'user', id: `u${at}`, at, index: 0, text, source } as UserItem)
  async function sentAt(start: number) {
    vi.useFakeTimers(); vi.setSystemTime(start)
    registerWorkerLease(H, E, lease())
    const q = new SendQueue(workerAdapter(H, E).port())
    q.enqueue('run the tests')
    await vi.advanceTimersByTimeAsync(UNDO_MS + 10)
    expect(q.entries()[0].state).toBe('sent')
    return q
  }
  const stop = () => vi.useRealTimers()

  it('a row that comes a whole turn later, as `user` or `queued`, still settles it (Nexen held the message while the worker was busy)', async () => {
    for (const source of ['user', 'queued']) {
      const q = await sentAt(1_000_000)
      vi.setSystemTime(1_000_000 + 90_000)
      q.reconcile([row('run the tests', 1_000_000 + 90_000, source)])
      expect(q.entries()).toEqual([])
      stop()
    }
  })

  it('the same late row does NOT settle a terminal message (its window is the mod\'s: seconds)', async () => {
    vi.useFakeTimers(); vi.setSystemTime(1_000_000)
    const q = new SendQueue({ submit: async () => ({ kind: 'accepted' }), interrupt: async () => ({ kind: 'accepted' }) })
    q.enqueue('run the tests')
    await vi.advanceTimersByTimeAsync(UNDO_MS + 10)
    q.reconcile([row('run the tests', 1_000_000 + 90_000)])
    expect(q.entries()).toHaveLength(1)
    stop()
  })
})

describe('the adapter registry', () => {
  const KEY = draftKey('p1', H, 's1')

  it('an unbound key is the deck\'s own memory; a bound adapter that keeps its own draft answers for its key', () => {
    writeDraft(KEY, 'terminal text')
    expect(readDraft(KEY)).toBe('terminal text')
    bindAdapter(KEY, workerAdapter(H, E))
    expect(readDraft(KEY)).toBeUndefined() // the worker's memory has nothing yet
    writeDraft(KEY, 'worker text')
    expect(readWorkerDraft(`${H}:${E}`)).toBe('worker text')
    expect(readDraft(KEY)).toBe('worker text')
    forgetDraft(KEY)
    expect(readWorkerDraft(`${H}:${E}`)).toBeUndefined()
  })

  it('the send queue of a key is made on the bound adapter\'s port, else on the caller\'s', async () => {
    registerWorkerLease(H, E, lease())
    bindAdapter(KEY, workerAdapter(H, E))
    const q = sendQueueFor(KEY, () => { throw new Error('the terminal port must not be made') })
    expect(await q.interrupt()).toEqual({ kind: 'accepted' })
    expect(api.interruptExecution).toHaveBeenCalled()
    const other = draftKey('p2', H, 's1')
    const port = { submit: vi.fn(), interrupt: vi.fn(async () => ({ kind: 'accepted' as const })) }
    await sendQueueFor(other, () => port as never).interrupt()
    expect(port.interrupt).toHaveBeenCalled()
  })

  it('terminalAdapter keeps today\'s shape: the mod\'s port, uploads only with a session code, the deck\'s own draft', () => {
    expect(terminalAdapter(H, 's1', 'dev001').upload).toBeTypeOf('function')
    expect(terminalAdapter(H, 's1').upload).toBeNull()
    expect(terminalAdapter(H, 's1', 'dev001').draft).toBeUndefined()
    expect(terminalAdapter(H, 's1', 'dev001').kind).toBe('terminal')
  })

  it('pickAdapter: what the pane bound; else for a session another back-end owns the unavailable one, never the terminal\'s; else the terminal\'s', async () => {
    const terminal = terminalAdapter(H, 's1', 'dev001')
    const worker = workerAdapter(H, E)
    expect(pickAdapter(KEY, { send: 'prompt' } as never, terminal)).toBe(terminal)
    expect(pickAdapter(KEY, undefined, terminal)).toBe(terminal)
    expect(pickAdapter(KEY, { send: 'execution' } as never, terminal)).toBe(unavailableAdapter)
    bindAdapter(KEY, worker)
    expect(pickAdapter(KEY, { send: 'execution' } as never, terminal)).toBe(worker)
    expect(pickAdapter(KEY, undefined, terminal)).toBe(worker) // an older daemon says nothing for a worker; its pane still bound it
    expect(await unavailableAdapter.port().submit('x', 'c')).toEqual({ kind: 'no_mod' })
    expect(await unavailableAdapter.port().interrupt()).toEqual({ kind: 'no_mod' })
    expect(unavailableAdapter.upload).toBeNull()
  })

  it('each adapter reads its own capability: the terminal\'s only `prompt` (a value it does not know is "cannot send"), the worker\'s always', () => {
    const t = terminalAdapter(H, 's1', 'dev001'), w = workerAdapter(H, E)
    expect(t.canSend({ send: 'prompt' } as never)).toBe(true)
    for (const send of ['execution', 'typed', 'none', undefined]) expect(t.canSend({ send } as never)).toBe(false)
    expect(t.canSend(undefined)).toBe(false)
    expect(w.canSend(undefined)).toBe(true)
    expect(w.canSend({ send: 'execution' } as never)).toBe(true)
    expect(t.recall).toBe(true)
    expect(w.recall).toBe(true)
    expect(unavailableAdapter.canSend({ send: 'prompt' } as never)).toBe(false)
    expect(unavailableAdapter.recall).toBe(false)
  })

  it('a released pane, and a session the pane no longer shows, let their bindings go', () => {
    bindAdapter(draftKey('p1', H, 's1'), workerAdapter(H, E))
    bindAdapter(draftKey('p1', H, 's2'), workerAdapter(H, E))
    bindAdapter(draftKey('p2', H, 's1'), workerAdapter(H, E))
    retireStaleSessions('p1', H, 's2')
    expect(adapterOf(draftKey('p1', H, 's1'))).toBeUndefined()
    expect(adapterOf(draftKey('p1', H, 's2'))).toBeDefined()
    releasePane('p1')
    expect(adapterOf(draftKey('p1', H, 's2'))).toBeUndefined()
    expect(adapterOf(draftKey('p2', H, 's1'))).toBeDefined()
    forgetAdaptersWhere(() => true)
    expect(adapterOf(draftKey('p2', H, 's1'))).toBeUndefined()
  })
})
