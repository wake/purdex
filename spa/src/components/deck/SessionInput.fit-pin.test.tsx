// v3 point 3 under the real input: a reader at the bottom stays there while the textarea grows line by line. Measuring the text
// (`fitHeight`) sets the height to `auto`, which for a moment makes the footer SHORTER, the scrolling box taller, and the browser
// clamps the box's scrollTop to the new, smaller maximum — at the old size the box then ends up scrolled up by that much, its
// scroll event reads "left the bottom" (a growth of more than NEAR_BOTTOM), and the resize observer's pin is ignored. Chrome's
// scroll anchoring used to hide this; the deck opts out of it (`overflow-anchor: none`, #2586). So the footer must not get
// shorter while it is measured. jsdom has no layout: the wrapper's height is faked and what is asserted is the hold.
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { cleanup, fireEvent, render, screen } from '@testing-library/react'
import { SessionInput } from './SessionInput'
import { clearAllDrafts } from '../../lib/conversations/draft-memory'
import { clearAllSendQueues } from '../../lib/conversations/send-queue'

vi.mock('../../lib/host-api', () => ({ pinnedHostFetch: vi.fn() }))

const box = () => screen.getByRole('textbox') as HTMLTextAreaElement

beforeEach(() => { cleanup() })
afterEach(() => { cleanup(); clearAllDrafts(); clearAllSendQueues(); vi.restoreAllMocks() })

describe('SessionInput fitHeight does not shrink the footer while measuring', () => {
  it("holds the textarea's wrapper at its current height while the height is `auto`, and lets go after", () => {
    render(<SessionInput paneKey="p1" hostId="h" sessionId="s" capabilities={{ send: 'prompt' }} onSwitchToTerminal={() => {}} />)
    const wrapper = box().parentElement as HTMLElement
    vi.spyOn(HTMLElement.prototype, 'offsetHeight', 'get').mockImplementation(function (this: HTMLElement) { return this === wrapper ? 86 : 0 })
    const seen: Array<{ height: string; hold: string }> = []
    vi.spyOn(HTMLTextAreaElement.prototype, 'scrollHeight', 'get').mockImplementation(function (this: HTMLTextAreaElement) {
      seen.push({ height: this.style.height, hold: wrapper.style.minHeight })
      return 120
    })
    fireEvent.change(box(), { target: { value: 'a\nb\nc' } })
    const measuring = seen.filter((s) => s.height === 'auto')
    expect(measuring.length).toBeGreaterThan(0)
    for (const s of measuring) expect(s.hold).toBe('86px')
    expect(wrapper.style.minHeight).toBe('') // the footer is free to be what the textarea says
  })
})
