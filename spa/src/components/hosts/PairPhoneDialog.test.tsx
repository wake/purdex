import { hostLookOf } from '../../lib/host-look'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { act, cleanup, fireEvent, render, screen } from '@testing-library/react'
import * as pairing from '../../lib/pairing'
import type { PairingInput, PairingSession, PairingState } from '../../lib/pairing'
import * as profileApi from '../../lib/profile/api'
import { useHostStore } from '../../stores/useHostStore'
import { useHostLookStore } from '../../stores/useHostLookStore'
import { useProfileStore } from '../../stores/useProfileStore'
import { usePendingRevocationsStore } from '../../stores/usePendingRevocationsStore'
import { PairPhoneDialog } from './PairPhoneDialog'

const T0 = 1_700_000_000_000
const QR_URL = 'purdex://pair?v=1&relay=100.64.0.2:7860&code=ABCD2345'

const idle: PairingState = { phase: 'idle', seenClaim: false, unknownOutcome: false, leftOut: [], revokeFailed: [] }
const readyResult = {
  kind: 'ok' as const,
  code: 'ABCD2345',
  expiresAt: T0 + 600_000,
  qrUrl: QR_URL,
  leftOut: [],
  mintedHostIds: ['relay', 'air'],
  pairingId: 'pid',
  deadline: T0 + 600_000,
  revokeFailed: [],
}

/** A hand-driven PairingSession: `push` publishes a state to the dialog. */
function fakeSession() {
  let state = idle
  const listeners = new Set<(s: PairingState) => void>()
  const session = {
    start: vi.fn(() => Promise.resolve()),
    close: vi.fn(() => Promise.resolve()),
    subscribe: vi.fn((l: (s: PairingState) => void) => {
      listeners.add(l)
      return () => void listeners.delete(l)
    }),
    getState: () => state,
  }
  const push = (patch: Partial<PairingState>) => {
    state = { ...state, ...patch }
    act(() => listeners.forEach((l) => l(state)))
  }
  return { session: session as unknown as PairingSession & typeof session, push }
}

let fake: ReturnType<typeof fakeSession>
let create: ReturnType<typeof vi.spyOn>

function inputOf(): PairingInput {
  return create.mock.calls[0][0] as PairingInput
}

async function renderDialog(onClose = vi.fn()) {
  const view = render(<PairPhoneDialog onClose={onClose} />)
  await screen.findByRole('option', { name: 'Home' })
  return { ...view, onClose }
}

async function clickCreate() {
  await act(async () => {
    fireEvent.click(screen.getByRole('button', { name: 'Generate code' }))
  })
}

beforeEach(() => {
  cleanup()
  vi.useFakeTimers({ toFake: ['setInterval', 'clearInterval', 'Date'] })
  vi.setSystemTime(T0)
  fake = fakeSession()
  create = vi.spyOn(pairing, 'createPairingSession').mockReturnValue(fake.session)
  vi.spyOn(profileApi, 'listProfiles').mockResolvedValue({
    kind: 'ok',
    value: [
      { id: 'p1', name: 'Home' },
      { id: 'p2', name: 'Work' },
    ] as never,
  })
  useHostLookStore.setState({ looks: {} })
  useHostStore.setState({
    hosts: {
      relay: { id: 'relay', name: 'mlab', ip: '100.64.0.2', port: 7860, order: 0, token: 'relay-tok', daemonId: 'd1_m' },
      air: { id: 'air', name: 'air26', ip: '100.64.0.4', port: 7860, order: 1, token: 'air-tok', daemonId: 'd1_a' },
      bare: { id: 'bare', name: 'tokenless', ip: '10.0.0.9', port: 7860, order: 2 },
    },
    hostOrder: ['relay', 'air', 'bare'],
    runtime: { relay: { status: 'connected' }, air: { status: 'connected' } },
    activeHostId: 'relay',
  })
  useProfileStore.setState({ masterHostId: 'air', masterProfileId: 'p2' } as never)
})

afterEach(() => {
  vi.useRealTimers()
  vi.restoreAllMocks()
})

describe('PairPhoneDialog setup', () => {
  it('lists the profiles of the master host and selects the master profile; the relay defaults to the active host', async () => {
    await renderDialog()
    expect(profileApi.listProfiles).toHaveBeenCalledWith('air')
    expect((screen.getByLabelText('Workbench') as HTMLSelectElement).value).toBe('p2')
    expect((screen.getByLabelText('Relay through') as HTMLSelectElement).value).toBe('relay')
  })

  it('without a master, the profiles come from the active host and the first one is selected', async () => {
    useProfileStore.setState({ masterHostId: null, masterProfileId: null } as never)
    await renderDialog()
    expect(profileApi.listProfiles).toHaveBeenCalledWith('relay')
    expect((screen.getByLabelText('Workbench') as HTMLSelectElement).value).toBe('p1')
  })

  it('shows the master profile as 「<name> (workbench master)」 and the others plainly', async () => {
    await renderDialog()
    expect(screen.getByRole('option', { name: 'Work (workbench master)' })).toBeTruthy()
    expect(screen.getByRole('option', { name: 'Home' })).toBeTruthy()
  })

  it('SETUP lists a host with a token but no daemon id under Left out with its reason, same as the ready view will', async () => {
    useHostStore.setState((s) => ({
      hosts: { ...s.hosts, nas: { id: 'nas', name: 'NAS', ip: '10.0.0.7', port: 7860, order: 3, token: 'nas-tok' } },
      hostOrder: [...s.hostOrder, 'nas'],
    }))
    await renderDialog()
    expect(screen.getByTestId('pair-included').textContent).not.toContain('NAS')
    const setupLeft = screen.getByTestId('pair-leftout').textContent!
    expect(setupLeft).toMatch(/NAS: its identity is not known yet/)
    expect(setupLeft).toMatch(/tokenless: no token/)
    await clickCreate()
    const sent = inputOf().hosts.map((h) => h.id)
    expect(sent).toEqual(Object.keys(useHostStore.getState().hosts)) // the session gets every host and classifies with the same helper, so its leftOut matches the setup list
    const cls = pairing.classifyHostsForPairing(Object.values(useHostStore.getState().hosts), hostLookOf)
    fake.push({ phase: 'ready', result: { ...readyResult, leftOut: cls.leftOut }, leftOut: cls.leftOut })
    const ready = screen.getByTestId('pair-leftout-ready').textContent!
    expect(ready).toMatch(/NAS: its identity is not known yet/)
    expect(ready).toMatch(/tokenless: no token/)
  })

  it('the trust note says the access lasts until revoked, in the setup and the ready view', async () => {
    const TEXT = 'This QR code is a credential: the first person to scan it within 10 minutes keeps access to your hosts until you revoke it under “Paired phones”. Scan it only with your own phone and never share a screenshot.'
    await renderDialog()
    expect(screen.getByTestId('pair-trust').textContent).toBe(TEXT)
    await clickCreate()
    fake.push({ phase: 'ready', result: readyResult })
    expect(screen.getByTestId('pair-trust').textContent).toBe(TEXT)
  })

  it('lists a host without a token as left out, and the others as included', async () => {
    await renderDialog()
    const inc = screen.getByTestId('pair-included')
    expect(inc.textContent).toContain('mlab')
    expect(inc.textContent).toContain('air26')
    expect(inc.textContent).not.toContain('tokenless')
    expect(screen.getByTestId('pair-leftout').textContent).toMatch(/tokenless.*no token/)
  })

  it('an empty profile list disables the button and says why', async () => {
    vi.spyOn(profileApi, 'listProfiles').mockResolvedValue({ kind: 'ok', value: [] })
    render(<PairPhoneDialog onClose={() => {}} />)
    await screen.findByText(/has no workbench/)
    expect((screen.getByRole('button', { name: 'Generate code' }) as HTMLButtonElement).disabled).toBe(true)
  })

  it('a failed profile listing disables the button and says why', async () => {
    vi.spyOn(profileApi, 'listProfiles').mockResolvedValue({ kind: 'failed', reason: 'network', status: 0, message: 'x' })
    render(<PairPhoneDialog onClose={() => {}} />)
    await screen.findByText(/Could not load the workbenches/)
    expect((screen.getByRole('button', { name: 'Generate code' }) as HTMLButtonElement).disabled).toBe(true)
  })

  it('Generate code starts a session with the picked profile, relay, every host (the session classifies them) and the default label', async () => {
    await renderDialog()
    fireEvent.change(screen.getByLabelText('Workbench'), { target: { value: 'p1' } })
    fireEvent.change(screen.getByLabelText('Relay through'), { target: { value: 'air' } })
    await clickCreate()
    const input = inputOf()
    expect(input.profile).toEqual({ sotHostId: 'air', profileId: 'p1', profileName: 'Home' })
    expect(input.relay.id).toBe('air')
    expect(input.hosts.map((h) => h.id)).toEqual(['relay', 'air', 'bare'])
    expect(input.label).toBe('iPhone')
    expect(fake.session.start).toHaveBeenCalledTimes(1)
  })

  it('while minting, the controls are disabled', async () => {
    await renderDialog()
    await clickCreate()
    fake.push({ phase: 'minting' })
    expect((screen.getByLabelText('Workbench') as HTMLSelectElement).disabled).toBe(true)
    expect((screen.getByLabelText('Relay through') as HTMLSelectElement).disabled).toBe(true)
    expect((screen.getByRole('button', { name: /Generate code/ }) as HTMLButtonElement).disabled).toBe(true)
  })
})

describe('PairPhoneDialog session states', () => {
  async function toReady(leftOut: PairingState['leftOut'] = [], revokeFailed: string[] = []) {
    const view = await renderDialog()
    await clickCreate()
    fake.push({ phase: 'ready', result: { ...readyResult, leftOut, revokeFailed }, leftOut, revokeFailed })
    return view
  }

  it('ready: a QR svg, the formatted code, and a countdown that ticks', async () => {
    await toReady()
    expect(screen.getByRole('img').tagName.toLowerCase()).toBe('svg')
    expect(screen.getByTestId('pair-code').textContent).toBe('ABCD-2345')
    expect(screen.getByTestId('pair-countdown').textContent).toContain('10:00')
    act(() => {
      vi.advanceTimersByTime(3000)
    })
    expect(screen.getByTestId('pair-countdown').textContent).toContain('09:57')
    expect(screen.getByTestId('pair-trust')).toBeTruthy()
  })

  it('never prints the QR url as text', async () => {
    const { container } = await toReady()
    expect(container.textContent).not.toContain('purdex://')
    expect(container.textContent).not.toContain('relay-tok')
  })

  it('explains a host that was left out', async () => {
    await toReady([{ hostId: 'air', reason: 'mint_failed' }])
    expect(screen.getByTestId('pair-leftout-ready').textContent).toMatch(/air26.*could not issue/)
  })

  it('claimed: shows Paired instead of the QR; Close calls session.close once', async () => {
    const { onClose } = await toReady()
    fake.push({ phase: 'claimed', seenClaim: true })
    expect(screen.getByText('Paired')).toBeTruthy()
    expect(screen.queryByRole('img')).toBeNull()
    fireEvent.click(screen.getAllByRole('button', { name: 'Close' })[1])
    expect(fake.session.close).toHaveBeenCalledTimes(1)
    expect(onClose).toHaveBeenCalledTimes(1)
  })

  it('gone with an unknown outcome shows the pointer to the paired-phones list', async () => {
    await toReady()
    fake.push({ phase: 'gone', unknownOutcome: true })
    expect(screen.getByTestId('pair-ended').textContent).toBe(
      'This code is no longer valid. If the phone finished pairing, it appears in "Paired phones".',
    )
  })

  it('expired and closed show their own text', async () => {
    await toReady()
    fake.push({ phase: 'expired' })
    expect(screen.getByTestId('pair-ended').textContent).toMatch(/expired/)
    fake.push({ phase: 'closed' })
    expect(screen.getByTestId('pair-ended').textContent).toMatch(/closed/)
  })

  it.each([
    ['sot_failed', /workbench.*host/i],
    ['too_late', /too long/],
    ['capacity', /too many/],
    ['unavailable', /not available/],
    ['unauthorized', /not allowed/],
    ['no_token', /no token/],
    ['network', /Could not reach/],
    ['timeout', /did not answer/],
  ])('failed %s is said in words', async (reason, re) => {
    await renderDialog()
    await clickCreate()
    fake.push({ phase: 'failed', failure: { kind: 'failed', reason: reason as never, revokeFailed: [] } })
    expect(screen.getByRole('alert').textContent).toMatch(re)
  })

  it('revokeFailed says some hosts could not be reached to revoke', async () => {
    await renderDialog()
    await clickCreate()
    fake.push({ phase: 'failed', failure: { kind: 'failed', reason: 'network', revokeFailed: ['air'] }, revokeFailed: ['air'] })
    expect(screen.getByTestId('pair-revoke-failed').textContent).toMatch(/could not be reached.*Paired phones/)
  })
})

describe('PairPhoneDialog closing', () => {
  async function ready() {
    const view = await renderDialog()
    await clickCreate()
    fake.push({ phase: 'ready', result: readyResult })
    return view
  }

  it('X, Escape and backdrop each close the session exactly once', async () => {
    const x = await ready()
    fireEvent.click(screen.getAllByRole('button', { name: 'Close' })[0])
    expect(fake.session.close).toHaveBeenCalledTimes(1)
    expect(x.onClose).toHaveBeenCalledTimes(1)
    x.unmount()
    expect(fake.session.close).toHaveBeenCalledTimes(1)

    cleanup()
    fake = fakeSession()
    create.mockReturnValue(fake.session)
    const e = await ready()
    fireEvent.keyDown(document, { key: 'Escape' })
    expect(fake.session.close).toHaveBeenCalledTimes(1)
    expect(e.onClose).toHaveBeenCalledTimes(1)

    cleanup()
    fake = fakeSession()
    create.mockReturnValue(fake.session)
    const b = await ready()
    fireEvent.click(screen.getByRole('dialog'))
    expect(fake.session.close).toHaveBeenCalledTimes(1)
    expect(b.onClose).toHaveBeenCalledTimes(1)
  })

  it('closing before any session exists just calls onClose', async () => {
    const { onClose } = await renderDialog()
    fireEvent.click(screen.getByRole('button', { name: 'Cancel' }))
    expect(create).not.toHaveBeenCalled()
    expect(onClose).toHaveBeenCalledTimes(1)
  })

  it('unmounting closes the session; no timers are left behind', async () => {
    const { unmount } = await ready()
    expect(vi.getTimerCount()).toBeGreaterThan(0)
    unmount()
    expect(fake.session.close).toHaveBeenCalledTimes(1)
    expect(vi.getTimerCount()).toBe(0)
  })
})

describe('PairPhoneDialog pending revocations', () => {
  beforeEach(() => usePendingRevocationsStore.setState({ items: [] }))

  it('records every revokeFailed host with the pairing id as a pending revocation', async () => {
    await renderDialog()
    await clickCreate()
    fake.push({ phase: 'ready', result: readyResult, revokeFailed: ['air'], pairingId: 'pid' })
    expect(usePendingRevocationsStore.getState().items).toMatchObject([{ hostId: 'air', pairingId: 'pid', label: 'iPhone' }])
    expect(usePendingRevocationsStore.getState().items).toHaveLength(1)
  })

  it('records the hosts of a failed session too (the id comes with the state)', async () => {
    await renderDialog()
    await clickCreate()
    fake.push({ phase: 'failed', failure: { kind: 'failed', reason: 'capacity', revokeFailed: ['air'] }, revokeFailed: ['air'], pairingId: 'pid2' })
    expect(usePendingRevocationsStore.getState().has('air', 'pid2')).toBe(true)
  })

  it('still records a revoke that fails after the dialog was closed', async () => {
    const { onClose } = await renderDialog()
    await clickCreate()
    fake.push({ phase: 'ready', result: readyResult })
    fireEvent.click(screen.getAllByRole('button', { name: 'Close' }).at(-1)!)
    expect(onClose).toHaveBeenCalled()
    fake.push({ phase: 'closed', revokeFailed: ['relay'], pairingId: 'pid' })
    expect(usePendingRevocationsStore.getState().has('relay', 'pid')).toBe(true)
  })

  it('records nothing when no host failed, or the id is unknown', async () => {
    await renderDialog()
    await clickCreate()
    fake.push({ phase: 'ready', result: readyResult, revokeFailed: [] })
    fake.push({ revokeFailed: ['air'] })
    expect(usePendingRevocationsStore.getState().items).toEqual([])
  })
})
