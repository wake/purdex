// spa/src/components/settings/profile/wizard/ProfileWizard.test.tsx — the wizard's steps, one at a time. The
// stores are the real ones; `start`, `api`, `switch-active`, the client identity and `readMasterWorld` are
// replaced. Which world a pull keeps a copy of is pinned against the REAL switch-active.ts in wizard-run.test.ts.
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { act, cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import en from '../../../../locales/en.json'
import { ProfileWizard } from './ProfileWizard'
import { ATTACH_REASONS } from './wizard-run'
import { reasonKey, requestKey } from './wizard-shared'
import { useProfileStore } from '../../../../stores/useProfileStore'
import { useHostStore } from '../../../../stores/useHostStore'
import { useDeviceNameStore } from '../../../../stores/useDeviceNameStore'
import { useLocalProfilesStore } from '../../../../stores/useLocalProfilesStore'
import { useUndoToast } from '../../../../stores/useUndoToast'
import { attachMaster, detachMaster } from '../../../../lib/profile/start'
import { createProfile, getSection, listProfiles } from '../../../../lib/profile/api'
import type { ProfileIndexEntry } from '../../../../lib/profile/api'
import { copyMasterAsSlave, promoteToMaster, saveScreenAsSlave } from '../../../../lib/profile/switch-active'
import { isClientIdPersisted } from '../../../../lib/client-identity'
import { readMasterWorld } from '../../../../lib/profile/master-world'
import type { MasterWorldRead } from '../../../../lib/profile/master-world'

vi.mock('../../../../lib/profile/start', () => ({ attachMaster: vi.fn(), detachMaster: vi.fn() }))
vi.mock('../../../../lib/profile/api', () => ({ listProfiles: vi.fn(), createProfile: vi.fn(), getSection: vi.fn() }))
vi.mock('../../../../lib/profile/switch-active', () => ({ promoteToMaster: vi.fn(), copyMasterAsSlave: vi.fn(), saveScreenAsSlave: vi.fn() }))
vi.mock('../../../../lib/client-identity', () => ({ isClientIdPersisted: vi.fn(), getClientId: () => 'c_aaaaaaaaaaaa' }))
vi.mock('../../../../lib/profile/master-world', async (importOriginal) => ({
  ...(await importOriginal<typeof import('../../../../lib/profile/master-world')>()),
  readMasterWorld: vi.fn(),
}))

const tabs = (n: number) => Object.fromEntries(Array.from({ length: n }, (_, i) => [`t${i}`, { id: `t${i}` }])) as never
const SETTLED: MasterWorldRead = { settled: true, onScreen: true, world: { workspaces: [{ id: 'w1', name: 'One', tabs: [], activeTabId: null }, { id: 'w2', name: 'Two', tabs: [], activeTabId: null }], tabs: tabs(3), activeWorkspaceId: 'w1', activeTabId: null } }
const entry = (id: string, name: string, sections = 1): ProfileIndexEntry => ({ id, name, createdAt: 1, updatedAt: 1, attachments: [], sections: Array.from({ length: sections }, (_, i) => ({ section: `s${i}`, rev: 1, hash: 'h', fingerprint: 'f', ordinal: 1, writer: 'c', updatedAt: 1 })) })
const P1 = 'p_000000000001'
const P2 = 'p_000000000002'
const failed = (reason: string, status = 0) => ({ kind: 'failed', reason, status, message: `RAW-${reason} http://10.0.0.1/?token=SECRET` }) as never
const host = (id: string, name: string, ip: string) => ({ id, name, ip, port: 7860, order: 0 })
const MLAB = 'mlab:278cbm'
const H1_VERIFIED = { status: 'connected' as const, daemonIdVerified: { endpoint: '10.0.0.1:7860', daemonId: MLAB } }

const calls: string[] = []
const onClose = vi.fn()
const open = () => render(<ProfileWizard onClose={onClose} />)
const wizard = () => screen.getByTestId('profile-wizard')
const step = () => wizard().getAttribute('data-step')
const click = (id: string) => fireEvent.click(screen.getByTestId(id))
const next = () => click('profile-wizard-next')
const flush = async () => {
  await act(async () => {
    for (let i = 0; i < 10; i++) await Promise.resolve()
  })
}
const attachedStore = () => useProfileStore.setState({ masterHostId: 'h1', masterProfileId: P1, masterEndpoint: '10.0.0.1:7860' })

/** Step 2 done with the existing profile `P1`: now on step 3. */
async function toLocal(profile = P1): Promise<void> {
  open()
  await flush()
  click(`profile-wizard-profile-${profile}`)
  next()
  await flush()
  expect(step()).toBe('local')
}
async function toDirection(localId = 'master', profile = P1): Promise<void> {
  await toLocal(profile)
  click(`profile-wizard-local-${localId}`)
  next()
  expect(step()).toBe('direction')
}
/** Pull chosen, and the check of the profile's hosts it asks for (`previewPull`) answered. */
async function choosePull(): Promise<void> {
  click('profile-wizard-direction-pull')
  await flush()
}
async function toRun(direction: 'push' | 'pull', localId = 'master'): Promise<void> {
  await toDirection(localId)
  if (direction === 'pull') await choosePull()
  else click('profile-wizard-direction-push')
  next()
  expect(step()).toBe('run')
}

beforeEach(() => {
  calls.length = 0
  onClose.mockReset()
  vi.mocked(isClientIdPersisted).mockReset().mockReturnValue(true)
  vi.mocked(readMasterWorld).mockReset().mockReturnValue(SETTLED)
  vi.mocked(listProfiles).mockReset().mockResolvedValue({ kind: 'ok', value: [entry(P1, 'default'), entry(P2, 'empty one', 0)] })
  vi.mocked(createProfile).mockReset().mockResolvedValue({ kind: 'ok', value: { id: 'p_00000000000c', name: 'x', createdAt: 1, updatedAt: 1 } })
  vi.mocked(detachMaster).mockReset().mockImplementation(async () => {
    calls.push('detach')
    useProfileStore.setState({ masterHostId: null, masterProfileId: null, masterEndpoint: null })
    return { ok: true }
  })
  vi.mocked(promoteToMaster).mockReset().mockImplementation(async () => (calls.push('promote'), { ok: true, demotedId: 'd1' }))
  vi.mocked(copyMasterAsSlave).mockReset().mockImplementation(() => (calls.push('copy-master'), { ok: true, id: 'c1' }))
  vi.mocked(saveScreenAsSlave).mockReset().mockImplementation(() => (calls.push('save-screen'), { ok: true, id: 'c2' }))
  vi.mocked(attachMaster).mockReset().mockImplementation(async () => (calls.push('attach'), { ok: true }))
  useProfileStore.setState({ masterHostId: null, masterProfileId: null, masterEndpoint: null, pendingDirection: null, suspension: null, pendingDetaches: [] })
  // h1 knows its daemon and this session confirmed it there: a pull from it is possible (host-sync-identity §8)
  useHostStore.setState({ hosts: { h1: { ...host('h1', 'mlab', '10.0.0.1'), daemonId: MLAB }, h2: host('h2', 'air', '10.0.0.2'), h3: host('h3', 'gone', '10.0.0.3') }, hostOrder: ['h1', 'h2', 'h3'], devHostId: 'h1', runtime: { h1: H1_VERIFIED, h2: { status: 'connected' }, h3: { status: 'disconnected' } } })
  // the profiles listed hold no `hosts` section: a pull removes no host
  vi.mocked(getSection).mockReset().mockResolvedValue({ kind: 'ok', value: null })
  useDeviceNameStore.setState({ deviceName: 'Laptop' })
  useLocalProfilesStore.setState({ slaves: { s1: { id: 's1', name: 'Scratch', createdAt: 1, world: { workspaces: [], tabs: tabs(7), activeWorkspaceId: null, activeTabId: null } } }, slaveOrder: ['s1'], activeProfileId: 'master', parkedMaster: null, worldEpoch: 0, relabelCount: 0, master: { name: null } })
})

afterEach(() => cleanup())

describe('it refuses to start', () => {
  it('a client id that would not survive a reload: no step, the reason, and nothing was asked of anybody', async () => {
    vi.mocked(isClientIdPersisted).mockReturnValue(false)
    open()
    await flush()
    expect(step()).toBe('refused')
    expect(screen.getByTestId('profile-wizard-refused')).toHaveAttribute('data-reason', 'client-id')
    expect(screen.getByTestId('profile-wizard-refused')).toHaveTextContent(en['settings.profile.wizard.refused.client_id'])
    expect(screen.queryByTestId('profile-wizard-next')).toBeNull()
    expect(screen.queryByTestId('profile-wizard-stop-confirm')).toBeNull()
    expect(listProfiles).not.toHaveBeenCalled()
    click('profile-wizard-close')
    expect(onClose).toHaveBeenCalled()
  })

  it.each(['junk-epoch', 'no-parked-master'] as const)('a master world that is %s: refused, with the way out in words', async (reason) => {
    vi.mocked(readMasterWorld).mockReturnValue({ settled: false, reason })
    open()
    await flush()
    expect(step()).toBe('refused')
    expect(screen.getByTestId('profile-wizard-refused')).toHaveAttribute('data-reason', reason)
    expect(screen.getByTestId('profile-wizard-refused')).toHaveTextContent(en[`settings.profile.wizard.refused.${reason.replace(/-/g, '_')}` as keyof typeof en])
  })

  it('a world that is merely catching up is no refusal', async () => {
    vi.mocked(readMasterWorld).mockReturnValue({ settled: false, reason: 'behind-fence' })
    open()
    await flush()
    expect(step()).toBe('sot')
  })
})

describe('step 1 — stop the sync (only with a master attached)', () => {
  it('no master: the wizard starts at step 2, and the step list has no "stop"', async () => {
    open()
    await flush()
    expect(step()).toBe('sot')
    expect(screen.queryByTestId('profile-wizard-step-stop')).toBeNull()
  })

  it('attached: it starts at "stop", nothing is stopped until the user says so, and there is no way past it', async () => {
    attachedStore()
    open()
    await flush()
    expect(step()).toBe('stop')
    expect(detachMaster).not.toHaveBeenCalled()
    expect(screen.queryByTestId('profile-wizard-next')).toBeNull()
    expect(screen.queryByTestId('profile-wizard-host')).toBeNull()
    // the step list is not a set of buttons
    expect(screen.getByTestId('profile-wizard-step-sot').tagName).toBe('LI')
    fireEvent.click(screen.getByTestId('profile-wizard-step-sot'))
    expect(step()).toBe('stop')
  })

  it('confirmed and the host told: on to step 2', async () => {
    attachedStore()
    open()
    click('profile-wizard-stop-confirm')
    await flush()
    expect(detachMaster).toHaveBeenCalledTimes(1)
    expect(step()).toBe('sot')
  })

  it('the host was NOT told: the wizard says so and waits for the user — it neither hides it nor goes on by itself', async () => {
    attachedStore()
    vi.mocked(detachMaster).mockImplementation(async () => {
      useProfileStore.setState({ masterHostId: null, masterProfileId: null, masterEndpoint: null })
      return { ok: false, reason: 'daemon-not-told', detail: 'timeout' }
    })
    open()
    click('profile-wizard-stop-confirm')
    await flush()
    expect(step()).toBe('stop')
    expect(screen.getByTestId('profile-wizard-stop-not-told')).toHaveAttribute('data-reason', 'daemon-not-told')
    expect(screen.getByTestId('profile-wizard-stop-not-told')).toHaveTextContent(en['settings.profile.wizard.stop.not_told'])
    click('profile-wizard-stop-continue')
    expect(step()).toBe('sot')
  })

  it('the sync was stopped in another window meanwhile: on to step 2, and it says why', async () => {
    attachedStore()
    open()
    await flush()
    act(() => useProfileStore.setState({ masterHostId: null, masterProfileId: null, masterEndpoint: null }))
    expect(step()).toBe('sot')
    expect(screen.getByTestId('profile-wizard-notice')).toHaveAttribute('data-reason', 'stopped-elsewhere')
  })
})

describe('step 2 — the host and the profile on it', () => {
  it('the dev host is chosen; a host that is not connected is listed, disabled, with the reason', async () => {
    open()
    await flush()
    expect((screen.getByTestId('profile-wizard-host') as HTMLSelectElement).value).toBe('h1')
    const offline = screen.getByTestId('profile-wizard-host-option-h3') as HTMLOptionElement
    expect(offline.disabled).toBe(true)
    expect(offline).toHaveTextContent(en['settings.profile.wizard.sot.host_offline'].replace('{{name}}', 'gone'))
    expect((screen.getByTestId('profile-wizard-host-option-h2') as HTMLOptionElement).disabled).toBe(false)
    expect(listProfiles).toHaveBeenCalledWith('h1', { expectEndpoint: '10.0.0.1:7860' })
  })

  it('the dev host is offline: the first connected host stands in; none connected: nothing to choose, and it says so', async () => {
    useHostStore.setState({ devHostId: 'h3' })
    open()
    await flush()
    expect((screen.getByTestId('profile-wizard-host') as HTMLSelectElement).value).toBe('h1')
    cleanup()
    useHostStore.setState({ runtime: {} })
    open()
    await flush()
    expect(screen.getByTestId('profile-wizard-host-none')).toBeInTheDocument()
    expect(screen.getByTestId('profile-wizard-next')).toBeDisabled()
  })

  it('loading → rows; another host → its own list', async () => {
    let release: (v: never) => void = () => {}
    vi.mocked(listProfiles).mockReturnValueOnce(new Promise((r) => (release = r)))
    open()
    expect(screen.getByTestId('profile-wizard-profiles')).toHaveAttribute('data-state', 'loading')
    expect(screen.getByTestId('profile-wizard-next')).toBeDisabled()
    await act(async () => release({ kind: 'ok', value: [entry(P1, 'default')] } as never))
    expect(screen.getByTestId('profile-wizard-profiles')).toHaveAttribute('data-state', 'rows')
    fireEvent.change(screen.getByTestId('profile-wizard-host'), { target: { value: 'h2' } })
    await flush()
    expect(listProfiles).toHaveBeenLastCalledWith('h2', { expectEndpoint: '10.0.0.2:7860' })
  })

  it('the list cannot be read: a sentence for the failure\'s class — never the transport\'s message — and a retry', async () => {
    vi.mocked(listProfiles).mockResolvedValueOnce(failed('unauthorized', 401))
    open()
    await flush()
    expect(screen.getByTestId('profile-wizard-profiles')).toHaveAttribute('data-state', 'error')
    expect(screen.getByTestId('profile-wizard-profiles-error')).toHaveAttribute('data-reason', 'unauthorized')
    expect(screen.getByTestId('profile-wizard-profiles-error')).toHaveTextContent(en['settings.profile.wizard.request.unauthorized'])
    expect(wizard().textContent).not.toMatch(/RAW-|SECRET/)
    click('profile-wizard-profiles-retry')
    await flush()
    expect(screen.getByTestId('profile-wizard-profiles')).toHaveAttribute('data-state', 'rows')
  })

  it('no profile on the host: it says so, and a new one is still offered', async () => {
    vi.mocked(listProfiles).mockResolvedValue({ kind: 'ok', value: [] })
    open()
    await flush()
    expect(screen.getByTestId('profile-wizard-profiles')).toHaveAttribute('data-state', 'empty')
    expect(screen.getByTestId('profile-wizard-profile-new')).toBeInTheDocument()
  })

  it('nothing chosen: no way on', async () => {
    open()
    await flush()
    expect(screen.getByTestId('profile-wizard-next')).toBeDisabled()
    next()
    expect(step()).toBe('sot')
  })

  it('the step list is no way on either: a later step\'s name is text, not a door', async () => {
    open()
    await flush()
    for (const id of ['local', 'direction', 'run']) {
      fireEvent.click(screen.getByTestId(`profile-wizard-step-${id}`))
      expect(step()).toBe('sot')
      expect(screen.getByTestId(`profile-wizard-step-${id}`)).toHaveAttribute('data-state', 'todo')
    }
    expect(screen.getByTestId('profile-wizard-host')).toBeInTheDocument()
  })

  it('a new profile: named after this device by default, a name is required, and it is created only on Next', async () => {
    open()
    await flush()
    click('profile-wizard-profile-new')
    const name = screen.getByTestId('profile-wizard-new-name') as HTMLInputElement
    expect(name.value).toBe('Laptop')
    fireEvent.change(name, { target: { value: '   ' } })
    expect(screen.getByTestId('profile-wizard-next')).toBeDisabled()
    expect(createProfile).not.toHaveBeenCalled()
    fireEvent.change(name, { target: { value: ' Work ' } })
    next()
    await flush()
    expect(createProfile).toHaveBeenCalledWith('h1', 'Work')
    expect(step()).toBe('local')
  })

  it.each(['network', 'timeout', 'unauthorized', 'server', 'rejected', 'too-large', 'contended', 'malformed', 'unknown-host', 'not-found', 'aborted'])('creating fails (%s): its own sentence, the step stays, nothing of the raw message', async (reason) => {
    vi.mocked(createProfile).mockResolvedValueOnce(failed(reason, 500))
    open()
    await flush()
    click('profile-wizard-profile-new')
    next()
    await flush()
    expect(step()).toBe('sot')
    expect(screen.getByTestId('profile-wizard-create-error')).toHaveAttribute('data-reason', reason)
    expect(screen.getByTestId('profile-wizard-create-error')).toHaveTextContent(en[`settings.profile.wizard.request.${reason.replace(/-/g, '_')}` as keyof typeof en])
    expect(wizard().textContent).not.toMatch(/RAW-|SECRET/)
  })

  it('creating throws: a sentence, not the error', async () => {
    vi.mocked(createProfile).mockRejectedValueOnce(new Error('boom SECRET'))
    open()
    await flush()
    click('profile-wizard-profile-new')
    next()
    await flush()
    expect(screen.getByTestId('profile-wizard-create-error')).toHaveAttribute('data-reason', 'thrown')
    expect(wizard().textContent).not.toMatch(/SECRET/)
  })
})

describe('step 3 — which local profile becomes the master', () => {
  it('the master and every local profile; the master is chosen; the one on screen is marked', async () => {
    await toLocal()
    expect((screen.getByTestId('profile-wizard-local-master') as HTMLInputElement).checked).toBe(true)
    expect(screen.getByTestId('profile-wizard-local-s1')).toBeInTheDocument()
    expect(screen.getByTestId('profile-wizard-local-on-screen-master')).toBeInTheDocument()
    expect(screen.queryByTestId('profile-wizard-local-on-screen-s1')).toBeNull()
  })

  it('a local profile chosen: the consequence is said in plain words — a move, the old master kept as a local profile', async () => {
    await toLocal()
    expect(screen.getByTestId('profile-wizard-local-consequence')).toHaveTextContent(en['settings.profile.wizard.local.keep'])
    click('profile-wizard-local-s1')
    expect(screen.getByTestId('profile-wizard-local-consequence')).toHaveTextContent(en['settings.profile.wizard.local.move'].replace('{{name}}', 'Scratch').replace('{{master}}', 'Home'))
    expect(promoteToMaster).not.toHaveBeenCalled() // said, not done: that is step 5
  })

  it('no local profile: the step is still there, with one choice, and Next works', async () => {
    useLocalProfilesStore.setState({ slaves: {}, slaveOrder: [] })
    await toLocal()
    expect(screen.getAllByRole('radio')).toHaveLength(1)
    next()
    expect(step()).toBe('direction')
  })

  it('the world is catching up with another window: said, and no way on until it has', async () => {
    await toLocal()
    vi.mocked(readMasterWorld).mockReturnValue({ settled: false, reason: 'epoch-mismatch' })
    act(() => useLocalProfilesStore.setState({ relabelCount: 1 }))
    expect(screen.getByTestId('profile-wizard-local-world')).toHaveAttribute('data-reason', 'epoch-mismatch')
    expect(screen.getByTestId('profile-wizard-next')).toBeDisabled()
  })

  it('Back goes to step 2 and keeps what was chosen there', async () => {
    await toLocal()
    click('profile-wizard-back')
    expect(step()).toBe('sot')
    expect((screen.getByTestId(`profile-wizard-profile-${P1}`) as HTMLInputElement).checked).toBe(true)
  })
})

describe('step 4 — the direction', () => {
  it('an existing profile: nothing is chosen for the user, and there is no way on without a choice', async () => {
    await toDirection()
    expect((screen.getByTestId('profile-wizard-direction-push') as HTMLInputElement).checked).toBe(false)
    expect((screen.getByTestId('profile-wizard-direction-pull') as HTMLInputElement).checked).toBe(false)
    expect(screen.getByTestId('profile-wizard-next')).toBeDisabled()
  })

  it('push onto an existing profile: it says the SOT\'s content is replaced and the other devices receive it', async () => {
    await toDirection()
    click('profile-wizard-direction-push')
    expect(screen.getByTestId('profile-wizard-push-warning')).toHaveTextContent(en['settings.profile.wizard.direction.push_replaces'].replace('{{profile}}', 'default'))
  })

  it('A NEW PROFILE OFFERS PUSH ONLY', async () => {
    open()
    await flush()
    click('profile-wizard-profile-new')
    next()
    await flush()
    next()
    expect(step()).toBe('direction')
    expect(screen.getByTestId('profile-wizard-direction-pull')).toBeDisabled()
    expect(screen.getByTestId('profile-wizard-pull-unavailable')).toHaveTextContent(en['settings.profile.wizard.direction.pull_new'])
    fireEvent.click(screen.getByTestId('profile-wizard-direction-pull'))
    expect((screen.getByTestId('profile-wizard-direction-pull') as HTMLInputElement).checked).toBe(false)
    expect((screen.getByTestId('profile-wizard-direction-push') as HTMLInputElement).checked).toBe(true)
    expect(screen.queryByTestId('profile-wizard-push-warning')).toBeNull() // nothing there to replace
  })

  it('an existing profile that holds nothing offers push only, too: there is nothing to pull', async () => {
    await toDirection('master', P2)
    expect(screen.getByTestId('profile-wizard-direction-pull')).toBeDisabled()
    expect(screen.getByTestId('profile-wizard-pull-unavailable')).toHaveTextContent(en['settings.profile.wizard.direction.pull_empty'])
  })

  it('pull: what is replaced, in counts; keeping a copy is TICKED by default, named after this device', async () => {
    await toDirection()
    click('profile-wizard-direction-pull')
    expect(screen.getByTestId('profile-wizard-pull-replaces')).toHaveTextContent(en['settings.profile.wizard.direction.pull_replaces'].replace('{{workspaces}}', '2').replace('{{tabs}}', '3'))
    expect((screen.getByTestId('profile-wizard-save-first') as HTMLInputElement).checked).toBe(true)
    expect((screen.getByTestId('profile-wizard-save-name') as HTMLInputElement).value).toBe('Laptop')
  })

  it('pull with a local profile chosen: the counts are THAT profile\'s world', async () => {
    await toDirection('s1')
    click('profile-wizard-direction-pull')
    expect(screen.getByTestId('profile-wizard-pull-replaces')).toHaveTextContent(en['settings.profile.wizard.direction.pull_replaces'].replace('{{workspaces}}', '0').replace('{{tabs}}', '7'))
  })

  it('the copy\'s default name never repeats a name in use', async () => {
    useLocalProfilesStore.setState({ slaves: { s1: { id: 's1', name: 'Laptop', createdAt: 1, world: { workspaces: [], tabs: {}, activeWorkspaceId: null, activeTabId: null } } }, master: { name: 'Laptop 2' } })
    await toDirection()
    click('profile-wizard-direction-pull')
    expect((screen.getByTestId('profile-wizard-save-name') as HTMLInputElement).value).toBe('Laptop 3')
  })

  it('a copy needs a name; unticked, it says that nothing is kept — and lets the user go on', async () => {
    await toDirection()
    await choosePull()
    fireEvent.change(screen.getByTestId('profile-wizard-save-name'), { target: { value: ' ' } })
    expect(screen.getByTestId('profile-wizard-next')).toBeDisabled()
    click('profile-wizard-save-first')
    expect(screen.getByTestId('profile-wizard-no-copy-warning')).toBeInTheDocument()
    expect(screen.getByTestId('profile-wizard-next')).not.toBeDisabled()
  })
})

describe('step 4 — a pull\'s hosts: the host verified, and the hosts it removes named first (host-sync-identity §8)', () => {
  const HOSTS_META = { section: 'hosts', rev: 4, hash: 'hh', fingerprint: 'f', ordinal: 3, writer: 'c', updatedAt: 1 }
  /** P1 holds a `hosts` section with ONE row: mlab's daemon. h2 and h3 are only this device's — a pull removes them. */
  beforeEach(() => {
    vi.mocked(listProfiles).mockResolvedValue({ kind: 'ok', value: [{ ...entry(P1, 'default'), sections: [HOSTS_META] }, entry(P2, 'empty one', 0)] })
    vi.mocked(getSection).mockResolvedValue({ kind: 'ok', value: { ...HOSTS_META, payload: { hosts: { d1_x: { name: 'mlab', daemonId: MLAB } }, hostOrder: ['d1_x'] } } })
  })
  const removedShown = (): string[] => [...screen.getByTestId('profile-wizard-pull-removes').querySelectorAll('li')].map((li) => li.getAttribute('data-testid') ?? '')

  it('names, one by one, the hosts the pull removes — read pinned to the host\'s address; Start runs with exactly that list', async () => {
    await toDirection()
    await choosePull()
    expect(getSection).toHaveBeenCalledWith('h1', P1, 'hosts', { expectEndpoint: '10.0.0.1:7860' })
    expect(removedShown()).toEqual(['profile-wizard-pull-removes-h2', 'profile-wizard-pull-removes-h3'])
    expect(screen.getByTestId('profile-wizard-pull-removes-h2')).toHaveTextContent('air')
    expect(screen.getByTestId('profile-wizard-pull-removes-h3')).toHaveTextContent('gone')
    expect(screen.getByTestId('profile-wizard-pull-removes')).toHaveTextContent(en['settings.profile.wizard.pull.removes'])
    next()
    click('profile-wizard-start')
    await flush()
    expect(calls).toEqual(['copy-master', 'attach'])
  })

  it('nothing to remove: no list', async () => {
    vi.mocked(getSection).mockResolvedValue({ kind: 'ok', value: { ...HOSTS_META, payload: { hosts: { a: { daemonId: MLAB }, b: { name: 'air' } }, hostOrder: [] } } })
    useHostStore.setState({ hosts: { h1: useHostStore.getState().hosts.h1 }, hostOrder: ['h1'] })
    await toDirection()
    await choosePull()
    expect(screen.queryByTestId('profile-wizard-pull-removes')).toBeNull()
    expect(screen.getByTestId('profile-wizard-next')).not.toBeDisabled()
  })

  it.each([
    ['not verified', { status: 'connected' as const }, 'master-unverified'],
    ['a mismatch', { status: 'connected' as const, daemonIdMismatch: { stored: MLAB, observed: 'other:zzzzzz', endpoint: '10.0.0.1:7860' } }, 'master-mismatch'],
  ])('the host %s: pull is refused in words, Next stays shut, the host is not asked — push remains', async (_label, rt, reason) => {
    useHostStore.setState({ runtime: { ...useHostStore.getState().runtime, h1: rt } })
    await toDirection()
    await choosePull()
    expect(screen.getByTestId('profile-wizard-pull-refused')).toHaveAttribute('data-reason', reason)
    expect(screen.getByTestId('profile-wizard-pull-refused')).toHaveTextContent(en[`settings.profile.wizard.pull.${reason.replace(/-/g, '_')}` as keyof typeof en])
    expect(screen.getByTestId('profile-wizard-next')).toBeDisabled()
    expect(getSection).not.toHaveBeenCalled()
    click('profile-wizard-direction-push')
    expect(screen.getByTestId('profile-wizard-next')).not.toBeDisabled()
  })

  it('verified while the step is open: the check runs then', async () => {
    useHostStore.setState({ runtime: { ...useHostStore.getState().runtime, h1: { status: 'connected' } } })
    await toDirection()
    await choosePull()
    expect(screen.getByTestId('profile-wizard-pull-refused')).toHaveAttribute('data-reason', 'master-unverified')
    act(() => useHostStore.setState({ runtime: { ...useHostStore.getState().runtime, h1: H1_VERIFIED } }))
    await flush()
    expect(removedShown()).toHaveLength(2)
  })

  it('verification lost and back WHILE the first check is out: the first answer, arriving last, is not the one shown', async () => {
    type SectionAnswer = Awaited<ReturnType<typeof getSection>>
    const answers: Array<(v: SectionAnswer) => void> = []
    vi.mocked(getSection).mockImplementation(() => new Promise<SectionAnswer>((r) => answers.push(r)))
    const rows = (hosts: Record<string, unknown>): SectionAnswer => ({ kind: 'ok', value: { ...HOSTS_META, payload: { hosts, hostOrder: Object.keys(hosts) } } }) as SectionAnswer
    await toDirection()
    await choosePull()
    expect(answers).toHaveLength(1)
    act(() => useHostStore.setState({ runtime: { ...useHostStore.getState().runtime, h1: { status: 'connected' } } }))
    act(() => useHostStore.setState({ runtime: { ...useHostStore.getState().runtime, h1: H1_VERIFIED } }))
    await flush()
    expect(answers).toHaveLength(2) // the same choice, asked again
    // the second (current) answer: mlab only → air and gone are removed
    await act(async () => answers[1](rows({ d1_x: { name: 'mlab', daemonId: MLAB } })))
    await flush()
    expect(removedShown()).toEqual(['profile-wizard-pull-removes-h2', 'profile-wizard-pull-removes-h3'])
    // the first, older answer arrives last, and says something else altogether
    await act(async () => answers[0](rows({ a: { daemonId: MLAB }, b: { daemonId: MLAB } })))
    await flush()
    expect(screen.queryByTestId('profile-wizard-pull-refused')).toBeNull()
    expect(removedShown()).toEqual(['profile-wizard-pull-removes-h2', 'profile-wizard-pull-removes-h3'])
    expect(screen.getByTestId('profile-wizard-next')).not.toBeDisabled()
  })

  it('the profile\'s host list cannot be matched (one daemon twice): said, Next shut', async () => {
    vi.mocked(getSection).mockResolvedValue({ kind: 'ok', value: { ...HOSTS_META, payload: { hosts: { a: { daemonId: MLAB }, b: { daemonId: MLAB } }, hostOrder: [] } } })
    await toDirection()
    await choosePull()
    expect(screen.getByTestId('profile-wizard-pull-refused')).toHaveAttribute('data-reason', 'duplicate-host-identity')
    expect(screen.getByTestId('profile-wizard-next')).toBeDisabled()
  })

  it('the profile has NO ROW for the attach host itself: pull refused in words naming it, Next shut — other hosts would not have stopped it', async () => {
    vi.mocked(getSection).mockResolvedValue({ kind: 'ok', value: { ...HOSTS_META, payload: { hosts: { b: { name: 'air-row', daemonId: 'air:999999' } }, hostOrder: ['b'] } } })
    await toDirection()
    await choosePull()
    expect(screen.getByTestId('profile-wizard-pull-refused')).toHaveAttribute('data-reason', 'master-unmatched')
    expect(screen.getByTestId('profile-wizard-pull-refused')).toHaveTextContent(en['settings.profile.wizard.pull.master_unmatched'].replace('{{name}}', 'mlab'))
    expect(screen.queryByTestId('profile-wizard-pull-removes')).toBeNull()
    expect(screen.getByTestId('profile-wizard-next')).toBeDisabled()
    click('profile-wizard-direction-push')
    expect(screen.getByTestId('profile-wizard-next')).not.toBeDisabled()
  })

  it('START FINDS THE ATTACH HOST UNMATCHED (the profile\'s host list changed meanwhile): nothing runs — back to the direction step, which says why', async () => {
    await toDirection()
    await choosePull()
    next()
    vi.mocked(getSection).mockResolvedValue({ kind: 'ok', value: { ...HOSTS_META, payload: { hosts: { b: { name: 'air-row', daemonId: 'air:999999' } }, hostOrder: ['b'] } } })
    click('profile-wizard-start')
    await flush()
    expect(calls).toEqual([])
    expect(step()).toBe('direction')
    expect(screen.getByTestId('profile-wizard-notice')).toHaveAttribute('data-reason', 'pull-refused')
    expect(screen.getByTestId('profile-wizard-pull-refused')).toHaveAttribute('data-reason', 'master-unmatched')
  })

  it('the host list cannot be read: the class of the failure, and Try again asks again', async () => {
    vi.mocked(getSection).mockResolvedValueOnce(failed('timeout'))
    await toDirection()
    await choosePull()
    expect(screen.getByTestId('profile-wizard-pull-refused')).toHaveTextContent(en['settings.profile.wizard.request.timeout'])
    expect(screen.getByTestId('profile-wizard-pull-refused')).not.toHaveTextContent('SECRET')
    click('profile-wizard-pull-retry')
    await flush()
    expect(removedShown()).toHaveLength(2)
  })

  it('START FINDS ANOTHER LIST (a host was added here meanwhile): nothing runs — back to the direction step, the list as it is now', async () => {
    await toDirection()
    await choosePull()
    next()
    act(() => useHostStore.setState({ hosts: { ...useHostStore.getState().hosts, h4: host('h4', 'fresh', '10.0.0.4') }, hostOrder: ['h1', 'h2', 'h3', 'h4'] }))
    click('profile-wizard-start')
    await flush()
    expect(calls).toEqual([])
    expect(step()).toBe('direction')
    expect(screen.getByTestId('profile-wizard-notice')).toHaveAttribute('data-reason', 'removes-changed')
    expect(removedShown()).toEqual(['profile-wizard-pull-removes-h2', 'profile-wizard-pull-removes-h3', 'profile-wizard-pull-removes-h4'])
    // shown now: the next Start runs
    next()
    click('profile-wizard-start')
    await flush()
    expect(calls).toEqual(['copy-master', 'attach'])
  })

  it('START FINDS THE HOST UNVERIFIED: nothing runs — back to the direction step, which says why', async () => {
    await toDirection()
    await choosePull()
    next()
    act(() => useHostStore.setState({ runtime: { ...useHostStore.getState().runtime, h1: { status: 'connected' } } }))
    click('profile-wizard-start')
    await flush()
    expect(calls).toEqual([])
    expect(step()).toBe('direction')
    expect(screen.getByTestId('profile-wizard-notice')).toHaveAttribute('data-reason', 'pull-refused')
    expect(screen.getByTestId('profile-wizard-pull-refused')).toHaveAttribute('data-reason', 'master-unverified')
  })

  it('A HOST ADDED WHILE THE COPY IS MADE (after the door said yes): the attach is not made — back to the direction step, the list as it is now; the copy stays', async () => {
    vi.mocked(copyMasterAsSlave).mockImplementationOnce(() => {
      calls.push('copy-master')
      useHostStore.setState({ hosts: { ...useHostStore.getState().hosts, h4: host('h4', 'fresh', '10.0.0.4') }, hostOrder: ['h1', 'h2', 'h3', 'h4'] })
      return { ok: true, id: 'c1' }
    })
    await toDirection()
    await choosePull()
    next()
    click('profile-wizard-start')
    await flush()
    expect(calls).toEqual(['copy-master'])
    expect(attachMaster).not.toHaveBeenCalled()
    expect(step()).toBe('direction')
    expect(screen.getByTestId('profile-wizard-notice')).toHaveAttribute('data-reason', 'removes-changed')
    expect(removedShown()).toEqual(['profile-wizard-pull-removes-h2', 'profile-wizard-pull-removes-h3', 'profile-wizard-pull-removes-h4'])
  })

  it('THE HOST CANNOT BE ASKED RIGHT BEFORE THE ATTACH: the attach step fails with the request\'s class; Try again asks again and attaches — no second copy', async () => {
    await toDirection()
    await choosePull()
    next()
    // the door's two reads pass; the ask before the attach cannot list
    vi.mocked(listProfiles).mockResolvedValueOnce({ kind: 'ok', value: [{ ...entry(P1, 'default'), sections: [HOSTS_META] }] }).mockResolvedValueOnce(failed('timeout'))
    click('profile-wizard-start')
    await flush()
    expect(calls).toEqual(['copy-master'])
    expect(screen.getByTestId('profile-wizard-failure')).toHaveAttribute('data-step', 'attach')
    expect(screen.getByTestId('profile-wizard-check-failed')).toHaveTextContent(en['settings.profile.wizard.request.timeout'])
    click('profile-wizard-retry')
    await flush()
    expect(calls).toEqual(['copy-master', 'attach'])
    expect(screen.getByTestId('profile-wizard-done')).toBeInTheDocument()
  })

  /** The copy is made, and meanwhile h1 is re-pointed to 10.0.0.9 (and confirmed at its daemon there). */
  const repointDuringCopy = (): void => {
    vi.mocked(copyMasterAsSlave).mockImplementationOnce(() => {
      calls.push('copy-master')
      const hosts = useHostStore.getState()
      useHostStore.setState({ hosts: { ...hosts.hosts, h1: { ...hosts.hosts.h1, ip: '10.0.0.9' } }, runtime: { ...hosts.runtime, h1: { status: 'connected', daemonIdVerified: { endpoint: '10.0.0.9:7860', daemonId: MLAB } } } })
      return { ok: true, id: 'c1' }
    })
  }

  it('THE HOST RE-POINTED WHILE THE COPY IS MADE: the attach waits; Retry reads the NEW address and attaches there — no second promote, no second copy', async () => {
    repointDuringCopy()
    await toDirection('s1')
    await choosePull()
    next()
    click('profile-wizard-start')
    await flush()
    expect(calls).toEqual(['promote', 'copy-master'])
    expect(screen.getByTestId('profile-wizard-failure')).toHaveAttribute('data-step', 'attach')
    expect(screen.getByTestId('profile-wizard-check-failed')).toHaveTextContent(en['settings.profile.wizard.request.endpoint_changed'])
    click('profile-wizard-retry')
    await flush()
    expect(calls).toEqual(['promote', 'copy-master', 'attach'])
    expect(promoteToMaster).toHaveBeenCalledTimes(1)
    expect(copyMasterAsSlave).toHaveBeenCalledTimes(1)
    expect(getSection).toHaveBeenLastCalledWith('h1', P1, 'hosts', { expectEndpoint: '10.0.0.9:7860' })
    expect(screen.getByTestId('profile-wizard-done')).toBeInTheDocument()
  })

  it('… and when Retry finds OTHER hosts to remove (one added meanwhile): back to the direction step with the list as it is now — no attach', async () => {
    repointDuringCopy()
    await toDirection('s1')
    await choosePull()
    next()
    click('profile-wizard-start')
    await flush()
    expect(screen.getByTestId('profile-wizard-failure')).toHaveAttribute('data-step', 'attach')
    act(() => useHostStore.setState({ hosts: { ...useHostStore.getState().hosts, h4: host('h4', 'fresh', '10.0.0.4') }, hostOrder: ['h1', 'h2', 'h3', 'h4'] }))
    click('profile-wizard-retry')
    await flush()
    expect(calls).toEqual(['promote', 'copy-master'])
    expect(attachMaster).not.toHaveBeenCalled()
    expect(step()).toBe('direction')
    expect(screen.getByTestId('profile-wizard-notice')).toHaveAttribute('data-reason', 'removes-changed')
    expect(removedShown()).toEqual(['profile-wizard-pull-removes-h2', 'profile-wizard-pull-removes-h3', 'profile-wizard-pull-removes-h4'])
  })

  it('ANOTHER host at a daemon other than its record: not in the way, but said — the sync will pause on it', async () => {
    const hosts = useHostStore.getState().hosts
    useHostStore.setState({ hosts: { ...hosts, h2: { ...hosts.h2, daemonId: 'air:111111' } }, runtime: { ...useHostStore.getState().runtime, h2: { status: 'connected', daemonIdMismatch: { stored: 'air:111111', observed: 'else:222222', endpoint: '10.0.0.2:7860' } } } })
    await toDirection()
    expect(screen.getByTestId('profile-wizard-host-mismatch-h2')).toHaveTextContent(en['settings.profile.wizard.direction.host_mismatch'].replace('{{name}}', 'air'))
    expect(screen.queryByTestId('profile-wizard-host-mismatch-h1')).toBeNull()
    click('profile-wizard-direction-push')
    expect(screen.getByTestId('profile-wizard-next')).not.toBeDisabled()
    await choosePull()
    expect(screen.getByTestId('profile-wizard-host-mismatch-h2')).toBeInTheDocument()
    expect(screen.getByTestId('profile-wizard-next')).not.toBeDisabled()
  })
})

describe('step 5 — the run', () => {
  it('nothing runs until Start; the summary lists what will', async () => {
    await toRun('pull', 's1')
    expect(calls).toEqual([])
    expect(['promote', 'save', 'attach'].map((id) => screen.getByTestId(`profile-wizard-substep-${id}`).getAttribute('data-state'))).toEqual(['pending', 'pending', 'pending'])
  })

  it('pull, a local profile chosen, a copy kept: promote → the copy OF THE MASTER → attach; never the screen', async () => {
    await toRun('pull', 's1')
    click('profile-wizard-start')
    await flush()
    expect(calls).toEqual(['promote', 'copy-master', 'attach'])
    expect(promoteToMaster).toHaveBeenCalledWith('s1', 'Laptop 2')
    expect(copyMasterAsSlave).toHaveBeenCalledWith('Laptop')
    expect(attachMaster).toHaveBeenCalledWith('h1', P1, 'pull')
    expect(saveScreenAsSlave).not.toHaveBeenCalled()
    expect(screen.getByTestId('profile-wizard-done')).toBeInTheDocument()
    expect(screen.queryByTestId('profile-wizard-start')).toBeNull()
  })

  it('pull, copy unticked: no copy', async () => {
    await toDirection()
    await choosePull()
    click('profile-wizard-save-first')
    next()
    click('profile-wizard-start')
    await flush()
    expect(calls).toEqual(['attach'])
  })

  it('push, the master as it is: attach only', async () => {
    await toRun('push')
    click('profile-wizard-start')
    await flush()
    expect(calls).toEqual(['attach'])
    expect(attachMaster).toHaveBeenCalledWith('h1', P1, 'push')
  })

  it('attached at the start: the sync is stopped BEFORE anything is promoted', async () => {
    attachedStore()
    open()
    click('profile-wizard-stop-confirm')
    await flush()
    click(`profile-wizard-profile-${P1}`)
    next()
    await flush()
    click('profile-wizard-local-s1')
    next()
    click('profile-wizard-direction-push')
    next()
    click('profile-wizard-start')
    await flush()
    expect(calls).toEqual(['detach', 'promote', 'attach'])
  })

  it('the promote fails: it STOPS there — no copy, no attach — and the screen says what was and was not done', async () => {
    vi.mocked(promoteToMaster).mockResolvedValueOnce({ ok: false, reason: 'busy' })
    await toRun('pull', 's1')
    click('profile-wizard-start')
    await flush()
    expect(copyMasterAsSlave).not.toHaveBeenCalled()
    expect(attachMaster).not.toHaveBeenCalled()
    expect(['promote', 'save', 'attach'].map((id) => screen.getByTestId(`profile-wizard-substep-${id}`).getAttribute('data-state'))).toEqual(['failed', 'pending', 'pending'])
    const failure = screen.getByTestId('profile-wizard-failure')
    expect(failure).toHaveAttribute('data-step', 'promote')
    expect(failure).toHaveAttribute('data-reason', 'busy')
    expect(failure).toHaveTextContent(en['settings.profile.wizard.promote.busy'])
    expect(failure).toHaveTextContent(en['settings.profile.wizard.now.nothing_changed'])
    expect(screen.queryByTestId('profile-wizard-done')).toBeNull()
  })

  it('the attach fails after a promote and a copy: both are said as done, the device as not syncing; Retry runs the attach ONLY', async () => {
    vi.mocked(attachMaster).mockImplementationOnce(async () => (calls.push('attach'), { ok: false, reason: 'timeout' }))
    await toRun('pull', 's1')
    click('profile-wizard-start')
    await flush()
    expect(['promote', 'save', 'attach'].map((id) => screen.getByTestId(`profile-wizard-substep-${id}`).getAttribute('data-state'))).toEqual(['done', 'done', 'failed'])
    const failure = screen.getByTestId('profile-wizard-failure')
    expect(failure).toHaveTextContent(en['settings.profile.wizard.attach.timeout'])
    expect(failure).toHaveTextContent(en['settings.profile.wizard.now.promoted'].replace('{{name}}', 'Scratch'))
    expect(failure).toHaveTextContent(en['settings.profile.wizard.now.saved'].replace('{{name}}', 'Laptop'))
    expect(failure).toHaveTextContent(en['settings.profile.wizard.now.not_syncing'])
    click('profile-wizard-retry')
    await flush()
    expect(calls).toEqual(['promote', 'copy-master', 'attach', 'attach'])
    expect(screen.getByTestId('profile-wizard-done')).toBeInTheDocument()
  })

  it('the copy fails: no attach', async () => {
    vi.mocked(copyMasterAsSlave).mockReturnValueOnce({ ok: false, reason: 'unsettled' })
    await toRun('pull')
    click('profile-wizard-start')
    await flush()
    expect(attachMaster).not.toHaveBeenCalled()
    expect(screen.getByTestId('profile-wizard-failure')).toHaveTextContent(en['settings.profile.wizard.save.unsettled'])
  })

  it.each(ATTACH_REASONS)('attach reason %s has its own sentence', async (reason) => {
    vi.mocked(attachMaster).mockResolvedValueOnce({ ok: false, reason })
    await toRun('push')
    click('profile-wizard-start')
    await flush()
    const key = `settings.profile.wizard.attach.${reason.replace(/-/g, '_')}` as keyof typeof en
    expect(en[key]).toBeTruthy()
    expect(screen.getByTestId('profile-wizard-failure')).toHaveAttribute('data-reason', reason)
    expect(screen.getByTestId('profile-wizard-failure')).toHaveTextContent(en[key])
  })

  it('every attach sentence is its own', () => {
    const sentences = ATTACH_REASONS.map((r) => en[`settings.profile.wizard.attach.${r.replace(/-/g, '_')}` as keyof typeof en])
    expect(new Set(sentences).size).toBe(ATTACH_REASONS.length)
  })

  it('a reason that is an error\'s message is NEVER shown', async () => {
    vi.mocked(attachMaster).mockResolvedValueOnce({ ok: false, reason: 'TypeError: Failed to fetch http://10.0.0.1:7860/?token=SECRET' })
    await toRun('push')
    click('profile-wizard-start')
    await flush()
    expect(screen.getByTestId('profile-wizard-failure')).toHaveAttribute('data-reason', 'other')
    expect(screen.getByTestId('profile-wizard-failure')).toHaveTextContent(en['settings.profile.wizard.attach.other'])
    expect(document.body.textContent).not.toMatch(/SECRET|Failed to fetch/)
  })

  it('a failure a retry cannot cure (another window attached): Start over instead of Retry — from the first step, as things are NOW', async () => {
    vi.mocked(promoteToMaster).mockImplementationOnce(async () => {
      attachedStore()
      return { ok: false, reason: 'master-attached' }
    })
    await toRun('push', 's1')
    click('profile-wizard-start')
    await flush()
    expect(screen.queryByTestId('profile-wizard-retry')).toBeNull()
    click('profile-wizard-restart')
    expect(step()).toBe('stop')
  })

  it('while it runs there is no Close, no Back, no second Start', async () => {
    let release: (v: never) => void = () => {}
    vi.mocked(attachMaster).mockReturnValueOnce(new Promise((r) => (release = r)))
    await toRun('push')
    click('profile-wizard-start')
    await flush()
    expect(screen.getByTestId('profile-wizard-substep-attach')).toHaveAttribute('data-state', 'running')
    expect(screen.queryByTestId('profile-wizard-start')).toBeNull()
    expect(screen.queryByTestId('profile-wizard-back')).toBeNull()
    expect(screen.getByTestId('profile-wizard-close')).toBeDisabled()
    await act(async () => release({ ok: true } as never))
    expect(screen.getByTestId('profile-wizard-close')).not.toBeDisabled()
  })

  it('the master attached by the run itself does not send the wizard back to "stop"', async () => {
    vi.mocked(attachMaster).mockImplementationOnce(async () => {
      attachedStore()
      return { ok: true }
    })
    await toRun('push')
    click('profile-wizard-start')
    await flush()
    expect(step()).toBe('run')
    expect(screen.getByTestId('profile-wizard-done')).toBeInTheDocument()
  })
})

describe('a reason becomes a sentence through a closed list — whatever the reason holds', () => {
  it('a run reason that is on no list gets the step\'s `other` sentence; it is never made into a key', () => {
    expect(reasonKey('attach', 'timeout')).toBe('settings.profile.wizard.attach.timeout')
    expect(reasonKey('attach', 'Failed to fetch http://h/?token=SECRET')).toBe('settings.profile.wizard.attach.other')
    expect(reasonKey('promote', 'master-attached')).toBe('settings.profile.wizard.promote.master_attached')
    expect(reasonKey('promote', 'timeout')).toBe('settings.profile.wizard.promote.other') // another step's reason is not this step's
    expect(reasonKey('save', 'QuotaExceededError: SECRET')).toBe('settings.profile.wizard.save.other')
  })

  it('a request reason likewise', () => {
    expect(requestKey('too-large')).toBe('settings.profile.wizard.request.too_large')
    expect(requestKey('TypeError SECRET')).toBe('settings.profile.wizard.request.thrown')
  })

  it('every key either list can produce exists', () => {
    const promote = ['master-attached', 'busy', 'unsettled', 'superseded', 'not-found', 'bad-name', 'bad-epoch', 'write-failed', 'x']
    const save = ['unsettled', 'bad-name', 'bad-world', 'write-failed', 'x']
    const keys = [...promote.map((r) => reasonKey('promote', r)), ...save.map((r) => reasonKey('save', r)), ...[...ATTACH_REASONS, 'x'].map((r) => reasonKey('attach', r))]
    for (const key of keys) expect(en[key as keyof typeof en], key).toBeTruthy()
  })
})

describe('closed half-way, and opened again', () => {
  it('starts from the beginning, from the state as it is — nothing of the first visit is remembered', async () => {
    await toDirection('s1')
    click('profile-wizard-close')
    expect(onClose).toHaveBeenCalledTimes(1)
    cleanup()
    open()
    await flush()
    expect(step()).toBe('sot')
    expect((screen.getByTestId(`profile-wizard-profile-${P1}`) as HTMLInputElement).checked).toBe(false)
    expect(localStorage.length === 0 || ![...Array(localStorage.length).keys()].some((i) => /wizard/i.test(localStorage.key(i) ?? ''))).toBe(true)
  })
})

describe('another window changed things: every premise is checked again, and the wizard goes back to the step that still stands', () => {
  it('a master was attached elsewhere: back to "stop"', async () => {
    await toDirection()
    act(() => attachedStore())
    expect(step()).toBe('stop')
    expect(screen.getByTestId('profile-wizard-notice')).toHaveAttribute('data-reason', 'attached-elsewhere')
  })

  it('the chosen local profile was deleted: back to step 3, the master chosen', async () => {
    await toDirection('s1')
    act(() => useLocalProfilesStore.setState({ slaves: {}, slaveOrder: [] }))
    expect(step()).toBe('local')
    expect(screen.getByTestId('profile-wizard-notice')).toHaveAttribute('data-reason', 'local-gone')
    expect((screen.getByTestId('profile-wizard-local-master') as HTMLInputElement).checked).toBe(true)
  })

  it('the host went offline: back to step 2, nothing chosen on it', async () => {
    await toRun('push')
    act(() => useHostStore.getState().setRuntime('h1', { status: 'disconnected' }))
    expect(step()).toBe('sot')
    expect(screen.getByTestId('profile-wizard-notice')).toHaveAttribute('data-reason', 'host-offline')
    expect(screen.queryByTestId('profile-wizard-start')).toBeNull()
  })

  it('the host was removed: back to step 2', async () => {
    await toLocal()
    act(() => useHostStore.setState({ hosts: { h2: host('h2', 'air', '10.0.0.2') }, hostOrder: ['h2'] }))
    expect(step()).toBe('sot')
    expect(screen.getByTestId('profile-wizard-notice')).toHaveAttribute('data-reason', 'host-gone')
  })

  it('checked at the click, too — a store that moved without a render in between does not slip through', async () => {
    await toRun('push', 's1')
    // no act(): the component has not rendered this yet when Start is pressed
    useLocalProfilesStore.setState({ slaves: {}, slaveOrder: [] })
    fireEvent.click(screen.getByTestId('profile-wizard-start'))
    await waitFor(() => expect(step()).toBe('local'))
    expect(promoteToMaster).not.toHaveBeenCalled()
    expect(attachMaster).not.toHaveBeenCalled()
  })
})

// === PR-B (review F1, F2, F5; acceptance F6) ===

describe('Start asks the host ONCE MORE — the profile must still be what the user saw (review F1)', () => {
  const full = (id: string, name: string, rev = 1) => ({ ...entry(id, name), sections: [{ section: 'workspaces', rev, hash: `h${rev}`, fingerprint: 'f', ordinal: 1, writer: 'c', updatedAt: 1 }] })

  it('THE ATTACK: seen empty (push only, no warning) → another device fills it → Start runs NOTHING, goes back to the direction, says why; now both directions and the warning are there', async () => {
    await toDirection('master', P2)
    expect(screen.getByTestId('profile-wizard-direction-pull')).toBeDisabled()
    expect(screen.queryByTestId('profile-wizard-push-warning')).toBeNull()
    next()
    vi.mocked(listProfiles).mockResolvedValue({ kind: 'ok', value: [entry(P1, 'default'), full(P2, 'empty one')] })
    click('profile-wizard-start')
    await flush()
    expect(attachMaster).not.toHaveBeenCalled()
    expect(step()).toBe('direction')
    expect(screen.getByTestId('profile-wizard-notice')).toHaveAttribute('data-reason', 'profile-changed')
    expect(screen.getByTestId('profile-wizard-notice')).toHaveTextContent(en['settings.profile.wizard.notice.profile_changed'])
    expect(screen.getByTestId('profile-wizard-direction-pull')).not.toBeDisabled()
    expect((screen.getByTestId('profile-wizard-direction-push') as HTMLInputElement).checked).toBe(false) // chosen AGAIN, by the user
    expect(screen.getByTestId('profile-wizard-next')).toBeDisabled()
    click('profile-wizard-direction-push')
    expect(screen.getByTestId('profile-wizard-push-warning')).toBeInTheDocument()
    next()
    click('profile-wizard-start')
    await flush()
    expect(attachMaster).toHaveBeenCalledWith('h1', P2, 'push') // what is there now is what the user has seen now
  })

  it('a profile THIS visit created is no exception: filled by another device before Start → the same', async () => {
    open()
    await flush()
    click('profile-wizard-profile-new')
    next()
    await flush()
    next()
    next()
    expect(step()).toBe('run')
    vi.mocked(listProfiles).mockResolvedValue({ kind: 'ok', value: [full('p_00000000000c', 'Laptop')] })
    click('profile-wizard-start')
    await flush()
    expect(attachMaster).not.toHaveBeenCalled()
    expect(step()).toBe('direction')
    expect(screen.getByTestId('profile-wizard-direction-pull')).not.toBeDisabled()
  })

  it('holding content, and the content MOVED (a rev): nothing runs either', async () => {
    vi.mocked(listProfiles).mockResolvedValue({ kind: 'ok', value: [full(P1, 'default', 4)] })
    await toRun('pull')
    vi.mocked(listProfiles).mockResolvedValue({ kind: 'ok', value: [full(P1, 'default', 5)] })
    click('profile-wizard-start')
    await flush()
    expect(calls).toEqual([])
    expect(step()).toBe('direction')
    expect((screen.getByTestId('profile-wizard-direction-pull') as HTMLInputElement).checked).toBe(false)
  })

  it('emptied meanwhile: said as that, and pull is no longer offered', async () => {
    await toRun('pull')
    vi.mocked(listProfiles).mockResolvedValue({ kind: 'ok', value: [entry(P1, 'default', 0)] })
    click('profile-wizard-start')
    await flush()
    expect(calls).toEqual([])
    expect(screen.getByTestId('profile-wizard-notice')).toHaveAttribute('data-reason', 'profile-emptied')
    expect(screen.getByTestId('profile-wizard-direction-pull')).toBeDisabled()
  })

  it('deleted meanwhile: back to the profile step, nothing chosen', async () => {
    await toRun('push')
    vi.mocked(listProfiles).mockResolvedValue({ kind: 'ok', value: [entry(P2, 'empty one', 0)] })
    click('profile-wizard-start')
    await flush()
    expect(calls).toEqual([])
    expect(step()).toBe('sot')
    expect(screen.getByTestId('profile-wizard-notice')).toHaveAttribute('data-reason', 'profile-gone')
    expect(screen.getByTestId('profile-wizard-next')).toBeDisabled()
  })

  it('the host cannot be asked: NOTHING runs, it says why in a sentence of its own, and Start can be pressed again', async () => {
    await toRun('push', 's1')
    vi.mocked(listProfiles).mockResolvedValueOnce(failed('timeout'))
    click('profile-wizard-start')
    await flush()
    expect(calls).toEqual([])
    expect(step()).toBe('run')
    expect(screen.getByTestId('profile-wizard-check-failed')).toHaveAttribute('data-reason', 'timeout')
    expect(screen.getByTestId('profile-wizard-check-failed')).toHaveTextContent(en['settings.profile.wizard.run.check_failed'])
    expect(screen.getByTestId('profile-wizard-check-failed')).toHaveTextContent(en['settings.profile.wizard.request.timeout'])
    expect(document.body.textContent).not.toMatch(/RAW-|SECRET/)
    click('profile-wizard-start')
    await flush()
    expect(calls).toEqual(['promote', 'attach'])
    expect(screen.queryByTestId('profile-wizard-check-failed')).toBeNull()
  })

  it('while the host is being asked: said, and no second Start', async () => {
    await toRun('push')
    let release: (v: never) => void = () => {}
    vi.mocked(listProfiles).mockReturnValueOnce(new Promise((r) => (release = r)))
    click('profile-wizard-start')
    expect(screen.getByTestId('profile-wizard-checking')).toBeInTheDocument()
    expect(screen.queryByTestId('profile-wizard-start')).toBeNull()
    expect(screen.getByTestId('profile-wizard-close')).toBeDisabled()
    await act(async () => release({ kind: 'ok', value: [entry(P1, 'default')] } as never))
    await flush()
    expect(calls).toEqual(['attach'])
  })

  it('a Retry asks again, too: the profile changed since the first attempt → no second attempt', async () => {
    vi.mocked(attachMaster).mockImplementationOnce(async () => (calls.push('attach'), { ok: false, reason: 'timeout' }))
    await toRun('push')
    click('profile-wizard-start')
    await flush()
    vi.mocked(listProfiles).mockResolvedValue({ kind: 'ok', value: [full(P1, 'default', 9)] })
    click('profile-wizard-retry')
    await flush()
    expect(calls).toEqual(['attach'])
    expect(step()).toBe('direction')
  })
})

describe('a create whose outcome is not known is looked for, never simply sent again (review F2)', () => {
  const OURS = { ...entry('p_00000000000d', 'Laptop', 0), createdAt: 9 }
  const toNew = async () => {
    open()
    await flush()
    click('profile-wizard-profile-new')
  }

  it('the answer was lost and a profile like it has appeared: it is POINTED AT, never taken — it may be another device\'s. The USER chooses it (or renames); one POST', async () => {
    vi.mocked(createProfile).mockResolvedValueOnce(failed('timeout'))
    await toNew()
    vi.mocked(listProfiles).mockResolvedValue({ kind: 'ok', value: [entry(P1, 'default'), OURS] })
    next()
    await flush()
    expect(step()).toBe('sot') // NOT moved on by itself
    expect(screen.getByTestId('profile-wizard-notice')).toHaveAttribute('data-reason', 'create-maybe')
    expect(screen.getByTestId('profile-wizard-notice')).toHaveTextContent(en['settings.profile.wizard.notice.create_maybe'])
    expect(screen.getByTestId(`profile-wizard-profile-maybe-${OURS.id}`)).toHaveTextContent(en['settings.profile.wizard.sot.maybe_yours'])
    expect((screen.getByTestId(`profile-wizard-profile-${OURS.id}`) as HTMLInputElement).checked).toBe(false)
    // pressing again with "new" still chosen creates nothing: the candidate is pointed at again
    next()
    await flush()
    expect(createProfile).toHaveBeenCalledTimes(1)
    expect(step()).toBe('sot')
    // the user takes it: an existing profile like any other — empty, so push only; Start checks its fingerprint (F1)
    click(`profile-wizard-profile-${OURS.id}`)
    next()
    await flush()
    expect(step()).toBe('local')
    next()
    expect(screen.getByTestId('profile-wizard-direction-pull')).toBeDisabled()
    next()
    click('profile-wizard-start')
    await flush()
    expect(attachMaster).toHaveBeenCalledWith('h1', OURS.id, 'push')
    expect(createProfile).toHaveBeenCalledTimes(1)
  })

  it('… or renames: another name is another create, sent at once', async () => {
    vi.mocked(createProfile).mockResolvedValueOnce(failed('timeout'))
    await toNew()
    vi.mocked(listProfiles).mockResolvedValue({ kind: 'ok', value: [entry(P1, 'default'), OURS] })
    next()
    await flush()
    fireEvent.change(screen.getByTestId('profile-wizard-new-name'), { target: { value: 'Laptop B' } })
    next()
    await flush()
    expect(createProfile).toHaveBeenLastCalledWith('h1', 'Laptop B')
    expect(step()).toBe('local')
  })

  it('unknown AND the list unreadable: said; the next press LOOKS FIRST — finds a candidate, points at it, and sends no second POST', async () => {
    vi.mocked(createProfile).mockResolvedValueOnce(failed('network'))
    await toNew()
    // the look right after the POST cannot read the list; the list the page then reloads CAN — and shows the candidate
    vi.mocked(listProfiles).mockResolvedValueOnce(failed('network')).mockResolvedValue({ kind: 'ok', value: [entry(P1, 'default'), OURS] })
    next()
    await flush()
    expect(step()).toBe('sot')
    expect(screen.getByTestId('profile-wizard-create-error')).toHaveAttribute('data-outcome', 'unknown')
    expect(screen.getByTestId('profile-wizard-create-error')).toHaveTextContent(en['settings.profile.wizard.sot.create_unknown'])
    // THE BASELINE IS FROM BEFORE THE FIRST POST: the candidate is in the list on screen by now, and is still "new since"
    expect(screen.getByTestId(`profile-wizard-profile-${OURS.id}`)).toBeInTheDocument()
    next()
    await flush()
    expect(createProfile).toHaveBeenCalledTimes(1)
    expect(step()).toBe('sot')
    expect(screen.getByTestId(`profile-wizard-profile-maybe-${OURS.id}`)).toBeInTheDocument()
  })

  it('a profile of that name, empty, that was in the list BEFORE the create is not taken for it', async () => {
    const old = entry('p_00000000000e', 'Laptop', 0)
    vi.mocked(listProfiles).mockResolvedValue({ kind: 'ok', value: [entry(P1, 'default'), old] })
    vi.mocked(createProfile).mockResolvedValueOnce(failed('timeout'))
    await toNew()
    fireEvent.change(screen.getByTestId('profile-wizard-new-name'), { target: { value: 'Laptop' } })
    next()
    await flush()
    expect(step()).toBe('sot')
    expect(screen.getByTestId('profile-wizard-create-error')).toHaveAttribute('data-outcome', 'not-created')
    expect(screen.getByTestId('profile-wizard-create-error')).toHaveTextContent(en['settings.profile.wizard.sot.create_not_created'])
    next()
    await flush()
    expect(createProfile).toHaveBeenCalledTimes(2) // looked first, still not there: now it may be sent again
    expect(step()).toBe('local')
  })

  it('a definite refusal is just that: no list, and the next press sends again', async () => {
    vi.mocked(createProfile).mockResolvedValueOnce(failed('rejected', 400))
    await toNew()
    const lists = vi.mocked(listProfiles).mock.calls.length
    next()
    await flush()
    expect(screen.getByTestId('profile-wizard-create-error')).toHaveAttribute('data-outcome', 'failed')
    expect(vi.mocked(listProfiles).mock.calls.length).toBe(lists)
  })
})

describe('the host list follows the connections while the wizard is open', () => {
  it('a host that connects: its option becomes choosable, its label loses "not connected" — and back when it drops', async () => {
    open()
    await flush()
    const option = () => screen.getByTestId('profile-wizard-host-option-h3') as HTMLOptionElement
    expect(option().disabled).toBe(true)
    act(() => useHostStore.getState().setRuntime('h3', { status: 'connected' }))
    expect(option().disabled).toBe(false)
    expect(option()).toHaveTextContent(/^gone$/)
    act(() => useHostStore.getState().setRuntime('h3', { status: 'reconnecting' }))
    expect(option().disabled).toBe(true)
    expect(option()).toHaveTextContent(en['settings.profile.wizard.sot.host_offline'].replace('{{name}}', 'gone'))
  })

  it('a host added while it is open is listed', async () => {
    open()
    await flush()
    act(() => useHostStore.setState({ hosts: { ...useHostStore.getState().hosts, h4: host('h4', 'fresh', '10.0.0.4') }, hostOrder: ['h1', 'h2', 'h3', 'h4'], runtime: { ...useHostStore.getState().runtime, h4: { status: 'connected' } } }))
    expect((screen.getByTestId('profile-wizard-host-option-h4') as HTMLOptionElement).disabled).toBe(false)
  })
})

describe('no host was connected when the wizard opened (review F5)', () => {
  it('the select is disabled only WHILE nothing can be chosen: the first host that connects is chosen, and listed', async () => {
    useHostStore.setState({ runtime: {} })
    open()
    await flush()
    expect(screen.getByTestId('profile-wizard-host')).toBeDisabled()
    act(() => useHostStore.getState().setRuntime('h2', { status: 'connected' }))
    await flush()
    const select = screen.getByTestId('profile-wizard-host') as HTMLSelectElement
    expect(select).not.toBeDisabled()
    expect(select.value).toBe('h2') // nothing was chosen: the first host that connects is
    expect(screen.queryByTestId('profile-wizard-host-none')).toBeNull()
    expect(listProfiles).toHaveBeenLastCalledWith('h2', { expectEndpoint: '10.0.0.2:7860' })
    expect(screen.getByTestId('profile-wizard-profiles')).toHaveAttribute('data-state', 'rows')
    // and it stays the user's to change
    act(() => useHostStore.getState().setRuntime('h1', H1_VERIFIED))
    fireEvent.change(select, { target: { value: 'h1' } })
    await flush()
    expect(select.value).toBe('h1')
  })

  it('a host ALREADY chosen is never replaced by one that connects later', async () => {
    open()
    await flush()
    const select = screen.getByTestId('profile-wizard-host') as HTMLSelectElement
    expect(select.value).toBe('h1')
    act(() => useHostStore.getState().setRuntime('h3', { status: 'connected' }))
    await flush()
    expect(select.value).toBe('h1')
    expect(listProfiles).not.toHaveBeenCalledWith('h3', expect.anything())
  })
})

describe('the result outlives the page (acceptance F6)', () => {
  const toast = () => useUndoToast.getState().toast?.message ?? null
  beforeEach(() => useUndoToast.getState().dismiss())

  it('push: the done line AND a toast', async () => {
    await toRun('push')
    click('profile-wizard-start')
    await flush()
    expect(screen.getByTestId('profile-wizard-done')).toBeInTheDocument()
    expect(toast()).toBe(en['settings.profile.wizard.toast.done'].replace('{{profile}}', 'default').replace('{{host}}', 'mlab'))
  })

  it('pull with the copy: the toast says where the old workspaces and tabs are', async () => {
    await toRun('pull')
    click('profile-wizard-start')
    await flush()
    expect(toast()).toBe(en['settings.profile.wizard.toast.done_saved'].replace('{{profile}}', 'default').replace('{{host}}', 'mlab').replace('{{name}}', 'Laptop'))
  })

  it('the wizard is UNMOUNTED while the run is out (the world was replaced under it): the result still arrives as a toast, and no state is set on what is gone', async () => {
    const errors = vi.spyOn(console, 'error').mockImplementation(() => {})
    let release: (v: never) => void = () => {}
    vi.mocked(attachMaster).mockReturnValueOnce(new Promise((r) => (release = r)))
    await toRun('pull')
    click('profile-wizard-start')
    await flush()
    cleanup()
    expect(toast()).toBeNull()
    await act(async () => release({ ok: true } as never))
    await flush()
    expect(toast()).toBe(en['settings.profile.wizard.toast.done_saved'].replace('{{profile}}', 'default').replace('{{host}}', 'mlab').replace('{{name}}', 'Laptop'))
    expect(errors).not.toHaveBeenCalled()
    errors.mockRestore()
  })

  it('… and a FAILURE that nobody is there to read is a toast naming the step; one the wizard shows is not', async () => {
    let release: (v: never) => void = () => {}
    vi.mocked(attachMaster).mockReturnValueOnce(new Promise((r) => (release = r)))
    await toRun('push')
    click('profile-wizard-start')
    await flush()
    cleanup()
    await act(async () => release({ ok: false, reason: 'timeout' } as never))
    expect(toast()).toBe(en['settings.profile.wizard.toast.stopped_attach'])

    useUndoToast.getState().dismiss()
    vi.mocked(attachMaster).mockResolvedValueOnce({ ok: false, reason: 'timeout' })
    await toRun('push')
    click('profile-wizard-start')
    await flush()
    expect(screen.getByTestId('profile-wizard-failure')).toBeInTheDocument()
    expect(toast()).toBeNull()
  })
})
