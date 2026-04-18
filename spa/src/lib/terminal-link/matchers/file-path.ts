import type { LinkMatcher } from '../types'

// 絕對路徑 + 末段必須含副檔名（避免誤配 `/usr/local/bin` 或 `/home/user/.config`）
// 允許後接 :line 或 :line:col
// 段落用 `[\w.-]+\/` 確定性拆分（`/` 不在字元類內），避免 ReDoS
const PATH_RE = /(?<![\w/:])(\/(?:[\w.-]+\/)*[\w-]+\.[A-Za-z0-9]+)(?::(\d+)(?::(\d+))?)?/g

export const filePathMatcher: LinkMatcher = {
  id: 'builtin:file-path',
  type: 'file',
  provide(line) {
    const results: Array<{
      text: string
      range: { startCol: number; endCol: number }
      meta?: Record<string, unknown>
    }> = []
    for (const m of line.matchAll(PATH_RE)) {
      // 排除 URL 內的路徑：前方有 http(s):// 且與當前位置之間沒有空白，視為仍在 URL 中
      const before = line.slice(0, m.index!)
      if (/https?:\/\/\S*$/.test(before)) continue

      const path = m[1]
      const lineNum = m[2] ? parseInt(m[2], 10) : undefined
      const colNum = m[3] ? parseInt(m[3], 10) : undefined
      const text = m[0]
      const startCol = m.index!

      const meta: Record<string, unknown> = { path }
      if (lineNum !== undefined) meta.line = lineNum
      if (colNum !== undefined) meta.col = colNum

      results.push({
        text,
        range: { startCol, endCol: startCol + text.length },
        meta,
      })
    }
    return results
  },
}
