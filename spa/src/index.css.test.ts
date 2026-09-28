import { describe, it, expect } from 'vitest'
// `node:fs` / `node:path` / `__dirname` have no ambient declarations under
// tsconfig.app.json's `types: ["vite/client"]` (deliberately excludes
// @types/node — pulling it in globally, e.g. via a `/// <reference types="node" />`,
// overrides DOM's `setTimeout` return type project-wide and breaks HoverTooltip.tsx).
// vite-node still injects real `__dirname` / `__filename` at runtime under vitest
// (CJS-interop shim), so these values import cleanly; only their types are missing.
// @ts-expect-error node:fs is untyped here — see comment above.
import { readFileSync } from 'node:fs'
// @ts-expect-error node:path is untyped here — see comment above.
import { resolve } from 'node:path'

describe('index.css — host badge state rule', () => {
  // @ts-expect-error __dirname is untyped here — see comment above.
  const css: string = readFileSync(resolve(__dirname, 'index.css'), 'utf8')

  it('switches the badge icon to --hb-main on a hovered or active .group row', () => {
    const rule = /\.group:hover \[data-host-badge\],\s*\.group\[data-active="true"\] \[data-host-badge\]\s*\{\s*--hb-icon:\s*var\(--hb-main\);\s*\}/
    expect(css).toMatch(rule)
  })

  it('keeps the rule outside any @layer so utilities cannot outrank it', () => {
    const idx = css.indexOf('[data-host-badge]')
    const before = css.slice(0, idx)
    const opened = (before.match(/@layer\s+[a-z]+\s*\{/g) ?? []).length
    const closed = 0 // Tailwind 4 `@import` lines open no block; a hand-written @layer block would.
    expect(opened).toBe(closed)
  })
})

describe('index.css — transcript search marks', () => {
  // @ts-expect-error __dirname is untyped here — see comment above.
  const css: string = readFileSync(resolve(__dirname, 'index.css'), 'utf8')
  const rule = (name: string) => css.match(new RegExp(`::highlight\\(${name}\\)\\s*\\{([^}]*)\\}`))?.[1] ?? ''

  it('does not paint a match in the user bubble\'s own fill (finding A9)', () => {
    // ChatUserBubble is `bg-accent-muted`: a mark in that same fill vanishes inside it.
    const match = rule('search-match')
    expect(match).toMatch(/background-color:/)
    expect(match).not.toMatch(/var\(--accent-muted\)/)
  })

  it('marks every match with a cue that is not a fill alone', () => {
    expect(rule('search-match')).toMatch(/text-decoration:[^;]*underline/)
  })
})

describe('index.css — worker prose', () => {
  // @ts-expect-error __dirname is untyped here — see comment above.
  const css: string = readFileSync(resolve(__dirname, 'index.css'), 'utf8')

  it('has no `:where(>` — a relative selector is invalid inside :where(), and the browser drops the whole rule', () => {
    expect(css).not.toMatch(/:where\(\s*>/)
  })

  it('zeroes the last direct child\'s bottom margin with a valid child combinator', () => {
    expect(css).toMatch(/\.worker-prose > :last-child\s*\{\s*margin-bottom:\s*0;\s*\}/)
  })
})
