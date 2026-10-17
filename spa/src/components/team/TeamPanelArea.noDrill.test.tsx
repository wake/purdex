// spa/src/components/team/TeamPanelArea.noDrill.test.tsx — the workbook drill is off when EITHER the tab's own host OR the lead's host is
// journal.v1 (JV-2b codex R3). `useJournalTarget().journalHost` follows the lead's host under a follow route, so it cannot decide alone:
// a tab on a journal host whose lead lives on an older one must not offer the older host's stored drill or the rows' workbook button.
import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { cleanup, render, screen } from '@testing-library/react'

const target = vi.hoisted(() => ({ origin: null as { hostId: string; sessionId: string } | null }))
vi.mock('../journal/useJournalTarget', () => ({
  // The follow route: `journalHost` is the LEAD's answer, `origin` is the tab's own cc session.
  useJournalTarget: () => ({ journalHost: false, origin: target.origin }),
}))
vi.mock('./TeamSeatIcon', () => ({
  TeamSeatIcon: () => <span data-testid="seat-icon" />,
  TeamSeatHostBadge: () => <span data-testid="seat-host" />,
}))

import { TeamPanelArea } from './TeamPanelArea'
import { TeamDisplayProvider } from './TeamDisplayProvider'
import { HOST, KEY, resetTeamStores, seedScene } from '../../lib/team/__tests__/team-fixture'
import { seedWorkbook } from '../../lib/team/__tests__/workbook-fixture'
import { setJournalHost } from '../../lib/journal/__tests__/journal-fixture'
import { useJournalStore } from '../../stores/useJournalStore'
import { useTeamUiStore } from '../../stores/useTeamUiStore'
import { useWorkbookStore } from '../../stores/useWorkbookStore'
import { useShownHostsStore } from '../../stores/useShownHostsStore'

const TAB_HOST = 'h-tab'
const q = (id: string) => screen.queryByTestId(id)
const mount = () => render(<TeamDisplayProvider><TeamPanelArea /></TeamDisplayProvider>)

beforeEach(() => {
  cleanup()
  localStorage.clear()
  resetTeamStores()
  useJournalStore.getState().reset()
  useWorkbookStore.getState().reset()
  useShownHostsStore.setState({ ids: [HOST] })
  seedScene({ members: [['A', 'a-tm']], tabs: [['lead', 'lead-tm'], ['ma', 'a-tm']], workspaces: [{ id: 'w1', tabs: ['lead', 'ma'] }], activeTabId: 'lead' })
  seedWorkbook(HOST, 'A', { status: 'Wiring the login form. Next the tests.' }, { v2: true })
  seedWorkbook(HOST, 'L', { status: 'Planning.' }, { v2: true })
  useTeamUiStore.getState().setTeamDrill(KEY, { hostId: HOST, sessionId: 'A' }) // a drill stored while the lead's host was an older one
  target.origin = { hostId: TAB_HOST, sessionId: 'S' }
})
afterEach(() => cleanup())

describe('the workbook drill needs BOTH hosts to be older', () => {
  it('control: tab host older, lead host older -> the stored drill opens and the rows offer the workbook', () => {
    setJournalHost(TAB_HOST, false)
    setJournalHost(HOST, false)
    mount()
    expect(q('team-seat-workbook')).toBeTruthy()
  })

  it('tab on a journal host, lead on an older one -> no drill, no workbook entry on the rows', () => {
    setJournalHost(TAB_HOST, true)
    setJournalHost(HOST, false)
    mount()
    expect(q('team-seat-workbook')).toBeNull()
    expect(screen.getAllByTestId('team-panel-row').length).toBe(2)
    expect(q('team-panel-workbook')).toBeNull()
    expect(q('team-panel-task')).toBeNull()
    expect(useTeamUiStore.getState().teamDrill[KEY]).toEqual({ hostId: HOST, sessionId: 'A' }) // kept, not erased
  })

  it('tab on an older host, lead on a journal host -> no drill, no workbook entry on the rows', () => {
    setJournalHost(TAB_HOST, false)
    setJournalHost(HOST, true)
    mount()
    expect(q('team-seat-workbook')).toBeNull()
    expect(screen.getAllByTestId('team-panel-row').length).toBe(2)
    expect(q('team-panel-workbook')).toBeNull()
    expect(q('team-panel-task')).toBeNull()
  })
})
