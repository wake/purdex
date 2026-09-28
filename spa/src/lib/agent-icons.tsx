/// <reference types="vite-plugin-svgr/client" />
import type { ComponentType, SVGProps } from 'react'
import { OpenAiLogo } from '@phosphor-icons/react'
import ClaudeCodeBotSvg from '@lobehub/icons-static-svg/icons/claudecode.svg?react'
import ClaudeStarSvg from '@lobehub/icons-static-svg/icons/claude.svg?react'
import CodexLobeSvg from '@lobehub/icons-static-svg/icons/codex.svg?react'
import OpenCodeSvg from '@lobehub/icons-static-svg/icons/opencode.svg?react'
import ClaudeCodeBotColorSvg from '@lobehub/icons-static-svg/icons/claudecode-color.svg?react'
import ClaudeStarColorSvg from '@lobehub/icons-static-svg/icons/claude-color.svg?react'
import CodexColorSvg from '@lobehub/icons-static-svg/icons/codex-color.svg?react'
import type { CcIconVariant, CodexIconVariant } from '../stores/useUISettingsStore'

type SvgComponent = ComponentType<SVGProps<SVGSVGElement>>

export type AgentIconComponent = ComponentType<{ size: number; className?: string }>

function wrapSvg(Svg: SvgComponent): AgentIconComponent {
  return function AgentBrandIcon({ size, className }) {
    return <Svg width={size} height={size} className={className} aria-hidden="true" />
  }
}

function CodexOpenAiIcon({ size, className }: { size: number; className?: string }) {
  return <OpenAiLogo size={size} className={className} aria-hidden="true" />
}

const CC_VARIANTS: Record<CcIconVariant, AgentIconComponent> = {
  bot: wrapSvg(ClaudeCodeBotSvg),
  star: wrapSvg(ClaudeStarSvg),
}

const CODEX_VARIANTS: Record<CodexIconVariant, AgentIconComponent> = {
  openai: CodexOpenAiIcon,
  codex: wrapSvg(CodexLobeSvg),
}

// Single brand icon for opencode (no variant system — plan §5 explicitly
// rejects introducing opencodeVariant for a single official svg).
const OPENCODE_ICON: AgentIconComponent = wrapSvg(OpenCodeSvg)

export interface GetAgentIconOptions {
  ccVariant: CcIconVariant
  codexVariant: CodexIconVariant
}

// This file is a component registry — every export resolves to a component.
// eslint-disable-next-line react-refresh/only-export-components
export function getAgentIcon(agentType: string, options: GetAgentIconOptions): AgentIconComponent | undefined {
  if (agentType === 'cc') return CC_VARIANTS[options.ccVariant]
  if (agentType === 'codex') return CODEX_VARIANTS[options.codexVariant]
  if (agentType === 'opencode') return OPENCODE_ICON
  return undefined
}

/** Icon components for each cc variant — exposed so Settings can render a live preview. */
export const CC_ICON_VARIANTS = CC_VARIANTS

/** Icon components for each codex variant — exposed so Settings can render a live preview. */
export const CODEX_ICON_VARIANTS = CODEX_VARIANTS

/**
 * Original-colour provider logos for the worker tab's `color` icon style
 * (worker theme spec §8.3). Codex has one: OpenAI's mark has no colour
 * variant, so `color` always uses the lobe `codex-color`.
 */
const CC_COLOR_VARIANTS: Record<CcIconVariant, AgentIconComponent> = {
  bot: wrapSvg(ClaudeCodeBotColorSvg),
  star: wrapSvg(ClaudeStarColorSvg),
}

const CODEX_COLOR: AgentIconComponent = wrapSvg(CodexColorSvg)

export const CC_COLOR_ICON_VARIANTS = CC_COLOR_VARIANTS
export const CODEX_COLOR_ICON = CODEX_COLOR
