// Chinese text only gets the 1px spacing (c9d9c7-52 follow-up): the splitter, the rehype step over real markdown, the plain-text component,
// and the places the deck and the chat use them.
import { describe, it, expect, afterEach } from 'vitest'
import { cleanup, render, screen } from '@testing-library/react'
import { hasCjk, splitCjk } from './cjk'
import RoomProse, { RoomProseLight } from '../../components/room/RoomProse'
import { CjkText } from '../../components/deck/CjkText'
import { UserBlock } from '../../components/deck/UserBlock'
import { UserBubble } from '../../components/deck/ChatBubbles'
import type { UserItem } from './types'

afterEach(cleanup)
const user = (over: Partial<UserItem> = {}) => ({ type: 'user', id: 'u', at: 1, index: 0, text: 'hi', source: 'user', ...over }) as UserItem
const cjkOf = (el: Element) => [...el.querySelectorAll('.cjk')].map((n) => n.textContent)

describe('splitCjk', () => {
  it('Chinese runs are cjk, Latin letters, digits and spaces are not', () => {
    expect(splitCjk('Hello 世界 foo')).toEqual([{ text: 'Hello ', cjk: false }, { text: '世界', cjk: true }, { text: ' foo', cjk: false }])
    expect(splitCjk('abc')).toEqual([{ text: 'abc', cjk: false }])
    expect(splitCjk('')).toEqual([])
    expect(splitCjk('你好').map((p) => p.cjk)).toEqual([true])
  })
  it('full-width punctuation, kana, bopomofo and astral ideographs are CJK; ASCII punctuation is not', () => {
    expect(splitCjk('好，嗎？').map((p) => p.text)).toEqual(['好，嗎？'])
    expect(splitCjk('ひらがなカタカナ')).toEqual([{ text: 'ひらがなカタカナ', cjk: true }])
    expect(splitCjk('a㈱b㎏')).toEqual([{ text: 'a', cjk: false }, { text: '㈱', cjk: true }, { text: 'b', cjk: false }, { text: '㎏', cjk: true }])
    expect(splitCjk('ㄅㄆㄇ')).toEqual([{ text: 'ㄅㄆㄇ', cjk: true }])
    expect(splitCjk('𠀀字')).toEqual([{ text: '𠀀字', cjk: true }]) // U+20000, a surrogate pair
    expect(splitCjk('a, b. (c)')).toEqual([{ text: 'a, b. (c)', cjk: false }])
    expect(splitCjk('😀 世')).toEqual([{ text: '😀 ', cjk: false }, { text: '世', cjk: true }])
  })
  it('the parts always add back up to the text', () => {
    for (const s of ['中a文b', 'a中', '中a', '  中  ', 'x\n世界\ny', '全形ＡＢＣ１２３']) expect(splitCjk(s).map((p) => p.text).join('')).toBe(s)
  })
  it('hasCjk', () => { expect(hasCjk('abc')).toBe(false); expect(hasCjk('a世')).toBe(true) })
})

describe('the agent\'s markdown (rehype step)', () => {
  const md = '中文 and English 混排。\n\n- 清單 item\n- `行內 code 中文`\n\n```js\nconst s = "程式碼 中文"\n```\n\n**粗體中文** 與 [連結文字](http://x.test)'
  it('wraps Chinese in prose, headings, lists, emphasis and links', () => {
    const { container } = render(<RoomProse content={md} cjk />)
    const wrapped = cjkOf(container)
    expect(wrapped).toContain('中文')
    expect(wrapped).toContain('混排。')
    expect(wrapped).toContain('清單')
    expect(wrapped).toContain('粗體中文')
    expect(wrapped).toContain('連結文字')
  })
  it('does not touch Latin text, inline code, or a code block', () => {
    const { container } = render(<RoomProse content={md} cjk />)
    expect(cjkOf(container).some((t) => /[A-Za-z]/.test(t ?? ''))).toBe(false)
    expect(container.querySelector('pre')!.querySelector('.cjk')).toBeNull()
    expect(container.querySelector('p code, li code')!.querySelector('.cjk')).toBeNull()
  })
  it('adds no text: the rendered textContent (and so the search index) is the same with and without', () => {
    const a = render(<RoomProse content={md} cjk />).container.textContent
    cleanup()
    const b = render(<RoomProse content={md} />).container.textContent
    expect(a).toBe(b)
  })
  it('only when asked: the room and the execution pane (no cjk prop) get no spans', () => {
    const { container } = render(<RoomProse content={md} />)
    expect(container.querySelector('.cjk')).toBeNull()
  })
  it('a kept (cached) tree carries the spans, and is not shared with an un-spanned render of the same key', () => {
    const first = render(<RoomProse content="快取 test" cacheKey="k1" cjk />)
    expect(cjkOf(first.container)).toEqual(['快取'])
    first.unmount()
    const again = render(<RoomProse content="快取 test" cacheKey="k1" cjk />)
    expect(cjkOf(again.container)).toEqual(['快取'])
    again.unmount()
    const plain = render(<RoomProse content="快取 test" cacheKey="k1" />)
    expect(plain.container.querySelector('.cjk')).toBeNull()
  })
  it('the far-off plain-text form spaces Chinese too', () => {
    const { container } = render(<RoomProseLight content={'Mixed 混排 text'} cjk />)
    expect(cjkOf(container)).toEqual(['混排'])
    expect(container.textContent).toBe('Mixed 混排 text')
  })
})

describe('the person\'s and peers\' plain text', () => {
  it('CjkText keeps the string and wraps the Chinese', () => {
    const { container } = render(<CjkText text="Fix 這個 bug 好嗎" />)
    expect(container.textContent).toBe('Fix 這個 bug 好嗎')
    expect(cjkOf(container)).toEqual(['這個', '好嗎'])
  })
  it('the deck block: your words, a peer\'s words; not a bash input (code)', () => {
    render(<UserBlock item={user({ text: 'Please 修正 it' })} />)
    expect(cjkOf(screen.getByTestId('deck-user'))).toEqual(['修正'])
    cleanup()
    render(<UserBlock item={user({ source: 'peer', from: { kind: 'peer', name: 'a/b' }, text: '收到 ok' })} />)
    expect(cjkOf(screen.getByTestId('deck-user'))).toContain('收到')
    cleanup()
    render(<UserBlock item={user({ source: 'bash', text: 'echo 中文' })} />)
    expect(screen.getByTestId('deck-user').textContent).toContain('! echo 中文')
    expect(screen.getByTestId('deck-user').querySelector('.cjk')).toBeNull()
  })
  it('the chat bubble', () => {
    render(<UserBubble item={user({ text: '你好 world' })} />)
    expect(cjkOf(screen.getByTestId('chat-user-bubble'))).toEqual(['你好'])
    expect(screen.getByTestId('chat-user-bubble').textContent).toBe('你好 world')
  })
})
