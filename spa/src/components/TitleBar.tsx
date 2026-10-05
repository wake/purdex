import { useState } from 'react'
import { Columns, Rows, Square } from '@phosphor-icons/react'
import { useTabStore } from '../stores/useTabStore'
import { useAgentStore } from '../stores/useAgentStore'
import { usePaneFocusStore } from '../stores/usePaneFocusStore'
import { useI18nStore } from '../stores/useI18nStore'
import { isAgentPane } from '../hooks/useStatusTargetPane'
import { collectLeaves, currentLayoutPattern } from '../lib/pane-tree'
import { planLayoutChange, type LayoutChangePlan } from '../lib/layout-change'
import type { LayoutPattern, Pane } from '../types/tab'
import { CollapseButton } from '../features/workspace/components/CollapseButton'
import { ConfirmDialog } from './ConfirmDialog'
import { LayoutClosingList, LayoutKeepPicker } from './LayoutKeepPicker'

interface Props { title: string }

const patterns: { pattern: LayoutPattern; icon: typeof Square; labelKey: string }[] = [
  { pattern: 'single', icon: Square, labelKey: 'pane.layout_single' },
  { pattern: 'split-h', icon: Columns, labelKey: 'pane.split_horizontal' },
  { pattern: 'split-v', icon: Rows, labelKey: 'pane.split_vertical' },
]

const BUTTON = 'p-1 rounded cursor-pointer disabled:opacity-40 disabled:pointer-events-none'
const PRESSED = 'text-accent-base bg-accent-base/10'
const IDLE = 'text-text-secondary hover:text-text-primary hover:bg-surface-hover'

/** A layout change waiting on a dialog (cases 2 and 3 of rule D.1a), with the tab's leaves as they were planned. */
type Pending = Exclude<LayoutChangePlan, { kind: 'apply' }> & {
  /** Keys the dialog, so a re-plan opens a fresh one (the picker's ticks start from the new plan). */
  seq: number
  tabId: string
  pattern: LayoutPattern
  leaves: Pane[]
}

let pendingSeq = 0

/**
 * Change tab `tabId` to `pattern` under rule D.1a, read from the stores now (shell cleanup spec §10). Nothing happens
 * when the tab is gone or already has that pattern. Case 1 applies here; cases 2 and 3 come back as the dialog to
 * show.
 */
function startLayoutChange(tabId: string, pattern: LayoutPattern): Pending | null {
  const tab = useTabStore.getState().tabs[tabId]
  if (!tab || currentLayoutPattern(tab.layout) === pattern) return null
  const { agentTypes } = useAgentStore.getState()
  const plan = planLayoutChange(tab.layout, pattern, {
    isAgent: (content) => isAgentPane(content, agentTypes),
    recent: usePaneFocusStore.getState().recent[tabId],
  })
  if (plan.kind === 'apply') {
    useTabStore.getState().applyLayout(tabId, pattern, plan.keepIds)
    return null
  }
  return { ...plan, seq: ++pendingSeq, tabId, pattern, leaves: collectLeaves(tab.layout) }
}

/** Same panes, same contents: a content change replaces the pane object, so identity is enough. */
function sameLeaves(a: readonly Pane[], b: readonly Pane[]): boolean {
  return a.length === b.length && a.every((p, i) => p === b[i])
}

export function TitleBar({ title }: Props) {
  const t = useI18nStore((s) => s.t)
  const activeTabId = useTabStore((s) => s.activeTabId)
  const current = useTabStore((s) => {
    const tab = s.activeTabId ? s.tabs[s.activeTabId] : undefined
    return tab ? currentLayoutPattern(tab.layout) : null
  })
  const [pending, setPending] = useState<Pending | null>(null)

  // The dialog belongs to the tab it was opened for. Once another tab is shown (a shortcut, a notification, a deep
  // link, a closed tab) it is cancelled, not just hidden, so coming back does not revive it. Adjusted during render
  // (https://react.dev/learn/you-might-not-need-an-effect#adjusting-some-state-when-a-prop-changes), so it never paints
  // over the other tab.
  if (pending && pending.tabId !== activeTabId) setPending(null)

  const handlePattern = (pattern: LayoutPattern) => {
    if (!activeTabId || pattern === current) return
    setPending(startLayoutChange(activeTabId, pattern))
  }

  const finish = (keepIds: string[]) => {
    if (!pending) return
    const { tabs, activeTabId: shown } = useTabStore.getState()
    // Read live, not from the render: the active tab can change in the same click, before this component re-renders.
    if (shown !== pending.tabId) {
      setPending(null)
      return
    }
    const tab = tabs[pending.tabId]
    if (tab && !sameLeaves(collectLeaves(tab.layout), pending.leaves)) {
      // The tab's panes changed under the dialog: what the user agreed to close is no longer what would close.
      setPending(startLayoutChange(pending.tabId, pending.pattern))
      return
    }
    setPending(null)
    if (tab) useTabStore.getState().applyLayout(pending.tabId, pending.pattern, keepIds)
  }

  return (
    <>
      <div
        className="shrink-0 relative flex items-center bg-surface-secondary border-b border-border-subtle px-2"
        style={{ height: 36, WebkitAppRegion: 'drag' } as React.CSSProperties}
      >
        {/* macOS traffic-light reserve (titleBarStyle='hiddenInset' draws them at x=12, y=12). */}
        <div className="w-[72px] shrink-0" aria-hidden="true" />
        <div
          data-testid="sidebar-toggle"
          className="shrink-0 flex items-center translate-y-[2.5px]"
          style={{ WebkitAppRegion: 'no-drag' } as React.CSSProperties}
        >
          <CollapseButton variant="topbar" />
        </div>

        <div className="absolute inset-0 flex items-center justify-center pointer-events-none select-none px-2 gap-2">
          <span className="text-xs text-text-secondary truncate max-w-[calc(100%-27rem)]">{title}</span>
        </div>

        <div className="flex-1" />
        <div
          data-testid="layout-buttons"
          className="shrink-0 flex items-center gap-0.5 translate-y-[2.5px]"
          style={{ WebkitAppRegion: 'no-drag' } as React.CSSProperties}
        >
          {patterns.map(({ pattern, icon: Icon, labelKey }) => {
            const pressed = pattern === current
            return (
              <button
                key={pattern}
                disabled={!activeTabId}
                aria-pressed={pressed}
                className={`${BUTTON} ${pressed ? PRESSED : IDLE}`}
                title={t(labelKey)}
                onClick={() => handlePattern(pattern)}
              >
                <Icon size={14} />
              </button>
            )
          })}
        </div>
      </div>

      {pending?.kind === 'confirm' && (
        <ConfirmDialog
          key={pending.seq}
          testIdPrefix="layout-apply"
          title={t('pane.layout_confirm_title')}
          body={t('pane.layout_confirm_body')}
          confirmLabel={t('pane.layout_apply')}
          onCancel={() => setPending(null)}
          onConfirm={() => finish(pending.keepIds)}
        >
          <LayoutClosingList testIdPrefix="layout-apply" panes={pending.closing} />
        </ConfirmDialog>
      )}
      {pending?.kind === 'pick' && (
        <LayoutKeepPicker
          key={pending.seq}
          k={pending.k}
          candidates={pending.candidates}
          preselected={pending.preselected}
          onCancel={() => setPending(null)}
          onConfirm={finish}
        />
      )}
    </>
  )
}
