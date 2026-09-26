// spa/src/components/chat/ChatPartialGroup.test.tsx — chat's streaming blocks
// (R2 plan T1.3b): the typewriter goes into an agent bubble; a thought and a
// tool call being streamed draw nothing (spec §5).
import type { ReactNode } from 'react'
import { describe, it, expect } from 'vitest'
import { render, screen, within } from '@testing-library/react'
import ChatPartialGroup from './ChatPartialGroup'
import { FoldContext, useFoldMemory } from '../room/fold-context'
import type { PartialAssembly, PartialBlock } from '../../lib/nex/partial'

function Pane({ children }: { children: ReactNode }) {
  const store = useFoldMemory()
  return <FoldContext.Provider value={store}>{children}</FoldContext.Provider>
}

const pb = (index: number, over: Partial<PartialBlock> & { type: PartialBlock['type'] }): PartialBlock =>
  ({ index, text: '', thinking: '', partialJson: '', ...over })
const assembly = (...blocks: PartialBlock[]): PartialAssembly =>
  ({ messageId: 'm', finalized: 0, blocks: Object.fromEntries(blocks.map((b) => [b.index, b])) })

const renderGroup = (partial: PartialAssembly) =>
  render(<Pane><ChatPartialGroup partial={partial} /></Pane>)

describe('ChatPartialGroup', () => {
  it('streams text into an agent bubble with the cursor', () => {
    renderGroup(assembly(pb(0, { type: 'text', text: 'hello' })))
    const group = within(screen.getByTestId('chat-partial-group'))
    const bubble = group.getByTestId('chat-bubble-agent')
    const prose = within(bubble).getByTestId('room-prose')
    expect(prose).toHaveTextContent('hello')
    expect(within(prose).getByTestId('stream-cursor')).toBeInTheDocument()
  })

  it('renders nothing for a streaming thought', () => {
    renderGroup(assembly(pb(0, { type: 'thinking', thinking: 'secret musing' })))
    expect(screen.queryByText(/secret musing/)).toBeNull()
    expect(screen.queryByTestId('room-thinking')).toBeNull()
    expect(screen.queryByTestId('chat-bubble-agent')).toBeNull()
    expect(screen.queryByTestId('stream-cursor')).toBeNull()
  })

  it('renders nothing for a streaming tool_use', () => {
    renderGroup(assembly(pb(0, { type: 'tool_use', toolName: 'Bash', partialJson: '{"command":"ls' })))
    expect(screen.queryByText(/Bash/)).toBeNull()
    expect(screen.queryByTestId('operation-block')).toBeNull()
    expect(screen.queryByTestId('chat-bubble-agent')).toBeNull()
  })
})
