// spa/src/components/team/TeamSettingsPanel.test.tsx — the team settings (journal v5 §3.9, U14): 「團隊設定・{name}」 with the
// cap and the folders moved from the unattended panel (their own cases: TeamSettingsPanel.cap.test.tsx, TeamRootsEditor*.test.tsx).
// Real stores and panel.
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { fireEvent, render, screen, within } from '@testing-library/react'
import { createRef } from 'react'
import { TeamSettingsPanel } from './TeamSettingsPanel'
import { useHostStore } from '../../stores/useHostStore'
import { useI18nStore } from '../../stores/useI18nStore'
import { useTeamRosterStore } from '../../stores/useTeamRosterStore'
import { useUnattendedStore } from '../../stores/useUnattendedStore'
import type { RosterSession, TeamRoster } from '../../lib/team/roster'

const A = 'host-a'
const T = 't-L'
const sess = (id: string): RosterSession => ({ session_id: id, ref: `_${id}`, address: `mlab/${id}-xx`, live: true })
const team = (over: Partial<TeamRoster> = {}): TeamRoster => ({
  id: T, host_id: 'd', created_at: 1, team_name: 'Payments', team_label: '', lead: sess('L'),
  members: [{ ...sess('M1'), state: 'active', origin: 'spawned', joined_at: 1 }], ...over,
})

function openSettings() {
  const anchorRef = createRef<HTMLButtonElement>()
  const onClose = vi.fn()
  const utils = render(<><button ref={anchorRef} /><TeamSettingsPanel target={{ hostId: A, teamId: T, name: 'Payments' }} anchorRef={anchorRef} onClose={onClose} /></>)
  return { ...utils, onClose }
}
const settings = () => screen.getByTestId('team-settings-panel')

beforeEach(() => {
  useI18nStore.getState().setLocale('zh-TW')
  useHostStore.setState({ hosts: { [A]: { id: A, name: 'mlab', ip: '1', port: 1, token: 't', order: 0 } }, hostOrder: [A], activeHostId: A, runtime: {} })
  useTeamRosterStore.getState().reset()
  useTeamRosterStore.getState().apply(A, [team({ max_members: 3, in_use: 1, roots: ['/w'], roots_rev: 1 })])
  useUnattendedStore.getState().reset()
  useUnattendedStore.getState().setMaxMembersSupport(A, 'yes')
})

describe('the panel', () => {
  it('is titled 「團隊設定・{team name}」 and closes from its ×', () => {
    const { onClose } = openSettings()
    expect(within(settings()).getByText('團隊設定・Payments')).toBeInTheDocument()
    fireEvent.click(within(settings()).getByTestId('floating-panel-close'))
    expect(onClose).toHaveBeenCalled()
  })

  it('the sections come in order — 成員上限, 資料夾 — separated by lines', () => {
    openSettings()
    const body = within(settings()).getByTestId('team-settings-body')
    expect(Array.from(body.children).map((c) => c.getAttribute('data-testid'))).toEqual(['team-settings-cap', 'team-settings-folders'])
    expect(body.className).toContain('divide-y')
  })

  it('a team the roster no longer holds says it ended', () => {
    useTeamRosterStore.getState().apply(A, [])
    openSettings()
    expect(within(settings()).getByTestId('team-settings-ended')).toHaveTextContent('這個團隊已結束')
  })
})
