import { describe, it, expect, beforeEach } from 'vitest'
import { useQuickCommandStore } from './useQuickCommandStore'

describe('useQuickCommandStore', () => {
  beforeEach(() => {
    useQuickCommandStore.setState({
      global: [
        { id: 'start-cc', name: 'Start Claude Code', command: 'claude -p --verbose --output-format stream-json', category: 'agent' },
        { id: 'start-codex', name: 'Start Codex', command: 'codex', category: 'agent' },
      ],
      byHost: {},
    })
  })

  it('getCommands returns global commands when no host overrides', () => {
    const cmds = useQuickCommandStore.getState().getCommands('host-1')
    expect(cmds).toHaveLength(2)
    expect(cmds[0].id).toBe('start-cc')
  })

  it('per-host overrides global by id', () => {
    useQuickCommandStore.getState().addCommand(
      { id: 'start-cc', name: 'CC Custom', command: 'claude --custom', category: 'agent' },
      'host-1',
    )
    const cmds = useQuickCommandStore.getState().getCommands('host-1')
    const cc = cmds.find((c) => c.id === 'start-cc')!
    expect(cc.command).toBe('claude --custom')
    expect(cc.name).toBe('CC Custom')
  })

  it('addCommand to global', () => {
    useQuickCommandStore.getState().addCommand({ id: 'custom', name: 'Custom', command: 'ls -la' })
    expect(useQuickCommandStore.getState().global).toHaveLength(3)
  })

  it('removeCommand from global', () => {
    useQuickCommandStore.getState().removeCommand('start-codex')
    expect(useQuickCommandStore.getState().global).toHaveLength(1)
  })

  it('updateCommand in global', () => {
    useQuickCommandStore.getState().updateCommand('start-cc', { name: 'CC Updated' })
    const cmd = useQuickCommandStore.getState().global.find((c) => c.id === 'start-cc')!
    expect(cmd.name).toBe('CC Updated')
    expect(cmd.command).toBe('claude -p --verbose --output-format stream-json')
  })
})
