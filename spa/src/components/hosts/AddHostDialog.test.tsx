import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent, waitFor } from '@testing-library/react'
import { AddHostDialog } from './AddHostDialog'
import { useHostStore } from '../../stores/useHostStore'
import * as hostApi from '../../lib/host-api'
import * as pairingCodec from '../../lib/pairing-codec'

beforeEach(() => {
  vi.restoreAllMocks()
  // Default: the post-confirm /api/info is unreachable (never a real network call).
  vi.spyOn(hostApi, 'fetchInfoAt').mockRejectedValue(new Error('unreachable'))
  useHostStore.setState({
    hosts: {},
    hostOrder: [],
    runtime: {},
  })
})

describe('AddHostDialog', () => {
  it('renders pairing code input and pair button', () => {
    render(<AddHostDialog onClose={vi.fn()} />)
    expect(screen.getByPlaceholderText('XXXX-XXXX-XXXXX')).toBeInTheDocument()
    expect(screen.getByText('Pair')).toBeInTheDocument()
  })

  it('Pair button disabled when pairing code is too short', () => {
    render(<AddHostDialog onClose={vi.fn()} />)
    const btn = screen.getByText('Pair').closest('button')!
    expect(btn).toBeDisabled()
  })

  it('Confirm button disabled initially', () => {
    render(<AddHostDialog onClose={vi.fn()} />)
    const btn = screen.getByText('Confirm').closest('button')!
    expect(btn).toBeDisabled()
  })

  it('Cancel button calls onClose', () => {
    const onClose = vi.fn()
    render(<AddHostDialog onClose={onClose} />)
    fireEvent.click(screen.getByText('Cancel'))
    expect(onClose).toHaveBeenCalled()
  })

  it('Escape key calls onClose', () => {
    const onClose = vi.fn()
    render(<AddHostDialog onClose={onClose} />)
    fireEvent.keyDown(document, { key: 'Escape' })
    expect(onClose).toHaveBeenCalled()
  })

  it('clicking backdrop calls onClose', () => {
    const onClose = vi.fn()
    render(<AddHostDialog onClose={onClose} />)
    // The outer div is the backdrop
    fireEvent.click(screen.getByRole('dialog'))
    expect(onClose).toHaveBeenCalled()
  })

  it('successful pairing shows success message and enables Confirm', async () => {
    vi.spyOn(hostApi, 'fetchPairVerify').mockResolvedValue({ setupSecret: 'secret123' })
    vi.spyOn(pairingCodec, 'generatePurdexToken').mockReturnValue('purdex_' + 'a'.repeat(40))
    vi.spyOn(pairingCodec, 'decodePairingCode').mockReturnValue({
      ip: '10.0.0.1',
      port: 7860,
      secret: 'abc123',
    })
    vi.spyOn(pairingCodec, 'cleanPairingInput').mockReturnValue('ABCDEFGHIJKLM')

    render(<AddHostDialog onClose={vi.fn()} />)
    fireEvent.change(screen.getByPlaceholderText('XXXX-XXXX-XXXXX'), {
      target: { value: 'ABCD-EFGH-IJKLM' },
    })
    fireEvent.click(screen.getByText('Pair'))

    await waitFor(() => {
      expect(screen.getByText('Paired successfully')).toBeInTheDocument()
    })

    const confirmBtn = screen.getByText('Confirm').closest('button')!
    expect(confirmBtn).not.toBeDisabled()
  })

  it('failed pairing shows error and resets to idle', async () => {
    vi.spyOn(hostApi, 'fetchPairVerify').mockRejectedValue(new hostApi.PairingError(403, 'forbidden'))
    vi.spyOn(pairingCodec, 'decodePairingCode').mockReturnValue({
      ip: '10.0.0.1',
      port: 7860,
      secret: 'abc123',
    })
    vi.spyOn(pairingCodec, 'cleanPairingInput').mockReturnValue('ABCDEFGHIJKLM')

    render(<AddHostDialog onClose={vi.fn()} />)
    fireEvent.change(screen.getByPlaceholderText('XXXX-XXXX-XXXXX'), {
      target: { value: 'ABCD-EFGH-IJKLM' },
    })
    fireEvent.click(screen.getByText('Pair'))

    await waitFor(() => {
      expect(screen.getByText(/Pairing failed: HTTP 403/)).toBeInTheDocument()
    })
  })

  it('invalid pairing code shows error immediately', () => {
    vi.spyOn(pairingCodec, 'decodePairingCode').mockReturnValue(null)
    vi.spyOn(pairingCodec, 'cleanPairingInput').mockReturnValue('ABCDEFGHIJKLM')

    render(<AddHostDialog onClose={vi.fn()} />)
    fireEvent.change(screen.getByPlaceholderText('XXXX-XXXX-XXXXX'), {
      target: { value: 'ABCD-EFGH-IJKLM' },
    })
    fireEvent.click(screen.getByText('Pair'))

    expect(screen.getByText('Invalid pairing code')).toBeInTheDocument()
  })

  it('Use Token checkbox enables host/port/token fields', () => {
    render(<AddHostDialog onClose={vi.fn()} />)
    const checkbox = screen.getByRole('checkbox')
    fireEvent.click(checkbox)

    const ipInput = screen.getByPlaceholderText('100.64.0.1')
    expect(ipInput).not.toBeDisabled()
    const portInput = screen.getByPlaceholderText('7860')
    expect(portInput).not.toBeDisabled()
  })

  it('confirms with token route: calls fetchTokenAuth + addHost + onClose', async () => {
    vi.spyOn(hostApi, 'fetchTokenAuth').mockResolvedValue({ ok: true })
    const onClose = vi.fn()

    render(<AddHostDialog onClose={onClose} />)

    // Switch to token route
    fireEvent.click(screen.getByRole('checkbox'))

    // Fill IP and token
    fireEvent.change(screen.getByPlaceholderText('100.64.0.1'), { target: { value: '10.0.0.1' } })
    const tokenInput = screen.getByPlaceholderText('purdex_...')
    fireEvent.change(tokenInput, { target: { value: 'purdex_' + 'a'.repeat(40) } })

    fireEvent.click(screen.getByText('Confirm'))

    await waitFor(() => {
      expect(onClose).toHaveBeenCalled()
    })

    const { hosts } = useHostStore.getState()
    const hostIds = Object.keys(hosts)
    expect(hostIds.length).toBe(1)
    expect(hosts[hostIds[0]].ip).toBe('10.0.0.1')
    expect(hosts[hostIds[0]].port).toBe(7860)
  })

  it('trims whitespace from IP, port, and token before saving (token route)', async () => {
    vi.spyOn(hostApi, 'fetchTokenAuth').mockResolvedValue({ ok: true })
    const onClose = vi.fn()

    render(<AddHostDialog onClose={onClose} />)

    // Switch to token route
    fireEvent.click(screen.getByRole('checkbox'))

    // Fill IP, port, and token with leading/trailing spaces
    fireEvent.change(screen.getByPlaceholderText('100.64.0.1'), { target: { value: '  10.0.0.1  ' } })
    fireEvent.change(screen.getByPlaceholderText('7860'), { target: { value: ' 7860 ' } })
    const tokenInput = screen.getByPlaceholderText('purdex_...')
    fireEvent.change(tokenInput, { target: { value: '  purdex_' + 'a'.repeat(40) + '  ' } })

    fireEvent.click(screen.getByText('Confirm'))

    await waitFor(() => {
      expect(onClose).toHaveBeenCalled()
    })

    // Verify fetchTokenAuth was called with trimmed values
    expect(hostApi.fetchTokenAuth).toHaveBeenCalledWith(
      'http://10.0.0.1:7860',
      'purdex_' + 'a'.repeat(40),
    )

    // Verify host was saved with trimmed IP
    const { hosts } = useHostStore.getState()
    const hostIds = Object.keys(hosts)
    expect(hostIds.length).toBe(1)
    expect(hosts[hostIds[0]].ip).toBe('10.0.0.1')
  })

  it('confirms with pairing route: calls fetchPairSetup + addHost + onClose', async () => {
    vi.spyOn(hostApi, 'fetchPairVerify').mockResolvedValue({ setupSecret: 'secret123' })
    vi.spyOn(hostApi, 'fetchPairSetup').mockResolvedValue({ ok: true })
    vi.spyOn(pairingCodec, 'generatePurdexToken').mockReturnValue('purdex_' + 'a'.repeat(40))
    vi.spyOn(pairingCodec, 'decodePairingCode').mockReturnValue({
      ip: '10.0.0.1',
      port: 7860,
      secret: 'abc123',
    })
    vi.spyOn(pairingCodec, 'cleanPairingInput').mockReturnValue('ABCDEFGHIJKLM')

    const onClose = vi.fn()
    render(<AddHostDialog onClose={onClose} />)

    // Enter pairing code and pair
    fireEvent.change(screen.getByPlaceholderText('XXXX-XXXX-XXXXX'), {
      target: { value: 'ABCD-EFGH-IJKLM' },
    })
    fireEvent.click(screen.getByText('Pair'))

    await waitFor(() => {
      expect(screen.getByText('Paired successfully')).toBeInTheDocument()
    })

    // Confirm
    fireEvent.click(screen.getByText('Confirm'))

    await waitFor(() => {
      expect(onClose).toHaveBeenCalled()
    })

    const { hosts } = useHostStore.getState()
    const hostIds = Object.keys(hosts)
    expect(hostIds.length).toBe(1)
    expect(hosts[hostIds[0]].ip).toBe('10.0.0.1')
    expect(hosts[hostIds[0]].port).toBe(7860)
  })
})

describe('AddHostDialog — daemon identity (spec 2026-09-23 D4.1 / D5)', () => {
  const X = 'mini-lab:abc123'
  const TOKEN = 'purdex_' + 'a'.repeat(40)
  const info = (host_id: string) =>
    ({ host_id, tmux_instance: '', purdex_version: '', tmux_version: '', os: '', arch: '' })
  const existing = (extra: Record<string, unknown> = {}) => ({
    id: 'H', name: 'mlab', ip: '100.64.0.2', port: 7860, token: 'old-token', order: 0, daemonId: X, ...extra,
  })

  function confirmTokenRoute(ip = '10.0.0.1') {
    fireEvent.click(screen.getByRole('checkbox'))
    fireEvent.change(screen.getByPlaceholderText('100.64.0.1'), { target: { value: ip } })
    fireEvent.change(screen.getByPlaceholderText('purdex_...'), { target: { value: TOKEN } })
    fireEvent.click(screen.getByText('Confirm'))
  }

  async function confirmPairingRoute() {
    await pairOnly()
    fireEvent.click(screen.getByText('Confirm'))
  }

  async function pairOnly() {
    vi.spyOn(hostApi, 'fetchPairVerify').mockResolvedValue({ setupSecret: 'secret123' })
    vi.spyOn(hostApi, 'fetchPairSetup').mockResolvedValue({ ok: true })
    vi.spyOn(pairingCodec, 'generatePurdexToken').mockReturnValue(TOKEN)
    vi.spyOn(pairingCodec, 'decodePairingCode').mockReturnValue({ ip: '10.0.0.1', port: 7860, secret: 'abc123' })
    vi.spyOn(pairingCodec, 'cleanPairingInput').mockReturnValue('ABCDEFGHIJKLM')
    fireEvent.change(screen.getByPlaceholderText('XXXX-XXXX-XXXXX'), { target: { value: 'ABCD-EFGH-IJKLM' } })
    fireEvent.click(screen.getByText('Pair'))
    await waitFor(() => expect(screen.getByText('Paired successfully')).toBeInTheDocument())
  }

  beforeEach(() => {
    vi.spyOn(hostApi, 'fetchTokenAuth').mockResolvedValue({ ok: true })
  })

  it('learns the new daemon id after confirm (raw base + the entered token)', async () => {
    vi.mocked(hostApi.fetchInfoAt).mockResolvedValue(info(X))
    const onClose = vi.fn()
    render(<AddHostDialog onClose={onClose} />)
    confirmTokenRoute()
    await waitFor(() => expect(onClose).toHaveBeenCalled())
    expect(hostApi.fetchInfoAt).toHaveBeenCalledWith('http://10.0.0.1:7860', TOKEN, expect.any(AbortSignal))
    const hosts = Object.values(useHostStore.getState().hosts)
    expect(hosts).toHaveLength(1)
    expect(hosts[0].daemonId).toBe(X)
  })

  it('a failed /api/info never blocks the add', async () => {
    const onClose = vi.fn()
    render(<AddHostDialog onClose={onClose} />)
    confirmTokenRoute()
    await waitFor(() => expect(onClose).toHaveBeenCalled())
    const hosts = Object.values(useHostStore.getState().hosts)
    expect(hosts).toHaveLength(1)
    expect('daemonId' in hosts[0]).toBe(false)
  })

  it('same ip+port as an existing host still just updates its token', async () => {
    useHostStore.setState({ hosts: { H: existing({ ip: '10.0.0.1' }) }, hostOrder: ['H'] })
    const onClose = vi.fn()
    render(<AddHostDialog onClose={onClose} />)
    confirmTokenRoute()
    await waitFor(() => expect(onClose).toHaveBeenCalled())
    expect(Object.keys(useHostStore.getState().hosts)).toEqual(['H'])
    expect(useHostStore.getState().hosts.H.token).toBe(TOKEN)
  })

  it('a different endpoint reaching an already-added daemon is refused with the host name (token route: no re-point action)', async () => {
    vi.mocked(hostApi.fetchInfoAt).mockResolvedValue(info(X))
    useHostStore.setState({ hosts: { H: existing() }, hostOrder: ['H'] })
    const onClose = vi.fn()
    render(<AddHostDialog onClose={onClose} />)
    confirmTokenRoute()
    await waitFor(() => expect(screen.getByText('This daemon is already added as “mlab”.')).toBeInTheDocument())
    expect(Object.keys(useHostStore.getState().hosts)).toEqual(['H'])
    expect(useHostStore.getState().hosts.H).toEqual(existing())
    expect(onClose).not.toHaveBeenCalled()
    expect(screen.queryByText('Use this address for “mlab”')).toBeNull()
    fireEvent.click(screen.getByText('Close'))
    expect(onClose).toHaveBeenCalled()
    expect(useHostStore.getState().hosts.H).toEqual(existing())
  })

  it('pairing route: the explicit "use this address" re-points the existing host with the new token', async () => {
    vi.mocked(hostApi.fetchInfoAt).mockResolvedValue(info(X))
    useHostStore.setState({ hosts: { H: existing() }, hostOrder: ['H'] })
    const onClose = vi.fn()
    render(<AddHostDialog onClose={onClose} />)
    await confirmPairingRoute()
    await waitFor(() => expect(screen.getByText('This daemon is already added as “mlab”.')).toBeInTheDocument())
    // Nothing is rewritten until the user asks.
    expect(useHostStore.getState().hosts.H).toEqual(existing())
    fireEvent.click(screen.getByText('Use this address for “mlab”'))
    expect(onClose).toHaveBeenCalled()
    const hosts = useHostStore.getState().hosts
    expect(Object.keys(hosts)).toEqual(['H'])
    expect(hosts.H).toMatchObject({ ip: '10.0.0.1', port: 7860, token: TOKEN, name: 'mlab' })
    expect('daemonId' in hosts.H).toBe(false) // re-point: re-verified on connect
  })

  describe('pairing route duplicate: the rotated token is never lost (PR review #1)', () => {
    const verifiedRuntime = { H: { status: 'connected' as const, daemonIdVerified: { endpoint: '100.64.0.2:7860', daemonId: X } } }

    it('H verified here → its token is updated automatically; message + Close only', async () => {
      vi.mocked(hostApi.fetchInfoAt).mockResolvedValue(info(X))
      useHostStore.setState({ hosts: { H: existing() }, hostOrder: ['H'], runtime: verifiedRuntime })
      const onClose = vi.fn()
      render(<AddHostDialog onClose={onClose} />)
      await confirmPairingRoute()
      await waitFor(() => expect(screen.getByText('This daemon is already added as “mlab”; its token was updated.')).toBeInTheDocument())
      expect(useHostStore.getState().hosts.H).toEqual(existing({ token: TOKEN }))
      expect(screen.queryByText('Use this address for “mlab”')).toBeNull()
      expect(screen.queryByText('Add as a separate host')).toBeNull()
      fireEvent.click(screen.getByText('Close'))
      expect(onClose).toHaveBeenCalled()
      expect(Object.keys(useHostStore.getState().hosts)).toEqual(['H'])
    })

    it('H not verified → exactly two actions, no plain Close', async () => {
      vi.mocked(hostApi.fetchInfoAt).mockResolvedValue(info(X))
      useHostStore.setState({ hosts: { H: existing() }, hostOrder: ['H'] })
      render(<AddHostDialog onClose={vi.fn()} />)
      await confirmPairingRoute()
      await waitFor(() => expect(screen.getByText('Add as a separate host')).toBeInTheDocument())
      expect(screen.getByText('Use this address for “mlab”')).toBeInTheDocument()
      expect(screen.queryByText('Close')).toBeNull()
      expect(screen.queryByText('Cancel')).toBeNull()
      expect(useHostStore.getState().hosts.H).toEqual(existing())
    })

    it('H not verified → "Add as a separate host" adds a new host with the new token', async () => {
      vi.mocked(hostApi.fetchInfoAt).mockResolvedValue(info(X))
      useHostStore.setState({ hosts: { H: existing() }, hostOrder: ['H'] })
      const onClose = vi.fn()
      const { unmount } = render(<AddHostDialog onClose={onClose} />)
      await confirmPairingRoute()
      await waitFor(() => expect(screen.getByText('Add as a separate host')).toBeInTheDocument())
      fireEvent.click(screen.getByText('Add as a separate host'))
      expect(onClose).toHaveBeenCalled()
      unmount()
      const hosts = useHostStore.getState().hosts
      expect(Object.keys(hosts)).toHaveLength(2)
      expect(hosts.H).toEqual(existing())
      const added = Object.values(hosts).find((h) => h.id !== 'H')!
      expect(added).toMatchObject({ ip: '10.0.0.1', port: 7860, token: TOKEN, daemonId: X }) // as if no duplicate
    })

    it.each([
      ['Escape', () => fireEvent.keyDown(document, { key: 'Escape' })],
      ['the X button', () => fireEvent.click(screen.getByRole('heading', { name: 'Add Host' }).nextElementSibling!)],
      ['the backdrop', () => fireEvent.click(screen.getByRole('dialog'))],
    ])('H not verified → dismissing with %s counts as "Add as a separate host"', async (_label, dismiss) => {
      vi.mocked(hostApi.fetchInfoAt).mockResolvedValue(info(X))
      useHostStore.setState({ hosts: { H: existing() }, hostOrder: ['H'] })
      const onClose = vi.fn()
      const { unmount } = render(<AddHostDialog onClose={onClose} />)
      await confirmPairingRoute()
      await waitFor(() => expect(screen.getByText('Add as a separate host')).toBeInTheDocument())
      dismiss()
      expect(onClose).toHaveBeenCalled()
      unmount()
      const hosts = useHostStore.getState().hosts
      expect(Object.keys(hosts)).toHaveLength(2)
      expect(Object.values(hosts).find((h) => h.id !== 'H')).toMatchObject({ ip: '10.0.0.1', token: TOKEN })
    })

    it('H not verified → unmounted without a choice still adds the separate host, once', async () => {
      vi.mocked(hostApi.fetchInfoAt).mockResolvedValue(info(X))
      useHostStore.setState({ hosts: { H: existing() }, hostOrder: ['H'] })
      const { unmount } = render(<AddHostDialog onClose={vi.fn()} />)
      await confirmPairingRoute()
      await waitFor(() => expect(screen.getByText('Add as a separate host')).toBeInTheDocument())
      unmount()
      expect(Object.keys(useHostStore.getState().hosts)).toHaveLength(2)
    })

    it('"Use this address" then unmount → no extra host', async () => {
      vi.mocked(hostApi.fetchInfoAt).mockResolvedValue(info(X))
      useHostStore.setState({ hosts: { H: existing() }, hostOrder: ['H'] })
      const { unmount } = render(<AddHostDialog onClose={vi.fn()} />)
      await confirmPairingRoute()
      await waitFor(() => expect(screen.getByText('Use this address for “mlab”')).toBeInTheDocument())
      fireEvent.click(screen.getByText('Use this address for “mlab”'))
      unmount()
      expect(Object.keys(useHostStore.getState().hosts)).toEqual(['H'])
    })
  })

  it('token route duplicate: Escape adds nothing', async () => {
    vi.mocked(hostApi.fetchInfoAt).mockResolvedValue(info(X))
    useHostStore.setState({ hosts: { H: existing() }, hostOrder: ['H'] })
    const { unmount } = render(<AddHostDialog onClose={vi.fn()} />)
    confirmTokenRoute()
    await waitFor(() => expect(screen.getByText('This daemon is already added as “mlab”.')).toBeInTheDocument())
    fireEvent.keyDown(document, { key: 'Escape' })
    unmount()
    expect(Object.keys(useHostStore.getState().hosts)).toEqual(['H'])
    expect(useHostStore.getState().hosts.H).toEqual(existing())
  })

  it('an existing host flagged with a mismatch is not treated as a duplicate', async () => {
    vi.mocked(hostApi.fetchInfoAt).mockResolvedValue(info(X))
    useHostStore.setState({
      hosts: { H: existing() },
      hostOrder: ['H'],
      runtime: { H: { status: 'connected', daemonIdMismatch: { stored: X, observed: 'mini-lab:other', endpoint: '100.64.0.2:7860' } } },
    })
    const onClose = vi.fn()
    render(<AddHostDialog onClose={onClose} />)
    confirmTokenRoute()
    await waitFor(() => expect(onClose).toHaveBeenCalled())
    expect(Object.keys(useHostStore.getState().hosts)).toHaveLength(2)
  })

  // A probe that only ends when the dialog aborts it.
  const probeUntilAborted = () =>
    vi.mocked(hostApi.fetchInfoAt).mockImplementation((_b, _t, signal) => new Promise((_res, rej) => {
      signal?.addEventListener('abort', () => rej(new Error('aborted')))
    }))

  it('pairing route: a probe that hangs times out and the host is still added (review #3)', async () => {
    vi.mocked(hostApi.fetchInfoAt).mockRestore()
    vi.spyOn(globalThis, 'fetch').mockImplementation((_url, init) => new Promise<Response>((_res, rej) => {
      (init as RequestInit).signal?.addEventListener('abort', () => rej(new Error('aborted')))
    }))
    const onClose = vi.fn()
    render(<AddHostDialog onClose={onClose} />)
    await pairOnly()
    vi.useFakeTimers()
    try {
      fireEvent.click(screen.getByText('Confirm'))
      await vi.advanceTimersByTimeAsync(hostApi.INFO_AT_TIMEOUT_MS)
    } finally {
      vi.useRealTimers()
    }
    expect(onClose).toHaveBeenCalled()
    const hosts = Object.values(useHostStore.getState().hosts)
    expect(hosts).toHaveLength(1)
    expect(hosts[0]).toMatchObject({ ip: '10.0.0.1', token: TOKEN })
  })

  it('pairing route: dismissed mid-probe → the host (new token) is still added, exactly once (review #3)', async () => {
    probeUntilAborted()
    const { unmount } = render(<AddHostDialog onClose={vi.fn()} />)
    await confirmPairingRoute()
    await waitFor(() => expect(hostApi.fetchInfoAt).toHaveBeenCalled())
    unmount()
    await new Promise((r) => setTimeout(r, 0))
    const hosts = Object.values(useHostStore.getState().hosts)
    expect(hosts).toHaveLength(1)
    expect(hosts[0]).toMatchObject({ ip: '10.0.0.1', token: TOKEN })
    expect('daemonId' in hosts[0]).toBe(false)
  })

  it('token route: dismissed mid-probe → the probe is aborted and nothing is added (review #3)', async () => {
    probeUntilAborted()
    const { unmount } = render(<AddHostDialog onClose={vi.fn()} />)
    confirmTokenRoute()
    await waitFor(() => expect(hostApi.fetchInfoAt).toHaveBeenCalled())
    const signal = vi.mocked(hostApi.fetchInfoAt).mock.calls[0][2]!
    unmount()
    expect(signal.aborted).toBe(true)
    await new Promise((r) => setTimeout(r, 0))
    expect(Object.keys(useHostStore.getState().hosts)).toHaveLength(0)
  })

  it('an empty host_id cannot tell → added as today', async () => {
    vi.mocked(hostApi.fetchInfoAt).mockResolvedValue(info(''))
    useHostStore.setState({ hosts: { H: existing() }, hostOrder: ['H'] })
    const onClose = vi.fn()
    render(<AddHostDialog onClose={onClose} />)
    confirmTokenRoute()
    await waitFor(() => expect(onClose).toHaveBeenCalled())
    expect(Object.keys(useHostStore.getState().hosts)).toHaveLength(2)
  })
})
