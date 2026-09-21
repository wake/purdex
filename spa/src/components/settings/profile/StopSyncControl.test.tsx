import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { act, cleanup, fireEvent, render, screen, within } from '@testing-library/react'
import en from '../../../locales/en.json'
import { StopSyncControl } from './StopSyncControl'
import { pendingDetachTestId } from './profile-rules'
import { pendingDetachKey, useProfileStore } from '../../../stores/useProfileStore'
import { useHostStore } from '../../../stores/useHostStore'
import { detachMaster, retryPendingDetach } from '../../../lib/profile/start'
import type { DetachResult } from '../../../lib/profile/start'

vi.mock('../../../lib/profile/start', () => ({ detachMaster: vi.fn(), retryPendingDetach: vi.fn() }))

const P1 = 'p_000000000001'
const EP = '10.0.0.1:7860'
const NOT_TOLD: DetachResult = { ok: false, reason: 'daemon-not-told', detail: 'timeout' }
const LEFT = { hostId: 'h1', profileId: P1, endpoint: EP, detail: 'timeout', at: 1 }

/** What start.ts does: the master goes at once, the answer comes later — and a failure is written down. */
function detachAnswers(): (r: DetachResult) => Promise<void> {
  let release: (r: DetachResult) => void = () => {}
  vi.mocked(detachMaster).mockImplementation(() => {
    useProfileStore.getState().clearMaster()
    return new Promise<DetachResult>((resolve) => { release = resolve })
  })
  return (r) => act(async () => {
    if (!r.ok) useProfileStore.getState().addPendingDetach(LEFT)
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
  useProfileStore.setState({ masterHostId: 'h1', masterProfileId: P1, masterEndpoint: '10.0.0.1:7860', pendingDirection: null, suspension: null, pendingDetaches: [] })
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
    expect(notice).toHaveTextContent('timeout')
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
  const leftBehind = () => useProfileStore.setState({ masterHostId: null, masterProfileId: null, masterEndpoint: null, pendingDetaches: [LEFT] })

  it('is still said after the page is opened again — it is the store\'s, not the dialog\'s', () => {
    leftBehind()
    render(<Mounted />)
    expect(screen.getByTestId('profile-detach-leftover')).toBeInTheDocument()
    cleanup()
    render(<Mounted />)
    expect(screen.getByTestId('profile-detach-leftover')).toHaveTextContent('timeout')
  })

  it('Try again that gets through: the notice goes', async () => {
    leftBehind()
    vi.mocked(retryPendingDetach).mockImplementation(async () => {
      useProfileStore.getState().clearPendingDetach(pendingDetachKey(LEFT))
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
      useProfileStore.getState().addPendingDetach({ ...LEFT, detail: 'network', at: 2 })
      release({ ok: false, reason: 'daemon-not-told', detail: 'network' })
    })
    expect(screen.getByTestId('profile-detach-retry-failed')).toHaveTextContent(en['settings.profile.detach.retry_failed'])
    expect(screen.getByTestId('profile-detach-leftover')).toHaveTextContent('network')
    expect(screen.getByTestId('profile-detach-retry')).toBeEnabled()
  })

  it('Dismiss: the user gives up, the record goes', () => {
    leftBehind()
    render(<Mounted />)
    fireEvent.click(screen.getByTestId('profile-detach-dismiss'))
    expect(useProfileStore.getState().pendingDetaches).toEqual([])
    expect(screen.queryByTestId('profile-detach-leftover')).toBeNull()
    expect(retryPendingDetach).not.toHaveBeenCalled()
  })

  describe('the attachment is on the daemon the master was attached at — not wherever the host entry points today (review F4)', () => {
    const moveHost = (ip: string) => act(() => useHostStore.setState((s) => ({ hosts: { ...s.hosts, h1: { ...s.hosts.h1, ip } } })))

    it('reachable at the same address: an ordinary retry', () => {
      leftBehind()
      render(<Mounted />)
      expect(screen.getByTestId('profile-detach-leftover')).toHaveAttribute('data-state', 'retryable')
      expect(screen.getByTestId('profile-detach-retry')).toBeEnabled()
    })

    it('the host was re-pointed: BOTH addresses are shown, what to do is said, and there is nothing to press but Dismiss', () => {
      leftBehind()
      render(<Mounted />)
      moveHost('10.0.0.9')
      const notice = screen.getByTestId('profile-detach-leftover')
      expect(notice).toHaveAttribute('data-state', 'endpoint-changed')
      expect(notice).toHaveTextContent('10.0.0.1:7860')
      expect(notice).toHaveTextContent('10.0.0.9:7860')
      expect(notice).toHaveTextContent(en['settings.profile.detach.endpoint_changed_how'])
      expect(screen.queryByTestId('profile-detach-retry')).toBeNull()
      expect(screen.getByTestId('profile-detach-dismiss')).toBeEnabled()
      expect(retryPendingDetach).not.toHaveBeenCalled()
    })

    it('… pointed back: the retry is there again', () => {
      leftBehind()
      render(<Mounted />)
      moveHost('10.0.0.9')
      moveHost('10.0.0.1')
      expect(screen.getByTestId('profile-detach-leftover')).toHaveAttribute('data-state', 'retryable')
      expect(screen.getByTestId('profile-detach-retry')).toBeEnabled()
    })

    it('the host is not in the app any more: its own sentence (with the address it had), Dismiss only', () => {
      leftBehind()
      useHostStore.setState({ hosts: {}, hostOrder: [] })
      render(<Mounted />)
      const notice = screen.getByTestId('profile-detach-leftover')
      expect(notice).toHaveAttribute('data-state', 'host-gone')
      expect(notice).toHaveTextContent('10.0.0.1:7860')
      expect(notice).toHaveTextContent(en['settings.profile.detach.host_gone_how'])
      expect(screen.queryByTestId('profile-detach-retry')).toBeNull()
      expect(screen.getByTestId('profile-detach-dismiss')).toBeEnabled()
    })

    it('a record from before the address was written down: never sent on a guess — said, Dismiss only', () => {
      useProfileStore.setState({ masterHostId: null, masterProfileId: null, masterEndpoint: null, pendingDetaches: [{ ...LEFT, endpoint: null }] })
      render(<Mounted />)
      const notice = screen.getByTestId('profile-detach-leftover')
      expect(notice).toHaveAttribute('data-state', 'endpoint-unknown')
      expect(notice).toHaveTextContent(en['settings.profile.detach.endpoint_unknown'])
      expect(screen.queryByTestId('profile-detach-retry')).toBeNull()
    })

    it('a retry that start.ts refused to send (the address moved in the same instant) is not reported as "tried again"', async () => {
      leftBehind()
      vi.mocked(retryPendingDetach).mockResolvedValue({ ok: false, reason: 'endpoint-changed' })
      render(<Mounted />)
      await act(async () => { fireEvent.click(screen.getByTestId('profile-detach-retry')) })
      expect(screen.queryByTestId('profile-detach-retry-failed')).toBeNull()
    })
  })

  it('what is shown of the failure is the short reason the store keeps — never a transcript', () => {
    const transcript = `server ${'x'.repeat(200)} Authorization: Bearer xyz https://10.0.0.1:7860/api/profiles`
    useProfileStore.setState({ masterHostId: null, masterProfileId: null, masterEndpoint: null })
    useProfileStore.getState().addPendingDetach({ ...LEFT, detail: transcript })
    render(<Mounted />)
    expect(screen.getByTestId('profile-detach-leftover')).not.toHaveTextContent('Bearer')
    expect(screen.getByTestId('profile-detach-leftover')).not.toHaveTextContent('https://')
  })

  it('is said while attached to ANOTHER profile too', () => {
    useProfileStore.setState({ masterProfileId: 'p_000000000002', pendingDetaches: [LEFT] })
    render(<Mounted />)
    expect(screen.getByTestId('profile-detach-leftover')).toBeInTheDocument()
    expect(screen.getByTestId('profile-stop-sync')).toBeInTheDocument()
  })

  describe('several at once (review F3): one notice per attachment, each with its own buttons', () => {
    const OTHER = { ...LEFT, profileId: 'p_000000000009', detail: 'server (HTTP 502)', at: 2 }
    const item = (left: typeof LEFT) => screen.getByTestId(`profile-detach-item-${pendingDetachTestId(left)}`)
    const both = () => useProfileStore.setState({ masterHostId: null, masterProfileId: null, masterEndpoint: null, pendingDetaches: [LEFT, OTHER] })

    it('every record is said, under a test id made of its key — letters, digits, dot, dash and underscore only', () => {
      both()
      render(<Mounted />)
      expect(screen.getAllByTestId('profile-detach-leftover')).toHaveLength(2)
      expect(pendingDetachTestId(LEFT)).toMatch(/^[A-Za-z0-9._-]+$/)
      expect(pendingDetachTestId(LEFT)).not.toBe(pendingDetachTestId(OTHER))
      expect(within(item(LEFT)).getByTestId('profile-detach-leftover')).toHaveAttribute('data-profile', LEFT.profileId)
      expect(within(item(OTHER)).getByTestId('profile-detach-leftover')).toHaveTextContent('server (HTTP 502)')
    })

    it('Try again asks about THAT record, and only it is busy', async () => {
      both()
      let release: (r: DetachResult) => void = () => {}
      vi.mocked(retryPendingDetach).mockReturnValue(new Promise((resolve) => { release = resolve }))
      render(<Mounted />)
      fireEvent.click(within(item(OTHER)).getByTestId('profile-detach-retry'))
      expect(retryPendingDetach).toHaveBeenCalledWith(pendingDetachKey(OTHER))
      expect(within(item(OTHER)).getByTestId('profile-detach-retry')).toHaveAttribute('aria-busy', 'true')
      expect(within(item(LEFT)).getByTestId('profile-detach-retry')).toBeEnabled()
      await act(async () => { release({ ok: false, reason: 'daemon-not-told', detail: 'timeout' }) })
      expect(within(item(OTHER)).getByTestId('profile-detach-retry-failed')).toBeInTheDocument()
      expect(within(item(LEFT)).queryByTestId('profile-detach-retry-failed')).toBeNull()
    })

    it('Dismiss removes THAT record; the other stays', () => {
      both()
      render(<Mounted />)
      fireEvent.click(within(item(LEFT)).getByTestId('profile-detach-dismiss'))
      expect(useProfileStore.getState().pendingDetaches).toEqual([OTHER])
      expect(screen.getAllByTestId('profile-detach-leftover')).toHaveLength(1)
    })
  })

  it('nothing left, no master: renders nothing at all', () => {
    useProfileStore.setState({ masterHostId: null, masterProfileId: null, masterEndpoint: null })
    const { container } = render(<Mounted />)
    expect(container).toBeEmptyDOMElement()
  })
})
