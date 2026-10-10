import { describe, it, expect, afterEach } from 'vitest'
import { captureTextAnchor, currentTextTop, pickSnippet } from './deck-anchor'

describe('pickSnippet', () => {
  it('takes up to three words from the caret on, as letters and digits only', () => {
    expect(pickSnippet('alpha beta gamma delta', 0)).toEqual({ snippet: 'alpha beta gamma', index: 0 })
    // markdown syntax ends a run: nothing here is long enough alone
    expect(pickSnippet('see `parse` helper', 0)).toBeNull()
    expect(pickSnippet('see parse helper', 0)).toEqual({ snippet: 'see parse helper', index: 0 })
  })

  it('does not start in the middle of a word the caret is inside', () => {
    expect(pickSnippet('abcdefghij klmnopqrs', 3)).toEqual({ snippet: 'klmnopqrs', index: 11 })
  })

  it('gives up when nothing distinctive is near enough', () => {
    expect(pickSnippet('a b c `d`', 0)).toBeNull()
    expect(pickSnippet(`${' '.repeat(300)}distinctive`, 0)).toBeNull()
  })
})

// jsdom has no layout: a Range's rect is stubbed, as a function of where the range sits.
function stubRects(topOf: (container: Node) => number) {
  Object.defineProperty(Range.prototype, 'getBoundingClientRect', {
    configurable: true,
    value(this: Range) { return { top: topOf(this.startContainer) } as DOMRect },
  })
}
function stubCaret(node: Node, offset: number) {
  const range = document.createRange()
  range.setStart(node, offset)
  Object.defineProperty(document, 'caretRangeFromPoint', { configurable: true, value: () => range })
}
afterEach(() => {
  Reflect.deleteProperty(Range.prototype, 'getBoundingClientRect')
  Reflect.deleteProperty(document, 'caretRangeFromPoint')
  document.body.innerHTML = ''
})

describe('text anchor', () => {
  it('finds the same words again in the swapped DOM and reports where they are now', () => {
    const item = document.createElement('div')
    item.innerHTML = '<div id="plain">intro words here\n\nzebra quartz mango and the rest</div>'
    document.body.append(item)
    const plain = item.querySelector('#plain')!.firstChild as Text
    stubCaret(plain, plain.data.indexOf('zebra'))
    stubRects((c) => (item.querySelector('#plain')?.contains(c) ? 100 : 155))
    const anchor = captureTextAnchor(item, 24, 2)
    expect(anchor).toMatchObject({ snippet: 'zebra quartz mango', nth: 0, top: 100 })

    // the swap: markdown draws the same words over several text nodes
    item.innerHTML = '<p>intro words here</p><p>zebra <strong>quartz</strong> mango and the rest</p>'
    expect(currentTextTop(anchor!)).toBe(155)
  })

  it('tells a repeated phrase by its occurrence', () => {
    const item = document.createElement('div')
    item.innerHTML = '<div>same words here. same words here. same words here.</div>'
    document.body.append(item)
    const text = item.firstElementChild!.firstChild as Text
    stubCaret(text, text.data.indexOf('same words here', 5))
    stubRects(() => 10)
    const anchor = captureTextAnchor(item, 24, 2)!
    expect(anchor.nth).toBe(1)
    item.innerHTML = '<p>same words here.</p><p>same words here.</p><p>same words here.</p>'
    // the second occurrence sits in the second paragraph's text node
    const second = item.children[1].firstChild
    stubRects((c) => (c === second ? 77 : 5))
    expect(currentTextTop(anchor)).toBe(77)
  })

  it('has no anchor when the browser cannot say where the caret is, or the caret is outside the item', () => {
    const item = document.createElement('div')
    item.textContent = 'plenty of distinctive words'
    document.body.append(item)
    expect(captureTextAnchor(item, 24, 2)).toBeNull() // no caretRangeFromPoint in jsdom
    const other = document.createElement('div')
    other.textContent = 'elsewhere entirely'
    document.body.append(other)
    stubCaret(other.firstChild!, 0)
    expect(captureTextAnchor(item, 24, 2)).toBeNull()
  })

  it('cannot find words that are gone', () => {
    const item = document.createElement('div')
    item.textContent = 'plenty of distinctive words'
    document.body.append(item)
    stubCaret(item.firstChild!, 0)
    stubRects(() => 1)
    const anchor = captureTextAnchor(item, 24, 2)!
    item.textContent = 'nothing alike'
    expect(currentTextTop(anchor)).toBeNull()
  })
})
