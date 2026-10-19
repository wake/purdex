// spa/src/locales/placeholders.test.ts — a parameter in a locale string is written {{name}}: the i18n store replaces that and nothing else (useI18nStore `t`). A single-brace {name} is shown as it
// is, braces and all. The worker settings and 暫停 strings shipped with single braces once, and their tests (which compare with the output of t()) did not notice. So: every value of both locale files.
import { describe, it, expect } from 'vitest'
import en from './en.json'
import zhTW from './zh-TW.json'
import { useI18nStore } from '../stores/useI18nStore'

const SINGLE = /(?<!\{)\{\w+\}(?!\})/

/** Strings that show a single-brace word ON PURPOSE. They explain the template syntax of a resume command, where `{id}` is the thing being talked about, so it must stay literal. */
const LITERAL_BRACES = new Set(['resume_template.warning.exact_missing_id', 'resume_template.warning.fallback_has_id'])

/** Every parameter name a string may take, so that a double-brace placeholder is replaced whatever its name. */
const params = new Proxy({}, { get: (_t, key) => (typeof key === 'string' ? `<${key}>` : undefined) }) as Record<string, string>

describe.each([['en', en], ['zh-TW', zhTW]] as const)('locale %s', (lang, table) => {
  const entries = Object.entries(table as Record<string, string>).filter(([, v]) => typeof v === 'string')

  it('has strings to look at', () => {
    expect(entries.length).toBeGreaterThan(1000)
  })

  it('no string has a single-brace placeholder, except the ones that show {id} on purpose', () => {
    expect(entries.filter(([k, v]) => SINGLE.test(v) && !LITERAL_BRACES.has(k)).map(([k]) => k)).toEqual([])
  })

  it('the allowed ones really are the template-syntax strings (the list does not outlive them)', () => {
    for (const k of LITERAL_BRACES) expect((table as Record<string, string>)[k], k).toMatch(SINGLE)
  })

  it('rendered with their parameters, no string is left with a placeholder in it (a {{…}} not replaced, or a {word}), except the allowed ones', () => {
    useI18nStore.getState().setLocale(lang)
    const t = useI18nStore.getState().t
    // A brace that is not a placeholder is fine: a few strings show a JSON example (`{"name": …}`), which has quotes and a colon in it, never a bare word between the braces.
    const left = (text: string) => /\{\{[^{}]*\}\}/.test(text) || SINGLE.test(text) // a `}}` closing a nested JSON example is no placeholder: the pair has to be {{…}}
    const shown = entries.filter(([k]) => !LITERAL_BRACES.has(k)).map(([k]) => [k, t(k, params)] as const).filter(([, text]) => left(text))
    expect(shown).toEqual([])
  })

  it('a placeholder is replaced by its parameter: the rendered text carries the value, not the braces', () => {
    useI18nStore.getState().setLocale(lang)
    const t = useI18nStore.getState().t
    expect(t('pool.pause.hours', { n: 3 })).toContain('3')
    expect(t('pool.pause.hours', { n: 3 })).not.toMatch(/[{}]/)
    expect(t('pool.worker.no_answer', { alias: 'mlab', reason: 'timeout' })).toMatch(/mlab.*timeout/)
  })
})
