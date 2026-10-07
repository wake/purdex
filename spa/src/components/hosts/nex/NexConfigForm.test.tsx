import { describe, it, expect, beforeEach, vi } from 'vitest'
import { render, screen, fireEvent, waitFor, act, within } from '@testing-library/react'
import NexConfigForm from './NexConfigForm'
import { emptyNexConfig, normalizeNexConfig } from './nex-config-diff'
import * as hostApi from '../../../lib/host-api'
import { useDaemonRestartStore } from '../../../stores/useDaemonRestartStore'
import { useI18nStore } from '../../../stores/useI18nStore'

vi.mock('../../../lib/host-api', async () => {
  const actual = await vi.importActual<typeof import('../../../lib/host-api')>('../../../lib/host-api')
  return { ...actual, hostFetch: vi.fn() }
})

const info = { configured: true, mounted: true, ready: true, init_error: '', effective: { data_dir: '/d', claude_bin: '', max_profile: 'handoff', default_profile: 'standard', repo_roots: ['/a'], service_roots: [], path_prefix: '', lease_ttl: '2m0s', interrupt: '10s', turn: '5m0s' } }
const saved = { ...emptyNexConfig(), enabled: true, repo_roots: ['/a'], sandbox: { max_profile: 'handoff', default_profile: 'standard' } }

beforeEach(() => vi.mocked(hostApi.hostFetch).mockReset())

describe('NexConfigForm', () => {
  it('mirrors the saved config into the fields', () => {
    render(<NexConfigForm hostId="h" config={saved} info={info} onSaved={() => {}} />)
    expect((screen.getByLabelText(/enabled/i) as HTMLInputElement).checked).toBe(true)
    expect(screen.getByDisplayValue('/a')).toBeInTheDocument()
    expect((screen.getByLabelText(/max profile/i) as HTMLSelectElement).value).toBe('handoff')
  })

  it('PUTs the whole nex object and shows Saved; the restart notice follows info.restart_required', async () => {
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
    expect(screen.getByText(/^saved/i)).toBeInTheDocument()
    expect(screen.queryByTestId('nex-restart-required')).not.toBeInTheDocument()
  })

  it('shows the restart notice while info.restart_required is true, even with a ~ root the engine expanded', () => {
    const tildeSaved = { ...saved, repo_roots: ['~/Workspace'] }
    const { rerender } = render(<NexConfigForm hostId="h" config={tildeSaved} info={{ ...info, restart_required: false }} onSaved={() => {}} />)
    expect(screen.queryByTestId('nex-restart-required')).not.toBeInTheDocument()
    rerender(<NexConfigForm hostId="h" config={tildeSaved} info={{ ...info, restart_required: true }} onSaved={() => {}} />)
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
    expect(Object.keys(body.nex).sort()).toEqual(['claude_bin', 'enabled', 'path_prepend', 'peer', 'repo_roots', 'sandbox', 'service_roots', 'timeouts'])
    expect(Object.keys(body.nex.sandbox).sort()).toEqual(['default_profile', 'max_profile'])
    expect(Object.keys(body.nex.timeouts).sort()).toEqual(['interrupt', 'lease_ttl', 'turn'])
    expect(Object.keys(body.nex.peer).sort()).toEqual(['enabled', 'max_pending', 'reply_line', 'wake_template'])
    // U4: an empty form still sends the mailbox on, with Nexen's defaults.
    expect(body.nex.peer).toEqual({ enabled: true, max_pending: 0, wake_template: '', reply_line: '' })
  })

  // The UI never edits the two templates, but PUT replaces the whole [nex]
  // section — so whatever GET returned must go back byte for byte (no trim,
  // no newline folding), or saving the form would silently reset them.
  it('PUTs back two non-empty templates loaded by GET byte-identical', async () => {
    const wake = '  Peer {{.FromName}} ({{.FromMode}}) wrote:\n\n{{.Text}}\n\t{{.ReplyLine}}  \n'
    const reply = ' reply → pdx msg send {{.ReplyTo}} "<text>" '
    const fromGet = { ...saved, peer: { enabled: false, max_pending: 9, wake_template: wake, reply_line: reply } }
    vi.mocked(hostApi.hostFetch).mockResolvedValueOnce(new Response(JSON.stringify({ nex: fromGet }), { status: 200 }))
    render(<NexConfigForm hostId="h" config={normalizeNexConfig(JSON.parse(JSON.stringify(fromGet)))} info={info} onSaved={() => {}} />)
    fireEvent.change(screen.getByLabelText(/turn timeout/i), { target: { value: '5m' } })
    fireEvent.click(screen.getByRole('button', { name: /save/i }))
    await waitFor(() => expect(hostApi.hostFetch).toHaveBeenCalled())
    const body = JSON.parse(vi.mocked(hostApi.hostFetch).mock.calls[0][2]!.body as string)
    expect(body.nex.peer.wake_template).toBe(wake)
    expect(body.nex.peer.reply_line).toBe(reply)
    expect(body.nex.peer).toEqual({ enabled: false, max_pending: 9, wake_template: wake, reply_line: reply })
  })

  it('mirrors the peer mailbox toggle and queue limit, and PUTs the edits', async () => {
    vi.mocked(hostApi.hostFetch).mockResolvedValueOnce(new Response(JSON.stringify({ nex: saved }), { status: 200 }))
    const withPeer = { ...saved, peer: { enabled: true, max_pending: 0, wake_template: '', reply_line: '' } }
    render(<NexConfigForm hostId="h" config={withPeer} info={info} onSaved={() => {}} />)
    const toggle = screen.getByLabelText(/peer mailbox/i) as HTMLInputElement
    const limit = screen.getByLabelText(/queue limit/i) as HTMLInputElement
    expect(toggle.checked).toBe(true)
    expect(limit.value).toBe('0')
    expect(screen.getByText('0 = default')).toBeInTheDocument()

    fireEvent.click(toggle)
    fireEvent.change(limit, { target: { value: '8' } })
    fireEvent.click(screen.getByRole('button', { name: /save/i }))
    await waitFor(() => expect(hostApi.hostFetch).toHaveBeenCalled())
    const body = JSON.parse(vi.mocked(hostApi.hostFetch).mock.calls[0][2]!.body as string)
    expect(body.nex.peer.enabled).toBe(false)
    expect(body.nex.peer.max_pending).toBe(8)
  })

  it('a cleared queue limit is sent as 0 (Nexen default)', async () => {
    vi.mocked(hostApi.hostFetch).mockResolvedValueOnce(new Response(JSON.stringify({ nex: saved }), { status: 200 }))
    const withPeer = { ...saved, peer: { enabled: true, max_pending: 12, wake_template: '', reply_line: '' } }
    render(<NexConfigForm hostId="h" config={withPeer} info={info} onSaved={() => {}} />)
    fireEvent.change(screen.getByLabelText(/queue limit/i), { target: { value: '' } })
    fireEvent.click(screen.getByRole('button', { name: /save/i }))
    await waitFor(() => expect(hostApi.hostFetch).toHaveBeenCalled())
    const body = JSON.parse(vi.mocked(hostApi.hostFetch).mock.calls[0][2]!.body as string)
    expect(body.nex.peer.max_pending).toBe(0)
  })

  // Only a cleared field means "Nexen default" (0). Anything else must be a
  // safe non-negative integer as Number() reads it — parseInt would quietly
  // turn 1.5 or 1e3 into 1. An invalid entry stays in the field, is flagged
  // there, and is never sent.
  it.each([
    ['a decimal', '1.5'],
    ['beyond the safe integer range', '9007199254740993'],
    ['a negative number', '-1'],
    ['not a number', 'abc'],
  ])('keeps %s (%s) in the field, flags it, and does not send', async (_label, typed) => {
    const withPeer = { ...saved, peer: { enabled: true, max_pending: 12, wake_template: '', reply_line: '' } }
    render(<NexConfigForm hostId="h" config={withPeer} info={info} onSaved={() => {}} />)
    const limit = screen.getByLabelText(/queue limit/i) as HTMLInputElement
    fireEvent.change(limit, { target: { value: typed } })
    expect(limit.value).toBe(typed)
    expect(screen.getByTestId('field-error-peer.max_pending')).toHaveTextContent(/whole number/i)
    fireEvent.click(screen.getByRole('button', { name: /save/i }))
    await act(async () => {})
    expect(hostApi.hostFetch).not.toHaveBeenCalled()
  })

  it('reads scientific notation as Number does (1e3 = 1000) and sends it', async () => {
    vi.mocked(hostApi.hostFetch).mockResolvedValueOnce(new Response(JSON.stringify({ nex: saved }), { status: 200 }))
    render(<NexConfigForm hostId="h" config={saved} info={info} onSaved={() => {}} />)
    fireEvent.change(screen.getByLabelText(/queue limit/i), { target: { value: '1e3' } })
    expect(screen.queryByTestId('field-error-peer.max_pending')).not.toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: /save/i }))
    await waitFor(() => expect(hostApi.hostFetch).toHaveBeenCalled())
    const body = JSON.parse(vi.mocked(hostApi.hostFetch).mock.calls[0][2]!.body as string)
    expect(body.nex.peer.max_pending).toBe(1000)
  })

  it('an invalid queue limit corrected to a valid one clears the flag and is sent', async () => {
    vi.mocked(hostApi.hostFetch).mockResolvedValueOnce(new Response(JSON.stringify({ nex: saved }), { status: 200 }))
    render(<NexConfigForm hostId="h" config={saved} info={info} onSaved={() => {}} />)
    const limit = screen.getByLabelText(/queue limit/i)
    fireEvent.change(limit, { target: { value: '1.5' } })
    expect(screen.getByTestId('field-error-peer.max_pending')).toBeInTheDocument()
    fireEvent.change(limit, { target: { value: '7' } })
    expect(screen.queryByTestId('field-error-peer.max_pending')).not.toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: /save/i }))
    await waitFor(() => expect(hostApi.hostFetch).toHaveBeenCalled())
    const body = JSON.parse(vi.mocked(hostApi.hostFetch).mock.calls[0][2]!.body as string)
    expect(body.nex.peer.max_pending).toBe(7)
  })

  it('shows a peer.max_pending 400 next to the queue limit', async () => {
    vi.mocked(hostApi.hostFetch).mockResolvedValueOnce(new Response('nex.peer.max_pending: must not be negative (got -1)', { status: 400 }))
    render(<NexConfigForm hostId="h" config={saved} info={info} onSaved={() => {}} />)
    fireEvent.click(screen.getByRole('button', { name: /save/i }))
    await waitFor(() => expect(screen.getByTestId('field-error-peer.max_pending')).toHaveTextContent(/must not be negative/))
  })

  // The templates have no field in the form, so their 400 must not be
  // attached to an invisible slot: it goes on the general error line, key
  // included, so the user can tell which template is wrong.
  it('shows a template 400 on the general error line, naming the key', async () => {
    vi.mocked(hostApi.hostFetch).mockResolvedValueOnce(new Response('nex.peer.wake_template: {{.Text}} must appear exactly once, found 0', { status: 400 }))
    render(<NexConfigForm hostId="h" config={saved} info={info} onSaved={() => {}} />)
    fireEvent.click(screen.getByRole('button', { name: /save/i }))
    await waitFor(() => expect(screen.getByTestId('nex-config-error')).toHaveTextContent('nex.peer.wake_template: {{.Text}} must appear exactly once'))
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
    // Nor should it claim the (never-sent) edit was saved.
    expect(screen.queryByText(/^saved/i)).not.toBeInTheDocument()

    vi.mocked(hostApi.hostFetch).mockResolvedValueOnce(new Response(JSON.stringify({ nex: { ...saved, claude_bin: '/new/claude' } }), { status: 200 }))
    fireEvent.click(screen.getByRole('button', { name: /save/i }))
    await waitFor(() => expect(hostApi.hostFetch).toHaveBeenCalledTimes(2))
    const [, , secondInit] = vi.mocked(hostApi.hostFetch).mock.calls[1]
    const secondBody = JSON.parse(secondInit!.body as string)
    expect(secondBody.nex.claude_bin).toBe('/new/claude')
  })
})

describe('NexConfigForm save across a host change', () => {
  function deferredResponse() {
    let resolve!: (r: Response) => void
    const promise = new Promise<Response>((r) => { resolve = r })
    return { promise, resolve }
  }

  it('ignores a PUT response for host A once the form shows host B', async () => {
    const pending = deferredResponse()
    vi.mocked(hostApi.hostFetch).mockReturnValueOnce(pending.promise)
    const onSaved = vi.fn()
    const { rerender } = render(<NexConfigForm hostId="a" config={saved} info={info} onSaved={onSaved} />)
    fireEvent.click(screen.getByRole('button', { name: /save/i }))
    await waitFor(() => expect(hostApi.hostFetch).toHaveBeenCalledTimes(1))

    const hostB = { ...emptyNexConfig(), enabled: false, repo_roots: ['/b'], sandbox: { max_profile: 'trusted', default_profile: 'trusted' } }
    rerender(<NexConfigForm hostId="b" config={hostB} info={info} onSaved={onSaved} />)

    await act(async () => {
      pending.resolve(new Response(JSON.stringify({ nex: { ...saved, sandbox: { max_profile: 'handoff', default_profile: 'readonly' } } }), { status: 200 }))
      await pending.promise
    })

    expect(onSaved).not.toHaveBeenCalled()
    expect(screen.queryByText(/^saved/i)).not.toBeInTheDocument()
    expect(screen.getByDisplayValue('/b')).toBeInTheDocument()
    expect((screen.getByLabelText(/default profile/i) as HTMLSelectElement).value).toBe('trusted')
  })

  it('ignores a PUT response after unmount', async () => {
    const pending = deferredResponse()
    vi.mocked(hostApi.hostFetch).mockReturnValueOnce(pending.promise)
    const onSaved = vi.fn()
    const { unmount } = render(<NexConfigForm hostId="a" config={saved} info={info} onSaved={onSaved} />)
    fireEvent.click(screen.getByRole('button', { name: /save/i }))
    await waitFor(() => expect(hostApi.hostFetch).toHaveBeenCalledTimes(1))
    unmount()
    await act(async () => {
      pending.resolve(new Response(JSON.stringify({ nex: saved }), { status: 200 }))
      await pending.promise
    })
    expect(onSaved).not.toHaveBeenCalled()
  })
})

describe('restart now (R3)', () => {
  beforeEach(() => {
    useI18nStore.getState().setLocale('zh-TW')
    useDaemonRestartStore.setState({ restarting: {}, settled: {} })
  })

  it('appears inside the hint only when restart_required', () => {
    render(<NexConfigForm hostId="h" config={saved} info={{ ...info, restart_required: true }} onSaved={() => {}} />)
    const hint = screen.getByTestId('nex-restart-required')
    expect(within(hint).getByTestId('nex-restart-now').textContent).toBe('立即重啟')
  })

  it('absent without restart_required', () => {
    render(<NexConfigForm hostId="h" config={saved} info={{ ...info, restart_required: false }} onSaved={() => {}} />)
    expect(screen.queryByTestId('nex-restart-now')).toBeNull()
  })

  it('disabled while this host restarts', () => {
    useDaemonRestartStore.setState({ restarting: { h1: true } })
    render(<NexConfigForm hostId="h1" config={saved} info={{ ...info, restart_required: true }} onSaved={() => {}} />)
    expect((screen.getByTestId('nex-restart-now') as HTMLButtonElement).disabled).toBe(true)
  })
})
