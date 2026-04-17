/// <reference types="vite-plugin-svgr/client" />
import type { ComponentType, SVGProps } from 'react'
import { OpenAiLogo } from '@phosphor-icons/react'
import ClaudeCodeBotSvg from '@lobehub/icons-static-svg/icons/claudecode.svg?react'
import ClaudeStarSvg from '@lobehub/icons-static-svg/icons/claude.svg?react'
import type { CcIconVariant } from '../stores/useAgentStore'

type SvgComponent = ComponentType<SVGProps<SVGSVGElement>>

export type AgentIconComponent = ComponentType<{ size: number; className?: string }>

function wrapSvg(Svg: SvgComponent): AgentIconComponent {
  return function AgentBrandIcon({ size, className }) {
    return <Svg width={size} height={size} className={className} />
  }
}

function CodexIcon({ size, className }: { size: number; className?: string }) {
  return <OpenAiLogo size={size} className={className} />
}

const CC_VARIANTS: Record<CcIconVariant, AgentIconComponent> = {
  bot: wrapSvg(ClaudeCodeBotSvg),
  star: wrapSvg(ClaudeStarSvg),
}

export interface GetAgentIconOptions {
  ccVariant: CcIconVariant
}

// This file is a component registry — every export resolves to a component.
// eslint-disable-next-line react-refresh/only-export-components
export function getAgentIcon(agentType: string, options: GetAgentIconOptions): AgentIconComponent | undefined {
  if (agentType === 'cc') return CC_VARIANTS[options.ccVariant]
  if (agentType === 'codex') return CodexIcon
  return undefined
}

/** Icon components for each cc variant — exposed so Settings can render a live preview. */
export const CC_ICON_VARIANTS = CC_VARIANTS
