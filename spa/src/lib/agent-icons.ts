import { Lightning, Code, Terminal } from '@phosphor-icons/react'
import type { Icon } from '@phosphor-icons/react'

export const AGENT_ICONS: Record<string, Icon> = {
  cc: Lightning,
  codex: Code,
}

export const AGENT_NAMES: Record<string, string> = {
  cc: 'Claude Code',
  codex: 'Codex',
}

export const DEFAULT_SESSION_ICON = Terminal
