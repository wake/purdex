import { describe, it, expect, beforeEach, vi } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import NexEngineStatus from './NexEngineStatus'
import * as api from '../../../lib/nex/nex-api'
import type { NexInfo } from '../../../lib/host-api'

vi.mock('../../../lib/nex/nex-api', () => ({ fetchNexHost: vi.fn(), fetchNexCapabilities: vi.fn() }))

const ready: NexInfo = { configured: true, mounted: true, ready: true, init_error: '', effective: { data_dir: '/d/nex', claude_bin: '', cswap_bin: '', max_profile: 'handoff', default_profile: 'standard', repo_roots: ['/Users/w/Workspace'], service_roots: [], path_prefix: '/opt/bin', lease_ttl: '2m0s', interrupt: '10s', turn: '5m0s' } }

beforeEach(() => {
  vi.mocked(api.fetchNexHost).mockReset().mockResolvedValue({ active_account: 'wake@example.com', quota: { five_hour_pct: 12.5, seven_day_pct: 80, resets_at: 0, source: 'cswap' } })
  // sandbox_default_profile/sandbox_max_profile deliberately differ from
  // `ready.effective.{default_profile,max_profile}` so a fix#1 regression
  // (reading the clamped live capability instead of the configured
  // effective value) shows up as a wrong string, not a false pass.
  vi.mocked(api.fetchNexCapabilities).mockReset().mockResolvedValue({ phase: 'P1a', host_id: 'mlab', verbs: [], providers: ['claude'], events: [], provider_events: [], transient_events: [], sandbox_profiles: ['readonly', 'standard'], sandbox_default_profile: 'readonly', sandbox_max_profile: 'standard', roots: [{ path: '/Users/w/Workspace', kind: 'dev' }], lease: { ttl_seconds: 120, scope: 'execution', renew: { method: 'POST', path: '' }, release: { method: 'DELETE', path: '' } }, send: { delivery: [], max_text_bytes: 65536 } })
})

describe('NexEngineStatus', () => {
  it('shows Disabled and fetches nothing when not configured', () => {
    render(<NexEngineStatus hostId="h" info={{ configured: false, mounted: false, ready: false, init_error: '', effective: null }} onRefresh={() => {}} />)
    expect(screen.getByTestId('nex-status-badge')).toHaveTextContent(/disabled/i)
    expect(api.fetchNexHost).not.toHaveBeenCalled()
  })

  it('shows "not running" when configured but not mounted', () => {
    render(<NexEngineStatus hostId="h" info={{ configured: true, mounted: false, ready: false, init_error: '', effective: null }} onRefresh={() => {}} />)
    expect(screen.getByTestId('nex-status-badge')).toHaveTextContent(/not running/i)
  })

  it('shows Unavailable with the init error and skips Nexen calls', () => {
    render(<NexEngineStatus hostId="h" info={{ configured: true, mounted: true, ready: false, init_error: 'nex: init: assembling engine: boom', effective: null }} onRefresh={() => {}} />)
    expect(screen.getByTestId('nex-status-badge')).toHaveTextContent(/unavailable/i)
    expect(screen.getByText(/boom/)).toBeInTheDocument()
    expect(api.fetchNexCapabilities).not.toHaveBeenCalled()
  })

  it('when ready, renders effective config, account, quota bars, roots, profiles', async () => {
    render(<NexEngineStatus hostId="h" info={ready} onRefresh={() => {}} />)
    expect(screen.getByTestId('nex-status-badge')).toHaveTextContent(/ready/i)
    await waitFor(() => expect(screen.getByText('wake@example.com')).toBeInTheDocument())
    expect(screen.getByText('P1a · mlab')).toBeInTheDocument()
    expect(screen.getByText('12.5%')).toBeInTheDocument()
    expect(screen.getByText('80%')).toBeInTheDocument()
    expect(screen.getByText('/Users/w/Workspace')).toBeInTheDocument()
    // Profiles must come from info.effective (configured), not the live/
    // clamped capabilities — the fixture sets these to different strings.
    expect(screen.getByText('standard / handoff')).toBeInTheDocument()
    expect(screen.queryByText('readonly / standard')).not.toBeInTheDocument()
    expect(screen.getByText('/d/nex')).toBeInTheDocument()
  })

  it('profiles row shows effective values even when fetchNexCapabilities rejects', async () => {
    vi.mocked(api.fetchNexCapabilities).mockRejectedValueOnce(new Error('nexen down'))
    const warn = vi.spyOn(console, 'warn').mockImplementation(() => {})
    render(<NexEngineStatus hostId="h" info={ready} onRefresh={() => {}} />)
    expect(screen.getByText('standard / handoff')).toBeInTheDocument()
    await waitFor(() => expect(api.fetchNexCapabilities).toHaveBeenCalledTimes(1))
    expect(screen.getByText('standard / handoff')).toBeInTheDocument()
    warn.mockRestore()
  })

  it('renders quota as unknown when null (never 0)', async () => {
    vi.mocked(api.fetchNexHost).mockResolvedValueOnce({ active_account: '', quota: null })
    render(<NexEngineStatus hostId="h" info={ready} onRefresh={() => {}} />)
    await waitFor(() => expect(screen.getAllByText(/unknown/i).length).toBeGreaterThan(0))
    expect(screen.queryByText('0%')).not.toBeInTheDocument()
  })

  it('Refresh calls onRefresh and refetches', async () => {
    const onRefresh = vi.fn()
    render(<NexEngineStatus hostId="h" info={ready} onRefresh={onRefresh} />)
    await waitFor(() => expect(api.fetchNexHost).toHaveBeenCalledTimes(1))
    screen.getByRole('button', { name: /refresh/i }).click()
    expect(onRefresh).toHaveBeenCalled()
    await waitFor(() => expect(api.fetchNexHost).toHaveBeenCalledTimes(2))
  })
})
