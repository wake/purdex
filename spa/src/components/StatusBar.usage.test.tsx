import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, cleanup, act, waitFor } from '@testing-library/react'
import { StatusBar } from './StatusBar'
import { useSessionStore } from '../stores/useSessionStore'
import { useAgentStore } from '../stores/useAgentStore'
import { useUploadStore } from '../stores/useUploadStore'
import { useUISettingsStore } from '../stores/useUISettingsStore'
import { compositeKey } from '../lib/composite-key'
import { HOST_ID, GEN, setupStores, makeTab } from './StatusBar.test-helpers'

vi.mock('../lib/copy-text', () => ({ copyText: vi.fn(async () => {}) }))

beforeEach(() => {
  cleanup()
  setupStores()
})

describe('StatusBar agent label badge', () => {
  beforeEach(() => {
    setupStores()
    useUploadStore.setState({ sessions: {} })
  })

  it('renders agent label as badge with model name', () => {
    const ck = compositeKey(HOST_ID, 'dev001')
    useAgentStore.setState({
      lastEvents: { [ck]: { raw_event_name: 'PdxSessionStart', status: 'idle', agent_type: 'cc', broadcast_ts: Date.now(), model: 'Claude Opus 4' } },
      statuses: { [ck]: 'idle' },
      unread: {},
      subagents: {},
      models: { [ck]: 'Claude Opus 4' },
    })
    const tab = makeTab('t1', { kind: 'tmux-session', hostId: HOST_ID, sessionCode: 'dev001', mode: 'terminal', cachedName: '', tmuxInstance: '' })
    render(<StatusBar activeTab={tab} />)
    const badge = screen.getByTestId('agent-label')
    expect(badge.textContent).toBe('Claude Opus 4')
    expect(badge.className).toContain('border')
  })

  it('shows the pane session\'s context and limits from its statusLine snapshot, and nothing without one', () => {
    const tab = makeTab('t1', { kind: 'tmux-session', hostId: HOST_ID, sessionCode: 'dev001', mode: 'terminal', cachedName: '', tmuxInstance: '' })
    render(<StatusBar activeTab={tab} />)
    expect(screen.queryByTestId('status-seg-usage-context')).toBeNull()
    act(() => {
      useAgentStore.getState().setCcStatus(HOST_ID, 'dev001', {
        context_window: { used_percentage: 36 },
        rate_limits: { five_hour: { used_percentage: 25, resets_at: Date.now() / 1000 + 3600 }, seven_day: { used_percentage: 73, resets_at: Date.now() / 1000 + 86400 } },
      })
    })
    // Context ring is USED but its number is what is LEFT; the 5-hour / weekly limits show what is LEFT; no text label.
    expect(screen.getByTestId('status-seg-usage-context').textContent).toBe('64%')
    expect(screen.getByTestId('status-seg-usage-five-hour').textContent).toBe('75%')
    expect(screen.getByTestId('status-seg-usage-seven-day').textContent).toBe('27%')
  })

  it('puts the usage in the controls, after the pane title and before the mode buttons, in ctx, 5h, 7d order', () => {
    const ck = compositeKey(HOST_ID, 'dev001')
    useSessionStore.setState({
      sessions: { [HOST_ID]: [{ code: 'dev001', name: 'dev-server', cwd: '/tmp', mode: 'terminal', tmux_instance: '', pane_title: 'plan review' }] },
      activeHostId: HOST_ID,
      activeCode: null,
    })
    useAgentStore.setState({ agentTypes: { [ck]: 'cc' } })
    useUISettingsStore.setState({ showAgentTitleInStatusBar: true })
    useAgentStore.getState().setCcStatus(HOST_ID, 'dev001', {
      context_window: { used_percentage: 36 },
      rate_limits: { five_hour: { used_percentage: 25 }, seven_day: { used_percentage: 73 } },
    })
    const tab = makeTab('t1', { kind: 'tmux-session', hostId: HOST_ID, sessionCode: 'dev001', mode: 'terminal', cachedName: '', tmuxInstance: '' })
    render(<StatusBar activeTab={tab} />)
    const controls = screen.getByTestId('status-controls')
    const ids = ['agent-pane-title', 'status-seg-usage-context', 'status-seg-usage-five-hour', 'status-seg-usage-seven-day', 'status-mode-buttons']
    const els = ids.map((id) => screen.getByTestId(id))
    els.forEach((el, i) => {
      expect(controls.contains(el), ids[i]).toBe(true)
      if (i > 0) expect(els[i - 1].compareDocumentPosition(el) & Node.DOCUMENT_POSITION_FOLLOWING, ids[i]).toBeTruthy()
    })
    expect(screen.getByTestId('status-segments').querySelector('[data-testid^="status-seg-usage"]')).toBeNull()
  })

  it('without a pane title the usage still sits before the mode buttons', () => {
    useAgentStore.getState().setCcStatus(HOST_ID, 'dev001', { rate_limits: { five_hour: { used_percentage: 25 } } })
    const tab = makeTab('t1', { kind: 'tmux-session', hostId: HOST_ID, sessionCode: 'dev001', mode: 'terminal', cachedName: '', tmuxInstance: '' })
    render(<StatusBar activeTab={tab} />)
    const five = screen.getByTestId('status-seg-usage-five-hour')
    expect(screen.queryByTestId('agent-pane-title')).toBeNull()
    expect(five.compareDocumentPosition(screen.getByTestId('status-mode-buttons')) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy()
  })

  it('reactively shows badge when models updates after mount', async () => {
    const ck = compositeKey(HOST_ID, 'dev001')
    useAgentStore.setState({ lastEvents: {}, statuses: {}, unread: {}, subagents: {}, models: {} })
    const tab = makeTab('t1', { kind: 'tmux-session', hostId: HOST_ID, sessionCode: 'dev001', mode: 'terminal', cachedName: '', tmuxInstance: '' })
    render(<StatusBar activeTab={tab} />)
    expect(screen.queryByTestId('agent-label')).toBeNull()
    act(() => {
      useAgentStore.setState({ models: { [ck]: 'Claude Sonnet 4' } })
    })
    await waitFor(() => {
      const badge = screen.getByTestId('agent-label')
      expect(badge.textContent).toBe('Claude Sonnet 4')
    })
  })

  it('does not render badge when no model in models map', () => {
    const ck = compositeKey(HOST_ID, 'dev001')
    useAgentStore.setState({
      lastEvents: { [ck]: { raw_event_name: 'PdxUserPromptSubmit', status: 'running', agent_type: 'cc', broadcast_ts: Date.now() } },
      statuses: { [ck]: 'running' },
      unread: {},
      subagents: {},
      models: {},
    })
    const tab = makeTab('t1', { kind: 'tmux-session', hostId: HOST_ID, sessionCode: 'dev001', mode: 'terminal', cachedName: '', tmuxInstance: '' })
    render(<StatusBar activeTab={tab} />)
    expect(screen.queryByTestId('agent-label')).toBeNull()
  })
})

describe('StatusBar agent pane title', () => {
  beforeEach(() => {
    setupStores()
    useUploadStore.setState({ sessions: {} })
  })

  it('shows pane_title left of the mode buttons when showAgentTitleInStatusBar=true', () => {
    const ck = compositeKey(HOST_ID, 'dev001')
    useSessionStore.setState({
      sessions: {
        [HOST_ID]: [
          { code: 'dev001', name: 'dev-server', cwd: '/tmp', mode: 'terminal', tmux_instance: GEN, pane_title: 'plan review' },
        ],
      },
      activeHostId: HOST_ID,
      activeCode: null,
    })
    useAgentStore.setState({ agentTypes: { [ck]: 'cc' }, oscTitles: { [ck]: 'osc fallback' } })
    useUISettingsStore.setState({ showAgentTitleInStatusBar: true })

    const tab = makeTab('t1', { kind: 'tmux-session', hostId: HOST_ID, sessionCode: 'dev001', mode: 'terminal', cachedName: '', tmuxInstance: '' })
    render(<StatusBar activeTab={tab} />)

    const title = screen.getByTestId('agent-pane-title')
    expect(title.textContent).toBe('plan review')
    expect(title.compareDocumentPosition(screen.getByTestId('status-mode-buttons')) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy()
  })

  it('hides title when showAgentTitleInStatusBar=false', () => {
    const ck = compositeKey(HOST_ID, 'dev001')
    useSessionStore.setState({
      sessions: {
        [HOST_ID]: [
          { code: 'dev001', name: 'dev-server', cwd: '/tmp', mode: 'terminal', tmux_instance: GEN, pane_title: 'plan review' },
        ],
      },
      activeHostId: HOST_ID,
      activeCode: null,
    })
    useAgentStore.setState({ agentTypes: { [ck]: 'cc' } })
    useUISettingsStore.setState({ showAgentTitleInStatusBar: false })

    const tab = makeTab('t1', { kind: 'tmux-session', hostId: HOST_ID, sessionCode: 'dev001', mode: 'terminal', cachedName: '', tmuxInstance: '' })
    render(<StatusBar activeTab={tab} />)

    expect(screen.queryByTestId('agent-pane-title')).toBeNull()
  })
})
