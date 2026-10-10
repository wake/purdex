// spa/src/components/team/TeamPanel.workbook.test.tsx — the team panel's workbook parts (WA-2b-1): the task line, the
// workbook button, the drilled-in view, the ended list. Real stores and provider; the light and host badge are stand-ins.
import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { act, cleanup, fireEvent, render, screen, within } from '@testing-library/react'
import { TeamDisplayProvider } from './TeamDisplayProvider'
import { TeamPanelArea } from './TeamPanelArea'
import { HOST, KEY, resetTeamStores, seedScene } from '../../lib/team/__tests__/team-fixture'
import { seedWorkbook } from '../../lib/team/__tests__/workbook-fixture'
import { useTeamUiStore } from '../../stores/useTeamUiStore'
import { useWorkbookStore } from '../../stores/useWorkbookStore'
import { useI18nStore } from '../../stores/useI18nStore'
import { clearModuleRegistry } from '../../lib/module-registry'

vi.mock('./TeamSeatIcon', () => ({
  TeamSeatIcon: () => <span data-testid="seat-icon" />,
  TeamSeatHostBadge: () => <span data-testid="seat-host" />,
}))

function scene() {
  seedScene({
    members: [['A', 'a-tm'], ['B', 'b-tm']],
    tabs: [['lead', 'lead-tm'], ['ma', 'a-tm']],
    workspaces: [{ id: 'w1', tabs: ['lead', 'ma'] }],
    activeTabId: 'lead',
  })
}
const mount = () => render(<TeamDisplayProvider><TeamPanelArea /></TeamDisplayProvider>)
const row = (id: string) => screen.getAllByTestId('team-panel-row').find((r) => r.getAttribute('data-session-id') === id)!

beforeEach(() => {
  cleanup()
  localStorage.clear()
  resetTeamStores()
  useWorkbookStore.getState().reset()
  useTeamUiStore.setState({ panel: { width: 412 }, teamDrill: {}, endedSeats: {}, workbookTabs: {} })
  clearModuleRegistry()
  useI18nStore.getState().setLocale('zh-TW')
})
afterEach(() => cleanup())

describe('the task line', () => {
  it('shows the first sentence of the status as line 2, the whole status in the tooltip', () => {
    scene()
    seedWorkbook(HOST, 'A', { status: '正在修登入頁。接著補測試。' })
    mount()
    const task = within(row('A')).getByTestId('team-panel-task')
    expect(task).toHaveTextContent('正在修登入頁')
    expect(task).not.toHaveTextContent('接著補測試')
    expect(task.getAttribute('title')).toBe('正在修登入頁。接著補測試。')
  })
  it('a seat without a workbook, or with a blank status, stays one line', () => {
    scene()
    seedWorkbook(HOST, 'B', { status: '   ' })
    mount()
    expect(within(row('A')).queryByTestId('team-panel-task')).toBeNull()
    expect(within(row('B')).queryByTestId('team-panel-task')).toBeNull()
  })
  it('a host without workbook.v1 shows no task', () => {
    scene()
    seedWorkbook(HOST, 'A', { status: 'busy' }, { v1: false })
    mount()
    expect(within(row('A')).queryByTestId('team-panel-task')).toBeNull()
  })
})

// keep imports used by the later sections of this file
void KEY; void act; void fireEvent
