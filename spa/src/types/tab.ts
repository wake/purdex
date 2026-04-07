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

// === Workspace ===
export type IconWeight = 'bold' | 'duotone' | 'fill'

export interface Workspace {
  id: string
  name: string
  color: string
  icon?: string
  iconWeight?: IconWeight
  tabs: string[]
  activeTabId: string | null
}

// === Factories ===
const WORKSPACE_COLORS = ['#e75a5a', '#eb8b47', '#e4b444', '#8ccb4d', '#4dcb8c', '#3bcec2', '#49b9df', '#5e8eed', '#7c67e4', '#b164d8', '#e25a9e']

export function createTab(content: PaneContent, opts?: { pinned?: boolean }): Tab {
  return {
    id: generateId(),
    pinned: opts?.pinned ?? false,
    locked: false,
    createdAt: Date.now(),
    layout: { type: 'leaf', pane: { id: generateId(), content } },
  }
}

export function createWorkspace(name: string, color?: string, icon?: string): Workspace {
  return {
    id: generateId(),
    name,
    color: color ?? WORKSPACE_COLORS[Math.floor(Math.random() * WORKSPACE_COLORS.length)],
    icon,
    tabs: [],
    activeTabId: null,
  }
}

export function isStandaloneTab(tabId: string, workspaces: Workspace[]): boolean {
  return !workspaces.some((ws) => ws.tabs.includes(tabId))
}
