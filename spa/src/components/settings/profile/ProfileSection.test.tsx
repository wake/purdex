import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { cleanup, render, screen } from '@testing-library/react'
import en from '../../../locales/en.json'
import { ProfileSection } from './ProfileSection'
import { useLocalProfilesStore } from '../../../stores/useLocalProfilesStore'
import { useProfileStore } from '../../../stores/useProfileStore'
import { useHostStore } from '../../../stores/useHostStore'
import { listProfiles } from '../../../lib/profile/api'
import { useProfileSync } from '../../../hooks/useProfileSync'
import type { ProfileSyncSnapshot } from '../../../lib/profile/start'

vi.mock('../../../hooks/useProfileSync', () => ({ useProfileSync: vi.fn() }))
vi.mock('../../../lib/profile/start', () => ({ requestSyncNow: vi.fn(), detachMaster: vi.fn(), retryPendingDetach: vi.fn() }))
vi.mock('../../../lib/profile/api', () => ({ listProfiles: vi.fn(), renameProfile: vi.fn(), deleteProfile: vi.fn() }))

const NO_MASTER: ProfileSyncSnapshot = { master: null, leader: false, blocked: null, status: null, problems: [], remote: false, stale: false }
const attach = () => {
  useProfileStore.setState({ masterHostId: 'h1', masterProfileId: 'p1', masterEndpoint: '10.0.0.1:7860' })
  useHostStore.setState({ hosts: { h1: { id: 'h1', name: 'mlab', ip: '10.0.0.1', port: 7860, order: 0 } }, hostOrder: ['h1'] })
  vi.mocked(useProfileSync).mockReturnValue({ ...NO_MASTER, master: { hostId: 'h1', profileId: 'p1' }, leader: true, status: { profile: 'synced', schemaLock: null, sections: {}, locks: {}, profileGone: false, detail: {}, indexFailures: 0, lastSuccessAt: null } })
}

beforeEach(() => {
  vi.mocked(useProfileSync).mockReturnValue(NO_MASTER)
  vi.mocked(listProfiles).mockReset()
  vi.mocked(listProfiles).mockResolvedValue({ kind: 'ok', value: [] })
  useHostStore.setState({ hosts: {}, hostOrder: [] })
  useLocalProfilesStore.setState({ slaves: {}, slaveOrder: [], activeProfileId: 'master', parkedMaster: null, worldEpoch: 0, relabelCount: 0, master: { name: null } })
  useProfileStore.setState({ masterHostId: null, masterProfileId: null, masterEndpoint: null, pendingDirection: null, suspension: null })
})

afterEach(() => {
  cleanup()
  vi.restoreAllMocks()
})

describe('Settings › Profile', () => {
  it('has the section title and says what a profile is', () => {
    render(<ProfileSection />)
    expect(screen.getByTestId('profile-section')).toBeInTheDocument()
    expect(screen.getByRole('heading', { level: 2 })).toHaveTextContent(en['settings.section.profile'])
    expect(screen.getByText(en['settings.profile.description'])).toBeInTheDocument()
  })

  it('opens with this computer\'s name, before any block', () => {
    render(<ProfileSection />)
    const name = screen.getByRole('textbox', { name: en['settings.profile.device_name_aria'] })
    expect(screen.getByText(en['settings.profile.device_name'])).toBeInTheDocument()
    expect(name.compareDocumentPosition(screen.getByTestId('profile-current-block')) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy()
  })

  it('lists the profiles of this device — with nothing set up, the master alone', () => {
    render(<ProfileSection />)
    expect(screen.getByTestId('profile-local-block')).toBeInTheDocument()
    expect(screen.getAllByTestId(/^profile-row-[a-z0-9]+$/)).toHaveLength(1)
  })

  it('the order of the blocks: the sync state, this device\'s profiles, the host\'s', async () => {
    attach()
    render(<ProfileSection />)
    await screen.findByTestId('profile-sot-block')
    const blocks = Array.from(screen.getByTestId('profile-section').querySelectorAll('section')).map((el) => el.getAttribute('data-testid'))
    expect(blocks).toEqual(['profile-current-block', 'profile-local-block', 'profile-sot-block'])
  })

  it('no master: the Current block explains, and that is all it does', () => {
    render(<ProfileSection />)
    expect(screen.getByTestId('profile-current-block')).toHaveAttribute('data-state', 'none')
  })

  it('the master\'s name on the host reaches the Current block once the host has answered', async () => {
    attach()
    vi.mocked(listProfiles).mockResolvedValue({ kind: 'ok', value: [{ id: 'p1', name: 'default', createdAt: 1, updatedAt: 2, sections: [], attachments: [] }] })
    render(<ProfileSection />)
    expect(screen.getByTestId('profile-current-master')).toHaveTextContent('p1')
    await screen.findByTestId('profile-sot-name-p1')
    expect(screen.getByTestId('profile-current-master')).toHaveTextContent('default')
  })

  it('no master: the host is not asked for anything, and its profiles are not a block', () => {
    render(<ProfileSection />)
    expect(screen.queryByTestId('profile-sot-block')).toBeNull()
    expect(listProfiles).not.toHaveBeenCalled()
  })

  it('THE IRON RULE: opening the page writes no storage and starts no timer', () => {
    vi.useFakeTimers()
    try {
      const setItem = vi.spyOn(Storage.prototype, 'setItem')
      const removeItem = vi.spyOn(Storage.prototype, 'removeItem')
      render(<ProfileSection />)
      expect(setItem).not.toHaveBeenCalled()
      expect(removeItem).not.toHaveBeenCalled()
      expect(vi.getTimerCount()).toBe(0)
    } finally {
      vi.useRealTimers()
    }
  })

  it('a master attached: the profiles of ITS host', async () => {
    attach()
    render(<ProfileSection />)
    expect(await screen.findByTestId('profile-sot-block')).toBeInTheDocument()
    expect(listProfiles).toHaveBeenCalledWith('h1', { expectEndpoint: '10.0.0.1:7860' })
  })
})
