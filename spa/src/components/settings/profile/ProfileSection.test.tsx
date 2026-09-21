import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { cleanup, render, screen } from '@testing-library/react'
import en from '../../../locales/en.json'
import { ProfileSection } from './ProfileSection'
import { useLocalProfilesStore } from '../../../stores/useLocalProfilesStore'
import { useProfileStore } from '../../../stores/useProfileStore'
import { listProfiles } from '../../../lib/profile/api'

vi.mock('../../../lib/profile/api', () => ({ listProfiles: vi.fn(), renameProfile: vi.fn(), deleteProfile: vi.fn() }))

const attach = () => useProfileStore.setState({ masterHostId: 'h1', masterProfileId: 'p1', masterEndpoint: '10.0.0.1:7860' })

beforeEach(() => {
  vi.mocked(listProfiles).mockReset()
  vi.mocked(listProfiles).mockResolvedValue({ kind: 'ok', value: [] })
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

  it('lists the profiles of this device — with nothing set up, the master alone', () => {
    render(<ProfileSection />)
    expect(screen.getByTestId('profile-local-block')).toBeInTheDocument()
    expect(screen.getAllByTestId(/^profile-row-[a-z0-9]+$/)).toHaveLength(1)
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
    expect(listProfiles).toHaveBeenCalledWith('h1')
  })
})
