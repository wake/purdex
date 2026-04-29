import { useCallback, useEffect, useRef, useState } from 'react'
import { CaretRight, CaretDown, Plus } from '@phosphor-icons/react'
import { useDroppable } from '@dnd-kit/core'
import { useSortable } from '@dnd-kit/sortable'
import type { Workspace, Tab } from '../../../types/tab'
import { useLayoutStore } from '../../../stores/useLayoutStore'
import { useI18nStore } from '../../../stores/useI18nStore'
import { useTabStore } from '../../../stores/useTabStore'
import { inferWorkspaceHostId } from '../../../lib/infer-workspace-host-id'
import { WorkspaceIcon } from './WorkspaceIcon'
import { InlineTabList } from './InlineTabList'
import { WorkspaceQuickActionsPopover } from './WorkspaceQuickActionsPopover'

interface Props {
  workspace: Workspace
  isActive: boolean
  tabsById: Record<string, Tab>
  activeTabId: string | null
  onSelectWorkspace: (wsId: string) => void
  onContextMenuWorkspace?: (e: React.MouseEvent, wsId: string) => void
  onSelectTab: (tabId: string) => void
  onCloseTab: (tabId: string) => void
  onMiddleClickTab: (tabId: string) => void
  onContextMenuTab: (e: React.MouseEvent, tabId: string) => void
  onRenameTab?: (tabId: string) => void
  onAddTabToWorkspace: (wsId: string) => void
}

export function WorkspaceRow(props: Props) {
  const {
    workspace,
    isActive,
    tabsById,
    activeTabId,
    onSelectWorkspace,
    onContextMenuWorkspace,
    onSelectTab,
    onCloseTab,
    onMiddleClickTab,
    onContextMenuTab,
    onRenameTab,
    onAddTabToWorkspace,
  } = props
  const t = useI18nStore((s) => s.t)
  const expanded = useLayoutStore((s) => !!s.workspaceExpanded[workspace.id])
  const toggleExpanded = useLayoutStore((s) => s.toggleWorkspaceExpanded)
  const tabPosition = useLayoutStore((s) => s.tabPosition)
  const showTabs = tabPosition !== 'top'

  // Phase 1b' — Plus hover popover state + touch fallback (codex round-1 C17).
  // hostId resolves via inferWorkspaceHostId (spec v4 §3.2.1 majority vote, no
  // activeHostId fallback). Subscribing to tabsMap so hostId reacts to layout
  // changes (e.g. user opens / closes a tmux pane).
  const [popoverOpen, setPopoverOpen] = useState(false)
  const hubRef = useRef<HTMLDivElement>(null)
  const tabsMap = useTabStore((s) => s.tabs)
  const hostId = inferWorkspaceHostId(workspace, tabsMap)

  const longPressTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null)
  const longPressFiredRef = useRef(false)
  // codex round-1 P2 (F3 — picker hover-dismissal). When the popover's internal
  // HostPickerPopover is up we must NOT collapse the hub on mouseleave /
  // pointerdown — otherwise moving the pointer toward a host option unmounts
  // the popover (and the picker with it) before the user can click. Tracked
  // via refs because mouseleave/pointerdown handlers fire from event listeners
  // whose closures snapshot stale state.
  const pickerOpenRef = useRef(false)
  const pointerInHubRef = useRef(false)
  const handlePickerOpenChange = useCallback((open: boolean) => {
    const wasOpen = pickerOpenRef.current
    pickerOpenRef.current = open
    // Only collapse on the open → closed transition. Initial mount notifies
    // false (and so does the unmount cleanup); collapsing on that would shut
    // the popover the moment it opens, especially on touch where mouseEnter
    // never fires and pointerInHubRef stays false.
    if (wasOpen && !open && !pointerInHubRef.current) {
      setPopoverOpen(false)
    }
  }, [])

  const handleTouchStart = () => {
    longPressFiredRef.current = false
    longPressTimerRef.current = setTimeout(() => {
      longPressFiredRef.current = true
      setPopoverOpen(true)
    }, 500)
  }
  const handleTouchEnd = () => {
    if (longPressTimerRef.current) {
      clearTimeout(longPressTimerRef.current)
      longPressTimerRef.current = null
    }
    // long-press fired → user is now interacting with popover; do NOT trigger add-tab onClick.
  }

  // Cleanup any pending timer on unmount so we don't fire setPopoverOpen on a
  // disposed component (silent in React 18 but a real bug if the component
  // re-mounts at the same workspace.id).
  useEffect(() => {
    return () => {
      if (longPressTimerRef.current) {
        clearTimeout(longPressTimerRef.current)
        longPressTimerRef.current = null
      }
    }
  }, [])

  // codex round-2 A2/F2 — clear the long-press click suppressor whenever the
  // popover collapses. Without this the ref stays true after long-press → tap
  // outside → next mouse/keyboard activation of Plus is silently dropped.
  // (We also clear it inline in onClick when we actually consume the
  // compatibility click; this useEffect catches the case where the browser
  // skips the synthetic click entirely.)
  useEffect(() => {
    if (!popoverOpen) {
      longPressFiredRef.current = false
    }
  }, [popoverOpen])

  // Document-level pointerdown closes touch-popover when user taps outside hub.
  // codex round-1 P2 (F3) — but NOT while the host picker is open; the picker
  // is positioned outside the hub DOM and a pointerdown on a host option would
  // otherwise close the popover before the picker's own onSelect handler fires.
  useEffect(() => {
    if (!popoverOpen) return
    const onPointer = (e: PointerEvent) => {
      if (pickerOpenRef.current) return
      if (!hubRef.current?.contains(e.target as Node | null)) {
        setPopoverOpen(false)
      }
    }
    document.addEventListener('pointerdown', onPointer)
    return () => document.removeEventListener('pointerdown', onPointer)
  }, [popoverOpen])

  const { attributes, listeners, setNodeRef, transform, transition, isDragging } = useSortable({
    id: workspace.id,
    data: { type: 'workspace', wsId: workspace.id },
  })

  const { setNodeRef: setHeaderDropRef, isOver: isHeaderOver } = useDroppable({
    id: `ws-header-${workspace.id}`,
    data: { type: 'workspace-header', wsId: workspace.id },
  })

  // Use a Y-only integer translate to avoid sub-pixel transform interpolation
  // blurring the row's text during drag (CSS.Transform.toString emits a 3-axis
  // translate + scaleX/Y which can land on non-integer pixels). Mirrors the
  // narrow-mode InlineTab / SortableWorkspaceButton pattern.
  const style: React.CSSProperties = {
    transform: transform
      ? `translate3d(0, ${Math.round(transform.y)}px, 0)`
      : undefined,
    transition,
    opacity: isDragging ? 0.5 : 1,
  }

  const Chevron = expanded ? CaretDown : CaretRight
  const chevronLabel = expanded
    ? `Collapse ${workspace.name}`
    : `Expand ${workspace.name}`

  return (
    <div ref={setNodeRef} style={style} className="flex flex-col">
      <div
        ref={setHeaderDropRef}
        data-testid={`ws-header-${workspace.id}`}
        {...attributes}
        {...listeners}
        className={`group/ws-header mx-2 flex items-center gap-1 pl-1.5 pr-1.5 rounded-md text-sm transition-colors focus:outline-none focus-visible:outline-none ${
          isActive
            ? 'bg-[#8b5cf6]/25 text-text-primary ring-1 ring-purple-400'
            : 'text-text-secondary hover:bg-surface-hover hover:text-text-primary'
        } ${isHeaderOver ? 'ring-2 ring-purple-400/80 bg-surface-hover' : ''}`}
      >
        <button
          type="button"
          onClick={() => {
            if (isActive && showTabs) {
              toggleExpanded(workspace.id)
            } else {
              onSelectWorkspace(workspace.id)
            }
          }}
          onContextMenu={(e) => {
            e.preventDefault()
            onContextMenuWorkspace?.(e, workspace.id)
          }}
          className="flex-1 flex items-center gap-2 py-1.5 text-left cursor-pointer focus:outline-none"
        >
          <WorkspaceIcon
            icon={workspace.icon}
            name={workspace.name}
            size={16}
            weight={workspace.iconWeight}
          />
          <span className="truncate">{workspace.name}</span>
        </button>
        {showTabs && (
          <div
            ref={hubRef}
            className="relative inline-flex"
            onMouseEnter={() => {
              pointerInHubRef.current = true
              setPopoverOpen(true)
            }}
            onMouseLeave={() => {
              pointerInHubRef.current = false
              // codex round-1 P2 (F3) — keep popover up while picker is open so
              // the user can drift the pointer onto a host option without it
              // disappearing.
              if (pickerOpenRef.current) return
              setPopoverOpen(false)
            }}
            onFocusCapture={() => setPopoverOpen(true)}
            onBlurCapture={(e) => {
              // Only close if focus actually left the hub (chip→chip Tab keeps focus inside).
              if (!hubRef.current?.contains(e.relatedTarget as Node | null)) {
                if (pickerOpenRef.current) return
                setPopoverOpen(false)
              }
            }}
            onTouchStart={handleTouchStart}
            onTouchEnd={handleTouchEnd}
            onTouchCancel={handleTouchEnd}
          >
            <button
              type="button"
              aria-label={t('nav.add_tab_to_workspace', { name: workspace.name })}
              title={t('nav.add_tab_to_workspace', { name: workspace.name })}
              onClick={(e) => {
                // codex round-1 C17 — touch → click compat: if long-press fired, suppress click.
                if (longPressFiredRef.current) {
                  // codex round-2 A2 — one-shot suppressor: clear immediately
                  // so the next mouse/keyboard activation isn't also dropped.
                  longPressFiredRef.current = false
                  e.preventDefault()
                  e.stopPropagation()
                  return
                }
                e.stopPropagation()
                onAddTabToWorkspace(workspace.id)
              }}
              className="p-0.5 rounded hover:bg-surface-secondary hover:text-text-primary cursor-pointer opacity-0 group-hover/ws-header:opacity-100 focus:opacity-100 transition-opacity focus:outline-none"
            >
              <Plus size={12} />
            </button>
            {popoverOpen && (
              <WorkspaceQuickActionsPopover
                workspaceId={workspace.id}
                hostId={hostId}
                onPickerOpenChange={handlePickerOpenChange}
              />
            )}
          </div>
        )}
        {showTabs && (
          <button
            type="button"
            aria-label={chevronLabel}
            aria-expanded={expanded}
            onClick={(e) => {
              e.stopPropagation()
              toggleExpanded(workspace.id)
            }}
            className="p-0.5 rounded hover:bg-surface-secondary hover:text-text-primary cursor-pointer focus:outline-none"
          >
            <Chevron size={12} />
          </button>
        )}
      </div>

      {showTabs && expanded && (
        <InlineTabList
          tabIds={workspace.tabs}
          tabsById={tabsById}
          activeTabId={activeTabId}
          sourceWsId={workspace.id}
          onSelect={onSelectTab}
          onClose={onCloseTab}
          onMiddleClick={onMiddleClickTab}
          onContextMenu={onContextMenuTab}
          onRename={onRenameTab}
        />
      )}
    </div>
  )
}
