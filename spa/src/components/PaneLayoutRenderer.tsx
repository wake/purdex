import { useRef, useState } from 'react'
import type { ComponentType } from 'react'
import { resolvePaneRenderer } from '../lib/module-registry'
import { getLayoutKey, collectLeaves, swapPaneContent, countLeaves, findPane } from '../lib/pane-tree'
import { PaneSplitter } from './PaneSplitter'
import { PaneHeader } from './PaneHeader'
import { PaneContextMenu, type PaneMenuAction } from './PaneContextMenu'
import { QuickCommandMenu } from './QuickCommandMenu'
import { executeCommand } from '../lib/execute-command'
import { useTabStore } from '../stores/useTabStore'
import { useWorkspaceStore } from '../features/workspace/store'
import {
  useModuleEnabledStore,
  isModuleEnabledIn,
} from '../stores/useModuleEnabledStore'
import { DisabledModulePlaceholder } from './modules/DisabledModulePlaceholder'
import type { PaneLayout, Pane } from '../types/tab'

interface Props {
  layout: PaneLayout
  tabId: string
  isActive: boolean
  showHeader?: boolean
}

export function PaneLayoutRenderer({ layout, tabId, isActive, showHeader = false }: Props) {
  const containerRef = useRef<HTMLDivElement>(null)
  // Snapshot the module-enabled map at component creation. The reload-required
  // contract — DisabledModulePlaceholder hint, file-opener registry only
  // reconciling at bootstrap, NewTabPage memoising once — must extend to the
  // pane renderer too: any later parent re-render (active-tab change,
  // showHeader flip, layout swap) must NOT cause the leaf to read a fresher
  // enable state than the rest of the system. Round 4 codex review caught
  // that an unconditional getState() at render time leaked the live state
  // through that path; pinning the map in useState fixes it. Going
  // fully-immediate is tracked in issue #678.
  const [pinnedEnabled] = useState(() => useModuleEnabledStore.getState().enabled)
  const isEnabledSnapshot = (moduleId: string) => isModuleEnabledIn(pinnedEnabled, moduleId)

  // Right-click pane menu. Each leaf renders its own PaneLayoutRenderer
  // instance, so this per-instance state is scoped to a single pane.
  const [menu, setMenu] = useState<{ x: number; y: number } | null>(null)

  if (layout.type === 'leaf') {
    const resolution = resolvePaneRenderer(
      layout.pane.content.kind,
      isEnabledSnapshot,
    )
    if (resolution.kind === 'unknown') {
      return (
        <div className="flex-1 flex items-center justify-center text-text-muted">
          No renderer for &quot;{resolution.paneKind}&quot;
        </div>
      )
    }
    let Component: ComponentType<{ pane: Pane; isActive: boolean }>
    if (resolution.kind === 'render') {
      Component = resolution.component
    } else {
      // resolution.kind === 'disabled' — render the module-supplied custom
      // component or fall back to the generic placeholder, ignoring pane /
      // isActive (the disabled state has nothing meaningful to do with them).
      const Custom = resolution.customComponent ?? DisabledModulePlaceholder
      const moduleId = resolution.moduleId
      const paneKind = resolution.paneKind
      Component = () => <Custom moduleId={moduleId} paneKind={paneKind} />
    }
    const leafContent = layout.pane.content

    // Right-click interception: editor(Monaco) panes are never intercepted so
    // their native menu survives; Shift+right-click is a universal escape hatch
    // that lets the native menu through (xterm/browser). Everything else opens
    // the PaneContextMenu. stopPropagation prevents ancestor splits from also
    // handling the event.
    const handleContextMenu = (e: React.MouseEvent) => {
      if (leafContent.kind === 'editor' || e.shiftKey) return
      e.preventDefault()
      e.stopPropagation()
      setMenu({ x: e.clientX, y: e.clientY })
    }

    const paneMenu = menu ? (
      <PaneContextMenu
        position={menu}
        canDetach={(() => {
          const tab = useTabStore.getState().tabs[tabId]
          return tab ? countLeaves(tab.layout) > 1 : false
        })()}
        onClose={() => setMenu(null)}
        onAction={(action: PaneMenuAction) => {
          const paneId = layout.pane.id
          // Action-time guard against a stale menu. The menu captured this
          // pane's id at open time, but by the time it is clicked another path
          // may have mutated the tree. Re-read the LIVE layout and bail if:
          //  - the tab is gone,
          //  - this pane no longer exists in the tree, or
          //  - the tab collapsed to a single leaf and the action is close/detach
          //    (closePane on a lone leaf escalates to closeTab → loses the whole
          //    tab; single-leaf detach returns null). Split stays valid on any
          //    live pane.
          const tab = useTabStore.getState().tabs[tabId]
          if (!tab) { setMenu(null); return }
          if (!findPane(tab.layout, paneId)) { setMenu(null); return }
          if ((action === 'close' || action === 'detach') && countLeaves(tab.layout) <= 1) {
            setMenu(null)
            return
          }
          if (action === 'split-h') {
            useTabStore.getState().splitPaneBlank(tabId, paneId, 'h')
          } else if (action === 'split-v') {
            useTabStore.getState().splitPaneBlank(tabId, paneId, 'v')
          } else if (action === 'close') {
            useTabStore.getState().closePane(tabId, paneId)
          } else if (action === 'detach') {
            const newTabId = useTabStore.getState().detachPane(tabId, paneId, tabId)
            if (newTabId) {
              const ws = useWorkspaceStore.getState().findWorkspaceByTab(tabId)
              if (ws) useWorkspaceStore.getState().insertTab(newTabId, ws.id, tabId)
              useTabStore.getState().setActiveTab(newTabId)
            }
          }
        }}
      />
    ) : null

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
        <div className="flex-1 flex flex-col overflow-hidden" onContextMenu={handleContextMenu}>
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
          {paneMenu}
        </div>
      )
    }
    // No-header case = a top-level single leaf. Its parent (TabContent's
    // `.absolute inset-0`) is a BLOCK box, and the bare `<Component>` previously
    // filled it via the component's own h-full/w-full (or block full-width).
    // Keep this wrapper a block (NOT flex): a flex row would size children that
    // only set `h-full`/`flex h-full` (BrowserPane, HostPage, SettingsPage) to
    // content width and shrink them left (R1 P1 regression). `h-full w-full`
    // preserves the exact prior block context; the wrapper exists only to carry
    // onContextMenu.
    return (
      <div className="h-full w-full" onContextMenu={handleContextMenu}>
        <Component pane={layout.pane} isActive={isActive} />
        {paneMenu}
      </div>
    )
  }

  if (layout.children.length === 0) {
    return (
      <div className="flex-1 flex items-center justify-center text-text-muted">
        Empty split layout
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
    // Height MUST come from h-full, not flex-1. A top-level split renders
    // directly under TabContent's `.absolute inset-0` wrapper, which is a BLOCK
    // box — `flex-1` (a flex-item property) is inert there, so the container
    // collapses to CONTENT height and every pane below it becomes unbounded:
    // an overflow-y-auto region inside a pane then grows to full content height
    // and can never scroll (the tail is clipped by an ancestor overflow-hidden).
    // `h-full w-full` resolves against the absolute wrapper's definite height,
    // cascading a bounded height down through nested splits (whose parent is the
    // flex child wrapper below, where 100% also resolves). w-full keeps a
    // horizontal split full-width in that same block context.
    <div ref={containerRef} className={`h-full w-full flex ${layout.direction === 'h' ? 'flex-row' : 'flex-col'} overflow-hidden`}>
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
