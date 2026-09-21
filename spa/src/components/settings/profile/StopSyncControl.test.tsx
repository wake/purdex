import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { act, cleanup, fireEvent, render, screen } from '@testing-library/react'
import en from '../../../locales/en.json'
import { StopSyncControl } from './StopSyncControl'
import { useProfileStore } from '../../../stores/useProfileStore'
import { useHostStore } from '../../../stores/useHostStore'
import { detachMaster, retryPendingDetach } from '../../../lib/profile/start'
import type { DetachResult } from '../../../lib/profile/start'

vi.mock('../../../lib/profile/start', () => ({ detachMaster: vi.fn(), retryPendingDetach: vi.fn() }))

const P1 = 'p_000000000001'
const NOT_TOLD: DetachResult = { ok: false, reason: 'daemon-not-told', detail: 'timeout: no answer' }
const LEFT = { hostId: 'h1', profileId: P1, detail: 'timeout: no answer', at: 1 }

/** What start.ts does: the master goes at once, the answer comes later — and a failure is written down. */
function detachAnswers(): (r: DetachResult) => Promise<void> {
  let release: (r: DetachResult) => void = () => {}
  vi.mocked(detachMaster).mockImplementation(() => {
    useProfileStore.getState().clearMaster()
    return new Promise<DetachResult>((resolve) => { release = resolve })
  })
  return (r) => act(async () => {
    if (!r.ok) useProfileStore.getState().setPendingDetach(LEFT)
    release(r)
  })
}

/** As CurrentBlock mounts it: `attached` follows the store. */
function Mounted() {
  const attached = useProfileStore((s) => s.masterHostId !== null)
  return <StopSyncControl attached={attached} />
}

beforeEach(() => {
  vi.mocked(detachMaster).mockReset()
  vi.mocked(retryPendingDetach).mockReset()
  useProfileStore.setState({ masterHostId: 'h1', masterProfileId: P1, masterEndpoint: '10.0.0.1:7860', pendingDirection: null, suspension: null, pendingDetach: null })
  useHostStore.setState({ hosts: { h1: { id: 'h1', name: 'mlab', ip: '10.0.0.1', port: 7860, order: 0 } }, hostOrder: ['h1'] })
})

afterEach(cleanup)

describe('Stop sync', () => {
  it('asks first, saying what stays; Cancel stops nothing', () => {
    render(<Mounted />)
    fireEvent.click(screen.getByTestId('profile-stop-sync'))
    expect(detachMaster).not.toHaveBeenCalled()
    expect(screen.getByTestId('profile-stop-sync-dialog')).toHaveTextContent(en['settings.profile.current.stop_body'])
    fireEvent.click(screen.getByTestId('profile-stop-sync-cancel'))
    expect(screen.queryByTestId('profile-stop-sync-dialog')).toBeNull()
    expect(detachMaster).not.toHaveBeenCalled()
  })

  it('Confirm WAITS for the answer: busy meanwhile — though the master is gone at once — then closes; told → nothing more to say', async () => {
    const answer = detachAnswers()
    render(<Mounted />)
    fireEvent.click(screen.getByTestId('profile-stop-sync'))
    fireEvent.click(screen.getByTestId('profile-stop-sync-confirm'))
    expect(detachMaster).toHaveBeenCalledTimes(1)
    expect(useProfileStore.getState().masterHostId).toBeNull()
    expect(screen.getByTestId('profile-stop-sync-dialog')).toBeInTheDocument()
    expect(screen.getByTestId('profile-stop-sync-confirm')).toBeDisabled()
    expect(screen.getByTestId('profile-stop-sync-cancel')).toBeDisabled()
    await answer({ ok: true })
    expect(screen.queryByTestId('profile-stop-sync-dialog')).toBeNull()
    expect(screen.queryByTestId('profile-detach-leftover')).toBeNull()
    expect(screen.queryByTestId('profile-stop-sync')).toBeNull() // nothing to stop any more
  })

  it('the daemon was NOT told: said in words — stopped here, the host may still list this device, nobody can delete that profile', async () => {
    const answer = detachAnswers()
    render(<Mounted />)
    fireEvent.click(screen.getByTestId('profile-stop-sync'))
    fireEvent.click(screen.getByTestId('profile-stop-sync-confirm'))
    await answer(NOT_TOLD)
    expect(screen.queryByTestId('profile-stop-sync-dialog')).toBeNull()
    const notice = screen.getByTestId('profile-detach-leftover')
    expect(notice).toHaveAttribute('data-host', 'h1')
    expect(notice).toHaveAttribute('data-profile', P1)
    expect(notice).toHaveTextContent('mlab')
    expect(notice).toHaveTextContent('timeout: no answer')
    expect(notice).toHaveTextContent(en['settings.profile.detach.consequence'])
    expect(useProfileStore.getState().masterHostId).toBeNull()
  })

  it('a detach that throws still lets go of the dialog', async () => {
    vi.mocked(detachMaster).mockRejectedValue(new Error('boom'))
    render(<Mounted />)
    fireEvent.click(screen.getByTestId('profile-stop-sync'))
    await act(async () => { fireEvent.click(screen.getByTestId('profile-stop-sync-confirm')) })
    expect(screen.queryByTestId('profile-stop-sync-dialog')).toBeNull()
  })
})

describe('an attachment left on the daemon', () => {
  const leftBehind = () => useProfileStore.setState({ masterHostId: null, masterProfileId: null, masterEndpoint: null, pendingDetach: LEFT })

  it('is still said after the page is opened again — it is the store\'s, not the dialog\'s', () => {
    leftBehind()
    render(<Mounted />)
    expect(screen.getByTestId('profile-detach-leftover')).toBeInTheDocument()
    cleanup()
    render(<Mounted />)
    expect(screen.getByTestId('profile-detach-leftover')).toHaveTextContent('timeout: no answer')
  })

  it('Try again that gets through: the notice goes', async () => {
    leftBehind()
    vi.mocked(retryPendingDetach).mockImplementation(async () => {
      useProfileStore.getState().clearPendingDetach('h1', P1)
      return { ok: true }
    })
    render(<Mounted />)
    await act(async () => { fireEvent.click(screen.getByTestId('profile-detach-retry')) })
    expect(retryPendingDetach).toHaveBeenCalledTimes(1)
    expect(screen.queryByTestId('profile-detach-leftover')).toBeNull()
  })

  it('Try again that does not: busy meanwhile, then says it tried, with the newer reason; still there', async () => {
    leftBehind()
    let release: (r: DetachResult) => void = () => {}
    vi.mocked(retryPendingDetach).mockReturnValue(new Promise((resolve) => { release = resolve }))
    render(<Mounted />)
    expect(screen.queryByTestId('profile-detach-retry-failed')).toBeNull()
    fireEvent.click(screen.getByTestId('profile-detach-retry'))
    expect(screen.getByTestId('profile-detach-retry')).toBeDisabled()
    expect(screen.getByTestId('profile-detach-retry')).toHaveAttribute('aria-busy', 'true')
    expect(screen.getByTestId('profile-detach-dismiss')).toBeDisabled()
    fireEvent.click(screen.getByTestId('profile-detach-retry'))
    expect(retryPendingDetach).toHaveBeenCalledTimes(1)
    await act(async () => {
      useProfileStore.getState().setPendingDetach({ ...LEFT, detail: 'network: refused', at: 2 })
      release({ ok: false, reason: 'daemon-not-told', detail: 'network: refused' })
    })
    expect(screen.getByTestId('profile-detach-retry-failed')).toHaveTextContent(en['settings.profile.detach.retry_failed'])
    expect(screen.getByTestId('profile-detach-leftover')).toHaveTextContent('network: refused')
    expect(screen.getByTestId('profile-detach-retry')).toBeEnabled()
  })

  it('Dismiss: the user gives up, the record goes', () => {
    leftBehind()
    render(<Mounted />)
    fireEvent.click(screen.getByTestId('profile-detach-dismiss'))
    expect(useProfileStore.getState().pendingDetach).toBeNull()
    expect(screen.queryByTestId('profile-detach-leftover')).toBeNull()
    expect(retryPendingDetach).not.toHaveBeenCalled()
  })

  it('a host that is no longer in the app: named by its id', () => {
    leftBehind()
    useHostStore.setState({ hosts: {}, hostOrder: [] })
    render(<Mounted />)
    expect(screen.getByTestId('profile-detach-leftover')).toHaveTextContent('h1')
  })

  it('is said while attached to ANOTHER profile too', () => {
    useProfileStore.setState({ masterProfileId: 'p_000000000002', pendingDetach: LEFT })
    render(<Mounted />)
    expect(screen.getByTestId('profile-detach-leftover')).toBeInTheDocument()
    expect(screen.getByTestId('profile-stop-sync')).toBeInTheDocument()
  })

  it('nothing left, no master: renders nothing at all', () => {
    useProfileStore.setState({ masterHostId: null, masterProfileId: null, masterEndpoint: null })
    const { container } = render(<Mounted />)
    expect(container).toBeEmptyDOMElement()
  })
})
