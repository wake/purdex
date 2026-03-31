import { generateId } from '../lib/id'

// === Tab (tab bar unit) ===
export interface Tab {
  id: string
  pinned: boolean
  locked: boolean
  createdAt: number
  layout: PaneLayout
}

// === Pane Layout (tab-internal split tree) ===
export type PaneLayout =
  | { type: 'leaf'; pane: Pane }
  | { type: 'split'; id: string; direction: 'h' | 'v'; children: PaneLayout[]; sizes: number[] }

// === Pane (content slot) ===
export interface Pane {
  id: string
  content: PaneContent
}

// === Pane Content (discriminated union) ===
export type PaneContent =
  | { kind: 'new-tab' }
  | { kind: 'session'; hostId: string; sessionCode: string; mode: 'terminal' | 'stream' }
  | { kind: 'dashboard' }
  | { kind: 'hosts' }
  | { kind: 'history' }
  | { kind: 'settings'; scope: 'global' | { workspaceId: string } }
  | { kind: 'browser'; url: string }
  | { kind: 'memory-monitor' }

// === Workspace ===
export interface Workspace {
  id: string
  name: string
  color: string
  icon?: string
  tabs: string[]
  activeTabId: string | null
}

// === Factories ===
const WORKSPACE_COLORS = ['#7a6aaa', '#6aaa7a', '#aa6a7a', '#6a8aaa', '#aa8a6a', '#8a6aaa']

export function createTab(content: PaneContent, opts?: { pinned?: boolean }): Tab {
  return {
    id: generateId(),
    pinned: opts?.pinned ?? false,
    locked: false,
    createdAt: Date.now(),
    layout: { type: 'leaf', pane: { id: generateId(), content } },
  }
}

export function createWorkspace(name: string, color?: string): Workspace {
  return {
    id: generateId(),
    name,
    color: color ?? WORKSPACE_COLORS[Math.floor(Math.random() * WORKSPACE_COLORS.length)],
    tabs: [],
    activeTabId: null,
  }
}

export function isStandaloneTab(tabId: string, workspaces: Workspace[]): boolean {
  return !workspaces.some((ws) => ws.tabs.includes(tabId))
}
