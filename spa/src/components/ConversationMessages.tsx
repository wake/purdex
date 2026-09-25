// spa/src/components/ConversationMessages.tsx
import { useRef, useEffect, useMemo, type ReactNode } from 'react'
import { useI18nStore } from '../stores/useI18nStore'
import {
  type StreamMessage,
  type AssistantMessage,
  type UserMessage,
} from '../lib/nex/message-types'
import { partialVersionOf, type PartialAssembly } from '../lib/nex/partial'
import { toToolCallActivity, type ToolActivity } from '../lib/nex/tool-activity'
import { blockKey, indexOperations, toolResultText } from '../lib/nex/operations'
import MessageBubble from './MessageBubble'
import ThinkingBlock from './ThinkingBlock'
import ThinkingIndicator from './ThinkingIndicator'
import PartialMessageGroup from './PartialMessageGroup'
import OperationBlock from './room/OperationBlock'
import { FoldContext, useFoldMemory } from './room/fold-context'
import { Prohibit, TerminalWindow } from '@phosphor-icons/react'

export interface ConversationMessagesProps {
  messages: StreamMessage[]
  keyPrefix: string            // stable per pane; keys are `${keyPrefix}-${i}`
  showThinking: boolean        // ThinkingIndicator visibility
  showEmptyHint: boolean       // "waiting" hint (caller decides: Stream = no messages && !isStreaming)
  emptyText?: string           // override for the hint (default t('stream.waiting'))
  scrollKey?: number           // extra auto-scroll dependency (prompt count / optimistic bubble)
  children?: ReactNode         // rendered after the list, BEFORE ThinkingIndicator (Execution: optimistic bubble)
  afterThinking?: ReactNode    // rendered AFTER ThinkingIndicator (Stream: pending prompts — today's DOM order)
  // P-B2.2 spec §4.4 — Stream mode passes none of these and renders as today.
  partial?: PartialAssembly | null          // R1: trailing in-flight assistant group
  tools?: Record<string, ToolActivity>      // R2: status/timing for durable tool_use blocks, by block id
  now?: number                              // R2: ticker value for running tools
}

export default function ConversationMessages({
  messages,
  keyPrefix,
  showThinking,
  showEmptyHint,
  emptyText,
  scrollKey,
  children,
  afterThinking,
  partial,
  tools,
  now,
}: ConversationMessagesProps) {
  const t = useI18nStore((s) => s.t)
  const scrollRef = useRef<HTMLDivElement>(null)
  const hasPartial = !!partial && Object.keys(partial.blocks).length > 0
  // Spec §4.2: a tool_use and the tool_result that answers it are one block.
  // The pairing is positional, so the renderer asks by the call's own
  // position and skips exactly the result blocks a call already showed.
  const index = useMemo(() => indexOperations(messages), [messages])
  // Spec §3.2: one fold memory per pane, above the blocks — a block unmounts
  // whenever its row is re-keyed and would take a local useState with it.
  const foldStore = useFoldMemory()
  // R4: follow the typewriter by a content/structure key, not the assembly's identity.
  const partialVersion: string = useMemo(() => partialVersionOf(partial), [partial])

  // Auto-scroll on new messages, control requests, or partial growth
  useEffect(() => {
    if (scrollRef.current?.scrollTo) {
      scrollRef.current.scrollTo({ top: scrollRef.current.scrollHeight, behavior: 'smooth' })
    }
  }, [messages, scrollKey, partialVersion])

  return (
    <div ref={scrollRef} className="flex-1 overflow-y-auto p-4 space-y-4">
      <FoldContext.Provider value={foldStore}>
        {showEmptyHint && (
          <div className="flex items-center justify-center h-full text-text-muted text-sm">
            {emptyText ?? t('stream.waiting')}
          </div>
        )}
        {messages.map((msg, i) => {
          const key = `${keyPrefix}-${i}`

          // --- Assistant messages ---
          if (msg.type === 'assistant' && 'message' in msg) {
            const am = msg as AssistantMessage
            return (
              <div key={key}>
                {am.message.content.map((block, j) => {
                  if (block.type === 'thinking' && block.thinking) {
                    return <ThinkingBlock key={j} content={block.thinking} />
                  }
                  if (block.type === 'text' && block.text) {
                    return <MessageBubble key={j} role="assistant" content={block.text} />
                  }
                  if (block.type === 'tool_use') {
                    // R2: a tools entry (execution pane only) adds status, timing and
                    // the N2 facts. Own-key lookup: block ids are untrusted strings
                    // (`constructor` would otherwise hit Object.prototype), and this
                    // is the only place that guard still lives — OperationBlock takes
                    // no `tools` map.
                    const entry = tools && block.id && Object.hasOwn(tools, block.id) ? tools[block.id] : undefined
                    // The `??` belongs here, not in the block: it fires on an absent
                    // name, while the block's own guard fires on an empty one. A
                    // nameless call still renders — skipping it would consume its
                    // result in the pairing and then show it nowhere.
                    return (
                      <OperationBlock
                        key={j}
                        tool={block.name ?? t('execution.tool.unknown')}
                        input={block.input ?? {}}
                        activity={entry ? toToolCallActivity(entry, now ?? 0) : undefined}
                        summaryEntry={entry}
                        facts={entry}
                        result={index.resultForCall.get(blockKey(i, j)) ?? null}
                        foldKey={blockKey(i, j)}
                      />
                    )
                  }
                  return null
                })}
              </div>
            )
          }

          // --- User messages ---
          if (msg.type === 'user' && 'message' in msg) {
            const um = msg as UserMessage
            const blocks = um.message.content

            return (
              <div key={key}>
                {blocks.map((block, j) => {
                  // Tool result
                  if (block.type === 'tool_result') {
                    // Its call already showed it (spec §4.2) — drawing it again
                    // would be the second card the operation block replaces.
                    if (index.consumedResults.has(blockKey(i, j))) return null
                    // An orphan: no call on this list claimed it, so it carries
                    // the whole operation on its own. P-B3 R4: the N2 entry for
                    // this result's tool_use_id. Own-key lookup: ids are
                    // untrusted strings (`constructor` …).
                    const facts = tools && block.tool_use_id && Object.hasOwn(tools, block.tool_use_id)
                      ? tools[block.tool_use_id]
                      : undefined
                    return (
                      <OperationBlock
                        key={j}
                        tool={facts?.file?.path ?? t('execution.tool.unknown')}
                        input={{}}
                        facts={facts}
                        result={{ text: toolResultText(block.content), isError: block.is_error ?? false }}
                        foldKey={blockKey(i, j)}
                      />
                    )
                  }

                  // Text blocks
                  if (block.type === 'text' && block.text) {
                    // Interrupted
                    if (block.text === '[Request interrupted by user]') {
                      return (
                        <div key={j} data-testid="interrupted-msg"
                          className="flex items-center gap-1.5 bg-status-error rounded-[12px_12px_4px_12px] px-3 py-1.5 text-sm text-[#eaa] italic"> {/* TODO: theme token for text-[#eaa] */}
                          <Prohibit size={14} />
                          <span>{t('stream.interrupted')}</span>
                        </div>
                      )
                    }

                    // Slash command
                    if (block.text.startsWith('/')) {
                      return (
                        <div key={j} className="flex justify-end">
                          <div data-testid="command-bubble"
                            className="flex items-center gap-1.5 bg-status-warning rounded-[12px_12px_4px_12px] px-3 py-1.5 text-[13px] text-[#e0d0a0] italic font-mono"> {/* TODO: theme token for text-[#e0d0a0] */}
                            <TerminalWindow size={14} weight="bold" className="text-[#c0a060]" /> {/* TODO: theme token */}
                            <span>{block.text}</span>
                          </div>
                        </div>
                      )
                    }

                    // Normal user text
                    return <MessageBubble key={j} role="user" content={block.text} />
                  }

                  return null
                })}
              </div>
            )
          }

          return null
        })}

        {/* R1: the in-flight assistant message, after the durable list and before children */}
        {hasPartial && <PartialMessageGroup key={`${keyPrefix}-partial`} partial={partial} />}

        {children}

        {/* Thinking indicator */}
        <ThinkingIndicator visible={showThinking} />

        {afterThinking}
      </FoldContext.Provider>
    </div>
  )
}
