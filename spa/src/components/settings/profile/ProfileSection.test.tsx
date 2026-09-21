import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { cleanup, render, screen } from '@testing-library/react'
import en from '../../../locales/en.json'
import { ProfileSection } from './ProfileSection'
import { useLocalProfilesStore } from '../../../stores/useLocalProfilesStore'
import { useProfileStore } from '../../../stores/useProfileStore'

beforeEach(() => {
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
})
