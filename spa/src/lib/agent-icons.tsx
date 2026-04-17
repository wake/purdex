/// <reference types="vite-plugin-svgr/client" />
import type { ComponentType, SVGProps } from 'react'
import ClaudeCodeSvg from '@lobehub/icons-static-svg/icons/claudecode.svg?react'
import CodexSvg from '@lobehub/icons-static-svg/icons/codex.svg?react'

type SvgComponent = ComponentType<SVGProps<SVGSVGElement>>

export type AgentIconComponent = ComponentType<{ size: number; className?: string }>

function wrap(Svg: SvgComponent): AgentIconComponent {
  return function AgentBrandIcon({ size, className }) {
    return <Svg width={size} height={size} className={className} />
  }
}

export const AGENT_ICONS: Record<string, AgentIconComponent> = {
  cc: wrap(ClaudeCodeSvg),
  codex: wrap(CodexSvg),
}
