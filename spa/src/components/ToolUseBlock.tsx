// spa/src/components/ToolUseBlock.tsx — a durable assistant `tool_use`
// content block (P-B2.2 spec §4.4 R2): looks up its ToolActivity by block id,
// maps it to the renderer's activity variant and hands ToolCallBlock the
// result. No `tools` (Stream mode) or no entry → today's plain DOM.
import { useI18nStore } from '../stores/useI18nStore'
import type { ContentBlock } from '../lib/nex/message-types'
import { toToolCallActivity, type ToolActivity } from '../lib/nex/tool-activity'
import ToolCallBlock from './ToolCallBlock'

interface Props {
  block: ContentBlock
  tools?: Record<string, ToolActivity>
  /** Ticker value (unix ms); only consulted for a running entry. */
  now: number
}

export default function ToolUseBlock({ block, tools, now }: Props) {
  const t = useI18nStore((s) => s.t)
  const entry = block.id ? tools?.[block.id] : undefined
  const activity = entry ? toToolCallActivity(entry, now) : undefined
  return <ToolCallBlock tool={block.name ?? t('execution.tool.unknown')} input={block.input ?? {}} activity={activity} />
}
