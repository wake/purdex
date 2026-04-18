// English needs separate singular/plural strings. Our i18n layer only does
// `{{var}}` replacement, so we emulate CLDR-style plurals by splitting each
// count-sensitive key into two variants (`_one` / `_other`) and picking at
// callsite. Locales without plural distinction (zh-TW) use the same text for
// both variants.

export function pluralKey(base: string, count: number): string {
  return count === 1 ? `${base}_one` : `${base}_other`
}
