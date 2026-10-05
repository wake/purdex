import { useEffect, useState } from 'react'
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

/**
 * How long the Confirm of a dialog opened by a re-plan stays inert. The click that found the plan changed may be the
 * first of a double-click, and the fresh dialog's Confirm sits where the old one was: the rest of that click must not
 * apply a plan the user has not seen.
 */
export const REPLAN_CONFIRM_LOCK_MS = 500

/** A layout change waiting on a dialog (cases 2 and 3 of rule D.1a), with the tab's leaves as they were planned. */
type Pending = Exclude<LayoutChangePlan, { kind: 'apply' }> & {
  /** Keys the dialog, so a re-plan opens a fresh one (the picker's ticks start from the new plan). */
  seq: number
  /** Opened by a re-plan at Confirm, not by a button press: its Confirm is inert for `REPLAN_CONFIRM_LOCK_MS`. */
  replanned: boolean
  tabId: string
  pattern: LayoutPattern
  leaves: Pane[]
}

let pendingSeq = 0

interface Planned { plan: LayoutChangePlan; leaves: Pane[] }

/**
 * Rule D.1a for changing tab `tabId` to `pattern`, read from the stores now (shell cleanup spec §10): the tab's
 * layout, the live agent set and the focus record. Null when the tab is gone or already has that pattern.
 */
function planNow(tabId: string, pattern: LayoutPattern): Planned | null {
  const tab = useTabStore.getState().tabs[tabId]
  if (!tab || currentLayoutPattern(tab.layout) === pattern) return null
  const { agentTypes } = useAgentStore.getState()
  const plan = planLayoutChange(tab.layout, pattern, {
    isAgent: (content) => isAgentPane(content, agentTypes),
    recent: usePaneFocusStore.getState().recent[tabId],
  })
  return { plan, leaves: collectLeaves(tab.layout) }
}

/**
 * Carry out `planned` (default: planned now) for tab `tabId`. Nothing happens for null. Case 1 applies here; cases 2
 * and 3 come back as the dialog to show.
 */
function startLayoutChange(tabId: string, pattern: LayoutPattern, planned = planNow(tabId, pattern)): Pending | null {
  if (!planned) return null
  const { plan, leaves } = planned
  if (plan.kind === 'apply') {
    useTabStore.getState().applyLayout(tabId, pattern, plan.keepIds)
    return null
  }
  return { ...plan, seq: ++pendingSeq, replanned: false, tabId, pattern, leaves }
}

/** Same panes, same contents: a content change replaces the pane object, so identity is enough. */
function sameLeaves(a: readonly Pane[], b: readonly Pane[]): boolean {
  return a.length === b.length && a.every((p, i) => p === b[i])
}

/** Same ids, in any order (ids are unique within a tab). */
function sameIds(a: readonly string[], b: readonly string[]): boolean {
  return a.length === b.length && a.every((id) => b.includes(id))
}

const idsOf = (panes: readonly Pane[]) => panes.map((p) => p.id)

/**
 * Whether two plans put the same question to the user: the same case, and the same survivors and closing panes (case
 * 2) or the same candidates (case 3). The picker's preselection does not count: once the user is picking, their ticks
 * stand.
 */
function sameQuestion(a: LayoutChangePlan, b: LayoutChangePlan): boolean {
  if (a.kind === 'confirm' && b.kind === 'confirm') {
    return sameIds(a.keepIds, b.keepIds) && sameIds(idsOf(a.closing), idsOf(b.closing))
  }
  if (a.kind === 'pick' && b.kind === 'pick') return a.k === b.k && sameIds(idsOf(a.candidates), idsOf(b.candidates))
  return false
}

export function TitleBar({ title }: Props) {
  const t = useI18nStore((s) => s.t)
  const activeTabId = useTabStore((s) => s.activeTabId)
  const current = useTabStore((s) => {
    const tab = s.activeTabId ? s.tabs[s.activeTabId] : undefined
    return tab ? currentLayoutPattern(tab.layout) : null
  })
  const [pending, setPending] = useState<Pending | null>(null)
  // The guard window of a re-planned dialog, by its seq: the timer belongs to that dialog and is dropped with it.
  const [unlockedSeq, setUnlockedSeq] = useState<number | null>(null)
  const lockedSeq = pending?.replanned ? pending.seq : null
  useEffect(() => {
    if (lockedSeq === null) return
    const timer = setTimeout(() => setUnlockedSeq(lockedSeq), REPLAN_CONFIRM_LOCK_MS)
    return () => clearTimeout(timer)
  }, [lockedSeq])
  const confirmLocked = lockedSeq !== null && unlockedSeq !== lockedSeq

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
    if (!pending || confirmLocked) return
    // Read live, not from the render: the active tab can change in the same click, before this component re-renders.
    if (useTabStore.getState().activeTabId !== pending.tabId) {
      setPending(null)
      return
    }
    // Plan again from the live stores. When the tab's panes changed, or the agent set did (detection, exit and
    // transfer touch only the agent store) so that rule D.1a now asks something else, what the user agreed to is no
    // longer what would happen: show the fresh plan instead (case 1 applies at once; a gone tab or a pattern it already
    // has closes the dialog). A picker whose candidates are unchanged keeps the user's ticks. The fresh dialog opens
    // with its Confirm locked for a moment, so the second click of a double-click does not land on it.
    const planned = planNow(pending.tabId, pending.pattern)
    if (!planned || !sameLeaves(planned.leaves, pending.leaves) || !sameQuestion(planned.plan, pending)) {
      const next = startLayoutChange(pending.tabId, pending.pattern, planned)
      setPending(next && { ...next, replanned: true })
      return
    }
    setPending(null)
    useTabStore.getState().applyLayout(pending.tabId, pending.pattern, keepIds)
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
          confirmDisabled={confirmLocked}
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
          confirmLocked={confirmLocked}
          onCancel={() => setPending(null)}
          onConfirm={finish}
        />
      )}
    </>
  )
}
