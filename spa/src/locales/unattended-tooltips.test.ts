import { describe, it, expect } from 'vitest'
import en from './en.json'
import zhTW from './zh-TW.json'

// Relay v2 (App contract 2): both switch tooltips say the v2 rule - relay requests are approved by the quota, "become lead" and
// adopt requests are always refused - and what still waits for the person, so nobody reads the switch as "everything is automatic".
// A host whose daemon lacks `team.person.v1` still auto-approves lead and adopt: one more sentence, only then (UnattendedButton).
describe('unattended tooltips state the v2 rule', () => {
  it.each(['unattended.tooltip.off', 'unattended.tooltip.on'] as const)('%s (zh-TW)', (key) => {
    const text = zhTW[key]
    expect(text).toContain('接力申請')
    expect(text).toContain('自動核准')
    expect(text).toContain('額度')
    expect(text).toContain('『成為 lead』')
    expect(text).toContain('納入申請')
    expect(text).toContain('一律拒絕')
    expect(text).toContain('工具權限與 agent 提問仍會等你')
    expect(text).not.toContain('自我接力')
    expect(text).not.toContain('自動通過')
  })

  it('zh-TW off: spells out what turning it on does, per the contract wording', () => {
    expect(zhTW['unattended.tooltip.off']).toBe('無人值守模式已關閉。開啟後，所有顯示中主機的接力申請會自動核准（有設額度時照額度，額度用完的 session 會暫停）；『成為 lead』與納入申請一律拒絕，目前開著的也會被拒絕。工具權限與 agent 提問仍會等你')
    expect(zhTW['unattended.tooltip.on']).toBe('無人值守中：接力申請自動核准（照額度），『成為 lead』與納入申請一律拒絕；工具權限與 agent 提問仍會等你。按下關閉')
    expect(zhTW['unattended.tooltip.legacy_daemon']).toBe('（舊版 daemon 的主機仍會自動通過『成為 lead』與納入申請）')
  })

  it.each(['unattended.tooltip.off', 'unattended.tooltip.on'] as const)('%s (en)', (key) => {
    const text = en[key]
    expect(text).toContain('relay requests')
    expect(text).toContain('approved automatically')
    expect(text).toContain('quota')
    expect(text).toContain('"become lead"')
    expect(text).toContain('adopt')
    expect(text).toContain('always refused')
    expect(text).toContain('tool permissions and agent questions still wait for you')
    expect(text).not.toContain('"self relay"')
  })

  it('the legacy-daemon sentence exists in both languages', () => {
    expect(en['unattended.tooltip.legacy_daemon']).toContain('"become lead"')
    expect(en['unattended.tooltip.legacy_daemon']).toContain('adopt')
  })
})
