// spa/src/components/room/transcript-scroll.test.tsx — both transcripts hand
// their scroll container out (`scrollRef`) and, while `holdScroll` is on
// (the search bar is open), follow new content only when the reader was
// already at the bottom (R3 plan T3.3, A4).
import { createRef } from 'react'
import type { TranscriptScrollControl } from '../../hooks/useTranscriptScroll'
import { describe, it, expect, vi, afterEach, beforeEach } from 'vitest'
import { render, fireEvent } from '@testing-library/react'
import RoomTranscript, { type RoomTranscriptProps } from './RoomTranscript'
import ChatTranscript from '../chat/ChatTranscript'
import type { StreamMessage } from '../../lib/nex/message-types'

const said = (text: string): StreamMessage =>
  ({ type: 'user', message: { role: 'user', content: [{ type: 'text', text }], stop_reason: null } }) as StreamMessage

const views = [
  ['room', RoomTranscript],
  ['chat', ChatTranscript],
] as const

/** jsdom has no layout: give the box a geometry and a scrollTop that sticks. */
function geometry(box: HTMLElement, scrollHeight: number, clientHeight: number, scrollTop: number) {
  Object.defineProperty(box, 'scrollHeight', { configurable: true, value: scrollHeight })
  Object.defineProperty(box, 'clientHeight', { configurable: true, value: clientHeight })
  Object.defineProperty(box, 'scrollTop', { configurable: true, writable: true, value: scrollTop })
}

const scrollTo = vi.fn()
beforeEach(() => {
  Element.prototype.scrollTo = scrollTo as unknown as Element['scrollTo']
})
afterEach(() => {
  scrollTo.mockClear()
  delete (Element.prototype as { scrollTo?: unknown }).scrollTo
})

describe.each(views)('%s transcript scrolling', (_name, Transcript) => {
  const T = (props: Partial<RoomTranscriptProps> & Pick<RoomTranscriptProps, 'messages'>) => (
    <Transcript keyPrefix="k" showThinking={false} showEmptyHint={false} turnStarts={[0]} {...props} />
  )

  it('scrollRef reaches the scroll container', () => {
    const ref = createRef<HTMLDivElement>()
    render(T({ messages: [said('hello')], scrollRef: ref }))
    expect(ref.current).toBeInstanceOf(HTMLDivElement)
    expect(ref.current).toHaveClass('overflow-y-auto')
    expect(ref.current).toHaveTextContent('hello')
  })

  it('without holdScroll new content always follows the bottom', () => {
    const ref = createRef<HTMLDivElement>()
    const { rerender } = render(T({ messages: [said('a')], scrollRef: ref }))
    geometry(ref.current!, 1000, 200, 100)
    fireEvent.scroll(ref.current!)
    scrollTo.mockClear()
    rerender(T({ messages: [said('a'), said('b')], scrollRef: ref }))
    expect(scrollTo).toHaveBeenCalledTimes(1)
  })

  it('A4: with holdScroll new content follows only when the reader was at the bottom', () => {
    const ref = createRef<HTMLDivElement>()
    let messages = [said('a')]
    const { rerender } = render(T({ messages, scrollRef: ref, holdScroll: true }))
    const box = ref.current!
    const grow = () => {
      messages = [...messages, said(`m${messages.length}`)]
      rerender(T({ messages, scrollRef: ref, holdScroll: true }))
    }

    // At the bottom: the new line is followed.
    geometry(box, 1000, 200, 800)
    fireEvent.scroll(box)
    scrollTo.mockClear()
    grow()
    expect(scrollTo).toHaveBeenCalledTimes(1)

    // Moved up — a jump to a match moves scrollTop synchronously, before any
    // scroll event — so the next line does not pull the reader away.
    geometry(box, 1100, 200, 300)
    scrollTo.mockClear()
    grow()
    expect(scrollTo).not.toHaveBeenCalled()
    fireEvent.scroll(box)
    grow()
    expect(scrollTo).not.toHaveBeenCalled()

    // Scrolling down without reaching the bottom still does not follow.
    geometry(box, 1200, 200, 600)
    fireEvent.scroll(box)
    grow()
    expect(scrollTo).not.toHaveBeenCalled()

    // Back at the bottom: following resumes.
    geometry(box, 1300, 200, 1100)
    fireEvent.scroll(box)
    grow()
    expect(scrollTo).toHaveBeenCalledTimes(1)
  })

  // A F4: a jump to a match in the last screen leaves scrollTop clamped
  // within NEAR_BOTTOM of the end, which read as "at the bottom", and the next
  // line pushed the match off screen. `release()` stops following until the
  // reader scrolls on their own.
  it('A F4: after release() a jump that ends near the bottom does not follow', () => {
    const ref = createRef<HTMLDivElement>()
    const control = createRef<TranscriptScrollControl>()
    let messages = [said('a')]
    const { rerender } = render(T({ messages, scrollRef: ref, scrollControl: control, holdScroll: true }))
    const box = ref.current!
    const grow = () => {
      messages = [...messages, said(`m${messages.length}`)]
      rerender(T({ messages, scrollRef: ref, scrollControl: control, holdScroll: true }))
    }
    geometry(box, 1000, 200, 800)
    fireEvent.scroll(box)
    // The jump lands in the last screen: scrollTop is clamped to the bottom.
    geometry(box, 1000, 200, 790)
    control.current!.release()
    // …and its own scroll event arrives.
    fireEvent.scroll(box)
    scrollTo.mockClear()
    geometry(box, 1100, 200, 790)
    grow()
    expect(scrollTo).not.toHaveBeenCalled()
    // The reader scrolls back to the bottom: following resumes.
    geometry(box, 1100, 200, 900)
    fireEvent.scroll(box)
    grow()
    expect(scrollTo).toHaveBeenCalledTimes(1)
  })

  // R4 T3.2: the dock's inspect jump releases with the search bar closed.
  // The release holds on its own — the next line does not pull the reader
  // back down — until the reader returns to the bottom.
  it('release() holds without the search bar until the reader is back at the bottom', () => {
    const ref = createRef<HTMLDivElement>()
    const control = createRef<TranscriptScrollControl>()
    let messages = [said('a')]
    const { rerender } = render(T({ messages, scrollRef: ref, scrollControl: control }))
    const box = ref.current!
    const grow = () => {
      messages = [...messages, said(`m${messages.length}`)]
      rerender(T({ messages, scrollRef: ref, scrollControl: control }))
    }
    geometry(box, 1000, 200, 800)
    fireEvent.scroll(box)
    // The jump lands mid-transcript and releases.
    geometry(box, 1000, 200, 300)
    control.current!.release()
    fireEvent.scroll(box)
    scrollTo.mockClear()
    geometry(box, 1100, 200, 300)
    grow()
    expect(scrollTo).not.toHaveBeenCalled()
    // Moving down short of the bottom still holds.
    geometry(box, 1100, 200, 500)
    fireEvent.scroll(box)
    grow()
    expect(scrollTo).not.toHaveBeenCalled()
    // Back at the bottom: following resumes, and keeps on.
    geometry(box, 1200, 200, 1000)
    fireEvent.scroll(box)
    grow()
    expect(scrollTo).toHaveBeenCalledTimes(1)
    geometry(box, 1300, 200, 400)
    fireEvent.scroll(box)
    grow()
    expect(scrollTo).toHaveBeenCalledTimes(2)
  })

  it('release() into the last screen holds without the search bar too', () => {
    const ref = createRef<HTMLDivElement>()
    const control = createRef<TranscriptScrollControl>()
    let messages = [said('a')]
    const { rerender } = render(T({ messages, scrollRef: ref, scrollControl: control }))
    const box = ref.current!
    geometry(box, 1000, 200, 800)
    fireEvent.scroll(box)
    geometry(box, 1000, 200, 790)
    control.current!.release()
    fireEvent.scroll(box)
    scrollTo.mockClear()
    geometry(box, 1100, 200, 790)
    messages = [...messages, said('b')]
    rerender(T({ messages, scrollRef: ref, scrollControl: control }))
    expect(scrollTo).not.toHaveBeenCalled()
  })

  // R1-1: a view switch under the open bar mounts the transcript holding;
  // the bar, not the transcript, decides where the reader lands.
  it('mounted while holding, the first follow does not jump', () => {
    render(T({ messages: [said('a')], holdScroll: true }))
    expect(scrollTo).not.toHaveBeenCalled()
    render(T({ messages: [said('a')] }))
    expect(scrollTo).toHaveBeenCalledTimes(1)
  })

  it('A4: an in-flight smooth scroll towards the bottom keeps following', () => {
    const ref = createRef<HTMLDivElement>()
    let messages = [said('a')]
    const { rerender } = render(T({ messages, scrollRef: ref, holdScroll: true }))
    const box = ref.current!
    geometry(box, 1000, 200, 800)
    fireEvent.scroll(box)
    messages = [...messages, said('b')]
    rerender(T({ messages, scrollRef: ref, holdScroll: true }))
    // The smooth scroll animates downwards and has not arrived yet.
    geometry(box, 1400, 200, 900)
    fireEvent.scroll(box)
    scrollTo.mockClear()
    messages = [...messages, said('c')]
    rerender(T({ messages, scrollRef: ref, holdScroll: true }))
    expect(scrollTo).toHaveBeenCalledTimes(1)
  })
})
