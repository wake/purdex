// c9d9c7-57: [nex.sandbox] extra_profiles in the Nex config form. The daemon's PUT is presence-aware (omitted = keep, [] = clear,
// a list = replace), so the form always sends the key. A name the form does not know stays visible and ticked.
import { describe, it, expect, beforeEach, vi } from 'vitest'
import { render, screen, fireEvent, waitFor } from '@testing-library/react'
import NexConfigForm from './NexConfigForm'
import { EXTRA_SANDBOX_PROFILES, SANDBOX_PROFILES, emptyNexConfig, normalizeNexConfig } from './nex-config-diff'
import * as hostApi from '../../../lib/host-api'
import { useI18nStore } from '../../../stores/useI18nStore'

vi.mock('../../../lib/host-api', async () => {
  const actual = await vi.importActual<typeof import('../../../lib/host-api')>('../../../lib/host-api')
  return { ...actual, hostFetch: vi.fn() }
})

const info = { configured: true, mounted: true, ready: true, init_error: '', effective: { data_dir: '/d', claude_bin: '', max_profile: 'handoff', default_profile: 'standard', repo_roots: ['/a'], service_roots: [], path_prefix: '', lease_ttl: '2m0s', interrupt: '10s', turn: '5m0s' } }
const withExtra = (extra: string[]) => ({ ...emptyNexConfig(), enabled: true, repo_roots: ['/a'], sandbox: { max_profile: 'handoff', default_profile: 'standard', extra_profiles: extra } })
const confined = () => screen.getByRole('checkbox', { name: 'confined' }) as HTMLInputElement
const save = () => fireEvent.click(screen.getByRole('button', { name: /^save$/i }))
const sentSandbox = () => JSON.parse(vi.mocked(hostApi.hostFetch).mock.calls[0][2]!.body as string).nex.sandbox
const ok = (cfg: unknown) => vi.mocked(hostApi.hostFetch).mockResolvedValueOnce(new Response(JSON.stringify({ nex: cfg }), { status: 200 }))

beforeEach(() => {
  vi.mocked(hostApi.hostFetch).mockReset()
  useI18nStore.getState().setLocale('en')
})

describe('extra sandbox profiles: what the form shows', () => {
  it('lists each known opt-in profile with a one-line explanation, ticked by the loaded value', () => {
    render(<NexConfigForm hostId="h" config={withExtra(['confined'])} info={info} onSaved={() => {}} />)
    expect(confined().checked).toBe(true)
    expect(screen.getByText(/Stricter isolation/)).toBeInTheDocument()
  })

  it('is unticked when the host has none', () => {
    render(<NexConfigForm hostId="h" config={withExtra([])} info={info} onSaved={() => {}} />)
    expect(confined().checked).toBe(false)
  })

  it('shows a name the form does not know, ticked, beside the known ones', () => {
    render(<NexConfigForm hostId="h" config={withExtra(['confined', 'sealed'])} info={info} onSaved={() => {}} />)
    expect((screen.getByRole('checkbox', { name: 'sealed' }) as HTMLInputElement).checked).toBe(true)
    expect(screen.getByText(/Not a profile this form knows/)).toBeInTheDocument()
  })

  it('an older daemon that sends no extra_profiles reads as none', () => {
    const raw = { ...withExtra([]), sandbox: { max_profile: '', default_profile: '' } }
    render(<NexConfigForm hostId="h" config={normalizeNexConfig(raw as never)} info={info} onSaved={() => {}} />)
    expect(confined().checked).toBe(false)
  })

  it('a repeated name is shown once, and a non-array value reads as none', () => {
    expect(normalizeNexConfig({ sandbox: { max_profile: '', default_profile: '', extra_profiles: ['sealed', 'sealed', 'confined'] } }).sandbox.extra_profiles).toEqual(['sealed', 'confined'])
    expect(normalizeNexConfig({ sandbox: { max_profile: '', default_profile: '', extra_profiles: 'confined' as never } }).sandbox.extra_profiles).toEqual([])
  })

  it('an opt-in profile is never an option of the max / default selects', () => {
    expect(SANDBOX_PROFILES.some((p) => (EXTRA_SANDBOX_PROFILES as readonly string[]).includes(p))).toBe(false)
    render(<NexConfigForm hostId="h" config={withExtra(['confined'])} info={info} onSaved={() => {}} />)
    for (const label of [/max profile/i, /default profile/i]) {
      const values = Array.from((screen.getByLabelText(label) as HTMLSelectElement).options).map((o) => o.value)
      expect(values).not.toContain('confined')
    }
  })
})

describe('extra sandbox profiles: what is sent', () => {
  it('untouched: the loaded list goes back as it was, the key is always present', async () => {
    ok(withExtra(['confined']))
    render(<NexConfigForm hostId="h" config={withExtra(['confined'])} info={info} onSaved={() => {}} />)
    save()
    await waitFor(() => expect(hostApi.hostFetch).toHaveBeenCalled())
    expect(sentSandbox().extra_profiles).toEqual(['confined'])
  })

  it('an empty loaded list is sent as [] (not left out)', async () => {
    ok(withExtra([]))
    render(<NexConfigForm hostId="h" config={withExtra([])} info={info} onSaved={() => {}} />)
    save()
    await waitFor(() => expect(hostApi.hostFetch).toHaveBeenCalled())
    expect(sentSandbox()).toHaveProperty('extra_profiles', [])
  })

  it('ticking adds the name', async () => {
    ok(withExtra(['confined']))
    render(<NexConfigForm hostId="h" config={withExtra([])} info={info} onSaved={() => {}} />)
    fireEvent.click(confined())
    save()
    await waitFor(() => expect(hostApi.hostFetch).toHaveBeenCalled())
    expect(sentSandbox().extra_profiles).toEqual(['confined'])
  })

  it('unticking the only one sends [] (which clears it)', async () => {
    ok(withExtra([]))
    render(<NexConfigForm hostId="h" config={withExtra(['confined'])} info={info} onSaved={() => {}} />)
    fireEvent.click(confined())
    save()
    await waitFor(() => expect(hostApi.hostFetch).toHaveBeenCalled())
    expect(sentSandbox().extra_profiles).toEqual([])
  })

  it('an unknown name goes back with the save unless the reader unticks it', async () => {
    ok(withExtra(['confined', 'sealed']))
    render(<NexConfigForm hostId="h" config={withExtra(['confined', 'sealed'])} info={info} onSaved={() => {}} />)
    fireEvent.click(confined()) // another change; the unknown name must survive it
    save()
    await waitFor(() => expect(hostApi.hostFetch).toHaveBeenCalled())
    expect(sentSandbox().extra_profiles).toEqual(['sealed'])
  })

  it('unticking an unknown name removes it', async () => {
    ok(withExtra([]))
    render(<NexConfigForm hostId="h" config={withExtra(['sealed'])} info={info} onSaved={() => {}} />)
    fireEvent.click(screen.getByRole('checkbox', { name: 'sealed' }))
    save()
    await waitFor(() => expect(hostApi.hostFetch).toHaveBeenCalled())
    expect(sentSandbox().extra_profiles).toEqual([])
  })

  it('the saved answer is what the boxes show afterwards', async () => {
    ok(withExtra(['confined']))
    render(<NexConfigForm hostId="h" config={withExtra([])} info={info} onSaved={() => {}} />)
    fireEvent.click(confined())
    save()
    await waitFor(() => expect(screen.getByText(/^saved/i)).toBeInTheDocument())
    expect(confined().checked).toBe(true)
  })
})

describe('extra sandbox profiles: errors and the restart hint', () => {
  it('a 400 for the list shows under that field, the list index folded away', async () => {
    vi.mocked(hostApi.hostFetch).mockResolvedValueOnce(new Response('nex.sandbox.extra_profiles[1]: "sealed" is not a known opt-in profile', { status: 400 }))
    render(<NexConfigForm hostId="h" config={withExtra(['confined', 'sealed'])} info={info} onSaved={() => {}} />)
    save()
    await waitFor(() => expect(screen.getByTestId('field-error-sandbox.extra_profiles')).toHaveTextContent('"sealed" is not a known opt-in profile'))
    expect(screen.queryByTestId('nex-config-error')).not.toBeInTheDocument()
  })

  it('the restart hint lights from the daemon\'s own verdict after a change', () => {
    const { rerender } = render(<NexConfigForm hostId="h" config={withExtra(['confined'])} info={{ ...info, restart_required: false }} onSaved={() => {}} />)
    expect(screen.queryByTestId('nex-restart-required')).not.toBeInTheDocument()
    rerender(<NexConfigForm hostId="h" config={withExtra(['confined'])} info={{ ...info, restart_required: true }} onSaved={() => {}} />)
    expect(screen.getByTestId('nex-restart-required')).toBeInTheDocument()
  })

  it('the Traditional Chinese labels exist', () => {
    useI18nStore.getState().setLocale('zh-TW')
    render(<NexConfigForm hostId="h" config={withExtra(['confined'])} info={info} onSaved={() => {}} />)
    expect(screen.getByText('額外沙箱設定（opt-in）')).toBeInTheDocument()
  })
})
