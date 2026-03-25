// spa/src/lib/register-locales.ts
import { registerLocale } from './locale-registry'
import en from '../locales/en.json'
import zhTW from '../locales/zh-TW.json'

export function registerBuiltinLocales(): void {
  registerLocale({ id: 'en', name: 'English', translations: en, builtin: true })
  registerLocale({ id: 'zh-TW', name: '繁體中文', translations: zhTW, builtin: true })
}
