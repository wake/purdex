import { describe, it, expect } from 'vitest'
import en from './en.json'
import zhTW from './zh-TW.json'

// Plan ruling 37: both switch tooltips name the two request kinds the
// daemon approves and say what still waits for the person, so nobody reads
// the switch as "everything is automatic".
describe('unattended tooltips state the scope', () => {
  it.each(['unattended.tooltip.off', 'unattended.tooltip.on'] as const)('%s (zh-TW)', (key) => {
    const text = zhTW[key]
    expect(text).toContain('『成為 lead』')
    expect(text).toContain('『自我接力』')
    expect(text).toContain('工具權限與 agent 提問仍會等你')
  })

  it.each(['unattended.tooltip.off', 'unattended.tooltip.on'] as const)('%s (en)', (key) => {
    const text = en[key]
    expect(text).toContain('"become lead"')
    expect(text).toContain('"self relay"')
    expect(text).toContain('tool permissions and agent questions still wait for you')
  })
})
