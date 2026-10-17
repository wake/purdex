// spa/src/features/workspace/components/InlineTabList.fresh.test.tsx — 「已寫好接力檔」 on the sidebar rows (relay v2 contract 5):
// a plain tab by (hostId, code), a team's lead row / member bead / ghost lead by the roster. Real stores and provider.
import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { act, cleanup, render, screen, within } from '@testing-library/react'
import { DndContext } from '@dnd-kit/core'
import { InlineTabList } from './InlineTabList'
import { TeamDisplayProvider } from '../../../components/team/TeamDisplayProvider'
import { useAgentStore } from '../../../stores/useAgentStore'
import { useHostStore } from '../../../stores/useHostStore'
import { useI18nStore } from '../../../stores/useI18nStore'
import { useRelayFreshStore } from '../../../stores/useRelayFreshStore'
import { useShownHostsStore } from '../../../stores/useShownHostsStore'
import { useTabStore } from '../../../stores/useTabStore'
import { useTeamRosterStore } from '../../../stores/useTeamRosterStore'
import { useTeamUiStore } from '../../../stores/useTeamUiStore'
import { useWorkspaceStore } from '../store'
import { HOST, KEY, resetTeamStores, seedScene } from '../../../lib/team/__tests__/team-fixture'

function List() {
  const tabs = useTabStore((s) => s.tabs)
  const ids = useWorkspaceStore((s) => s.workspaces.find((w) => w.id === 'w1')?.tabs ?? [])
  return (
    <InlineTabList tabIds={ids} tabsById={tabs} activeTabId={null} sourceWsId="w1" onSelect={() => {}} onClose={() => {}} onMiddleClick={() => {}} onContextMenu={() => {}} />
  )
}
const mount = () => render(<DndContext><TeamDisplayProvider><List /></TeamDisplayProvider></DndContext>)
const marks = (el: HTMLElement) => within(el).queryAllByTestId('half-handoff-mark')
const rowOf = (title: string) => screen.getAllByTestId('inline-tab-row').find((r) => within(r).queryByTestId('inline-tab-title')?.textContent?.includes(title))!
const bead = (id: string) => screen.getAllByTestId('team-bead').find((b) => b.getAttribute('data-session-id') === id)!

function setFresh(patch: { lead?: boolean; members?: string[] }) {
  const team = useTeamRosterStore.getState().byHost[HOST][0]
  const fh = { op_id: 'o', path: '/w/handoff.md', written_at: Date.now() }
  act(() => useTeamRosterStore.getState().apply(HOST, [{
    ...team,
    lead: patch.lead ? { ...team.lead, fresh_handoff: fh } : { ...team.lead, fresh_handoff: undefined },
    members: team.members.map((m) => (patch.members?.includes(m.session_id) ? { ...m, fresh_handoff: fh } : { ...m, fresh_handoff: undefined })),
  }]))
}

beforeEach(() => {
  cleanup()
  resetTeamStores()
  useRelayFreshStore.getState().reset()
  useShownHostsStore.setState({ ids: [HOST] })
  useAgentStore.setState({ statuses: {}, agentTypes: {}, unread: {}, subagents: {} })
  useHostStore.setState({ hosts: {} })
  useI18nStore.getState().setLocale('zh-TW')
})
afterEach(() => cleanup())

const scene = { members: [['A', 'a-tm'], ['B', 'b-tm']] as Array<[string, string]>, tabs: [['lead', 'lead-tm'], ['ma', 'a-tm'], ['mb', 'b-tm'], ['solo', 'solo-tm']] as Array<[string, string | null]>, workspaces: [{ id: 'w1', tabs: ['lead', 'ma', 'mb', 'solo'] }] }

describe('the sidebar rows', () => {
  it('a plain tab shows the mark when the list holds its (host, code), and loses it when the list drops it', () => {
    seedScene({ ...scene, listed: ['lead-tm', 'a-tm', 'b-tm', 'solo-tm'] })
    mount()
    expect(screen.queryByTestId('half-handoff-mark')).toBeNull()
    act(() => useRelayFreshStore.getState().apply(HOST, [{ session_id: 'X', ref: '_x', op_id: 'o', path: '/w/solo.md', written_at: Date.now(), code: 'code-solo-tm' }]))
    const solo = marks(rowOf('solo-tm'))
    expect(solo).toHaveLength(1)
    expect(solo[0]).toHaveAttribute('data-variant', 'icon')
    expect(solo[0]).toHaveAttribute('aria-label', '已寫好接力檔')
    expect(solo[0].getAttribute('title')).toContain('/w/solo.md')
    expect(screen.getAllByTestId('half-handoff-mark')).toHaveLength(1)
    act(() => useRelayFreshStore.getState().apply(HOST, []))
    expect(screen.queryByTestId('half-handoff-mark')).toBeNull()
  })

  it('an entry without a code marks no row, even one whose session id equals a tab\'s code', () => {
    seedScene(scene)
    mount()
    act(() => useRelayFreshStore.getState().apply(HOST, [{ session_id: 'code-solo-tm', ref: '_solo-tm', op_id: 'solo-tm', path: '/w/solo.md', written_at: 1 }]))
    expect(screen.queryByTestId('half-handoff-mark')).toBeNull()
  })

  it('the lead row and a member bead read the roster', () => {
    seedScene(scene)
    mount()
    setFresh({ lead: true, members: ['A'] })
    const block = screen.getByTestId('team-lead-block')
    const leadRow = within(block).getAllByTestId('inline-tab-row')[0]
    expect(marks(leadRow)).toHaveLength(1)
    expect(marks(bead('A'))).toHaveLength(1)
    expect(marks(bead('B'))).toHaveLength(0)
    setFresh({ lead: false, members: [] })
    expect(screen.queryByTestId('half-handoff-mark')).toBeNull()
  })

  it('a ghost lead row (its tab closed) shows the mark from the roster', () => {
    seedScene({ ...scene, tabs: [['solo', 'solo-tm']], workspaces: [{ id: 'w1', tabs: ['solo'] }] })
    act(() => useTeamUiStore.getState().setGhostWorkspace(KEY, 'w1'))
    mount()
    const ghost = screen.getByTestId('team-ghost-lead')
    expect(marks(ghost)).toHaveLength(0)
    setFresh({ lead: true })
    expect(marks(ghost)).toHaveLength(1)
  })

  it('the mark does not push the title out: it sits after the title and the title keeps truncating', () => {
    seedScene(scene)
    mount()
    act(() => useRelayFreshStore.getState().apply(HOST, [{ session_id: 'X', ref: '_x', op_id: 'o', path: '/p', written_at: 1, code: 'code-solo-tm' }]))
    const row = rowOf('solo-tm')
    const title = within(row).getByTestId('inline-tab-title')
    const mark = within(row).getByTestId('half-handoff-mark')
    expect(title.className).toContain('truncate')
    expect(mark.className).toContain('flex-shrink-0')
    expect(title.compareDocumentPosition(mark) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy()
  })
})

vi.mock('../../../components/team/TeamSeatIcon', () => ({
  TeamSeatIcon: () => <span data-testid="seat-icon" />,
  TeamSeatHostBadge: () => <span data-testid="seat-host" />,
}))
