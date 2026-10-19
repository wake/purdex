// spa/src/locales/placeholders.test.ts — a parameter in a locale string is written {{name}}: the i18n store replaces that and nothing else (useI18nStore `t`: /\{\{(\w+)\}\}/). Anything else that looks
// like a placeholder is shown as it is, braces and all. The worker settings and 暫停 strings shipped with single braces once, and their tests (which compare with the output of t()) did not notice.
// So: every value of both locale files, with a detector that is itself tested.
import { describe, it, expect } from 'vitest'
import en from './en.json'
import zhTW from './zh-TW.json'
import { useI18nStore } from '../stores/useI18nStore'

/** What `t` replaces, exactly. */
const REPLACED = /\{\{\w+\}\}/g

/**
 * A brace group that is meant to be a placeholder and that `t` would NOT replace: after the real `{{name}}` ones are taken out, any `{…}` that has a word character in it and none of the
 * punctuation of a JSON example (a quote, a colon, a comma). So `{n}`, `{ n }`, `{user-name}`, `{a.b}`, `{x}}`, `{{ n }}` all count; a JSON example (`{"name": …}`, `{...}`) does not.
 */
function looksLikeUnreplacedPlaceholder(text: string): boolean {
  return /\{[^{}"':,]*\w[^{}"':,]*\}/.test(text.replace(REPLACED, ''))
}

/** Strings that show a single-brace word ON PURPOSE. They explain the template syntax of a resume command, where `{id}` is the thing being talked about, so it must stay literal. */
const LITERAL_BRACES = new Set(['resume_template.warning.exact_missing_id', 'resume_template.warning.fallback_has_id'])

/** Every parameter name a string may take, so that a double-brace placeholder is replaced whatever its name. */
const params = new Proxy({}, { get: (_t, key) => (typeof key === 'string' ? `<${key}>` : undefined) }) as Record<string, string>

describe('the detector', () => {
  it.each(['{n}', '{ n }', '{user-name}', '{a.b}', '{x}}', '{{ n }}', '{{n}} and {m}', 'a {n} b', '{n }'])('sees %j as a placeholder that would be shown with its braces', (text) => {
    expect(looksLikeUnreplacedPlaceholder(text)).toBe(true)
  })

  it.each(['{{n}}', '{{n}} 小時', '{{alias}}: {{reason}}', '{"flip": true}', '{"name": "My Theme", "tokens": {...}}', '{...}', 'no braces', '', '{}', '{ }'])('lets %j through', (text) => {
    expect(looksLikeUnreplacedPlaceholder(text)).toBe(false)
  })
})

describe.each([['en', en], ['zh-TW', zhTW]] as const)('locale %s', (lang, table) => {
  const all = Object.entries(table as Record<string, unknown>)

  it('every value is a string (`t` calls .replace on it)', () => {
    expect(all.filter(([, v]) => typeof v !== 'string').map(([k]) => k)).toEqual([])
  })

  const entries = all.filter((e): e is [string, string] => typeof e[1] === 'string')

  it('has strings to look at, and none was left out', () => {
    expect(entries.length).toBeGreaterThan(1000)
    expect(entries.length).toBe(all.length)
  })

  it('no string has a placeholder `t` would not replace, except the ones that show {id} on purpose', () => {
    expect(entries.filter(([k, v]) => looksLikeUnreplacedPlaceholder(v) && !LITERAL_BRACES.has(k)).map(([k]) => k)).toEqual([])
  })

  it('the allowed ones really are the template-syntax strings (the list does not outlive them)', () => {
    for (const k of LITERAL_BRACES) expect(looksLikeUnreplacedPlaceholder((table as Record<string, string>)[k]), k).toBe(true)
  })

  it('rendered with their parameters, no string is left with a placeholder in it, except the allowed ones', () => {
    useI18nStore.getState().setLocale(lang)
    const t = useI18nStore.getState().t
    const shown = entries.filter(([k]) => !LITERAL_BRACES.has(k)).map(([k]) => [k, t(k, params)] as const).filter(([, text]) => looksLikeUnreplacedPlaceholder(text) || /\{\{[^{}]*\}\}/.test(text))
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
