// spa/src/components/settings/profile/ResolveBlock.test.tsx — the Resolve rows and their confirmation (P3d-4 plan, the
// Resolve block; R1, R2). The counts helper and `requestResolve` are replaced here; resolve-counts.test.ts pins the
// counts and ResolveBlock.integration.test.tsx the whole way to the executor.
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { act, cleanup, fireEvent, render, screen, within } from '@testing-library/react'
import en from '../../../locales/en.json'
import { requestResolve } from '../../../lib/profile/start'
import { INVALID_REASONS, type InvalidReason } from '../../../lib/profile/apply-to-stores'
import type { ExecutorStatus, SectionLock } from '../../../lib/profile/executor'
import { COMMAND_TTL_MS } from '../../../lib/profile/sync-status'
import { describeSections, type SectionView } from '../../../lib/profile/sync-view'
import { readHostSide, readLocalSide, type HostSide, type LocalSide } from './resolve-counts'
import { ResolveBlock } from './ResolveBlock'

vi.mock('../../../lib/profile/start', () => ({ requestResolve: vi.fn() }))
vi.mock('./resolve-counts', () => ({ readLocalSide: vi.fn(), readHostSide: vi.fn() }))
// the real list (every code has a sentence), nothing else of that module
vi.mock('../../../lib/profile/apply-to-stores', async (importOriginal) => ({
  INVALID_REASONS: (await importOriginal<typeof import('../../../lib/profile/apply-to-stores')>()).INVALID_REASONS,
}))

const MASTER = { hostId: 'h1', profileId: 'p_0123456789ab' }
const H = (c: string) => c.repeat(64)
const CONFLICT: SectionLock = { status: 'locked:conflict', currentHash: H('a'), sot: { rev: 7, hash: H('b') }, conflict: { localHash: H('a'), sot: { rev: 7, hash: H('b') } } }
const RESET: SectionLock = { status: 'locked:reset', currentHash: H('c'), sot: { rev: 2, hash: H('d') }, conflict: null }
const INVALID: SectionLock = { status: 'locked:invalid', currentHash: H('e'), sot: { rev: 9, hash: H('f') }, conflict: null }

const detail = (invalidReason: InvalidReason | null = null) => ({ rev: 1, failures: 0, retryAt: null, invalidReason })

function statusOf(locks: Record<string, SectionLock>, extra: Record<string, ExecutorStatus['sections'][string]> = {}, reasons: Record<string, InvalidReason | null> = {}): ExecutorStatus {
  const sections = { ...extra, ...Object.fromEntries(Object.entries(locks).map(([k, l]) => [k, l.status])) }
  return {
    profile: 'locked:conflict',
    schemaLock: null,
    sections,
    locks,
    profileGone: false,
    detail: Object.fromEntries(Object.keys(sections).map((k) => [k, detail(reasons[k] ?? null)])),
    indexFailures: 0,
    lastSuccessAt: null,
  }
}

const labelOf = (view: SectionView): string => `label:${view.key}`

interface Opts {
  fromLeader?: boolean
  disabled?: boolean
}

function view(status: ExecutorStatus, opts: Opts = {}) {
  const el = (s: ExecutorStatus) => (
    <ResolveBlock master={MASTER} status={s} views={describeSections(Object.keys(s.sections), [])} labelOf={labelOf} fromLeader={opts.fromLeader ?? false} disabled={opts.disabled ?? false} />
  )
  const r = render(el(status))
  return { ...r, update: (s: ExecutorStatus) => r.rerender(el(s)) }
}

let resolveLocal: (v: LocalSide) => void
let resolveHost: (v: HostSide) => void

beforeEach(() => {
  vi.mocked(requestResolve).mockReset()
  vi.mocked(requestResolve).mockReturnValue(true)
  vi.mocked(readLocalSide).mockReset()
  vi.mocked(readHostSide).mockReset()
  vi.mocked(readLocalSide).mockImplementation(() => new Promise((r) => (resolveLocal = r)))
  vi.mocked(readHostSide).mockImplementation(() => new Promise((r) => (resolveHost = r)))
})

afterEach(() => {
  cleanup()
  vi.useRealTimers()
})

const openKeepLocal = (key: string) => fireEvent.click(screen.getByTestId(`profile-resolve-keep-local-${key}`))
const openTakeSot = (key: string) => fireEvent.click(screen.getByTestId(`profile-resolve-take-sot-${key}`))

describe('the rows', () => {
  it('nothing locked → no block at all', () => {
    view(statusOf({}, { hosts: 'synced' }))
    expect(screen.queryByTestId('profile-resolve-block')).toBeNull()
  })

  it('one row per lock, in the section list\'s order, labelled like it; unlocked sections have none', () => {
    view(statusOf({ workspaces: CONFLICT, hosts: RESET, settings: INVALID }, { 'tabs.w1': 'synced' }))
    const rows = within(screen.getByTestId('profile-resolve-block')).getAllByTestId(/^profile-resolve-row-/)
    expect(rows.map((r) => r.getAttribute('data-section'))).toEqual(['hosts', 'settings', 'workspaces'])
    expect(rows.map((r) => r.getAttribute('data-lock'))).toEqual(['locked:reset', 'locked:invalid', 'locked:conflict'])
    expect(rows[0]).toHaveTextContent('label:hosts')
    expect(screen.queryByTestId('profile-resolve-row-tabs.w1')).toBeNull()
  })

  it('conflict and reset: both ways out; invalid: ONLY "Keep this device\'s"', () => {
    view(statusOf({ workspaces: CONFLICT, hosts: RESET, settings: INVALID }))
    for (const key of ['workspaces', 'hosts']) {
      expect(screen.getByTestId(`profile-resolve-keep-local-${key}`)).toHaveTextContent(en['settings.profile.resolve.keep_local'])
      expect(screen.getByTestId(`profile-resolve-take-sot-${key}`)).toHaveTextContent(en['settings.profile.resolve.take_sot'])
    }
    expect(screen.getByTestId('profile-resolve-keep-local-settings')).toBeInTheDocument()
    expect(screen.queryByTestId('profile-resolve-take-sot-settings')).toBeNull()
    expect(screen.getByTestId('profile-resolve-why-settings')).toHaveTextContent(en['settings.profile.resolve.invalid_only'])
  })

  it('says why, per kind', () => {
    view(statusOf({ workspaces: CONFLICT, hosts: RESET }))
    expect(screen.getByTestId('profile-resolve-why-workspaces')).toHaveTextContent(en['settings.profile.resolve.why.conflict'])
    expect(screen.getByTestId('profile-resolve-why-hosts')).toHaveTextContent(en['settings.profile.resolve.why.reset'])
  })

  it.each(INVALID_REASONS)('invalid, reason %s → its own sentence', (code) => {
    view(statusOf({ settings: INVALID }, {}, { settings: code }))
    expect(screen.getByTestId('profile-resolve-why-settings')).toHaveAttribute('data-reason', code)
    expect(screen.getByTestId('profile-resolve-why-settings')).toHaveTextContent(en[`settings.profile.resolve.why.invalid.${code.replace(/-/g, '_')}` as keyof typeof en])
  })

  it('invalid with no reason (an older leader, or a code this build does not know) → the general sentence, not a guess', () => {
    view(statusOf({ settings: INVALID }))
    expect(screen.getByTestId('profile-resolve-why-settings')).toHaveAttribute('data-reason', 'unknown')
    expect(screen.getByTestId('profile-resolve-why-settings')).toHaveTextContent(en['settings.profile.resolve.why.invalid.unknown'])
  })

  it('a conflict edited here since it arose: "Keep this device\'s" also undoes that — said on the row', () => {
    const { update } = view(statusOf({ workspaces: CONFLICT }))
    expect(screen.queryByTestId('profile-resolve-undoes-workspaces')).toBeNull()
    update(statusOf({ workspaces: { ...CONFLICT, currentHash: H('9') } }))
    expect(screen.getByTestId('profile-resolve-undoes-workspaces')).toHaveTextContent(en['settings.profile.resolve.undoes'])
  })

  it('a follower window: every row carries the "reported by the window that is syncing" badge', () => {
    view(statusOf({ workspaces: CONFLICT, hosts: RESET }), { fromLeader: true })
    for (const key of ['workspaces', 'hosts']) {
      expect(within(screen.getByTestId(`profile-resolve-row-${key}`)).getByTestId(`profile-resolve-source-${key}`)).toHaveTextContent(en['settings.profile.current.from_leader'])
      expect(screen.getByTestId(`profile-resolve-row-${key}`)).toHaveAttribute('data-source', 'leader')
    }
  })

  it('the leader window: no badge', () => {
    view(statusOf({ workspaces: CONFLICT }))
    expect(screen.queryByTestId('profile-resolve-source-workspaces')).toBeNull()
  })

  it('while no driver runs (blocked): the buttons are disabled — a press would do nothing', () => {
    view(statusOf({ workspaces: CONFLICT }), { disabled: true })
    expect(screen.getByTestId('profile-resolve-keep-local-workspaces')).toBeDisabled()
    expect(screen.getByTestId('profile-resolve-take-sot-workspaces')).toBeDisabled()
  })

  it('opening the page starts no timer and reads nothing', () => {
    vi.useFakeTimers()
    view(statusOf({ workspaces: CONFLICT, hosts: RESET, settings: INVALID }))
    expect(vi.getTimerCount()).toBe(0)
    expect(readHostSide).not.toHaveBeenCalled()
    expect(readLocalSide).not.toHaveBeenCalled()
  })
})

describe('the confirmation', () => {
  it('opens with the row\'s lock FROZEN and reads each side once: the counts inform, they do not gate', async () => {
    view(statusOf({ workspaces: CONFLICT }))
    openKeepLocal('workspaces')
    const dialog = screen.getByTestId('profile-resolve-dialog')
    expect(dialog).toHaveTextContent(en['settings.profile.resolve.keep_local_body'])
    expect(readLocalSide).toHaveBeenCalledTimes(1)
    expect(readLocalSide).toHaveBeenCalledWith(MASTER.profileId, 'workspaces', CONFLICT)
    expect(readHostSide).toHaveBeenCalledTimes(1)
    expect(readHostSide).toHaveBeenCalledWith(MASTER.hostId, MASTER.profileId, 'workspaces', CONFLICT, expect.any(AbortSignal))
    expect(screen.getByTestId('profile-resolve-count-local')).toHaveAttribute('data-state', 'loading')
    expect(screen.getByTestId('profile-resolve-count-sot')).toHaveAttribute('data-state', 'loading')
    expect(screen.getByTestId('profile-resolve-confirm')).not.toBeDisabled()
    await act(async () => {
      resolveLocal({ count: { state: 'read', count: 2 }, changedSince: false })
      resolveHost({ count: { state: 'unreadable' }, movedOn: false })
    })
    expect(screen.getByTestId('profile-resolve-count-local')).toHaveAttribute('data-state', 'read')
    expect(screen.getByTestId('profile-resolve-count-local')).toHaveTextContent(en['settings.profile.resolve.unit.workspaces'].replace('{{count}}', '2'))
    expect(screen.getByTestId('profile-resolve-count-sot')).toHaveAttribute('data-state', 'unreadable')
    expect(screen.getByTestId('profile-resolve-count-sot')).toHaveTextContent(en['settings.profile.resolve.count_unreadable'])
    expect(screen.getByTestId('profile-resolve-confirm')).not.toBeDisabled()
    expect(readHostSide).toHaveBeenCalledTimes(1)
  })

  it('says when the host changed again, and when this device changed since the lock', async () => {
    view(statusOf({ hosts: RESET }))
    openKeepLocal('hosts')
    expect(screen.queryByTestId('profile-resolve-sot-moved')).toBeNull()
    await act(async () => {
      resolveLocal({ count: { state: 'read', count: 3 }, changedSince: true })
      resolveHost({ count: { state: 'read', count: 4 }, movedOn: true })
    })
    expect(screen.getByTestId('profile-resolve-sot-moved')).toHaveTextContent(en['settings.profile.resolve.sot_moved'])
    expect(screen.getByTestId('profile-resolve-local-moved')).toHaveTextContent(en['settings.profile.resolve.local_moved'])
    expect(screen.getByTestId('profile-resolve-count-sot')).toHaveTextContent(en['settings.profile.resolve.unit.hosts'].replace('{{count}}', '4'))
  })

  it('a conflict edited here since: the dialog says that keeping this device\'s puts back what was SENT', () => {
    view(statusOf({ workspaces: { ...CONFLICT, currentHash: H('9') } }))
    openKeepLocal('workspaces')
    expect(screen.getByTestId('profile-resolve-dialog-undoes')).toHaveTextContent(en['settings.profile.resolve.dialog_undoes'])
  })

  it('"Take the host\'s" has its own words, and no "undoes" line', () => {
    view(statusOf({ workspaces: { ...CONFLICT, currentHash: H('9') } }))
    openTakeSot('workspaces')
    expect(screen.getByTestId('profile-resolve-dialog')).toHaveTextContent(en['settings.profile.resolve.take_sot_body'])
    expect(screen.queryByTestId('profile-resolve-dialog-undoes')).toBeNull()
  })

  it('confirm → requestResolve(key, keep, THE FROZEN LOCK); the row says "sent"', () => {
    view(statusOf({ hosts: RESET }))
    openTakeSot('hosts')
    fireEvent.click(screen.getByTestId('profile-resolve-confirm'))
    expect(requestResolve).toHaveBeenCalledWith('hosts', 'sot', RESET)
    expect(screen.queryByTestId('profile-resolve-dialog')).toBeNull()
    expect(screen.getByTestId('profile-resolve-sent-hosts')).toHaveAttribute('data-state', 'sent')
    expect(screen.getByTestId('profile-resolve-sent-hosts')).toHaveTextContent(en['settings.profile.resolve.sent'])
  })

  it('cancel sends nothing', () => {
    view(statusOf({ hosts: RESET }))
    openKeepLocal('hosts')
    fireEvent.click(screen.getByTestId('profile-resolve-cancel'))
    expect(screen.queryByTestId('profile-resolve-dialog')).toBeNull()
    expect(requestResolve).not.toHaveBeenCalled()
  })

  it('the live lock moves while the dialog is open → it closes ITSELF, the row says so, nothing is sent', () => {
    const { update } = view(statusOf({ hosts: RESET }))
    openKeepLocal('hosts')
    update(statusOf({ hosts: { ...RESET, sot: { rev: 1, hash: H('0') } } }))
    expect(screen.queryByTestId('profile-resolve-dialog')).toBeNull()
    expect(screen.getByTestId('profile-resolve-changed-hosts')).toHaveTextContent(en['settings.profile.resolve.changed'])
    expect(requestResolve).not.toHaveBeenCalled()
  })

  it('a NEW snapshot with the SAME lock (another section moved) does not close it', () => {
    const { update } = view(statusOf({ hosts: RESET }))
    openKeepLocal('hosts')
    update(statusOf({ hosts: { ...RESET, sot: { ...RESET.sot } } }, { settings: 'pending' }))
    expect(screen.getByTestId('profile-resolve-dialog')).toBeInTheDocument()
    expect(screen.queryByTestId('profile-resolve-changed-hosts')).toBeNull()
    // …and does not ask the host again: ONE read per confirmation
    expect(readHostSide).toHaveBeenCalledTimes(1)
    expect(readLocalSide).toHaveBeenCalledTimes(1)
  })

  it('an answer that arrives after the dialog closed sets nothing (no warning, no stale count next time)', async () => {
    view(statusOf({ hosts: RESET }))
    openKeepLocal('hosts')
    const firstHost = resolveHost
    fireEvent.click(screen.getByTestId('profile-resolve-cancel'))
    openKeepLocal('hosts')
    await act(async () => firstHost({ count: { state: 'read', count: 99 }, movedOn: true }))
    expect(screen.getByTestId('profile-resolve-count-sot')).toHaveAttribute('data-state', 'loading')
    expect(screen.queryByTestId('profile-resolve-sot-moved')).toBeNull()
  })
})

describe('"sent" ends (R2)', () => {
  it('not written (requestResolve → false) → "could not be sent"', () => {
    vi.mocked(requestResolve).mockReturnValue(false)
    view(statusOf({ hosts: RESET }))
    openKeepLocal('hosts')
    fireEvent.click(screen.getByTestId('profile-resolve-confirm'))
    expect(screen.getByTestId('profile-resolve-sent-hosts')).toHaveAttribute('data-state', 'not-sent')
    expect(screen.getByTestId('profile-resolve-sent-hosts')).toHaveTextContent(en['settings.profile.resolve.not_sent'])
    expect(screen.getByTestId('profile-resolve-keep-local-hosts')).not.toBeDisabled()
  })

  it('no answer by the command\'s TTL → "no answer; try again", and the buttons are back', async () => {
    vi.useFakeTimers()
    view(statusOf({ hosts: RESET }))
    openKeepLocal('hosts')
    fireEvent.click(screen.getByTestId('profile-resolve-confirm'))
    expect(screen.getByTestId('profile-resolve-keep-local-hosts')).toBeDisabled()
    await act(async () => vi.advanceTimersByTime(COMMAND_TTL_MS - 1))
    expect(screen.getByTestId('profile-resolve-sent-hosts')).toHaveAttribute('data-state', 'sent')
    await act(async () => vi.advanceTimersByTime(1))
    expect(screen.getByTestId('profile-resolve-sent-hosts')).toHaveAttribute('data-state', 'no-answer')
    expect(screen.getByTestId('profile-resolve-sent-hosts')).toHaveTextContent(en['settings.profile.resolve.no_answer'])
    expect(screen.getByTestId('profile-resolve-keep-local-hosts')).not.toBeDisabled()
    expect(vi.getTimerCount()).toBe(0)
  })

  it('the lock for that key changes → "sent" is over, and its timer with it', async () => {
    vi.useFakeTimers()
    const { update } = view(statusOf({ hosts: RESET }))
    openKeepLocal('hosts')
    fireEvent.click(screen.getByTestId('profile-resolve-confirm'))
    expect(vi.getTimerCount()).toBe(1)
    update(statusOf({ hosts: { ...RESET, currentHash: H('1') } }))
    expect(screen.queryByTestId('profile-resolve-sent-hosts')).toBeNull()
    expect(vi.getTimerCount()).toBe(0)
  })

  it('the section unlocks → its row is gone, and the timer too', async () => {
    vi.useFakeTimers()
    const { update } = view(statusOf({ hosts: RESET, workspaces: CONFLICT }))
    openKeepLocal('hosts')
    fireEvent.click(screen.getByTestId('profile-resolve-confirm'))
    update(statusOf({ workspaces: CONFLICT }, { hosts: 'pending' }))
    expect(screen.queryByTestId('profile-resolve-row-hosts')).toBeNull()
    expect(vi.getTimerCount()).toBe(0)
  })
})
