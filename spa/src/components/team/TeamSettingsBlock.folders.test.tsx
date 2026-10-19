// spa/src/components/team/TeamSettingsBlock.folders.test.tsx — 「資料夾」 in the team settings block (name pool v2 spec §9.5, NA-0 design): the team's roots as truncated
// chips and a dashed +; the + opens the roots editor under them (the unattended panel's editor, same store and writer), and shows only for a roster that carries `roots`.
import { describe, it, expect, beforeEach } from 'vitest'
import { fireEvent, render, screen, within } from '@testing-library/react'
import { TeamSettingsBlock } from './TeamSettingsBlock'
import { useHostStore } from '../../stores/useHostStore'
import { useI18nStore } from '../../stores/useI18nStore'
import { useTeamRosterStore } from '../../stores/useTeamRosterStore'
import { useUnattendedStore } from '../../stores/useUnattendedStore'
import { useRelayQuotaStore } from '../../lib/team/relay-quota'
import type { RosterSession, TeamRoster } from '../../lib/team/roster'

const A = 'host-a'
const sess = (id: string): RosterSession => ({ session_id: id, ref: `_${id}`, address: `mlab/${id}-xx`, live: true })
const team = (over: Partial<TeamRoster> = {}): TeamRoster => ({
  id: 't-l1', host_id: 'd', created_at: 1, team_name: '', team_label: '', lead: sess('l1'), members: [], roots: ['/Users/wake/a', '/b'], roots_rev: 3, ...over,
})
const show = () => render(<TeamSettingsBlock hostId={A} teamId="t-l1" name="Team" />)

beforeEach(() => {
  useI18nStore.getState().setLocale('zh-TW')
  useHostStore.setState({ hosts: { [A]: { id: A, name: 'mlab', ip: '1', port: 1, token: 't', order: 0 } }, hostOrder: [A], activeHostId: A, runtime: {} })
  useTeamRosterStore.getState().reset()
  useRelayQuotaStore.getState().reset()
  useUnattendedStore.getState().reset()
})

describe('the folders line', () => {
  it('shows a chip per root (the full path in its tooltip) and a pencil', () => {
    useTeamRosterStore.getState().apply(A, [team()])
    show()
    const line = screen.getByTestId('team-settings-folders')
    expect(within(line).getByText('資料夾')).toBeInTheDocument()
    const chips = within(line).getAllByTestId('team-folder-chip')
    expect(chips.map((c) => c.textContent)).toEqual(['/Users/wake/a', '/b'])
    expect(chips[0]).toHaveAttribute('title', '/Users/wake/a')
    expect(chips[0].className).toContain('truncate')
    expect(within(line).getByTestId('team-folder-edit')).toBeInTheDocument()
  })

  it('is the only line a roster with roots alone brings (no cap, no quota): the block still shows', () => {
    useTeamRosterStore.getState().apply(A, [team()])
    show()
    expect(screen.getByTestId('team-settings-block')).toBeInTheDocument()
    expect(screen.queryByTestId('team-settings-cap')).toBeNull()
    expect(screen.queryByTestId('team-settings-quota')).toBeNull()
  })

  it('is not there for a roster without roots (an older daemon), nor roots without their revision', () => {
    useTeamRosterStore.getState().apply(A, [team({ roots: undefined, roots_rev: undefined })])
    const { unmount } = show()
    expect(screen.queryByTestId('team-settings-folders')).toBeNull()
    unmount()
    useTeamRosterStore.getState().apply(A, [team({ roots_rev: undefined })])
    show()
    expect(screen.queryByTestId('team-settings-folders')).toBeNull()
  })

  it('the + opens the editor under the chips, and a second click folds it away', () => {
    useTeamRosterStore.getState().apply(A, [team()])
    show()
    expect(screen.queryByTestId('roots-editor')).toBeNull()
    fireEvent.click(screen.getByTestId('team-folder-edit'))
    const editor = screen.getByTestId('roots-editor')
    expect(editor.className).not.toContain('col-span-full')
    expect(editor.className).not.toContain('pl-3')
    expect(within(editor).getAllByTestId('roots-path').map((p) => p.textContent)).toEqual(['/Users/wake/a', '/b'])
    expect(screen.getByTestId('team-folder-edit')).toHaveAttribute('aria-expanded', 'true')
    fireEvent.click(screen.getByTestId('team-folder-edit'))
    expect(screen.queryByTestId('roots-editor')).toBeNull()
  })

  it('switching the block to another team folds the editor away and starts clean', () => {
    useTeamRosterStore.getState().apply(A, [team(), team({ id: 't-l2', lead: sess('l2'), roots: ['/other'] })])
    const view = render(<TeamSettingsBlock hostId={A} teamId="t-l1" name="Team" />)
    fireEvent.click(screen.getByTestId('team-folder-edit'))
    expect(screen.getByTestId('roots-editor')).toBeInTheDocument()
    view.rerender(<TeamSettingsBlock hostId={A} teamId="t-l2" name="Team 2" />)
    expect(screen.queryByTestId('roots-editor')).toBeNull()
    expect(screen.getAllByTestId('team-folder-chip').map((c) => c.textContent)).toEqual(['/other'])
  })

  it('a team with no roots yet shows just the + (the editor adds the first)', () => {
    useTeamRosterStore.getState().apply(A, [team({ roots: [] })])
    show()
    expect(screen.queryAllByTestId('team-folder-chip')).toHaveLength(0)
    expect(screen.getByTestId('team-folder-edit')).toBeInTheDocument()
  })
})
