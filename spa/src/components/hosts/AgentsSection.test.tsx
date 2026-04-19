import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import { AgentsSection } from './AgentsSection'
import { hostFetch } from '../../lib/host-api'
import { useHostStore } from '../../stores/useHostStore'

vi.mock('../../lib/host-api', () => ({ hostFetch: vi.fn() }))

const mockFetch = hostFetch as unknown as ReturnType<typeof vi.fn>

beforeEach(() => {
  mockFetch.mockReset()
  // Set runtime as connected so the component isn't offline
  useHostStore.setState({
    runtime: { h1: { status: 'connected' } },
  })
})

function mockDetect(payload: Record<string, { installed: boolean; version?: string; path?: string }>) {
  mockFetch.mockImplementation((_hostId: string, path: string) => {
    if (path === '/api/agents/detect') {
      return Promise.resolve({ ok: true, json: () => Promise.resolve(payload) })
    }
    if (path === '/api/agent/cc/statusline/status') {
      return Promise.resolve({ ok: true, json: () => Promise.resolve({ mode: 'none', installed: false, settingsPath: '/x' }) })
    }
    return Promise.resolve({ ok: false, status: 404 })
  })
}

describe('AgentsSection Extensions region', () => {
  it('renders Extensions region for installed cc agent', async () => {
    mockDetect({ cc: { installed: true, version: '0.1.0' } })
    render(<AgentsSection hostId="h1" />)
    // Wait for the AgentExtensionRow to render after detect + statusline status fetch
    await waitFor(() => {
      // heading key renders as raw key when T21 translations are absent
      expect(screen.getByText('hosts.extensions.heading')).toBeInTheDocument()
    })
    // AgentExtensionRow renders its Install button
    await waitFor(() =>
      expect(screen.getByRole('button', { name: /install/i })).toBeInTheDocument()
    )
  })

  it('does not render Extensions region for codex agent', async () => {
    mockDetect({ codex: { installed: true, version: '1.0.0' } })
    render(<AgentsSection hostId="h1" />)
    // Wait for the detect call to complete and agent card to render
    await waitFor(() =>
      expect(screen.getByText(/codex/i)).toBeInTheDocument()
    )
    // Extensions heading should NOT appear
    expect(screen.queryByText('hosts.extensions.heading')).not.toBeInTheDocument()
  })

  it('does not render Extensions region when cc is not installed', async () => {
    mockDetect({ cc: { installed: false } })
    render(<AgentsSection hostId="h1" />)
    // Wait for detect call and agent card — "Not found" is the translation for hosts.agent_not_found
    await waitFor(() =>
      expect(screen.getByText(/not found/i)).toBeInTheDocument()
    )
    expect(screen.queryByText('hosts.extensions.heading')).not.toBeInTheDocument()
  })
})
