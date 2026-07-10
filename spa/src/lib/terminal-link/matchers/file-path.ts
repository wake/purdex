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
// The `+` build-metadata ambiguity (is `.tar.gz` after `+075a408` build
// metadata or a real extension?) is resolved by a single robust signal: a real
// file ends in an ALPHABETIC extension (txt, gz, log, md, ts, …), whereas IP
// octets, version numbers, and SemVer build-metadata identifiers (hashes like
// 075a408 / 5114f85) do not. So a purely alphabetic final segment means "real
// file → keep". Otherwise we strip any build metadata (everything from the
// first `+`) and reject only when the remaining core is a pure version.
//
// keep:   foo.d.ts · v1.2.3.tar.gz · morphy.pre-edit.SOUL.md · data.2024-01.json
//         pkg-0.0.0+075a408.tar.gz · v1.0.0+build123.txt · report.2024+01.log
// reject: 192.168.1.1 · 1.2.3 · v1.2.3-beta · 1.2.3-rc.1 · report.2024-01 ·
//         bar.123 · 1.0.0+abc · 1.0.0+build-123 · 1.0.0+exp.sha.5114f85
// Note: a sole numeric-hyphen extension with no real (alphabetic) ext after it —
// e.g. "report.2024-01", "build.123-rc", "bar.123" — is rejected (trade-off:
// indistinguishable from a version/date; these were never links pre-change).
function allExtensionsVersionLike(path: string): boolean {
  // Extract the filename from any preceding path segments
  const name = path.split('/').pop() ?? path
  const parts = name.split('.')
  if (parts.length < 2) return false
  // A real trailing extension (pure letters) → treat as a real file, keep it.
  if (/^[A-Za-z]+$/.test(parts[parts.length - 1])) return false
  // Otherwise strip SemVer build metadata and require the core to be a pure
  // version (so "1.0.0+exp.sha.5114f85" rejects but any alpha-ext file above
  // has already returned).
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
