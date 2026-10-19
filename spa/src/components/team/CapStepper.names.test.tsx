// spa/src/components/team/CapStepper.names.test.tsx — #3611: the − / + of a CapStepper (the team's member cap) are told apart by name, as #3607 did for the QuotaStepper. Two teams on one screen used to have
// two buttons both called 「調低上限」 / 「調高上限」, which a screen reader or voice control cannot tell apart. The names say the action, the field and which team it is (and the host, when there is more than one);
// the number box has the same name minus the action. The assertions are on accessible names (getByRole), several steppers mounted at once.
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { cleanup, fireEvent, render, screen } from '@testing-library/react'
import { CapStepper } from './Steppers'
import { useI18nStore } from '../../stores/useI18nStore'
import { useHostStore } from '../../stores/useHostStore'
import { setMaxMembers } from '../../lib/team/max-members'
import type { TeamRoster } from '../../lib/team/roster'

vi.mock('../../lib/team/max-members', async (orig) => ({ ...(await orig<typeof import('../../lib/team/max-members')>()), setMaxMembers: vi.fn() }))

const team = (id: string, over: Partial<TeamRoster> = {}): TeamRoster => ({
  id, host_id: 'd', created_at: 1, team_name: '', team_label: '', lead: { session_id: `l-${id}`, ref: `_${id}`, address: `mlab/${id}-xx`, live: true }, members: [], max_members: 4, in_use: 2, ...over,
})
const hostCfg = (id: string, name: string, order: number) => ({ id, name, ip: '1', port: 1, token: 't', order })

/** The − and + buttons (the number's own display is a button too, named after the field and the value). */
const stepButtons = () => screen.getAllByRole('button').filter((b) => b.textContent === '−' || b.textContent === '+')
const names = () => stepButtons().map((b) => b.getAttribute('aria-label'))

function mountTwoTeams() {
  render(
    <div>
      <CapStepper hostId="h1" team={team('a')} label="Alma" labelText="成員上限" />
      <CapStepper hostId="h1" team={team('b', { max_members: 6 })} label="Bram" labelText="成員上限" />
    </div>,
  )
}

beforeEach(() => {
  cleanup()
  vi.mocked(setMaxMembers).mockClear()
  useHostStore.setState({ hosts: {}, hostOrder: [], runtime: {} } as never)
})
afterEach(() => { cleanup() })

describe('CapStepper accessible names', () => {
  it('zh-TW: each button says the action, the field and which team it is, and no two are the same', () => {
    useI18nStore.getState().setLocale('zh-TW')
    mountTwoTeams()
    expect(names()).toEqual(['調低成員上限（Alma）', '調高成員上限（Alma）', '調低成員上限（Bram）', '調高成員上限（Bram）'])
    expect(new Set(names()).size).toBe(4)
  })

  it('en: the same', () => {
    useI18nStore.getState().setLocale('en')
    render(
      <div>
        <CapStepper hostId="h1" team={team('a')} label="Alma" labelText="Member cap" />
        <CapStepper hostId="h1" team={team('b')} label="Bram" labelText="Member cap" />
      </div>,
    )
    expect(names()).toEqual(['Decrease Member cap (Alma)', 'Increase Member cap (Alma)', 'Decrease Member cap (Bram)', 'Increase Member cap (Bram)'])
  })

  it('a button is found by its accessible name, one at a time, and the bare 「調低上限」 / 「調高上限」 is gone', () => {
    useI18nStore.getState().setLocale('zh-TW')
    mountTwoTeams()
    expect(screen.getByRole('button', { name: '調高成員上限（Bram）' })).toHaveTextContent('+')
    expect(screen.getByRole('button', { name: '調低成員上限（Alma）' })).toHaveTextContent('−')
    expect(screen.queryByRole('button', { name: '調低上限' })).toBeNull()
    expect(screen.queryByRole('button', { name: '調高上限' })).toBeNull()
  })

  it('clicking the named button writes that team\'s cap', () => {
    useI18nStore.getState().setLocale('zh-TW')
    mountTwoTeams()
    fireEvent.click(screen.getByRole('button', { name: '調高成員上限（Bram）' }))
    expect(setMaxMembers).toHaveBeenLastCalledWith({ hostId: 'h1', teamId: 'b', label: 'Bram' }, 7)
    fireEvent.click(screen.getByRole('button', { name: '調低成員上限（Alma）' }))
    expect(setMaxMembers).toHaveBeenLastCalledWith({ hostId: 'h1', teamId: 'a', label: 'Alma' }, 3)
  })

  it('the default field text (no labelText) is 「上限」 and still names the team', () => {
    useI18nStore.getState().setLocale('zh-TW')
    render(<CapStepper hostId="h1" team={team('a')} label="Alma" />)
    expect(names()).toEqual(['調低上限（Alma）', '調高上限（Alma）'])
  })

  it('a custom locale saved before this change, with the old bare keys, does not take the names away (the buttons use their own keys; a missing one falls back to the built-in English)', () => {
    const id = useI18nStore.getState().importLocale({ name: 'Old custom', translations: { 'unattended.cap.minus': 'Lower', 'unattended.cap.plus': 'Raise', 'unattended.cap.label': 'Cap' } })
    useI18nStore.getState().setLocale(id)
    mountTwoTeams()
    expect(names()).toEqual(['Decrease 成員上限 (Alma)', 'Increase 成員上限 (Alma)', 'Decrease 成員上限 (Bram)', 'Increase 成員上限 (Bram)'])
    expect(names()).not.toContain('Lower')
    expect(names()).not.toContain('Raise')
  })

  it('two hosts that show the same team name: the host is in the name, so the buttons are still told apart (the heading above is no part of a button\'s name)', () => {
    useI18nStore.getState().setLocale('zh-TW')
    useHostStore.setState({ hosts: { h1: hostCfg('h1', 'mlab', 0), h2: hostCfg('h2', 'air26', 1) }, hostOrder: ['h1', 'h2'] } as never)
    render(
      <div>
        <CapStepper hostId="h1" team={team('a')} label="Alma" labelText="成員上限" />
        <CapStepper hostId="h2" team={team('a2')} label="Alma" labelText="成員上限" />
      </div>,
    )
    expect(names()).toEqual(['調低成員上限（Alma · mlab）', '調高成員上限（Alma · mlab）', '調低成員上限（Alma · air26）', '調高成員上限（Alma · air26）'])
    expect(screen.getByRole('button', { name: '調高成員上限（Alma · air26）' })).toBeInTheDocument()
    const boxes = screen.getAllByTestId('cap-value').map((b) => b.getAttribute('aria-label') ?? b.querySelector('[aria-label]')?.getAttribute('aria-label'))
    expect(new Set(boxes).size).toBe(2)
  })

  it('with one host the name does not carry it (nothing to tell apart)', () => {
    useI18nStore.getState().setLocale('zh-TW')
    useHostStore.setState({ hosts: { h1: hostCfg('h1', 'mlab', 0) }, hostOrder: ['h1'] } as never)
    render(<CapStepper hostId="h1" team={team('a')} label="Alma" labelText="成員上限" />)
    expect(names()).toEqual(['調低成員上限（Alma）', '調高成員上限（Alma）'])
  })

  it('the number box has the same name without the action, so it is told apart too', () => {
    useI18nStore.getState().setLocale('zh-TW')
    mountTwoTeams()
    const boxes = screen.getAllByTestId('cap-value')
    expect(boxes).toHaveLength(2)
    const boxNames = boxes.map((b) => b.getAttribute('aria-label') ?? b.querySelector('[aria-label]')?.getAttribute('aria-label'))
    expect(new Set(boxNames).size).toBe(2)
    expect(boxNames).toEqual(['成員上限（Alma）: 4', '成員上限（Bram）: 6'])
  })
})
