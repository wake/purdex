// spa/src/lib/notification-gate.member.test.ts — member spec R1: a team member's waiting / idle never notify; its error
// is judged like any session's (same settings, same tab condition, same debounce).
import { beforeEach, describe, expect, it } from 'vitest'
import { __resetDebounceStateForTests, shouldNotify } from './notification-gate'
import { useNotificationSettingsStore } from '../stores/useNotificationSettingsStore'

const base = () => ({
  derived: 'waiting' as string | null,
  eventName: 'PdxPermissionRequest',
  compositeKey: 'h1:aaaaaa',
  visibleInActiveTab: false,
  hasTab: true,
  settings: useNotificationSettingsStore.getState().getSettingsForAgent('cc'),
})

beforeEach(() => {
  __resetDebounceStateForTests()
  useNotificationSettingsStore.setState({ agents: {} })
})

describe('shouldNotify — team member (R1)', () => {
  it('a non-member waiting notifies (control)', () => {
    expect(shouldNotify({ ...base(), isMember: false })).toBe(true)
  })

  it('omitting isMember is a non-member', () => {
    expect(shouldNotify(base())).toBe(true)
  })

  it('a member waiting does not notify, tab or not', () => {
    expect(shouldNotify({ ...base(), isMember: true })).toBe(false)
    useNotificationSettingsStore.getState().setNotifyWithoutTab('cc', true)
    const s = useNotificationSettingsStore.getState().getSettingsForAgent('cc')
    expect(shouldNotify({ ...base(), settings: s, hasTab: false, isMember: true })).toBe(false)
  })

  it('a member idle does not notify; a non-member idle does', () => {
    const idle = { ...base(), derived: 'idle', eventName: 'PdxStop' }
    expect(shouldNotify({ ...idle, isMember: false })).toBe(true)
    expect(shouldNotify({ ...idle, isMember: true })).toBe(false)
  })

  it('a member error notifies like a non-member, and keeps the 60 s debounce', () => {
    const err = { ...base(), derived: 'error', eventName: 'PdxStopFailure', errorString: 'boom' }
    expect(shouldNotify({ ...err, isMember: true })).toBe(true)
    expect(shouldNotify({ ...err, isMember: true })).toBe(false)
  })

  it('a member error follows the tab condition: no tab and no 「沒開分頁也通知」 → no notification', () => {
    const err = { ...base(), derived: 'error', eventName: 'PdxStopFailure', hasTab: false }
    expect(shouldNotify({ ...err, isMember: true })).toBe(false)
  })

  it('a member error follows the settings: the event switched off → no notification', () => {
    useNotificationSettingsStore.getState().setEventEnabled('cc', 'StopFailure', false)
    const settings = useNotificationSettingsStore.getState().getSettingsForAgent('cc')
    const err = { ...base(), settings, derived: 'error', eventName: 'PdxStopFailure' }
    expect(shouldNotify({ ...err, isMember: true })).toBe(false)
  })
})
