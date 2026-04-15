import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent, waitFor } from '@testing-library/react'
import { AddHostDialog } from './AddHostDialog'
import { useHostStore } from '../../stores/useHostStore'
import * as hostApi from '../../lib/host-api'
import * as pairingCodec from '../../lib/pairing-codec'

beforeEach(() => {
  vi.restoreAllMocks()
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
