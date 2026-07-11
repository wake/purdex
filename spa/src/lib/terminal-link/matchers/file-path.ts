import type { LinkMatcher } from '../types'

// 絕對路徑：必須以 `/` 開頭 + 末段 name.ext（支援多重副檔名如 .d.ts / .min.js，副檔名段允許內含連字號如 pre-edit 與 `+` build metadata 如 0.0.0+075a408）
export const ABS_RE = /(?<![\w/:~.])(\/(?:[\w.-]+\/)*[\w-]+(?:\.[A-Za-z0-9]+(?:[-+][A-Za-z0-9]+)*)+)(?::(\d+)(?::(\d+))?)?/g

// Tilde 路徑：以 ~/ 開頭 + 末段 name.ext（支援 dotdir 與多重副檔名，副檔名段允許內含連字號與 `+` build metadata）
export const TILDE_RE = /(?<![\w/:~])(~\/(?:[\w.-]+\/)*[\w-]+(?:\.[A-Za-z0-9]+(?:[-+][A-Za-z0-9]+)*)+)(?::(\d+)(?::(\d+))?)?/g

// 相對路徑（含至少一個 `/`）：不能以 `/` 開頭，至少一個中間段 + 末段（支援多重副檔名，副檔名段允許內含連字號與 `+` build metadata）
export const REL_RE = /(?<![\w/:])((?:[\w.-]+\/)+[\w-]+(?:\.[A-Za-z0-9]+(?:[-+][A-Za-z0-9]+)*)+)(?::(\d+)(?::(\d+))?)?/g

// 純檔名：無 `/`；lookbehind 阻擋 word/`/`/`:`/`.` 避免匹配路徑片段或 URL 內段、或次級副檔名（支援多重副檔名，副檔名段允許內含連字號與 `+` build metadata）
export const BARE_RE = /(?<![\w/:.])([\w-]+(?:\.[A-Za-z0-9]+(?:[-+][A-Za-z0-9]+)*)+)(?::(\d+)(?::(\d+))?)?/g

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

// Returns true if every extension part (parts after the base) is "version-like":
// pure digits, optionally plus a single `-<alnum>` prerelease.
// e.g. "192.168.1.1" → base "192", exts 168/1/1 all numeric → true
// e.g. "1.2.3-rc.1"  → 2, 3-rc, 1 all version-like → true
// e.g. "foo.d.ts"    → false (exts include letters → keep)
function extensionsVersionLike(name: string): boolean {
  const parts = name.split('.')
  if (parts.length < 2) return false
  // parts[0] = base name; parts[1..] = extensions
  return parts.slice(1).every((ext) => /^\d+(?:-[A-Za-z0-9]+)?$/.test(ext))
}

// Returns true when the filename should NOT be linkified because it is an
// IP / decimal / bare SemVer (including build metadata) rather than a real path.
//
// SemVer `+` build metadata creates an inherent ambiguity: a trailing dotted
// identifier (`.sha`, `.log`) is syntactically indistinguishable as "build
// metadata identifier" vs "real file extension". We resolve it with a
// deliberate bias — do NOT linkify bare version strings (common terminal
// noise like `1.0.0+exp.sha`) — by stripping everything from the first `+` and
// rejecting when the remaining core is a pure version. Real build artifacts
// carry a package-name stem (e.g. `com.wake.custom-css-0.0.0+075a408.tar.gz`),
// so their core is not a pure version and they stay linkable.
//
// keep:   foo.d.ts · v1.2.3.tar.gz · morphy.pre-edit.SOUL.md · data.2024-01.json
//         com.wake.custom-css-0.0.0+075a408.tar.gz (package stem → not a version)
// reject: 192.168.1.1 · 1.2.3 · v1.2.3-beta · 1.2.3-rc.1 · report.2024-01 · bar.123
//         1.0.0+abc · 1.0.0+build-123 · 1.0.0+exp.sha · 1.0.0+exp.sha.5114f85
// Trade-off (chosen bias): a filename whose stem before `+` is itself a bare
// version — e.g. "v1.0.0+build123.txt", "report.2024+01.log" — is sacrificed
// (not linkified), preferred over linkifying the far more common version noise.
function allExtensionsVersionLike(path: string): boolean {
  // Extract the filename from any preceding path segments
  const name = path.split('/').pop() ?? path
  // Strip SemVer build metadata (from the first `+`); reject a pure-version core.
  const plusIdx = name.indexOf('+')
  return extensionsVersionLike(plusIdx >= 0 ? name.slice(0, plusIdx) : name)
}

function runRegex(line: string, re: RegExp): MatchResult[] {
  const results: MatchResult[] = []
  for (const m of line.matchAll(re)) {
    const before = line.slice(0, m.index!)
    // 排除 URL 內的路徑：前方若有 http(s):// 且到此位置之間沒有空白，視為仍在 URL 中
    if (/https?:\/\/\S*$/.test(before)) continue
    const path = m[1]
    // 排除 IP / 版本號 / 小數：所有副檔名段均為版本樣式（純數字或數字+單一 -prerelease）時拒絕
    if (allExtensionsVersionLike(path)) continue
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
