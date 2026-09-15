import { describe, it, expect, beforeEach, vi } from 'vitest'
import { render, screen, fireEvent, waitFor } from '@testing-library/react'
import NexConfigForm from './NexConfigForm'
import { emptyNexConfig } from './nex-config-diff'
import * as hostApi from '../../../lib/host-api'

vi.mock('../../../lib/host-api', async () => {
  const actual = await vi.importActual<typeof import('../../../lib/host-api')>('../../../lib/host-api')
  return { ...actual, hostFetch: vi.fn() }
})

const info = { configured: true, mounted: true, ready: true, init_error: '', effective: { data_dir: '/d', claude_bin: '', cswap_bin: '', max_profile: 'handoff', default_profile: 'standard', repo_roots: ['/a'], service_roots: [], path_prefix: '', lease_ttl: '2m0s', interrupt: '10s', turn: '5m0s' } }
const saved = { ...emptyNexConfig(), enabled: true, repo_roots: ['/a'], sandbox: { max_profile: 'handoff', default_profile: 'standard' } }

beforeEach(() => vi.mocked(hostApi.hostFetch).mockReset())

describe('NexConfigForm', () => {
  it('mirrors the saved config into the fields', () => {
    render(<NexConfigForm hostId="h" config={saved} info={info} onSaved={() => {}} />)
    expect((screen.getByLabelText(/enabled/i) as HTMLInputElement).checked).toBe(true)
    expect(screen.getByDisplayValue('/a')).toBeInTheDocument()
    expect((screen.getByLabelText(/max profile/i) as HTMLSelectElement).value).toBe('handoff')
  })

  it('PUTs the whole nex object and shows restart-required after a change', async () => {
    vi.mocked(hostApi.hostFetch).mockResolvedValueOnce(new Response(JSON.stringify({ nex: { ...saved, sandbox: { max_profile: 'handoff', default_profile: 'readonly' } } }), { status: 200 }))
    const onSaved = vi.fn()
    render(<NexConfigForm hostId="h" config={saved} info={info} onSaved={onSaved} />)
    fireEvent.change(screen.getByLabelText(/default profile/i), { target: { value: 'readonly' } })
    fireEvent.click(screen.getByRole('button', { name: /save/i }))
    await waitFor(() => expect(hostApi.hostFetch).toHaveBeenCalled())
    const [, path, init] = vi.mocked(hostApi.hostFetch).mock.calls[0]
    expect(path).toBe('/api/config')
    expect(init?.method).toBe('PUT')
    const body = JSON.parse(init!.body as string)
    expect(body.nex.sandbox.default_profile).toBe('readonly')
    expect(body.nex.repo_roots).toEqual(['/a'])
    await waitFor(() => expect(onSaved).toHaveBeenCalled())
    expect(screen.getByTestId('nex-restart-required')).toBeInTheDocument()
  })

  it('shows the validator message next to the offending field on 400', async () => {
    vi.mocked(hostApi.hostFetch).mockResolvedValueOnce(new Response('nex.timeouts.turn: time: invalid duration "soon"', { status: 400 }))
    render(<NexConfigForm hostId="h" config={saved} info={info} onSaved={() => {}} />)
    fireEvent.change(screen.getByLabelText(/turn timeout/i), { target: { value: 'soon' } })
    fireEvent.click(screen.getByRole('button', { name: /save/i }))
    await waitFor(() => expect(screen.getByTestId('field-error-timeouts.turn')).toHaveTextContent(/invalid duration/))
  })

  it('adds and removes list entries', () => {
    render(<NexConfigForm hostId="h" config={saved} info={info} onSaved={() => {}} />)
    fireEvent.click(screen.getAllByRole('button', { name: /^add$/i })[0]) // repo roots
    const inputs = screen.getAllByPlaceholderText('/absolute/path')
    fireEvent.change(inputs[inputs.length - 1], { target: { value: '/b' } })
    expect(screen.getByDisplayValue('/b')).toBeInTheDocument()
    fireEvent.click(screen.getAllByRole('button', { name: /remove/i })[0])
    expect(screen.queryByDisplayValue('/a')).not.toBeInTheDocument()
  })

  it('PUT body carries every top-level and nested key even when the config is empty', async () => {
    vi.mocked(hostApi.hostFetch).mockResolvedValueOnce(new Response(JSON.stringify({ nex: emptyNexConfig() }), { status: 200 }))
    render(<NexConfigForm hostId="h" config={undefined} info={null} onSaved={() => {}} />)
    fireEvent.click(screen.getByRole('button', { name: /save/i }))
    await waitFor(() => expect(hostApi.hostFetch).toHaveBeenCalled())
    const [, , init] = vi.mocked(hostApi.hostFetch).mock.calls[0]
    const body = JSON.parse(init!.body as string)
    expect(Object.keys(body.nex).sort()).toEqual(['claude_bin', 'cswap_bin', 'enabled', 'path_prepend', 'repo_roots', 'sandbox', 'service_roots', 'timeouts'])
    expect(Object.keys(body.nex.sandbox).sort()).toEqual(['default_profile', 'max_profile'])
    expect(Object.keys(body.nex.timeouts).sort()).toEqual(['interrupt', 'lease_ttl', 'turn'])
  })

  it('shows the Enabled label exactly once', () => {
    render(<NexConfigForm hostId="h" config={saved} info={info} onSaved={() => {}} />)
    expect(screen.getAllByText('Enabled')).toHaveLength(1)
  })

  it('keeps an edit made while a save is in flight, and sends it on the next save', async () => {
    let resolveFetch: (value: Response) => void = () => {}
    const pending = new Promise<Response>((resolve) => { resolveFetch = resolve })
    vi.mocked(hostApi.hostFetch).mockReturnValueOnce(pending)
    const onSaved = vi.fn()
    render(<NexConfigForm hostId="h" config={saved} info={info} onSaved={onSaved} />)

    fireEvent.click(screen.getByRole('button', { name: /save/i }))
    // Edit while the first save is still pending.
    fireEvent.change(screen.getByLabelText(/claude binary/i), { target: { value: '/new/claude' } })

    resolveFetch(new Response(JSON.stringify({ nex: saved }), { status: 200 }))
    await waitFor(() => expect(onSaved).toHaveBeenCalledTimes(1))

    // The in-flight response must not clobber the edit made during the save.
    expect((screen.getByLabelText(/claude binary/i) as HTMLInputElement).value).toBe('/new/claude')

    vi.mocked(hostApi.hostFetch).mockResolvedValueOnce(new Response(JSON.stringify({ nex: { ...saved, claude_bin: '/new/claude' } }), { status: 200 }))
    fireEvent.click(screen.getByRole('button', { name: /save/i }))
    await waitFor(() => expect(hostApi.hostFetch).toHaveBeenCalledTimes(2))
    const [, , secondInit] = vi.mocked(hostApi.hostFetch).mock.calls[1]
    const secondBody = JSON.parse(secondInit!.body as string)
    expect(secondBody.nex.claude_bin).toBe('/new/claude')
  })
})
