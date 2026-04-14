import { generateId } from '../lib/id'
import type { FileSource } from './fs'

// === Tab (tab bar unit) ===
export interface Tab {
  id: string
  pinned: boolean
  locked: boolean
  createdAt: number
  layout: PaneLayout
}

// === Pane Layout (tab-internal split tree) ===
export type SplitLayout = { type: 'split'; id: string; direction: 'h' | 'v'; children: PaneLayout[]; sizes: number[] }
export type PaneLayout =
  | { type: 'leaf'; pane: Pane }
  | SplitLayout

export type LayoutPattern = 'single' | 'split-h' | 'split-v' | 'grid-4'

// === Pane (content slot) ===
export interface Pane {
  id: string
  content: PaneContent
}

// === Pane Content (discriminated union) ===
export type TerminatedReason = 'session-closed' | 'tmux-restarted' | 'host-removed'

export type PaneContent =
  | { kind: 'new-tab' }
  | { kind: 'tmux-session'; hostId: string; sessionCode: string; mode: 'terminal' | 'stream'; cachedName: string; tmuxInstance: string; terminated?: TerminatedReason }
  | { kind: 'dashboard' }
  | { kind: 'hosts' }
  | { kind: 'history' }
  | { kind: 'settings'; scope: 'global' | { workspaceId: string } }
  | { kind: 'browser'; url: string }
  | { kind: 'memory-monitor' }
  | { kind: 'editor'; source: FileSource; filePath: string; diff?: { against: 'saved' | string } }

// === Workspace ===
export type IconWeight = 'bold' | 'regular' | 'thin' | 'light' | 'fill' | 'duotone'

export interface Workspace {
  id: string
  name: string
  icon?: string
  iconWeight?: IconWeight
  tabs: string[]
  activeTabId: string | null
  moduleConfig?: Record<string, Record<string, unknown>>
}

// === Sidebar Region (layout system) ===
export type SidebarRegion =
  | 'primary-sidebar'
  | 'primary-panel'
  | 'secondary-panel'
  | 'secondary-sidebar'

// === Factories ===

export function createTab(content: PaneContent, opts?: { pinned?: boolean }): Tab {
  return {
    id: generateId(),
    pinned: opts?.pinned ?? false,
    locked: false,
    createdAt: Date.now(),
    layout: { type: 'leaf', pane: { id: generateId(), content } },
  }
}

export function createWorkspace(name: string, icon?: string): Workspace {
  return {
    id: generateId(),
    name,
    icon,
    tabs: [],
    activeTabId: null,
    moduleConfig: {},
  }
}

export function isStandaloneTab(tabId: string, workspaces: Workspace[]): boolean {
  return !workspaces.some((ws) => ws.tabs.includes(tabId))
}
