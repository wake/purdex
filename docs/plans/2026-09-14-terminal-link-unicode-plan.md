# Plan — Terminal link matchers: Unicode paths + URL boundary hardening

Spec: `docs/specs/2026-09-14-terminal-link-unicode-spec.md`
Branch: `worktree-terminal-link-unicode`
Worktree: `/Users/wake/Workspace/wake/purdex/.claude/worktrees/terminal-link-unicode`

Two independent tasks, one file each (plus its test). Each task is one
commit. Both run TDD: add failing tests first, run them to see red, then
implement, then run the whole `terminal-link` folder.

## Task 1 — `file-path.ts`: Unicode classes (spec §3.1)

Files: `spa/src/lib/terminal-link/matchers/file-path.ts`,
`spa/src/lib/terminal-link/matchers/file-path.test.ts`.

### Steps

1. **Tests (red).** Append a `describe('unicode paths')` block covering
   every bullet in spec §4 "file-path.test.ts additions". Use the existing
   `make()` helpers per regex. Assert `text`, `meta.path`, and where noted
   `meta.line`. Add the "glued prefix" pin test (`已更新docs/a.pdf` →
   whole) and the two version-rejection re-checks. Run
   `npx vitest run src/lib/terminal-link/matchers/file-path.test.ts` and
   confirm the new cases fail while the old ones pass.
2. **Implement (green).** Rewrite the four exported regexes from shared
   string fragments so the classes cannot drift:

   ```ts
   const W = '\\p{L}\\p{N}_'          // "word" char (was \w)
   const SEG = `[${W}.-]`             // directory segment char
   const STEM = `[${W}-]`             // file stem char
   const INNER_EXT = `[\\p{L}\\p{N}]+(?:[-+][\\p{L}\\p{N}]+)*`
   const FINAL_EXT = `[A-Za-z0-9]+(?:[-+][A-Za-z0-9]+)*`
   const EXT = `(?:\\.${INNER_EXT})*\\.${FINAL_EXT}`
   const SUFFIX = `(?::(\\d+)(?::(\\d+))?)?`
   export const ABS_RE   = new RegExp(`(?<![${W}/:~.])(\\/(?:${SEG}+\\/)*${STEM}+${EXT})${SUFFIX}`, 'gu')
   export const TILDE_RE = new RegExp(`(?<![${W}/:~])(~\\/(?:${SEG}+\\/)*${STEM}+${EXT})${SUFFIX}`, 'gu')
   export const REL_RE   = new RegExp(`(?<![${W}/:])((?:${SEG}+\\/)+${STEM}+${EXT})${SUFFIX}`, 'gu')
   export const BARE_RE  = new RegExp(`(?<![${W}/:.])(${STEM}+${EXT})${SUFFIX}`, 'gu')
   ```

   Keep the capture-group layout (1 = path, 2 = line, 3 = col) — `runRegex`
   depends on it. Keep the existing comments above each regex, updated to
   say Unicode, and add a short "known limitations" comment block (spec
   §3.1 list). `runRegex`, `extensionsVersionLike`,
   `allExtensionsVersionLike`, `createFilePathMatcher` are untouched.
3. Run the folder: `npx vitest run src/lib/terminal-link`. All green,
   including `register.test.ts` / `xterm-provider.test.ts` which import
   the regexes.
4. Commit: `fix(spa): terminal file-path links accept Unicode segments`.

### Guard rails

- Do not add `-` to `BARE_RE`'s lookbehind (spec: pre-existing, out of
  scope).
- Do not touch `url.ts`.
- Do not add a trailing-boundary assertion; the ASCII final extension is
  the boundary by construction.

## Task 2 — `url.ts`: boundary hardening (spec §3.2)

Files: `spa/src/lib/terminal-link/matchers/url.ts`,
`spa/src/lib/terminal-link/matchers/url.test.ts`.

### Steps

1. **Tests (red).** Add describe blocks: `full-width boundaries`,
   `non-ASCII cut (option C)`, `bracket balance`. One `it` per table row in
   spec §1.2 and §3.2 (steps 2 and 3), plus:
   - `https://a.com，https://b.com` → two links.
   - one range assertion on a cut case: `'參考https://e.com/x這頁'` →
     `startCol 2`, `endCol 2 + 'https://e.com/x'.length`.
   - existing six tests stay.
   Run `npx vitest run src/lib/terminal-link/matchers/url.test.ts`; confirm
   red on the new cases only.
2. **Implement (green).** In `url.ts`:
   - `STOP_CHARS` string constant = `\s"'<>\`` + the full-width set from spec
     §3.2 step 1 (write the literal characters; list code points in the
     comment). `URL_RE = new RegExp(\`https?:\\/\\/[^${STOP_CHARS}]+\`, 'gu')`.
   - `cutNonAscii(text)`: iterate code points (`for (const ch of text)`),
     track `prev` and `prevPrev`; a code point > 0x7F is kept iff
     `prev > 0x7F` || `prev ∈ '/?#=&'` || (`prev === '.'` && `prevPrev > 0x7F`);
     otherwise return the prefix accumulated so far. Scheme `https://`
     is ASCII so no special-casing.
   - `stripTail(text)`: loop — last char in `.,;:!?>}` → drop; `)` → drop
     only if `count(')') > count('(')`; `]` likewise vs `[`; else break.
   - `provide` = for each match: `cutNonAscii` → `stripTail` → push with
     `endCol = startCol + text.length`. Skip a candidate whose text is
     reduced to just the scheme? Not needed: the regex requires ≥1 char
     after `//` and the cut can only remove non-ASCII, so at least the
     scheme + one ASCII char survive; strip cannot remove `/`.
   - Update the header comment: remove the "known limitation" about
     unbalanced parens (now handled); state option C and its accepted
     sacrifice.
3. Run the folder: `npx vitest run src/lib/terminal-link`.
4. Commit: `fix(spa): terminal URL links stop at full-width punctuation, glued CJK and balanced brackets`.

### Guard rails

- Keep `URL_RE` a single class match; all refinement is in the two pure
  helpers so they are unit-testable via `provide`.
- Do not export the helpers unless a test needs them; `provide` coverage is
  sufficient.
- No change to `openers/url.ts`.

## Task 3 — integration (main session)

1. `cd spa && npx vitest run` (full), `pnpm run lint`, `pnpm run build`.
2. Push, open PR against `main` referencing the spec; codex R1 standard
   review + R2 three-way (attack / defend / file health).
3. Merge (`--merge`), bump PR (`--squash`), align `main`, verify the dev
   server at `100.64.0.2:5174` serves the merged code (SPA-only; no
   daemon/Electron rebuild).
