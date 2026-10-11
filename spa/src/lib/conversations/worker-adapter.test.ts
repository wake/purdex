// U4-2: the worker's deck adapter (what the deck's input does on a Nexen execution) and the registry the input finds its adapter in.
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { adapterOf, bindAdapter, clearAllAdapters, forgetAdaptersWhere } from './deck-adapter'
import { draftKey, forgetDraft, readDraft, writeDraft, clearAllDrafts } from './draft-memory'
import { clearAllSendQueues, sendQueueFor } from './send-queue'
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
