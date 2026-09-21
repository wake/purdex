import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import en from '../locales/en.json'
import { __resetProfileSwitcherForTest, BUSY_RETRY_MS, BUSY_RETRY_TOTAL_MS, useProfileSwitcherStore } from './useProfileSwitcherStore'
import { useUndoToast } from './useUndoToast'
import { switchActiveProfile, type SwitchResult } from '../lib/profile/switch-active'

vi.mock('../lib/profile/switch-active', () => ({ switchActiveProfile: vi.fn() }))

const store = () => useProfileSwitcherStore.getState()
const toast = () => useUndoToast.getState().toast?.message ?? null
const advance = (ms: number) => vi.advanceTimersByTimeAsync(ms)

/** Every call gets its own promise, answered by hand. */
function deferred() {
  const answers: ((r: SwitchResult) => void)[] = []
  vi.mocked(switchActiveProfile).mockImplementation(() => new Promise((resolve) => { answers.push(resolve) }))
  return { answer: async (i: number, r: SwitchResult) => { answers[i](r); await advance(0) } }
}

beforeEach(() => {
  vi.useFakeTimers()
  vi.mocked(switchActiveProfile).mockReset()
  __resetProfileSwitcherForTest()
  useUndoToast.setState({ toast: null })
})

afterEach(() => {
  __resetProfileSwitcherForTest()
  vi.useRealTimers()
})

describe('useProfileSwitcherStore — the switch under way has one owner', () => {
  it('is idle until asked: no pending, no timer', () => {
    expect(store().pending).toBeNull()
    expect(vi.getTimerCount()).toBe(0)
    store().setOpen(true)
    store().setOpen(false)
    expect(vi.getTimerCount()).toBe(0)
  })

  it('chooseProfile records what is pending and calls switchActiveProfile once', () => {
    deferred()
    vi.setSystemTime(1_000)
    expect(store().chooseProfile('s1')).toBe(true)
    expect(store().pending).toMatchObject({ targetId: 's1', startedAt: 1_000, abandoned: false })
    expect(switchActiveProfile).toHaveBeenCalledTimes(1)
    expect(switchActiveProfile).toHaveBeenCalledWith('s1')
  })

  it('a second choice while one is pending is refused — whoever asks', () => {
    deferred()
    store().chooseProfile('s1')
    expect(store().chooseProfile('s2')).toBe(false)
    expect(switchActiveProfile).toHaveBeenCalledTimes(1)
    expect(store().pending?.targetId).toBe('s1')
  })

  it('success clears pending and closes the menu', async () => {
    const d = deferred()
    store().setOpen(true)
    store().chooseProfile('s1')
    await d.answer(0, { ok: true })
    expect(store().pending).toBeNull()
    expect(store().open).toBe(false)
    expect(vi.getTimerCount()).toBe(0)
  })

  it('every generation is new, and an answer under an old one is dropped', async () => {
    const d = deferred()
    store().setOpen(true)
    store().chooseProfile('s1')
    const first = store().pending!.generation
    __resetProfileSwitcherForTest() // whatever took `pending` away: the first question is nobody's any more
    store().setOpen(true)
    store().chooseProfile('s2')
    expect(store().pending!.generation).toBeGreaterThan(first)

    await d.answer(0, { ok: false, reason: 'write-failed', detail: 'old' })
    expect(toast()).toBeNull()
    expect(store().pending?.targetId).toBe('s2')
    await d.answer(0, { ok: true }) // answering twice changes nothing
    expect(store().open).toBe(true)

    await d.answer(1, { ok: true })
    expect(store().pending).toBeNull()
    expect(store().open).toBe(false)
  })

  it('an old generation\'s busy schedules no retry', async () => {
    const d = deferred()
    store().chooseProfile('s1')
    __resetProfileSwitcherForTest()
    await d.answer(0, { ok: false, reason: 'busy' })
    expect(vi.getTimerCount()).toBe(0)
    await advance(60_000)
    expect(switchActiveProfile).toHaveBeenCalledTimes(1)
  })
})

describe('useProfileSwitcherStore — closing the menu is "stop trying", not "undo"', () => {
  it('closed between two attempts: the timer is cleared and the switch is over', async () => {
    const d = deferred()
    store().setOpen(true)
    store().chooseProfile('s1')
    await d.answer(0, { ok: false, reason: 'busy' })
    expect(vi.getTimerCount()).toBe(1)
    store().setOpen(false)
    expect(vi.getTimerCount()).toBe(0)
    expect(store().pending).toBeNull()
    await advance(60_000)
    expect(switchActiveProfile).toHaveBeenCalledTimes(1)
    expect(toast()).toBeNull()
  })

  it('closed with a call in flight: pending stays (no second switch) until it answers', async () => {
    const d = deferred()
    store().setOpen(true)
    store().chooseProfile('s1')
    store().setOpen(false)
    expect(store().pending).toMatchObject({ targetId: 's1', abandoned: true })
    store().setOpen(true)
    expect(store().chooseProfile('s2')).toBe(false)
    await d.answer(0, { ok: true })
    expect(store().pending).toBeNull()
    expect(store().open).toBe(false) // the world changed: the menu re-opened meanwhile goes, as after any success
  })

  it('… and its busy is neither retried nor said', async () => {
    const d = deferred()
    store().setOpen(true)
    store().chooseProfile('s1')
    store().setOpen(false)
    await d.answer(0, { ok: false, reason: 'busy' })
    expect(store().pending).toBeNull()
    expect(vi.getTimerCount()).toBe(0)
    await advance(60_000)
    expect(switchActiveProfile).toHaveBeenCalledTimes(1)
    expect(toast()).toBeNull()
  })

  it('… but a real failure is still said', async () => {
    const d = deferred()
    store().setOpen(true)
    store().chooseProfile('s1')
    store().setOpen(false)
    await d.answer(0, { ok: false, reason: 'write-failed', detail: 'QuotaExceededError' })
    expect(toast()).toBe(en['profile.switch.write_failed'].replace('{{detail}}', 'QuotaExceededError'))
    expect(store().pending).toBeNull()
  })

  it('an OPEN menu keeps retrying: every BUSY_RETRY_MS until BUSY_RETRY_TOTAL_MS, then one toast and idle again', async () => {
    vi.mocked(switchActiveProfile).mockResolvedValue({ ok: false, reason: 'busy' })
    store().setOpen(true)
    store().chooseProfile('s1')
    await advance(BUSY_RETRY_TOTAL_MS - 1)
    expect(toast()).toBeNull()
    expect(store().pending).not.toBeNull()
    await advance(1)
    expect(switchActiveProfile).toHaveBeenCalledTimes(BUSY_RETRY_TOTAL_MS / BUSY_RETRY_MS + 1)
    expect(toast()).toBe(en['profile.switch.busy'])
    expect(store().pending).toBeNull()
    expect(store().open).toBe(true)
    expect(vi.getTimerCount()).toBe(0)
  })
})
