import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, cleanup } from '@testing-library/react'
import { TabContent } from './TabContent'
import type { Tab } from '../types/tab'

// Mock heavy components
vi.mock('./TerminalView', () => ({
  default: ({ wsUrl, visible }: { wsUrl: string; visible: boolean }) => (
    <div data-testid="terminal-view" data-visible={visible}>Terminal: {wsUrl}</div>
  ),
}))
vi.mock('./ConversationView', () => ({
  default: ({ sessionName }: { sessionName: string }) => (
    <div data-testid="conversation-view">Stream: {sessionName}</div>
  ),
}))

beforeEach(() => cleanup())

const termTab: Tab = { id: 't1', type: 'terminal', label: 'dev', icon: 'Terminal', hostId: 'mlab', sessionName: 'dev' }
const streamTab: Tab = { id: 't2', type: 'stream', label: 'claude', icon: 'ChatCircleDots', hostId: 'mlab', sessionName: 'claude' }
const editorTab: Tab = { id: 't3', type: 'editor', label: 'file.ts', icon: 'File', hostId: 'mlab', filePath: '/file.ts' }

describe('TabContent', () => {
  it('renders TerminalView for terminal tab', () => {
    render(<TabContent allTabs={[termTab]} activeTabId="t1" wsBase="ws://test"  />)
    expect(screen.getByTestId('terminal-view')).toBeTruthy()
  })

  it('renders ConversationView for stream tab', () => {
    render(<TabContent allTabs={[streamTab]} activeTabId="t2" wsBase="ws://test"  />)
    expect(screen.getByTestId('conversation-view')).toBeTruthy()
  })

  it('renders placeholder for editor tab', () => {
    render(<TabContent allTabs={[editorTab]} activeTabId="t3" wsBase="ws://test"  />)
    expect(screen.getByText(/file\.ts/)).toBeTruthy()
  })

  it('renders empty state when no tabs', () => {
    render(<TabContent allTabs={[]} activeTabId={null} wsBase="ws://test"  />)
    expect(screen.getByText(/選擇或建立/)).toBeTruthy()
  })

  it('keeps all terminal tabs mounted but hides inactive ones', () => {
    render(<TabContent allTabs={[termTab, streamTab]} activeTabId="t2" wsBase="ws://test"  />)
    // Terminal is mounted but not visible
    const termView = screen.getByTestId('terminal-view')
    expect(termView).toBeTruthy()
    expect(termView.dataset.visible).toBe('false')
    // Stream is visible
    expect(screen.getByTestId('conversation-view')).toBeTruthy()
  })
})
