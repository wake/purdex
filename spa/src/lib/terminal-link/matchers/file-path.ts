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

// Valid SemVer build metadata: dot-separated identifiers of alnum + hyphen
// (e.g. "075a408", "build-123", "exp.sha.5114f85").
const BUILD_METADATA_RE = /^[A-Za-z0-9-]+(?:\.[A-Za-z0-9-]+)*$/

// Returns true if every extension part (parts after the base) is "version-like":
// pure digits, optionally plus a single `-<alnum>` prerelease. (Build metadata
// `+...` is stripped by the caller before this runs, see allExtensionsVersionLike.)
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
// IP / decimal / bare SemVer rather than a real path.
//
// SemVer build metadata (everything after the first `+`) is handled specially:
// per SemVer it is dot-separated alnum/hyphen identifiers, so segments like
// `sha` or `exp` look like real extensions but are not. When the tail after `+`
// is valid build metadata, the name is version-like only if the part BEFORE the
// `+` is itself a pure version — so bare SemVer such as "1.0.0+build-123" or
// "1.0.0+exp.sha.5114f85" is rejected, while a real file merely carrying build
// metadata before a genuine extension (e.g. "pkg-0.0.0+075a408.tar.gz", whose
// `.tar.gz` is a real ext, so its pre-`+` part is not a pure version) stays
// linkable.
// e.g. "v1.2.3-beta"  → true (reject semver)
// e.g. "v1.2.3.tar.gz" → false (tar, gz are letters → keep)
// e.g. "com.wake.custom-css-0.0.0+075a408.tar.gz" → false (pre has non-version
//        segments like `wake` + real `.tar.gz` → keep)
// e.g. "bar.min-2.js"  → false (min-2 not version-like, js is a real ext → keep)
// e.g. "data.2024-01.json" → false (json is a real ext → keep)
// e.g. "morphy.pre-edit.SOUL.md" → false (pre-edit not version-like → keep)
// Note: "bar.123" → true (rotated-log style rejected; trade-off documented).
// Note: a sole numeric-hyphen extension with no real ext after it — e.g.
// "report.2024-01", "build.123-rc" — is likewise rejected (same trade-off:
// indistinguishable from a version/date; these were never links pre-change).
function allExtensionsVersionLike(path: string): boolean {
  // Extract the filename from any preceding path segments
  const name = path.split('/').pop() ?? path
  const plusIdx = name.indexOf('+')
  if (plusIdx >= 0 && BUILD_METADATA_RE.test(name.slice(plusIdx + 1))) {
    return extensionsVersionLike(name.slice(0, plusIdx))
  }
  return extensionsVersionLike(name)
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
