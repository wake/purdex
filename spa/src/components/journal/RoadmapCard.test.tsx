// spa/src/components/journal/RoadmapCard.test.tsx — the roadmap card wired to the real stores (J-2d): the first open reads the roadmap, a
// table per feature, the status word and token per row, the empty state, and its place in the stack (under the team card / alone).
import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { act, cleanup, render, screen, within } from '@testing-library/react'

const getRoadmap = vi.fn()
vi.mock('../../lib/journal/api', () => ({
  getJournal: vi.fn(),
  getTasks: vi.fn(),
  getRoadmap: (...a: unknown[]) => getRoadmap(...a),
  setConfirmItem: vi.fn(),
}))
vi.mock('../team/TeamSeatIcon', () => ({
  TeamSeatIcon: () => <span data-testid="seat-icon" />,
  TeamSeatHostBadge: () => <span data-testid="seat-host" />,
}))

import { CardStack } from './CardStack'
import { TeamDisplayProvider } from '../team/TeamDisplayProvider'
import { HOST, resetTeamStores, seedScene } from '../../lib/team/__tests__/team-fixture'
import { ccTabOf, seedUnit, setJournalHost } from '../../lib/journal/__tests__/journal-fixture'
import { clearViewMemos, patchViewMemo } from '../../lib/journal/view-memory'
import { clearModuleRegistry } from '../../lib/module-registry'
import { useTabStore } from '../../stores/useTabStore'
import { useJournalUiStore } from '../../stores/useJournalUiStore'
import { useJournalStore } from '../../stores/useJournalStore'
import { useShownHostsStore } from '../../stores/useShownHostsStore'
import { useI18nStore } from '../../stores/useI18nStore'
import type { RoadmapFeature } from '../../lib/journal/types'

const SOLO = { kind: 'solo' as const, id: 'a', label: 'Solo A' }
const TEAM = { kind: 'team' as const, id: 'tm1', label: 'Journal team' }
const FEATURES: RoadmapFeature[] = [
  { name: 'Journal', stages: [
    { stage: 'J-1', description: 'daemon', status: '完成' },
    { stage: 'J-2', description: 'Mac App', status: '進行中' },
    { stage: 'J-3', description: 'iOS', status: '未開始' },
    { stage: 'J-4', description: 'polish', status: '審查中' },
    { stage: 'J-5', description: 'later', status: '暫停' },
  ] },
  { name: 'Sync', stages: [{ stage: 'S-1', description: 'pairing', status: '完成' }] },
]

const setActive = (id: string) => act(() => { useTabStore.setState({ activeTabId: id }) })
const mount = () => render(<TeamDisplayProvider><CardStack /></TeamDisplayProvider>)
const card = () => within(screen.getByTestId('journal-roadmap-card'))
const stageRow = (stage: string) => card().getAllByTestId('journal-roadmap-stage').find((r) => within(r).queryByText(stage))!

beforeEach(() => {
  cleanup()
  localStorage.clear()
  resetTeamStores()
  useJournalStore.getState().reset()
  useJournalUiStore.setState({ lastWidths: {} })
  clearViewMemos()
  clearModuleRegistry()
  getRoadmap.mockReset()
  Object.defineProperty(window, 'innerWidth', { configurable: true, value: 1800 })
  useShownHostsStore.setState({ ids: [HOST] })
  useI18nStore.getState().setLocale('en')
  seedScene({ members: [['A', 'a-tm']], tabs: [['x', null]], workspaces: [{ id: 'w1', tabs: ['x'] }], activeTabId: 'x' })
  act(() => {
    useTabStore.setState((s) => ({
      tabs: { ...s.tabs, a: ccTabOf('a', HOST, 'a-solo', 'SA'), lead: ccTabOf('lead', HOST, 'lead-tm', 'SL') },
      tabOrder: [...s.tabOrder, 'a', 'lead'],
    }))
  })
  setJournalHost(HOST, true)
})
afterEach(() => { cleanup(); Object.defineProperty(window, 'innerWidth', { configurable: true, value: 1024 }) })

const seedSolo = (roadmap: RoadmapFeature[] | null) => {
  const key = seedUnit(HOST, 'SA', SOLO, { roadmap })
  patchViewMemo(HOST, key, { journalOpen: true, roadmapOpen: true })
  setActive('a')
}

describe('the roadmap card: reading', () => {
  it('reads the roadmap the first time it opens (roadmap === null), once', async () => {
    getRoadmap.mockResolvedValue({ kind: 'ok', value: FEATURES })
    seedSolo(null)
    mount()
    expect(screen.getByTestId('journal-roadmap-loading')).toBeTruthy()
    await act(async () => { await Promise.resolve() })
    expect(getRoadmap).toHaveBeenCalledTimes(1)
    expect(getRoadmap).toHaveBeenCalledWith(HOST, 'SA')
    expect(card().getAllByTestId('journal-roadmap-feature')).toHaveLength(2)
  })
  it('does not read again when the roadmap is held already', async () => {
    seedSolo(FEATURES)
    mount()
    await act(async () => { await Promise.resolve() })
    expect(getRoadmap).not.toHaveBeenCalled()
  })
  it('says so when the unit has no roadmap', () => {
    seedSolo([])
    mount()
    expect(screen.getByTestId('journal-roadmap-empty').textContent).toBe('No roadmap yet')
  })
})

describe('the roadmap card: tables', () => {
  it('draws one table per feature with 階段 / 說明 / 狀態 columns', () => {
    seedSolo(FEATURES)
    mount()
    const features = card().getAllByTestId('journal-roadmap-feature')
    expect(features.map((f) => within(f).getByRole('heading').textContent)).toEqual(['Journal', 'Sync'])
    expect(within(features[0]).getAllByRole('columnheader').map((h) => h.textContent)).toEqual(['Stage', 'Description', 'Status'])
    expect(within(features[0]).getAllByTestId('journal-roadmap-stage')).toHaveLength(5)
    expect(within(features[1]).getAllByTestId('journal-roadmap-stage')).toHaveLength(1)
    expect(within(stageRow('J-2')).getByText('Mac App')).toBeTruthy()
  })
  it('shows each status as its word, in its theme token', () => {
    seedSolo(FEATURES)
    mount()
    const status = (stage: string) => within(stageRow(stage)).getByTestId('journal-roadmap-status')
    expect([status('J-3').textContent, status('J-2').textContent, status('J-4').textContent, status('J-1').textContent, status('J-5').textContent])
      .toEqual(['Not started', 'In progress', 'In review', 'Done', 'Paused'])
    expect(status('J-3').className).toContain('text-text-muted')
    expect(status('J-2').className).toContain('text-journal-kind-exec')
    expect(status('J-4').className).toContain('text-journal-kind-comm')
    expect(status('J-1').className).toContain('text-status-success-text')
    expect(status('J-5').className).toContain('text-journal-kind-task-new')
    expect(status('J-5').className).not.toContain('text-status-success-text')
    // The dot shares the word's colour; 暫停 must not use the pale bg-status-warning (near invisible on light themes).
    expect(status('J-5').innerHTML).toContain('bg-journal-kind-task-new')
    expect(status('J-5').innerHTML).not.toContain('bg-status-warning')
    expect(status('J-4').innerHTML).toContain('bg-journal-kind-comm')
  })
  it('the words come in zh-TW too', () => {
    useI18nStore.getState().setLocale('zh-TW')
    seedSolo(FEATURES)
    mount()
    expect(within(stageRow('J-4')).getByTestId('journal-roadmap-status').textContent).toBe('審查中')
    expect(within(stageRow('J-5')).getByTestId('journal-roadmap-status').textContent).toBe('暫停')
  })
})

describe('the roadmap card in the stack', () => {
  it('alone on the left, with or without a team', () => {
    seedSolo(FEATURES)
    const r = mount()
    expect([...screen.getByTestId('journal-side-column').children].map((c) => (c as HTMLElement).dataset.testid)).toEqual(['journal-roadmap-card'])
    r.unmount()
    const key = seedUnit(HOST, 'SL', TEAM, { roadmap: FEATURES })
    patchViewMemo(HOST, key, { journalOpen: true, roadmapOpen: true })
    setActive('lead')
    mount()
    expect([...screen.getByTestId('journal-side-column').children].map((c) => (c as HTMLElement).dataset.testid)).toEqual(['journal-roadmap-card'])
    expect(card().getAllByTestId('journal-roadmap-feature')).toHaveLength(2)
  })
})
