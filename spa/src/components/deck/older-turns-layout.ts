// Test helper: a fake layout for the scrolling boxes of the deck and the chat. jsdom lays nothing out, so the box gets a
// clientHeight, every [data-turn-index] a fixed height, a clamping scrollTop and the rects that follow from them.
import { vi } from 'vitest'

export const layout = { turnH: 300, client: 600 }
const tops = new WeakMap<HTMLElement, number>()
const isBox = (el: HTMLElement) => el.dataset.testid === 'deck-scroll' || el.dataset.testid === 'chat-scroll'
const turnsOf = (box: HTMLElement) => Array.from(box.querySelectorAll<HTMLElement>('[data-turn-index]'))
// The status lines (paging / start of the conversation) are 28 px tall when they take part in the flow; a `sticky` or `absolute`
// one (an overlay) takes none, as in the browser.
const flowNotes = (box: HTMLElement) => Array.from(box.querySelectorAll<HTMLElement>('[data-testid$="paging"],[data-testid$="history-start"]')).filter((n) => !n.closest('.sticky,.absolute')).length * 28
const contentH = (box: HTMLElement) => turnsOf(box).length * layout.turnH + flowNotes(box)
const maxTop = (box: HTMLElement) => Math.max(0, contentH(box) - layout.client)

export function installFakeLayout(): void {
  const proto = HTMLElement.prototype
  vi.spyOn(proto, 'scrollHeight', 'get').mockImplementation(function (this: HTMLElement) { return isBox(this) ? contentH(this) : 0 })
  vi.spyOn(proto, 'clientHeight', 'get').mockImplementation(function (this: HTMLElement) { return isBox(this) ? layout.client : 0 })
  Object.defineProperty(proto, 'scrollTop', {
    configurable: true,
    get(this: HTMLElement) { return tops.get(this) ?? 0 },
    set(this: HTMLElement, v: number) { tops.set(this, isBox(this) ? Math.max(0, Math.min(v, maxTop(this))) : v) },
  })
  ;(proto as unknown as { scrollTo: unknown }).scrollTo = function (this: HTMLElement, o: { top: number }) { this.scrollTop = o.top }
  vi.spyOn(proto, 'getBoundingClientRect').mockImplementation(function (this: HTMLElement) {
    const r = (top: number, h: number) => ({ top, bottom: top + h, left: 0, right: 100, width: 100, height: h, x: 0, y: top, toJSON: () => ({}) }) as DOMRect
    if (isBox(this)) return r(0, layout.client)
    const box = this.closest<HTMLElement>('[data-testid="deck-scroll"],[data-testid="chat-scroll"]')
    if (box && this.dataset.turnIndex !== undefined) return r(flowNotes(box) + turnsOf(box).indexOf(this) * layout.turnH - box.scrollTop, layout.turnH)
    return r(0, 0)
  })
}

export function uninstallFakeLayout(): void {
  delete (HTMLElement.prototype as unknown as Record<string, unknown>).scrollTop
  delete (HTMLElement.prototype as unknown as Record<string, unknown>).scrollTo
  vi.restoreAllMocks()
}

/** The top of turn `index` relative to the box's top (what the reader sees), null when it is not drawn. */
export function turnTop(box: HTMLElement, index: number): number | null {
  const el = box.querySelector<HTMLElement>(`[data-turn-index="${index}"]`)
  return el ? el.getBoundingClientRect().top - box.getBoundingClientRect().top : null
}
