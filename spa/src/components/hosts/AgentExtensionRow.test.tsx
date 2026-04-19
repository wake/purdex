import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent, waitFor } from '@testing-library/react'
import { AgentExtensionRow } from './AgentExtensionRow'
import { hostFetch } from '../../lib/host-api'
import { useHostStore } from '../../stores/useHostStore'

vi.mock('../../lib/host-api', () => ({ hostFetch: vi.fn() }))
const mockFetch = hostFetch as unknown as ReturnType<typeof vi.fn>

beforeEach(() => {
  mockFetch.mockReset()
  // Fallback: the StatuslineTestPanel auto-run fires on install transitions.
  // Tests that don't explicitly care about the test endpoint get a safe non-ok
  // response so the panel fails stage 1 gracefully (no unhandled rejection).
  mockFetch.mockResolvedValue({ ok: false, status: 500, body: null })
  useHostStore.setState({ runtime: { h1: { status: 'connected' } } })
})

describe('AgentExtensionRow (statusline)', () => {
  it('shows Install button when mode=none', async () => {
    mockFetch.mockResolvedValueOnce({ ok: true, json: () => Promise.resolve({ mode: 'none', installed: false, settingsPath: '/x' }) })
    render(<AgentExtensionRow hostId="h1" extensionId="statusline" />)
    await waitFor(() => expect(screen.getByRole('button', { name: /install/i })).toBeInTheDocument())
  })

  it('shows Remove button when mode=pdx', async () => {
    mockFetch.mockResolvedValueOnce({ ok: true, json: () => Promise.resolve({ mode: 'pdx', installed: true, settingsPath: '/x' }) })
    render(<AgentExtensionRow hostId="h1" extensionId="statusline" />)
    await waitFor(() => expect(screen.getByRole('button', { name: /remove/i })).toBeInTheDocument())
  })

  it('shows Remove button when mode=wrapped', async () => {
    mockFetch.mockResolvedValueOnce({ ok: true, json: () => Promise.resolve({ mode: 'wrapped', installed: true, innerCommand: 'x', settingsPath: '/x' }) })
    render(<AgentExtensionRow hostId="h1" extensionId="statusline" />)
    await waitFor(() => expect(screen.getByRole('button', { name: /remove/i })).toBeInTheDocument())
  })

  it('shows Install button when mode=unmanaged', async () => {
    mockFetch.mockResolvedValueOnce({ ok: true, json: () => Promise.resolve({ mode: 'unmanaged', installed: true, innerCommand: 'ccstatusline', settingsPath: '/x' }) })
    render(<AgentExtensionRow hostId="h1" extensionId="statusline" />)
    await waitFor(() => expect(screen.getByRole('button', { name: /install/i })).toBeInTheDocument())
  })

  it('Install on mode=none directly installs pdx (no dialog)', async () => {
    mockFetch
      .mockResolvedValueOnce({ ok: true, json: () => Promise.resolve({ mode: 'none', installed: false, settingsPath: '/x' }) })
      .mockResolvedValueOnce({ ok: true, json: () => Promise.resolve({ mode: 'pdx', installed: true, settingsPath: '/x' }) })
    render(<AgentExtensionRow hostId="h1" extensionId="statusline" />)
    await waitFor(() => screen.getByRole('button', { name: /install/i }))
    fireEvent.click(screen.getByRole('button', { name: /install/i }))
    await waitFor(() => expect(screen.getByRole('button', { name: /remove/i })).toBeInTheDocument())
    const call = mockFetch.mock.calls[1]
    expect(JSON.parse(call[2].body)).toMatchObject({ action: 'install', mode: 'pdx' })
  })

  it('Install on mode=unmanaged shows conflict dialog', async () => {
    mockFetch.mockResolvedValueOnce({ ok: true, json: () => Promise.resolve({ mode: 'unmanaged', installed: true, rawCommand: 'ccstatusline', settingsPath: '/x' }) })
    render(<AgentExtensionRow hostId="h1" extensionId="statusline" />)
    await waitFor(() => screen.getByRole('button', { name: /install/i }))
    fireEvent.click(screen.getByRole('button', { name: /install/i }))
    expect(await screen.findByText('ccstatusline')).toBeInTheDocument() // dialog shows existingCommand (rawCommand ?? innerCommand)
  })

  it('Wrap choice in dialog sends install with mode=wrap + inner', async () => {
    mockFetch
      .mockResolvedValueOnce({ ok: true, json: () => Promise.resolve({ mode: 'unmanaged', installed: true, rawCommand: 'ccstatusline', settingsPath: '/x' }) })
      .mockResolvedValueOnce({ ok: true, json: () => Promise.resolve({ mode: 'wrapped', installed: true, innerCommand: 'ccstatusline', settingsPath: '/x' }) })
    render(<AgentExtensionRow hostId="h1" extensionId="statusline" />)
    await waitFor(() => screen.getByRole('button', { name: /install/i }))
    fireEvent.click(screen.getByRole('button', { name: /install/i }))
    // dialog appears; click Wrap button
    const wrapBtn = await screen.findByRole('button', { name: /wrap/i })
    fireEvent.click(wrapBtn)
    await waitFor(() => expect(mockFetch).toHaveBeenCalledTimes(2))
    const call = mockFetch.mock.calls[1]
    expect(JSON.parse(call[2].body)).toMatchObject({ action: 'install', mode: 'wrap', inner: 'ccstatusline' })
  })

  it('Remove button triggers window.confirm then remove', async () => {
    mockFetch
      .mockResolvedValueOnce({ ok: true, json: () => Promise.resolve({ mode: 'pdx', installed: true, settingsPath: '/x' }) })
      // Panel auto-run fires on mount (transition none→pdx) — respond with non-ok so it fails stage 1 and stops
      .mockResolvedValueOnce({ ok: false, status: 500, body: null })
      .mockResolvedValueOnce({ ok: true, json: () => Promise.resolve({ mode: 'none', installed: false, settingsPath: '/x' }) })
    const confirmSpy = vi.spyOn(window, 'confirm').mockReturnValue(true)
    render(<AgentExtensionRow hostId="h1" extensionId="statusline" />)
    await waitFor(() => screen.getByRole('button', { name: /remove/i }))
    // Wait until panel auto-run fetch has fired (2 calls: GET status + POST test)
    await waitFor(() => expect(mockFetch).toHaveBeenCalledTimes(2))
    fireEvent.click(screen.getByRole('button', { name: /remove/i }))
    await waitFor(() => expect(mockFetch).toHaveBeenCalledTimes(3))
    expect(confirmSpy).toHaveBeenCalled()
    const removeCall = mockFetch.mock.calls[2]
    expect(JSON.parse(removeCall[2].body)).toMatchObject({ action: 'remove' })
    confirmSpy.mockRestore()
  })

  it('Remove cancelled in window.confirm does not POST', async () => {
    mockFetch
      .mockResolvedValueOnce({ ok: true, json: () => Promise.resolve({ mode: 'pdx', installed: true, settingsPath: '/x' }) })
      // Panel auto-run fires on mount — respond with non-ok
      .mockResolvedValueOnce({ ok: false, status: 500, body: null })
    const confirmSpy = vi.spyOn(window, 'confirm').mockReturnValue(false)
    render(<AgentExtensionRow hostId="h1" extensionId="statusline" />)
    await waitFor(() => screen.getByRole('button', { name: /remove/i }))
    // Wait until panel auto-run fetch has fired
    await waitFor(() => expect(mockFetch).toHaveBeenCalledTimes(2))
    fireEvent.click(screen.getByRole('button', { name: /remove/i }))
    // still 2 calls — confirm=false → no remove POST
    expect(mockFetch).toHaveBeenCalledTimes(2)
    confirmSpy.mockRestore()
  })

  // --- Status icon (#480) ---

  it('renders green CheckCircle icon when mode=pdx', async () => {
    mockFetch.mockResolvedValueOnce({ ok: true, json: () => Promise.resolve({ mode: 'pdx', installed: true, settingsPath: '/x' }) })
    const { container } = render(<AgentExtensionRow hostId="h1" extensionId="statusline" />)
    await waitFor(() => screen.getByRole('button', { name: /remove/i }))
    const icon = container.querySelector('[data-testid="ext-status-icon"]')
    expect(icon).not.toBeNull()
    expect(icon?.getAttribute('data-icon')).toBe('check')
    expect(icon?.classList.contains('text-green-400')).toBe(true)
    expect(icon?.getAttribute('aria-hidden')).toBe('true')
  })

  it('renders green CheckCircle icon when mode=wrapped', async () => {
    mockFetch.mockResolvedValueOnce({ ok: true, json: () => Promise.resolve({ mode: 'wrapped', installed: true, innerCommand: 'x', settingsPath: '/x' }) })
    const { container } = render(<AgentExtensionRow hostId="h1" extensionId="statusline" />)
    await waitFor(() => screen.getByRole('button', { name: /remove/i }))
    const icon = container.querySelector('[data-testid="ext-status-icon"]')
    expect(icon).not.toBeNull()
    expect(icon?.getAttribute('data-icon')).toBe('check')
    expect(icon?.classList.contains('text-green-400')).toBe(true)
  })

  it('renders yellow WarningCircle icon when mode=unmanaged', async () => {
    mockFetch.mockResolvedValueOnce({ ok: true, json: () => Promise.resolve({ mode: 'unmanaged', installed: true, rawCommand: 'ccstatusline', settingsPath: '/x' }) })
    const { container } = render(<AgentExtensionRow hostId="h1" extensionId="statusline" />)
    await waitFor(() => screen.getByRole('button', { name: /install/i }))
    const icon = container.querySelector('[data-testid="ext-status-icon"]')
    expect(icon).not.toBeNull()
    expect(icon?.getAttribute('data-icon')).toBe('warning')
    expect(icon?.classList.contains('text-yellow-400')).toBe(true)
    expect(icon?.getAttribute('aria-hidden')).toBe('true')
  })

  it('omits status icon when mode=none', async () => {
    mockFetch.mockResolvedValueOnce({ ok: true, json: () => Promise.resolve({ mode: 'none', installed: false, settingsPath: '/x' }) })
    const { container } = render(<AgentExtensionRow hostId="h1" extensionId="statusline" />)
    await waitFor(() => screen.getByRole('button', { name: /install/i }))
    expect(container.querySelector('[data-testid="ext-status-icon"]')).toBeNull()
  })

  // --- Offline guard (Fix 3) ---

  it('disables buttons when host is offline', async () => {
    mockFetch.mockResolvedValueOnce({ ok: true, json: () => Promise.resolve({ mode: 'pdx', installed: true, settingsPath: '/x' }) })
    useHostStore.setState({ runtime: { h1: { status: 'disconnected' } } })
    render(<AgentExtensionRow hostId="h1" extensionId="statusline" />)
    await waitFor(() => screen.getByRole('button', { name: /remove/i }))
    expect(screen.getByRole('button', { name: /remove/i })).toBeDisabled()
  })

  // --- Pipeline test panel (#481) ---

  it('renders StatuslineTestPanel when mode=pdx', async () => {
    mockFetch.mockResolvedValueOnce({ ok: true, json: () => Promise.resolve({ mode: 'pdx', installed: true, settingsPath: '/x' }) })
    render(<AgentExtensionRow hostId="h1" extensionId="statusline" />)
    await waitFor(() => screen.getByRole('button', { name: /remove/i }))
    expect(screen.getByText(/pipeline test/i)).toBeInTheDocument()
  })

  it('renders StatuslineTestPanel when mode=wrapped', async () => {
    mockFetch.mockResolvedValueOnce({ ok: true, json: () => Promise.resolve({ mode: 'wrapped', installed: true, innerCommand: 'x', settingsPath: '/x' }) })
    render(<AgentExtensionRow hostId="h1" extensionId="statusline" />)
    await waitFor(() => screen.getByRole('button', { name: /remove/i }))
    expect(screen.getByText(/pipeline test/i)).toBeInTheDocument()
  })

  it('does not render panel when mode=none', async () => {
    mockFetch.mockResolvedValueOnce({ ok: true, json: () => Promise.resolve({ mode: 'none', installed: false, settingsPath: '/x' }) })
    render(<AgentExtensionRow hostId="h1" extensionId="statusline" />)
    await waitFor(() => screen.getByRole('button', { name: /install/i }))
    expect(screen.queryByText(/pipeline test/i)).toBeNull()
  })

  it('does not render panel when mode=unmanaged', async () => {
    mockFetch.mockResolvedValueOnce({ ok: true, json: () => Promise.resolve({ mode: 'unmanaged', installed: true, rawCommand: 'ccstatusline', settingsPath: '/x' }) })
    render(<AgentExtensionRow hostId="h1" extensionId="statusline" />)
    await waitFor(() => screen.getByRole('button', { name: /install/i }))
    expect(screen.queryByText(/pipeline test/i)).toBeNull()
  })
})
