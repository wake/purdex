import { useRef } from 'react'
import { getPaneRenderer } from '../lib/module-registry'
import { getLayoutKey, collectLeaves, swapPaneContent } from '../lib/pane-tree'
import { PaneSplitter } from './PaneSplitter'
import { PaneHeader } from './PaneHeader'
import { QuickCommandMenu } from './QuickCommandMenu'
import { executeCommand } from '../lib/execute-command'
import { useTabStore } from '../stores/useTabStore'
import { useWorkspaceStore } from '../features/workspace/store'
import type { PaneLayout, SplitLayout } from '../types/tab'

function isSplit(layout: PaneLayout): layout is SplitLayout {
  return layout.type === 'split'
}

export function isGrid4(layout: PaneLayout): boolean {
  if (!isSplit(layout) || layout.direction !== 'v' || layout.children.length !== 2) return false
  return layout.children.every(
    (c) => c.type === 'split' && c.direction === 'h' && c.children.length === 2,
  )
}

interface Props {
  layout: PaneLayout
  tabId: string
  isActive: boolean
  showHeader?: boolean
}

export function PaneLayoutRenderer({ layout, tabId, isActive, showHeader = false }: Props) {
  const containerRef = useRef<HTMLDivElement>(null)

  if (layout.type === 'leaf') {
    const config = getPaneRenderer(layout.pane.content.kind)
    if (!config) {
      return (
        <div className="flex-1 flex items-center justify-center text-text-muted">
          No renderer for &quot;{layout.pane.content.kind}&quot;
        </div>
      )
    }
    const Component = config.component
    if (showHeader) {
      const allLeaves = (() => {
        const tab = useTabStore.getState().tabs[tabId]
        return tab ? collectLeaves(tab.layout) : []
      })()
      const swapTargets = allLeaves
        .filter((p) => p.id !== layout.pane.id)
        .map((p) => ({ id: p.id, label: p.content.kind }))

      const content = layout.pane.content

      return (
        <div className="flex-1 flex flex-col overflow-hidden">
          <PaneHeader
            title={content.kind}
            onClose={() => useTabStore.getState().closePane(tabId, layout.pane.id)}
            onDetach={() => {
              const newTabId = useTabStore.getState().detachPane(tabId, layout.pane.id, tabId)
              if (newTabId) {
                const ws = useWorkspaceStore.getState().findWorkspaceByTab(tabId)
                if (ws) useWorkspaceStore.getState().insertTab(newTabId, ws.id, tabId)
                useTabStore.getState().setActiveTab(newTabId)
              }
            }}
            onSwap={(targetPaneId) => {
              const tab = useTabStore.getState().tabs[tabId]
              if (!tab) return
              const newLayout = swapPaneContent(tab.layout, layout.pane.id, targetPaneId)
              useTabStore.getState().setTabLayout(tabId, newLayout)
            }}
            swapTargets={swapTargets}
            extraActions={content.kind === 'tmux-session' ? (
              <QuickCommandMenu
                hostId={content.hostId}
                workspaceId={useWorkspaceStore.getState().activeWorkspaceId}
                onExecute={async (cmd) => {
                  try { await executeCommand(content.hostId, content.sessionCode, cmd.command) } catch { /* ignore */ }
                }}
              />
            ) : undefined}
          />
          <Component pane={layout.pane} isActive={isActive} />
        </div>
      )
    }
    return <Component pane={layout.pane} isActive={isActive} />
  }

  if (layout.children.length === 0) {
    return (
      <div className="flex-1 flex items-center justify-center text-text-muted">
        Empty split layout
      </div>
    )
  }

  if (isSplit(layout) && isGrid4(layout)) {
    const topSplit = layout.children[0] as SplitLayout
    const bottomSplit = layout.children[1] as SplitLayout

    const handleHorizontalResize = (index: number, deltaPx: number) => {
      const container = containerRef.current
      if (!container) return
      const containerWidth = container.offsetWidth
      if (containerWidth === 0) return
      const percentDelta = (deltaPx / containerWidth) * 100
      // Read fresh layout from store to avoid stale closure
      const currentTab = useTabStore.getState().tabs[tabId]
      if (!currentTab || !isSplit(currentTab.layout) || !isGrid4(currentTab.layout)) return
      const freshTop = currentTab.layout.children[0] as SplitLayout
      const freshBottom = currentTab.layout.children[1] as SplitLayout
      for (const split of [freshTop, freshBottom]) {
        const totalPercent = split.sizes[index] + split.sizes[index + 1]
        const newLeft = Math.max(10, Math.min(totalPercent - 10, split.sizes[index] + percentDelta))
        const newRight = totalPercent - newLeft
        useTabStore.getState().resizePanes(tabId, split.id, [newLeft, newRight])
      }
    }

    const handleVerticalResize = (index: number, deltaPx: number) => {
      const container = containerRef.current
      if (!container) return
      const containerHeight = container.offsetHeight
      if (containerHeight === 0) return
      const percentDelta = (deltaPx / containerHeight) * 100
      // Read fresh layout from store to avoid stale closure
      const currentTab = useTabStore.getState().tabs[tabId]
      if (!currentTab || !isSplit(currentTab.layout) || !isGrid4(currentTab.layout)) return
      const totalPercent = currentTab.layout.sizes[index] + currentTab.layout.sizes[index + 1]
      const newTop = Math.max(10, Math.min(totalPercent - 10, currentTab.layout.sizes[index] + percentDelta))
      const newBottom = totalPercent - newTop
      useTabStore.getState().resizePanes(tabId, currentTab.layout.id, [newTop, newBottom])
    }

    return (
      <div ref={containerRef} className="flex-1 flex flex-col overflow-hidden">
        {/* Top row */}
        <div style={{ flex: `${layout.sizes[0]} 0 0%` }} className="min-h-0 flex flex-row overflow-hidden">
          {topSplit.children.map((child, i) => (
            <div key={getLayoutKey(child)} className="contents">
              {i > 0 && <PaneSplitter direction="h" onResize={(d) => handleHorizontalResize(i - 1, d)} />}
              <div style={{ flex: `${topSplit.sizes[i]} 0 0%` }} className="min-w-0 min-h-0 flex overflow-hidden">
                <PaneLayoutRenderer layout={child} tabId={tabId} isActive={isActive} showHeader={true} />
              </div>
            </div>
          ))}
        </div>
        {/* Vertical splitter */}
        <PaneSplitter direction="v" onResize={(d) => handleVerticalResize(0, d)} />
        {/* Bottom row */}
        <div style={{ flex: `${layout.sizes[1]} 0 0%` }} className="min-h-0 flex flex-row overflow-hidden">
          {bottomSplit.children.map((child, i) => (
            <div key={getLayoutKey(child)} className="contents">
              {i > 0 && <PaneSplitter direction="h" onResize={(d) => handleHorizontalResize(i - 1, d)} />}
              <div style={{ flex: `${bottomSplit.sizes[i]} 0 0%` }} className="min-w-0 min-h-0 flex overflow-hidden">
                <PaneLayoutRenderer layout={child} tabId={tabId} isActive={isActive} showHeader={true} />
              </div>
            </div>
          ))}
        </div>
      </div>
    )
  }

  const handleResize = (index: number, deltaPx: number) => {
    const container = containerRef.current
    if (!container) return
    const containerSize = layout.direction === 'h' ? container.offsetWidth : container.offsetHeight
    if (containerSize === 0) return
    const percentDelta = (deltaPx / containerSize) * 100
    const totalPercent = layout.sizes[index] + layout.sizes[index + 1]
    const newLeft = Math.max(10, Math.min(totalPercent - 10, layout.sizes[index] + percentDelta))
    const newRight = totalPercent - newLeft
    const newSizes = [...layout.sizes]
    newSizes[index] = newLeft
    newSizes[index + 1] = newRight
    useTabStore.getState().resizePanes(tabId, layout.id, newSizes)
  }

  return (
    <div ref={containerRef} className={`flex-1 flex ${layout.direction === 'h' ? 'flex-row' : 'flex-col'} overflow-hidden`}>
      {layout.children.map((child, i) => (
        <div key={getLayoutKey(child)} className="contents">
          {i > 0 && (
            <PaneSplitter
              direction={layout.direction}
              onResize={(delta) => handleResize(i - 1, delta)}
            />
          )}
          <div style={{ flex: `${layout.sizes[i]} 0 0%` }} className="min-w-0 min-h-0 flex overflow-hidden">
            <PaneLayoutRenderer layout={child} tabId={tabId} isActive={isActive} showHeader={true} />
          </div>
        </div>
      ))}
    </div>
  )
}
