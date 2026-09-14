# Spec — Terminal link matchers: Unicode paths + URL boundary hardening

Status: v1 (draft, pre-codex review)
Date: 2026-09-14
Branch: `worktree-terminal-link-unicode`
Scope: SPA only (`spa/src/lib/terminal-link/matchers/`). No daemon, no Electron.

## 1. Problems

### 1.1 File paths containing CJK are never linkified

`docs/data/申請流程/潛優/115潛優修正計畫-核定.pdf` produces no link. All four
file-path regexes in `matchers/file-path.ts` (`ABS_RE` / `TILDE_RE` /
`REL_RE` / `BARE_RE`) build path segments from `[\w.-]` and the file stem
from `[\w-]`. Without the `u` flag JS `\w` is `[A-Za-z0-9_]`, so any segment
containing a CJK character breaks the match. Verified with node: all three
applicable regexes return `[]`; the ASCII twin
`docs/data/flow/sub/115plan-final.pdf` matches in full.

The rest of the pipeline is already wide-character aware
(`col-map.ts` maps JS offsets to terminal cells; the openers pass the path
through unchanged). Only the matcher character classes block it.

### 1.2 URL boundary is wrong when prose is glued to the URL

`matchers/url.ts` matches `https?:\/\/[^\s"'<>\`]+` and strips a trailing
run of ASCII punctuation `[.,;:!?)\]}>]`. Observed misfires:

| input | current link text | defect |
|---|---|---|
| `https://e.com/a，然後回報` | `https://e.com/a，然後回報` | full-width comma is not a boundary |
| `https://e.com/a。` | `https://e.com/a。` | full-width stop not stripped |
| `（見 https://e.com/a）` | `https://e.com/a）` | full-width paren not stripped |
| `https://en.wikipedia.org/wiki/Foo_(bar) ok` | `…/wiki/Foo_(bar` | balanced `)` wrongly stripped (already noted in a code comment) |
| `參考https://e.com/x這頁` | `https://e.com/x這頁` | CJK glued straight after the URL |

## 2. Goals / non-goals

### Goals

1. File-path matchers accept Unicode letters/digits in directory segments and
   file stems, while keeping every existing ASCII behaviour (multi-extension,
   `-`/`+` chains, version/IP rejection, lookbehind guards) unchanged.
2. URL matcher treats full-width punctuation as a hard boundary and strips it
   from the tail.
3. URL matcher strips a trailing `)` / `]` only when it is unbalanced within
   the URL.
4. URL matcher cuts a run of non-ASCII characters off the tail unless it is
   plausibly URL content (option C below).

### Non-goals

- Wrapped-line reassembly (a URL split across two terminal rows). Separate
  problem; not touched.
- Percent-decoding / IDN normalisation. The link text is whatever was on
  screen.
- Changing which matchers are enabled by default (`useUISettingsStore`
  `linkDetect*` flags stay as they are).
- Opener changes.

## 3. Design

### 3.1 File-path regexes

Replace the ASCII classes with Unicode property classes and add the `u`
flag. Defined once and composed so all four regexes stay in lock-step:

| role | today | new |
|---|---|---|
| directory segment char | `[\w.-]` | `[\p{L}\p{N}_.-]` |
| file stem char | `[\w-]` | `[\p{L}\p{N}_-]` |
| lookbehind "word" char | `\w` | `\p{L}\p{N}_` |
| **inner** extension part | `[A-Za-z0-9]+(?:[-+][A-Za-z0-9]+)*` | `[\p{L}\p{N}]+(?:[-+][\p{L}\p{N}]+)*` |
| **final** extension part | same as inner | **unchanged — ASCII only** `[A-Za-z0-9]+(?:[-+][A-Za-z0-9]+)*` |
| line/col suffix | `(?::(\d+)(?::(\d+))?)?` | unchanged |

Extension chain becomes `(?:\.<inner>)*\.<final>` instead of `(?:\.<part>)+`.

Why the final part stays ASCII: the dominant real-world case is agent prose
glued straight after a file name — `已更新 docs/a.pdf並重新` — and an ASCII-only
terminal extension makes the regex stop at `pdf` naturally, without a
trailing-boundary assertion. The inner parts are Unicode so that a dotted
CJK stem such as `docs/報告.最終版.pdf` still links whole (`.最終版` is an
inner part, `.pdf` the final part). The regex engine backtracks between the
two roles as needed: `docs/a.pdf然後，再看` → inner `.pdf然後` cannot be
followed by a final part → backtrack → final `.pdf` → link is `docs/a.pdf`.

Interactions checked:

- `extensionsVersionLike` / `allExtensionsVersionLike` split on `.` and test
  each part with `/^\d+(?:-[A-Za-z0-9]+)?$/`. A Unicode inner part contains
  letters → not version-like → the path is kept. `\p{N}` also covers
  full-width digits (`１２３`), which `\d` does not; a stem of full-width
  digits is therefore not rejected as a version — acceptable, they are not
  version noise in practice.
- `BARE_RE`'s lookbehind now blocks on any Unicode letter/digit, so
  `115潛優修正計畫-核定.pdf` inside a `REL_RE` match is not double-matched by
  `BARE_RE` (preceded by `/`), and `報告.pdf` inside `已產生報告.pdf` is not
  split off (preceded by a letter). Behaviour mirrors ASCII today.
- The URL guard in `runRegex` (`/https?:\/\/\S*$/` on the prefix) is
  unchanged.
- `u` flag: `\d` stays ASCII; `\/` and `\.` escapes remain legal; the `-` at
  the end of a class remains literal.

Known limitations (documented in a code comment):

- Prose glued *before* a relative path with no separator (`已更新docs/a.pdf`)
  becomes part of the first segment. Identical to ASCII (`seedocs/a.pdf`);
  there is no boundary to find.
- Pre-existing, out of scope: `BARE_RE`'s lookbehind does not include `-`,
  so `/a/b/foo.pre-edit.md` also yields a shadowed `BARE` match `edit.md`
  (ABS registered first wins on hover). Unchanged by this spec.
- A CJK sentence that ends in a file-like token is linked as a whole by
  `BARE_RE` (`請見附件.pdf` → one link). Same ambiguity as ASCII
  (`seeattachment.pdf`); click resolves through the existing
  stat → not-found popup path.
- Prose glued after a CJK stem with no punctuation and no ASCII extension
  cannot be separated (`docs/a.pdf然後.txt` links whole). Rare; accepted.

### 3.2 URL matcher

Processing per candidate, in order:

1. **Scan** — start at `https?://`, consume characters until a *stop
   character*: any `\s`, the existing `"'<>\``, or any full-width /
   CJK punctuation in the set

   ```
   ，。、；：！？（）「」『』【】《》〈〉〔〕｛｝“”‘’…・
   ```

   (U+FF0C U+3002 U+3001 U+FF1B U+FF1A U+FF01 U+FF1F U+FF08 U+FF09 U+300C
   U+300D U+300E U+300F U+3010 U+3011 U+300A U+300B U+3008 U+3009 U+3014
   U+3015 U+FF5B U+FF5D U+201C U+201D U+2018 U+2019 U+2026 U+30FB).
   These never appear un-encoded in a URL; zero false-cut risk.

2. **Non-ASCII cut (option C)** — walk the scanned text after the scheme.
   A non-ASCII code point is URL content iff the character immediately
   before it is (a) itself non-ASCII, (b) one of `/ ? # = &`, or (c) a `.`
   whose own predecessor is non-ASCII (an IDN label boundary such as
   `例子.測試`). Otherwise cut the URL just before it. First cut wins.

   | input | result | note |
   |---|---|---|
   | `https://zh.wikipedia.org/wiki/臺灣 島` | `…/wiki/臺灣` | after `/` → content |
   | `https://e.com/?q=測試` | `…/?q=測試` | after `=` → content |
   | `https://例子.測試/a` | `https://例子.測試/a` | after `/` of `://` → content; `.` between non-ASCII → rule (c) |
   | `https://e.com/a.然後` | `https://e.com/a.` → strip → `https://e.com/a` | `.` after ASCII → cut |
   | `參考https://e.com/x這頁` | `https://e.com/x` | after `x` → cut |
   | `https://e.com/x🎉` | `https://e.com/x` | emoji after ASCII → cut |
   | `https://zh.wikipedia.org/wiki/臺灣是個島` | whole | accepted sacrifice of option C |

   "Non-ASCII" means code point > 0x7F. Full-width punctuation never reaches
   this step (it stopped the scan in step 1).

3. **Trailing strip with bracket balance** — repeatedly inspect the last
   character:
   - ASCII `. , ; : ! ? > }` → strip.
   - `)` → strip only if the count of `)` in the text exceeds the count of
     `(`; likewise `]` vs `[`. Otherwise stop.
   - anything else → stop.

   Full-width closers cannot be at the tail (step 1 removed them), so the
   strip set is ASCII only. Examples:

   | input | result |
   |---|---|
   | `https://en.wikipedia.org/wiki/Foo_(bar) ok` | `…/Foo_(bar)` |
   | `(see https://e.com/a)` | `https://e.com/a` |
   | `[x](https://e.com/a).` | `https://e.com/a` |
   | `https://e.com/a).` | `https://e.com/a` |
   | `https://e.com/a[1]` | `https://e.com/a[1]` |

4. Emit `{ text, range }` with `endCol = startCol + text.length` as today.

The regex itself stays a single greedy class match (`[^…]+`) so the hot path
cost is one pass per line plus a linear post-scan on matched text only.

### 3.3 Column mapping

Unchanged. `xterm-provider.ts` already converts JS offsets through
`buildJsOffsetToCol`, which reads cell widths from the buffer, so CJK/emoji
inside a link land on the right cells.

## 4. Test plan (vitest, TDD)

### file-path.test.ts additions

- REL: `docs/data/申請流程/潛優/115潛優修正計畫-核定.pdf` → whole path,
  `meta.path` equal; with `:12` suffix → `line: 12`.
- ABS: `/Users/wake/文件/報告.pdf`; TILDE: `~/文件/報告.pdf`; BARE:
  `報告.pdf`.
- CJK prose glued after extension: `已更新 docs/a.pdf並重新` → `docs/a.pdf`
  (note the space before `docs`; see "glued prefix" below).
- Full-width punctuation after: `docs/a.pdf，然後` / `docs/a.pdf。` →
  `docs/a.pdf`.
- Dotted CJK stem: `docs/報告.最終版.pdf` → whole.
- BARE lookbehind: `已產生報告.pdf` → `已產生報告.pdf` (one link, not
  `報告.pdf`); `已更新 docs/報告.pdf` → REL link `docs/報告.pdf` and no BARE
  link for `報告.pdf`.
- Glued prefix is inherent and mirrors ASCII: `已更新docs/a.pdf` links as
  `已更新docs/a.pdf` exactly as `seedocs/a.pdf` links whole today. Pin this
  with a test so the behaviour is deliberate, not accidental.
- Version rejection still holds: `/path/1.2.3`, `192.168.1.1`,
  `1.0.0+exp.sha` → none.
- Every existing case stays green (the file already has ~40).

### url.test.ts additions

Every row in §3.2 tables, plus the five §1.2 rows, plus multi-URL on one
line with full-width separators (`https://a.com，https://b.com` → two links),
plus range assertions for one CJK-cut case (`endCol` equals cut index).

### Regression

`cd spa && npx vitest run src/lib/terminal-link` then full
`npx vitest run`, `pnpm run lint`, `pnpm run build`.

## 5. Rollout

Pure SPA change; served via the dev server (HMR) once on `main`. No
daemon/Electron rebuild required. Bump to next alpha after merge.
