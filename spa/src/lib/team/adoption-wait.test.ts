// adoption-wait.ts — the wait after a remote adopt is approved: every terminal answer, the 11 minute bound, retry on
// errors, one poll loop per approval, and a toast when the card was closed first. fetchAdoption is the only mock.
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { useAdoptionWait, startAdoptionWait, resumeAdoptionWaits, resetAdoptionWaitForTests, ADOPTION_WAIT_BOUND_MS } from './adoption-wait'
import { STORAGE_KEYS } from '../storage'
import { ApprovalApiError, fetchAdoption } from './approval-api'
import { adoptPayloadOf, type Approval } from './types'
import { useI18nStore } from '../../stores/useI18nStore'
import { useUndoToast } from '../../stores/useUndoToast'

vi.mock('./approval-api', async (importOriginal) => ({
  ...(await importOriginal<typeof import('./approval-api')>()),
  fetchAdoption: vi.fn(),
}))
const mocked = vi.mocked(fetchAdoption)

const payload = adoptPayloadOf({
  id: 'ap-1', kind: 'adopt', payload: { title: '寫文件的', target_host_id: 'hid-b', target_host_alias: 'air26' },
} as unknown as Approval)
const key = () => Object.keys(useAdoptionWait.getState().entries)[0]
const entry = () => useAdoptionWait.getState().entries[key()]
const ans = (state: string, code?: string) => ({ approval_id: 'ap-1', state, ...(code ? { code } : {}) })
const flush = () => vi.advanceTimersByTimeAsync(0)

beforeEach(() => {
  vi.useFakeTimers()
  useI18nStore.getState().setLocale('zh-TW')
  resetAdoptionWaitForTests()
  useUndoToast.setState({ toast: null, notice: null })
  mocked.mockReset()
})
afterEach(() => { resetAdoptionWaitForTests(); vi.useRealTimers() })

describe('adoptPayloadOf — the remote fields', () => {
  it('reads target_host_id / target_host_alias; absent = a local target', () => {
    expect(payload.target_host_id).toBe('hid-b')
    expect(payload.target_host_alias).toBe('air26')
    const local = adoptPayloadOf({ id: 'x', kind: 'adopt', payload: { title: 't' } } as unknown as Approval)
    expect(local.target_host_id).toBe('')
    expect(local.target_host_alias).toBe('')
    expect(adoptPayloadOf({ id: 'x', kind: 'adopt', payload: { target_host_id: 7 } } as unknown as Approval).target_host_id).toBe('')
  })
})

describe('startAdoptionWait', () => {
  it('polls the lead host with wait=30 and keeps waiting on joining', async () => {
    mocked.mockResolvedValueOnce(ans('joining')).mockImplementation(() => new Promise(() => {}))
    startAdoptionWait('lead', 'ap-1', payload)
    await flush()
    expect(mocked).toHaveBeenCalledWith('lead', 'ap-1', 30, expect.any(AbortSignal))
    expect(entry().state).toBe('waiting')
    expect(entry().alias).toBe('air26')
    await vi.advanceTimersByTimeAsync(1_000)
    expect(mocked).toHaveBeenCalledTimes(2)
  })

  it.each(['active', 'releasing', 'released', 'killing', 'killed', 'gone'])('%s counts as joined', async (state) => {
    mocked.mockResolvedValue(ans(state))
    startAdoptionWait('lead', 'ap-1', payload)
    await flush()
    expect(entry().state).toBe('active')
    await vi.advanceTimersByTimeAsync(2_000)
    expect(useAdoptionWait.getState().entries).toEqual({}) // closes as today
  })

  it('a state this client does not know is never read as joined: it keeps waiting and warns once', async () => {
    const warn = vi.spyOn(console, 'warn').mockImplementation(() => {})
    mocked.mockResolvedValue(ans('teleporting'))
    startAdoptionWait('lead', 'ap-1', payload)
    await vi.advanceTimersByTimeAsync(5_000)
    expect(entry().state).toBe('waiting')
    expect(mocked.mock.calls.length).toBeGreaterThan(2)
    expect(warn).toHaveBeenCalledTimes(1)
    warn.mockRestore()
  })

  it('failed keeps the code', async () => {
    mocked.mockResolvedValue(ans('failed', 'dir_missing'))
    startAdoptionWait('lead', 'ap-1', payload)
    await flush()
    expect(entry()).toMatchObject({ state: 'failed', code: 'dir_missing' })
  })

  it('void is its own ending', async () => {
    mocked.mockResolvedValue(ans('void'))
    startAdoptionWait('lead', 'ap-1', payload)
    await flush()
    expect(entry().state).toBe('void')
  })

  it('a request error backs off and asks again', async () => {
    mocked.mockRejectedValueOnce(new ApprovalApiError(0, 'network')).mockResolvedValueOnce(ans('active'))
    startAdoptionWait('lead', 'ap-1', payload)
    await flush()
    expect(entry().state).toBe('waiting')
    expect(mocked).toHaveBeenCalledTimes(1)
    await vi.advanceTimersByTimeAsync(2_000)
    expect(mocked).toHaveBeenCalledTimes(2)
    expect(entry().state).toBe('active')
  })

  it.each([[404, 'unsupported'], [404, 'not_found'], [409, 'not_approved'], [400, 'bad_request'], [0, 'host_removed']])('HTTP %i %s is permanent: no retry, ends failed with the reason', async (status, code) => {
    mocked.mockRejectedValue(new ApprovalApiError(status, code))
    startAdoptionWait('lead', 'ap-1', payload)
    await vi.advanceTimersByTimeAsync(60_000)
    expect(mocked).toHaveBeenCalledTimes(1)
    expect(entry()).toMatchObject({ state: 'failed', code })
  })

  it.each([[429, 'http_429'], [500, 'http_500'], [503, 'http_503']])('HTTP %i is retried', async (status, code) => {
    mocked.mockRejectedValueOnce(new ApprovalApiError(status, code)).mockResolvedValueOnce(ans('active'))
    startAdoptionWait('lead', 'ap-1', payload)
    await vi.advanceTimersByTimeAsync(2_500)
    expect(mocked).toHaveBeenCalledTimes(2)
    expect(entry().state).toBe('active')
  })

  it('a second start for the same approval does not open a second loop', async () => {
    mocked.mockImplementation(() => new Promise(() => {}))
    startAdoptionWait('lead', 'ap-1', payload)
    startAdoptionWait('lead', 'ap-1', payload)
    await flush()
    expect(mocked).toHaveBeenCalledTimes(1)
  })

  it('closed while waiting: the card hides, the wait goes on, the outcome is a toast', async () => {
    let resolve!: (v: ReturnType<typeof ans>) => void
    mocked.mockImplementation(() => new Promise((r) => { resolve = r }))
    startAdoptionWait('lead', 'ap-1', payload)
    await flush()
    useAdoptionWait.getState().dismiss(key())
    expect(entry().dismissed).toBe(true)
    expect(useUndoToast.getState().toast).toBeNull()
    resolve(ans('void'))
    await flush()
    expect(useUndoToast.getState().toast?.message).toBe('寫文件的：air26 十分鐘沒有回應，這次納入已作廢')
    expect(useAdoptionWait.getState().entries).toEqual({})
  })

  it('at the 11 minute bound asks once more (wait=0), and no answer ends as timeout', async () => {
    mocked.mockResolvedValue(ans('joining'))
    startAdoptionWait('lead', 'ap-1', payload)
    await vi.advanceTimersByTimeAsync(ADOPTION_WAIT_BOUND_MS + 2_000)
    const waits = mocked.mock.calls.map((c) => c[2])
    expect(waits[waits.length - 1]).toBe(0)
    expect(waits.filter((w) => w === 0)).toHaveLength(1)
    expect(entry().state).toBe('timeout')
  })

  it('the last ask can still find the answer', async () => {
    mocked.mockResolvedValueOnce(ans('joining'))
    mocked.mockImplementation(async (_h, _id, wait) => (wait === 0 ? ans('active') : ans('joining')))
    startAdoptionWait('lead', 'ap-1', payload)
    await vi.advanceTimersByTimeAsync(ADOPTION_WAIT_BOUND_MS + 2_000)
    expect(entry()?.state ?? 'closed').not.toBe('timeout')
  })
})

describe('a reload does not lose the wait or its result', () => {
  const persistedEntry = (over: Record<string, unknown> = {}) => ({
    hostId: 'lead', approvalId: 'ap-1', alias: 'air26', target: '寫文件的', startedAt: Date.now(), state: 'waiting', code: '', dismissed: false, ...over,
  })
  const writeAndReload = async (entries: unknown) => {
    localStorage.setItem(STORAGE_KEYS.ADOPTION_WAITS, JSON.stringify({ state: { entries }, version: 0 }))
    await useAdoptionWait.persist.rehydrate()
  }

  it('a finished result survives a reload and shows again', async () => {
    mocked.mockResolvedValue(ans('failed', 'dir_missing'))
    startAdoptionWait('lead', 'ap-1', payload)
    await flush()
    const raw = localStorage.getItem(STORAGE_KEYS.ADOPTION_WAITS)!
    useAdoptionWait.setState({ entries: {} }) // the new window starts empty …
    localStorage.setItem(STORAGE_KEYS.ADOPTION_WAITS, raw) // … and reads what the old one saved
    await useAdoptionWait.persist.rehydrate()
    expect(entry()).toMatchObject({ state: 'failed', code: 'dir_missing', alias: 'air26' })
  })

  it('a wait in progress resumes by its ORIGINAL deadline', async () => {
    await writeAndReload({ k: persistedEntry({ startedAt: Date.now() - 10 * 60_000 }) })
    mocked.mockImplementation(hanging)
    resumeAdoptionWaits()
    await flush()
    // 11 min bound, 10 elapsed: about 60 s left, so the long poll is 30 s (not a fresh 11 minutes of them).
    await vi.advanceTimersByTimeAsync(80_000)
    expect(entry().state).toBe('timeout')
    expect(mocked.mock.calls.filter((c) => c[2] === 0)).toHaveLength(1)
  })

  it('a wait already past its deadline gets only the final ask', async () => {
    await writeAndReload({ k: persistedEntry({ startedAt: Date.now() - 12 * 60_000 }) })
    mocked.mockResolvedValue(ans('joining'))
    resumeAdoptionWaits()
    await flush()
    expect(mocked).toHaveBeenCalledTimes(1)
    expect(mocked.mock.calls[0][2]).toBe(0)
    expect(entry().state).toBe('timeout')
  })

  it('resuming twice does not start a second loop', async () => {
    await writeAndReload({ k: persistedEntry() })
    mocked.mockImplementation(hanging)
    resumeAdoptionWaits()
    resumeAdoptionWaits()
    await flush()
    expect(mocked).toHaveBeenCalledTimes(1)
  })

  it('a dismissed wait still toasts after the reload', async () => {
    await writeAndReload({ k: persistedEntry({ dismissed: true }) })
    mocked.mockResolvedValue(ans('active'))
    resumeAdoptionWaits()
    await flush()
    expect(useUndoToast.getState().toast?.message).toBe('寫文件的：已納入')
    expect(useAdoptionWait.getState().entries).toEqual({})
  })

  it('bad data is dropped, good entries around it are kept', async () => {
    await writeAndReload({
      a: persistedEntry({ approvalId: '' }),
      b: persistedEntry({ startedAt: 'yesterday' }),
      c: persistedEntry({ state: 'exploded' }),
      d: persistedEntry({ dismissed: true, state: 'failed' }), // already dealt with
      e: 'nonsense',
      f: persistedEntry({ approvalId: 'ap-ok', key: 'forged' }),
    })
    expect(Object.values(useAdoptionWait.getState().entries).map((e) => e.approvalId)).toEqual(['ap-ok'])
    expect(Object.keys(useAdoptionWait.getState().entries)[0]).toBe('lead\u0000ap-ok')
    await writeAndReload([persistedEntry()]) // entries of the wrong shape
    expect(useAdoptionWait.getState().entries).toEqual({})
    localStorage.setItem(STORAGE_KEYS.ADOPTION_WAITS, JSON.stringify({ state: 'nonsense', version: 0 }))
    await useAdoptionWait.persist.rehydrate()
    expect(useAdoptionWait.getState().entries).toEqual({})
  })
})

// A fetch that never answers and ends only when its signal aborts (what the browser does).
const hanging = (_h: string, _id: string, _w: number, signal?: AbortSignal) => new Promise<never>((_, reject) => {
  signal?.addEventListener('abort', () => reject(new ApprovalApiError(0, 'network', 'aborted')))
})

describe('the bound holds against a request that hangs', () => {
  it('a stuck long poll is aborted shortly after the wait it asked for, and the loop asks again', async () => {
    mocked.mockImplementation(hanging)
    startAdoptionWait('lead', 'ap-1', payload)
    await flush()
    expect(mocked).toHaveBeenCalledTimes(1)
    await vi.advanceTimersByTimeAsync(35_000) // 30 s wait + 5 s slack
    await vi.advanceTimersByTimeAsync(2_000) // the backoff
    expect(mocked.mock.calls.length).toBeGreaterThanOrEqual(2)
  })

  it('ends in timeout at the deadline with exactly one final ask, no wait over 30 s', async () => {
    mocked.mockImplementation(hanging)
    startAdoptionWait('lead', 'ap-1', payload)
    await vi.advanceTimersByTimeAsync(ADOPTION_WAIT_BOUND_MS + 10_000)
    for (const c of mocked.mock.calls) expect(c[2]).toBeLessThanOrEqual(30)
    expect(mocked.mock.calls.filter((c) => c[2] === 0)).toHaveLength(1)
    expect(entry().state).toBe('timeout')
  })

  it('the last long poll is shortened to the time left', async () => {
    mocked.mockResolvedValue(ans('joining')) // each ask comes straight back: one per second
    startAdoptionWait('lead', 'ap-1', payload)
    await vi.advanceTimersByTimeAsync(ADOPTION_WAIT_BOUND_MS + 10_000)
    const waits = mocked.mock.calls.map((c) => c[2])
    expect(waits.some((w) => w > 0 && w < 30)).toBe(true)
  })
})
