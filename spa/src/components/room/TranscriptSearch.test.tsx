// spa/src/components/room/TranscriptSearch.test.tsx — the search bar over a
// real transcript (R3 plan T3.3; A4/A5/A8/A11 from the R3-C1 review). jsdom
// has no CSS Custom Highlight API, so it is stubbed to observe the marks.
import { useRef, useState } from 'react'
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, fireEvent, act } from '@testing-library/react'
import TranscriptSearch from './TranscriptSearch'
import RoomTranscript from './RoomTranscript'
import ChatTranscript from '../chat/ChatTranscript'
import { FoldContext, useFoldMemory } from './fold-context'
import { clearSearchHighlights } from '../../lib/nex/search-highlight'
import type { ContentBlock, StreamMessage } from '../../lib/nex/message-types'
import type { PartialAssembly } from '../../lib/nex/partial'
import type { ToolActivity } from '../../lib/nex/tool-activity'
import type { TranscriptScrollControl } from '../../hooks/useTranscriptScroll'

const asst = (...blocks: ContentBlock[]): StreamMessage =>
  ({ type: 'assistant', message: { id: 'm', role: 'assistant', content: blocks, stop_reason: null } } as StreamMessage)
const usr = (...blocks: ContentBlock[]): StreamMessage =>
  ({ type: 'user', message: { role: 'user', content: blocks, stop_reason: null } } as StreamMessage)
const said = (text: string): StreamMessage => usr({ type: 'text', text })
const prose = (text: string): StreamMessage => asst({ type: 'text', text })
const call = (id: string, command: string): ContentBlock => ({ type: 'tool_use', id, name: 'Bash', input: { command } })
const res = (id: string, content: string): ContentBlock => ({ type: 'tool_result', tool_use_id: id, content, is_error: false })
const ran = (command: string): ToolActivity =>
  ({ name: 'Bash', startedAt: 0, endedAt: 1, status: 'done', primaryArg: { key: 'command', value: command } })
const textPartial = (text: string): PartialAssembly =>
  ({ messageId: 'mp', finalized: 0, blocks: { 0: { index: 0, type: 'text', text, thinking: '', partialJson: '' } } })

class FakeHighlight {
  ranges: Range[] = []
  add(range: Range) {
    this.ranges.push(range)
    return this
  }
}
const g = globalThis as unknown as { CSS?: unknown; Highlight?: unknown }
let highlights: Map<string, FakeHighlight>
let saved: [unknown, unknown]

beforeEach(() => {
  saved = [g.CSS, g.Highlight]
  highlights = new Map()
  g.CSS = { highlights }
  g.Highlight = FakeHighlight
})
afterEach(() => {
  clearSearchHighlights('p1')
  ;[g.CSS, g.Highlight] = saved
  delete (Element.prototype as { scrollTo?: unknown }).scrollTo
  delete (Element.prototype as { scrollIntoView?: unknown }).scrollIntoView
  vi.restoreAllMocks()
})

const current = () => highlights.get('search-current')?.ranges ?? []
const marked = () => highlights.get('search-match')?.ranges ?? []
/** The unit the current mark sits in. */
const currentUnit = () => current()[0]?.startContainer.parentElement?.closest('[data-search-unit]')?.getAttribute('data-search-unit')

interface HarnessProps {
  messages: StreamMessage[]
  view?: 'room' | 'chat'
  tools?: Record<string, ToolActivity>
  turnStarts?: number[]
  partial?: PartialAssembly | null
  onClose?: () => void
}

/** The pane as ExecutionView composes it: one fold memory, the bar above the transcript, one scroll box. */
function Harness({ messages, view = 'room', tools, turnStarts = [0], partial = null, onClose = () => {} }: HarnessProps) {
  const fold = useFoldMemory()
  // As the pane: the scroll box is state, so a view switch (a new box) re-marks.
  const [box, setBox] = useState<HTMLDivElement | null>(null)
  const control = useRef<TranscriptScrollControl>(null)
  const Transcript = view === 'chat' ? ChatTranscript : RoomTranscript
  return (
    <FoldContext.Provider value={fold}>
      <TranscriptSearch owner="p1" container={box} messages={messages} tools={tools} view={view}
        keyPrefix="k" turnStarts={turnStarts} onClose={onClose} onJump={() => control.current?.release()} />
      <Transcript messages={messages} keyPrefix="k" showThinking={false} showEmptyHint={false}
        turnStarts={turnStarts} tools={tools} partial={partial} scrollRef={setBox} scrollControl={control} holdScroll />
    </FoldContext.Provider>
  )
}

const input = () => screen.getByTestId('transcript-search-input')
const count = () => screen.getByTestId('transcript-search-count')
const type = (value: string) => fireEvent.change(input(), { target: { value } })
const next = () => fireEvent.keyDown(input(), { key: 'Enter' })
const prev = () => fireEvent.keyDown(input(), { key: 'Enter', shiftKey: true })

describe('TranscriptSearch', () => {
  it('focuses its input when it opens', () => {
    render(<Harness messages={[said('hello')]} />)
    expect(input()).toHaveFocus()
  })

  it('shows the match count', () => {
    render(<Harness messages={[said('one needle'), prose('two needle, three needle')]} />)
    expect(screen.queryByTestId('transcript-search-count')).toBeNull()
    type('needle')
    expect(count()).toHaveTextContent('1 / 3')
    next()
    expect(count()).toHaveTextContent('2 / 3')
    type('nothing like it')
    expect(count()).toHaveTextContent('No results')
    // Too short to search: no count at all.
    type('n')
    expect(screen.queryByTestId('transcript-search-count')).toBeNull()
  })

  it('shows 10000+ past the limit', () => {
    render(<Harness messages={[said('ab '.repeat(10_001))]} />)
    type('ab')
    expect(count()).toHaveTextContent('1 / 10000+')
  })

  // User decision (2026-09-27): like a browser's find, the search starts
  // from what is on screen. jsdom has no layout: the viewport is stubbed as
  // the scroll box's top at 100 and each unit's bottom edge.
  describe('starting from the viewport', () => {
    const native = Element.prototype.getBoundingClientRect
    const layout = (bottoms: Record<string, number>) => {
      Element.prototype.getBoundingClientRect = function (this: Element) {
        const id = this.getAttribute('data-search-unit')
        const top = this.classList.contains('overflow-y-auto') ? 100 : id !== null && id in bottoms ? bottoms[id] - 20 : 0
        const bottom = this.classList.contains('overflow-y-auto') ? 300 : id !== null && id in bottoms ? bottoms[id] : 0
        return { top, bottom, left: 0, right: 0, width: 0, height: bottom - top, x: 0, y: top, toJSON() {} } as DOMRect
      }
    }
    afterEach(() => { Element.prototype.getBoundingClientRect = native })

    it('typing starts at the first match at or below the viewport', () => {
      render(<Harness messages={[said('xx a'), said('xx b'), said('xx c')]} />)
      layout({ '0:0:text': 50, '1:0:text': 150, '2:0:text': 250 })
      type('xx')
      expect(count()).toHaveTextContent('2 / 3')
      expect(currentUnit()).toBe('1:0:text')
      next()
      expect(currentUnit()).toBe('2:0:text')
      // Next wraps from the end to the first.
      next()
      expect(count()).toHaveTextContent('1 / 3')
      expect(currentUnit()).toBe('0:0:text')
    })

    it('wraps to the first match when none is below', () => {
      render(<Harness messages={[said('xx a'), said('xx b'), said('zz')]} />)
      layout({ '0:0:text': 50, '1:0:text': 80, '2:0:text': 150 })
      type('xx')
      expect(count()).toHaveTextContent('1 / 2')
      expect(currentUnit()).toBe('0:0:text')
    })

    it('past the limit, keeps the matches around the viewport and can reach the newest', () => {
      render(<Harness messages={[said('ab '.repeat(10_001)), said('ab newest')]} />)
      layout({ '0:0:text': 50, '1:0:text': 150 })
      type('ab')
      // The newest line is on screen and is where the search starts, though
      // 10,002 matches come before it: the oldest ones are the ones dropped.
      expect(currentUnit()).toBe('1:0:text')
      expect(count()).toHaveTextContent('10000 / 10000+')
      // Next, at the end, wraps to the very first match…
      next()
      expect(currentUnit()).toBe('0:0:text')
      expect(current()[0].startOffset).toBe(0)
      // …and previous, from there, back to the newest.
      prev()
      expect(currentUnit()).toBe('1:0:text')
    })
  })

  it('marks every match and the current one', () => {
    render(<Harness messages={[said('one needle'), said('two needle')]} />)
    type('needle')
    expect(current().map(String)).toEqual(['needle'])
    expect(currentUnit()).toBe('0:0:text')
    expect(marked().map(String)).toEqual(['needle'])
  })

  it('next expands a folded output that holds the match', () => {
    const body = Array.from({ length: 100 }, (_, i) => (i === 90 ? 'the needle line' : `line ${i}`)).join('\n')
    const messages = [said('go'), asst(call('t1', 'ls')), usr(res('t1', body)), said('a needle after')]
    render(<Harness messages={messages} tools={{ t1: ran('ls') }} />)
    expect(screen.getByTestId('fold-more')).toBeInTheDocument()
    type('needle')
    // The first match is the folded output's: reaching it opens the fold.
    expect(count()).toHaveTextContent('1 / 2')
    expect(screen.queryByTestId('fold-more')).toBeNull()
    expect(currentUnit()).toBe('1:0:output')
    next()
    expect(currentUnit()).toBe('3:0:text')
  })

  it('wraps from the last match to the first', () => {
    render(<Harness messages={[said('xx one'), said('xx two'), said('xx three')]} />)
    type('xx')
    next()
    next()
    expect(count()).toHaveTextContent('3 / 3')
    next()
    expect(count()).toHaveTextContent('1 / 3')
    expect(currentUnit()).toBe('0:0:text')
    prev()
    expect(count()).toHaveTextContent('3 / 3')
    fireEvent.click(screen.getByTestId('transcript-search-prev'))
    expect(count()).toHaveTextContent('2 / 3')
    fireEvent.click(screen.getByTestId('transcript-search-next'))
    expect(count()).toHaveTextContent('3 / 3')
  })

  it('does not move while an IME is composing', () => {
    render(<Harness messages={[said('錯 一'), said('錯 二')]} />)
    type('錯')
    fireEvent.keyDown(input(), { key: 'Enter', isComposing: true })
    expect(count()).toHaveTextContent('1 / 2')
  })

  it('escape closes and clears', () => {
    const onClose = vi.fn()
    const { unmount } = render(<Harness messages={[said('one needle')]} onClose={onClose} />)
    type('needle')
    expect(current()).toHaveLength(1)
    fireEvent.keyDown(input(), { key: 'Escape' })
    expect(onClose).toHaveBeenCalledTimes(1)
    fireEvent.click(screen.getByTestId('transcript-search-close'))
    expect(onClose).toHaveBeenCalledTimes(2)
    // The parent closes it by unmounting: the marks go with it.
    unmount()
    expect(highlights.has('search-current')).toBe(false)
    expect(highlights.has('search-match')).toBe(false)
  })

  it('works in chat and reveals through the tools line', () => {
    const messages = [said('go'), asst(call('a', 'grep needle')), usr(res('a', 'ok')), prose('done')]
    render(<Harness messages={messages} view="chat" tools={{ a: ran('grep needle') }} />)
    expect(screen.queryByTestId('chat-tools-ops')).toBeNull()
    type('needle')
    expect(count()).toHaveTextContent('1 / 1')
    expect(screen.getByTestId('chat-tools-ops')).toBeInTheDocument()
    expect(currentUnit()).toBe('1:0:arg')
  })

  it('a new message keeps the current match', () => {
    // Chat draws a turn's tools where its first one sits, so a new tool call
    // lands *before* the prose that is current: its list index moves, it does not.
    const tools = { a: ran('xx a'), b: ran('xx b') }
    const first = [said('go'), asst(call('a', 'xx a')), usr(res('a', 'ok')), prose('xx between')]
    const { rerender } = render(<Harness messages={first} view="chat" tools={tools} />)
    type('xx')
    next()
    expect(count()).toHaveTextContent('2 / 2')
    expect(currentUnit()).toBe('3:0:text')
    rerender(<Harness messages={[...first, asst(call('b', 'xx b')), usr(res('b', 'ok'))]} view="chat" tools={tools} />)
    expect(count()).toHaveTextContent('3 / 3')
    expect(currentUnit()).toBe('3:0:text')
  })

  it('the current match survives the stream ending', () => {
    const messages = [said('needle one'), prose('needle two')]
    const { rerender } = render(<Harness messages={messages} partial={null} />)
    type('needle')
    next()
    expect(count()).toHaveTextContent('2 / 2')
    // A streaming reply is not indexed…
    rerender(<Harness messages={messages} partial={textPartial('needle thr')} />)
    expect(count()).toHaveTextContent('2 / 2')
    // …until it lands.
    rerender(<Harness messages={[...messages, prose('needle three')]} partial={null} />)
    expect(count()).toHaveTextContent('2 / 3')
    expect(currentUnit()).toBe('1:0:text')
    expect(current()[0].collapsed).toBe(false)
  })

  it('a mark survives a new message', () => {
    const { rerender } = render(<Harness messages={[said('first needle')]} />)
    type('needle')
    const before = current()[0]
    expect(before.collapsed).toBe(false)
    // The marked line is redrawn (its text node replaced) as a message lands.
    rerender(<Harness messages={[said('the first needle'), said('more')]} />)
    expect(before.collapsed).toBe(true)
    expect(current()[0].collapsed).toBe(false)
    expect(current().map(String)).toEqual(['needle'])
  })

  it('re-marking after a new message does not drag the reader back to the match', () => {
    const intoView = vi.fn()
    Element.prototype.scrollIntoView = intoView
    const { rerender } = render(<Harness messages={[said('the needle')]} />)
    type('needle')
    expect(intoView).toHaveBeenCalledTimes(1)
    rerender(<Harness messages={[said('the needle'), said('a new line')]} />)
    expect(current()).toHaveLength(1)
    expect(intoView).toHaveBeenCalledTimes(1)
    next()
    expect(intoView).toHaveBeenCalledTimes(2)
  })

  // A F4: a match in the last screen leaves the box within NEAR_BOTTOM of
  // the end; after the jump, a new line must still not pull the reader down.
  it('a jump into the last screen stops the bottom-follow', () => {
    const scrollTo = vi.fn()
    Element.prototype.scrollTo = scrollTo as unknown as Element['scrollTo']
    const messages = [said('filler'), said('the needle')]
    const { rerender } = render(<Harness messages={messages} />)
    const box = document.querySelector('.overflow-y-auto') as HTMLElement
    const geometry = (scrollHeight: number, scrollTop: number) => {
      Object.defineProperty(box, 'scrollHeight', { configurable: true, value: scrollHeight })
      Object.defineProperty(box, 'clientHeight', { configurable: true, value: 200 })
      Object.defineProperty(box, 'scrollTop', { configurable: true, writable: true, value: scrollTop })
    }
    geometry(1000, 800)
    fireEvent.scroll(box)
    // The match is on the last screen: centring it is clamped near the end.
    Element.prototype.scrollIntoView = vi.fn(() => { box.scrollTop = 790 })
    type('needle')
    fireEvent.scroll(box)
    scrollTo.mockClear()
    geometry(1100, 790)
    rerender(<Harness messages={[...messages, said('a new line')]} />)
    expect(scrollTo).not.toHaveBeenCalled()
  })

  // R1-1 / A F2: room ⇄ chat remounts the transcript under an open bar.
  it('switching view keeps the marks, returns to the current match and is not pulled to the bottom', () => {
    const scrollTo = vi.fn()
    Element.prototype.scrollTo = scrollTo as unknown as Element['scrollTo']
    const intoView = vi.fn()
    Element.prototype.scrollIntoView = intoView
    const messages = [said('one needle'), said('two needle'), said('filler')]
    const { rerender } = render(<Harness messages={messages} />)
    type('needle')
    next()
    expect(currentUnit()).toBe('1:0:text')
    const jumps = intoView.mock.calls.length
    scrollTo.mockClear()
    rerender(<Harness messages={messages} view="chat" />)
    expect(document.querySelector('.\\@container')).not.toBeNull()
    expect(current()[0].collapsed).toBe(false)
    expect(currentUnit()).toBe('1:0:text')
    expect(marked()).toHaveLength(1)
    expect(intoView.mock.calls.length).toBe(jumps + 1)
    expect(scrollTo).not.toHaveBeenCalled()
  })

  it('a current match only one view draws hands over to the next one there', () => {
    const think = (text: string) => asst({ type: 'thinking', thinking: text } as ContentBlock)
    const messages = [think('x1'), think('x2'), said('needle a'), think('needle idea'), said('needle b'), said('needle c')]
    const { rerender } = render(<Harness messages={messages} />)
    type('needle')
    expect(currentUnit()).toBe('2:0:text')
    next()
    expect(currentUnit()).toBe('3:0:thinking')
    // Chat draws no thinking: the match after it takes over.
    rerender(<Harness messages={messages} view="chat" />)
    expect(currentUnit()).toBe('4:0:text')
    expect(count()).toHaveTextContent('2 / 3')
  })

  it('a streaming message does not scroll away from the current match', () => {
    const scrollTo = vi.fn()
    Element.prototype.scrollTo = scrollTo as unknown as Element['scrollTo']
    const messages = [said('the needle'), ...Array.from({ length: 5 }, (_, i) => said(`filler ${i}`))]
    const { rerender } = render(<Harness messages={messages} />)
    const box = document.querySelector('.overflow-y-auto') as HTMLElement
    const geometry = (scrollHeight: number, scrollTop: number) => {
      Object.defineProperty(box, 'scrollHeight', { configurable: true, value: scrollHeight })
      Object.defineProperty(box, 'clientHeight', { configurable: true, value: 200 })
      Object.defineProperty(box, 'scrollTop', { configurable: true, writable: true, value: scrollTop })
    }
    // The reader is at the bottom…
    geometry(1000, 800)
    fireEvent.scroll(box)
    // …and jumps to the match near the top (jsdom has no layout: the jump is stubbed).
    Element.prototype.scrollIntoView = vi.fn(() => { box.scrollTop = 50 })
    type('needle')
    expect(box.scrollTop).toBe(50)
    scrollTo.mockClear()
    act(() => {
      rerender(<Harness messages={messages} partial={textPartial('streaming…')} />)
    })
    rerender(<Harness messages={messages} partial={textPartial('streaming… more')} />)
    expect(scrollTo).not.toHaveBeenCalled()
    expect(box.scrollTop).toBe(50)
  })
})
