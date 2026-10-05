import { beforeEach, describe, expect, it } from 'vitest'
import { useHandoffDialogStore, type HandoffDialogTarget } from './useHandoffDialogStore'
import type { TmuxSessionContent } from '../types/tab'

const content: TmuxSessionContent = {
  kind: 'tmux-session', hostId: 'h1', sessionCode: 'zk16vd', mode: 'terminal', cachedName: 'purdex', tmuxInstance: 'inst-1',
}
const target = (over: Partial<HandoffDialogTarget> = {}): HandoffDialogTarget => ({ tabId: 't1', paneId: 'p1', content, ...over })

beforeEach(() => useHandoffDialogStore.setState({ target: null }))

describe('useHandoffDialogStore', () => {
  it('starts closed; open sets the target, close clears it', () => {
    expect(useHandoffDialogStore.getState().target).toBeNull()
    useHandoffDialogStore.getState().open(target({ mode: 'chat' }))
    expect(useHandoffDialogStore.getState().target).toEqual({ tabId: 't1', paneId: 'p1', content, mode: 'chat' })
    useHandoffDialogStore.getState().close()
    expect(useHandoffDialogStore.getState().target).toBeNull()
  })

  it('one dialog at a time: a second open replaces the target', () => {
    useHandoffDialogStore.getState().open(target())
    useHandoffDialogStore.getState().open(target({ paneId: 'p2' }))
    expect(useHandoffDialogStore.getState().target?.paneId).toBe('p2')
  })

  it('each open is a distinct target, even for the same arguments', () => {
    const t = target()
    useHandoffDialogStore.getState().open(t)
    const first = useHandoffDialogStore.getState().target
    useHandoffDialogStore.getState().open(t)
    expect(useHandoffDialogStore.getState().target).not.toBe(first)
  })

  it('closeFor closes only that opening: a finished request never closes a dialog opened after it', () => {
    useHandoffDialogStore.getState().open(target())
    const first = useHandoffDialogStore.getState().target!
    useHandoffDialogStore.getState().open(target({ paneId: 'p2' }))
    useHandoffDialogStore.getState().closeFor(first)
    expect(useHandoffDialogStore.getState().target?.paneId).toBe('p2')
    useHandoffDialogStore.getState().closeFor(useHandoffDialogStore.getState().target!)
    expect(useHandoffDialogStore.getState().target).toBeNull()
  })
})
