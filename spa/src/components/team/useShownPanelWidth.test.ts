// spa/src/components/team/useShownPanelWidth.test.ts — the one width the team panel draws and the journal cards (CardStack) hug.
import { describe, it, expect, beforeEach } from 'vitest'
import { act, renderHook } from '@testing-library/react'
import { useShownPanelWidth } from './useShownPanelWidth'
import { BESIDE_CELLS_W, LINE_SLACK_W, cellsWidth, cellWidthFor } from './panel-layout'
import { useTeamUiStore } from '../../stores/useTeamUiStore'
import { usePanelDraftStore } from '../../stores/usePanelDraftStore'
import type { TeamPanelTeam } from './team-display'

const team = (mode: TeamPanelTeam['mode'], members: number) =>
  ({ mode, lead: {}, members: Array.from({ length: members }, () => ({})) }) as unknown as TeamPanelTeam
const lead = BESIDE_CELLS_W + cellsWidth(1, cellWidthFor('badge'))

describe('useShownPanelWidth', () => {
  beforeEach(() => {
    useTeamUiStore.setState({ panel: { width: 900, followsMin: false } })
    usePanelDraftStore.getState().setDraft(null)
  })
  it('one line follows the content, full and a drilled workbook take the area width', () => {
    expect(renderHook(() => useShownPanelWidth(team('line', 0), false)).result.current).toBe(lead + LINE_SLACK_W)
    expect(renderHook(() => useShownPanelWidth(team('full', 0), false)).result.current).toBe(900)
    expect(renderHook(() => useShownPanelWidth(team('line', 0), true)).result.current).toBe(900)
    expect(renderHook(() => useShownPanelWidth(null, false)).result.current).toBe(900)
  })
  it('the dragged draft is the cap while dragging', () => {
    const { result } = renderHook(() => useShownPanelWidth(team('line', 20), false))
    expect(result.current).toBe(900)
    act(() => usePanelDraftStore.getState().setDraft(400))
    expect(result.current).toBe(400)
  })
})
