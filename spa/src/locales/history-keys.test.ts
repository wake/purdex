import { describe, expect, it } from 'vitest'
import en from './en.json'
import zhTW from './zh-TW.json'

const REQUIRED_KEYS = [
  'settings.sync.history.title',
  'settings.sync.history.viewLink',
  'settings.sync.history.tabs.local',
  'settings.sync.history.tabs.remote',
  'settings.sync.history.tabs.remoteDaemonOnly',
  'settings.sync.history.empty.local',
  'settings.sync.history.retry',
  'settings.sync.history.error.loadList',
  'settings.sync.history.trigger.auto',
  'settings.sync.history.trigger.manual',
  'settings.sync.history.trigger.preImport',
  'settings.sync.history.trigger.preRestore',
  'settings.sync.history.trigger.sessionPristine',
  'settings.sync.history.detail.metadata',
  'settings.sync.history.detail.timestamp',
  'settings.sync.history.detail.device',
  'settings.sync.history.detail.size',
  'settings.sync.history.detail.diff.title',
  'settings.sync.history.detail.diff.identical',
  'settings.sync.history.detail.diff.changed',
  'settings.sync.history.detail.diff.missingInSnapshot',
  'settings.sync.history.detail.diff.missingInCurrent',
  'settings.sync.history.detail.restore',
  'settings.sync.history.detail.selectPrompt',
  'settings.sync.history.restore.confirmTitle',
  'settings.sync.history.restore.confirmBody',
  'settings.sync.history.restore.confirmPendingConflicts_one',
  'settings.sync.history.restore.confirmPendingConflicts_other',
  'settings.sync.history.restore.cancel',
  'settings.sync.history.restore.proceed',
  'settings.sync.history.restore.success',
]

function flatten(obj: unknown, prefix = ''): Record<string, unknown> {
  const out: Record<string, unknown> = {}
  if (typeof obj !== 'object' || obj === null) return out
  for (const [k, v] of Object.entries(obj)) {
    const key = prefix ? `${prefix}.${k}` : k
    if (typeof v === 'object' && v !== null && !Array.isArray(v)) {
      Object.assign(out, flatten(v, key))
    } else {
      out[key] = v
    }
  }
  return out
}

describe('history i18n keys', () => {
  it('en has all required keys', () => {
    const flat = flatten(en)
    for (const k of REQUIRED_KEYS) expect(flat[k]).toBeDefined()
  })

  it('zh-TW has all required keys', () => {
    const flat = flatten(zhTW)
    for (const k of REQUIRED_KEYS) expect(flat[k]).toBeDefined()
  })
})
