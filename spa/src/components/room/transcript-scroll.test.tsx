// spa/src/components/room/transcript-scroll.test.tsx — both transcripts hand
// their scroll container out (`scrollRef`), follow new content only when the
// reader is at the bottom (worker pane theme spec §6; with the search bar
// open, R3 plan T3.3, A4), and remember the position per pane.
import { createRef } from 'react'
import type { TranscriptScrollControl } from '../../hooks/useTranscriptScroll'
import { describe, it, expect, vi, afterEach, beforeEach } from 'vitest'
import { render, fireEvent, createEvent } from '@testing-library/react'
import RoomTranscript, { type RoomTranscriptProps } from './RoomTranscript'
import ChatTranscript from '../chat/ChatTranscript'
import type { StreamMessage } from '../../lib/nex/message-types'
import { forgetScrollMemo, readScrollMemo, writeScrollMemo } from '../../lib/nex/transcript-scroll-memory'

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

/**
 * A pointerdown on the box itself at (x, y) in its padding box. jsdom has no
 * layout, so the box gets a clientWidth and the event its offsets by hand.
 */
function pressBox(box: HTMLElement, x: number, y: number) {
  Object.defineProperty(box, 'clientWidth', { configurable: true, value: 300 })
  const ev = createEvent.pointerDown(box)
  Object.defineProperty(ev, 'offsetX', { value: x })
  Object.defineProperty(ev, 'offsetY', { value: y })
  fireEvent(box, ev)
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

  // Worker pane theme spec §6: growth follows only a reader at the bottom,
  // bar open or not. A tab kept alive under visibility:hidden grows while the
  // reader is away; they come back to where they left it.
  it('hidden growth keeps position: scrolled up + new message with the bar closed → no scroll', () => {
    const ref = createRef<HTMLDivElement>()
    const { rerender } = render(T({ messages: [said('a')], scrollRef: ref }))
    geometry(ref.current!, 1000, 200, 100)
    fireEvent.scroll(ref.current!)
    scrollTo.mockClear()
    rerender(T({ messages: [said('a'), said('b')], scrollRef: ref }))
    expect(scrollTo).not.toHaveBeenCalled()
  })

  it('scrolled down but short of the bottom + new message → no scroll', () => {
    const ref = createRef<HTMLDivElement>()
    const { rerender } = render(T({ messages: [said('a')], scrollRef: ref }))
    const box = ref.current!
    geometry(box, 1000, 200, 100)
    fireEvent.scroll(box)
    geometry(box, 1000, 200, 400)
    fireEvent.scroll(box)
    scrollTo.mockClear()
    rerender(T({ messages: [said('a'), said('b')], scrollRef: ref }))
    expect(scrollTo).not.toHaveBeenCalled()
  })

  it('at bottom + new message → follows, and growth alone does not count as leaving', () => {
    const ref = createRef<HTMLDivElement>()
    let messages = [said('a')]
    const { rerender } = render(T({ messages, scrollRef: ref }))
    const box = ref.current!
    geometry(box, 1000, 200, 800)
    fireEvent.scroll(box)
    scrollTo.mockClear()
    // The new line has already made the box taller when the effect runs.
    geometry(box, 1100, 200, 800)
    messages = [...messages, said('b')]
    rerender(T({ messages, scrollRef: ref }))
    expect(scrollTo).toHaveBeenCalledTimes(1)
    expect(scrollTo).toHaveBeenLastCalledWith({ top: 1100, behavior: 'smooth' })
  })

  it("growth during follow()'s own smooth scroll keeps following", () => {
    const ref = createRef<HTMLDivElement>()
    let messages = [said('a')]
    const { rerender } = render(T({ messages, scrollRef: ref }))
    const box = ref.current!
    geometry(box, 1000, 200, 800)
    fireEvent.scroll(box)
    messages = [...messages, said('b')]
    rerender(T({ messages, scrollRef: ref }))
    // The smooth scroll animates downwards and has not arrived yet.
    geometry(box, 1400, 200, 900)
    fireEvent.scroll(box)
    scrollTo.mockClear()
    messages = [...messages, said('c')]
    rerender(T({ messages, scrollRef: ref }))
    expect(scrollTo).toHaveBeenCalledTimes(1)
    // The reader wheels up mid-flight: that ends it.
    geometry(box, 1500, 200, 700)
    fireEvent.scroll(box)
    geometry(box, 1500, 200, 750)
    fireEvent.scroll(box)
    messages = [...messages, said('d')]
    rerender(T({ messages, scrollRef: ref }))
    expect(scrollTo).toHaveBeenCalledTimes(1)
  })

  // A3: the reader's own input during follow()'s smooth scroll ends it, so
  // stopping short of the bottom on the way down is not "the box catching up".
  it.each([
    ['wheel', (box: HTMLElement) => fireEvent.wheel(box, { deltaY: 40 })],
    ['touchstart', (box: HTMLElement) => fireEvent.touchStart(box)],
    ['touchmove', (box: HTMLElement) => fireEvent.touchMove(box)],
    // clientWidth / clientHeight exclude the scrollbar: past them is the gutter.
    ['pointerdown on the vertical scrollbar', (box: HTMLElement) => pressBox(box, 305, 50)],
    ['pointerdown on the horizontal scrollbar', (box: HTMLElement) => pressBox(box, 50, 205)],
    ['PageDown', (box: HTMLElement) => fireEvent.keyDown(box, { key: 'PageDown' })],
    ['ArrowDown', (box: HTMLElement) => fireEvent.keyDown(box, { key: 'ArrowDown' })],
    ['Space', (box: HTMLElement) => fireEvent.keyDown(box, { key: ' ' })],
  ])('A3: %s during follow()\'s smooth scroll, then stopping short of the bottom → no follow', (_input, act) => {
    const ref = createRef<HTMLDivElement>()
    let messages = [said('a')]
    const { rerender } = render(T({ messages, scrollRef: ref }))
    const box = ref.current!
    geometry(box, 1000, 200, 800)
    fireEvent.scroll(box)
    messages = [...messages, said('b')]
    rerender(T({ messages, scrollRef: ref }))
    expect(scrollTo).toHaveBeenLastCalledWith({ top: 1000, behavior: 'smooth' })
    // Mid-flight the reader takes over and stops going down short of the end.
    geometry(box, 1400, 200, 850)
    act(box)
    geometry(box, 1400, 200, 900)
    fireEvent.scroll(box)
    scrollTo.mockClear()
    messages = [...messages, said('c')]
    rerender(T({ messages, scrollRef: ref }))
    expect(scrollTo).not.toHaveBeenCalled()
  })

  it('A3: a click on the content or its padding, or a non-scrolling key, mid-flight does not end the smooth follow', () => {
    const ref = createRef<HTMLDivElement>()
    let messages = [said('a')]
    const { rerender, getByText } = render(T({ messages, scrollRef: ref }))
    const box = ref.current!
    geometry(box, 1000, 200, 800)
    fireEvent.scroll(box)
    messages = [...messages, said('b')]
    rerender(T({ messages, scrollRef: ref }))
    fireEvent.pointerDown(getByText('a'))
    // The box's own padding / the gap between messages: target is the box,
    // but inside clientWidth × clientHeight, so not the scrollbar.
    pressBox(box, 120, 150)
    fireEvent.keyDown(box, { key: 'c', metaKey: true })
    geometry(box, 1400, 200, 900)
    fireEvent.scroll(box)
    scrollTo.mockClear()
    messages = [...messages, said('c')]
    rerender(T({ messages, scrollRef: ref }))
    expect(scrollTo).toHaveBeenCalledTimes(1)
  })

  it('A3: after the reader takes over, reaching the bottom follows again', () => {
    const ref = createRef<HTMLDivElement>()
    let messages = [said('a')]
    const { rerender } = render(T({ messages, scrollRef: ref }))
    const box = ref.current!
    geometry(box, 1000, 200, 800)
    fireEvent.scroll(box)
    messages = [...messages, said('b')]
    rerender(T({ messages, scrollRef: ref }))
    fireEvent.wheel(box, { deltaY: 40 })
    geometry(box, 1400, 200, 1200)
    fireEvent.scroll(box)
    scrollTo.mockClear()
    messages = [...messages, said('c')]
    rerender(T({ messages, scrollRef: ref }))
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
    geometry(box, 1300, 200, 1100)
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

  // Closing the search bar is the explicit "back to live" gesture (alpha.463
  // CHANGELOG): whatever a release held, the next line follows the bottom.
  it('a search jump, then closing the bar: the next growth follows', () => {
    const ref = createRef<HTMLDivElement>()
    const control = createRef<TranscriptScrollControl>()
    let messages = [said('a')]
    let hold = true
    const { rerender } = render(T({ messages, scrollRef: ref, scrollControl: control, holdScroll: hold }))
    const box = ref.current!
    const grow = () => {
      messages = [...messages, said(`m${messages.length}`)]
      rerender(T({ messages, scrollRef: ref, scrollControl: control, holdScroll: hold }))
    }
    geometry(box, 1000, 200, 800)
    fireEvent.scroll(box)
    geometry(box, 1000, 200, 300)
    control.current!.release()
    fireEvent.scroll(box)
    scrollTo.mockClear()
    geometry(box, 1100, 200, 300)
    grow()
    expect(scrollTo).not.toHaveBeenCalled()
    // The bar closes.
    hold = false
    rerender(T({ messages, scrollRef: ref, scrollControl: control, holdScroll: hold }))
    geometry(box, 1200, 200, 300)
    grow()
    expect(scrollTo).toHaveBeenCalledTimes(1)
  })

  // Fix round 1, finding 2: closing the bar sets `resume`, but it is not a
  // standing pass — a reader who scrolls up before anything grows cancels it,
  // same as any other move off the bottom.
  it('closing the bar, then scrolling up before growth: resume is cancelled', () => {
    const ref = createRef<HTMLDivElement>()
    const control = createRef<TranscriptScrollControl>()
    let messages = [said('a')]
    let hold = true
    const { rerender } = render(T({ messages, scrollRef: ref, scrollControl: control, holdScroll: hold }))
    const box = ref.current!
    const grow = () => {
      messages = [...messages, said(`m${messages.length}`)]
      rerender(T({ messages, scrollRef: ref, scrollControl: control, holdScroll: hold }))
    }
    geometry(box, 1000, 200, 800)
    fireEvent.scroll(box)
    geometry(box, 1000, 200, 300)
    control.current!.release()
    fireEvent.scroll(box)
    // The bar closes: the next growth would normally follow wherever the reader is.
    hold = false
    rerender(T({ messages, scrollRef: ref, scrollControl: control, holdScroll: hold }))
    // But before anything grows, the reader scrolls further up.
    geometry(box, 1000, 200, 200)
    fireEvent.scroll(box)
    scrollTo.mockClear()
    geometry(box, 1100, 200, 200)
    grow()
    expect(scrollTo).not.toHaveBeenCalled()
  })

  it('an inspect release, then opening and closing the bar: follow resumes', () => {
    const ref = createRef<HTMLDivElement>()
    const control = createRef<TranscriptScrollControl>()
    let messages = [said('a')]
    let hold = false
    const { rerender } = render(T({ messages, scrollRef: ref, scrollControl: control, holdScroll: hold }))
    const box = ref.current!
    const grow = () => {
      messages = [...messages, said(`m${messages.length}`)]
      rerender(T({ messages, scrollRef: ref, scrollControl: control, holdScroll: hold }))
    }
    geometry(box, 1000, 200, 800)
    fireEvent.scroll(box)
    geometry(box, 1000, 200, 300)
    control.current!.release()
    fireEvent.scroll(box)
    scrollTo.mockClear()
    geometry(box, 1100, 200, 300)
    grow()
    expect(scrollTo).not.toHaveBeenCalled()
    // Re-rendering with the bar still closed keeps the hold.
    rerender(T({ messages, scrollRef: ref, scrollControl: control, holdScroll: hold }))
    geometry(box, 1150, 200, 300)
    grow()
    expect(scrollTo).not.toHaveBeenCalled()
    hold = true
    rerender(T({ messages, scrollRef: ref, scrollControl: control, holdScroll: hold }))
    hold = false
    rerender(T({ messages, scrollRef: ref, scrollControl: control, holdScroll: hold }))
    geometry(box, 1200, 200, 300)
    grow()
    expect(scrollTo).toHaveBeenCalledTimes(1)
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

// Worker pane theme spec §6: a non-persisted memo per pane, read back on
// (re)mount. Room ⇄ chat differ in height, so across a view switch only
// "at the bottom" carries over; otherwise the first visible turn does.
describe('transcript scroll memory', () => {
  const PANE = 'pane-scroll-memo'
  const three = [said('a'), said('b'), said('c')]
  const props = (extra: Partial<RoomTranscriptProps> = {}): RoomTranscriptProps => ({
    keyPrefix: 'k', showThinking: false, showEmptyHint: false, messages: three, turnStarts: [0, 1, 2],
    scrollMemoryKey: PANE, ...extra,
  })
  // Each turn is 300px tall; `offset` is how far the box is scrolled.
  let offset = 0
  let rectSpy: ReturnType<typeof vi.spyOn> | null = null
  function layout() {
    rectSpy = vi.spyOn(HTMLElement.prototype, 'getBoundingClientRect').mockImplementation(function (this: HTMLElement) {
      const turn = this.dataset.turnIndex
      if (turn === undefined) return { top: 0, bottom: 200 } as DOMRect
      const top = Number(turn) * 300 - offset
      return { top, bottom: top + 300 } as DOMRect
    })
  }
  afterEach(() => {
    forgetScrollMemo(PANE)
    rectSpy?.mockRestore()
    rectSpy = null
    offset = 0
  })

  it('scrolling writes the memo, with the first turn still on screen', () => {
    layout()
    const ref = createRef<HTMLDivElement>()
    render(<RoomTranscript {...props({ scrollRef: ref })} />)
    offset = 400
    geometry(ref.current!, 1000, 200, 400)
    fireEvent.scroll(ref.current!)
    // Turn 0 ends at -100 (scrolled out); turn 1 is the first one visible.
    expect(readScrollMemo(PANE)).toEqual({ scrollTop: 400, atBottom: false, view: 'room', firstTurn: 1 })
  })

  it.each(views)('%s: remount restores scrollTop from memory', (view, Transcript) => {
    const ref = createRef<HTMLDivElement>()
    const first = render(<Transcript {...props({ scrollRef: ref })} />)
    geometry(ref.current!, 1000, 200, 300)
    fireEvent.scroll(ref.current!)
    first.unmount()
    expect(readScrollMemo(PANE)).toMatchObject({ scrollTop: 300, atBottom: false, view })
    scrollTo.mockClear()
    render(<Transcript {...props()} />)
    expect(scrollTo).toHaveBeenCalledTimes(1)
    expect(scrollTo).toHaveBeenCalledWith({ top: 300, behavior: 'auto' })
  })

  it('a restored reader is not pulled down by the next line', () => {
    writeScrollMemo(PANE, { scrollTop: 300, atBottom: false, view: 'room', firstTurn: 1 })
    const ref = createRef<HTMLDivElement>()
    const { rerender } = render(<RoomTranscript {...props({ scrollRef: ref })} />)
    geometry(ref.current!, 1000, 200, 300)
    fireEvent.scroll(ref.current!)
    scrollTo.mockClear()
    rerender(<RoomTranscript {...props({ scrollRef: ref, messages: [...three, said('d')] })} />)
    expect(scrollTo).not.toHaveBeenCalled()
  })

  // A4 (PR #1514 review): the memo's scrollTop can exceed the remounted
  // box's max (less content now); the browser clamps the restore to the
  // bottom, and a reader left at the bottom follows the next line.
  describe('A4: a restore the browser clamps to the bottom', () => {
    /** An instant scrollTo that lands clamped, like the browser's, synchronously. */
    function clampingScrollTo(max: () => number) {
      scrollTo.mockImplementation(function (this: HTMLElement, o: ScrollToOptions) {
        const top = Math.max(0, Math.min(o.top ?? 0, max()))
        Object.defineProperty(this, 'scrollTop', { configurable: true, writable: true, value: top })
      })
    }
    afterEach(() => { scrollTo.mockReset() })

    it.each([
      ['its scroll event arrives before the growth', true],
      ['the growth lands before its scroll event', false],
    ])('memo past the new max, %s → the next growth follows', (_order, scrollFirst) => {
      writeScrollMemo(PANE, { scrollTop: 2000, atBottom: false, view: 'room', firstTurn: 2 })
      let height = 1000
      const client = 200
      // Geometry the box has at mount, before the first follow runs.
      const sh = vi.spyOn(HTMLElement.prototype, 'scrollHeight', 'get').mockImplementation(() => height)
      const ch = vi.spyOn(HTMLElement.prototype, 'clientHeight', 'get').mockImplementation(() => client)
      clampingScrollTo(() => height - client)
      try {
        const ref = createRef<HTMLDivElement>()
        const { rerender } = render(<RoomTranscript {...props({ scrollRef: ref })} />)
        expect(scrollTo).toHaveBeenCalledWith({ top: 2000, behavior: 'auto' })
        expect(ref.current!.scrollTop).toBe(800)
        if (scrollFirst) fireEvent.scroll(ref.current!)
        scrollTo.mockClear()
        height = 1100
        rerender(<RoomTranscript {...props({ scrollRef: ref, messages: [...three, said('d')] })} />)
        expect(scrollTo).toHaveBeenCalledTimes(1)
        expect(scrollTo).toHaveBeenLastCalledWith({ top: 1100, behavior: 'smooth' })
      } finally {
        sh.mockRestore()
        ch.mockRestore()
      }
    })

    it('an unclamped restore into the last screen keeps the memo\'s "not at the bottom" (a released jump)', () => {
      writeScrollMemo(PANE, { scrollTop: 790, atBottom: false, view: 'room', firstTurn: 2 })
      let height = 1000
      const client = 200
      const sh = vi.spyOn(HTMLElement.prototype, 'scrollHeight', 'get').mockImplementation(() => height)
      const ch = vi.spyOn(HTMLElement.prototype, 'clientHeight', 'get').mockImplementation(() => client)
      clampingScrollTo(() => height - client)
      try {
        const ref = createRef<HTMLDivElement>()
        const { rerender } = render(<RoomTranscript {...props({ scrollRef: ref })} />)
        expect(ref.current!.scrollTop).toBe(790)
        scrollTo.mockClear()
        height = 1100
        rerender(<RoomTranscript {...props({ scrollRef: ref, messages: [...three, said('d')] })} />)
        expect(scrollTo).not.toHaveBeenCalled()
      } finally {
        sh.mockRestore()
        ch.mockRestore()
      }
    })

    it('memo past the new max where everything fits (no scroll event ever) → the next growth follows', () => {
      writeScrollMemo(PANE, { scrollTop: 2000, atBottom: false, view: 'room', firstTurn: 2 })
      let height = 150
      const client = 200
      const sh = vi.spyOn(HTMLElement.prototype, 'scrollHeight', 'get').mockImplementation(() => height)
      const ch = vi.spyOn(HTMLElement.prototype, 'clientHeight', 'get').mockImplementation(() => client)
      clampingScrollTo(() => Math.max(0, height - client))
      try {
        const ref = createRef<HTMLDivElement>()
        const { rerender } = render(<RoomTranscript {...props({ scrollRef: ref })} />)
        expect(ref.current!.scrollTop).toBe(0)
        scrollTo.mockClear()
        height = 260
        rerender(<RoomTranscript {...props({ scrollRef: ref, messages: [...three, said('d')] })} />)
        expect(scrollTo).toHaveBeenCalledTimes(1)
      } finally {
        sh.mockRestore()
        ch.mockRestore()
      }
    })
  })

  it('remount at bottom → jumps to bottom', () => {
    const ref = createRef<HTMLDivElement>()
    const first = render(<RoomTranscript {...props({ scrollRef: ref })} />)
    geometry(ref.current!, 1000, 200, 800)
    fireEvent.scroll(ref.current!)
    first.unmount()
    expect(readScrollMemo(PANE)?.atBottom).toBe(true)
    scrollTo.mockClear()
    render(<RoomTranscript {...props()} />)
    expect(scrollTo).toHaveBeenCalledTimes(1)
    expect(scrollTo).toHaveBeenCalledWith(expect.objectContaining({ behavior: 'auto' }))
    expect(scrollTo).not.toHaveBeenCalledWith(expect.objectContaining({ top: 800 }))
  })

  it('view switch honours only atBottom; otherwise it scrolls the remembered first turn into view', () => {
    layout()
    const ref = createRef<HTMLDivElement>()
    const room = render(<RoomTranscript {...props({ scrollRef: ref })} />)
    offset = 400
    geometry(ref.current!, 1000, 200, 400)
    fireEvent.scroll(ref.current!)
    room.unmount()
    expect(readScrollMemo(PANE)?.firstTurn).toBe(1)
    scrollTo.mockClear()
    // Chat mounts at the top: turn 1 starts 300px down its box.
    offset = 0
    render(<ChatTranscript {...props()} />)
    expect(scrollTo).toHaveBeenCalledTimes(1)
    expect(scrollTo).toHaveBeenCalledWith({ top: 300, behavior: 'auto' })
  })

  it('view switch at the bottom → jumps to bottom', () => {
    writeScrollMemo(PANE, { scrollTop: 800, atBottom: true, view: 'room', firstTurn: 2 })
    render(<ChatTranscript {...props()} />)
    expect(scrollTo).toHaveBeenCalledTimes(1)
    expect(scrollTo).not.toHaveBeenCalledWith(expect.objectContaining({ top: 800 }))
  })

  it('the search bar placement wins over restore on mount', () => {
    writeScrollMemo(PANE, { scrollTop: 300, atBottom: false, view: 'room', firstTurn: 1 })
    render(<RoomTranscript {...props({ holdScroll: true })} />)
    expect(scrollTo).not.toHaveBeenCalled()
  })

  // Fix round 1, finding 3: `follow()`'s own jump never gets a scroll event
  // of its own to observe (jsdom, and — mid-flight — a real browser too), so
  // it must write the memo itself. Otherwise an immediate remount right
  // after a resumed catch-up would read back the stale, off-bottom memo the
  // reader had before the bar closed, instead of landing at the bottom.
  it("follow()'s own resume jump writes the memo, so an immediate remount lands at the bottom", () => {
    const ref = createRef<HTMLDivElement>()
    const control = createRef<TranscriptScrollControl>()
    let messages = three
    let hold = true
    const { rerender, unmount } = render(
      <RoomTranscript {...props({ scrollRef: ref, scrollControl: control, holdScroll: hold })} />,
    )
    const box = ref.current!
    geometry(box, 1000, 200, 300)
    fireEvent.scroll(box)
    expect(readScrollMemo(PANE)).toMatchObject({ scrollTop: 300, atBottom: false })
    // The bar closes: the next growth resumes, and follow() jumps to the bottom on its own.
    hold = false
    rerender(<RoomTranscript {...props({ scrollRef: ref, scrollControl: control, holdScroll: hold })} />)
    geometry(box, 1100, 200, 300)
    messages = [...three, said('d')]
    rerender(<RoomTranscript {...props({ scrollRef: ref, scrollControl: control, holdScroll: hold, messages })} />)
    expect(scrollTo).toHaveBeenCalled()
    // Nothing else observes a position between the jump and the remount.
    unmount()
    scrollTo.mockClear()
    render(<RoomTranscript {...props({ messages })} />)
    expect(scrollTo).toHaveBeenCalledTimes(1)
    expect(scrollTo).not.toHaveBeenCalledWith(expect.objectContaining({ top: 300 }))
  })

  it('without a scrollMemoryKey nothing is remembered or restored', () => {
    writeScrollMemo(PANE, { scrollTop: 300, atBottom: false, view: 'room', firstTurn: 1 })
    const ref = createRef<HTMLDivElement>()
    render(<RoomTranscript {...props({ scrollRef: ref, scrollMemoryKey: undefined })} />)
    expect(scrollTo).not.toHaveBeenCalledWith({ top: 300, behavior: 'auto' })
    geometry(ref.current!, 1000, 200, 500)
    fireEvent.scroll(ref.current!)
    expect(readScrollMemo(PANE)?.scrollTop).toBe(300)
  })
})

/** jsdom has no layout: the prelude wrapper is 100px per [data-row] child, everything else 0. */
let offsetHeightDesc: PropertyDescriptor | undefined
beforeEach(() => {
  offsetHeightDesc = Object.getOwnPropertyDescriptor(HTMLElement.prototype, 'offsetHeight')
  Object.defineProperty(HTMLElement.prototype, 'offsetHeight', {
    configurable: true,
    get(this: HTMLElement) { return this.dataset?.testid === 'prelude-anchor' ? this.querySelectorAll('[data-row]').length * 100 : 0 },
  })
})
afterEach(() => { if (offsetHeightDesc) Object.defineProperty(HTMLElement.prototype, 'offsetHeight', offsetHeightDesc) })

const rows = (n: number) => <div>{Array.from({ length: n }, (_, i) => <div key={i} data-row />)}</div>

/** Like `geometry`, but scrollHeight is real-ish: `base` of content plus the prelude anchor's own height. */
function liveGeometry(box: HTMLElement, base: { v: number }, clientHeight: number, scrollTop: number) {
  Object.defineProperty(box, 'scrollHeight', {
    configurable: true,
    get() { return base.v + (box.querySelector<HTMLElement>('[data-testid="prelude-anchor"]')?.offsetHeight ?? 0) },
  })
  Object.defineProperty(box, 'clientHeight', { configurable: true, value: clientHeight })
  Object.defineProperty(box, 'scrollTop', { configurable: true, writable: true, value: scrollTop })
}

describe.each(views)('%s: prepending the prelude keeps the reader in place (Review Focus 4)', (_name, View) => {
  const props = { messages: [said('a')], keyPrefix: 'k', showThinking: false, showEmptyHint: false } as RoomTranscriptProps
  const setup = (top: number, anchorRows = 1, extra: Partial<RoomTranscriptProps> = {}) => {
    const base = { v: 1000 - anchorRows * 100 }
    const out = render(<View {...props} {...extra} prelude={rows(anchorRows)} preludeVersion="1:ok" />)
    const box = out.container.firstChild as HTMLElement
    liveGeometry(box, base, 400, top)
    fireEvent.scroll(box)
    scrollTo.mockClear()
    return { ...out, box, base }
  }

  it('mid-transcript: what is on screen does not move', () => {
    const { rerender, box } = setup(300)
    rerender(<View {...props} prelude={rows(7)} preludeVersion="2:ok" />)
    expect(box.scrollTop).toBe(900)
    expect(scrollTo).not.toHaveBeenCalled()
  })

  it('at the bottom: pinned to the end, and a live message still follows', () => {
    const { rerender, box, base } = setup(600)
    rerender(<View {...props} prelude={rows(7)} preludeVersion="2:ok" />)
    expect(box.scrollTop).toBe(box.scrollHeight)
    expect(box.scrollHeight).toBe(1600)
    base.v += 50
    rerender(<View {...props} messages={[said('a'), said('b')]} prelude={rows(7)} preludeVersion="2:ok" />)
    expect(scrollTo).toHaveBeenLastCalledWith({ top: 1650, behavior: 'smooth' })
  })

  it('a smooth follow still travelling is not stranded by the prepend: pinned to the bottom', () => {
    const { rerender, box, base } = setup(600)
    base.v += 50
    rerender(<View {...props} messages={[said('a'), said('b')]} prelude={rows(1)} preludeVersion="1:ok" />)
    expect(scrollTo).toHaveBeenCalledTimes(1)
    rerender(<View {...props} messages={[said('a'), said('b')]} prelude={rows(7)} preludeVersion="2:ok" />)
    expect(box.scrollTop).toBe(1650)
  })

  it('a live message in the same commit is not counted as growth above, and does not scroll a reader away from the middle', () => {
    const { rerender, box, base } = setup(300)
    base.v += 50
    rerender(<View {...props} messages={[said('a'), said('b')]} prelude={rows(7)} preludeVersion="2:ok" />)
    expect(box.scrollTop).toBe(900)
    expect(scrollTo).not.toHaveBeenCalled()
  })

  it('no version change, no correction', () => {
    const { rerender, box } = setup(300)
    rerender(<View {...props} prelude={rows(3)} preludeVersion="1:ok" />)
    expect(box.scrollTop).toBe(300)
  })

  it('a status-only change that shrinks the section (loading row gone) shifts up by that much', () => {
    const out = render(<View {...props} prelude={rows(7)} preludeVersion="2:loading" />)
    const box = out.container.firstChild as HTMLElement
    liveGeometry(box, { v: 2000 }, 400, 900)
    fireEvent.scroll(box)
    out.rerender(<View {...props} prelude={rows(1)} preludeVersion="2:ok" />)
    expect(box.scrollTop).toBe(300)
  })

  it('before the first placement there is nothing to keep', () => {
    delete (Element.prototype as { scrollTo?: unknown }).scrollTo
    const { rerender, box } = setup(300)
    rerender(<View {...props} prelude={rows(7)} preludeVersion="2:ok" />)
    expect(box.scrollTop).toBe(300)
  })

  it('a released reader keeps their place and is not pulled to the bottom by later growth', () => {
    const control = createRef<TranscriptScrollControl>()
    const { rerender, box, base } = setup(300, 1, { scrollControl: control })
    control.current!.release()
    rerender(<View {...props} scrollControl={control} prelude={rows(7)} preludeVersion="2:ok" />)
    expect(box.scrollTop).toBe(900)
    base.v += 50
    rerender(<View {...props} scrollControl={control} messages={[said('a'), said('b')]} prelude={rows(7)} preludeVersion="2:ok" />)
    expect(box.scrollTop).toBe(900)
    expect(scrollTo).not.toHaveBeenCalled()
  })

  it('the box opts out of the browser’s own scroll anchoring only when it has a prelude', () => {
    const withPrelude = render(<View {...props} prelude={rows(1)} preludeVersion="1:ok" />)
    expect((withPrelude.container.firstChild as HTMLElement).className).toContain('[overflow-anchor:none]')
    withPrelude.unmount()
    const without = render(<View {...props} />)
    expect((without.container.firstChild as HTMLElement).className).not.toContain('overflow-anchor')
  })
})
