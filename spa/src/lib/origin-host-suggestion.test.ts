import { describe, it, expect } from 'vitest'
import { shouldSuggestOriginHost, originHostDraft } from './origin-host-suggestion'
import type { HostConfig } from '../stores/useHostStore'

function host(over: Partial<HostConfig>): HostConfig {
  return { id: 'h', name: 'h', ip: '10.0.0.1', port: 7860, order: 0, ...over }
}

describe('shouldSuggestOriginHost', () => {
  it('web + https + 無任何 https host → 建議', () => {
    expect(shouldSuggestOriginHost({ isElectron: false, protocol: 'https:', hosts: [host({ scheme: 'http' })] })).toBe(true)
  })
  it('已有任一 https host → 不建議', () => {
    expect(shouldSuggestOriginHost({ isElectron: false, protocol: 'https:', hosts: [host({ scheme: 'https' })] })).toBe(false)
  })
  it('Electron → 不建議', () => {
    expect(shouldSuggestOriginHost({ isElectron: true, protocol: 'https:', hosts: [] })).toBe(false)
  })
  it('非 https 頁面 → 不建議', () => {
    expect(shouldSuggestOriginHost({ isElectron: false, protocol: 'http:', hosts: [] })).toBe(false)
  })
})

describe('originHostDraft', () => {
  it('無 port → 443、scheme https、token off', () => {
    expect(originHostDraft({ hostname: 'purdex.mlab.host', port: '' }))
      .toEqual({ scheme: 'https', ip: 'purdex.mlab.host', port: '443', useToken: false })
  })
  it('顯式 port 保留', () => {
    expect(originHostDraft({ hostname: 'h', port: '8443' }).port).toBe('8443')
  })
})
