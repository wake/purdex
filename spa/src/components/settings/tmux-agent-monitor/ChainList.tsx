import type { AgentMonitorChainSummary } from '../../../lib/host-api'
import { useI18nStore } from '../../../stores/useI18nStore'

interface ChainListProps {
  chains: AgentMonitorChainSummary[]
  selectedChainId: string
  onSelectChain: (chain: AgentMonitorChainSummary) => void
}

export function ChainList({ chains, selectedChainId, onSelectChain }: ChainListProps) {
  const t = useI18nStore((s) => s.t)

  return (
    <div className="rounded-lg border border-border-default bg-surface-secondary overflow-hidden">
      <div className="border-b border-border-default px-4 py-3 text-sm font-medium text-text-primary">
        {t('settings.monitor.chains')}
      </div>
      <div className="max-h-[28rem] overflow-auto">
        {chains.length === 0 && (
          <div className="px-4 py-6 text-sm text-text-secondary">{t('settings.monitor.empty_chains')}</div>
        )}
        {chains.map((chain) => {
          const active = chain.chain_id === selectedChainId
          return (
            <button
              key={chain.chain_id}
              type="button"
              className={`w-full border-b border-border-subtle px-4 py-3 text-left transition-colors last:border-b-0 ${
                active ? 'bg-surface-primary' : 'bg-transparent hover:bg-surface-primary'
              }`}
              onClick={() => onSelectChain(chain)}
            >
              <div className="text-sm font-medium text-text-primary">{chain.chain_id}</div>
              <div className="mt-1 text-xs text-text-secondary">
                {chain.tmux_session || 'no-session'} / {chain.pane_id || 'no-pane'}
              </div>
              <div className="mt-1 text-xs text-text-secondary">{String(chain.started_at)}</div>
              <div className="mt-1 text-xs text-text-secondary">
                {chain.root_agent_type || 'unknown'} · {chain.root_event_name || 'unknown'}
              </div>
            </button>
          )
        })}
      </div>
    </div>
  )
}
