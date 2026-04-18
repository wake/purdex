import type { LinkMatcher } from '../types'

// 允許 URL 字元集（RFC 3986 + 常見實務），結尾剝除標點
// 已知限制：不處理括號平衡，Wikipedia 風格 URL 結尾的 `)` 會被誤剝
const URL_RE = /https?:\/\/[^\s"'<>`]+/g
const TRAILING_PUNCT = /[.,;:!?)\]}>]+$/

export const urlMatcher: LinkMatcher = {
  id: 'builtin:url',
  type: 'url',
  provide(line) {
    const results: Array<{ text: string; range: { startCol: number; endCol: number } }> = []
    for (const m of line.matchAll(URL_RE)) {
      let text = m[0]
      const trailing = text.match(TRAILING_PUNCT)
      if (trailing) text = text.slice(0, -trailing[0].length)
      const startCol = m.index!
      results.push({ text, range: { startCol, endCol: startCol + text.length } })
    }
    return results
  },
}
