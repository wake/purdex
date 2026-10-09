// spa/src/components/team/TeamPanel.header.test.tsx — the panel header's click and double-click (TI-7, spec §4.4 header click,
// §4.12): a click toggles full / one-line, a click on the name waits for a possible double-click, a double-click on the name
// opens the edit form (only when the lead's host lists `team.edit.v1`) and Save sends all three values to that host.
import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { act, cleanup, fireEvent, render, screen } from '@testing-library/react'
import { TeamDisplayProvider } from './TeamDisplayProvider'
import { TeamPanelArea } from './TeamPanelArea'
import { teamColor } from './team-display'
import { HOST, TEAM, resetTeamStores, seedScene } from '../../lib/team/__tests__/team-fixture'
import { clearModuleRegistry } from '../../lib/module-registry'
import { useTeamRosterStore } from '../../stores/useTeamRosterStore'
import { useTeamUiStore } from '../../stores/useTeamUiStore'
import { useShownHostsStore } from '../../stores/useShownHostsStore'
import { useUnattendedStore } from '../../stores/useUnattendedStore'
import { useI18nStore } from '../../stores/useI18nStore'
import { useUndoToast } from '../../stores/useUndoToast'
import { ApprovalApiError } from '../../lib/team/approval-api'
import { NAME_CLICK_DELAY_MS } from './panel-layout'

const send = vi.hoisted(() => vi.fn())
vi.mock('../../lib/team/approval-api', async (orig) => ({ ...(await orig<typeof import('../../lib/team/approval-api')>()), send }))
vi.mock('../../lib/team/unattended-api', async (orig) => ({ ...(await orig<typeof import('../../lib/team/unattended-api')>()), descriptorFor: async () => ({ kind: 'app', label: 'Purdex.app' }) }))
vi.mock('./TeamSeatIcon', () => ({
  TeamSeatIcon: () => <span data-testid="seat-icon" />,
  TeamSeatHostBadge: () => <span data-testid="seat-host" />,
}))

const scene = () => seedScene({
  members: [['A', 'a-tm'], ['B', 'b-tm']],
  tabs: [['lead', 'lead-tm'], ['ma', 'a-tm']],
  workspaces: [{ id: 'w1', tabs: ['lead', 'ma'] }],
  activeTabId: 'lead',
  teamName: 'Release train',
  teamLabel: '發版',
})
const patchRoster = (patch: (t: ReturnType<typeof useTeamRosterStore.getState>['byHost'][string][number]) => void) => act(() => {
  const roster = structuredClone(useTeamRosterStore.getState().byHost[HOST])
  patch(roster[0])
  useTeamRosterStore.setState({ byHost: { [HOST]: roster } })
})
const setEdit = (v: 'yes' | 'no') => act(() => useUnattendedStore.getState().setEditSupport(HOST, v))
const mount = () => render(<TeamDisplayProvider><TeamPanelArea /></TeamDisplayProvider>)
const mode = () => screen.getByTestId('team-panel').getAttribute('data-mode')
const name = () => screen.getByTestId('team-panel-name')
const header = () => screen.getByTestId('team-panel-header')
const popover = () => screen.queryByTestId('team-edit-popover')
const advance = (ms: number) => act(() => { vi.advanceTimersByTime(ms) })
const settle = () => act(async () => { await Promise.resolve(); await Promise.resolve() })
const dblClickName = () => { fireEvent.click(name()); fireEvent.click(name()); fireEvent.doubleClick(name()) }
const body = () => JSON.parse(send.mock.calls[0][2].body as string) as Record<string, unknown>

beforeEach(() => {
  cleanup()
  localStorage.clear()
  vi.useFakeTimers()
  send.mockReset()
  send.mockResolvedValue({})
  resetTeamStores()
  useUnattendedStore.getState().reset()
  useTeamUiStore.setState({ panel: { width: 312, expanded: false }, teamDrill: {}, workbookTabs: {} })
  useShownHostsStore.setState({ ids: [HOST] })
  clearModuleRegistry()
})
afterEach(() => { cleanup(); vi.useRealTimers() })

describe('a click on the header', () => {
  it('shows the pointer cursor across the whole row in both modes, and the name does not override it', () => {
    scene()
    mount()
    expect(header().className).toContain('cursor-pointer')
    expect(header().className).toContain('select-none') // a double-click on the name must not leave its text selected
    expect(name().className).not.toMatch(/cursor-(?!pointer)/)
    fireEvent.click(screen.getByTestId('team-panel-to-line'))
    expect(mode()).toBe('line')
    expect(header().className).toContain('cursor-pointer')
    expect(name().className).not.toMatch(/cursor-(?!pointer)/)
  })

  it('toggles full and one-line from anywhere on it', () => {
    scene()
    mount()
    expect(mode()).toBe('full')
    fireEvent.click(screen.getByTestId('team-panel-count'))
    expect(mode()).toBe('line')
    fireEvent.click(header())
    expect(mode()).toBe('full')
  })

  it('the switch, enlarge and the cells do not toggle on their own', () => {
    scene()
    mount()
    fireEvent.click(screen.getByTestId('team-panel-to-line')) // the switch itself toggles once, not twice
    expect(mode()).toBe('line')
    fireEvent.click(screen.getByTestId('team-panel-expand'))
    expect(mode()).toBe('line')
    expect(screen.getByTestId('team-panel-area').getAttribute('data-expanded')).toBe('true')
    fireEvent.click(screen.getAllByTestId('team-panel-cell')[1])
    expect(mode()).toBe('line')
    fireEvent.click(screen.getByTestId('team-panel-to-full'))
    expect(mode()).toBe('full')
  })

  it('a click on the name toggles only after the double-click interval', () => {
    scene()
    mount()
    fireEvent.click(name())
    advance(NAME_CLICK_DELAY_MS - 1)
    expect(mode()).toBe('full')
    advance(1)
    expect(mode()).toBe('line')
  })

  it('a double-click on the name does not toggle (and opens no form without the capability)', () => {
    scene()
    mount()
    dblClickName()
    advance(NAME_CLICK_DELAY_MS * 3)
    expect(mode()).toBe('full')
    expect(popover()).toBeNull()
  })
})

describe('double-click the name', () => {
  it('opens the form from the roster values, and does not toggle', () => {
    scene()
    patchRoster((t) => { t.team_color = 3 })
    setEdit('yes')
    mount()
    dblClickName()
    advance(NAME_CLICK_DELAY_MS * 3)
    expect(mode()).toBe('full')
    expect(popover()).not.toBeNull()
    expect((screen.getByTestId('team-edit-name') as HTMLInputElement).value).toBe('Release train')
    expect((screen.getByTestId('team-edit-label') as HTMLInputElement).value).toBe('發版')
    expect(screen.getByTestId('team-edit-color-3').getAttribute('aria-pressed')).toBe('true')
    expect(screen.getByTestId('team-edit-color-auto').getAttribute('aria-pressed')).toBe('false')
  })

  it('a host without team.edit.v1 gets no form', () => {
    scene()
    setEdit('no')
    mount()
    dblClickName()
    expect(popover()).toBeNull()
  })

  it('clicks inside the form do not toggle the panel', () => {
    scene()
    setEdit('yes')
    mount()
    dblClickName()
    fireEvent.click(screen.getByTestId('team-edit-name'))
    fireEvent.click(popover()!)
    advance(NAME_CLICK_DELAY_MS * 2)
    expect(mode()).toBe('full')
  })
})

describe('the form follows its header', () => {
  const rectOf = (left: number, bottom: number) => ({ left, bottom, right: left + 312, top: bottom - 34, width: 312, height: 34, x: left, y: bottom - 34, toJSON: () => ({}) })
  let box = rectOf(900, 34)
  const pos = () => { const el = popover() as HTMLElement; return [el.style.left, el.style.top] }
  beforeEach(() => {
    box = rectOf(900, 34)
    vi.spyOn(HTMLElement.prototype, 'getBoundingClientRect').mockImplementation(function (this: HTMLElement) {
      return (this.getAttribute('data-testid') === 'team-panel-header' ? box : rectOf(0, 0)) as DOMRect
    })
    scene(); setEdit('yes'); mount(); dblClickName()
  })
  afterEach(() => vi.restoreAllMocks())

  it('hangs under the live header, clamped at the right edge', () => {
    expect(window.innerWidth).toBe(1024)
    expect(pos()).toEqual([`${1024 - 260 - 8}px`, '38px'])
  })

  it('moves when the window resizes', () => {
    box = rectOf(100, 34)
    fireEvent(window, new Event('resize'))
    expect(pos()).toEqual(['100px', '38px'])
  })

  it('after a mode switch it watches the NEW header: its resize moves the form', () => {
    const watchers: Array<{ cb: () => void; seen: Set<Element> }> = []
    class FakeRO {
      seen = new Set<Element>()
      constructor(cb: () => void) { watchers.push({ cb, seen: this.seen }) }
      observe(el: Element) { this.seen.add(el) }
      unobserve(el: Element) { this.seen.delete(el) }
      disconnect() { this.seen.clear() }
    }
    vi.stubGlobal('ResizeObserver', FakeRO)
    // the form was opened before the stub: re-open it so it creates its observer
    fireEvent.keyDown(screen.getByTestId('team-edit-name'), { key: 'Escape' })
    dblClickName()
    const oldHeader = header()
    fireEvent.click(screen.getByTestId('team-panel-to-line')) // the header element is replaced
    const fresh = header()
    expect(fresh).not.toBe(oldHeader)
    box = rectOf(30, 80) // the new header's geometry changed
    const live = watchers.filter((w) => w.seen.has(fresh))
    expect(live.length).toBeGreaterThan(0)
    expect(watchers.some((w) => w.seen.has(oldHeader))).toBe(false) // nothing keeps watching the detached one
    act(() => live.forEach((w) => w.cb()))
    expect(pos()).toEqual(['30px', '84px'])
    vi.unstubAllGlobals()
  })

  it('the colour choices are one "auto" row and a fixed 8-column grid of the 8 swatches (no orphan at any width)', () => {
    const grid = screen.getByTestId('team-edit-swatches')
    expect(grid.className).toContain('grid-cols-8')
    expect(grid.children.length).toBe(8)
    expect(grid.contains(screen.getByTestId('team-edit-color-auto'))).toBe(false)
    expect((popover() as HTMLElement).style.width).toBe('260px') // the form's width does not follow the panel's
  })

  it('moves when the panel is enlarged or its mode changes (the header is another element then)', () => {
    box = rectOf(40, 60)
    fireEvent.click(screen.getByTestId('team-panel-expand'))
    expect(pos()).toEqual(['40px', '64px'])
    box = rectOf(50, 70)
    fireEvent.click(screen.getByTestId('team-panel-to-line'))
    expect(popover()).not.toBeNull()
    expect(pos()).toEqual(['50px', '74px'])
  })
})

describe('Save', () => {
  const open = () => { scene(); patchRoster((t) => { t.team_color = 3 }); setEdit('yes'); mount(); dblClickName() }

  it('sends all three values to the lead host: the roster values plus the edit', async () => {
    open()
    fireEvent.change(screen.getByTestId('team-edit-name'), { target: { value: 'Night train' } })
    fireEvent.click(screen.getByTestId('team-edit-save'))
    await settle()
    expect(send).toHaveBeenCalledTimes(1)
    expect(send.mock.calls[0][0]).toBe(HOST)
    expect(send.mock.calls[0][1]).toBe('/api/team/appearance')
    expect(body()).toMatchObject({ team_id: TEAM, team_name: 'Night train', team_label: '發版', team_color: 3 })
    expect(popover()).toBeNull() // 200 closes it
  })

  it('colour "auto" is sent as null; a swatch as its index; Enter in a field saves too', async () => {
    open()
    fireEvent.click(screen.getByTestId('team-edit-color-auto'))
    fireEvent.keyDown(screen.getByTestId('team-edit-name'), { key: 'Enter' })
    await settle()
    expect(body()).toMatchObject({ team_name: 'Release train', team_label: '發版', team_color: null })
    send.mockClear()
    dblClickName()
    fireEvent.click(screen.getByTestId('team-edit-color-6'))
    fireEvent.click(screen.getByTestId('team-edit-save'))
    await settle()
    expect(body()).toMatchObject({ team_color: 6 })
  })

  it('a roster colour is not copied: the panel keeps showing what the roster says until its next frame', async () => {
    open()
    const before = (name() as HTMLElement).style.background
    fireEvent.click(screen.getByTestId('team-edit-color-6'))
    fireEvent.click(screen.getByTestId('team-edit-save'))
    await settle()
    expect((name() as HTMLElement).style.background).toBe(before)
    patchRoster((t) => { t.team_color = 6 })
    const probe = document.createElement('div')
    probe.style.background = teamColor(6)
    expect((name() as HTMLElement).style.background).toBe(probe.style.background)
  })

  it('400 puts the daemon message under the field it names and keeps the form', async () => {
    open()
    send.mockRejectedValueOnce(new ApprovalApiError(400, 'bad_request', 'team_name: has a control character'))
    fireEvent.click(screen.getByTestId('team-edit-save'))
    await settle()
    expect(screen.getByTestId('team-edit-name-error').textContent).toBe('team_name: has a control character')
    expect(popover()).not.toBeNull()
    send.mockRejectedValueOnce(new ApprovalApiError(400, 'bad_request', 'team_label: too wide'))
    fireEvent.click(screen.getByTestId('team-edit-save'))
    await settle()
    expect(screen.getByTestId('team-edit-label-error').textContent).toBe('team_label: too wide')
    expect(screen.queryByTestId('team-edit-name-error')).toBeNull() // the old error is cleared on a new try
  })

  it('a 400 that names no field shows at the bottom of the form', async () => {
    open()
    send.mockRejectedValueOnce(new ApprovalApiError(400, 'bad_request', 'invalid JSON: x'))
    fireEvent.click(screen.getByTestId('team-edit-save'))
    await settle()
    expect(screen.getByTestId('team-edit-form-error').textContent).toContain('invalid JSON: x')
  })

  it('409 closes the form and toasts', async () => {
    open()
    send.mockRejectedValueOnce(new ApprovalApiError(409, 'not_live', 'the team has ended'))
    fireEvent.click(screen.getByTestId('team-edit-save'))
    await settle()
    expect(popover()).toBeNull()
    expect(useUndoToast.getState().toast?.message).toBeTruthy()
  })

  it('a network failure shows at the bottom and keeps the form', async () => {
    open()
    send.mockRejectedValueOnce(new ApprovalApiError(0, 'network', 'Failed to fetch'))
    fireEvent.click(screen.getByTestId('team-edit-save'))
    await settle()
    expect(screen.getByTestId('team-edit-form-error').textContent).toContain('Failed to fetch')
    expect(popover()).not.toBeNull()
  })
})

describe('the label width hint', () => {
  const open = () => { scene(); setEdit('yes'); mount(); dblClickName() }
  const label = () => screen.getByTestId('team-edit-label')

  it('follows the typing, counting a wide character as 2', () => {
    open()
    expect(screen.getByTestId('team-edit-label-width').textContent).toContain('4') // 發版
    fireEvent.change(label(), { target: { value: 'ab' } })
    expect(screen.getByTestId('team-edit-label-width').textContent).toMatch(/2.*10/)
    fireEvent.change(label(), { target: { value: '發版發' } })
    expect(screen.getByTestId('team-edit-label-width').textContent).toMatch(/6.*10/)
  })

  it('says an empty label is derived from the name: hint always, placeholder when the roster label is empty', () => {
    act(() => useI18nStore.getState().setLocale('zh-TW'))
    open()
    expect(screen.getByTestId('team-edit-label-hint').textContent).toBe('留空後儲存，會依名稱重新產生')
    expect(label().getAttribute('placeholder')).toBeNull() // the roster has a label
    fireEvent.change(label(), { target: { value: '' } })
    expect(screen.getByTestId('team-edit-label-hint')).not.toBeNull()
    fireEvent.keyDown(label(), { key: 'Escape' })
    patchRoster((t) => { t.team_label = '' })
    dblClickName()
    expect(label().getAttribute('placeholder')).toBe('留空＝依名稱自動')
    act(() => useI18nStore.getState().setLocale('en'))
  })

  it('10 is allowed; over 10 blocks Save and says why', async () => {
    open()
    fireEvent.change(label(), { target: { value: '一二三四五' } }) // 5 wide characters = 10
    expect((screen.getByTestId('team-edit-save') as HTMLButtonElement).disabled).toBe(false)
    expect(screen.queryByTestId('team-edit-label-wide')).toBeNull()
    fireEvent.change(label(), { target: { value: '一二三四五六' } }) // 12
    expect((screen.getByTestId('team-edit-save') as HTMLButtonElement).disabled).toBe(true)
    expect(screen.getByTestId('team-edit-label-wide')).not.toBeNull()
    fireEvent.click(screen.getByTestId('team-edit-save'))
    fireEvent.keyDown(label(), { key: 'Enter' })
    await settle()
    expect(send).not.toHaveBeenCalled()
  })
})

describe('leaving the form', () => {
  const open = () => { scene(); setEdit('yes'); mount() }

  it('Esc, Cancel and a press outside drop the draft and send nothing', async () => {
    open()
    dblClickName()
    fireEvent.change(screen.getByTestId('team-edit-name'), { target: { value: 'changed' } })
    fireEvent.keyDown(screen.getByTestId('team-edit-name'), { key: 'Escape' })
    expect(popover()).toBeNull()
    dblClickName()
    expect((screen.getByTestId('team-edit-name') as HTMLInputElement).value).toBe('Release train') // the draft is gone
    fireEvent.click(screen.getByTestId('team-edit-cancel'))
    expect(popover()).toBeNull()
    dblClickName()
    fireEvent.mouseDown(document.body)
    expect(popover()).toBeNull()
    await settle()
    expect(send).not.toHaveBeenCalled()
  })

  it('the focus goes back to where it was before the form opened', () => {
    open()
    const term = document.createElement('textarea')
    document.body.appendChild(term)
    term.focus()
    dblClickName()
    expect(document.activeElement).toBe(screen.getByTestId('team-edit-name')) // the form's own input may take it
    fireEvent.keyDown(screen.getByTestId('team-edit-name'), { key: 'Escape' })
    expect(document.activeElement).toBe(term)
    term.remove()
  })
})

describe('the roster colour', () => {
  it('team_color recolours the panel header; absent is the hash colour', () => {
    scene()
    mount()
    const probe = (i: number) => { const d = document.createElement('div'); d.style.background = teamColor(i); return d.style.background }
    patchRoster((t) => { t.team_color = 5 })
    expect((name() as HTMLElement).style.background).toBe(probe(5))
    patchRoster((t) => { delete t.team_color })
    expect((name() as HTMLElement).style.background).not.toBe(probe(5))
  })
})
