import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent, waitFor } from '@testing-library/react'
import { AddHostDialog } from './AddHostDialog'
import { useHostStore } from '../../stores/useHostStore'

beforeEach(() => {
  vi.restoreAllMocks()
  useHostStore.setState({
    hosts: {},
    hostOrder: [],
    runtime: {},
  })
})

describe('AddHostDialog', () => {
  it('renders form with IP, Port, Name fields', () => {
    render(<AddHostDialog onClose={vi.fn()} />)
    expect(screen.getByPlaceholderText('My Server')).toBeInTheDocument()
    expect(screen.getByPlaceholderText('100.64.0.1')).toBeInTheDocument()
    expect(screen.getByPlaceholderText('7860')).toBeInTheDocument()
  })

  it('Test Connection button disabled when IP is empty', () => {
    render(<AddHostDialog onClose={vi.fn()} />)
    const btn = screen.getByText('Test Connection').closest('button')!
    expect(btn).toBeDisabled()
  })

  it('Test Connection button enabled when IP is filled', () => {
    render(<AddHostDialog onClose={vi.fn()} />)
    fireEvent.change(screen.getByPlaceholderText('100.64.0.1'), { target: { value: '10.0.0.1' } })
    const btn = screen.getByText('Test Connection').closest('button')!
    expect(btn).not.toBeDisabled()
  })

  it('successful health check + sessions check shows Save button', async () => {
    vi.spyOn(globalThis, 'fetch')
      .mockResolvedValueOnce({ ok: true, json: () => Promise.resolve({ status: 'ok' }) } as Response)
      .mockResolvedValueOnce({ ok: true, json: () => Promise.resolve([]) } as Response)

    render(<AddHostDialog onClose={vi.fn()} />)
    fireEvent.change(screen.getByPlaceholderText('100.64.0.1'), { target: { value: '10.0.0.1' } })
    fireEvent.click(screen.getByText('Test Connection'))

    await waitFor(() => {
      expect(screen.getByText('Save')).toBeInTheDocument()
    })
  })

  it('401 response shows token field', async () => {
    vi.spyOn(globalThis, 'fetch')
      .mockResolvedValueOnce({ ok: true, json: () => Promise.resolve({ status: 'ok' }) } as Response)
      .mockResolvedValueOnce({ ok: false, status: 401 } as Response)

    render(<AddHostDialog onClose={vi.fn()} />)
    fireEvent.change(screen.getByPlaceholderText('100.64.0.1'), { target: { value: '10.0.0.1' } })
    fireEvent.click(screen.getByText('Test Connection'))

    await waitFor(() => {
      expect(screen.getByPlaceholderText('tbox_...')).toBeInTheDocument()
      expect(screen.getByText('This daemon requires authentication. Enter a token.')).toBeInTheDocument()
    })
  })

  it('changing IP after success resets stage to idle', async () => {
    vi.spyOn(globalThis, 'fetch')
      .mockResolvedValueOnce({ ok: true, json: () => Promise.resolve({ status: 'ok' }) } as Response)
      .mockResolvedValueOnce({ ok: true, json: () => Promise.resolve([]) } as Response)

    render(<AddHostDialog onClose={vi.fn()} />)
    fireEvent.change(screen.getByPlaceholderText('100.64.0.1'), { target: { value: '10.0.0.1' } })
    fireEvent.click(screen.getByText('Test Connection'))

    await waitFor(() => {
      expect(screen.getByText('Save')).toBeInTheDocument()
    })

    // Change IP — should reset back to idle / Test Connection button
    fireEvent.change(screen.getByPlaceholderText('100.64.0.1'), { target: { value: '10.0.0.2' } })

    expect(screen.queryByText('Save')).toBeNull()
    expect(screen.getByText('Test Connection')).toBeInTheDocument()
  })

  it('close button calls onClose', () => {
    const onClose = vi.fn()
    render(<AddHostDialog onClose={onClose} />)
    // The Cancel button in the footer
    fireEvent.click(screen.getByText('Cancel'))
    expect(onClose).toHaveBeenCalled()
  })

  it('Save button calls addHost and onClose', async () => {
    vi.spyOn(globalThis, 'fetch')
      .mockResolvedValueOnce({ ok: true, json: () => Promise.resolve({ status: 'ok' }) } as Response)
      .mockResolvedValueOnce({ ok: true, json: () => Promise.resolve([]) } as Response)

    const onClose = vi.fn()
    render(<AddHostDialog onClose={onClose} />)
    fireEvent.change(screen.getByPlaceholderText('My Server'), { target: { value: 'My Host' } })
    fireEvent.change(screen.getByPlaceholderText('100.64.0.1'), { target: { value: '10.0.0.1' } })
    fireEvent.click(screen.getByText('Test Connection'))

    await waitFor(() => {
      expect(screen.getByText('Save')).toBeInTheDocument()
    })

    fireEvent.click(screen.getByText('Save'))
    expect(onClose).toHaveBeenCalled()

    // Verify host was added to store
    const { hosts } = useHostStore.getState()
    const hostIds = Object.keys(hosts)
    expect(hostIds.length).toBe(1)
    const addedHost = hosts[hostIds[0]]
    expect(addedHost.name).toBe('My Host')
    expect(addedHost.ip).toBe('10.0.0.1')
    expect(addedHost.port).toBe(7860)
  })
})
