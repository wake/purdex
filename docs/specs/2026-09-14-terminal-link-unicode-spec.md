# Spec — Terminal link matchers: Unicode paths + URL boundary hardening

Status: v3 (v2 after codex spec/plan review `task-mu1ease5-55nt5s`; v3 after PR #1022
R2 defender `review-mu1f0dzs-f8gq93` — see §6)
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
| directory segment char | `[\w.-]` | `[\p{L}\p{M}\p{N}_.-]` |
| file stem char | `[\w-]` | `[\p{L}\p{M}\p{N}_-]` |
| lookbehind "word" char | `\w` | `\p{L}\p{M}\p{N}_` |
| **inner** extension part | `[A-Za-z0-9]+(?:[-+][A-Za-z0-9]+)*` | `[\p{L}\p{M}\p{N}]+(?:[-+][\p{L}\p{M}\p{N}]+)*` |
| **final** extension part | same as inner | **unchanged — ASCII only** `[A-Za-z0-9]+(?:[-+][A-Za-z0-9]+)*` |
| line/col suffix | `(?::(\d+)(?::(\d+))?)?` | unchanged |

Extension chain becomes `(?:\.<inner>)*\.<final>` instead of `(?:\.<part>)+`.

`\p{M}` (combining marks) is included everywhere a letter is, because macOS
`ls` output is frequently NFD-decomposed (`が` as `か` + U+3099, `é` as `e` +
U+0301). The matched string is never normalised — it is passed to the opener
exactly as it appeared on screen so offsets and the path stay faithful.

Why the final part stays ASCII: the dominant real-world case is agent prose
glued straight after a file name — `已更新 docs/a.pdf並重新` — and an ASCII-only
terminal extension makes the regex stop at `pdf` when nothing path-like
follows, without a trailing-boundary assertion. This is a bias, not a
guarantee: if the glued prose is itself followed by another extension or a
`/`, the regex legitimately consumes it (`docs/a.pdf然後.txt`,
`docs/a.pdf然後/x.txt` both link whole). Both are pinned by tests as
accepted behaviour. The inner parts are Unicode so that a dotted
CJK stem such as `docs/報告.最終版.pdf` still links whole (`.最終版` is an
inner part, `.pdf` the final part). The regex engine backtracks between the
two roles as needed: `docs/a.pdf然後，再看` → inner `.pdf然後` cannot be
followed by a final part → backtrack → final `.pdf` → link is `docs/a.pdf`.

Interactions checked:

- `extensionsVersionLike` / `allExtensionsVersionLike` split on `.` and test
  each part with `/^\d+(?:-[A-Za-z0-9]+)?$/`. A Unicode inner part contains
  letters → not version-like → the path is kept. `\p{N}` also covers
  full-width digits (`１２３`), which `\d` does not. The version filter
  looks only at the *extension* parts, so `１２３.1.2` is still rejected
  (extensions `1`, `2` are ASCII digits) while `１２３.１２.3` is kept
  (`１２` is not `\d+`). Acceptable — full-width digits are not version
  noise in practice.
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
- Prose glued after the extension and then followed by another extension or
  path segment cannot be separated (`docs/a.pdf然後.txt`,
  `docs/a.pdf然後/x.txt` link whole). Rare; accepted and pinned.
- `grep -n` style output whose file name has no ASCII extension gets no
  link (`docs/報告.最終版:12:內容`), exactly as `docs/report:12` gets none
  today — the matchers require an extension. Unchanged policy; pinned.

### 3.2 URL matcher

Processing per candidate, in order:

1. **Scan** — start at `https?://`, consume characters until a *stop
   character*: any `\s`, the existing `"'<>\``, or any full-width /
   CJK punctuation in the set

   ```
   ，。、；：！？（）［］「」『』【】《》〈〉〔〕｛｝“”‘’…｡｢｣､
   ```

   (U+FF0C U+3002 U+3001 U+FF1B U+FF1A U+FF01 U+FF1F U+FF08 U+FF09 U+FF3B
   U+FF3D U+300C U+300D U+300E U+300F U+3010 U+3011 U+300A U+300B U+3008
   U+3009 U+3014 U+3015 U+FF5B U+FF5D U+201C U+201D U+2018 U+2019 U+2026
   U+FF61 U+FF62 U+FF63 U+FF64).

   This is a heuristic, not a guarantee. The WHATWG URL parser accepts
   un-encoded Unicode punctuation and percent-encodes it, so any of these
   *can* legally appear in a pasted URL. The set is chosen for characters
   that are overwhelmingly sentence punctuation in terminal output. Two
   deliberate calls:
   - `・` (U+30FB, katakana middle dot) is **not** a stop character — it is
     common inside Japanese Wikipedia titles (`アラン・チューリング`).
   - `。` **is** a stop character even though it can act as an IDN dot
     separator (`例子。測試`); an un-encoded ideographic full stop inside a
     hostname on a terminal is far rarer than a sentence ending in `。`.

2. **Non-ASCII cut (option C)** — walk the scanned text by code point. A
   non-ASCII code point (> 0x7F) is cut, together with everything after it,
   iff **both**:
   - the character immediately before it is an ASCII letter or digit, and
   - the code point is **not** `\p{Script=Latin}` and **not** `\p{M}`.

   After anything else — another non-ASCII character, or any ASCII
   punctuation such as `/ ? # = & . _ - ~ % + : @` — it is URL content.
   First cut wins.

   The Latin/mark exemption exists because accented Latin letters continue
   an ASCII word (`bücher`, `café`, NFD `cafe` + U+0301) and are common in
   real hostnames and paths; cutting there changes the destination the
   opener navigates to. Scripts that do not mix with ASCII inside one word
   (Han, kana, Hangul, emoji, Cyrillic, …) keep the cut.

   **Rescan after a cut**: the greedy scan may have swallowed a later URL
   (`參考https://a.com/x或https://b.com/y`). When a candidate is cut, the
   next regex search resumes at the cut position (UTF-16 offset
   `m.index + cut.length`), so every subsequent `https?://` on the line is
   still found. Strip does not trigger a rescan (stripped characters are
   punctuation and cannot start a scheme). A cut always leaves at least the
   ASCII scheme, so the search position strictly advances.

   Rationale: prose glued to a URL essentially always follows the last
   alphanumeric of the URL (`/x這頁`, `.com這頁`, `?q=abc然後`). Real
   Unicode URL content essentially always follows a delimiter (`/臺灣`,
   `=測試`, `_標準`, `-🎉`, `.例子`). The rule does not need to know where
   the authority ends, so IDN hosts with mixed labels (`www.例子.com`,
   `例子1.測試`) are handled by the same clause as paths.

   | input | result | note |
   |---|---|---|
   | `https://zh.wikipedia.org/wiki/臺灣 島` | `…/wiki/臺灣` | after `/` → content |
   | `https://e.com/?q=測試` | `…/?q=測試` | after `=` → content |
   | `https://www.例子.com/a` | whole | after `.` → content |
   | `https://例子1.測試/a` | whole | `1` is ASCII after non-ASCII → kept; `測` after `.` → content |
   | `https://e.com/wiki/ISO_標準` | whole | after `_` → content |
   | `https://e.com/release-🎉` | whole | after `-` → content |
   | `https://e.com/wiki/アラン・チューリング` | whole | `・` is content (not a stop char) |
   | `請見 https://bücher.example/catalog` | whole | `ü` is Latin → exempt |
   | `https://example.com/café/menu` (NFC and NFD) | whole | Latin / mark → exempt |
   | `參考https://a.com/x或https://b.com/y` | `https://a.com/x`, `https://b.com/y` | cut, then rescan finds the second |
   | `參考https://e.com/x這頁` | `https://e.com/x` | after `x` → cut |
   | `https://e.com/abcдом` | `https://e.com/abc` | Cyrillic after ASCII → cut (accepted) |
   | `詳見https://e.com這頁` | `https://e.com` | after `m` → cut |
   | `https://e.com/x🎉` | `https://e.com/x` | emoji after ASCII letter → cut |
   | `https://e.com/?q=abc中文` | `https://e.com/?q=abc` | **accepted sacrifice** — mixed alnum→CJK inside a real URL is indistinguishable from glued prose |
   | `https://e.com/?然後回報` | whole | **accepted false keep** — CJK prose right after a delimiter |
   | `https://e.com/a.然後` | whole | accepted false keep; CJK prose after an ASCII `.` is rare (CJK uses `。`) |
   | `https://zh.wikipedia.org/wiki/臺灣是個島` | whole | accepted false keep |

   Full-width punctuation never reaches this step (it stopped the scan in
   step 1).

3. **Trailing strip with bracket balance** — count `(` `)` `[` `]` once
   over the text after step 2, then walk backwards from the end:
   - ASCII `. , ; : ! ? > }` → strip.
   - `)` → strip only while `count(')') > count('(')`, decrementing the
     close count as you go; likewise `]` vs `[`. Otherwise stop.
   - anything else → stop.
   Slice once at the end. Cost is linear in the text length (a tail of 300
   `)` must not re-scan the string per character).

   Full-width closers cannot be at the tail (step 1 removed them), so the
   strip set is ASCII only. Examples:

   | input | result |
   |---|---|
   | `https://en.wikipedia.org/wiki/Foo_(bar) ok` | `…/Foo_(bar)` |
   | `(see https://e.com/a)` | `https://e.com/a` |
   | `[x](https://e.com/a).` | `https://e.com/a` |
   | `https://e.com/a).` | `https://e.com/a` |
   | `https://e.com/a_(b)).` | `https://e.com/a_(b)` |
   | `https://e.com/a[1]` | `https://e.com/a[1]` |
   | `https://e.com/a[1]]` | `https://e.com/a[1]` |
   | `https://[::1]` | `https://[::1]` |
   | `https://e.com/a` + 300 × `)` | `https://e.com/a` |

   **Scheme-only guard**: if, after steps 2–3, nothing remains after
   `://`, emit no link (`https://).` → none). The regex requires one
   character after `//` but strip can remove it.

4. Emit `{ text, range }` with `endCol = startCol + text.length` as today.

The regex itself stays a single greedy class match (`[^…]+`) so the hot path
cost is one pass per line plus a linear post-scan on matched text only.
Offsets: `range.startCol` / `endCol` are JS UTF-16 offsets (`m.index` and
`text.length`), not code-point counts — a non-BMP prefix such as `😀`
occupies two units. `col-map.ts` converts them to cells downstream.

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
- Combining marks (NFD): `docs/か\u3099.txt` and `docs/cafe\u0301.txt` →
  whole, `meta.path` byte-identical to the input (no normalisation);
  BARE lookbehind blocks on a mark — isolated via ABS input
  `/a/か\u3099x.txt` run through `BARE_RE` → no link (the `x.txt` start is
  preceded by U+3099).
- Pre-existing BARE shadow pinned as-is: `/a/b/foo.pre-edit.md` through
  `BARE_RE` → `edit.md` (documents the known quirk; not a target).
- No-extension grep line: `docs/報告.最終版:12:內容` → no REL link.
- Accepted whole-match limitations pinned: `docs/a.pdf然後.txt`,
  `docs/a.pdf然後/x.txt` → whole string.
- Every existing case stays green (the file already has ~40).

### url.test.ts additions

Every row in §3.2 tables (steps 1–3, including the accepted sacrifices and
the scheme-only guard), plus the five §1.2 rows, plus:
- multi-URL on one line with full-width separators
  (`https://a.com，https://b.com` → two links);
- `https://e.com/［說明］` → `https://e.com/`;
- range assertion for a CJK-cut case: `參考https://e.com/x這頁` →
  `startCol 2`, `endCol 2 + 'https://e.com/x'.length`;
- range assertion with a non-BMP prefix: `😀https://e.com/臺灣` →
  `startCol 2`, `endCol 18` (UTF-16 offsets, not code points);
- parametrised stop-set test: for **every** character `c` in the step-1 set,
  `https://e.com/a${c}b` → exactly `https://e.com/a`;
- Latin exemption rows and the rescan rows from the step-2 table, with
  range assertions on the rescan case (`startCol` of the second link is
  18).

### Regression

`cd spa && npx vitest run src/lib/terminal-link` then full
`npx vitest run`, `pnpm run lint`, `pnpm run build`.

## 5. Rollout

Pure SPA change; served via the dev server (HMR) once on `main`. No
daemon/Electron rebuild required. Bump to next alpha after merge.

## 6. Review log

### R1 — codex `task-mu1ease5-55nt5s` (spec + plan, verdict: rework)

| # | sev | finding | resolution |
|---|---|---|---|
| 1 | Major | rule (c) only handled IDN labels whose predecessor label ends non-ASCII; `www.例子.com` → cut to `www` | option C rewritten: cut only after an ASCII **alphanumeric** (§3.2 step 2). No authority special-casing needed. |
| 2 | Major | option C over-cut legitimate mixed content (`ISO_標準`, `release-🎉`, `?q=abc中文`) and the sacrifice was under-documented | same rewrite fixes `_`/`-` cases; `?q=abc中文` and `/?然後回報` recorded explicitly as accepted sacrifice / false keep |
| 3 | Major | "zero false-cut risk" for the stop set is false; `・` breaks Japanese titles, `。` can be an IDN dot | claim removed; `・` dropped from the set; `。` kept with the trade-off written down |
| 4 | Minor | `\p{L}\p{N}` misses combining marks (NFD `が`, `é`) | `\p{M}` added to every letter class and lookbehind; NFD tests added; no normalisation |
| 5 | Minor | `［］` and half-width `｡｢｣､` missing from the stop set | added |
| 6 | Minor | ASCII final extension is not an unconditional terminator (`docs/a.pdf然後/x.txt`) | wording changed to "bias, not guarantee"; both whole-match cases pinned by tests |
| 7 | Minor | per-iteration `count()` in the strip loop is quadratic on a long `)` tail | count once, decrement, slice once (§3.2 step 3) |
| 8 | Minor | tests missed "strip to balance then stop", non-BMP offsets, `[::1]`, `a[1]]` | all added to §4 |
| 9 | Nit | "cannot be scheme-only" argument was wrong (`https://).`) | scheme-only guard added |

### R2 — PR #1022 codex reviews

- Standard `review-mu1exgof-hi68g0`: no findings.
- Attacker `review-mu1ey65u-u66dm4`: approve — 123k ASCII regex comparisons
  against main identical; ~300-char stress lines no catastrophic
  backtracking; mid-URL astral offsets, bracket handling, scheme guard OK.
- File health `review-mu1f2nnr-ad5uoy`: ship.
- Defender `review-mu1f0dzs-f8gq93`: needs-attention.

| # | sev | finding | resolution |
|---|---|---|---|
| 1 | P2 | alnum-only cut breaks accented Latin and IDN hosts (`bücher.example` → `https://b`, `café/menu` → `caf`) — opener navigates to the wrong destination | Latin/`\p{M}` exemption added to the cut rule (§3.2 step 2) |
| 2 | P2 | after a cut, later URLs on the same line are lost (`…/x或https://b.com/y` → one link) | rescan from the cut offset (§3.2 step 2) |
| 3 | P3 | alignment pins missing: BARE shadow `edit.md`, stop-set characters individually, mark lookbehind not isolated | tests added (§4) |
| 4 | P3 | §3.1 full-width digit sentence attributed the decision to the stem; it is the extension | reworded |
| 5 | P3 | ASCII-final bias cost on grep-style lines (`docs/報告.最終版:12`) not stated | limitation added and pinned; matches existing no-extension policy |
| 6 | nit | url.ts comment "ASCII 標點永遠剝" should name `TAIL_PUNCT` | comment fixed |
