// Agent icons a command may carry, rendered by explicit variant — independent
// of the user's global cc/codex icon-variant setting (spec §4.3).
import { CC_ICON_VARIANTS, CODEX_ICON_VARIANTS, getAgentIcon, type AgentIconComponent } from './agent-icons'
import type { AgentIconValue, CommandIcon } from './host-config-api'

export const AGENT_ICON_VALUES: readonly AgentIconValue[] = ['cc-bot', 'cc-star', 'openai', 'codex', 'opencode']

export const DEFAULT_COMMAND_ICON: CommandIcon = { kind: 'phosphor', value: 'Terminal' }

const OPENCODE = getAgentIcon('opencode', { ccVariant: 'bot', codexVariant: 'openai' }) as AgentIconComponent

const AGENT_COMPONENTS: Record<AgentIconValue, AgentIconComponent> = {
  'cc-bot': CC_ICON_VARIANTS.bot,
  'cc-star': CC_ICON_VARIANTS.star,
  openai: CODEX_ICON_VARIANTS.openai,
  codex: CODEX_ICON_VARIANTS.codex,
  opencode: OPENCODE,
}

export function agentIconComponent(value: AgentIconValue): AgentIconComponent {
  return AGENT_COMPONENTS[value]
}
