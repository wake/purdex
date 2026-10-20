// The Mac's notification title is to follow the phone push's (notify-title spec §3.4, N-3): both run the SAME fixture
// (internal/push/testdata/notify-title-cases.json -> TestNotifyTitleFixture). This only proves vitest can read it by a
// relative path; the port that runs the cases arrives with N-3.
import { describe, it, expect } from 'vitest'
// node:fs / node:path / __dirname are untyped under tsconfig.app.json (see src/index.css.test.ts); they work at runtime.
// @ts-expect-error node:fs is untyped here.
import { readFileSync } from 'node:fs'
// @ts-expect-error node:path is untyped here.
import { resolve } from 'node:path'

interface Case { case: string; locale: string; name: string; group: string; tmux: string; host_label: string; thing: string; want: string }
// @ts-expect-error __dirname is untyped here.
const cases = JSON.parse(readFileSync(resolve(__dirname, '../../../internal/push/testdata/notify-title-cases.json'), 'utf8')) as Case[]

describe('notify-title-cases fixture', () => {
  it('is readable from the SPA, and every case has a want', () => {
    expect(cases.length).toBeGreaterThan(0)
    for (const c of cases) expect(c.want).not.toBe('')
  })
})
