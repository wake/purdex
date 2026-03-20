import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, cleanup } from '@testing-library/react'
import { TabContent } from './TabContent'
import { registerTabRenderer, clearRegistry } from '../lib/tab-registry'
import type { Tab } from '../types/tab'

const MockSessionRenderer = ({ tab }: { tab: Tab }) => (
  <div data-testid="session-renderer">Session: {tab.data.sessionName as string}</div>
)
const MockEditorRenderer = ({ tab }: { tab: Tab }) => (
  <div data-testid="editor-renderer">Editor: {tab.data.filePath as string}</div>
)

beforeEach(() => {
  cleanup()
  clearRegistry()
  registerTabRenderer('session', {
    component: MockSessionRenderer as any,
    viewModes: ['terminal', 'stream'],
    defaultViewMode: 'terminal',
    icon: (tab) => tab.viewMode === 'stream' ? 'ChatCircleDots' : 'Terminal',
  })
  registerTabRenderer('editor', {
    component: MockEditorRenderer as any,
    icon: () => 'File',
  })
})

const sessionTab: Tab = {
  id: 't1', type: 'session', label: 'dev', icon: 'Terminal', hostId: 'mlab',
  viewMode: 'terminal', data: { sessionName: 'dev' },
}
const editorTab: Tab = {
  id: 't3', type: 'editor', label: 'file.ts', icon: 'File', hostId: 'mlab',
  data: { filePath: '/file.ts', isDirty: false },
}

describe('TabContent', () => {
  it('renders registered session renderer', () => {
    render(<TabContent activeTab={sessionTab} wsBase="ws://test" daemonBase="http://test" />)
    expect(screen.getByTestId('session-renderer')).toBeTruthy()
  })

  it('renders registered editor renderer', () => {
    render(<TabContent activeTab={editorTab} wsBase="ws://test" daemonBase="http://test" />)
    expect(screen.getByTestId('editor-renderer')).toBeTruthy()
  })

  it('renders fallback for unknown tab type', () => {
    const unknownTab: Tab = { id: 'u1', type: 'monitoring', label: 'mon', icon: 'X', hostId: 'mlab', data: {} }
    render(<TabContent activeTab={unknownTab} wsBase="ws://test" daemonBase="http://test" />)
    expect(screen.getByText(/monitoring/)).toBeTruthy()
  })

  it('renders empty state when no active tab', () => {
    render(<TabContent activeTab={null} wsBase="ws://test" daemonBase="http://test" />)
    expect(screen.getByText(/選擇或建立/)).toBeTruthy()
  })
})
