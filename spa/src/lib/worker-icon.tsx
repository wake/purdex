// spa/src/lib/worker-icon.tsx — the worker (execution) tab icon, per the
// `iconStyle` setting (worker-pane theme spec §8.3 / L2). The light is drawn
// on whatever this returns exactly as on a terminal agent icon.
import type { TabIconComponent } from '../hooks/useSessionAgentIndicator'
import type { CcIconVariant, CodexIconVariant } from '../stores/useUISettingsStore'
import type { WorkerIconStyle } from '../stores/useWorkerSettingsStore'
import { getAgentIcon, CC_COLOR_ICON_VARIANTS, CODEX_COLOR_ICON, CC_ICON_VARIANTS, CODEX_ICON_VARIANTS } from './agent-icons'
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

// Profile Sync only checks that ccIconVariant/codexIconVariant are strings
// (not that they're a known variant — see `useUISettingsStore`'s validator),
// so a stale or foreign value can reach here at runtime even though the
// static type claims otherwise. Guard both variant lookups against that: an
// unrecognised value falls back to the default variant's icon rather than
// `undefined`, for mono and color alike.
// `in` also matches inherited Object.prototype keys (`__proto__`,
// `constructor`, `toString`), which a synced value can legitimately be —
// use `Object.hasOwn` so only an actual variant entry passes.
function safeCcVariant(v: CcIconVariant): CcIconVariant {
  return Object.hasOwn(CC_ICON_VARIANTS, v) ? v : 'bot'
}

function safeCodexVariant(v: CodexIconVariant): CodexIconVariant {
  return Object.hasOwn(CODEX_ICON_VARIANTS, v) ? v : 'openai'
}

export function workerIcon(provider: string, style: WorkerIconStyle, opts: WorkerIconOptions): TabIconComponent {
  if (style === 'custom') return customIconComponent(opts.customIcon)
  const agentType = providerAgentType(provider)
  const ccVariant = safeCcVariant(opts.ccVariant)
  const codexVariant = safeCodexVariant(opts.codexVariant)
  if (style === 'color') {
    if (agentType === 'cc') return CC_COLOR_ICON_VARIANTS[ccVariant]
    if (agentType === 'codex') return CODEX_COLOR_ICON
    // No colour logo for this provider: its mono logo, else Robot (below).
  }
  // A provider `getAgentIcon` recognises but has no case above for (e.g.
  // opencode) still resolves to its own logo here; only a provider unknown
  // to `getAgentIcon` itself falls through to Robot.
  return getAgentIcon(agentType, { ccVariant, codexVariant }) ?? ROBOT
}
