import type { LinkMatcher } from '../types'

// URL 偵測分三步（詳見 docs/specs/2026-09-14-terminal-link-unicode-spec.md §3.2）：
//   1. 掃描：從 https?:// 起貪婪吃到 STOP_CHARS 為止（空白、引號、全形標點）
//   2. 非 ASCII 截斷（option C）：非 ASCII 字元只有緊接在「ASCII 字母或數字」之後
//      才截斷（連同其後全部丟棄）；接在其他任何字元（非 ASCII、或 `/?#=&._-~%+:@`
//      等 ASCII 標點）之後一律視為 URL 內容。不需知道 authority 在哪結束。
//   3. 尾端剝除：ASCII 標點永遠剝；`)` / `]` 只在 URL 內不平衡時才剝；
//      剝完只剩 scheme（`://` 後為空）則不產生連結。
// Option C 的已知取捨：
//   - 犧牲：`https://e.com/?q=abc中文` → `https://e.com/?q=abc`（alnum→CJK 在真
//     URL 內與黏著散文無法區分）
//   - 誤保留：`https://e.com/?然後回報`、`https://…/wiki/臺灣是個島` 整段連結
//     （分隔符後的 CJK 一律視為內容）

// 全形／CJK 標點停止集合。這是 heuristic 不是保證：WHATWG URL parser 接受未編碼
// 的 Unicode 標點（會被 percent-encode），所以它們理論上可出現在貼上的 URL 裡；
// 這組挑的是在終端輸出中壓倒性屬於句子標點的字元。兩個刻意決定：
//   - `・` U+30FB 不在集合內（日文標題常見，如 アラン・チューリング）
//   - `。` U+3002 在集合內，即使它可當 IDN 點號（例子。測試）——終端上未編碼的
//     全形句號出現在 hostname 裡遠比句尾罕見
// U+FF0C ， U+3002 。 U+3001 、 U+FF1B ； U+FF1A ： U+FF01 ！ U+FF1F ？
// U+FF08 （ U+FF09 ） U+FF3B ［ U+FF3D ］ U+300C 「 U+300D 」 U+300E 『 U+300F 』
// U+3010 【 U+3011 】 U+300A 《 U+300B 》 U+3008 〈 U+3009 〉 U+3014 〔 U+3015 〕
// U+FF5B ｛ U+FF5D ｝ U+201C “ U+201D ” U+2018 ‘ U+2019 ’ U+2026 …
// U+FF61 ｡ U+FF62 ｢ U+FF63 ｣ U+FF64 ､
const STOP_CHARS = '\\s"\'<>`，。、；：！？（）［］「」『』【】《》〈〉〔〕｛｝“”‘’…｡｢｣､'
const URL_RE = new RegExp(`https?:\\/\\/[^${STOP_CHARS}]+`, 'gu')

const TAIL_PUNCT = '.,;:!?>}'
const ASCII_ALNUM = /^[A-Za-z0-9]$/

const isNonAscii = (ch: string) => ch.codePointAt(0)! > 0x7f

// Option C：走訪 code point，非 ASCII 字元只有在前一個字元是 ASCII 字母／數字時
// 才截斷（回傳截斷前累積的前綴）；第一個截斷點決定結果。
function cutNonAscii(text: string): string {
  let out = ''
  let prev = ''
  for (const ch of text) {
    if (isNonAscii(ch) && ASCII_ALNUM.test(prev)) return out
    out += ch
    prev = ch
  }
  return out
}

// 尾端剝除：ASCII 標點永遠剝；`)` / `]` 只在數量多於對應開括號時才剝。
// 括號只數一次，之後以 end 索引往回走、最後切一刀，成本線性。
// 全形閉括號不會出現在尾端（掃描階段已擋下），所以這裡只需處理 ASCII。
function stripTail(text: string): string {
  let openParen = 0
  let closeParen = 0
  let openBracket = 0
  let closeBracket = 0
  for (let i = 0; i < text.length; i++) {
    switch (text[i]) {
      case '(':
        openParen++
        break
      case ')':
        closeParen++
        break
      case '[':
        openBracket++
        break
      case ']':
        closeBracket++
        break
    }
  }
  let end = text.length
  while (end > 0) {
    const last = text[end - 1]
    if (TAIL_PUNCT.includes(last)) {
      end--
    } else if (last === ')' && closeParen > openParen) {
      closeParen--
      end--
    } else if (last === ']' && closeBracket > openBracket) {
      closeBracket--
      end--
    } else {
      break
    }
  }
  return end === text.length ? text : text.slice(0, end)
}

const SCHEME_RE = /^https?:\/\//

export const urlMatcher: LinkMatcher = {
  id: 'builtin:url',
  type: 'url',
  provide(line) {
    const results: Array<{ text: string; range: { startCol: number; endCol: number } }> = []
    for (const m of line.matchAll(URL_RE)) {
      const text = stripTail(cutNonAscii(m[0]))
      // scheme-only guard：剝完 `://` 後沒有東西就不產生連結（如 `https://).`）
      if (text.replace(SCHEME_RE, '') === '') continue
      const startCol = m.index!
      results.push({ text, range: { startCol, endCol: startCol + text.length } })
    }
    return results
  },
}
