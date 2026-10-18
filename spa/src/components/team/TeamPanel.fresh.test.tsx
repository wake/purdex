// spa/src/components/team/TeamPanel.fresh.test.tsx — 「已寫好接力檔」 on the team panel's rows (relay v2 contract 5): shown exactly when
// the roster row has `fresh_handoff`, as a chip beside the title; the title-bar cell carries it in the tooltip (it has no room).
import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { act, cleanup, render, screen, within } from '@testing-library/react'
import { TeamDisplayProvider } from './TeamDisplayProvider'
import { TeamPanelArea } from './TeamPanelArea'
import { TeamCell } from './TeamCell'
import type { TeamSeatView } from './team-display'
import { HOST, KEY, resetTeamStores, seedScene } from '../../lib/team/__tests__/team-fixture'
import { useTeamRosterStore } from '../../stores/useTeamRosterStore'
import { useTeamUiStore } from '../../stores/useTeamUiStore'
import { useWorkbookStore } from '../../stores/useWorkbookStore'
import { useI18nStore } from '../../stores/useI18nStore'
import { useShownHostsStore } from '../../stores/useShownHostsStore'
import { clearModuleRegistry } from '../../lib/module-registry'

vi.mock('./TeamSeatIcon', () => ({
  TeamSeatIcon: () => <span data-testid="seat-icon" />,
  TeamSeatHostBadge: () => <span data-testid="seat-host" />,
}))

const mount = () => render(<TeamDisplayProvider><TeamPanelArea /></TeamDisplayProvider>)
const row = (id: string) => screen.getAllByTestId('team-panel-row').find((r) => r.getAttribute('data-session-id') === id)!
const fh = { op_id: 'o', path: '/w/purdex/handoff.md', written_at: new Date(2026, 9, 17, 8, 30).getTime() }

function mark(ids: string[]) {
  const teams = useTeamRosterStore.getState().byHost[HOST]
  act(() => useTeamRosterStore.getState().apply(HOST, teams.map((t) => ({
    ...t,
    lead: ids.includes(t.lead.session_id) ? { ...t.lead, fresh_handoff: fh } : { ...t.lead, fresh_handoff: undefined },
    members: t.members.map((m) => (ids.includes(m.session_id) ? { ...m, fresh_handoff: fh } : { ...m, fresh_handoff: undefined })),
  }))))
}

beforeEach(() => {
  cleanup()
  localStorage.clear()
  resetTeamStores()
  useWorkbookStore.getState().reset()
  useTeamUiStore.setState({ panel: { width: 412 }, teamDrill: {}, endedSeats: {} })
  clearModuleRegistry()
  useShownHostsStore.setState({ ids: [HOST] })
  useI18nStore.getState().setLocale('zh-TW')
  seedScene({ members: [['A', 'a-tm'], ['B', 'b-tm']], tabs: [['lead', 'lead-tm'], ['ma', 'a-tm']], workspaces: [{ id: 'w1', tabs: ['lead', 'ma'] }], activeTabId: 'lead' })
})
afterEach(() => cleanup())

describe('the handoff chip on the panel rows', () => {
  it('appears only on rows whose roster entry has fresh_handoff, lead and members alike, with the path in its tooltip', () => {
    mark(['L', 'A'])
    mount()
    const lead = within(row('L')).getByTestId('half-handoff-mark')
    expect(lead).toHaveAttribute('data-variant', 'chip')
    expect(lead).toHaveTextContent('已寫好接力檔')
    expect(lead.getAttribute('title')).toContain('/w/purdex/handoff.md')
    expect(within(row('A')).getByTestId('half-handoff-mark')).toBeTruthy()
    expect(within(row('B')).queryByTestId('half-handoff-mark')).toBeNull()
  })

  it('is absent when nothing is written, and goes the moment the roster drops it', () => {
    mount()
    expect(screen.queryByTestId('half-handoff-mark')).toBeNull()
    mark(['B'])
    expect(within(row('B')).getByTestId('half-handoff-mark')).toBeTruthy()
    mark([])
    expect(screen.queryByTestId('half-handoff-mark')).toBeNull()
  })

  it('sits after the title and keeps its width, so the title is what truncates', () => {
    mark(['B'])
    mount()
    const r = row('B')
    const chip = within(r).getByTestId('half-handoff-mark')
    expect(chip.className).toContain('flex-shrink-0')
    const title = within(r).getByText('title B')
    expect(title.className).toContain('truncate')
    expect(title.compareDocumentPosition(chip) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy()
  })
})

describe('the title-bar cell', () => {
  const seat: TeamSeatView = { sessionId: 'A', title: 'title A', address: 'mlab/A-xx', hostId: HOST, sessionCode: 'code-a-tm', role: 'member', tabId: 'ma', state: 'active', hostAlias: '', remote: false }
  it('carries the handoff in its tooltip only: no mark, no extra width', () => {
    render(<TeamCell teamKey={KEY} seat={seat} isActive={false} onOpen={() => {}} />)
    expect(screen.getByTestId('team-panel-cell').getAttribute('title')).not.toContain('已寫好接力檔')
    cleanup()
    mark(['A'])
    render(<TeamCell teamKey={KEY} seat={seat} isActive={false} onOpen={() => {}} />)
    expect(screen.getByTestId('team-panel-cell').getAttribute('title')).toContain('已寫好接力檔')
    expect(screen.queryByTestId('half-handoff-mark')).toBeNull()
  })
})
