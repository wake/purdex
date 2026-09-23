// spa/src/components/settings/profile/wizard/ProfileWizard.delete.test.tsx — deleting a profile on the host from
// the wizard's step 2 (#1325): after every device has stopped syncing there is no master, so Settings' host block is
// gone — this is the only place left. The rules are useSotDelete.ts's, shared with SotProfilesBlock. The stores are
// the real ones; `start`, `api`, the client identity and `readMasterWorld` are replaced (as ProfileWizard.test.tsx).
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { act, cleanup, fireEvent, render, screen } from '@testing-library/react'
import en from '../../../../locales/en.json'
import { ProfileWizard } from './ProfileWizard'
import { useProfileStore } from '../../../../stores/useProfileStore'
import { useHostStore } from '../../../../stores/useHostStore'
import { useDeviceNameStore } from '../../../../stores/useDeviceNameStore'
import { useLocalProfilesStore } from '../../../../stores/useLocalProfilesStore'
import { deleteProfile, listProfiles } from '../../../../lib/profile/api'
import type { Attachment, DeleteProfileOutcome, ProfileIndexEntry } from '../../../../lib/profile/api'
import { isClientIdPersisted } from '../../../../lib/client-identity'
import { readMasterWorld } from '../../../../lib/profile/master-world'

vi.mock('../../../../lib/profile/start', () => ({ attachMaster: vi.fn(), detachMaster: vi.fn() }))
vi.mock('../../../../lib/profile/api', () => ({ listProfiles: vi.fn(), createProfile: vi.fn(), deleteProfile: vi.fn() }))
vi.mock('../../../../lib/profile/switch-active', () => ({ promoteToMaster: vi.fn(), copyMasterAsSlave: vi.fn(), saveScreenAsSlave: vi.fn() }))
vi.mock('../../../../lib/client-identity', () => ({ isClientIdPersisted: vi.fn(), getClientId: () => 'c_aaaaaaaaaaaa' }))
vi.mock('../../../../lib/profile/master-world', async (importOriginal) => ({
  ...(await importOriginal<typeof import('../../../../lib/profile/master-world')>()),
  readMasterWorld: vi.fn(),
}))

const P1 = 'p_000000000001'
const P2 = 'p_000000000002'
const P9 = 'p_000000000009'
/** Where h1 is — and where its list was fetched from. */
const A = '10.0.0.1:7860'
const attachment = (clientId: string, deviceName: string): Attachment => ({ clientId, profileId: 'p', deviceName, attachedAt: 1, lastSeen: 2 })
const entry = (id: string, name: string, attachments: Attachment[] = []): ProfileIndexEntry => ({ id, name, createdAt: 1, updatedAt: 1, attachments, sections: [{ section: 's', rev: 1, hash: 'h', fingerprint: 'f', ordinal: 1, writer: 'c', updatedAt: 1 }] })
const host = (id: string, name: string, ip: string) => ({ id, name, ip, port: 7860, order: 0 })
const byHost = (map: Record<string, ProfileIndexEntry[]>) =>
  vi.mocked(listProfiles).mockImplementation((hostId: string) => Promise.resolve({ kind: 'ok', value: map[hostId] ?? [] }))
const listsOf = (hostId: string) => vi.mocked(listProfiles).mock.calls.filter(([h]) => h === hostId).length

const open = () => render(<ProfileWizard onClose={() => {}} />)
const wizard = () => screen.getByTestId('profile-wizard')
const step = () => wizard().getAttribute('data-step')
const click = (id: string) => fireEvent.click(screen.getByTestId(id))
const flush = async () => {
  await act(async () => {
    for (let i = 0; i < 10; i++) await Promise.resolve()
  })
}
const askDelete = async (id: string) => {
  open()
  await flush()
  click(`profile-wizard-profile-delete-${id}`)
  expect(screen.getByTestId('profile-wizard-delete-dialog')).toBeInTheDocument()
}

beforeEach(() => {
  vi.mocked(isClientIdPersisted).mockReset().mockReturnValue(true)
  vi.mocked(readMasterWorld).mockReset().mockReturnValue({ settled: true, onScreen: true, world: { workspaces: [], tabs: {} as never, activeWorkspaceId: null, activeTabId: null } })
  vi.mocked(deleteProfile).mockReset().mockResolvedValue({ kind: 'deleted' })
  vi.mocked(listProfiles).mockReset()
  byHost({ h1: [entry(P1, 'default', [attachment('c2', 'Air')]), entry(P2, 'experiment')], h2: [entry(P2, 'same id, other host'), entry(P9, 'other')] })
  useProfileStore.setState({ masterHostId: null, masterProfileId: null, masterEndpoint: null, pendingDirection: null, suspension: null, pendingDetaches: [] })
  useHostStore.setState({ hosts: { h1: host('h1', 'mlab', '10.0.0.1'), h2: host('h2', 'air', '10.0.0.2') }, hostOrder: ['h1', 'h2'], devHostId: 'h1', runtime: { h1: { status: 'connected' }, h2: { status: 'connected' } } })
  useDeviceNameStore.setState({ deviceName: 'Laptop' })
  useLocalProfilesStore.setState({ slaves: {}, slaveOrder: [], activeProfileId: 'master', parkedMaster: null, worldEpoch: 0, relabelCount: 0, master: { name: null } })
})

afterEach(() => cleanup())

describe('step 2 — delete is offered only where the FETCHED index shows nobody attached', () => {
  it('nobody attached → a Delete; anybody attached → none, and the row says why', async () => {
    open()
    await flush()
    expect(screen.getByTestId(`profile-wizard-profile-delete-${P2}`)).toBeEnabled()
    expect(screen.queryByTestId(`profile-wizard-profile-delete-blocked-${P2}`)).toBeNull()
    expect(screen.queryByTestId(`profile-wizard-profile-delete-${P1}`)).toBeNull()
    expect(screen.getByTestId(`profile-wizard-profile-delete-blocked-${P1}`)).toHaveTextContent(en['settings.profile.sot.delete_blocked_attached'])
  })

  it('it asks first, naming the profile; Cancel deletes nothing', async () => {
    await askDelete(P2)
    expect(screen.getByTestId('profile-wizard-delete-dialog')).toHaveTextContent('experiment')
    click('profile-wizard-delete-cancel')
    expect(screen.queryByTestId('profile-wizard-delete-dialog')).toBeNull()
    expect(deleteProfile).not.toHaveBeenCalled()
  })
})

describe('step 2 — confirmed', () => {
  it('deleted on THIS host; the list is fetched again and the row is gone', async () => {
    await askDelete(P2)
    byHost({ h1: [entry(P1, 'default', [attachment('c2', 'Air')])] })
    click('profile-wizard-delete-confirm')
    await flush()
    expect(deleteProfile).toHaveBeenCalledWith('h1', P2, { expectEndpoint: A })
    expect(listsOf('h1')).toBe(2)
    expect(screen.queryByTestId('profile-wizard-delete-dialog')).toBeNull()
    expect(screen.queryByTestId(`profile-wizard-profile-${P2}`)).toBeNull()
    expect(step()).toBe('sot')
  })

  it('409 attached: the devices the host names are said on that row, and the list is fetched again', async () => {
    vi.mocked(deleteProfile).mockResolvedValue({ kind: 'attached', attachments: [attachment('c3', 'Air'), attachment('c4', 'Studio')] })
    await askDelete(P2)
    click('profile-wizard-delete-confirm')
    await flush()
    const refused = screen.getByTestId(`profile-wizard-profile-attached-${P2}`)
    expect(refused).toHaveTextContent(en['settings.profile.sot.attached'].replace('{{names}}', 'Air, Studio'))
    expect(screen.queryByTestId('profile-wizard-delete-dialog')).toBeNull()
    expect(listsOf('h1')).toBe(2)
  })

  it('409 attached with nobody listed: said as that', async () => {
    vi.mocked(deleteProfile).mockResolvedValue({ kind: 'attached', attachments: [] })
    await askDelete(P2)
    click('profile-wizard-delete-confirm')
    await flush()
    expect(screen.getByTestId(`profile-wizard-profile-attached-${P2}`)).toHaveTextContent(en['settings.profile.sot.attached_none'])
  })

  it('failed: a sentence for the failure\'s class — never the host\'s raw text', async () => {
    vi.mocked(deleteProfile).mockResolvedValue({ kind: 'failed', reason: 'server', status: 500, message: 'RAW http://10.0.0.1/?token=SECRET' })
    await askDelete(P2)
    click('profile-wizard-delete-confirm')
    await flush()
    const status = screen.getByTestId('profile-wizard-delete-status')
    expect(status).toHaveTextContent(en['settings.profile.wizard.sot.delete_failed'])
    expect(status).toHaveTextContent(en['settings.profile.wizard.request.server'])
    expect(wizard().textContent).not.toMatch(/RAW|SECRET/)
    expect(screen.queryByTestId('profile-wizard-delete-dialog')).toBeNull()
  })

  it('thrown: a sentence, not the error', async () => {
    vi.mocked(deleteProfile).mockRejectedValue(new Error('boom SECRET'))
    await askDelete(P2)
    click('profile-wizard-delete-confirm')
    await flush()
    expect(screen.getByTestId('profile-wizard-delete-status')).toHaveTextContent(en['settings.profile.wizard.request.thrown'])
    expect(document.body.textContent).not.toMatch(/boom|SECRET/)
  })

  it('while it is in flight nothing else on the step acts: no Next, no choosing, no host change', async () => {
    let answer: (v: DeleteProfileOutcome) => void = () => {}
    vi.mocked(deleteProfile).mockReturnValue(new Promise((resolve) => { answer = resolve }))
    await askDelete(P2)
    click('profile-wizard-delete-confirm')
    expect(screen.getByTestId('profile-wizard-delete-confirm')).toBeDisabled()
    expect(screen.getByTestId('profile-wizard-next')).toBeDisabled()
    expect(screen.getByTestId(`profile-wizard-profile-${P2}`)).toBeDisabled()
    expect(screen.getByTestId('profile-wizard-profile-new')).toBeDisabled()
    expect(screen.getByTestId('profile-wizard-host')).toBeDisabled()
    await act(async () => { answer({ kind: 'deleted' }) })
    await flush()
    expect(screen.getByTestId('profile-wizard-profile-new')).toBeEnabled()
  })

  it('the CHOSEN profile deleted: the choice is dropped — even if a list still shows it', async () => {
    open()
    await flush()
    click(`profile-wizard-profile-${P2}`)
    expect(screen.getByTestId('profile-wizard-next')).toBeEnabled()
    click(`profile-wizard-profile-delete-${P2}`)
    click('profile-wizard-delete-confirm')
    await flush()
    expect(deleteProfile).toHaveBeenCalledWith('h1', P2, { expectEndpoint: A })
    expect(screen.getByTestId(`profile-wizard-profile-${P2}`)).not.toBeChecked()
    expect(screen.getByTestId('profile-wizard-next')).toBeDisabled()
  })

  it('another profile deleted: the choice stays', async () => {
    byHost({ h1: [entry(P1, 'default'), entry(P2, 'experiment')] })
    open()
    await flush()
    click(`profile-wizard-profile-${P1}`)
    click(`profile-wizard-profile-delete-${P2}`)
    click('profile-wizard-delete-confirm')
    await flush()
    expect(screen.getByTestId(`profile-wizard-profile-${P1}`)).toBeChecked()
    expect(screen.getByTestId('profile-wizard-next')).toBeEnabled()
  })
})

describe('a confirmation belongs to the host and the step it was opened on', () => {
  it('the host changes while it is open: it closes — and nothing is deleted on either host', async () => {
    await askDelete(P2)
    fireEvent.change(screen.getByTestId('profile-wizard-host'), { target: { value: 'h2' } })
    expect(screen.queryByTestId('profile-wizard-delete-dialog')).toBeNull()
    await flush()
    expect(screen.getByTestId(`profile-wizard-profile-${P9}`)).toBeInTheDocument()
    expect(screen.queryByTestId('profile-wizard-delete-dialog')).toBeNull()
    expect(deleteProfile).not.toHaveBeenCalled()
  })

  it('the wizard leaves step 2 while it is open (a master was attached elsewhere): it closes, nothing is sent', async () => {
    await askDelete(P2)
    act(() => useProfileStore.setState({ masterHostId: 'h1', masterProfileId: P1, masterEndpoint: '10.0.0.1:7860' }))
    await flush()
    expect(step()).toBe('stop')
    expect(screen.queryByTestId('profile-wizard-delete-dialog')).toBeNull()
    expect(deleteProfile).not.toHaveBeenCalled()
  })

  it('checked again when it is sent: a profile that left the list meanwhile is not deleted, and that is said', async () => {
    await askDelete(P2)
    byHost({ h1: [entry(P1, 'default', [attachment('c2', 'Air')])] })
    click('profile-wizard-profiles-refresh')
    await flush()
    expect(screen.queryByTestId(`profile-wizard-profile-${P2}`)).toBeNull()
    click('profile-wizard-delete-confirm')
    expect(deleteProfile).not.toHaveBeenCalled()
    expect(screen.queryByTestId('profile-wizard-delete-dialog')).toBeNull()
    expect(screen.getByTestId('profile-wizard-delete-status')).toHaveTextContent(en['settings.profile.sot.stale_action'])
  })
})

describe('a confirmation belongs to the ADDRESS the list was fetched from, too — the same host id may move (PR #1340 review)', () => {
  const moveH1 = () => act(() => useHostStore.setState((s) => ({ hosts: { ...s.hosts, h1: { ...s.hosts.h1, ip: '10.0.0.99' } } })))

  it('h1\'s address changes while it is open: it closes in that render, nothing is sent, and h1 is listed again at its new address', async () => {
    await askDelete(P2)
    moveH1()
    expect(screen.queryByTestId('profile-wizard-delete-dialog')).toBeNull()
    await flush()
    expect(listsOf('h1')).toBe(2)
    expect(screen.queryByTestId('profile-wizard-delete-dialog')).toBeNull()
    expect(deleteProfile).not.toHaveBeenCalled()
  })

  it('the delete is pinned to the address it was confirmed at; a move that slips in after the check → the api refuses, nothing is deleted, and that is said', async () => {
    vi.mocked(deleteProfile).mockResolvedValue({ kind: 'failed', reason: 'endpoint-changed', status: 0, message: 'host h1 is not at 10.0.0.1:7860 any more' })
    open()
    await flush()
    click(`profile-wizard-profile-${P2}`)
    click(`profile-wizard-profile-delete-${P2}`)
    click('profile-wizard-delete-confirm')
    await flush()
    expect(deleteProfile).toHaveBeenCalledWith('h1', P2, { expectEndpoint: A })
    expect(screen.getByTestId('profile-wizard-delete-status')).toHaveTextContent(en['settings.profile.sot.endpoint_changed'])
    expect(wizard().textContent).not.toMatch(/any more/)
    expect(listsOf('h1')).toBe(2)
    expect(screen.getByTestId(`profile-wizard-profile-${P2}`)).toBeChecked() // not deleted: still chosen
  })
})
