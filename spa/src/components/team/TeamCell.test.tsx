// spa/src/components/team/TeamCell.test.tsx — the one-line cell, round 4 (spec §4.4, user 2026-10-10): laid out like the
// sidebar tab row's "bot -> host icon" run (6px gap, the same icon, the same padding as a bead), with the usage ring in the
// place of the host square: sized by the sidebar host box, its model symbol in the host's main colour, no host square at all.
import { describe, it, expect, beforeEach } from 'vitest'
import { act, render, screen } from '@testing-library/react'
import { TeamCell } from './TeamCell'
import type { TeamSeatView } from './team-display'
import { useAgentStore } from '../../stores/useAgentStore'
import { useHostLookStore } from '../../stores/useHostLookStore'
import { useUISettingsStore } from '../../stores/useUISettingsStore'
import { compositeKey } from '../../lib/composite-key'
import { resolveHostColors } from '../../lib/host-color'

const seat = (over: Partial<TeamSeatView> = {}): TeamSeatView => ({
  sessionId: 'S', title: 'S', hostId: 'h1', sessionCode: 's1', role: 'member', tabId: null, state: 'active', hostAlias: '', remote: false, ...over,
})
const COLORS = { console: { main: { color: '#3b82f6', alpha: 100 } } }
const ref = { id: 'a', type: 'cc', started_at: 1, source_pid: 0, source_start_time: '' }
const cell = () => screen.getByTestId('team-panel-cell')
const mount = (s: TeamSeatView = seat()) => render(<TeamCell teamKey={`h1\u0000t`} seat={s} isActive={false} onOpen={() => {}} />)
const ringOf = () => screen.getByTestId('context-ring')

beforeEach(() => {
  useAgentStore.setState({ statuses: { [compositeKey('h1', 's1')]: 'running' }, agentTypes: { [compositeKey('h1', 's1')]: 'cc' }, subagents: {} } as never)
  useUISettingsStore.setState({ tabIndicatorStyle: 'badge', hostBadgeSidebarBox: 16 } as never)
  useHostLookStore.setState({ looks: {} })
})

describe('the cell\'s layout (the sidebar run: bot -> 6px -> square)', () => {
  it('is the bead\'s box: h-6, 6px left and 3px right padding, 6px between the icon and the ring', () => {
    mount()
    const cls = cell().className
    for (const c of ['flex', 'items-center', 'gap-1.5', 'h-6', 'pl-1.5', 'pr-[3px]', 'rounded-md']) expect(cls).toContain(c)
    expect(cell().style.height).toBe('') // no inline measures: the classes are the layout
    expect(cell().style.paddingInline).toBe('')
  })

  it('holds the bot and then the ring, and no host square — local or remote', () => {
    for (const s of [seat(), seat({ remote: true, hostAlias: 'b26' }), seat({ hostId: '' })]) {
      const { unmount } = mount(s)
      const kids = [...cell().children]
      expect(kids).toHaveLength(2)
      expect(kids[0].getAttribute('data-testid')).toBe('team-panel-light')
      expect(kids[1].getAttribute('data-testid')).toBe('context-ring')
      expect(cell().querySelector('[data-host-badge], [data-testid="host-badge"], [data-testid="team-bead-host-unknown"]')).toBeNull()
      unmount()
    }
  })

  it('draws the bot like the sidebar row: the 14px icon in the 16px box, not the compact corner light', () => {
    mount()
    const light = screen.getByTestId('team-panel-light')
    expect(light.style.marginLeft).toBe('') // no pull: the box keeps the sidebar\'s own margins
    expect(light.querySelector('.w-4.h-4')).not.toBeNull()
    expect(light.querySelector('[data-testid="seat-subagent-slot"]')).toBeNull() // no reserved slot any more
  })

  it('keeps a remote seat\'s host in the tooltip, and its state dims the bot', () => {
    mount(seat({ remote: true, hostAlias: 'b26', state: 'releasing' }))
    expect(cell().getAttribute('title')).toContain('b26')
    expect(screen.getByTestId('team-panel-light').getAttribute('data-dim')).toBe('true')
  })
})

describe('the ring takes the host square\'s place', () => {
  it.each([12, 16, 20, 24])('is as big as the sidebar host box (%ipx) and follows the setting', (box) => {
    act(() => useUISettingsStore.setState({ hostBadgeSidebarBox: box } as never))
    mount()
    expect(ringOf().style.width).toBe(`${box}px`)
    expect(ringOf().style.height).toBe(`${box}px`)
  })

  it('changes when the setting changes', () => {
    mount()
    expect(ringOf().style.width).toBe('16px')
    act(() => useUISettingsStore.setState({ hostBadgeSidebarBox: 22 } as never))
    expect(ringOf().style.width).toBe('22px')
  })

  it('its model symbol is the host\'s main colour (the host icon\'s bright colour)', () => {
    act(() => useHostLookStore.setState({ looks: { h1: { colors: COLORS } } } as never))
    mount()
    const main = resolveHostColors({ colors: COLORS as never, color: undefined }, 'terminal')!.main
    expect(main).toMatch(/^rgba?\(/)
    const probe = document.createElement('span') // the browser's own normal form of the same colour
    probe.style.color = main
    expect(ringOf().style.color).toBe(probe.style.color)
    expect(ringOf().style.color).not.toBe('')
  })

  it('a host with no colour, or a host this Mac lacks (hostId ""), keeps the neutral symbol', () => {
    mount()
    expect(ringOf().style.color).toBe('')
    cleanupMount(seat({ hostId: '' }))
    expect(ringOf().style.color).toBe('')
  })
})

function cleanupMount(s: TeamSeatView) {
  document.body.innerHTML = ''
  mount(s)
}

describe('subagents and the light do not change or leave the cell', () => {
  it('the cell\'s elements are the same with and without subagents; the dots are absolutely placed (in the padding)', () => {
    const none = mount()
    const before = [...cell().querySelectorAll('*')].map((e) => e.tagName + '|' + e.className)
    none.unmount()
    useAgentStore.setState({ subagents: { [compositeKey('h1', 's1')]: [ref, { ...ref, id: 'b' }] } } as never)
    mount()
    const dots = [...cell().querySelectorAll('[data-testid="subagent-dot"]')]
    expect(dots).toHaveLength(2)
    dots.forEach((d) => expect(d.className).toContain('absolute'))
    const after = [...cell().querySelectorAll('*')].filter((e) => !dots.includes(e)).map((e) => e.tagName + '|' + e.className)
    expect(after).toEqual(before) // nothing was added to the flow: the width is the same
  })

  it('the light sits on the bot\'s own 16px box inside the cell (overlay, no extra width)', () => {
    mount()
    const light = cell().querySelector('[data-testid="tab-status-indicator"]')!
    const box = light.closest('.relative')!
    expect(cell().contains(box)).toBe(true)
    expect(box.className).toContain('w-4')
    // the dot is an overlay on the box, not a flow item: nothing between it and the box takes width
    let el: Element | null = light
    let overlay = false
    while (el && el !== box) { overlay ||= el.className.toString().includes('absolute') || (el as HTMLElement).style.position === 'absolute'; el = el.parentElement }
    expect(overlay).toBe(true)
  })
})
