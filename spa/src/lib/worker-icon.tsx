// spa/src/lib/worker-icon.tsx — the worker (execution) tab icon, per the
// `iconStyle` setting (worker-pane theme spec §8.3 / L2). The light is drawn
// on whatever this returns exactly as on a terminal agent icon.
import type { TabIconComponent } from '../hooks/useSessionAgentIndicator'
import type { CcIconVariant, CodexIconVariant } from '../stores/useUISettingsStore'
import type { WorkerIconStyle } from '../stores/useWorkerSettingsStore'
import { getAgentIcon, CC_COLOR_ICON_VARIANTS, CODEX_COLOR_ICON } from './agent-icons'
import { providerAgentType } from './nex/worker-agent-status'
import { ICON_MAP } from '../components/tab-icon-map'
// Same renderer + name catalogue the workspace icon picker uses (direct file
// imports, not the feature barrel: the barrel pulls in the activity bar, which
// renders tabs through useTabDisplay → here).
import { WorkspaceIcon } from '../features/workspace/components/WorkspaceIcon'
import iconMetaData from '../features/workspace/generated/icon-meta.json'

export interface WorkerIconOptions {
  ccVariant: CcIconVariant
  codexVariant: CodexIconVariant
  /** `''` = none set (never null). */
  customIcon: string
}

const ROBOT = ICON_MAP.Robot as TabIconComponent

const KNOWN_ICONS: ReadonlySet<string> = new Set((iconMetaData as { n: string }[]).map((m) => m.n))

// One component per name, so a re-render never remounts the tab icon.
const customCache = new Map<string, TabIconComponent>()

function customIconComponent(name: string): TabIconComponent {
  if (!KNOWN_ICONS.has(name)) return ROBOT
  let Icon = customCache.get(name)
  if (!Icon) {
    Icon = function WorkerCustomIcon({ size, className }) {
      return <WorkspaceIcon icon={name} name="" size={size} className={className} />
    }
    customCache.set(name, Icon)
  }
  return Icon
}

export function workerIcon(provider: string, style: WorkerIconStyle, opts: WorkerIconOptions): TabIconComponent {
  if (style === 'custom') return customIconComponent(opts.customIcon)
  const agentType = providerAgentType(provider)
  if (style === 'color') {
    if (agentType === 'cc') return CC_COLOR_ICON_VARIANTS[opts.ccVariant]
    if (agentType === 'codex') return CODEX_COLOR_ICON
    // No colour logo for this provider: its mono logo, else Robot (below).
  }
  // A provider `getAgentIcon` recognises but has no case above for (e.g.
  // opencode) still resolves to its own logo here; only a provider unknown
  // to `getAgentIcon` itself falls through to Robot.
  return getAgentIcon(agentType, { ccVariant: opts.ccVariant, codexVariant: opts.codexVariant }) ?? ROBOT
}
