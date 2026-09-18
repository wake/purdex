import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent, waitFor, act } from '@testing-library/react'

vi.mock('../../features/workspace/lib/icon-path-cache', () => ({
  isWeightLoaded: () => true, prefetchWeight: () => Promise.resolve(), getIconPath: () => 'M0,0',
}))
const setLocation = vi.fn()
vi.mock('wouter', async (importOriginal) => ({
  ...(await importOriginal<typeof import('wouter')>()),
  useLocation: () => ['/', setLocation],
}))

import { SessionLauncher } from './SessionLauncher'
import { SEND_UNSUPPORTED } from '../../lib/session-launch'
import { emptyHostConfigEntry, useHostConfigStore } from '../../stores/useHostConfigStore'
import { useHostStore } from '../../stores/useHostStore'
import { useSessionStore } from '../../stores/useSessionStore'
import { useUndoToast } from '../../stores/useUndoToast'
import type { HostCommand, HostProject } from '../../lib/host-config-api'
import type { Session } from '../../lib/host-api'

const H = 'h1'
const P1: HostProject = { id: 'p1', name: 'Purdex', slug: 'purdex', path: '~/w/purdex' }
const P2: HostProject = { id: 'p2', name: 'Ploom', slug: 'ploom', path: '/srv/ploom' }
const C1: HostCommand = { id: 'c1', name: 'Claude', command: 'claude', icon: { kind: 'agent', value: 'cc-bot' } }
const C2: HostCommand = { id: 'c2', name: 'Codex', command: 'codex', icon: { kind: 'agent', value: 'codex' } }
const made: Session = { code: 'new1', name: 'x', cwd: '~', mode: 'terminal', tmux_instance: '1:1' }

const onLaunched = vi.fn()
const onCancel = vi.fn()
const launch = vi.fn()

function seed(status: 'ready' | 'unsupported' = 'ready', projects = [P1, P2], commands = [C1, C2]) {
  useHostConfigStore.setState({
    byHost: { [H]: { ...emptyHostConfigEntry(status), projects, commands } },
    ensureLoaded: vi.fn(async () => {}),
  })
}

/** The host page's "live" shape: configured, connected, tmux usable. */
function seedHost(runtime: Partial<{ status: string; tmuxState: string }> = {}) {
  useHostStore.setState({
    hosts: { [H]: { id: H, name: 'Mini', ip: '127.0.0.1', port: 7860, order: 0 } },
    runtime: { [H]: { status: 'connected', tmuxState: 'ok', ...runtime } as never },
  })
}

function renderLauncher(disabled = false) {
  return render(<SessionLauncher hostId={H} disabled={disabled} onLaunched={onLaunched} onCancel={onCancel} launch={launch} />)
}

beforeEach(() => {
  onLaunched.mockReset(); onCancel.mockReset(); setLocation.mockReset()
  launch.mockReset().mockResolvedValue({ status: 'created', session: made })
  useSessionStore.setState({ sessions: { [H]: [] } })
  useUndoToast.setState({ toast: null })
  seedHost()
  seed()
})

describe('SessionLauncher', () => {
  it('autofocuses the name input and uses a ≥2-column container-query grid', () => {
    renderLauncher()
    expect(screen.getByTestId('launcher-name')).toHaveFocus()
    expect(screen.getByTestId('launcher').className).toContain('@container')
    const grid = screen.getByTestId('launcher-grid').className
    expect(grid).toContain('grid-cols-2')
    expect(grid).toContain('@md:grid-cols-3')
    expect(grid).toContain('@3xl:grid-cols-4')
  })

  // The card is one row now: name + icons. Path/slug/command names moved to
  // HoverTooltip, so neither the path line nor the native titles remain.
  it('renders every project with its name and every command icon, and prints no path or title', () => {
    renderLauncher()
    expect(screen.getByTestId('launcher-project-name-p1')).toHaveTextContent('Purdex')
    expect(screen.getByTestId('launcher-project-p2').textContent).not.toContain('/srv/ploom')
    expect(screen.queryByTitle('/srv/ploom')).toBeNull()
    expect(screen.getByTestId('launcher-command-p2-c2')).not.toHaveAttribute('title')
    expect(screen.getAllByTestId(/^launcher-command-(?!tip-)/)).toHaveLength(4)
  })

  it('hovering a project reveals a tooltip with name \u00b7 slug \u00b7 path after the 800ms delay', () => {
    renderLauncher()
    vi.useFakeTimers()
    try {
      expect(screen.getByTestId('launcher-project-tip-p1').className).toContain('opacity-0')
      // A3: the tooltip hangs off a never-disabled wrapper, not the button.
      fireEvent.mouseEnter(screen.getByTestId('launcher-project-name-p1').parentElement!)
      act(() => { vi.advanceTimersByTime(800) })
      const tip = screen.getByTestId('launcher-project-tip-p1')
      expect(tip.className).toContain('opacity-100')
      expect(tip.textContent).toBe('Purdex \u00b7 purdex \u00b7 ~/w/purdex')
    } finally {
      vi.useRealTimers()
    }
  })

  it('hovering a command icon reveals a tooltip with the command name after the 800ms delay', () => {
    renderLauncher()
    vi.useFakeTimers()
    try {
      expect(screen.getByTestId('launcher-command-tip-p2-c2').className).toContain('opacity-0')
      fireEvent.mouseEnter(screen.getByTestId('launcher-command-p2-c2').parentElement!)
      act(() => { vi.advanceTimersByTime(800) })
      const tip = screen.getByTestId('launcher-command-tip-p2-c2')
      expect(tip.className).toContain('opacity-100')
      expect(tip.textContent).toBe('Codex')
    } finally {
      vi.useRealTimers()
    }
  })

  it('Enter in the name input launches name + ~ (no project, no command)', async () => {
    renderLauncher()
    fireEvent.change(screen.getByTestId('launcher-name'), { target: { value: 'dev' } })
    fireEvent.keyDown(screen.getByTestId('launcher-name'), { key: 'Enter' })
    await waitFor(() => expect(onLaunched).toHaveBeenCalledWith(made))
    expect(launch).toHaveBeenCalledWith(H, { name: 'dev' })
  })

  it('Enter with an empty or invalid name shows inline validation and launches nothing', () => {
    renderLauncher()
    fireEvent.keyDown(screen.getByTestId('launcher-name'), { key: 'Enter' })
    expect(screen.getByTestId('launcher-name-error')).toBeInTheDocument()
    fireEvent.change(screen.getByTestId('launcher-name'), { target: { value: 'a b' } })
    expect(screen.getByTestId('launcher-name-error')).toBeInTheDocument()
    fireEvent.keyDown(screen.getByTestId('launcher-name'), { key: 'Enter' })
    expect(launch).not.toHaveBeenCalled()
  })

  it('clicking a command icon launches {project, command} with the typed name', async () => {
    renderLauncher()
    fireEvent.change(screen.getByTestId('launcher-name'), { target: { value: 'mine' } })
    fireEvent.click(screen.getByTestId('launcher-command-p1-c2'))
    await waitFor(() => expect(onLaunched).toHaveBeenCalled())
    expect(launch).toHaveBeenCalledWith(H, { name: 'mine', project: P1, command: C2 })
  })

  it('clicking a project name launches {project} only', async () => {
    renderLauncher()
    fireEvent.click(screen.getByTestId('launcher-project-name-p2'))
    await waitFor(() => expect(onLaunched).toHaveBeenCalled())
    expect(launch).toHaveBeenCalledWith(H, { name: '', project: P2 })
  })

  it('create failure stays open with an inline error', async () => {
    launch.mockResolvedValue({ status: 'failed', reason: 'create_failed', error: '409 Conflict' })
    renderLauncher()
    fireEvent.click(screen.getByTestId('launcher-project-name-p1'))
    expect(await screen.findByTestId('launcher-error')).toHaveTextContent('409 Conflict')
    expect(onLaunched).not.toHaveBeenCalled()
  })

  it('send failure toasts "created but command failed" and still lands in the session', async () => {
    launch.mockResolvedValue({ status: 'created', session: made, sendError: 'boom' })
    renderLauncher()
    fireEvent.click(screen.getByTestId('launcher-command-p1-c1'))
    await waitFor(() => expect(onLaunched).toHaveBeenCalledWith(made))
    expect(useUndoToast.getState().toast?.message).toContain('command failed to send')
  })

  it('old daemon: the command was never sent — its own localized toast, no raw internal error', async () => {
    launch.mockResolvedValue({ status: 'created', session: made, sendError: SEND_UNSUPPORTED })
    renderLauncher()
    fireEvent.click(screen.getByTestId('launcher-command-p1-c1'))
    await waitFor(() => expect(onLaunched).toHaveBeenCalledWith(made))
    const message = useUndoToast.getState().toast?.message ?? ''
    expect(message).toContain('too old to run the command')
    expect(message).not.toContain(SEND_UNSUPPORTED)
  })

  it('busy disables every item and a second click does not launch twice', async () => {
    let resolve!: (v: unknown) => void
    launch.mockReturnValue(new Promise((r) => { resolve = r }))
    renderLauncher()
    fireEvent.click(screen.getByTestId('launcher-command-p1-c1'))
    fireEvent.click(screen.getByTestId('launcher-command-p1-c1'))
    expect(screen.getByTestId('launcher-name')).toBeDisabled()
    expect(screen.getByTestId('launcher-project-name-p2')).toBeDisabled()
    await act(async () => { resolve({ status: 'created', session: made }) })
    expect(launch).toHaveBeenCalledTimes(1)
  })

  it('does not call onLaunched after unmount', async () => {
    let resolve!: (v: unknown) => void
    launch.mockReturnValue(new Promise((r) => { resolve = r }))
    const { unmount } = renderLauncher()
    fireEvent.click(screen.getByTestId('launcher-project-name-p1'))
    unmount()
    await act(async () => { resolve({ status: 'created', session: made }) })
    expect(onLaunched).not.toHaveBeenCalled()
  })

  it('keyboard: ArrowDown enters the grid, arrows walk items row-major, Enter launches, ArrowUp at the start returns to input, Escape cancels', async () => {
    renderLauncher()
    const input = screen.getByTestId('launcher-name')
    fireEvent.keyDown(input, { key: 'ArrowDown' })
    expect(screen.getByTestId('launcher-project-name-p1')).toHaveFocus()
    fireEvent.keyDown(document.activeElement!, { key: 'ArrowRight' })
    expect(screen.getByTestId('launcher-command-p1-c1')).toHaveFocus()
    fireEvent.keyDown(document.activeElement!, { key: 'ArrowDown' })
    expect(screen.getByTestId('launcher-command-p1-c2')).toHaveFocus()
    fireEvent.keyDown(document.activeElement!, { key: 'ArrowRight' })
    expect(screen.getByTestId('launcher-project-name-p2')).toHaveFocus()
    fireEvent.keyDown(document.activeElement!, { key: 'ArrowLeft' })
    fireEvent.keyDown(document.activeElement!, { key: 'ArrowLeft' })
    fireEvent.keyDown(document.activeElement!, { key: 'ArrowUp' })
    expect(screen.getByTestId('launcher-project-name-p1')).toHaveFocus()
    fireEvent.keyDown(document.activeElement!, { key: 'ArrowUp' })
    expect(input).toHaveFocus()
    fireEvent.keyDown(input, { key: 'Escape' })
    expect(onCancel).toHaveBeenCalled()
    fireEvent.click(screen.getByTestId('launcher-command-p1-c2'))
    await waitFor(() => expect(launch).toHaveBeenCalledWith(H, { name: '', project: P1, command: C2 }))
  })

  it('Escape anywhere in the grid cancels', () => {
    renderLauncher()
    fireEvent.keyDown(screen.getByTestId('launcher-project-name-p1'), { key: 'Escape' })
    expect(onCancel).toHaveBeenCalled()
  })

  // A4: the items answer the keyboard themselves — a focused item must launch on
  // Enter and on Space without any click being dispatched.
  it('Enter and Space on a focused project name launch {project}', async () => {
    renderLauncher()
    const item = screen.getByTestId('launcher-project-name-p1')
    item.focus()
    fireEvent.keyDown(item, { key: 'Enter' })
    await waitFor(() => expect(launch).toHaveBeenCalledWith(H, { name: '', project: P1 }))
    launch.mockClear()
    fireEvent.keyDown(item, { key: ' ' })
    await waitFor(() => expect(launch).toHaveBeenCalledWith(H, { name: '', project: P1 }))
  })

  it('Enter and Space on a focused command icon launch {project, command}', async () => {
    renderLauncher()
    const item = screen.getByTestId('launcher-command-p2-c1')
    item.focus()
    fireEvent.keyDown(item, { key: 'Enter' })
    await waitFor(() => expect(launch).toHaveBeenCalledWith(H, { name: '', project: P2, command: C1 }))
    launch.mockClear()
    fireEvent.keyDown(item, { key: ' ' })
    await waitFor(() => expect(launch).toHaveBeenCalledWith(H, { name: '', project: P2, command: C1 }))
  })

  it('cancels the native activation click so a key press launches exactly once', () => {
    renderLauncher()
    const item = screen.getByTestId('launcher-project-name-p1')
    const event = new KeyboardEvent('keydown', { key: 'Enter', bubbles: true, cancelable: true })
    fireEvent(item, event)
    expect(event.defaultPrevented).toBe(true)
  })

  it('placeholder names the generated-name rule', () => {
    renderLauncher()
    expect(screen.getByTestId('launcher-name')).toHaveAttribute('placeholder', expect.stringContaining('<slug>-N'))
  })

  it('empty host: hint + link to Host › Projects; the name input still works', async () => {
    seed('ready', [], [C1])
    renderLauncher()
    expect(screen.getByTestId('launcher-empty')).toBeInTheDocument()
    fireEvent.click(screen.getByTestId('launcher-empty-link'))
    expect(setLocation).toHaveBeenCalledWith('/hosts/h1/projects')
    expect(onCancel).toHaveBeenCalled()
    fireEvent.change(screen.getByTestId('launcher-name'), { target: { value: 'dev' } })
    fireEvent.keyDown(screen.getByTestId('launcher-name'), { key: 'Enter' })
    await waitFor(() => expect(launch).toHaveBeenCalledWith(H, { name: 'dev' }))
  })

  it('old daemon: unsupported hint, no grid, Enter still creates', () => {
    seed('unsupported', [], [])
    renderLauncher()
    expect(screen.getByTestId('launcher-unsupported')).toBeInTheDocument()
    expect(screen.queryByTestId('launcher-grid')).toBeNull()
    expect(screen.getByTestId('launcher-name')).toBeEnabled()
  })

  // A1: the liveness guard survives the launcher being open.
  it('disabled: the input and every item are disabled and nothing launches', () => {
    renderLauncher(true)
    expect(screen.getByTestId('launcher-name')).toBeDisabled()
    expect(screen.getByTestId('launcher-project-name-p1')).toBeDisabled()
    expect(screen.getByTestId('launcher-command-p1-c1')).toBeDisabled()
    fireEvent.click(screen.getByTestId('launcher-project-name-p1'))
    fireEvent.keyDown(screen.getByTestId('launcher-name'), { key: 'Enter' })
    expect(launch).not.toHaveBeenCalled()
  })

  it('host goes offline after the launcher opens: launching refuses with the offline message', async () => {
    renderLauncher()
    seedHost({ status: 'disconnected' })
    fireEvent.click(screen.getByTestId('launcher-project-name-p1'))
    expect(await screen.findByTestId('launcher-error')).toHaveTextContent('offline')
    expect(launch).not.toHaveBeenCalled()
    expect(onLaunched).not.toHaveBeenCalled()
  })

  // A3: the session really was created; the launcher must not vanish leaving the
  // user with no pane and no explanation.
  it('host drops while the create is in flight: the launcher stays open with an error and hands nothing over', async () => {
    launch.mockImplementation(async () => {
      seedHost({ status: 'disconnected' })
      return { status: 'created', session: made }
    })
    renderLauncher()
    fireEvent.click(screen.getByTestId('launcher-project-name-p1'))
    expect(await screen.findByTestId('launcher-error')).toHaveTextContent('went offline')
    expect(onLaunched).not.toHaveBeenCalled()
    expect(screen.getByTestId('launcher')).toBeInTheDocument()
  })

  it('tmux goes unavailable after the launcher opens: Enter refuses too', async () => {
    renderLauncher()
    seedHost({ tmuxState: 'unavailable' })
    fireEvent.change(screen.getByTestId('launcher-name'), { target: { value: 'dev' } })
    fireEvent.keyDown(screen.getByTestId('launcher-name'), { key: 'Enter' })
    expect(await screen.findByTestId('launcher-error')).toBeInTheDocument()
    expect(launch).not.toHaveBeenCalled()
  })
})
