import type { LinkMatcher } from '../types'

// 絕對路徑：必須以 `/` 開頭 + 末段 name.ext（支援多重副檔名如 .d.ts / .min.js）
export const ABS_RE = /(?<![\w/:])(\/(?:[\w.-]+\/)*[\w-]+(?:\.[A-Za-z0-9]+)+)(?::(\d+)(?::(\d+))?)?/g

// 相對路徑（含至少一個 `/`）：不能以 `/` 開頭，至少一個中間段 + 末段（支援多重副檔名）
export const REL_RE = /(?<![\w/:])((?:[\w.-]+\/)+[\w-]+(?:\.[A-Za-z0-9]+)+)(?::(\d+)(?::(\d+))?)?/g

// 純檔名：無 `/`；lookbehind 阻擋 word/`/`/`:`/`.` 避免匹配路徑片段或 URL 內段、或次級副檔名（支援多重副檔名）
export const BARE_RE = /(?<![\w/:.])([\w-]+(?:\.[A-Za-z0-9]+)+)(?::(\d+)(?::(\d+))?)?/g

export interface FilePathMatcherConfig {
  id: string
  regex: RegExp
  isEnabled: () => boolean // called on each provide() — hot path, must be cheap
}

type MatchResult = {
  text: string
  range: { startCol: number; endCol: number }
  meta?: Record<string, unknown>
}

// Returns true if every extension part (parts after the first) is purely digits.
// e.g. "192.168.1.1" → true (all exts are digits → reject as IP/version/decimal)
// e.g. "foo.d.ts"    → false (exts include letters → keep)
// e.g. "v1.2.3.tar.gz" → false (tar, gz are letters → keep)
// Note: "bar.123" → true (rotated-log style rejected; trade-off documented).
function allExtensionsDigits(path: string): boolean {
  // Extract the filename from any preceding path segments
  const name = path.split('/').pop() ?? path
  const parts = name.split('.')
  if (parts.length < 2) return false
  // parts[0] = base name; parts[1..] = extensions
  return parts.slice(1).every((ext) => /^\d+$/.test(ext))
}

function runRegex(line: string, re: RegExp): MatchResult[] {
  const results: MatchResult[] = []
  for (const m of line.matchAll(re)) {
    const before = line.slice(0, m.index!)
    // 排除 URL 內的路徑：前方若有 http(s):// 且到此位置之間沒有空白，視為仍在 URL 中
    if (/https?:\/\/\S*$/.test(before)) continue
    const path = m[1]
    // 排除 IP / 版本號 / 小數：所有副檔名段均為純數字時拒絕
    if (allExtensionsDigits(path)) continue
    const lineNum = m[2] ? parseInt(m[2], 10) : undefined
    const colNum = m[3] ? parseInt(m[3], 10) : undefined
    const text = m[0]
    const startCol = m.index!
    const meta: Record<string, unknown> = { path }
    if (lineNum !== undefined) meta.line = lineNum
    if (colNum !== undefined) meta.col = colNum
    results.push({ text, range: { startCol, endCol: startCol + text.length }, meta })
  }
  return results
}

export function createFilePathMatcher(cfg: FilePathMatcherConfig): LinkMatcher {
  return {
    id: cfg.id,
    type: 'file',
    provide(line) {
      if (!cfg.isEnabled()) return []
      return runRegex(line, cfg.regex)
    },
  }
}
