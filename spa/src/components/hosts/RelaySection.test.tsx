import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, fireEvent, waitFor, act } from '@testing-library/react'
import { RelaySection } from './RelaySection'
import { useHostStore } from '../../stores/useHostStore'
import { emptyHostConfigEntry, useHostConfigStore, type HostConfigEntry } from '../../stores/useHostConfigStore'
import { useI18nStore } from '../../stores/useI18nStore'
import { HostConfigApiError, HostConfigConflictError, type RelaySwitches } from '../../lib/host-config-api'
import { fetchRelayPrompts, type RelayPrompts } from '../../lib/relay-prompts-api'
import { queueHostConfigSave } from '../../lib/host-config-queue'
import { clearAllRelayPromptDrafts, readRelayPromptDraft } from '../../lib/relay-prompt-draft-memory'

vi.mock('../../lib/relay-prompts-api', async (importOriginal) => ({
  ...(await importOriginal<typeof import('../../lib/relay-prompts-api')>()), fetchRelayPrompts: vi.fn(),
}))
// The real queue, watched: a prompt save must take the switches' key.
vi.mock('../../lib/host-config-queue', async (importOriginal) => {
  const real = await importOriginal<typeof import('../../lib/host-config-queue')>()
  return { ...real, queueHostConfigSave: vi.fn(real.queueHostConfigSave) }
})

const H = 'h1'
const saveRelay = vi.fn()
const PROMPTS: RelayPrompts = {
  write: 'default write', fix: 'default fix', seed: 'default seed',
  defaults: { write: 'default write', fix: 'default fix', seed: 'default seed' },
  fixed: {
    write: { head: '[pdx-relay op={{op}} n={{nonce}}] ', tail: '# HANDOFF' },
    fix: { head: '[pdx-relay op={{op}} n={{nonce}}] ', tail: '缺少段落：{{missing}}' },
    seed: { head: '↪ 接手自 {{old_ref}}\n[pdx-relay seed op={{op}} n={{nonce}}] ', tail: '' },
  },
  variables: ['path', 'old_ref', 'old_session', 'context', 'whoami'],
}

function entry(relay: RelaySwitches, supported = true, status: HostConfigEntry['status'] = 'ready'): HostConfigEntry {
  const e = emptyHostConfigEntry(status)
  return { ...e, relay, relaySupported: supported, revisions: { ...e.revisions, relay: 1 } }
}

function seed(e: HostConfigEntry) {
  useHostConfigStore.setState({ byHost: { [H]: e }, load: vi.fn(async () => {}), saveRelay })
}

beforeEach(() => {
  useI18nStore.getState().setLocale('zh-TW')
  saveRelay.mockReset().mockImplementation(async (hostId: string, items: RelaySwitches) => {
    useHostConfigStore.setState((s) => {
      const cur = s.byHost[hostId]
      return { byHost: { ...s.byHost, [hostId]: { ...cur, relay: items, revisions: { ...cur.revisions, relay: cur.revisions.relay + 1 } } } }
    })
  })
  useHostStore.setState({
    hosts: { [H]: { id: H, name: 'mlab', ip: '1.2.3.4', port: 7860, order: 0 } },
    hostOrder: [H], runtime: { [H]: { status: 'connected' } },
  })
  vi.mocked(fetchRelayPrompts).mockReset().mockResolvedValue(PROMPTS)
  vi.mocked(queueHostConfigSave).mockClear()
  clearAllRelayPromptDrafts()
})
afterEach(() => clearAllRelayPromptDrafts())

describe('RelaySection', () => {
  it('shows the two switches with the stored values and the member line (spec §8.7 (a))', () => {
    seed(entry({ self_solo: true, self_lead: false }))
    render(<RelaySection hostId={H} />)
    expect(screen.getByText('接力')).toBeTruthy()
    expect(screen.getByTestId('relay-self-solo').getAttribute('aria-checked')).toBe('true')
    expect(screen.getByTestId('relay-self-lead').getAttribute('aria-checked')).toBe('false')
    expect(screen.getByTestId('relay-member-note').textContent).toBe('member 的接力一律由 lead 安排')
  })

  it('a toggle PUTs the whole relay object with the one field changed', async () => {
    seed(entry({ self_solo: true, self_lead: true }))
    render(<RelaySection hostId={H} />)
    fireEvent.click(screen.getByTestId('relay-self-solo'))
    await waitFor(() => expect(saveRelay).toHaveBeenCalledWith(H, { self_solo: false, self_lead: true }))
    await waitFor(() => expect(screen.getByTestId('relay-self-solo').getAttribute('aria-checked')).toBe('false'))
    fireEvent.click(screen.getByTestId('relay-self-lead'))
    await waitFor(() => expect(saveRelay).toHaveBeenLastCalledWith(H, { self_solo: false, self_lead: false }))
  })

  it('two clicks in one tick serialize: the second saves on top of the first, not over it', async () => {
    seed(entry({ self_solo: true, self_lead: true }))
    render(<RelaySection hostId={H} />)
    fireEvent.click(screen.getByTestId('relay-self-solo'))
    fireEvent.click(screen.getByTestId('relay-self-lead'))
    await waitFor(() => expect(saveRelay).toHaveBeenCalledTimes(2))
    expect(saveRelay.mock.calls[1][1]).toEqual({ self_solo: false, self_lead: false })
  })

  it('a double click on ONE switch in one tick toggles twice: back to where it was (PR #1742 R1)', async () => {
    seed(entry({ self_solo: true, self_lead: true }))
    render(<RelaySection hostId={H} />)
    fireEvent.click(screen.getByTestId('relay-self-solo'))
    fireEvent.click(screen.getByTestId('relay-self-solo')) // same render: both clicks saw checked=true
    await waitFor(() => expect(saveRelay).toHaveBeenCalledTimes(2))
    expect(saveRelay.mock.calls[0][1]).toEqual({ self_solo: false, self_lead: true })
    expect(saveRelay.mock.calls[1][1]).toEqual({ self_solo: true, self_lead: true })
  })

  it('a save failure is shown and the switch keeps the daemon copy', async () => {
    seed(entry({ self_solo: true, self_lead: true }))
    saveRelay.mockRejectedValueOnce(new Error('boom'))
    render(<RelaySection hostId={H} />)
    fireEvent.click(screen.getByTestId('relay-self-lead'))
    await waitFor(() => expect(screen.getByTestId('relay-save-error').textContent).toContain('boom'))
    expect(screen.getByTestId('relay-self-lead').getAttribute('aria-checked')).toBe('true')
  })

  it('an older daemon (no relay field) shows the unsupported note and no switches', () => {
    seed(entry({ self_solo: true, self_lead: true }, false))
    render(<RelaySection hostId={H} />)
    expect(screen.getByTestId('relay-unsupported').textContent).toContain('太舊')
    expect(screen.queryByTestId('relay-self-solo')).toBeNull()
  })

  it('offline: the notice shows and a click saves nothing', async () => {
    seed(entry({ self_solo: true, self_lead: true }))
    useHostStore.setState((s) => ({ runtime: { ...s.runtime, [H]: { status: 'disconnected' } } }))
    render(<RelaySection hostId={H} />)
    expect(screen.getByTestId('host-config-notice').dataset.notice).toBe('host_config.offline')
    fireEvent.click(screen.getByTestId('relay-self-solo'))
    // A save would run from the queue, a microtask later: let it run before asserting it never did.
    await act(async () => { await new Promise((r) => setTimeout(r, 0)) })
    expect(saveRelay).not.toHaveBeenCalled()
    expect(fetchRelayPrompts).not.toHaveBeenCalled()
  })
})

// Spec §8.8 bullet 5, U21; plan v3 P9a-3.
describe('RelaySection prompts', () => {
  const box = (kind: string) => screen.getByTestId(`relay-prompt-${kind}-box`) as HTMLTextAreaElement
  const type = (kind: string, text: string) => fireEvent.change(box(kind), { target: { value: text } })
  const click = (testId: string) => fireEvent.click(screen.getByTestId(testId))

  it('three editors when the route exists, each with the stored body, else the default', async () => {
    seed(entry({ self_solo: true, self_lead: true, prompt_fix: 'my fix' }))
    render(<RelaySection hostId={H} />)
    expect(await screen.findByTestId('relay-prompt-write')).toBeTruthy()
    expect(box('write').value).toBe('default write')
    expect(box('fix').value).toBe('my fix')
    expect(box('seed').value).toBe('default seed')
    expect(fetchRelayPrompts).toHaveBeenCalledTimes(1)
    expect(vi.mocked(fetchRelayPrompts).mock.calls[0][0]).toBe(H)
  })

  it('an older daemon (plain 404): one line, no editors, and the toggles still work', async () => {
    vi.mocked(fetchRelayPrompts).mockResolvedValue('unsupported')
    seed(entry({ self_solo: true, self_lead: true }))
    render(<RelaySection hostId={H} />)
    expect((await screen.findByTestId('relay-prompts-unsupported')).textContent).toContain('太舊')
    expect(screen.queryByTestId('relay-prompt-write')).toBeNull()
    click('relay-self-lead')
    await waitFor(() => expect(saveRelay).toHaveBeenCalledWith(H, { self_solo: true, self_lead: false }))
  })

  it('a load failure is said, and the toggles still work', async () => {
    vi.mocked(fetchRelayPrompts).mockRejectedValue(new Error('storage_error'))
    seed(entry({ self_solo: true, self_lead: true }))
    render(<RelaySection hostId={H} />)
    expect((await screen.findByTestId('relay-prompts-error')).textContent).toContain('storage_error')
    click('relay-self-solo')
    await waitFor(() => expect(saveRelay).toHaveBeenCalledTimes(1))
  })

  it('a save PUTs the whole relay object, both switches and the other bodies included, through the relay queue', async () => {
    seed(entry({ self_solo: false, self_lead: true, prompt_fix: 'my fix', prompt_seed: 'my seed' }))
    render(<RelaySection hostId={H} />)
    await screen.findByTestId('relay-prompt-write')
    type('write', 'my write')
    click('relay-prompt-write-save')
    await waitFor(() => expect(saveRelay).toHaveBeenCalledWith(H,
      { self_solo: false, self_lead: true, prompt_fix: 'my fix', prompt_seed: 'my seed', prompt_write: 'my write' }))
    expect(queueHostConfigSave).toHaveBeenCalledWith(`${H}:relay`, expect.any(Function))
    await waitFor(() => expect(readRelayPromptDraft(`${H}:write`)).toBeUndefined())
    expect(box('write').value).toBe('my write')
    expect(screen.getByTestId('relay-prompt-write-badge').textContent).toBe('自訂')
  })

  it('a toggle and a save in one tick serialize: the save reads the switch the toggle just wrote', async () => {
    seed(entry({ self_solo: true, self_lead: true }))
    render(<RelaySection hostId={H} />)
    await screen.findByTestId('relay-prompt-write')
    type('write', 'my write')
    click('relay-self-solo')
    click('relay-prompt-write-save')
    await waitFor(() => expect(saveRelay).toHaveBeenCalledTimes(2))
    expect(saveRelay.mock.calls[0][1]).toEqual({ self_solo: false, self_lead: true })
    expect(saveRelay.mock.calls[1][1]).toEqual({ self_solo: false, self_lead: true, prompt_write: 'my write' })
  })

  it('a toggle after a save keeps the bodies', async () => {
    seed(entry({ self_solo: true, self_lead: true }))
    render(<RelaySection hostId={H} />)
    await screen.findByTestId('relay-prompt-seed')
    type('seed', 'my seed')
    click('relay-prompt-seed-save')
    await waitFor(() => expect(saveRelay).toHaveBeenCalledTimes(1))
    click('relay-self-lead')
    await waitFor(() => expect(saveRelay).toHaveBeenLastCalledWith(H, { self_solo: true, self_lead: false, prompt_seed: 'my seed' }))
  })

  it('還原預設 PUTs "" for that body only', async () => {
    seed(entry({ self_solo: true, self_lead: false, prompt_write: 'my write', prompt_seed: 'my seed' }))
    render(<RelaySection hostId={H} />)
    await screen.findByTestId('relay-prompt-seed')
    click('relay-prompt-seed-restore')
    await waitFor(() => expect(saveRelay).toHaveBeenCalledWith(H,
      { self_solo: true, self_lead: false, prompt_write: 'my write', prompt_seed: '' }))
    await waitFor(() => expect(box('seed').value).toBe('default seed'))
    expect((screen.getByTestId('relay-prompt-seed-restore') as HTMLButtonElement).disabled).toBe(true)
  })

  it('a 409 keeps the draft and shows the conflict', async () => {
    seed(entry({ self_solo: true, self_lead: true }))
    saveRelay.mockRejectedValueOnce(new HostConfigConflictError({ items: { self_solo: true, self_lead: true }, revision: 5 }))
    render(<RelaySection hostId={H} />)
    await screen.findByTestId('relay-prompt-fix')
    type('fix', 'my fix')
    click('relay-prompt-fix-save')
    await waitFor(() => expect(screen.getByTestId('relay-prompt-fix-error').textContent).toBe(useI18nStore.getState().t('host_config.conflict')))
    expect(box('fix').value).toBe('my fix')
    expect(readRelayPromptDraft(`${H}:fix`)).toBe('my fix')
  })

  it("a daemon 400's detail is shown", async () => {
    seed(entry({ self_solo: true, self_lead: true }))
    saveRelay.mockRejectedValueOnce(new HostConfigApiError(400, 'prompt_write: a relay prompt may hold no control character but newline and tab (found U+200E)'))
    render(<RelaySection hostId={H} />)
    await screen.findByTestId('relay-prompt-write')
    type('write', 'my write')
    click('relay-prompt-write-save')
    await waitFor(() => expect(screen.getByTestId('relay-prompt-write-error').textContent).toContain('found U+200E'))
    expect(box('write').value).toBe('my write')
  })
})
