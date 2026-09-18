import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent, waitFor } from '@testing-library/react'

vi.mock('../../lib/nex/nex-api', async (importOriginal) => ({
  ...(await importOriginal<typeof import('../../lib/nex/nex-api')>()),
  delegateExecution: vi.fn(),
}))

import { HeadlessLauncher } from './HeadlessLauncher'
import { delegateExecution } from '../../lib/nex/nex-api'
import { NexApiError, type NexCapabilities } from '../../lib/nex/types'
import { useNexHostStore, type NexHostEntry } from '../../stores/useNexHostStore'
import { useHeadlessLauncherMemoryStore } from '../../stores/useHeadlessLauncherMemoryStore'
import { useHostStore } from '../../stores/useHostStore'
import type { NexInfo } from '../../lib/host-api'

const H = 'h1'
const delegate = vi.mocked(delegateExecution)
const onSelect = vi.fn()
const ensureSpy = vi.fn(async () => {})
const invalidateSpy = vi.fn(async () => {})

const READY_INFO: NexInfo = { configured: true, mounted: true, ready: true, init_error: '', effective: null } as never

function caps(over: Partial<NexCapabilities> = {}): NexCapabilities {
  return {
    phase: 'ga', host_id: 'mlab', verbs: [], providers: ['claude'], events: [], provider_events: [], transient_events: [],
    sandbox_profiles: ['strict', 'standard', 'handoff'],
    sandbox_default_profile: 'standard',
    sandbox_max_profile: 'handoff',
    roots: [{ path: '/srv/dev', kind: 'dev' }, { path: '/srv/svc', kind: 'service' }],
    lease: { ttl_seconds: 60, scope: 'x', renew: { method: 'POST', path: '' }, release: { method: 'DELETE', path: '' } },
    send: { delivery: [], max_text_bytes: 1 },
    brief: { max_bytes: 32 },
    ...over,
  }
}

function seedEntry(entry: Partial<NexHostEntry>) {
  useNexHostStore.setState({
    byHost: { [H]: { info: null, capabilities: null, phase: 'unknown', error: null, fetchedAt: 1, generation: 1, fingerprint: '', ...entry } },
    ensure: ensureSpy,
    invalidate: invalidateSpy,
  })
}

function seedReady(c: NexCapabilities = caps()) {
  seedEntry({ info: READY_INFO, capabilities: c, phase: 'ready' })
}

function seedHost(runtime: Partial<{ status: string; tmuxState: string }> = {}) {
  useHostStore.setState({
    hosts: { [H]: { id: H, name: 'Mini', ip: '127.0.0.1', port: 7860, order: 0 } },
    runtime: { [H]: { status: 'connected', tmuxState: 'ok', ...runtime } as never },
  })
}

const brief = () => screen.getByTestId('headless-brief') as HTMLTextAreaElement
const root = () => screen.getByTestId('headless-root') as HTMLSelectElement
const subpath = () => screen.getByTestId('headless-subpath') as HTMLInputElement
const profile = () => screen.getByTestId('headless-profile') as HTMLSelectElement
const submit = () => screen.getByTestId('headless-submit') as HTMLButtonElement

function typeBrief(text: string) {
  fireEvent.change(brief(), { target: { value: text } })
}

function renderLauncher() {
  return render(<HeadlessLauncher hostId={H} onSelect={onSelect} />)
}

beforeEach(() => {
  onSelect.mockReset()
  ensureSpy.mockClear()
  invalidateSpy.mockClear()
  delegate.mockReset().mockResolvedValue({ id: 'ex-1', state: 'queued' })
  useHeadlessLauncherMemoryStore.setState({ byHost: {} })
  useNexHostStore.setState({ byHost: {} })
  seedHost()
})

describe('HeadlessLauncher phases', () => {
  it('calls ensure(hostId) on mount', () => {
    seedReady()
    renderLauncher()
    expect(ensureSpy).toHaveBeenCalledWith(H)
  })

  it('renders a skeleton while loading (and when the store has no entry yet)', () => {
    seedEntry({ phase: 'loading' })
    const { unmount } = renderLauncher()
    expect(screen.getByTestId('headless-loading')).toBeInTheDocument()
    unmount()
    useNexHostStore.setState({ byHost: {} })
    renderLauncher()
    expect(screen.getByTestId('headless-loading')).toBeInTheDocument()
  })

  it('renders the disabled line with the Hosts → Nex hint when Nexen is not enabled', () => {
    seedEntry({ info: { ...READY_INFO, configured: false }, phase: 'disabled' })
    renderLauncher()
    expect(screen.getByTestId('headless-disabled')).toHaveTextContent('Hosts → Nex')
    expect(screen.queryByTestId('headless-brief')).toBeNull()
  })

  it('renders the unavailable line with the error text', () => {
    seedEntry({ info: READY_INFO, phase: 'unavailable', error: 'capabilities: 503' })
    renderLauncher()
    expect(screen.getByTestId('headless-unavailable')).toHaveTextContent('capabilities: 503')
  })
})

describe('HeadlessLauncher ready form', () => {
  beforeEach(() => seedReady())

  it('renders every root with its kind badge and preselects the default profile', () => {
    renderLauncher()
    const options = Array.from(root().options).map((o) => o.value)
    expect(options).toEqual(['/srv/dev', '/srv/svc'])
    expect(root().value).toBe('/srv/dev')
    expect(screen.getByTestId('headless-root-kind')).toHaveTextContent('dev')
    expect(Array.from(profile().options).map((o) => o.value)).toEqual(['strict', 'standard', 'handoff'])
    expect(profile().value).toBe('standard')
    expect(screen.getByTestId('headless-max-profile')).toHaveTextContent('handoff')
  })

  it('counts brief bytes in UTF-8 against max_bytes and blocks submit above the limit', () => {
    renderLauncher()
    expect(screen.getByTestId('headless-bytes')).toHaveTextContent('0 / 32 bytes')
    typeBrief('中文')
    expect(screen.getByTestId('headless-bytes')).toHaveTextContent('6 / 32 bytes')
    expect(submit()).not.toBeDisabled()
    typeBrief('中'.repeat(11))
    expect(screen.getByTestId('headless-bytes')).toHaveTextContent('33 / 32 bytes')
    expect(submit()).toBeDisabled()
    fireEvent.click(submit())
    expect(delegate).not.toHaveBeenCalled()
  })

  it('falls back to 65536 bytes when capabilities carry no brief.max_bytes', () => {
    seedReady(caps({ brief: undefined }))
    renderLauncher()
    expect(screen.getByTestId('headless-bytes')).toHaveTextContent('0 / 65536 bytes')
  })

  it('blocks submit while the brief is empty', () => {
    renderLauncher()
    expect(submit()).toBeDisabled()
    typeBrief('   ')
    expect(submit()).toBeDisabled()
    typeBrief('go')
    expect(submit()).not.toBeDisabled()
  })

  it.each([
    ['/abs', 'absolute'],
    ['~/x', 'tilde'],
    ['a/../b', 'dotdot'],
    ['./a', 'dot_segment'],
    ['a//b', 'empty_segment'],
    ['a\\b', 'backslash'],
    [' a', 'whitespace'],
  ])('sub-path %s blocks submit with the %s message', (value, reason) => {
    renderLauncher()
    typeBrief('go')
    fireEvent.change(subpath(), { target: { value } })
    const err = screen.getByTestId('headless-subpath-error')
    expect(err).toBeInTheDocument()
    expect(err.textContent).not.toBe('')
    expect(err.textContent).not.toContain(`subpath_error.${reason}`)
    expect(err).toHaveAttribute('data-reason', reason)
    expect(submit()).toBeDisabled()
  })

  it('disables the form with no_roots when the host allows no roots', () => {
    seedReady(caps({ roots: [] }))
    renderLauncher()
    expect(screen.getByTestId('headless-no-roots')).toBeInTheDocument()
    expect(root()).toBeDisabled()
    typeBrief('go')
    expect(submit()).toBeDisabled()
  })

  it('submits the joined cwd, purdex labels, newtab origin and the chosen profile', async () => {
    renderLauncher()
    typeBrief('fix the build')
    fireEvent.change(root(), { target: { value: '/srv/svc' } })
    fireEvent.change(subpath(), { target: { value: 'api/v2/' } })
    fireEvent.change(profile(), { target: { value: 'strict' } })
    fireEvent.click(submit())
    await waitFor(() => expect(delegate).toHaveBeenCalledTimes(1))
    expect(delegate).toHaveBeenCalledWith(H, {
      brief: 'fix the build',
      cwd: '/srv/svc/api/v2',
      profile: 'strict',
      labels: { source: 'purdex' },
      origin: `purdex://host/${H}/newtab`,
    }, caps())
  })

  it('shows the reject_reason inline on state=rejected and does not select', async () => {
    delegate.mockResolvedValue({ id: 'ex-2', state: 'rejected', reject_reason: 'cwd_outside_roots' })
    renderLauncher()
    typeBrief('go')
    fireEvent.click(submit())
    await waitFor(() => expect(screen.getByTestId('headless-error')).toHaveTextContent('cwd_outside_roots'))
    expect(onSelect).not.toHaveBeenCalled()
    expect(invalidateSpy).not.toHaveBeenCalled()
  })

  it('shows the 400 code as a refused request', async () => {
    delegate.mockRejectedValue(new NexApiError(400, 'invalid_brief', 'bad'))
    renderLauncher()
    typeBrief('go')
    fireEvent.click(submit())
    await waitFor(() => expect(screen.getByTestId('headless-error')).toHaveTextContent('invalid_brief'))
    expect(onSelect).not.toHaveBeenCalled()
    expect(invalidateSpy).not.toHaveBeenCalled()
  })

  it('shows unavailable and invalidates the host on 503 nex_unavailable', async () => {
    delegate.mockRejectedValue(new NexApiError(503, 'nex_unavailable', 'engine down'))
    renderLauncher()
    typeBrief('go')
    fireEvent.click(submit())
    await waitFor(() => expect(screen.getByTestId('headless-error')).toHaveTextContent('engine down'))
    expect(invalidateSpy).toHaveBeenCalledWith(H)
    expect(onSelect).not.toHaveBeenCalled()
  })

  it('shows unavailable and invalidates the host on a network failure', async () => {
    delegate.mockRejectedValue(new NexApiError(0, 'network', 'Failed to fetch'))
    renderLauncher()
    typeBrief('go')
    fireEvent.click(submit())
    await waitFor(() => expect(screen.getByTestId('headless-error')).toHaveTextContent('Failed to fetch'))
    expect(invalidateSpy).toHaveBeenCalledWith(H)
  })

  it('selects the execution pane content on success', async () => {
    delegate.mockResolvedValue({ id: 'ex-9', state: 'queued' })
    renderLauncher()
    typeBrief('go')
    fireEvent.click(submit())
    await waitFor(() => expect(onSelect).toHaveBeenCalledWith({ kind: 'execution', executionId: 'ex-9', host: H }))
    expect(screen.queryByTestId('headless-error')).toBeNull()
  })

  it('remembers the last-used root and profile per host and restores them on remount', async () => {
    const { unmount } = renderLauncher()
    typeBrief('go')
    fireEvent.change(root(), { target: { value: '/srv/svc' } })
    fireEvent.change(profile(), { target: { value: 'handoff' } })
    fireEvent.click(submit())
    await waitFor(() => expect(onSelect).toHaveBeenCalled())
    expect(useHeadlessLauncherMemoryStore.getState().byHost[H]).toEqual({ root: '/srv/svc', profile: 'handoff' })
    unmount()
    renderLauncher()
    expect(root().value).toBe('/srv/svc')
    expect(profile().value).toBe('handoff')
  })

  it('ignores a remembered root or profile the host no longer offers', () => {
    useHeadlessLauncherMemoryStore.setState({ byHost: { [H]: { root: '/gone', profile: 'nope' } } })
    renderLauncher()
    expect(root().value).toBe('/srv/dev')
    expect(profile().value).toBe('standard')
  })

  it('refuses to submit when the daemon is not connected and says so', async () => {
    renderLauncher()
    typeBrief('go')
    seedHost({ status: 'disconnected' })
    fireEvent.click(submit())
    await waitFor(() => expect(screen.getByTestId('headless-error')).toHaveTextContent('offline'))
    expect(delegate).not.toHaveBeenCalled()
    expect(onSelect).not.toHaveBeenCalled()
  })

  it('submits when the daemon is connected even though tmux is unavailable (headless needs no tmux)', async () => {
    renderLauncher()
    typeBrief('go')
    seedHost({ status: 'connected', tmuxState: 'unavailable' })
    fireEvent.click(submit())
    await waitFor(() => expect(delegate).toHaveBeenCalledTimes(1))
    expect(screen.queryByTestId('headless-error')).toBeNull()
    await waitFor(() => expect(onSelect).toHaveBeenCalledTimes(1))
  })

  it('ignores a second click while a delegate is in flight', async () => {
    let resolve!: (v: { id: string; state: string }) => void
    delegate.mockReturnValue(new Promise((r) => { resolve = r }))
    renderLauncher()
    typeBrief('go')
    fireEvent.click(submit())
    expect(submit()).toBeDisabled()
    fireEvent.click(submit())
    resolve({ id: 'ex-1', state: 'queued' })
    await waitFor(() => expect(onSelect).toHaveBeenCalledTimes(1))
    expect(delegate).toHaveBeenCalledTimes(1)
  })
})
