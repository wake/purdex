import type { LinkMatcher } from '../types'

// 四支 regex 共用的字元類別片段（Unicode，需 `u` flag）。集中定義避免四支各自漂移。
//
// 目錄段與檔名主幹接受任何 Unicode 字母/數字（`\p{L}\p{N}`），讓 CJK 路徑如
// `docs/申請流程/115潛優修正計畫-核定.pdf` 能整段連結。凡是字母出現的地方都同時
// 收 `\p{M}`（combining marks）：macOS `ls` 常輸出 NFD（`が` = `か` + U+3099、
// `é` = `e` + U+0301）；匹配結果不做正規化，原樣交給 opener。副檔名鏈拆成兩種角色：
//   - inner（中間的副檔名段）：Unicode，讓 `docs/報告.最終版.pdf` 整段連結
//   - final（最後一段）：**維持 ASCII**，讓黏在檔名後的 CJK 敘述
//     （`已更新 docs/a.pdf並重新`）自然停在 `pdf`。這是 bias，不是 guarantee：
//     黏上的敘述後面若再接副檔名或 `/`，regex 會合法吃掉整段（見下方 limitations）。
//   引擎會在兩種角色間回溯：`docs/a.pdf然後，` → inner `.pdf然後` 後面接不到
//   final → 回溯 → final `.pdf` → 連結為 `docs/a.pdf`。
//
// Known limitations（刻意接受，勿「修」）：
//   - 敘述直接黏在相對路徑前而無分隔（`已更新docs/a.pdf`）會被吃進第一段，
//     與 ASCII 的 `seedocs/a.pdf` 完全相同——沒有邊界可找。
//   - 以檔名樣式結尾的 CJK 句子會被 BARE_RE 整句連結（`請見附件.pdf` → 一個連結），
//     與 ASCII 的 `seeattachment.pdf` 同樣歧義；點擊後走既有 stat → not-found popup。
//   - 黏在 ASCII 副檔名後的敘述若再接副檔名或 `/`，整段連結
//     （`docs/a.pdf然後.txt`、`docs/a.pdf然後/x.txt`）。罕見，接受，測試已 pin。
const W = '\\p{L}\\p{M}\\p{N}_' // "word" char（原 \w）；\p{M} = combining marks（NFD）
const SEG = `[${W}.-]` // 目錄段字元
const STEM = `[${W}-]` // 檔名主幹字元
const INNER_EXT = `[\\p{L}\\p{M}\\p{N}]+(?:[-+][\\p{L}\\p{M}\\p{N}]+)*`
const FINAL_EXT = `[A-Za-z0-9]+(?:[-+][A-Za-z0-9]+)*`
const EXT = `(?:\\.${INNER_EXT})*\\.${FINAL_EXT}`
const SUFFIX = `(?::(\\d+)(?::(\\d+))?)?`

// 絕對路徑：必須以 `/` 開頭 + 末段 name.ext（Unicode 段名；支援多重副檔名如 .d.ts / .min.js，副檔名段允許內含連字號如 pre-edit 與 `+` build metadata 如 0.0.0+075a408）
// capture groups：1 = path, 2 = line, 3 = col（runRegex 依賴此配置）
export const ABS_RE = new RegExp(`(?<![${W}/:~.])(\\/(?:${SEG}+\\/)*${STEM}+${EXT})${SUFFIX}`, 'gu')

// Tilde 路徑：以 ~/ 開頭 + 末段 name.ext（Unicode 段名；支援 dotdir 與多重副檔名，副檔名段允許內含連字號與 `+` build metadata）
export const TILDE_RE = new RegExp(`(?<![${W}/:~])(~\\/(?:${SEG}+\\/)*${STEM}+${EXT})${SUFFIX}`, 'gu')

// 相對路徑（含至少一個 `/`）：不能以 `/` 開頭，至少一個中間段 + 末段（Unicode 段名；支援多重副檔名，副檔名段允許內含連字號與 `+` build metadata）
export const REL_RE = new RegExp(`(?<![${W}/:])((?:${SEG}+\\/)+${STEM}+${EXT})${SUFFIX}`, 'gu')

// 純檔名：無 `/`；lookbehind 阻擋 Unicode word/`/`/`:`/`.`/`-` 避免匹配路徑片段、URL 內段、次級副檔名、或連字號後的尾段（`foo.pre-edit.md` 的 `edit.md`、`計畫-核定.pdf` 的 `核定.pdf`）（支援多重副檔名，副檔名段允許內含連字號與 `+` build metadata）
export const BARE_RE = new RegExp(`(?<![${W}/:.-])(${STEM}+${EXT})${SUFFIX}`, 'gu')

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
