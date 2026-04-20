import type { AgentMonitorStepNode } from '../../../lib/host-api'
import { useI18nStore } from '../../../stores/useI18nStore'

interface StepTreeProps {
  nodes: AgentMonitorStepNode[]
  selectedStepId: string
  onSelectStep: (stepId: string) => void
}

export function StepTree({ nodes, selectedStepId, onSelectStep }: StepTreeProps) {
  const t = useI18nStore((s) => s.t)

  return (
    <div className="rounded-lg border border-border-default bg-surface-secondary overflow-hidden">
      <div className="border-b border-border-default px-4 py-3 text-sm font-medium text-text-primary">
        {t('settings.monitor.step_tree')}
      </div>
      <div className="max-h-[28rem] overflow-auto p-3">
        {nodes.length === 0 && <div className="text-sm text-text-secondary">{t('settings.monitor.empty_steps')}</div>}
        {nodes.map((node) => (
          <StepTreeNode
            key={node.step.step_id}
            node={node}
            depth={0}
            selectedStepId={selectedStepId}
            onSelectStep={onSelectStep}
          />
        ))}
      </div>
    </div>
  )
}

interface StepTreeNodeProps {
  node: AgentMonitorStepNode
  depth: number
  selectedStepId: string
  onSelectStep: (stepId: string) => void
}

function StepTreeNode({ node, depth, selectedStepId, onSelectStep }: StepTreeNodeProps) {
  const active = node.step.step_id === selectedStepId

  return (
    <div className="space-y-2">
      <button
        type="button"
        className={`w-full rounded-md border px-3 py-2 text-left ${
          active ? 'border-accent-primary bg-surface-primary' : 'border-border-default bg-transparent hover:bg-surface-primary'
        }`}
        style={{ marginLeft: `${depth * 16}px` }}
        onClick={() => onSelectStep(node.step.step_id)}
      >
        <div className="text-sm text-text-primary">{node.step.kind}</div>
        <div className="mt-1 text-xs text-text-secondary">
          {node.step.decision} · {node.step.reason}
        </div>
        <div className="mt-1 text-xs text-text-secondary">
          {node.step.agent_type || 'unknown'}
          {node.step.frame_id ? ` · ${node.step.frame_id}` : ''}
          {node.step.parent_frame_id ? ` · ${node.step.parent_frame_id}` : ''}
        </div>
      </button>
      {node.children.map((child) => (
        <StepTreeNode
          key={child.step.step_id}
          node={child}
          depth={depth + 1}
          selectedStepId={selectedStepId}
          onSelectStep={onSelectStep}
        />
      ))}
    </div>
  )
}
