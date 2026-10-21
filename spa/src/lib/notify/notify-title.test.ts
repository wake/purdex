// spa/src/lib/notify/notify-title.test.ts — the Mac's title rule runs every case of the file the phone push runs (notify-title spec §3.4).
import { describe, it, expect } from 'vitest'
// node:fs / node:path / __dirname are untyped under tsconfig.app.json (see src/index.css.test.ts); they work at runtime.
// @ts-expect-error node:fs is untyped here.
import { readFileSync } from 'node:fs'
// @ts-expect-error node:path is untyped here.
import { resolve } from 'node:path'
import { notifyTitle, type NotifyTitleLocale } from './notify-title'

interface Case { case: string; locale: string; name: string; group: string; tmux: string; host_label: string; thing: string; want: string }
// @ts-expect-error __dirname is untyped here.
const cases = JSON.parse(readFileSync(resolve(__dirname, '../../../../internal/push/testdata/notify-title-cases.json'), 'utf8')) as Case[]

describe('notifyTitle runs the shared cases', () => {
  it('has cases', () => { expect(cases.length).toBeGreaterThan(10) })
  it.each(cases.map((c) => [c.case, c] as const))('%s', (_n, c) => {
    expect(notifyTitle({ name: c.name, group: c.group, tmux: c.tmux }, c.host_label, c.thing, c.locale as NotifyTitleLocale)).toBe(c.want)
  })
})
