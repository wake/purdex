import type { LinkMatcher } from '../types'

// URL 偵測分三步（詳見 docs/specs/2026-09-14-terminal-link-unicode-spec.md §3.2）：
//   1. 掃描：從 https?:// 起貪婪吃到 STOP_CHARS 為止（空白、引號、全形標點）
//   2. 非 ASCII 截斷（option C）：非 ASCII 字元只有接在「非 ASCII / `/?#=&` /
//      前面是非 ASCII 的 `.`」之後才算 URL 內容，否則從該處截斷
//   3. 尾端剝除：ASCII 標點永遠剝；`)` / `]` 只在 URL 內不平衡時才剝
// Option C 的已知犧牲：`https://…/wiki/臺灣是個島` 會整段連結（`/` 後的 CJK
// 一律視為內容，無法分辨後面黏著的散文）。

// 全形／CJK 標點：這些字元不會未編碼出現在 URL 內，可直接當硬邊界。
// U+FF0C ， U+3002 。 U+3001 、 U+FF1B ； U+FF1A ： U+FF01 ！ U+FF1F ？
// U+FF08 （ U+FF09 ） U+300C 「 U+300D 」 U+300E 『 U+300F 』 U+3010 【 U+3011 】
// U+300A 《 U+300B 》 U+3008 〈 U+3009 〉 U+3014 〔 U+3015 〕 U+FF5B ｛ U+FF5D ｝
// U+201C “ U+201D ” U+2018 ‘ U+2019 ’ U+2026 … U+30FB ・
const STOP_CHARS = '\\s"\'<>`，。、；：！？（）「」『』【】《》〈〉〔〕｛｝“”‘’…・'
const URL_RE = new RegExp(`https?:\\/\\/[^${STOP_CHARS}]+`, 'gu')

const CONTENT_PREFIX = '/?#=&'
const TAIL_PUNCT = '.,;:!?>}'

const isNonAscii = (ch: string) => ch.codePointAt(0)! > 0x7f

// Option C：走訪 code point，非 ASCII 字元是否為 URL 內容取決於它前一個字元
//   (a) 前一個也是非 ASCII
//   (b) 前一個是 `/ ? # = &`
//   (c) 前一個是 `.` 且再前一個是非 ASCII（IDN label 邊界，如 例子.測試）
// 否則在該字元前截斷。scheme 是純 ASCII，不需特殊處理。
function cutNonAscii(text: string): string {
  let out = ''
  let prev = ''
  let prevPrev = ''
  for (const ch of text) {
    if (isNonAscii(ch)) {
      const keep =
        (prev !== '' && isNonAscii(prev)) ||
        CONTENT_PREFIX.includes(prev) ||
        (prev === '.' && prevPrev !== '' && isNonAscii(prevPrev))
      if (!keep) return out
    }
    out += ch
    prevPrev = prev
    prev = ch
  }
  return out
}

const count = (text: string, ch: string) => text.split(ch).length - 1

// 尾端剝除：ASCII 標點永遠剝；`)` / `]` 只在數量多於對應開括號時才剝。
// 全形閉括號不會出現在尾端（掃描階段已擋下），所以這裡只需處理 ASCII。
function stripTail(text: string): string {
  for (;;) {
    const last = text.at(-1)
    if (last === undefined) return text
    if (TAIL_PUNCT.includes(last)) {
      text = text.slice(0, -1)
    } else if (last === ')' && count(text, ')') > count(text, '(')) {
      text = text.slice(0, -1)
    } else if (last === ']' && count(text, ']') > count(text, '[')) {
      text = text.slice(0, -1)
    } else {
      return text
    }
  }
}

export const urlMatcher: LinkMatcher = {
  id: 'builtin:url',
  type: 'url',
  provide(line) {
    const results: Array<{ text: string; range: { startCol: number; endCol: number } }> = []
    for (const m of line.matchAll(URL_RE)) {
      const text = stripTail(cutNonAscii(m[0]))
      const startCol = m.index!
      results.push({ text, range: { startCol, endCol: startCol + text.length } })
    }
    return results
  },
}
