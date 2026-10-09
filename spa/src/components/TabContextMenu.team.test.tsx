// spa/src/components/TabContextMenu.team.test.tsx — spec R12: the pin item of a tab in a team group is disabled (with the
// reason), and the store refuses it too (a shortcut cannot pin it).
import { describe, it, expect, beforeEach, vi } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import { TabContextMenu } from './TabContextMenu'
import { TeamDisplayProvider } from './team/TeamDisplayProvider'
import { useTabStore } from '../stores/useTabStore'
import { useI18nStore } from '../stores/useI18nStore'
import { resetTeamStores, seedScene } from '../lib/team/__tests__/team-fixture'

beforeEach(() => {
  resetTeamStores()
  useI18nStore.getState().setLocale('en')
  seedScene({
    members: [['A', 'a-tm']],
    tabs: [['lead', 'lead-tm'], ['ma', 'a-tm'], ['x', null]],
    workspaces: [{ id: 'w1', tabs: ['lead', 'ma', 'x'] }],
  })
})

function menuFor(tabId: string) {
  const onAction = vi.fn()
  render(
    <TeamDisplayProvider>
      <TabContextMenu tab={useTabStore.getState().tabs[tabId]} position={{ x: 1, y: 1 }} onClose={vi.fn()} onAction={onAction} hasOtherUnlocked hasRightUnlocked />
    </TeamDisplayProvider>,
  )
  return onAction
}

describe('pin and teams', () => {
  it.each(['lead', 'ma'])('the menu disables "Pin tab" for the %s tab, with the reason as its title', (tabId) => {
    const onAction = menuFor(tabId)
    const pin = screen.getByText('Pin tab').closest('button')!
    expect(pin).toBeDisabled()
    expect(pin).toHaveAttribute('title', 'Tabs in a team group cannot be pinned')
    fireEvent.click(pin)
    expect(onAction).not.toHaveBeenCalledWith('pin', undefined)
  })

  it('a tab in no team keeps an enabled pin item without a title', () => {
    const onAction = menuFor('x')
    const pin = screen.getByText('Pin tab').closest('button')!
    expect(pin).toBeEnabled()
    expect(pin).not.toHaveAttribute('title')
    fireEvent.click(pin)
    expect(onAction).toHaveBeenCalledWith('pin', undefined)
  })

  it('togglePin refuses a team tab (a shortcut cannot pin it either) but pins any other, and unpins whatever is pinned', () => {
    useTabStore.getState().togglePin('ma')
    useTabStore.getState().togglePin('lead')
    expect(useTabStore.getState().tabs.ma.pinned).toBe(false)
    expect(useTabStore.getState().tabs.lead.pinned).toBe(false)
    useTabStore.getState().togglePin('x')
    expect(useTabStore.getState().tabs.x.pinned).toBe(true)
    // a tab that got pinned before it joined a team can still be unpinned
    useTabStore.setState((s) => ({ tabs: { ...s.tabs, ma: { ...s.tabs.ma, pinned: true } } }))
    useTabStore.getState().togglePin('ma')
    expect(useTabStore.getState().tabs.ma.pinned).toBe(false)
  })
})
