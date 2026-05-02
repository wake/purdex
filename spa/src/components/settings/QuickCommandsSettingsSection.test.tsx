import { describe, it, expect, beforeEach, afterEach } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import { QuickCommandsSettingsSection } from './QuickCommandsSettingsSection'
import { useQuickCommandStore } from '../../stores/useQuickCommandStore'
import { QUICK_COMMAND_SLOTS } from '../../lib/quick-command-slots'

function resetStore() {
  useQuickCommandStore.setState({ global: [], byHost: {}, bindings: {} })
}

describe('QuickCommandsSettingsSection', () => {
  beforeEach(() => resetStore())
  afterEach(() => resetStore())

  it('shows empty state when no commands', () => {
    render(<QuickCommandsSettingsSection />)
    expect(screen.getByText(/No quick commands yet/i)).toBeInTheDocument()
  })

  it('lists commands in capability order with mount chips', () => {
    useQuickCommandStore.setState({
      global: [
        { id: 'cmd-a', name: 'Alpha', command: 'a' },
        { id: 'cmd-b', name: 'Beta', command: 'b' },
      ],
      byHost: {},
      bindings: { 'cmd-a': [QUICK_COMMAND_SLOTS.WORKSPACE_ACTIONS, QUICK_COMMAND_SLOTS.HOST_ACTIONS] },
    })
    render(<QuickCommandsSettingsSection />)
    const rows = screen.getAllByTestId(/^qc-row-/)
    expect(rows).toHaveLength(2)
    expect(rows[0]).toHaveAttribute('data-testid', 'qc-row-cmd-a')
    // cmd-a 的 mount chips 顯示 Workspace 與 Host
    const aRow = rows[0]
    expect(aRow.textContent).toMatch(/Workspace/)
    expect(aRow.textContent).toMatch(/Host/)
  })

  it('clicking + New opens dialog with empty fields, focus traps inside', () => {
    render(<QuickCommandsSettingsSection />)
    fireEvent.click(screen.getByRole('button', { name: /New/i }))
    const dialog = screen.getByRole('dialog')
    expect(dialog).toBeInTheDocument()
    // First focusable inside dialog should receive focus
    expect(document.activeElement).toBeTruthy()
    expect(dialog.contains(document.activeElement)).toBe(true)
  })

  it('Esc closes dialog and returns focus to trigger', () => {
    render(<QuickCommandsSettingsSection />)
    const trigger = screen.getByRole('button', { name: /New/i })
    fireEvent.click(trigger)
    expect(screen.queryByRole('dialog')).toBeInTheDocument()
    fireEvent.keyDown(document, { key: 'Escape' })
    expect(screen.queryByRole('dialog')).toBeNull()
    expect(document.activeElement).toBe(trigger)
  })

  it('saving a new command persists to store with selected mount targets', () => {
    render(<QuickCommandsSettingsSection />)
    fireEvent.click(screen.getByRole('button', { name: /New/i }))
    fireEvent.change(screen.getByLabelText(/^Name$/i), { target: { value: 'My Cmd' } })
    fireEvent.change(screen.getByLabelText(/^Command$/i), { target: { value: 'echo hi' } })
    // toggle Workspace chip
    fireEvent.click(screen.getByRole('button', { name: /^Workspace$/i }))
    fireEvent.click(screen.getByRole('button', { name: /Save/i }))

    const state = useQuickCommandStore.getState()
    expect(state.global).toHaveLength(1)
    const cmd = state.global[0]
    expect(cmd.name).toBe('My Cmd')
    expect(state.bindings[cmd.id]).toEqual([QUICK_COMMAND_SLOTS.WORKSPACE_ACTIONS])
  })

  it('Edit existing command updates name + bindings', () => {
    useQuickCommandStore.setState({
      global: [{ id: 'cmd-a', name: 'Alpha', command: 'a' }],
      byHost: {},
      bindings: { 'cmd-a': [QUICK_COMMAND_SLOTS.WORKSPACE_ACTIONS] },
    })
    render(<QuickCommandsSettingsSection />)
    fireEvent.click(screen.getByRole('button', { name: /^Edit$/i }))
    fireEvent.change(screen.getByLabelText(/^Name$/i), { target: { value: 'Alpha 2' } })
    // toggle Workspace off, Host on
    fireEvent.click(screen.getByRole('button', { name: /^Workspace$/i }))
    fireEvent.click(screen.getByRole('button', { name: /^Host$/i }))
    fireEvent.click(screen.getByRole('button', { name: /Save/i }))

    const state = useQuickCommandStore.getState()
    expect(state.global[0].name).toBe('Alpha 2')
    expect(state.bindings['cmd-a']).toEqual([QUICK_COMMAND_SLOTS.HOST_ACTIONS])
  })

  it('Delete removes command and clears its binding', () => {
    useQuickCommandStore.setState({
      global: [{ id: 'cmd-a', name: 'Alpha', command: 'a' }],
      byHost: {},
      bindings: { 'cmd-a': [QUICK_COMMAND_SLOTS.WORKSPACE_ACTIONS] },
    })
    render(<QuickCommandsSettingsSection />)
    fireEvent.click(screen.getByRole('button', { name: /^Delete$/i }))
    const state = useQuickCommandStore.getState()
    expect(state.global).toHaveLength(0)
    expect(state.bindings['cmd-a']).toBeUndefined()
  })

  // codex round-2 — own-property guard mirrors the slot-render fix from
  // round-1 P2. Settings list previously did `bindings[cmd.id] ?? []` directly
  // and the dialog's initialTargets the same; for a capability id colliding
  // with an inherited Object.prototype method (toString / valueOf) those
  // lookups resolved to a function and `.map(...)` would crash the section.
  it('does not crash when capability id collides with Object.prototype method (toString) — list render', () => {
    useQuickCommandStore.setState({
      global: [{ id: 'toString', name: 'Evil', command: 'evil' }],
      byHost: {},
      bindings: {},
    })
    expect(() => render(<QuickCommandsSettingsSection />)).not.toThrow()
    // Row still rendered and has zero mount chips
    expect(screen.getByTestId('qc-row-toString')).toBeInTheDocument()
  })

  it('does not crash when editing a capability whose id is "toString" (dialog open)', () => {
    useQuickCommandStore.setState({
      global: [{ id: 'toString', name: 'Evil', command: 'evil' }],
      byHost: {},
      bindings: {},
    })
    render(<QuickCommandsSettingsSection />)
    expect(() =>
      fireEvent.click(screen.getByRole('button', { name: /^Edit$/i })),
    ).not.toThrow()
  })

  // PR-2 (spec §4.4) — header pattern unified with Appearance / Terminal:
  // outer wrapper drops `p-6` (GlobalSettingsPage already pads), and a
  // `<p>` description follows the `<h2>` title.
  it('outer wrapper does not include p-6 (relies on GlobalSettingsPage padding)', () => {
    const { container } = render(<QuickCommandsSettingsSection />)
    const outer = container.firstElementChild as HTMLElement
    expect(outer.className).not.toMatch(/\bp-6\b/)
  })

  it('renders an h2 title followed by a p description (Appearance/Terminal pattern)', () => {
    const { container } = render(<QuickCommandsSettingsSection />)
    const h2 = container.querySelector('h2')
    expect(h2).not.toBeNull()
    expect(h2!.textContent).toMatch(/Quick Commands/i)
    // The description `<p>` must be a sibling of the `<h2>` (not the
    // empty-state `<p>` deeper in the commands list). Walk forward from
    // the heading to its next element-level sibling.
    const desc = h2!.nextElementSibling as HTMLElement | null
    expect(desc).not.toBeNull()
    expect(desc!.tagName.toLowerCase()).toBe('p')
    // Description text must come from the new i18n key — t() with an
    // unknown key returns the raw key, so a successful resolution means
    // the textContent is NOT the literal key string.
    expect(desc!.textContent).not.toBe('settings.quick_commands.desc')
    expect(desc!.textContent?.length ?? 0).toBeGreaterThan(0)
  })

  it('+ New trigger still opens the dialog with empty fields (header refactor preserves CRUD)', () => {
    render(<QuickCommandsSettingsSection />)
    fireEvent.click(screen.getByRole('button', { name: /New/i }))
    expect(screen.getByRole('dialog')).toBeInTheDocument()
  })

  // codex round-1 C15 — keyboard accessibility on multi-select mount chips
  it('mount-target chips support Space/Enter activation and ArrowRight/ArrowLeft roving focus', () => {
    render(<QuickCommandsSettingsSection />)
    fireEvent.click(screen.getByRole('button', { name: /New/i }))
    const wsChip = screen.getByRole('button', { name: /^Workspace$/i })
    const hostChip = screen.getByRole('button', { name: /^Host$/i })

    // Focus first chip
    wsChip.focus()
    expect(document.activeElement).toBe(wsChip)

    // Space toggles aria-pressed
    expect(wsChip.getAttribute('aria-pressed')).toBe('false')
    fireEvent.keyDown(wsChip, { key: ' ' })
    fireEvent.click(wsChip) // RTL: native button Space → click; explicit click for jsdom safety
    expect(wsChip.getAttribute('aria-pressed')).toBe('true')

    // Enter also toggles
    fireEvent.keyDown(wsChip, { key: 'Enter' })
    fireEvent.click(wsChip)
    expect(wsChip.getAttribute('aria-pressed')).toBe('false')

    // ArrowRight moves focus to next chip (roving focus)
    fireEvent.keyDown(wsChip, { key: 'ArrowRight' })
    expect(document.activeElement).toBe(hostChip)

    // ArrowLeft moves focus back
    fireEvent.keyDown(hostChip, { key: 'ArrowLeft' })
    expect(document.activeElement).toBe(wsChip)
  })
})
