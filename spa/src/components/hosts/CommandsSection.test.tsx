import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent, waitFor } from '@testing-library/react'

vi.mock('../../features/workspace/lib/icon-path-cache', () => ({
  isWeightLoaded: () => true, prefetchWeight: () => Promise.resolve(), getIconPath: () => 'M0,0',
}))
vi.mock('../../features/workspace/generated/icon-meta.json', () => ({
  default: [{ n: 'Terminal', t: ['cli'], c: [] }, { n: 'Rocket', t: ['launch'], c: [] }],
}))
vi.mock('../../lib/host-api', async (importOriginal) => ({
  ...(await importOriginal<typeof import('../../lib/host-api')>()),
  resolveShellCommand: vi.fn(async () => ({ status: 'resolved', detail: '/usr/local/bin/claude' })),
}))

import { CommandsSection } from './CommandsSection'
import { useHostStore } from '../../stores/useHostStore'
import { emptyHostConfigEntry, useHostConfigStore } from '../../stores/useHostConfigStore'
import { resolveShellCommand } from '../../lib/host-api'
import { HostConfigApiError, type HostCommand } from '../../lib/host-config-api'
import { MAX_CONFIG_ITEMS } from '../../lib/host-config-validate'

const H = 'h1'
const C1: HostCommand = { id: 'c1', name: 'Claude', command: 'claude', icon: { kind: 'agent', value: 'cc-bot' } }
const C2: HostCommand = { id: 'c2', name: 'Logs', command: 'tail -f log', icon: { kind: 'phosphor', value: 'Rocket' } }
const saveCommands = vi.fn()

beforeEach(() => {
  saveCommands.mockReset().mockImplementation(async (hostId: string, items: HostCommand[]) => {
    useHostConfigStore.setState((s) => ({ byHost: { ...s.byHost, [hostId]: { ...s.byHost[hostId], commands: items } } }))
  })
  vi.mocked(resolveShellCommand).mockClear()
  useHostStore.setState({
    hosts: { [H]: { id: H, name: 'mlab', ip: '1.2.3.4', port: 7860, order: 0 } },
    hostOrder: [H], runtime: { [H]: { status: 'connected' } },
  })
  useHostConfigStore.setState({
    byHost: { [H]: { ...emptyHostConfigEntry('ready'), commands: [C1, C2] } },
    load: vi.fn(async () => {}),
    saveCommands,
  })
})

describe('CommandsSection', () => {
  it('Normal tab lists commands with icon, name and mono command', () => {
    render(<CommandsSection hostId={H} />)
    expect(screen.getAllByTestId(/^command-row-/).map((r) => r.dataset.testid)).toEqual(['command-row-c1', 'command-row-c2'])
    expect(screen.getByTestId('command-row-c2').querySelector('[data-testid="command-icon"]')).toHaveAttribute('data-value', 'Rocket')
    expect(screen.getByText('tail -f log')).toHaveClass('font-mono')
  })

  it('adds a command with a picked icon; the command-word check runs on this host and never blocks', async () => {
    vi.mocked(resolveShellCommand).mockResolvedValueOnce({ status: 'unresolved', reason: 'not_found' })
    render(<CommandsSection hostId={H} />)
    fireEvent.click(screen.getByTestId('command-add'))
    fireEvent.change(screen.getByTestId('command-field-name'), { target: { value: 'Codex' } })
    fireEvent.change(screen.getByTestId('command-field-command'), { target: { value: 'FOO=1 codex --yolo' } })
    fireEvent.click(screen.getByTestId('command-word-test'))
    await waitFor(() => expect(resolveShellCommand).toHaveBeenCalledWith(H, 'codex'))
    expect(await screen.findByTestId('command-word-verdict')).toHaveAttribute('data-status', 'unresolved')
    fireEvent.click(screen.getByTestId('command-icon-agent-codex'))
    fireEvent.click(screen.getByTestId('command-save'))
    await waitFor(() => expect(saveCommands).toHaveBeenCalledTimes(1))
    const saved = saveCommands.mock.calls[0][1] as HostCommand[]
    expect(saved[2]).toMatchObject({ name: 'Codex', command: 'FOO=1 codex --yolo', icon: { kind: 'agent', value: 'codex' } })
    await waitFor(() => expect(screen.queryByTestId('command-dialog')).toBeNull())
  })

  it('a new command defaults to the Terminal icon; empty fields are rejected', () => {
    render(<CommandsSection hostId={H} />)
    fireEvent.click(screen.getByTestId('command-add'))
    expect(screen.getByTestId('command-dialog').querySelector('[data-testid="command-icon"]')).toHaveAttribute('data-value', 'Terminal')
    fireEvent.click(screen.getByTestId('command-save'))
    expect(screen.getByTestId('command-error-name')).toBeInTheDocument()
    expect(screen.getByTestId('command-error-command')).toBeInTheDocument()
    expect(saveCommands).not.toHaveBeenCalled()
  })

  it('reorders and deletes', async () => {
    render(<CommandsSection hostId={H} />)
    fireEvent.click(screen.getByTestId('command-up-c2'))
    await waitFor(() => expect(saveCommands).toHaveBeenLastCalledWith(H, [C2, C1]))
    await waitFor(() => expect(screen.getByTestId('command-delete-c1')).toBeEnabled())
    fireEvent.click(screen.getByTestId('command-delete-c1'))
    fireEvent.click(screen.getByTestId('command-delete-confirm-c1'))
    await waitFor(() => expect(saveCommands).toHaveBeenLastCalledWith(H, [C2]))
  })

  it('a daemon 400 keeps the dialog open and shows the body text inline (amendment A3)', async () => {
    saveCommands.mockRejectedValue(new HostConfigApiError(400, 'commands[0].command: contains NUL'))
    render(<CommandsSection hostId={H} />)
    fireEvent.click(screen.getByTestId('command-edit-c1'))
    fireEvent.click(screen.getByTestId('command-save'))
    expect(await screen.findByTestId('command-dialog-error')).toHaveTextContent('commands[0].command: contains NUL')
    expect(screen.getByTestId('command-dialog')).toBeInTheDocument()
    expect(screen.queryByTestId('commands-save-error')).not.toBeInTheDocument()
  })

  // How a failure is WORDED is the shared hook's (useHostConfigCollection); what
  // this owns is where it is rendered.
  it('a daemon 400 on a row action shows the body text in the list', async () => {
    saveCommands.mockRejectedValueOnce(new HostConfigApiError(400, 'duplicate id'))
    render(<CommandsSection hostId={H} />)
    fireEvent.click(screen.getByTestId('command-down-c1'))
    expect(await screen.findByTestId('commands-save-error')).toHaveTextContent('duplicate id')
  })

  it('adding is disabled at the item limit', () => {
    useHostConfigStore.setState({
      byHost: { [H]: { ...emptyHostConfigEntry('ready'), commands: Array.from({ length: MAX_CONFIG_ITEMS }, (_, i) => ({ ...C1, id: `c${i}` })) } },
    })
    render(<CommandsSection hostId={H} />)
    expect(screen.getByTestId('command-add')).toBeDisabled()
    expect(screen.getByTestId('commands-limit')).toBeInTheDocument()
  })

  it('Resume tab renders the per-host template editor without a host picker', () => {
    render(<CommandsSection hostId={H} />)
    fireEvent.click(screen.getByTestId('commands-tab-resume'))
    expect(screen.getByTestId('resume-templates')).toBeInTheDocument()
    expect(screen.queryByTestId('resume-template-host')).toBeNull()
    expect(screen.queryByTestId('command-add')).toBeNull()
  })

  it('old daemon → notice and disabled editing on both tabs', () => {
    useHostConfigStore.setState({ byHost: { [H]: emptyHostConfigEntry('unsupported') } })
    render(<CommandsSection hostId={H} />)
    expect(screen.getByTestId('host-config-notice')).toHaveAttribute('data-notice', 'host_config.unsupported')
    expect(screen.getByTestId('command-add')).toBeDisabled()
    fireEvent.click(screen.getByTestId('commands-tab-resume'))
    expect(screen.getByTestId('resume-template-input-cc-exact')).toBeDisabled()
  })
})
