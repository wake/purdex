import { beforeEach, describe, expect, it, vi } from 'vitest'
import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import { TmuxAgentMonitorSection } from './TmuxAgentMonitorSection'
import { useHostStore } from '../../stores/useHostStore'
import { useI18nStore } from '../../stores/useI18nStore'
import {
  fetchAgentMonitorChain,
  fetchAgentMonitorChains,
  fetchAgentMonitorProjection,
  type AgentMonitorChainSummary,
  type AgentMonitorProjectionSummary,
  type AgentMonitorStepNode,
} from '../../lib/host-api'

vi.mock('../../lib/host-api', () => ({
  fetchAgentMonitorChains: vi.fn(),
  fetchAgentMonitorChain: vi.fn(),
  fetchAgentMonitorProjection: vi.fn(),
}))

function deferred<T>() {
  let resolve!: (value: T) => void
  let reject!: (reason?: unknown) => void
  const promise = new Promise<T>((res, rej) => {
    resolve = res
    reject = rej
  })
  return { promise, resolve, reject }
}

type ChainDetailResponse = {
  chain: AgentMonitorChainSummary
  step_tree: AgentMonitorStepNode[]
}

type ProjectionResponse = {
  projection: AgentMonitorProjectionSummary
}

describe('TmuxAgentMonitorSection', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    useI18nStore.setState({ t: (key: string) => key })
    useHostStore.setState({
      hosts: {
        'host-a': { id: 'host-a', name: 'Host A', ip: '100.64.0.1', port: 7860, order: 0 },
        'host-b': { id: 'host-b', name: 'Host B', ip: '100.64.0.2', port: 7860, order: 1 },
      },
      hostOrder: ['host-a', 'host-b'],
      activeHostId: 'host-b',
    })

    vi.mocked(fetchAgentMonitorChains).mockResolvedValue({
      chains: [
        {
          chain_id: 'chain-1',
          started_at: 1712345678901,
          completed_at: 120,
          terminal_status: 'completed',
          terminal_reason: 'emit_broadcasted',
          tmux_session: 'work',
          pane_id: '%7',
          root_agent_type: 'codex',
          root_event_name: 'UserPromptSubmit',
          root_reason: 'hook_post',
          latest_step_kind: 'emit',
          latest_decision: 'broadcasted',
          latest_step_reason: 'session_code_resolved',
          step_count: 3,
        },
      ],
      next_cursor: '',
    })

    vi.mocked(fetchAgentMonitorChain).mockResolvedValue({
      chain: {
        chain_id: 'chain-1',
        started_at: 1712345678901,
        completed_at: 120,
        terminal_status: 'completed',
        terminal_reason: 'emit_broadcasted',
        tmux_session: 'work',
        pane_id: '%7',
        root_agent_type: 'codex',
        root_event_name: 'UserPromptSubmit',
        root_reason: 'hook_post',
        latest_step_kind: 'emit',
        latest_decision: 'broadcasted',
        latest_step_reason: 'session_code_resolved',
        step_count: 3,
      },
      step_tree: [
        {
          step: {
            step_id: 'step-1',
            chain_id: 'chain-1',
            seq: 1,
            kind: 'trigger',
            tmux_session: 'work',
            pane_id: '%7',
            agent_type: 'codex',
            frame_id: '',
            parent_frame_id: '',
            event_name: 'UserPromptSubmit',
            decision: 'received',
            reason: 'hook_post',
            payload_json: '{"prompt":"hi"}',
            before_json: 'null',
            after_json: '{"accepted":true}',
            created_at: 100,
          },
          children: [
            {
              step: {
                step_id: 'step-2',
                chain_id: 'chain-1',
                parent_step_id: 'step-1',
                seq: 2,
                kind: 'verify',
                tmux_session: 'work',
                pane_id: '%7',
                agent_type: 'codex',
                frame_id: 'frame-verify',
                parent_frame_id: 'frame-root',
                event_name: 'UserPromptSubmit',
                decision: 'accepted',
                reason: 'verify_passed',
                payload_json: '{"tmux_session":"work"}',
                before_json: 'null',
                after_json: '{"accepted":true}',
                created_at: 110,
              },
              children: [],
            },
          ],
        },
      ],
    })

    vi.mocked(fetchAgentMonitorProjection).mockResolvedValue({
      projection: {
        tmux_session: 'work',
        pane_id: '%7',
        primary_frame_id: 'frame-1',
        top_frame_id: 'frame-1',
        top_agent_type: 'codex',
        latest_chain_id: 'chain-1',
      },
    })
  })

  it('renders chain list, step tree, selected step inspector, and projection summary', async () => {
    render(<TmuxAgentMonitorSection />)

    await waitFor(() => expect(fetchAgentMonitorChains).toHaveBeenCalledTimes(1))
    await waitFor(() => expect(fetchAgentMonitorChains).toHaveBeenCalledWith('host-b', expect.any(URLSearchParams)))
    await waitFor(() => expect(fetchAgentMonitorChain).toHaveBeenCalledWith('host-b', 'chain-1'))
    await waitFor(() => expect(fetchAgentMonitorProjection).toHaveBeenCalledTimes(1))
    expect(vi.mocked(fetchAgentMonitorProjection).mock.calls[0][1].toString()).toBe('pane=%257')

    await waitFor(() => expect(screen.getByText('chain-1')).toBeInTheDocument())
    expect(screen.getByText('settings.monitor.chains')).toBeInTheDocument()
    expect(screen.getByText('settings.monitor.step_tree')).toBeInTheDocument()
    expect(screen.getByText('settings.monitor.projection')).toBeInTheDocument()
    expect(screen.getByText('settings.monitor.inspect')).toBeInTheDocument()
    expect(screen.getByText('settings.monitor.selected_step')).toBeInTheDocument()
    expect(screen.getByText('1712345678901')).toBeInTheDocument()
    expect(screen.getByText(/UserPromptSubmit/)).toBeInTheDocument()
    expect(screen.getByText(/^trigger$/)).toBeInTheDocument()
    expect(screen.getByText(/^verify$/)).toBeInTheDocument()
    expect(screen.getAllByText(/^codex$/).length).toBeGreaterThan(0)
    expect(screen.getByText(/frame-root/)).toBeInTheDocument()
    expect(screen.getByText(/settings\.monitor\.projection\.primary:\s*frame-1/)).toBeInTheDocument()
    expect(screen.getByText('{"prompt":"hi"}')).toBeInTheDocument()
  })

  it('updates inspector when selecting another step', async () => {
    render(<TmuxAgentMonitorSection />)

    await waitFor(() => expect(screen.getByText('verify')).toBeInTheDocument())

    fireEvent.click(screen.getByText('verify'))

    expect(screen.getByText('{"tmux_session":"work"}')).toBeInTheDocument()
    expect(screen.getAllByText(/accepted/).length).toBeGreaterThan(0)
  })

  it('clears stale detail state when selecting a new chain fails', async () => {
    vi.mocked(fetchAgentMonitorChains).mockResolvedValue({
      chains: [
        {
          chain_id: 'chain-1',
          started_at: 1712345678901,
          completed_at: 120,
          terminal_status: 'completed',
          terminal_reason: 'emit_broadcasted',
          tmux_session: 'work',
          pane_id: '%7',
          root_agent_type: 'codex',
          root_event_name: 'UserPromptSubmit',
          root_reason: 'hook_post',
          latest_step_kind: 'emit',
          latest_decision: 'broadcasted',
          latest_step_reason: 'session_code_resolved',
          step_count: 3,
        },
        {
          chain_id: 'chain-2',
          started_at: 1712345678999,
          completed_at: 130,
          terminal_status: 'completed',
          terminal_reason: 'emit_broadcasted',
          tmux_session: 'work',
          pane_id: '%8',
          root_agent_type: 'cc',
          root_event_name: 'Stop',
          root_reason: 'hook_post',
          latest_step_kind: 'emit',
          latest_decision: 'broadcasted',
          latest_step_reason: 'session_code_resolved',
          step_count: 1,
        },
      ],
      next_cursor: '',
    })
    vi.mocked(fetchAgentMonitorChain).mockImplementation(async (_hostId, chainId) => {
      if (chainId === 'chain-2') {
        throw new Error('detail failed')
      }
      return {
        chain: {
          chain_id: 'chain-1',
          started_at: 1712345678901,
          completed_at: 120,
          terminal_status: 'completed',
          terminal_reason: 'emit_broadcasted',
          tmux_session: 'work',
          pane_id: '%7',
          root_agent_type: 'codex',
          root_event_name: 'UserPromptSubmit',
          root_reason: 'hook_post',
          latest_step_kind: 'emit',
          latest_decision: 'broadcasted',
          latest_step_reason: 'session_code_resolved',
          step_count: 3,
        },
        step_tree: [
          {
            step: {
              step_id: 'step-1',
              chain_id: 'chain-1',
              seq: 1,
              kind: 'trigger',
              tmux_session: 'work',
              pane_id: '%7',
              agent_type: 'codex',
              frame_id: '',
              parent_frame_id: '',
              event_name: 'UserPromptSubmit',
              decision: 'received',
              reason: 'hook_post',
              payload_json: '{"prompt":"hi"}',
              before_json: 'null',
              after_json: '{"accepted":true}',
              created_at: 100,
            },
            children: [],
          },
        ],
      }
    })
    vi.mocked(fetchAgentMonitorProjection).mockImplementation(async (_hostId, query) => {
      if (query.toString() === 'pane=%258') {
        throw new Error('projection failed')
      }
      return {
        projection: {
          tmux_session: 'work',
          pane_id: '%7',
          primary_frame_id: 'frame-1',
          top_frame_id: 'frame-1',
          top_agent_type: 'codex',
          latest_chain_id: 'chain-1',
        },
      }
    })

    render(<TmuxAgentMonitorSection />)

    await waitFor(() => expect(screen.getByText('{"prompt":"hi"}')).toBeInTheDocument())
    fireEvent.click(screen.getByText('chain-2'))

    await waitFor(() => expect(screen.getByText('detail failed')).toBeInTheDocument())
    expect(screen.queryByText('{"prompt":"hi"}')).not.toBeInTheDocument()
  })

  it('ignores stale responses from an older selection request', async () => {
    const chain1Detail = deferred<ChainDetailResponse>()
    const chain2Detail = deferred<ChainDetailResponse>()
    const chain1Projection = deferred<ProjectionResponse>()
    const chain2Projection = deferred<ProjectionResponse>()

    vi.mocked(fetchAgentMonitorChains).mockResolvedValue({
      chains: [
        {
          chain_id: 'chain-1',
          started_at: 100,
          completed_at: 120,
          terminal_status: 'completed',
          terminal_reason: 'emit_broadcasted',
          tmux_session: 'work',
          pane_id: '%7',
          root_agent_type: 'codex',
          root_event_name: 'UserPromptSubmit',
          root_reason: 'hook_post',
          latest_step_kind: 'emit',
          latest_decision: 'broadcasted',
          latest_step_reason: 'session_code_resolved',
          step_count: 1,
        },
        {
          chain_id: 'chain-2',
          started_at: 101,
          completed_at: 121,
          terminal_status: 'completed',
          terminal_reason: 'emit_broadcasted',
          tmux_session: 'work',
          pane_id: '%8',
          root_agent_type: 'cc',
          root_event_name: 'Stop',
          root_reason: 'hook_post',
          latest_step_kind: 'emit',
          latest_decision: 'broadcasted',
          latest_step_reason: 'session_code_resolved',
          step_count: 1,
        },
      ],
      next_cursor: '',
    })
    vi.mocked(fetchAgentMonitorChain).mockImplementation((_hostId, chainId) => {
      if (chainId === 'chain-2') return chain2Detail.promise
      return chain1Detail.promise
    })
    vi.mocked(fetchAgentMonitorProjection).mockImplementation((_hostId, query) => {
      if (query.toString() === 'pane=%258') return chain2Projection.promise
      return chain1Projection.promise
    })

    render(<TmuxAgentMonitorSection />)

    await waitFor(() => expect(screen.getByText('chain-2')).toBeInTheDocument())
    fireEvent.click(screen.getByText('chain-2'))

    chain2Detail.resolve({
      chain: {
        chain_id: 'chain-2',
        started_at: 101,
        completed_at: 121,
        terminal_status: 'completed',
        terminal_reason: 'emit_broadcasted',
        tmux_session: 'work',
        pane_id: '%8',
        root_agent_type: 'cc',
        root_event_name: 'Stop',
        root_reason: 'hook_post',
        latest_step_kind: 'emit',
        latest_decision: 'broadcasted',
        latest_step_reason: 'session_code_resolved',
        step_count: 1,
      },
      step_tree: [{
        step: {
          step_id: 'step-2',
          chain_id: 'chain-2',
          seq: 1,
          kind: 'emit',
          tmux_session: 'work',
          pane_id: '%8',
          agent_type: 'cc',
          frame_id: '',
          parent_frame_id: '',
          event_name: 'Stop',
          decision: 'broadcasted',
          reason: 'session_code_resolved',
          payload_json: '{"prompt":"second"}',
          before_json: 'null',
          after_json: '{"done":true}',
          created_at: 200,
        },
        children: [],
      }],
    })
    chain2Projection.resolve({
      projection: {
        tmux_session: 'work',
        pane_id: '%8',
        primary_frame_id: 'frame-2',
        top_frame_id: 'frame-2',
        top_agent_type: 'cc',
        latest_chain_id: 'chain-2',
      },
    })

    await waitFor(() => expect(screen.getByText('{"prompt":"second"}')).toBeInTheDocument())

    chain1Detail.resolve({
      chain: {
        chain_id: 'chain-1',
        started_at: 100,
        completed_at: 120,
        terminal_status: 'completed',
        terminal_reason: 'emit_broadcasted',
        tmux_session: 'work',
        pane_id: '%7',
        root_agent_type: 'codex',
        root_event_name: 'UserPromptSubmit',
        root_reason: 'hook_post',
        latest_step_kind: 'emit',
        latest_decision: 'broadcasted',
        latest_step_reason: 'session_code_resolved',
        step_count: 1,
      },
      step_tree: [{
        step: {
          step_id: 'step-1',
          chain_id: 'chain-1',
          seq: 1,
          kind: 'trigger',
          tmux_session: 'work',
          pane_id: '%7',
          agent_type: 'codex',
          frame_id: '',
          parent_frame_id: '',
          event_name: 'UserPromptSubmit',
          decision: 'received',
          reason: 'hook_post',
          payload_json: '{"prompt":"first"}',
          before_json: 'null',
          after_json: '{"accepted":true}',
          created_at: 100,
        },
        children: [],
      }],
    })
    chain1Projection.resolve({
      projection: {
        tmux_session: 'work',
        pane_id: '%7',
        primary_frame_id: 'frame-1',
        top_frame_id: 'frame-1',
        top_agent_type: 'codex',
        latest_chain_id: 'chain-1',
      },
    })

    await waitFor(() => expect(screen.getByText('{"prompt":"second"}')).toBeInTheDocument())
    expect(screen.queryByText('{"prompt":"first"}')).not.toBeInTheDocument()
    expect(screen.queryByText(/frame-1/)).not.toBeInTheDocument()
  })
})
