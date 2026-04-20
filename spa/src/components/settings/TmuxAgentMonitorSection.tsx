import { useEffect, useEffectEvent, useRef, useState } from 'react'
import {
  fetchAgentMonitorChain,
  fetchAgentMonitorChains,
  fetchAgentMonitorProjection,
} from '../../lib/host-api'
import type {
  AgentMonitorChainSummary,
  AgentMonitorProjectionSummary,
  AgentMonitorStep,
  AgentMonitorStepNode,
} from '../../lib/host-api'
import { useHostStore } from '../../stores/useHostStore'
import { useI18nStore } from '../../stores/useI18nStore'
import { ChainList } from './tmux-agent-monitor/ChainList'
import { StepTree } from './tmux-agent-monitor/StepTree'
import { StepInspector } from './tmux-agent-monitor/StepInspector'

export function TmuxAgentMonitorSection() {
  const t = useI18nStore((s) => s.t)
  const hostId = useHostStore((s) => s.activeHostId ?? s.hostOrder[0] ?? '')

  const [sessionFilter, setSessionFilter] = useState('')
  const [paneFilter, setPaneFilter] = useState('')
  const [agentTypeFilter, setAgentTypeFilter] = useState('')
  const [eventNameFilter, setEventNameFilter] = useState('')
  const [chains, setChains] = useState<AgentMonitorChainSummary[]>([])
  const [selectedChainId, setSelectedChainId] = useState('')
  const [stepTree, setStepTree] = useState<AgentMonitorStepNode[]>([])
  const [selectedStep, setSelectedStep] = useState<AgentMonitorStep | null>(null)
  const [projection, setProjection] = useState<AgentMonitorProjectionSummary | null>(null)
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState('')
  const requestIdRef = useRef(0)

  function nextRequestId() {
    requestIdRef.current += 1
    return requestIdRef.current
  }

  function isCurrentRequest(requestId: number) {
    return requestIdRef.current === requestId
  }

  function clearSelectedChainState() {
    setStepTree([])
    setSelectedStep(null)
    setProjection(null)
  }

  async function refreshChains(requestId = nextRequestId()) {
    if (!hostId) return

    setLoading(true)
    setError('')
    clearSelectedChainState()
    try {
      const query = new URLSearchParams()
      if (sessionFilter) query.set('session', sessionFilter)
      if (paneFilter) query.set('pane', paneFilter)
      if (agentTypeFilter) query.set('agent_type', agentTypeFilter)
      if (eventNameFilter) query.set('event_name', eventNameFilter)
      query.set('limit', '50')

      const page = await fetchAgentMonitorChains(hostId, query)
      if (!isCurrentRequest(requestId)) return

      setChains(page.chains)

      const firstChain = page.chains[0]
      if (!firstChain) {
        setSelectedChainId('')
        await refreshProjection(hostId, { session: sessionFilter, pane: paneFilter }, requestId)
        return
      }

      setSelectedChainId(firstChain.chain_id)
      const [detail, projectionSummary] = await Promise.all([
        loadChain(hostId, firstChain.chain_id, requestId),
        refreshProjection(hostId, { session: firstChain.tmux_session || sessionFilter, pane: firstChain.pane_id || paneFilter }, requestId),
      ])
      if (!isCurrentRequest(requestId)) return

      setStepTree(detail.step_tree)
      setSelectedStep(firstStep(detail.step_tree))
      setProjection(projectionSummary)
    } catch (err) {
      if (!isCurrentRequest(requestId)) return
      setChains([])
      setSelectedChainId('')
      clearSelectedChainState()
      setError(err instanceof Error ? err.message : String(err))
    } finally {
      if (isCurrentRequest(requestId)) {
        setLoading(false)
      }
    }
  }

  const refreshChainsOnHostChange = useEffectEvent(() => {
    void refreshChains()
  })

  useEffect(() => {
    refreshChainsOnHostChange()
  }, [hostId])

  async function loadChain(currentHostId: string, chainId: string, requestId: number) {
    const detail = await fetchAgentMonitorChain(currentHostId, chainId)
    if (!isCurrentRequest(requestId)) {
      throw new Error('stale-request')
    }
    return detail
  }

  async function refreshProjection(
    currentHostId: string,
    target: { session: string; pane: string },
    requestId: number,
  ) {
    const { session, pane } = target
    if (!session && !pane) {
      return null
    }
    const query = new URLSearchParams()
    if (pane) {
      query.set('pane', pane)
    } else if (session) {
      query.set('session', session)
    }
    const result = await fetchAgentMonitorProjection(currentHostId, query)
    if (!isCurrentRequest(requestId)) {
      throw new Error('stale-request')
    }
    return result.projection
  }

  async function handleSelectChain(chain: AgentMonitorChainSummary) {
    if (!hostId) return
    const requestId = nextRequestId()
    setSelectedChainId(chain.chain_id)
    setLoading(true)
    setError('')
    clearSelectedChainState()
    try {
      const [detail, projectionSummary] = await Promise.all([
        loadChain(hostId, chain.chain_id, requestId),
        refreshProjection(hostId, { session: chain.tmux_session, pane: chain.pane_id }, requestId),
      ])
      if (!isCurrentRequest(requestId)) return

      setStepTree(detail.step_tree)
      setSelectedStep(firstStep(detail.step_tree))
      setProjection(projectionSummary)
    } catch (err) {
      if (!isCurrentRequest(requestId)) return
      clearSelectedChainState()
      setError(err instanceof Error ? err.message : String(err))
    } finally {
      if (isCurrentRequest(requestId)) {
        setLoading(false)
      }
    }
  }

  function handleSelectStep(stepId: string) {
    const step = findStep(stepTree, stepId)
    if (step) {
      setSelectedStep(step)
    }
  }

  return (
    <div className="space-y-6">
      <div>
        <h2 className="text-lg text-text-primary">{t('settings.monitor.title')}</h2>
        <p className="text-xs text-text-secondary mb-6">{t('settings.monitor.desc')}</p>
      </div>

      <div className="grid gap-3 md:grid-cols-4">
        <input
          value={sessionFilter}
          onChange={(e) => setSessionFilter(e.target.value)}
          placeholder={t('settings.monitor.filter.session')}
          className="rounded-md border border-border-default bg-surface-secondary px-3 py-2 text-sm text-text-primary"
        />
        <input
          value={paneFilter}
          onChange={(e) => setPaneFilter(e.target.value)}
          placeholder={t('settings.monitor.filter.pane')}
          className="rounded-md border border-border-default bg-surface-secondary px-3 py-2 text-sm text-text-primary"
        />
        <input
          value={agentTypeFilter}
          onChange={(e) => setAgentTypeFilter(e.target.value)}
          placeholder={t('settings.monitor.filter.agent')}
          className="rounded-md border border-border-default bg-surface-secondary px-3 py-2 text-sm text-text-primary"
        />
        <div className="flex gap-2">
          <input
            value={eventNameFilter}
            onChange={(e) => setEventNameFilter(e.target.value)}
            placeholder={t('settings.monitor.filter.event')}
            className="min-w-0 flex-1 rounded-md border border-border-default bg-surface-secondary px-3 py-2 text-sm text-text-primary"
          />
          <button
            type="button"
            onClick={() => { void refreshChains() }}
            className="rounded-md border border-border-default bg-surface-secondary px-4 py-2 text-sm text-text-primary"
          >
            {t('settings.monitor.refresh')}
          </button>
        </div>
      </div>

      {error && (
        <div className="rounded-md border border-danger/40 bg-danger/10 px-4 py-3 text-sm text-danger">
          {error}
        </div>
      )}

      <div className="grid gap-4 xl:grid-cols-[minmax(0,0.95fr)_minmax(0,1.15fr)_minmax(0,1fr)]">
        <ChainList
          chains={chains}
          selectedChainId={selectedChainId}
          onSelectChain={(chain) => { void handleSelectChain(chain) }}
        />
        <StepTree
          nodes={stepTree}
          selectedStepId={selectedStep?.step_id ?? ''}
          onSelectStep={handleSelectStep}
        />
        <StepInspector step={selectedStep} projection={projection} />
      </div>

      {loading && (
        <div className="text-sm text-text-secondary">{t('settings.monitor.loading')}</div>
      )}
    </div>
  )
}

function firstStep(nodes: AgentMonitorStepNode[]): AgentMonitorStep | null {
  for (const node of nodes) {
    return node.step
  }
  return null
}

function findStep(nodes: AgentMonitorStepNode[], stepId: string): AgentMonitorStep | null {
  for (const node of nodes) {
    if (node.step.step_id === stepId) {
      return node.step
    }
    const child = findStep(node.children, stepId)
    if (child) {
      return child
    }
  }
  return null
}
