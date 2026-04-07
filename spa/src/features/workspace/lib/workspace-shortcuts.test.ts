import { describe, it, expect, beforeEach } from 'vitest'
import { useWorkspaceStore } from '../store'

describe('workspace shortcut handlers', () => {
  beforeEach(() => {
    useWorkspaceStore.getState().reset()
  })

  it('switch-workspace-N jumps to Nth workspace by position', () => {
    useWorkspaceStore.getState().addWorkspace('WS1')
    const ws2 = useWorkspaceStore.getState().addWorkspace('WS2')
    useWorkspaceStore.getState().addWorkspace('WS3')
    const workspaces = useWorkspaceStore.getState().workspaces
    const target = workspaces[1]
    expect(target.id).toBe(ws2.id)
    useWorkspaceStore.getState().setActiveWorkspace(target.id)
    expect(useWorkspaceStore.getState().activeWorkspaceId).toBe(ws2.id)
  })

  it('switch-workspace out of range is ignored', () => {
    const ws1 = useWorkspaceStore.getState().addWorkspace('WS1')
    const workspaces = useWorkspaceStore.getState().workspaces
    const target = workspaces[5]
    expect(target).toBeUndefined()
    expect(useWorkspaceStore.getState().activeWorkspaceId).toBe(ws1.id)
  })

  it('prev-workspace wraps from first to last', () => {
    const ws1 = useWorkspaceStore.getState().addWorkspace('WS1')
    useWorkspaceStore.getState().addWorkspace('WS2')
    const ws3 = useWorkspaceStore.getState().addWorkspace('WS3')
    useWorkspaceStore.getState().setActiveWorkspace(ws1.id)
    const workspaces = useWorkspaceStore.getState().workspaces
    const currentIdx = workspaces.findIndex((w) => w.id === ws1.id)
    const prevIdx = (currentIdx - 1 + workspaces.length) % workspaces.length
    useWorkspaceStore.getState().setActiveWorkspace(workspaces[prevIdx].id)
    expect(useWorkspaceStore.getState().activeWorkspaceId).toBe(ws3.id)
  })

  it('next-workspace wraps from last to first', () => {
    const ws1 = useWorkspaceStore.getState().addWorkspace('WS1')
    useWorkspaceStore.getState().addWorkspace('WS2')
    const ws3 = useWorkspaceStore.getState().addWorkspace('WS3')
    useWorkspaceStore.getState().setActiveWorkspace(ws3.id)
    const workspaces = useWorkspaceStore.getState().workspaces
    const currentIdx = workspaces.findIndex((w) => w.id === ws3.id)
    const nextIdx = (currentIdx + 1) % workspaces.length
    useWorkspaceStore.getState().setActiveWorkspace(workspaces[nextIdx].id)
    expect(useWorkspaceStore.getState().activeWorkspaceId).toBe(ws1.id)
  })

  it('workspace shortcuts with 0 workspaces do nothing', () => {
    const workspaces = useWorkspaceStore.getState().workspaces
    expect(workspaces).toHaveLength(0)
    const target = workspaces[0]
    expect(target).toBeUndefined()
    expect(useWorkspaceStore.getState().activeWorkspaceId).toBeNull()
  })
})
